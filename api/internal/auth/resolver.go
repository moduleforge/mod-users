package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/observer"
	coredb "github.com/moduleforge/core-model/db"
	db "github.com/moduleforge/mod-users/model/db"
)

// ErrUserGone is returned by UserResolver.Resolve when a locally-issued JWT
// references a user account that no longer exists in the user_accounts table
// (e.g., the account was deleted after the token was minted). Handlers should
// translate this into 401 Unauthorized — the signature is valid but the identity
// is no longer valid.
var ErrUserGone = errors.New("auth: user no longer exists")

// ErrUnverifiedTakeover is returned when a principal's email matches an existing
// user account that is not yet email-verified, but the IdP did not assert
// email_verified=true. The handler should redirect the user to a "verify your
// existing account first" page rather than linking the new OIDC identity.
var ErrUnverifiedTakeover = errors.New("auth: unverified account claim requires verification first")

// uuidLookupFn is the slot used by the local-issuer fast path. Extracted as
// a field so tests can substitute a stub without needing a running Postgres.
// Nil is valid — the fast path short-circuits and falls back to the OIDC
// path in that case (useful for pre-Phase 9 fallback semantics in tests).
type uuidLookupFn func(ctx context.Context, u uuid.UUID) (db.UserAccount, error)

// oidcIdentityLookupFn is the injectable stub for GetOIDCIdentityByIssuerSubject.
// Non-nil value replaces the real DB call in Resolve; used by tests only.
type oidcIdentityLookupFn func(ctx context.Context, issuer, subject string) (db.AuthOidcIdentity, error)

// emailAccountLookupFn is the injectable stub for GetUserAccountByEmail.
// Non-nil value replaces the real DB call in Resolve; used by tests only.
type emailAccountLookupFn func(ctx context.Context, email string) (db.UserAccount, error)

// idAccountLookupFn is the injectable stub for GetUserAccountByID.
// Non-nil value replaces the real DB call in Resolve; used by tests only.
type idAccountLookupFn func(ctx context.Context, id int64) (db.UserAccount, error)

// linkIdentityFn is the injectable stub for linkIdentity (branch 2).
// Non-nil value replaces the real DB+tx call; used by tests only.
type linkIdentityFn func(ctx context.Context, ua db.UserAccount, p Principal, entityID int64) error

// verifyAndLinkFn is the injectable stub for verifyAndLink (branch 3).
// Non-nil value replaces the real DB+tx call; used by tests only.
type verifyAndLinkFn func(ctx context.Context, ua db.UserAccount, p Principal, entityID int64) error

// autoCreateFn is the injectable stub for autoCreate (branch 5).
// Non-nil value replaces the real DB+tx call; used by tests only.
type autoCreateFn func(ctx context.Context, p Principal) (db.UserAccount, error)

// FirstUserHookFn is called after the first user account is committed to the
// database. entityID is the entity_id of the newly created natural person.
// Errors are logged and ignored so that a bootstrap failure does not prevent
// the OIDC login — the operator can create the grant manually.
type FirstUserHookFn func(ctx context.Context, entityID int64) error

// UserResolver resolves a Principal to a *UserContext. For OIDC principals it
// auto-creates the user account on first sight; for locally-issued JWTs
// (matching LocalIssuer) it takes a fast path that simply loads by UUID.
type UserResolver struct {
	pool          *pgxpool.Pool
	queries       *db.Queries
	coreQ         *coredb.Queries
	localIssuer   string
	uuidLookup    uuidLookupFn
	firstUserHook FirstUserHookFn         // nil if no bootstrap hook configured
	obs           *observer.ObserverGroup // nil-safe; may be nil in tests

	// Test-injectable stubs — nil in production; non-nil stubs replace the
	// corresponding real DB call so the 5-branch logic can be exercised
	// without a running Postgres.
	oidcIdentityLookup oidcIdentityLookupFn
	emailAccountLookup emailAccountLookupFn
	idAccountLookup    idAccountLookupFn
	linkIdentityFn     linkIdentityFn
	verifyAndLinkFn    verifyAndLinkFn
	autoCreateFn       autoCreateFn
}

// NewUserResolver creates a resolver. localIssuer is the value written into
// the "iss" claim by IssueLocalJWT — when Resolve sees a Principal with this
// issuer, it skips the OIDC auto-create path and looks up the user account by UUID.
// The adminRole parameter is accepted for backwards compatibility but is no
// longer used; admin privileges are determined solely by the grants table.
// obs may be nil (observer events will simply be skipped).
func NewUserResolver(pool *pgxpool.Pool, queries *db.Queries, coreQ *coredb.Queries, adminRole, localIssuer string, obs *observer.ObserverGroup) *UserResolver {
	_ = adminRole // retained in signature for call-site compatibility; no longer used
	r := &UserResolver{
		pool:        pool,
		queries:     queries,
		coreQ:       coreQ,
		localIssuer: localIssuer,
		obs:         obs,
	}
	if queries != nil {
		r.uuidLookup = queries.GetUserAccountByUUID
	}
	return r
}

// SetFirstUserHook registers a hook to be called after the first user account
// is created and committed (OIDC auto-create path only). A nil fn clears the hook.
func (r *UserResolver) SetFirstUserHook(fn FirstUserHookFn) {
	r.firstUserHook = fn
}

// SetObserverGroup wires an ObserverGroup into the resolver for emitting
// identity-link and email-verify events. May be called after construction
// (e.g., when the observer group is built later in the initialization sequence).
// A nil group is valid and causes observation calls to be silently skipped.
func (r *UserResolver) SetObserverGroup(obs *observer.ObserverGroup) {
	r.obs = obs
}

// safeObserve calls obs.Observe only when r.obs is non-nil. Returns nil when obs is nil.
func (r *UserResolver) safeObserve(ctx context.Context, tx pgx.Tx, op, resource string, targetEntityID *int64, before, after any) error {
	if r.obs == nil {
		return nil
	}
	return r.obs.Observe(ctx, tx, op, resource, targetEntityID, before, after)
}

// safeObserveAfterCommit calls obs.ObserveAfterCommit only when r.obs is non-nil.
func (r *UserResolver) safeObserveAfterCommit(ctx context.Context, op, resource string, targetEntityID *int64, after any) {
	if r.obs == nil {
		return
	}
	r.obs.ObserveAfterCommit(ctx, op, resource, targetEntityID, after)
}

// Resolve looks up or creates the user account associated with the given Principal.
// It applies the 5-branch multi-identity decision table:
//
//  1. (issuer, subject) already in auth_oidc_identities → touch last_seen_at, return account.
//  2. user_account with matching email exists and is verified → insert identity row, return account.
//  3. user_account with matching email exists, unverified, principal.EmailVerified==true →
//     set email_verified_at + insert identity row in one tx; emit events.
//  4. user_account with matching email exists, unverified, principal.EmailVerified==false →
//     return ErrUnverifiedTakeover.
//  5. no user_account with matching email → auto-create full chain + insert identity row.
func (r *UserResolver) Resolve(ctx context.Context, p Principal) (*UserContext, error) {
	// Local-issuer fast path. A principal minted by IssueLocalJWT carries
	// the user account's own UUID in `sub`; the HS256 signature has already
	// proven authenticity, so we only need to hydrate the DB row. No
	// auto-create, no email-link attempt. A missing UUID means the account
	// was deleted between token issue and use — 401, not 500.
	if r.localIssuer != "" && p.Issuer == r.localIssuer && r.uuidLookup != nil {
		parsed, err := uuid.Parse(p.Subject)
		if err != nil {
			return nil, fmt.Errorf("auth: local jwt sub is not a uuid: %w", err)
		}
		ua, err := r.uuidLookup(ctx, parsed)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrUserGone
			}
			return nil, fmt.Errorf("auth: lookup local user account by uuid: %w", err)
		}
		return r.buildUserContext(ctx, ua, p), nil
	}

	// Branch 1: identity row already exists for (issuer, subject).
	if p.Issuer != "" && p.Subject != "" {
		var identity db.AuthOidcIdentity
		var identErr error
		if r.oidcIdentityLookup != nil {
			identity, identErr = r.oidcIdentityLookup(ctx, p.Issuer, p.Subject)
		} else {
			identity, identErr = r.queries.GetOIDCIdentityByIssuerSubject(ctx, db.GetOIDCIdentityByIssuerSubjectParams{
				Issuer:  p.Issuer,
				Subject: p.Subject,
			})
		}
		if identErr == nil {
			// Touch last_seen_at — fire-and-forget, non-fatal.
			if r.queries != nil {
				if touchErr := r.queries.TouchOIDCIdentityLastSeen(ctx, identity.ID); touchErr != nil {
					slog.WarnContext(ctx, "resolver: touch last_seen_at failed", "identity_id", identity.ID, "error", touchErr)
				}
			}
			var ua db.UserAccount
			var loadErr error
			if r.idAccountLookup != nil {
				ua, loadErr = r.idAccountLookup(ctx, identity.UserAccountID)
			} else {
				ua, loadErr = r.queries.GetUserAccountByID(ctx, identity.UserAccountID)
			}
			if loadErr != nil {
				if errors.Is(loadErr, pgx.ErrNoRows) {
					return nil, ErrUserGone
				}
				return nil, fmt.Errorf("auth: load user account for existing identity: %w", loadErr)
			}
			return r.buildUserContext(ctx, ua, p), nil
		}
		if !errors.Is(identErr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("auth: lookup oidc identity: %w", identErr)
		}
	}

	// Branches 2–5: no existing identity row. Look up by email.
	if p.Email != "" {
		var ua db.UserAccount
		var emailErr error
		if r.emailAccountLookup != nil {
			ua, emailErr = r.emailAccountLookup(ctx, p.Email)
		} else {
			ua, emailErr = r.queries.GetUserAccountByEmail(ctx, p.Email)
		}
		if emailErr == nil {
			// Found an existing account.
			entityID := ua.AccountHolder

			if ua.EmailVerifiedAt != nil {
				// Branch 2: account exists and is already email-verified → link new identity.
				var linkErr error
				if r.linkIdentityFn != nil {
					linkErr = r.linkIdentityFn(ctx, ua, p, entityID)
				} else {
					linkErr = r.linkIdentity(ctx, ua, p, entityID)
				}
				if linkErr != nil {
					return nil, fmt.Errorf("auth: branch 2 link identity: %w", linkErr)
				}
				return r.buildUserContext(ctx, ua, p), nil
			}

			// Account is not yet verified.
			if p.EmailVerified {
				// Branch 3: IdP asserts verified → flip email_verified_at + link in one tx.
				var vlErr error
				if r.verifyAndLinkFn != nil {
					vlErr = r.verifyAndLinkFn(ctx, ua, p, entityID)
				} else {
					vlErr = r.verifyAndLink(ctx, ua, p, entityID)
				}
				if vlErr != nil {
					return nil, fmt.Errorf("auth: branch 3 verify and link: %w", vlErr)
				}
				// Reload to get updated email_verified_at.
				if r.idAccountLookup != nil {
					ua, vlErr = r.idAccountLookup(ctx, ua.ID)
				} else {
					ua, vlErr = r.queries.GetUserAccountByID(ctx, ua.ID)
				}
				if vlErr != nil {
					return nil, fmt.Errorf("auth: branch 3 reload user account: %w", vlErr)
				}
				return r.buildUserContext(ctx, ua, p), nil
			}

			// Branch 4: unverified account, IdP also unverified → block.
			return nil, ErrUnverifiedTakeover
		}
		if !errors.Is(emailErr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("auth: lookup by email: %w", emailErr)
		}
	}

	// Branch 5: no existing account at all → auto-create.
	var ua db.UserAccount
	var createErr error
	if r.autoCreateFn != nil {
		ua, createErr = r.autoCreateFn(ctx, p)
	} else {
		ua, createErr = r.autoCreate(ctx, p)
	}
	if createErr != nil {
		return nil, fmt.Errorf("auth: auto-create user account: %w", createErr)
	}
	return r.buildUserContext(ctx, ua, p), nil
}

// linkIdentity inserts a new auth_oidc_identities row for an existing account.
// Used by Branch 2 (account already verified). Emits oidc_identity.linked.
func (r *UserResolver) linkIdentity(ctx context.Context, ua db.UserAccount, p Principal, entityID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := r.queries.WithTx(tx)

	var emailVerifiedAt *time.Time
	if p.EmailVerified {
		now := time.Now()
		emailVerifiedAt = &now
	}

	identity, err := qtx.InsertOIDCIdentity(ctx, db.InsertOIDCIdentityParams{
		UserAccountID:      ua.ID,
		Issuer:             p.Issuer,
		Subject:            p.Subject,
		Email:              pgtype.Text{String: p.Email, Valid: p.Email != ""},
		EmailVerifiedAtIdp: emailVerifiedAt,
	})
	if err != nil {
		return fmt.Errorf("insert oidc identity: %w", err)
	}

	after := map[string]any{
		"uuid":   identity.Uuid.String(),
		"issuer": p.Issuer,
	}
	if err := r.safeObserve(ctx, tx, "create", "auth_oidc_identity", &entityID, nil, after); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	r.safeObserveAfterCommit(ctx, "create", "auth_oidc_identity", &entityID, after)
	return nil
}

// verifyAndLink sets email_verified_at on the user account and inserts an
// identity row in a single transaction. Used by Branch 3.
// Emits both email.verified and oidc_identity.linked observation events.
func (r *UserResolver) verifyAndLink(ctx context.Context, ua db.UserAccount, p Principal, entityID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := r.queries.WithTx(tx)

	now := time.Now()

	// Set email_verified_at.
	if err := qtx.UpdateUserAccount(ctx, db.UpdateUserAccountParams{
		ID:              ua.ID,
		Email:           ua.Email,
		EmailVerifiedAt: &now,
	}); err != nil {
		return fmt.Errorf("update email_verified_at: %w", err)
	}

	// Insert identity row.
	identity, err := qtx.InsertOIDCIdentity(ctx, db.InsertOIDCIdentityParams{
		UserAccountID:      ua.ID,
		Issuer:             p.Issuer,
		Subject:            p.Subject,
		Email:              pgtype.Text{String: p.Email, Valid: p.Email != ""},
		EmailVerifiedAtIdp: &now,
	})
	if err != nil {
		return fmt.Errorf("insert oidc identity: %w", err)
	}

	verifyBefore := map[string]any{
		"uuid":              ua.Uuid.String(),
		"email_verified_at": nil,
	}
	verifyAfter := map[string]any{
		"uuid":              ua.Uuid.String(),
		"email_verified_at": now,
	}
	if err := r.safeObserve(ctx, tx, "update", "user_account", &entityID, verifyBefore, verifyAfter); err != nil {
		return err
	}

	linkAfter := map[string]any{
		"uuid":   identity.Uuid.String(),
		"issuer": p.Issuer,
	}
	if err := r.safeObserve(ctx, tx, "create", "auth_oidc_identity", &entityID, nil, linkAfter); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	r.safeObserveAfterCommit(ctx, "update", "user_account", &entityID, verifyAfter)
	r.safeObserveAfterCommit(ctx, "create", "auth_oidc_identity", &entityID, linkAfter)
	return nil
}

// autoCreate creates a new entity → legal_entity → natural_person → user_account chain
// and inserts an auth_oidc_identities row in the same transaction.
func (r *UserResolver) autoCreate(ctx context.Context, p Principal) (db.UserAccount, error) {
	var ua db.UserAccount

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ua, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := r.queries.WithTx(tx)
	coreQtx := r.coreQ.WithTx(tx)

	// Check if this is the first user account (root bootstrap).
	var userAccountCount int64
	err = tx.QueryRow(ctx, "SELECT count(*) FROM user_accounts").Scan(&userAccountCount)
	if err != nil {
		return ua, fmt.Errorf("count user_accounts: %w", err)
	}
	isFirstUser := userAccountCount == 0

	// Resolve the natural_person type ID from the types registry.
	npType, err := coreQtx.GetTypeBySlug(ctx, "natural_person")
	if err != nil {
		return ua, fmt.Errorf("resolve natural_person type: %w", err)
	}

	// Create entity.
	entity, err := coreQtx.CreateEntity(ctx, npType.ID)
	if err != nil {
		return ua, fmt.Errorf("create entity: %w", err)
	}

	// Create legal entity (pure FK anchor — no kind/display_name).
	_, err = coreQtx.CreateLegalEntity(ctx, entity.ID)
	if err != nil {
		return ua, fmt.Errorf("create legal entity: %w", err)
	}

	// Derive given_name from email local-part for auto-created accounts.
	givenName := p.Email
	if idx := strings.Index(p.Email, "@"); idx > 0 {
		givenName = p.Email[:idx]
	}

	// Create natural person.
	_, err = coreQtx.CreateNaturalPerson(ctx, coredb.CreateNaturalPersonParams{
		EntityID:   entity.ID,
		GivenName:  pgtype.Text{String: givenName, Valid: true},
		FamilyName: pgtype.Text{},
	})
	if err != nil {
		return ua, fmt.Errorf("create natural person: %w", err)
	}

	// Set email_verified_at only when the IdP asserted verified.
	var emailVerifiedAt *time.Time
	if p.EmailVerified {
		now := time.Now()
		emailVerifiedAt = &now
	}

	// Create user account. account_holder references legal_entities(entity_id),
	// so entity.ID is valid here because we just created the legal_entity row.
	ua, err = qtx.CreateUserAccount(ctx, db.CreateUserAccountParams{
		AccountHolder:   entity.ID,
		Email:           pgtype.Text{String: p.Email, Valid: p.Email != ""},
		EmailVerifiedAt: emailVerifiedAt,
	})
	if err != nil {
		return ua, fmt.Errorf("create user account: %w", err)
	}

	// Insert identity row in the same transaction.
	var insertedIdentity db.AuthOidcIdentity
	hasIdentity := p.Issuer != "" && p.Subject != ""
	if hasIdentity {
		var idpVerifiedAt *time.Time
		if p.EmailVerified {
			t := time.Now()
			idpVerifiedAt = &t
		}
		var err error
		insertedIdentity, err = qtx.InsertOIDCIdentity(ctx, db.InsertOIDCIdentityParams{
			UserAccountID:      ua.ID,
			Issuer:             p.Issuer,
			Subject:            p.Subject,
			Email:              pgtype.Text{String: p.Email, Valid: p.Email != ""},
			EmailVerifiedAtIdp: idpVerifiedAt,
		})
		if err != nil {
			return ua, fmt.Errorf("insert oidc identity: %w", err)
		}
	}

	entityID := ua.AccountHolder

	var accountEmailVal any
	if ua.Email.Valid {
		accountEmailVal = ua.Email.String
	}
	accountAfter := map[string]any{
		"uuid":              ua.Uuid.String(),
		"email":             accountEmailVal,
		"email_verified_at": ua.EmailVerifiedAt,
	}
	if err := r.safeObserve(ctx, tx, "create", "user_account", &entityID, nil, accountAfter); err != nil {
		return ua, err
	}

	if hasIdentity {
		identityAfter := map[string]any{
			"uuid":    insertedIdentity.Uuid.String(),
			"issuer":  p.Issuer,
			"subject": p.Subject,
		}
		if err := r.safeObserve(ctx, tx, "create", "auth_oidc_identity", &entityID, nil, identityAfter); err != nil {
			return ua, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ua, fmt.Errorf("commit: %w", err)
	}

	r.safeObserveAfterCommit(ctx, "create", "user_account", &entityID, accountAfter)
	if hasIdentity {
		r.safeObserveAfterCommit(ctx, "create", "auth_oidc_identity", &entityID, map[string]any{
			"uuid":    insertedIdentity.Uuid.String(),
			"issuer":  p.Issuer,
			"subject": p.Subject,
		})
	}

	if isFirstUser {
		slog.InfoContext(ctx, "first user account created with admin privileges",
			"email", p.Email,
			"user_account_uuid", ua.Uuid.String(),
		)
		// Bootstrap wildcard grant for the first user. Runs after commit so the
		// entity row is visible to the hook's own transaction. A failure is logged
		// and ignored — the account is created; an operator can grant manually.
		if r.firstUserHook != nil {
			if err := r.firstUserHook(ctx, entity.ID); err != nil {
				slog.ErrorContext(ctx, "resolver: first-user hook failed; wildcard grant not created",
					"entity_id", entity.ID,
					"email", p.Email,
					"error", err,
				)
			} else {
				slog.InfoContext(ctx, "resolver: wildcard manage grant created for first user",
					"entity_id", entity.ID,
				)
			}
		}
	}

	return ua, nil
}

func (r *UserResolver) buildUserContext(ctx context.Context, ua db.UserAccount, p Principal) *UserContext {
	uc := &UserContext{
		UserAccountID:   ua.ID,
		UserUUID:        ua.Uuid.String(),
		EntityID:        ua.AccountHolder,
		Email:           ua.Email.String,
		EmailVerifiedAt: ua.EmailVerifiedAt,
	}

	if ua.DefaultAppID.Valid {
		appID := ua.DefaultAppID.Int64
		uc.AppID = &appID
	}

	// Populate AssumedUser when this is an assume-identity JWT. The sudo user
	// is looked up by UUID; if the lookup fails we silently skip it (the assume
	// session has degraded to a normal session, which is safe).
	if p.SudoUserUUID != "" && r.queries != nil {
		if sudoUUID, err := uuid.Parse(p.SudoUserUUID); err == nil {
			if sudoUA, err := r.queries.GetUserAccountByUUID(ctx, sudoUUID); err == nil {
				uc.AssumedUser = &AssumedUserInfo{
					UserAccountID: sudoUA.ID,
					UserUUID:      sudoUA.Uuid.String(),
					EntityID:      sudoUA.AccountHolder,
					Email:         sudoUA.Email.String,
				}
			}
		}
	}

	return uc
}
