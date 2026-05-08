// Package service implements service-layer logic for user account operations.
//
// UserAccountService follows the standard service-method shape used across all
// peer modules: Authorize → txhelper.Run(in-tx ops + Observe) → ObserveAfterCommit.
// The Create method is atomic: NaturalPerson creation and UserAccount creation
// run in a single transaction via NaturalPersonService.CreateInTx.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	coreservice "github.com/moduleforge/core-api/service"
	"github.com/moduleforge/core-api/txhelper"
	"github.com/moduleforge/core-api/types"
	coredb "github.com/moduleforge/core-model/db"
	db "github.com/moduleforge/users-module/model/db"
)

// CreateUserAccountInput carries the fields required to create a user account
// plus its underlying natural person entity.
type CreateUserAccountInput struct {
	Email      string
	Password   *string // optional; hashed before storage
	GivenName  string
	FamilyName string
	IsAdmin    bool
}

// UpdateUserAccountInput carries the fields that may be updated on a user account.
// Nil pointer fields are left unchanged.
type UpdateUserAccountInput struct {
	Email      *string
	GivenName  *string
	FamilyName *string
	IsAdmin    *bool
}

// ListUserAccountsInput carries pagination + optional filter parameters.
type ListUserAccountsInput struct {
	Search string
	Limit  int32
	Offset int32
}

// UserAccount is the service-layer representation of a user account.
type UserAccount struct {
	ID            int64
	UUID          uuid.UUID
	AccountHolder int64 // entity internal ID
	Email         string
	IsAdmin       bool
	EmailVerified bool
	GivenName     string
	FamilyName    string
	EntityKind    string
	CreatedAt     pgtype.Timestamptz
}

// ErrEmailTaken is returned by Create when the email is already registered.
var ErrEmailTaken = fmt.Errorf("email already registered")

// ErrInvalidInput is returned when the caller supplies invalid field values.
var ErrInvalidInput = fmt.Errorf("invalid input")

// UserAccountService implements user account CRUD with the standard
// service-method shape. The Create method composes NaturalPerson creation and
// UserAccount creation into a single atomic transaction.
type UserAccountService struct {
	db        txhelper.DB
	q         db.Querier
	coreQ     coredb.Querier
	az        coreAuthz.Authorizer
	obs       *observer.ObserverGroup
	npService coreservice.NaturalPersonServicer
	typeRes   *types.Resolver
	hashPw    func(plain string) (string, error)
}

// NewUserAccountService constructs a UserAccountService. hashPassword is
// typically localauth.HashPassword; it is injectable for tests.
func NewUserAccountService(
	pool txhelper.DB,
	q db.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	npService coreservice.NaturalPersonServicer,
	typeRes *types.Resolver,
	hashPassword func(plain string) (string, error),
) *UserAccountService {
	return &UserAccountService{
		db:        pool,
		q:         q,
		coreQ:     coreQ,
		az:        az,
		obs:       obs,
		npService: npService,
		typeRes:   typeRes,
		hashPw:    hashPassword,
	}
}

// Create creates a NaturalPerson entity and a UserAccount row in a single
// atomic transaction. Requires admin authorization.
func (s *UserAccountService) Create(ctx context.Context, in CreateUserAccountInput) (UserAccount, error) {
	// Validate input before touching the authorizer.
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" {
		return UserAccount{}, fmt.Errorf("%w: email is required", ErrInvalidInput)
	}
	if strings.TrimSpace(in.GivenName) == "" {
		return UserAccount{}, fmt.Errorf("%w: given_name is required", ErrInvalidInput)
	}
	if strings.TrimSpace(in.FamilyName) == "" {
		return UserAccount{}, fmt.Errorf("%w: family_name is required", ErrInvalidInput)
	}
	if in.Password != nil && len(*in.Password) < 12 {
		return UserAccount{}, fmt.Errorf("%w: password must be at least 12 characters", ErrInvalidInput)
	}

	// Authorize: create is admin-only; use type ID per convention.
	typeID := s.typeRes.IDForSlugMust("natural_person")
	if err := s.az.Authorize(ctx, "create", &typeID); err != nil {
		return UserAccount{}, err
	}

	var out UserAccount
	err := txhelper.Run(ctx, s.db, func(ctx context.Context, tx pgx.Tx) error {
		// 1. Create entity + legal_entity + natural_person in the same tx.
		np, _, entityID, err := s.npService.CreateInTx(ctx, tx, coreservice.CreateNaturalPersonInput{
			GivenName:  in.GivenName,
			FamilyName: in.FamilyName,
		})
		if err != nil {
			return fmt.Errorf("user_accounts.Create natural_person: %w", err)
		}

		// 2. Create the user_account row in the same tx.
		ua, err := db.New(tx).CreateUserAccount(ctx, db.CreateUserAccountParams{
			AccountHolder: np.EntityID,
			Email:         in.Email,
			IsAdmin:       in.IsAdmin,
		})
		if err != nil {
			return fmt.Errorf("user_accounts.Create user_account: %w", err)
		}

		// 3. Optionally hash and store the password.
		if in.Password != nil {
			hash, err := s.hashPw(*in.Password)
			if err != nil {
				return fmt.Errorf("user_accounts.Create hash password: %w", err)
			}
			if err := db.New(tx).UpsertAuthLocal(ctx, db.UpsertAuthLocalParams{
				UserAccountID: ua.ID,
				PasswordHash:  hash,
			}); err != nil {
				return fmt.Errorf("user_accounts.Create upsert auth_local: %w", err)
			}
		}

		// 4. Observe the user_account creation.
		after := uaSnapshot(ua)
		if err := s.obs.Observe(ctx, tx, "create", "user_account", &entityID, nil, after); err != nil {
			return err
		}

		out = toUserAccount(ua, in.GivenName, in.FamilyName)
		return nil
	})
	if err != nil {
		if isPgUniqueViolation(err) {
			return UserAccount{}, ErrEmailTaken
		}
		return UserAccount{}, err
	}

	// Post-commit observer.
	after := map[string]any{
		"uuid":     out.UUID.String(),
		"email":    out.Email,
		"is_admin": out.IsAdmin,
	}
	s.obs.ObserveAfterCommit(ctx, "create", "user_account", &out.AccountHolder, after)
	// Also fire the natural_person post-commit observer.
	s.obs.ObserveAfterCommit(ctx, "create", "natural_person", &out.AccountHolder, map[string]any{
		"given_name":  in.GivenName,
		"family_name": in.FamilyName,
	})

	return out, nil
}

// List returns all user accounts matching the optional search term, with
// pagination. Requires admin authorization.
func (s *UserAccountService) List(ctx context.Context, in ListUserAccountsInput) ([]UserAccount, error) {
	// Authorize: list is admin-only; nil target → admin-only per convention.
	if err := s.az.Authorize(ctx, "list", nil); err != nil {
		return nil, err
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 20
	} else if limit > 200 {
		limit = 200
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}

	accounts, err := s.q.SearchUserAccounts(ctx, db.SearchUserAccountsParams{
		Column1: in.Search,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return nil, fmt.Errorf("user_accounts.List: %w", err)
	}

	result := make([]UserAccount, 0, len(accounts))
	for _, ua := range accounts {
		result = append(result, toUserAccount(ua, "", ""))
	}
	return result, nil
}

// Get returns the user account identified by UUID. Requires read authorization
// on the account_holder entity.
func (s *UserAccountService) Get(ctx context.Context, id uuid.UUID) (UserAccount, error) {
	ua, err := s.q.GetUserAccountByUUID(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return UserAccount{}, coreservice.ErrNotFound
		}
		return UserAccount{}, fmt.Errorf("user_accounts.Get: %w", err)
	}

	eid := ua.AccountHolder
	if err := s.az.Authorize(ctx, "read", &eid); err != nil {
		return UserAccount{}, err
	}

	out := toUserAccount(ua, "", "")

	// Enrich with natural person name fields.
	if np, err := s.coreQ.GetNaturalPersonByEntityID(ctx, ua.AccountHolder); err == nil {
		out.GivenName = np.GivenName.String
		out.FamilyName = np.FamilyName.String
		out.EntityKind = "natural_person"
	}

	return out, nil
}

// Update updates mutable fields on the user account and/or its natural person.
// Requires update authorization on the account_holder entity.
func (s *UserAccountService) Update(ctx context.Context, id uuid.UUID, in UpdateUserAccountInput) (UserAccount, error) {
	ua, err := s.q.GetUserAccountByUUID(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return UserAccount{}, coreservice.ErrNotFound
		}
		return UserAccount{}, fmt.Errorf("user_accounts.Update load: %w", err)
	}

	eid := ua.AccountHolder
	if err := s.az.Authorize(ctx, "update", &eid); err != nil {
		return UserAccount{}, err
	}

	before := uaSnapshot(ua)

	newEmail := ua.Email
	if in.Email != nil {
		newEmail = strings.TrimSpace(strings.ToLower(*in.Email))
	}

	var updated db.UserAccount
	var after map[string]any
	err = txhelper.Run(ctx, s.db, func(ctx context.Context, tx pgx.Tx) error {
		qtx := db.New(tx)

		if err := qtx.UpdateUserAccount(ctx, db.UpdateUserAccountParams{
			ID:              ua.ID,
			Email:           newEmail,
			EmailVerifiedAt: ua.EmailVerifiedAt,
			AuthIssuer:      ua.AuthIssuer,
			AuthID:          ua.AuthID,
		}); err != nil {
			return fmt.Errorf("user_accounts.Update update: %w", err)
		}

		if in.IsAdmin != nil {
			if err := qtx.SetAdmin(ctx, db.SetAdminParams{
				ID:      ua.ID,
				IsAdmin: *in.IsAdmin,
			}); err != nil {
				return fmt.Errorf("user_accounts.Update set_admin: %w", err)
			}
		}

		// Update natural person name fields in the same tx.
		if in.GivenName != nil || in.FamilyName != nil {
			coreQtx := coredb.New(tx)
			if np, err := coreQtx.GetNaturalPersonByEntityID(ctx, ua.AccountHolder); err == nil {
				gn := np.GivenName
				fn := np.FamilyName
				if in.GivenName != nil {
					gn = pgtype.Text{String: *in.GivenName, Valid: true}
				}
				if in.FamilyName != nil {
					fn = pgtype.Text{String: *in.FamilyName, Valid: true}
				}
				_ = coreQtx.UpdateNaturalPerson(ctx, coredb.UpdateNaturalPersonParams{
					EntityID:   ua.AccountHolder,
					GivenName:  gn,
					FamilyName: fn,
				})
			}
		}

		// Reload for the after snapshot.
		var reloadErr error
		updated, reloadErr = qtx.GetUserAccountByID(ctx, ua.ID)
		if reloadErr != nil {
			slog.ErrorContext(ctx, "user_accounts.Update: reload", "error", reloadErr)
			updated = ua
			updated.Email = newEmail
		}

		// Compute snapshot once; reused by ObserveAfterCommit below.
		after = uaSnapshot(updated)
		return s.obs.Observe(ctx, tx, "update", "user_account", &eid, before, after)
	})
	if err != nil {
		return UserAccount{}, err
	}

	s.obs.ObserveAfterCommit(ctx, "update", "user_account", &eid, after)

	out := toUserAccount(updated, "", "")
	return out, nil
}

// Delete soft-deletes the user account by archiving its entity.
// Requires delete authorization on the account_holder entity.
func (s *UserAccountService) Delete(ctx context.Context, id uuid.UUID) error {
	ua, err := s.q.GetUserAccountByUUID(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return coreservice.ErrNotFound
		}
		return fmt.Errorf("user_accounts.Delete load: %w", err)
	}

	eid := ua.AccountHolder
	if err := s.az.Authorize(ctx, "delete", &eid); err != nil {
		return err
	}

	before := uaSnapshot(ua)

	err = txhelper.Run(ctx, s.db, func(ctx context.Context, tx pgx.Tx) error {
		coreQtx := coredb.New(tx)

		entityRow, err := coreQtx.GetEntityByID(ctx, ua.AccountHolder)
		if err != nil {
			return fmt.Errorf("user_accounts.Delete resolve entity: %w", err)
		}

		if err := coreQtx.ArchiveEntity(ctx, entityRow.Uuid); err != nil {
			return fmt.Errorf("user_accounts.Delete archive: %w", err)
		}

		return s.obs.Observe(ctx, tx, "delete", "user_account", &eid, before, nil)
	})
	if err != nil {
		return err
	}

	s.obs.ObserveAfterCommit(ctx, "delete", "user_account", &eid, nil)
	return nil
}

// SetAdmin grants or revokes admin privileges on the user account identified
// by UUID. The op string is "grant" or "revoke" and is used for audit logging.
// Requires authorization against the account_holder entity.
func (s *UserAccountService) SetAdmin(ctx context.Context, id uuid.UUID, isAdmin bool, op string) error {
	ua, err := s.q.GetUserAccountByUUID(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return coreservice.ErrNotFound
		}
		return fmt.Errorf("user_accounts.SetAdmin load: %w", err)
	}

	eid := ua.AccountHolder
	if err := s.az.Authorize(ctx, op, &eid); err != nil {
		return err
	}

	before := map[string]any{"is_admin": !isAdmin}
	after := map[string]any{"is_admin": isAdmin}

	err = txhelper.Run(ctx, s.db, func(ctx context.Context, tx pgx.Tx) error {
		qtx := db.New(tx)
		if err := qtx.SetAdmin(ctx, db.SetAdminParams{
			ID:      ua.ID,
			IsAdmin: isAdmin,
		}); err != nil {
			return fmt.Errorf("user_accounts.SetAdmin: %w", err)
		}
		return s.obs.Observe(ctx, tx, op, "user_account", &eid, before, after)
	})
	if err != nil {
		return err
	}

	s.obs.ObserveAfterCommit(ctx, op, "user_account", &eid, after)
	return nil
}

// RecordLogin records a successful login event in the audit log.
// It loads the user_account for accountID, sets the opctx actor to that
// account's entity so the Authorizer sees the right subject, and then
// wraps an Observe call in a transaction for atomicity.
//
// This method is called by the OIDC callback after the user has been resolved
// and before the JWT is issued. It is intentionally not called for the local
// auth (POST /v1/auth/login) path — that path historically did not write login
// audit rows and is not in scope here.
func (s *UserAccountService) RecordLogin(ctx context.Context, accountID int64, provider string) error {
	ua, err := s.q.GetUserAccountByID(ctx, accountID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return coreservice.ErrNotFound
		}
		return fmt.Errorf("user_accounts.RecordLogin load: %w", err)
	}

	entityID := ua.AccountHolder
	// Set the actor to the logging-in user so Authorize sees the correct subject.
	ctx = opctx.WithActor(ctx, entityID)

	if err := s.az.Authorize(ctx, "login", &entityID); err != nil {
		return err
	}

	detail := map[string]any{
		"provider": provider,
		"linked":   true,
	}
	txErr := txhelper.Run(ctx, s.db, func(ctx context.Context, tx pgx.Tx) error {
		return s.obs.Observe(ctx, tx, "login", "user_account", &entityID, nil, detail)
	})
	if txErr != nil {
		return fmt.Errorf("user_accounts.RecordLogin observe: %w", txErr)
	}

	s.obs.ObserveAfterCommit(ctx, "login", "user_account", &entityID, detail)
	return nil
}

// Assume records an admin identity-assumption event in the audit log and
// returns both the admin and assumed user accounts. No DB row is mutated;
// the Observe call participates in a transaction solely for audit-row atomicity.
//
// Returns coreservice.ErrNotFound when the target UUID does not exist.
// Returns an authz error when the actor is not permitted to assume the target.
func (s *UserAccountService) Assume(ctx context.Context, targetUUID uuid.UUID) (adminUA db.UserAccount, assumedUA db.UserAccount, err error) {
	assumedUA, err = s.q.GetUserAccountByUUID(ctx, targetUUID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return db.UserAccount{}, db.UserAccount{}, coreservice.ErrNotFound
		}
		return db.UserAccount{}, db.UserAccount{}, fmt.Errorf("user_accounts.Assume load target: %w", err)
	}

	actorEntityID, ok := opctx.ActorEntityID(ctx)
	if !ok {
		return db.UserAccount{}, db.UserAccount{}, coreservice.ErrNotFound
	}
	adminUA, err = s.q.GetUserAccountByAccountHolder(ctx, actorEntityID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return db.UserAccount{}, db.UserAccount{}, coreservice.ErrNotFound
		}
		return db.UserAccount{}, db.UserAccount{}, fmt.Errorf("user_accounts.Assume load admin: %w", err)
	}

	assumedEntityID := assumedUA.AccountHolder
	if err := s.az.Authorize(ctx, "assume", &assumedEntityID); err != nil {
		return db.UserAccount{}, db.UserAccount{}, err
	}

	detail := map[string]any{
		"admin_uuid":   adminUA.Uuid.String(),
		"assumed_uuid": assumedUA.Uuid.String(),
	}
	txErr := txhelper.Run(ctx, s.db, func(ctx context.Context, tx pgx.Tx) error {
		return s.obs.Observe(ctx, tx, "assume", "user_account", &assumedEntityID, nil, detail)
	})
	if txErr != nil {
		return db.UserAccount{}, db.UserAccount{}, fmt.Errorf("user_accounts.Assume observe: %w", txErr)
	}

	s.obs.ObserveAfterCommit(ctx, "assume", "user_account", &assumedEntityID, detail)
	return adminUA, assumedUA, nil
}

// LoadByUUID loads a user account by its public UUID without performing
// authorization. Used by the handler's thin loadUserAccountByUUIDParam helper.
func (s *UserAccountService) LoadByUUID(ctx context.Context, id uuid.UUID) (db.UserAccount, error) {
	return s.q.GetUserAccountByUUID(ctx, id)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// toUserAccount converts a db.UserAccount to the service-layer UserAccount type.
func toUserAccount(ua db.UserAccount, givenName, familyName string) UserAccount {
	return UserAccount{
		ID:            ua.ID,
		UUID:          ua.Uuid,
		AccountHolder: ua.AccountHolder,
		Email:         ua.Email,
		IsAdmin:       ua.IsAdmin,
		EmailVerified: ua.EmailVerifiedAt != nil,
		GivenName:     givenName,
		FamilyName:    familyName,
		CreatedAt:     ua.CreatedAt,
	}
}

// uaSnapshot builds an audit-log-friendly map from a db.UserAccount.
func uaSnapshot(ua db.UserAccount) map[string]any {
	return map[string]any{
		"uuid":     ua.Uuid.String(),
		"email":    ua.Email,
		"is_admin": ua.IsAdmin,
	}
}

// isPgUniqueViolation reports whether err is a Postgres unique-violation error.
func isPgUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return extractPgError(err, &pgErr) && pgErr.Code == "23505"
}

// extractPgError unwraps a *pgconn.PgError from err, if present.
func extractPgError(err error, target **pgconn.PgError) bool {
	if pgErr, ok := err.(*pgconn.PgError); ok {
		*target = pgErr
		return true
	}
	return false
}

// WithHashPassword returns a NewUserAccountService option that replaces the
// default password hasher. Intended for test use; production code uses
// localauth.HashPassword.
func WithHashPassword(hashPw func(plain string) (string, error)) func(*UserAccountService) {
	return func(s *UserAccountService) { s.hashPw = hashPw }
}
