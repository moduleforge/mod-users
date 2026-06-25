// Package users is the public facade for users-module user account hooks.
// It re-exports hook constructors from internal/users.
package users

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	authzservice "github.com/moduleforge/authz-api/service"
	inner "github.com/moduleforge/users-module/api/internal/users"
)

// NewFirstUserHook returns a hook function that bootstraps the wildcard manage
// grant for the very first user account created.
func NewFirstUserHook(pool *pgxpool.Pool, grantSvc authzservice.GrantServicer) func(ctx context.Context, entityID int64) error {
	return inner.NewFirstUserHook(pool, grantSvc)
}
