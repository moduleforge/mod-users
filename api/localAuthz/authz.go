// Package localAuthz is the public facade for the users-module grants-table Authorizer.
// It re-exports the Authorizer type and constructor from internal/authz.
package localAuthz

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	authzapi "github.com/moduleforge/authz-api/authz"
	authzdb "github.com/moduleforge/authz-model/db"
	inner "github.com/moduleforge/mod-users/api/internal/authz"
)

// Authorizer is the grants-table implementation of coreAuthz.Authorizer.
//
// WARNING: entity-level Authorize honours type grants over instances. A grant
// on a type's entity (directly or through a type-only target group) confers the
// same operation over every existing and future instance of exactly that type,
// so manage on a type entity is full control of every instance (for
// natural_person, including assume of every user account). That reaches
// accounts holding wildcard or admin grants, so manage on natural_person is
// effectively full admin; manage, grant or update on the type entity of
// authz_actor_group or authz_target_group likewise confers authority over every
// group of that kind (adding a member needs grant on the member and, for actor
// groups, update and grant on the group; a bare type-level update only permits
// removing members; manage confers all of it), a path to groups holding
// wildcard grants and so to privilege escalation, and the same holds for every
// other type with instances (app, system_actor, the anonymous system actor, an
// instance of system_actor, where they exist). Grant on type entities only to
// principals trusted with every instance. See the Type grants confer instance
// access decision in docs/architecture.md for the full warning. TypeAuthorizer,
// below, is unchanged: an instance grant never satisfies a type-level check.
type Authorizer = inner.Authorizer

// TypeAuthorizer is implemented by Authorizers that can answer type-level
// questions (create or list of a resource type) distinctly from entity-level
// ones. AuthorizeType's typeID is a types.id, never an entities.id. It is
// allowed to actors holding a wildcard grant covering the operation, or a grant
// on the exact type's entity (types.entity_id of typeID), held directly or
// through target groups, by the actor or its actor groups. It is never answered
// by entity ownership or by grants on unrelated entities (in particular the
// entity whose id happens to equal typeID), and a type does not inherit from its
// subtypes or parent types.
//
// Consumer contract: a caller holding only a coreAuthz.Authorizer must assert
// this capability and use it for type-level checks. When the assertion fails
// (for example a decorator hides the method) the caller must fall back to
// Authorize(ctx, op, nil), which is fail-safe, and never to
// Authorize(ctx, op, &typeID): Authorize treats its target as an entities.id,
// so a type id there is matched against unrelated entities. The nil-target
// fallback answers from wildcard grants only, so it denies actors who hold only
// a type grant; that is fail-closed.
type TypeAuthorizer interface {
	AuthorizeType(ctx context.Context, operation string, typeID int64) error
}

// Compile-time assertion: Authorizer implements TypeAuthorizer.
var _ TypeAuthorizer = (*Authorizer)(nil)

// New constructs an Authorizer backed by the authz grants table.
func New(authzQ authzdb.Querier, opReg *authzapi.OperationRegistry, pool *pgxpool.Pool) *Authorizer {
	return inner.New(authzQ, opReg, pool)
}
