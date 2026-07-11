// Package users provides exported constructor functions for user-account lifecycle
// hooks that the moduleforge manifest compiler wires into the composition root.
package users

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authzservice "github.com/moduleforge/authz-api/service"
	"github.com/moduleforge/core-api/opctx"
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
		return grantFirstUserWildcard(hookCtx, grantSvc, ent.Uuid, entityID)
	}
}

// grantFirstUserWildcard issues the wildcard "manage" grant for the newly
// created entity. The signup request that triggers this hook is
// unauthenticated (the account did not exist yet when the request started),
// so ctx carries no actor. CreateWildcardGrant's audit-observer write
// hard-requires opctx.ActorEntityID to be set, so attribute the
// grant-creation event to the newly created entity itself — the first
// user's own signup is what triggers their own wildcard grant.
//
// Factored out of NewFirstUserHook's closure so the actor-context
// requirement can be exercised directly in a unit test, without a live
// database connection to satisfy the enclosing GetEntityByID lookup.
func grantFirstUserWildcard(ctx context.Context, grantSvc authzservice.GrantServicer, actorUUID uuid.UUID, entityID int64) error {
	ctx = opctx.WithActor(ctx, entityID)
	if _, err := grantSvc.CreateWildcardGrant(ctx, actorUUID, "manage"); err != nil {
		return fmt.Errorf("first-user hook: create wildcard grant: %w", err)
	}
	return nil
}
