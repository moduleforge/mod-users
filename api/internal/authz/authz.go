// Package authz provides the users-module Authorizer implementation.
//
// Policy: users with is_admin = true in their user_account row can do anything
// (is_admin short-circuit, per Phase 2.2 Q8-A). All other actors must have an
// explicit grant in the grants table, resolved via recursive actor/target group
// CTEs, OR satisfy the per-resource "own" predicate (actor IS the entity).
//
// The implementation resolves the acting user from ctx via opctx.ActorEntityID
// (and opctx.SudoActorEntityID for assume sessions).
//
// Operations with a nil target (create, list, or admin-only operations) are
// denied for non-admin actors unless they hold an explicit grant over a type-ID
// target. For now (Phase 2.2) type-level grants are not yet implemented, so
// nil-target operations remain admin-only via the is_admin short-circuit.
// A comment near each nil-target denial documents this as a future evolution
// point: "Grants over type IDs are not yet supported; admin-only via is_admin."
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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	authzdb "github.com/moduleforge/authz-model/db"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/opctx"
	usersdb "github.com/moduleforge/users-module/model/db"

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
	q      usersdb.Querier
	authzQ authzdb.Querier
	opReg  *authzapi.OperationRegistry
	pool   *pgxpool.Pool
}

// New constructs an Authorizer.
//
//   - q is used to look up the actor's is_admin flag by account_holder.
//   - authzQ is used for grant resolution queries.
//   - opReg provides the SatisfiedBy closure for each operation string.
//   - pool is the database pool used for the recursive-CTE grant check.
func New(q usersdb.Querier, authzQ authzdb.Querier, opReg *authzapi.OperationRegistry, pool *pgxpool.Pool) *Authorizer {
	return &Authorizer{q: q, authzQ: authzQ, opReg: opReg, pool: pool}
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
//  2. Look up is_admin; if true, return nil (admin short-circuit).
//  3. For nil target (create/list/admin-only): deny. Grants over type IDs are
//     not yet supported — admin-only via is_admin.
//  4. For entity-ID target: run the recursive-CTE grant check; if a grant
//     covers this (actor, target, op-closure) tuple, allow.
//  5. Fall through to per-resource own-predicate check.
func (a *Authorizer) Authorize(ctx context.Context, operation string, target *int64) error {
	// Resolve effective actor. Assumed actor takes priority over real actor.
	actorEntityID, ok := effectiveActor(ctx)
	if !ok {
		return ErrUnauthenticated
	}

	// Look up admin status via the user_account row. account_holder = entity_id.
	isAdmin, err := a.isAdmin(ctx, actorEntityID)
	if err != nil {
		// A lookup failure is an internal fault, not an authz denial. Return
		// it as-is so handlers can map it to 500.
		return err
	}

	if isAdmin {
		return nil // admin short-circuit (Q8-A)
	}

	// Non-admin. Check target.
	if target == nil {
		// Nil target means create, list, or other admin-only gated operations.
		// Grants over type IDs are not yet supported; admin-only via is_admin.
		return ErrForbidden
	}

	// Compute the satisfied-by closure for the requested operation.
	opIDs, err := a.opReg.SatisfiedBy(operation)
	if err != nil {
		// Unknown operation slug — either a programming error or a stale registry.
		// Deny rather than allowing by accident.
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
	// For tag: we would need to query owner_id or subject_id — but the tag type
	// is not exposed here without a type lookup. The actor-IS-entity check below
	// covers the majority of self-access cases (user reading their own profile).
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

// isAdmin queries the user_account by account_holder (entity_id) to determine
// whether the actor has admin privileges.
//
// When the account_holder has no user_account row (deleted account, service
// account, or corporation with no user_account), the lookup returns ErrNoRows.
// This is a forbidden state, not a server fault: the actor cannot be
// authenticated as a user with any privileges.
func (a *Authorizer) isAdmin(ctx context.Context, entityID int64) (bool, error) {
	ua, err := a.q.GetUserAccountByAccountHolder(ctx, entityID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrForbidden
		}
		return false, err
	}
	return ua.IsAdmin, nil
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
