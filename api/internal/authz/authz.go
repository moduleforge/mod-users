// Package authz provides the users-module Authorizer implementation.
//
// Policy: actors with a wildcard grant in the grants table can perform any
// operation (checkWildcardGrant short-circuit, replacing the previous is_admin
// column-based approach). All other actors must have an explicit grant in the
// grants table, resolved via recursive actor/target group CTEs, OR satisfy the
// per-resource "own" predicate (actor IS the entity).
//
// The implementation resolves the acting user from ctx via opctx.ActorEntityID
// (and opctx.SudoActorEntityID for assume sessions).
//
// Operations with a nil target (create, list, or admin-only operations) are
// denied for non-wildcard-admin actors. A wildcard grant satisfies nil-target
// operations because the wildcard check runs before the nil-target denial.
//
// The Authorizer's single-row check issues a recursive-CTE SQL query that walks
// UP from the actor through actor groups, then checks for a grant between any
// actor-chain member and any target-chain member (target walking UP to target
// groups), for any operation in the SatisfiedBy closure. If no grant is found,
// a per-resource own-predicate check is performed in Go.
package authz

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	authzdb "github.com/moduleforge/authz-model/db"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/opctx"

	authzapi "github.com/moduleforge/authz-api/authz"
)

// ErrUnauthenticated is returned when no actor is present on the context.
// HTTP handlers should map this to 401.
var ErrUnauthenticated = errors.New("authz: no authenticated actor")

// ErrForbidden is returned when the actor is authenticated but not permitted
// to perform the requested operation. HTTP handlers should map this to 403.
var ErrForbidden = errors.New("authz: forbidden")

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
//  4. If target == nil: return ErrForbidden (type-level grants not yet supported).
//  5. If target != nil: run checkGrant (recursive-CTE) and per-resource own-
//     predicate checks.
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
	// Some callers pass semantic labels like "create" that are not registered
	// operations (they rely on the admin short-circuit). For the wildcard check,
	// we fall back to the "manage" opIDs if the slug is unknown — a wildcard
	// manage grant means full control over any operation.
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
		// Nil target means create, list, or other admin-only gated operations.
		// Type-level grants (grants over a type entity ID) are not yet
		// supported; admin-only via wildcard grant.
		return ErrForbidden
	}

	// Run the recursive-CTE grant check.
	granted, err := a.checkGrant(ctx, actorEntityID, *target, opIDs)
	if err != nil {
		return err
	}
	if granted {
		return nil
	}

	// Per-resource own-predicate: check if the actor IS the target entity.
	// For natural_person, corporation, service_account, legal_entity: the actor's
	// entity_id equals the target's entity_id when it's their own row.
	//
	// Per-resource own semantics (matches GrantTableGenerator and AdminOrOwnGenerator):
	//   - natural_person, service_account: entity_id = actor (covered here).
	//   - corporation: no own predicate; non-admins are never corporations.
	//   - legal_entity: delegates to natural_person/corporation — covered via
	//     entity_id equality for natural_persons.
	//   - tag: owner_id or subject_id = actor. Requires a separate tag query;
	//     covered by the tagOwnsCheck below.
	if *target == actorEntityID {
		return nil // actor IS the entity (natural_person / service_account own-predicate)
	}

	// Tag own-predicate: check if actor owns or is subject of the tag.
	// This requires a tag table lookup, which we perform via the authzQ pool.
	// If the lookup returns no-rows, the entity is not a tag — deny.
	tagOwned, err := a.checkTagOwnership(ctx, actorEntityID, *target)
	if err != nil {
		// Lookup failure or entity-not-a-tag maps to forbidden (deny-safe).
		return ErrForbidden
	}
	if tagOwned {
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

// checkWildcardGrant queries the grants table for a wildcard grant:
// a row where actor_id is in the actor's transitive group chain and
// target_id IS NULL and operation_id is in the opIDs closure.
//
// This implements the B4 mechanism from the Final design: a Go-side
// EXISTS query before checkGrant, replacing the is_admin column short-circuit.
//
// The query uses the same ActorChain CTE as checkGrant for consistency and
// so that actor-group-based wildcard grants work correctly.
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

// checkGrant runs the recursive-CTE grant check:
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
//	SELECT EXISTS(
//	    SELECT 1 FROM grants g
//	    JOIN ActorChain ac ON g.actor_id = ac.aid
//	    JOIN TargetChain tc ON g.target_id = tc.tid
//	    WHERE g.operation_id = ANY(opIDs)
//	)
//
// Returns true if a matching grant exists.
func (a *Authorizer) checkGrant(ctx context.Context, actorEntityID, targetEntityID int64, opIDs []int32) (bool, error) {
	const grantCheckSQL = `
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
SELECT EXISTS(
    SELECT 1 FROM grants g
    JOIN ActorChain ac ON g.actor_id = ac.aid
    JOIN TargetChain tc ON g.target_id = tc.tid
    WHERE g.operation_id = ANY($3::int[])
)`

	var exists bool
	err := a.pool.QueryRow(ctx, grantCheckSQL, actorEntityID, targetEntityID, opIDs).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// checkTagOwnership checks whether actorEntityID owns the tag with entity_id =
// targetEntityID (owner_id = actor OR subject_id = actor). Returns false if the
// entity is not a tag (pgx.ErrNoRows or a non-tag type).
//
// This implements the tag own-predicate from AdminOrOwnGenerator:
//
//	t.owner_id = p_actor_entity_id OR t.subject_id = p_actor_entity_id
func (a *Authorizer) checkTagOwnership(ctx context.Context, actorEntityID, targetEntityID int64) (bool, error) {
	const tagOwnerSQL = `
SELECT EXISTS(
    SELECT 1 FROM tags t
    WHERE t.entity_id = $1
      AND (t.owner_id = $2 OR t.subject_id = $2)
)`
	var owned bool
	err := a.pool.QueryRow(ctx, tagOwnerSQL, targetEntityID, actorEntityID).Scan(&owned)
	if err != nil {
		return false, err
	}
	return owned, nil
}
