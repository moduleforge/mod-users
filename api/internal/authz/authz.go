// Package authz provides the users-module Authorizer implementation.
//
// Policy: actors with a wildcard grant in the grants table can perform any
// operation (checkWildcardGrant short-circuit, replacing the previous is_admin
// column-based approach). All other actors must have an explicit grant in the
// grants table, resolved via recursive actor/target group CTEs, OR own the
// target entity outright (entities.owner_id equals the effective actor's
// entity id). Ownership is a single, resource-agnostic predicate: it is not
// scoped per resource type, and owning the target satisfies every operation
// on that entity, not just reads.
//
// The implementation resolves the acting user from ctx via opctx.ActorEntityID
// (and opctx.SudoActorEntityID for assume sessions).
//
// Operations with a nil target (list, or other admin-only operations) are
// denied for non-wildcard-admin actors. A wildcard grant satisfies nil-target
// operations because the wildcard check runs before the nil-target denial.
//
// The Authorizer's single-row check issues one recursive-CTE SQL query
// (checkGrantOrOwn) that walks UP from the actor through actor groups, checks
// for a grant between any actor-chain member and any target-chain member
// (target walking UP to target groups) for any operation in the SatisfiedBy
// closure, OR-ed with a check that the target entity's owner_id equals the
// effective actor's entity id. A NULL owner_id matches no actor, so entities
// that keep a NULL owner by design (corporation, authz_actor_group,
// authz_target_group) stay inaccessible via this predicate.
package authz

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	authzdb "github.com/moduleforge/authz-model/db"
	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/opctx"

	authzapi "github.com/moduleforge/authz-api/authz"
)

// ErrUnauthenticated is returned when no actor is present on the context.
// HTTP handlers should map this to 401.
//
// This is an alias for apiresp.ErrUnauthenticated (not an independent
// sentinel) so errors.Is matches it across module boundaries — apiresp is
// the canonical home; see docs/mf-standards/architecture/api-response-design.md
// "Go-layer ownership".
var ErrUnauthenticated = apiresp.ErrUnauthenticated

// ErrForbidden is returned when the actor is authenticated but not permitted
// to perform the requested operation. HTTP handlers should map this to 403.
//
// This is an alias for apiresp.ErrForbidden (not an independent sentinel);
// see the ErrUnauthenticated doc comment above.
var ErrForbidden = apiresp.ErrForbidden

// Compile-time assertion: Authorizer satisfies core's authz.Authorizer.
var _ coreAuthz.Authorizer = (*Authorizer)(nil)

// Authorizer is the users-module implementation of core's authz.Authorizer.
// It is constructed once at the composition root (main.go) and injected into
// all service constructors via coreservice.New.
type Authorizer struct {
	authzQ authzdb.Querier
	opReg  *authzapi.OperationRegistry
	pool   *pgxpool.Pool

	// wildcardGrantFn is used internally by tests to stub the wildcard grant
	// check without requiring a live database. If nil, checkWildcardGrant is
	// used instead. Only set this field in tests.
	wildcardGrantFn func(ctx context.Context, actorEntityID int64, opIDs []int32) (bool, error)

	// grantOrOwnFn is used internally by tests to stub the combined grant-or-
	// own check without requiring a live database. If nil, checkGrantOrOwn is
	// used instead. Only set this field in tests.
	grantOrOwnFn func(ctx context.Context, actorEntityID, targetEntityID int64, opIDs []int32) (bool, error)
}

// New constructs an Authorizer.
//
//   - authzQ is used for grant resolution queries.
//   - opReg provides the SatisfiedBy closure for each operation string.
//   - pool is the database pool used for the recursive-CTE grant check and the
//     wildcard grant check.
func New(authzQ authzdb.Querier, opReg *authzapi.OperationRegistry, pool *pgxpool.Pool) *Authorizer {
	return &Authorizer{authzQ: authzQ, opReg: opReg, pool: pool}
}

// Authorize enforces the policy described in the package doc.
//
// The effective actor is whichever entity ID is set on ctx:
//   - When SudoActorEntityID is set, that is the effective actor (admin is
//     acting as the sudo user; the sudo user's permissions apply).
//   - Otherwise ActorEntityID is the actor.
//
// Flow:
//  1. Resolve effective actor from context.
//  2. Compute opIDs via opReg.SatisfiedBy(operation).
//  3. checkWildcardGrant — if any (actor-chain, operation, NULL-target) grant
//     exists, return nil immediately (wildcard admin short-circuit).
//  4. If target == nil: return ErrForbidden (no entity to resolve a grant
//     against; only a wildcard grant, already checked in step 3, can satisfy
//     a nil-target operation).
//  5. If target != nil: run checkGrantOrOwn — a single recursive-CTE query
//     that resolves a grant via the actor/target group chains, OR-ed with an
//     entities.owner_id ownership check against the target.
func (a *Authorizer) Authorize(ctx context.Context, operation string, target *int64) error {
	// Resolve effective actor. Assumed actor takes priority over real actor.
	actorEntityID, ok := effectiveActor(ctx)
	if !ok {
		return ErrUnauthenticated
	}

	// Compute the satisfied-by closure for the requested operation. opIDs is used
	// by both the wildcard check and the targeted grant check.
	//
	// SatisfiedBy may return an error if the operation slug is not in the registry.
	// As of this writing, every in-tree caller passes a registered operation slug
	// (verified across mod-authz, mod-users, mod-tasks, mod-tags, and mod-core);
	// this branch is defense-in-depth against an uninitialized or lagging
	// registry, not a documented reliance on an unregistered slug. For the
	// wildcard check, we fall back to the "manage" opIDs if the slug is
	// unknown — a wildcard manage grant means full control over any operation.
	opIDs, err := a.opReg.SatisfiedBy(operation)
	if err != nil {
		// Unknown slug: use "manage" opIDs for the wildcard check.
		// If the actor has a wildcard manage grant, allow. Otherwise deny.
		manageIDs, mErr := a.opReg.SatisfiedBy("manage")
		if mErr != nil {
			// Even "manage" is unknown (uninitialized registry). Deny.
			return ErrForbidden
		}
		wildcardAllowed, wErr := a.checkWildcardGrantDispatch(ctx, actorEntityID, manageIDs)
		if wErr != nil {
			return wErr
		}
		if wildcardAllowed {
			return nil // wildcard manage admin can do anything
		}
		return ErrForbidden
	}

	// Wildcard grant check: if the actor (or any actor group they belong to)
	// holds a grant with target_id IS NULL and operation_id in the opIDs closure,
	// allow unconditionally. This replaces the is_admin column short-circuit.
	wildcardAllowed, err := a.checkWildcardGrantDispatch(ctx, actorEntityID, opIDs)
	if err != nil {
		return err
	}
	if wildcardAllowed {
		return nil
	}

	// Non-wildcard-admin. Check target.
	if target == nil {
		// Nil target means list or other operations with no entity to resolve
		// a grant or own-predicate against. Type-level checks — a non-nil
		// target that is a type entity ID rather than an owned resource, e.g.
		// registered actor-group/target-group "create" calls — are supported;
		// they fall through to checkGrantOrOwn below like any other non-nil
		// target.
		// With a nil target there is nothing to resolve a grant against, so
		// only a wildcard grant (already checked above) can satisfy this call.
		return ErrForbidden
	}

	// Run the recursive-CTE grant check, OR-ed with an entities.owner_id
	// ownership check against the target — one query, one DB round-trip.
	// Owning the target satisfies every operation on that entity (read,
	// update, delete, assume, grant, revoke, ...); the own arm is not gated
	// on the operation or on opIDs, mirroring the list-side own-arm in
	// mod-core/api/authz/setup/grant_table.go, which is scoped only by the
	// caller's op_ids closure and never by op identity within the arm
	// itself. A genuine DB error propagates to the caller rather than being
	// swallowed into a denial.
	granted, err := a.checkGrantOrOwnDispatch(ctx, actorEntityID, *target, opIDs)
	if err != nil {
		return err
	}
	if granted {
		return nil
	}

	return ErrForbidden
}

// effectiveActor returns the entity ID that should be used for policy checks.
// If a sudo actor is set (admin assuming another user's identity), that
// entity ID is returned, since the admin is acting as the sudo user.
func effectiveActor(ctx context.Context) (int64, bool) {
	if id, ok := opctx.SudoActorEntityID(ctx); ok {
		return id, true
	}
	return opctx.ActorEntityID(ctx)
}

// checkWildcardGrantDispatch calls wildcardGrantFn if set (test stub), otherwise
// delegates to checkWildcardGrant.
func (a *Authorizer) checkWildcardGrantDispatch(ctx context.Context, actorEntityID int64, opIDs []int32) (bool, error) {
	if a.wildcardGrantFn != nil {
		return a.wildcardGrantFn(ctx, actorEntityID, opIDs)
	}
	return a.checkWildcardGrant(ctx, actorEntityID, opIDs)
}

// checkGrantOrOwnDispatch calls grantOrOwnFn if set (test stub), otherwise
// delegates to checkGrantOrOwn.
func (a *Authorizer) checkGrantOrOwnDispatch(ctx context.Context, actorEntityID, targetEntityID int64, opIDs []int32) (bool, error) {
	if a.grantOrOwnFn != nil {
		return a.grantOrOwnFn(ctx, actorEntityID, targetEntityID, opIDs)
	}
	return a.checkGrantOrOwn(ctx, actorEntityID, targetEntityID, opIDs)
}

// checkWildcardGrant queries the grants table for a wildcard grant:
// a row where actor_id is in the actor's transitive group chain and
// target_id IS NULL and operation_id is in the opIDs closure.
//
// This implements the B4 mechanism from the Final design: a Go-side
// EXISTS query before checkGrantOrOwn, replacing the is_admin column
// short-circuit.
//
// The query uses the same ActorChain CTE as checkGrantOrOwn for consistency
// and so that actor-group-based wildcard grants work correctly.
func (a *Authorizer) checkWildcardGrant(ctx context.Context, actorEntityID int64, opIDs []int32) (bool, error) {
	const wildcardCheckSQL = `
WITH RECURSIVE
    ActorChain AS (
        SELECT $1::bigint AS aid
        UNION
        SELECT agm.group_id
        FROM authz_actor_group_members agm
        JOIN ActorChain ac ON agm.member_id = ac.aid
    )
SELECT EXISTS(
    SELECT 1 FROM grants g
    JOIN ActorChain ac ON g.actor_id = ac.aid
    WHERE g.operation_id = ANY($2::int[])
      AND g.target_id IS NULL
)`

	var exists bool
	err := a.pool.QueryRow(ctx, wildcardCheckSQL, actorEntityID, opIDs).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// checkGrantOrOwn runs the recursive-CTE grant check, OR-ed with a generic,
// resource-agnostic ownership check against entities.owner_id:
//
//	WITH RECURSIVE
//	    ActorChain AS (
//	        SELECT actorEntityID AS aid
//	        UNION
//	        SELECT agm.group_id FROM authz_actor_group_members agm JOIN ActorChain ac ON agm.member_id = ac.aid
//	    ),
//	    TargetChain AS (
//	        SELECT targetEntityID AS tid
//	        UNION
//	        SELECT atgm.group_id FROM authz_target_group_members atgm JOIN TargetChain tc ON atgm.member_id = tc.tid
//	    )
//	SELECT
//	    EXISTS (
//	        SELECT 1 FROM grants g
//	        JOIN ActorChain ac ON g.actor_id = ac.aid
//	        JOIN TargetChain tc ON g.target_id = tc.tid
//	        WHERE g.operation_id = ANY(opIDs)
//	    )
//	    OR EXISTS (
//	        SELECT 1 FROM entities e
//	        WHERE e.id = targetEntityID
//	          AND e.owner_id = actorEntityID
//	    )
//
// The ownership arm is a single, resource-agnostic predicate — it is not
// scoped per resource type, and it is not gated on the operation or on
// opIDs: owning the target satisfies every operation on that entity. It is
// also not scoped by type_is_or_descends_from, unlike the analogous
// list-side own-arm in mod-core/api/authz/setup/grant_table.go — that
// predicate is load-bearing there only because that arm scans all of
// entities; here the query already has one specific target entity id, so
// type scoping would be meaningless.
//
// e.owner_id = actorEntityID evaluates to NULL (not true) when owner_id IS
// NULL, so a NULL-owner entity — corporation, authz_actor_group,
// authz_target_group, by design — matches no actor and stays inaccessible
// via this predicate. No COALESCE / IS NOT DISTINCT FROM is used, since
// either would defeat that semantics.
//
// Returns true if a matching grant exists or the actor owns the target.
func (a *Authorizer) checkGrantOrOwn(ctx context.Context, actorEntityID, targetEntityID int64, opIDs []int32) (bool, error) {
	const grantOrOwnCheckSQL = `
WITH RECURSIVE
    ActorChain AS (
        SELECT $1::bigint AS aid
        UNION
        SELECT agm.group_id
        FROM authz_actor_group_members agm
        JOIN ActorChain ac ON agm.member_id = ac.aid
    ),
    TargetChain AS (
        SELECT $2::bigint AS tid
        UNION
        SELECT atgm.group_id
        FROM authz_target_group_members atgm
        JOIN TargetChain tc ON atgm.member_id = tc.tid
    )
SELECT
    EXISTS (
        SELECT 1 FROM grants g
        JOIN ActorChain ac ON g.actor_id = ac.aid
        JOIN TargetChain tc ON g.target_id = tc.tid
        WHERE g.operation_id = ANY($3::int[])
    )
    OR EXISTS (
        SELECT 1 FROM entities e
        WHERE e.id = $2::bigint
          AND e.owner_id = $1::bigint
    )`

	var exists bool
	err := a.pool.QueryRow(ctx, grantOrOwnCheckSQL, actorEntityID, targetEntityID, opIDs).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}
