package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	coredb "github.com/moduleforge/core-model/db"
	db "github.com/moduleforge/users-module/model/db"
)

// ErrUserGone is returned by UserResolver.Resolve when a locally-issued JWT
// references a user that no longer exists in the users table (e.g., the
// account was deleted after the token was minted). Handlers should translate
// this into 401 Unauthorized — the signature is valid but the identity is
// no longer valid.
var ErrUserGone = errors.New("auth: user no longer exists")

// uuidLookupFn is the slot used by the local-issuer fast path. Extracted as
// a field so tests can substitute a stub without needing a running Postgres.
// Nil is valid — the fast path short-circuits and falls back to the OIDC
// path in that case (useful for pre-Phase 9 fallback semantics in tests).
type uuidLookupFn func(ctx context.Context, u uuid.UUID) (db.User, error)

// UserResolver resolves a Principal to a *UserContext. For OIDC principals it
// auto-creates the user on first sight; for locally-issued JWTs (matching
// LocalIssuer) it takes a fast path that simply loads by UUID.
type UserResolver struct {
	pool        *pgxpool.Pool
	queries     *db.Queries
	coreQ       *coredb.Queries
	adminRole   string
	localIssuer string
	uuidLookup  uuidLookupFn
}

// NewUserResolver creates a resolver. localIssuer is the value written into
// the "iss" claim by IssueLocalJWT — when Resolve sees a Principal with this
// issuer, it skips the OIDC auto-create path and looks up the user by UUID.
func NewUserResolver(pool *pgxpool.Pool, queries *db.Queries, coreQ *coredb.Queries, adminRole, localIssuer string) *UserResolver {
	if adminRole == "" {
		adminRole = "admin"
	}
	r := &UserResolver{
		pool:        pool,
		queries:     queries,
		coreQ:       coreQ,
		adminRole:   adminRole,
		localIssuer: localIssuer,
	}
	if queries != nil {
		r.uuidLookup = queries.GetUserByUUID
	}
	return r
}

// Resolve looks up or creates the user associated with the given Principal.
// On first-ever user, sets is_admin = true (root bootstrap).
func (r *UserResolver) Resolve(ctx context.Context, p Principal) (*UserContext, error) {
	// Local-issuer fast path. A principal minted by IssueLocalJWT carries
	// the user's own UUID in `sub`; the HS256 signature has already proven
	// authenticity, so we only need to hydrate the DB row. No auto-create,
	// no email-link attempt. A missing UUID means the user was deleted
	// between token issue and use — 401, not 500.
	if r.localIssuer != "" && p.Issuer == r.localIssuer && r.uuidLookup != nil {
		parsed, err := uuid.Parse(p.Subject)
		if err != nil {
			return nil, fmt.Errorf("auth: local jwt sub is not a uuid: %w", err)
		}
		user, err := r.uuidLookup(ctx, parsed)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrUserGone
			}
			return nil, fmt.Errorf("auth: lookup local user by uuid: %w", err)
		}
		return r.buildUserContext(user, p), nil
	}

	// Try to find existing user by auth credentials.
	if p.Issuer != "" && p.Subject != "" {
		user, err := r.queries.GetUserByAuth(ctx, db.GetUserByAuthParams{
			AuthIssuer: pgtype.Text{String: p.Issuer, Valid: true},
			AuthID:     pgtype.Text{String: p.Subject, Valid: true},
		})
		if err == nil {
			return r.buildUserContext(user, p), nil
		}
		if err != pgx.ErrNoRows {
			return nil, fmt.Errorf("auth: lookup by auth: %w", err)
		}
	}

	// Try by email if available.
	if p.Email != "" {
		user, err := r.queries.GetUserByEmail(ctx, p.Email)
		if err == nil {
			// Found by email — link auth credentials if not already set.
			if p.Issuer != "" && p.Subject != "" {
				_ = r.queries.UpdateUser(ctx, db.UpdateUserParams{
					ID:              user.ID,
					Email:           user.Email,
					EmailVerifiedAt: user.EmailVerifiedAt,
					AuthIssuer:      pgtype.Text{String: p.Issuer, Valid: true},
					AuthID:          pgtype.Text{String: p.Subject, Valid: true},
				})
			}
			return r.buildUserContext(user, p), nil
		}
		if err != pgx.ErrNoRows {
			return nil, fmt.Errorf("auth: lookup by email: %w", err)
		}
	}

	// New user — auto-create within a transaction.
	user, err := r.autoCreate(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("auth: auto-create user: %w", err)
	}

	return r.buildUserContext(user, p), nil
}

// autoCreate creates a new entity → legal_entity → natural_person → user chain.
func (r *UserResolver) autoCreate(ctx context.Context, p Principal) (db.User, error) {
	var user db.User

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return user, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := r.queries.WithTx(tx)
	coreQtx := r.coreQ.WithTx(tx)

	// Check if this is the first user (root bootstrap).
	var userCount int64
	err = tx.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&userCount)
	if err != nil {
		return user, fmt.Errorf("count users: %w", err)
	}
	isFirstUser := userCount == 0

	// Create entity.
	entity, err := coreQtx.CreateEntity(ctx, "legal_entity")
	if err != nil {
		return user, fmt.Errorf("create entity: %w", err)
	}

	// Derive display name from email.
	displayName := p.Email
	if idx := strings.Index(p.Email, "@"); idx > 0 {
		displayName = p.Email[:idx]
	}

	// Create legal entity.
	legalEntity, err := coreQtx.CreateLegalEntity(ctx, coredb.CreateLegalEntityParams{
		EntityID:    entity.ID,
		Kind:        "natural_person",
		DisplayName: displayName,
	})
	if err != nil {
		return user, fmt.Errorf("create legal entity: %w", err)
	}

	// Create natural person.
	givenName := pgtype.Text{String: displayName, Valid: true}
	_, err = coreQtx.CreateNaturalPerson(ctx, coredb.CreateNaturalPersonParams{
		LegalEntityID: legalEntity.ID,
		GivenName:     givenName,
		FamilyName:    pgtype.Text{},
	})
	if err != nil {
		return user, fmt.Errorf("create natural person: %w", err)
	}

	// Create user.
	var authIssuer, authID pgtype.Text
	if p.Issuer != "" {
		authIssuer = pgtype.Text{String: p.Issuer, Valid: true}
	}
	if p.Subject != "" {
		authID = pgtype.Text{String: p.Subject, Valid: true}
	}

	user, err = qtx.CreateUser(ctx, db.CreateUserParams{
		EntityID:   entity.ID,
		Email:      p.Email,
		IsAdmin:    isFirstUser,
		AuthIssuer: authIssuer,
		AuthID:     authID,
	})
	if err != nil {
		return user, fmt.Errorf("create user: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return user, fmt.Errorf("commit: %w", err)
	}

	if isFirstUser {
		slog.InfoContext(ctx, "first user created with admin privileges",
			"email", p.Email,
			"user_uuid", user.Uuid.String(),
		)
	}

	return user, nil
}

func (r *UserResolver) buildUserContext(user db.User, p Principal) *UserContext {
	// Admin if DB flag is set OR principal has admin role.
	isAdmin := user.IsAdmin
	if !isAdmin {
		for _, role := range p.Roles {
			if role == r.adminRole {
				isAdmin = true
				break
			}
		}
	}

	uc := &UserContext{
		UserID:   user.ID,
		UserUUID: user.Uuid.String(),
		EntityID: user.EntityID,
		Email:    user.Email,
		IsAdmin:  isAdmin,
	}

	if user.DefaultAppID.Valid {
		appID := user.DefaultAppID.Int64
		uc.AppID = &appID
	}

	return uc
}
