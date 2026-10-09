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
// The implementation resolves the acting user from ctx via
// opctx.EffectiveActorEntityID, which applies the sudo-first-then-actor
// policy (a sudo actor, when set, takes priority over the real actor).
//
// Authorize is an entity-level check: its target is always an entities.id, or
// nil for "no specific entity". Operations with a nil target (list, or other
// admin-only operations) are denied for non-wildcard-admin actors. A wildcard
// grant satisfies nil-target operations because the wildcard check runs before
// the nil-target denial.
//
// Type-level checks (create or list of a resource type, where the caller holds
// a types.id) are a different question and use AuthorizeType. They are
// authorized by a wildcard grant, or by a grant on the exact type's entity
// (types.entity_id of the type), held directly or through target groups, by the
// actor or one of its actor groups. There is no ownership arm, no subtype or
// parent-type inheritance, and the types.id is never treated as an entities.id.
//
// Passing a types.id to Authorize is a caller bug that Authorize cannot detect
// (both are int64): it would be matched against entities.owner_id and
// grants.target_id as if it were an entity id. Callers that hold only a
// coreAuthz.Authorizer must assert the TypeAuthorizer capability and otherwise
// fall back to Authorize(ctx, op, nil), never to Authorize(ctx, op, &typeID).
// A type's own entity (types.entity_id) is a legitimate Authorize target for
// entity-level operations on the type entity itself (for example read or
// grant), but it is never a stand-in for the type id in a type-level check.
//
// The Authorizer's single-row check issues one recursive-CTE SQL query
// (checkGrantOrOwn) that walks UP from the actor through actor groups, checks
// for a grant between any actor-chain member and any target-chain member
// (target walking UP to target groups) for any operation in the SatisfiedBy
// closure, OR-ed with a check that the target entity's owner_id equals the
// effective actor's entity id. A NULL owner_id matches no actor, so entities
// that keep a NULL owner by design (corporation, authz_actor_group,
// authz_target_group) stay inaccessible via this predicate.
//
// Instance semantics: the target chain is also seeded with the entity of the
// target's exact type, so a grant on a type's entity (directly or through a
// type-only target group) confers the same operation over EVERY existing and
// future instance of exactly that type, for every operation in its SatisfiedBy
// closure. manage on a type entity is therefore full control of every instance
// (read, update, delete, assume, grant, revoke, ...); for natural_person that
// includes assume of every user account and installing SSH keys through the
// operator routes. list implies read, so a type-level list grant reads every
// instance. manage or grant on a type-only target group confers this for every
// type in the group. It does not extend to subtypes or parent types and never
// applies to type entities themselves (a grant on the sentinel "type" entity
// confers nothing over type entities).
// Escalation paths: manage, grant or update on the type entity of
// authz_actor_group or authz_target_group confers that operation over every
// group of that kind. Adding a member needs grant on the member (and, for actor
// groups, update and grant on the group); a bare type-level update only permits
// removing members, and manage confers all of it. That is a path to groups that
// hold wildcard grants and so to privilege escalation. The same applies to every
// other type that has instances (app, system_actor and the anonymous system
// actor, an instance of system_actor, where they exist). The assume of every
// user account that manage on the natural_person type entity confers includes
// accounts that themselves hold wildcard or admin grants, so that grant is
// effectively full admin.
// Grant on type entities only to principals trusted with every instance: a
// holder of grant on a type entity can hand out instance-wide authority over
// that type. Until mod-core's matching GrantTableGenerator arm lands,
// single-row Authorize and the list-side accessible_*_ids_for_actor functions
// disagree for type-grant holders (single-row allows, list omits).
// AuthorizeType is unchanged: an instance grant never satisfies a type-level
// check.
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

	// typeGrantFn is used internally by tests to stub the type-grant check
	// (AuthorizeType's grant-on-the-type's-entity arm) without requiring a live
	// database. If nil, checkTypeGrant is used instead. Only set this field in
	// tests.
	typeGrantFn func(ctx context.Context, actorEntityID, typeID int64, opIDs []int32) (bool, error)
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

// Authorize enforces the policy described in the package doc for an
// entity-level check: may the effective actor perform operation on the entity
// whose entities.id is *target?
//
// The target is ALWAYS an entities.id (or nil for "no specific entity"). It is
// never a types.id. Passing a types.id here is a caller bug that Authorize
// cannot detect, because both ids are plain int64: the value would be matched
// as an entity id against entities.owner_id and grants.target_id, so any actor
// who owns, or holds a grant over, the unrelated entity whose id happens to
// equal that type id would be allowed. Type-level checks (create or list of a
// resource type) must use AuthorizeType instead. A caller that holds only a
// coreAuthz.Authorizer must assert TypeAuthorizer and otherwise fall back to
// Authorize(ctx, op, nil), never to Authorize(ctx, op, &typeID).
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
//     that resolves a grant via the actor/target group chains (the target
//     chain is seeded with the target and, unless the target is itself a type
//     entity, the entity of its exact type), OR-ed with an entities.owner_id
//     ownership check against the target.
//
// WARNING, instance semantics: the target chain is also seeded with the entity of the
// target's exact type, so a grant on a type's entity (directly or through a
// type-only target group) confers the same operation over EVERY existing and
// future instance of exactly that type, for every operation in its SatisfiedBy
// closure. manage on a type entity is therefore full control of every instance
// (read, update, delete, assume, grant, revoke, ...); for natural_person that
// includes assume of every user account and installing SSH keys through the
// operator routes. list implies read, so a type-level list grant reads every
// instance. manage or grant on a type-only target group confers this for every
// type in the group. It does not extend to subtypes or parent types and never
// applies to type entities themselves (a grant on the sentinel "type" entity
// confers nothing over type entities).
// Escalation paths: manage, grant or update on the type entity of
// authz_actor_group or authz_target_group confers that operation over every
// group of that kind. Adding a member needs grant on the member (and, for actor
// groups, update and grant on the group); a bare type-level update only permits
// removing members, and manage confers all of it. That is a path to groups that
// hold wildcard grants and so to privilege escalation. The same applies to every
// other type that has instances (app, system_actor and the anonymous system
// actor, an instance of system_actor, where they exist). The assume of every
// user account that manage on the natural_person type entity confers includes
// accounts that themselves hold wildcard or admin grants, so that grant is
// effectively full admin.
// Grant on type entities only to principals trusted with every instance: a
// holder of grant on a type entity can hand out instance-wide authority over
// that type. Until mod-core's matching GrantTableGenerator arm lands,
// single-row Authorize and the list-side accessible_*_ids_for_actor functions
// disagree for type-grant holders (single-row allows, list omits).
// AuthorizeType is unchanged: an instance grant never satisfies a type-level
// check.
//
// Steps 1-3 are shared with AuthorizeType via authorizePrelude so the two
// entry points cannot drift.
func (a *Authorizer) Authorize(ctx context.Context, operation string, target *int64) error {
	actorEntityID, opIDs, done, err := a.authorizePrelude(ctx, operation)
	if done {
		return err
	}

	// Non-wildcard-admin. Check target.
	if target == nil {
		// Nil target means "no specific entity" (list, or other operations with
		// no entity to resolve a grant or own-predicate against), so there is
		// nothing to resolve a grant or the ownership predicate against: only a
		// wildcard grant (already checked in the prelude) can satisfy this call.
		//
		// A non-nil target is always treated as an entities.id and handed to
		// checkGrantOrOwn below. In particular a types.id must never be passed
		// here as a "type-level" target: Authorize cannot tell the two apart,
		// and the value would be matched against unrelated entities. Type-level
		// create/list checks go through AuthorizeType.
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

// AuthorizeType answers "may the effective actor perform operation on
// resources of type typeID?" — the type-level counterpart of Authorize, used
// for create and list of a resource type. typeID is a types.id, never an
// entities.id.
//
// The call is allowed when either holds:
//   - the effective actor (sudo first), or an actor group in its chain, holds a
//     wildcard grant (target_id IS NULL) whose operation is in the SatisfiedBy
//     closure of operation, for example manage, which implies create; or
//   - the actor chain holds a grant, with an operation in that closure, on the
//     exact type's entity (types.entity_id of typeID), directly or on a target
//     group that reaches it walking up through authz_target_group_members
//     (checkTypeGrant).
//
// Anything else returns ErrForbidden, as does typeID <= 0 (fail closed, even
// for a wildcard holder) and a typeID with no types row.
//
// The type-grant arm is exact-type only: there is no subtype or parent-type
// inheritance, so a grant on the entity (or legal_entity) type's entity does
// not cover natural_person. There is no ownership arm either. AuthorizeType
// deliberately never consults entity-level authority: it does not call
// checkGrantOrOwn and never compares typeID with entities.id,
// entities.owner_id, or grants.target_id; typeID is compared only with
// types.id. Doing otherwise would let any actor who owns or holds a grant over
// the unrelated entity whose id equals typeID pass the type-level check.
//
// types.deprecated_at is not consulted, for wildcard or type-grant holders:
// creating an entity of a deprecated type is rejected by the data layer, and a
// deprecated type must stay listable. A genuine DB error propagates rather than
// being swallowed into a denial.
func (a *Authorizer) AuthorizeType(ctx context.Context, operation string, typeID int64) error {
	// Report a missing actor as 401-class even for a malformed typeID, then
	// fail closed on a non-positive type id before any DB work: never "no
	// target, so allow", and not even a wildcard holder passes it.
	if _, ok := effectiveActor(ctx); !ok {
		return ErrUnauthenticated
	}
	if typeID <= 0 {
		return ErrForbidden
	}

	actorEntityID, opIDs, done, err := a.authorizePrelude(ctx, operation)
	if done {
		return err // nil: wildcard grant allows; non-nil: denial or DB error
	}

	// No wildcard grant. Look for a grant on the exact type's entity. A DB
	// error propagates unchanged and is never turned into a denial.
	granted, err := a.checkTypeGrantDispatch(ctx, actorEntityID, typeID, opIDs)
	if err != nil {
		return err
	}
	if granted {
		return nil
	}

	return ErrForbidden
}

// authorizePrelude runs the steps Authorize and AuthorizeType share, so the two
// entry points cannot drift: resolve the effective actor, compute the
// SatisfiedBy closure for operation (with the unknown-slug fallback to a
// wildcard-manage check), and run the wildcard grant check.
//
// When done is true the outcome is final and err is it: nil means a wildcard
// grant allows the call; ErrUnauthenticated, ErrForbidden, or a propagated DB
// error otherwise. When done is false the actor holds no wildcard grant for
// operation; actorEntityID and opIDs are valid for any further, entity-level
// check the caller chooses to make.
func (a *Authorizer) authorizePrelude(ctx context.Context, operation string) (actorEntityID int64, opIDs []int32, done bool, err error) {
	// Resolve effective actor. Assumed actor takes priority over real actor.
	actorEntityID, ok := effectiveActor(ctx)
	if !ok {
		return 0, nil, true, ErrUnauthenticated
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
	opIDs, sErr := a.opReg.SatisfiedBy(operation)
	if sErr != nil {
		// Unknown slug: use "manage" opIDs for the wildcard check.
		// If the actor has a wildcard manage grant, allow. Otherwise deny.
		manageIDs, mErr := a.opReg.SatisfiedBy("manage")
		if mErr != nil {
			// Even "manage" is unknown (uninitialized registry). Deny.
			return 0, nil, true, ErrForbidden
		}
		wildcardAllowed, wErr := a.checkWildcardGrantDispatch(ctx, actorEntityID, manageIDs)
		if wErr != nil {
			return 0, nil, true, wErr
		}
		if wildcardAllowed {
			return 0, nil, true, nil // wildcard manage admin can do anything
		}
		return 0, nil, true, ErrForbidden
	}

	// Wildcard grant check: if the actor (or any actor group they belong to)
	// holds a grant with target_id IS NULL and operation_id in the opIDs closure,
	// allow unconditionally. This replaces the is_admin column short-circuit.
	wildcardAllowed, wErr := a.checkWildcardGrantDispatch(ctx, actorEntityID, opIDs)
	if wErr != nil {
		return 0, nil, true, wErr
	}
	if wildcardAllowed {
		return 0, nil, true, nil
	}

	return actorEntityID, opIDs, false, nil
}

// effectiveActor returns the entity ID that should be used for policy checks.
// It delegates to opctx.EffectiveActorEntityID, which applies the
// sudo-first-then-actor policy (the sudo actor wins when one is set on ctx,
// otherwise the real actor). The helper is retained as a package-local name
// for the concept rather than being inlined at the call site.
func effectiveActor(ctx context.Context) (int64, bool) {
	return opctx.EffectiveActorEntityID(ctx)
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

// checkTypeGrantDispatch calls typeGrantFn if set (test stub), otherwise
// delegates to checkTypeGrant.
func (a *Authorizer) checkTypeGrantDispatch(ctx context.Context, actorEntityID, typeID int64, opIDs []int32) (bool, error) {
	if a.typeGrantFn != nil {
		return a.typeGrantFn(ctx, actorEntityID, typeID, opIDs)
	}
	return a.checkTypeGrant(ctx, actorEntityID, typeID, opIDs)
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
//	        -- instance semantics: the entity of the target's exact type, unless
//	        -- the target is itself a type entity
//	        SELECT t.entity_id
//	        FROM entities e JOIN types t ON t.id = e.fundamental_type_id
//	        WHERE e.id = targetEntityID
//	          AND t.entity_id IS NOT NULL
//	          AND NOT EXISTS (SELECT 1 FROM types tt WHERE tt.entity_id = targetEntityID)
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
// Instance semantics: TargetChain is seeded with the target AND the entity of
// the target's exact fundamental type (types.entity_id of
// entities.fundamental_type_id), so a grant on a type's entity, directly or
// through a type-only target group (the group walk starts from the seeded type
// entity), also confers the operation over every instance of exactly that
// type. The type-entity seed is skipped when the target is itself a type
// entity: its fundamental type is the sentinel "type", so without the guard a
// grant on the sentinel's own entity would confer the operation over every type
// entity, and grant or manage there would let its holder grant authority over
// every type. A target id with no entities row seeds nothing and fails closed.
// There is no parent or child type walk, and AuthorizeType is unaffected.
//
// WARNING: this makes a type grant instance-wide. It confers the operation
// over every existing and future instance of exactly that type, for every
// operation in the SatisfiedBy closure: manage on a type entity is full control
// of every instance (read, update, delete, assume, grant, revoke, ...), which
// for natural_person includes assume of every user account and installing SSH
// keys through the operator routes. list implies read, so a type-level list
// grant also reads every instance. manage or grant on a type-only target group
// confers all of this for every type in the group. It does not extend to
// subtypes or parent types, and never applies to type entities themselves.
// Escalation paths: manage, grant or update on the type entity of
// authz_actor_group or authz_target_group confers that operation over every
// group of that kind. Adding a member needs grant on the member (and, for actor
// groups, update and grant on the group); a bare type-level update only permits
// removing members, and manage confers all of it. That is a path to groups that
// hold wildcard grants and so to privilege escalation. The same applies to every
// other type that has instances (app, system_actor and the anonymous system
// actor, an instance of system_actor, where they exist). The assume of every
// user account that manage on the natural_person type entity confers includes
// accounts that themselves hold wildcard or admin grants, so that grant is
// effectively full admin.
// A holder of grant on a type entity can hand out instance-wide authority over
// that type, so grant on type entities only to principals trusted with every
// instance. Until mod-core's matching GrantTableGenerator arm lands, this
// single-row check and the list-side accessible_*_ids_for_actor functions
// disagree for type-grant holders (single-row allows, list omits).
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
        SELECT t.entity_id
        FROM entities e
        JOIN types t ON t.id = e.fundamental_type_id
        WHERE e.id = $2::bigint
          AND t.entity_id IS NOT NULL
          AND NOT EXISTS (SELECT 1 FROM types tt WHERE tt.entity_id = $2::bigint)
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

// checkTypeGrant reports whether the actor chain holds a grant, with an
// operation in the opIDs closure, on the entity of the exact type typeID,
// directly or on a target group reached by walking UP from that entity:
//
//	WITH RECURSIVE
//	    ActorChain AS (
//	        SELECT actorEntityID AS aid
//	        UNION
//	        SELECT agm.group_id FROM authz_actor_group_members agm JOIN ActorChain ac ON agm.member_id = ac.aid
//	    ),
//	    TargetChain AS (
//	        SELECT t.entity_id AS tid FROM types t WHERE t.id = typeID AND t.entity_id IS NOT NULL
//	        UNION
//	        SELECT atgm.group_id FROM authz_target_group_members atgm JOIN TargetChain tc ON atgm.member_id = tc.tid
//	    )
//	SELECT EXISTS (
//	    SELECT 1 FROM grants g
//	    JOIN ActorChain ac ON g.actor_id = ac.aid
//	    JOIN TargetChain tc ON g.target_id = tc.tid
//	    WHERE g.operation_id = ANY(opIDs)
//	)
//
// Properties:
//   - typeID is compared only with types.id. It never reaches grants.target_id,
//     entities.id or entities.owner_id, so a grant on an unrelated entity whose
//     id equals typeID cannot satisfy the check.
//   - The chain is seeded from the exact type only: there is no walk to parent types,
//     so a grant on a supertype's entity does not cover a subtype.
//   - There is no ownership arm; the query never reads entities.owner_id.
//   - An unknown typeID gives an empty TargetChain and therefore false (fail
//     closed). The entity_id IS NOT NULL guard is defensive.
//   - types.deprecated_at is deliberately not consulted.
//
// The wildcard grant is checked before this arm (authorizePrelude), so this
// query does not repeat it.
func (a *Authorizer) checkTypeGrant(ctx context.Context, actorEntityID, typeID int64, opIDs []int32) (bool, error) {
	const typeGrantCheckSQL = `
WITH RECURSIVE
    ActorChain AS (
        SELECT $1::bigint AS aid
        UNION
        SELECT agm.group_id
        FROM authz_actor_group_members agm
        JOIN ActorChain ac ON agm.member_id = ac.aid
    ),
    TargetChain AS (
        SELECT t.entity_id AS tid
        FROM types t
        WHERE t.id = $2::bigint
          AND t.entity_id IS NOT NULL
        UNION
        SELECT atgm.group_id
        FROM authz_target_group_members atgm
        JOIN TargetChain tc ON atgm.member_id = tc.tid
    )
SELECT EXISTS (
    SELECT 1 FROM grants g
    JOIN ActorChain ac ON g.actor_id = ac.aid
    JOIN TargetChain tc ON g.target_id = tc.tid
    WHERE g.operation_id = ANY($3::int[])
)`

	var exists bool
	err := a.pool.QueryRow(ctx, typeGrantCheckSQL, actorEntityID, typeID, opIDs).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}
