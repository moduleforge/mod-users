package auth

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/moduleforge/users-module/model/db"
)

// UserResolver resolves a Principal to a *UserContext, auto-creating the
// user on first sight via OIDC.
type UserResolver struct {
	pool      *pgxpool.Pool
	queries   *db.Queries
	adminRole string
}

// NewUserResolver creates a resolver.
func NewUserResolver(pool *pgxpool.Pool, queries *db.Queries, adminRole string) *UserResolver {
	if adminRole == "" {
		adminRole = "admin"
	}
	return &UserResolver{pool: pool, queries: queries, adminRole: adminRole}
}

// Resolve looks up or creates the user associated with the given Principal.
// On first-ever user, sets is_admin = true (root bootstrap).
func (r *UserResolver) Resolve(ctx context.Context, p Principal) (*UserContext, error) {
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

	// Check if this is the first user (root bootstrap).
	var userCount int64
	err = tx.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&userCount)
	if err != nil {
		return user, fmt.Errorf("count users: %w", err)
	}
	isFirstUser := userCount == 0

	// Create entity.
	entity, err := qtx.CreateEntity(ctx, "legal_entity")
	if err != nil {
		return user, fmt.Errorf("create entity: %w", err)
	}

	// Derive display name from email.
	displayName := p.Email
	if idx := strings.Index(p.Email, "@"); idx > 0 {
		displayName = p.Email[:idx]
	}

	// Create legal entity.
	legalEntity, err := qtx.CreateLegalEntity(ctx, db.CreateLegalEntityParams{
		EntityID:    entity.ID,
		Kind:        "natural_person",
		DisplayName: displayName,
	})
	if err != nil {
		return user, fmt.Errorf("create legal entity: %w", err)
	}

	// Create natural person.
	givenName := pgtype.Text{String: displayName, Valid: true}
	_, err = qtx.CreateNaturalPerson(ctx, db.CreateNaturalPersonParams{
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
		EntityID:    entity.ID,
		Email:       p.Email,
		IsAdmin:     isFirstUser,
		AuthIssuer:  authIssuer,
		AuthID:      authID,
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
