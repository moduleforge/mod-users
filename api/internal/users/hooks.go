// Package users provides exported constructor functions for user-account lifecycle
// hooks that the moduleforge manifest compiler wires into the composition root.
package users

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	authzservice "github.com/moduleforge/authz-api/service"
	coredb "github.com/moduleforge/core-model/db"
)

// NewFirstUserHook returns a hook function that bootstraps the wildcard manage
// grant for the very first user account created in a fresh database.
//
// The returned function is intended to be registered via SetFirstUserHook on
// both the local auth handler and the OIDC user resolver.  When it fires, the
// entity row is already committed, so the in-hook transaction can see it.
// Failure is non-fatal at the call site (the account already exists and an
// operator can create the grant manually); the caller is responsible for
// deciding whether to surface or suppress the error.
//
// Parameters:
//   - pool:     pgx connection pool used to resolve the entity UUID.
//   - grantSvc: GrantServicer that issues the wildcard manage grant.
func NewFirstUserHook(pool *pgxpool.Pool, grantSvc authzservice.GrantServicer) func(ctx context.Context, entityID int64) error {
	return func(hookCtx context.Context, entityID int64) error {
		ent, err := coredb.New(pool).GetEntityByID(hookCtx, entityID)
		if err != nil {
			return fmt.Errorf("first-user hook: resolve entity UUID: %w", err)
		}
		if _, err := grantSvc.CreateWildcardGrant(hookCtx, ent.Uuid, "manage"); err != nil {
			return fmt.Errorf("first-user hook: create wildcard grant: %w", err)
		}
		return nil
	}
}
