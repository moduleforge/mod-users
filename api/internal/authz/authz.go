// Package authz provides the users-module Authorizer: the production
// authorization engine of the identity model, over mod-authz's grants table.
//
// Actor. The effective actor is the acting profile, opctx.Auth(ctx).ProfileID:
// the profile the authentication middleware installed on the AuthContext (under
// impersonation, the impersonated profile, so the target's own permissions
// apply and never the administrator's). A context without an AuthContext, or
// with no profile, is ErrUnauthenticated. Liveness and the acting profile's own
// availability (confinement, its party's policy) are checked once per request
// by the authentication middleware; the engine trusts the installed
// AuthContext for those.
//
// Prelude. Every entry point first runs, in order:
//
//  1. The operation-level assurance gate, for contexts that carry a SessionID
//     or a CredentialID: the context's ACR must reach the operation's MinACR
//     and, when the operation sets MaxAuthAge, the authentication must be at
//     most that old. A failure is the shared action-required error
//     ErrStepUpRequired (409 users.step_up_required, rendered by
//     apiresp.WriteError wherever it surfaces), never a plain 403. Trusted
//     system-actor contexts (no session, no credential) and the anonymous
//     context skip it; an impersonating context always carries a session and
//     never does.
//  2. The owning-party gate, when the check names an owner: the target's
//     entities.owner_id for Authorize, ownerID for AuthorizeTypeIn. A party
//     is its own owning party (decision F): when Authorize's target is itself
//     a legal_entity, the target is a gate party too, alongside its
//     owner_id, so Authorize(update, &party) is gated by that party's own
//     policy even when the party has no owner.
//     Nested ownership is one level only: the gate checks the target (when it
//     is a party) and its direct owner, never the owner's owner or any
//     further ancestor, so content owned by a party that is itself owned by
//     another party is not gated by the outer party's policy. When the acting
//     profile is outside a gate party (an outside collaborator reaching the
//     party's content through actor groups or grants, or a personal profile
//     through the SSH fallback), the session's factors (or the single
//     credential's) must meet that party's credential_rule and min_acr,
//     with mod_users.session_meets_party_policy. An impersonation session
//     meets it with the administrator's parent-session factors and acr; only
//     an impersonation started through an active operator profile (the
//     session's recorded impersonator_profile_id) skips the credential rule
//     (decisions G as resolved, and J); for any other impersonation, a
//     parent factor confined to a party other than the impersonated
//     profile's fails the gate (decision L). A profile_policy_confirmations
//     row exempts the credential rule only. A failure is ErrForbidden, like a
//     failed grant arm. Acting profiles inside the owning party, personal
//     profiles reaching their own records, platform operator profiles
//     (core_is_operator_profile; decision D: a customer party's policy never
//     binds the operator, while the operation-level gate still applies),
//     system-actor contexts and the anonymous context skip it. It costs one query per checked target.
//  3. The wildcard arm: the actor chain (the profile and the actor groups it
//     belongs to, transitively) holds a grant with scope 'entity' and
//     target_id NULL for an operation in the requested operation's
//     satisfied-by closure. It allows every target, and nil targets.
//
// An unknown operation slug falls back to manage: the prelude gates it with
// manage's policy and only a wildcard manage grant allows it.
//
// Authorize(op, &target) then admits the target through any of:
//
//   - Entity arm: an entity-scope grant on the target, directly or through
//     target groups. Instance semantics: a grant on the entity of the target's
//     exact type (types.entity_id, directly or through a type-only target
//     group) confers the operation over every instance of exactly that type,
//     unless the target is itself a type entity.
//   - Owned-scope arm: an owned-scope grant with target_id = the target's
//     owner whose target_type_id is NULL, equals the target's type, or, with
//     include_subtypes, is an ancestor of it. It never reaches the owner's own
//     record.
//   - Own arm: the target's owner is profile_own_subject(actor) (the holder
//     user of a personal profile; NULL for every other actor) and an
//     operation in the closure has authz_operations.ownership_confers.
//     impersonate, manage and login never confer.
//
// These are the same arms as mod-core's generated accessible_*_ids_for_actor
// functions, so single-row and list checks agree by construction. A nil target
// is allowed only by the wildcard arm.
//
// AuthorizeType(op, typeID) answers type-level create and list: the wildcard
// arm, or an entity-scope grant on the exact type's entity. No ownership, no
// owned scope, no subtype or parent-type walk; an instance grant never
// satisfies it.
//
// AuthorizeTypeIn(op, typeID, ownerID) answers create and list within an
// owner: the prelude (with ownerID as the owning party), the wildcard arm, a
// type-entity grant on the exact type, an owned-scope grant with target_id =
// ownerID and the type match, then the own arm when ownerID =
// profile_own_subject(actor) and the operation confers.
//
// Ids: Authorize's target is always an entities.id, never a types.id; typeID
// is always a types.id. Passing a types.id to Authorize is a caller bug the
// engine cannot detect (both are int64): it would be matched against
// unrelated entities. Type-level checks go through AuthorizeType or
// AuthorizeTypeIn (in practice mod-core's authz.AuthorizeType and
// authz.AuthorizeTypeIn helpers).
//
// WARNING, operator-actor trust for type-entity grants. mod-authz accepts
// wildcard and type-entity grants only for operator actors (profiles of the
// operator party, with allow_platform_grants), and every such grant is
// instance-wide: it confers its operation over every existing and future
// instance of exactly that type, for every operation in its closure. manage
// on a type entity is full control of every instance, including the
// operations manage implies:
//
//   - manage on the profile type entity confers impersonate of every profile,
//     operator administrators holding wildcard grants included, so it is
//     effectively full administration.
//   - manage on the user or service_account type entity confers credentials
//     over every principal of that type (passwords, SSH keys, API keys), which
//     is account takeover. update on those type entities confers neither
//     impersonate nor credentials.
//   - manage, grant or update on the type entity of authz_actor_group or
//     authz_target_group confers that operation over every group of the kind,
//     a path to groups that hold wildcard grants. Adding a member needs grant
//     on the member (and, for actor groups, update and grant on the group); a
//     bare type-level update only permits removing members.
//   - grant on a type entity lets its holder hand out instance-wide authority
//     over that type.
//
// Give type-entity grants only to operator profiles trusted with every
// instance of the type. list implies read, so a type-level list grant reads
// every instance.
package authz

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	authzdb "github.com/moduleforge/authz-model/db"
	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/internal/useraction"

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

// ErrStepUpRequired is the assurance gate's refusal: the session or credential
// is below the operation's minimum assurance level, or its authentication is
// older than the operation's maximum age. It is the shared apiresp
// action-required error (409 users.step_up_required), so apiresp.WriteError
// renders it as an action wherever it surfaces, including inside another
// module's service. The engine returns it wrapped; match it with errors.Is.
var ErrStepUpRequired = apiresp.ActionRequired(useraction.StepUpRequired,
	"Confirm your identity to continue.", "/step-up", nil)

// Compile-time assertions: Authorizer implements core's Authorizer and both
// optional capabilities.
var (
	_ coreAuthz.Authorizer                = (*Authorizer)(nil)
	_ coreAuthz.TypeAuthorizer            = (*Authorizer)(nil)
	_ coreAuthz.OwnerScopedTypeAuthorizer = (*Authorizer)(nil)
)

// Authorizer is the users-module implementation of core's authz.Authorizer.
// It is constructed once at the composition root (main.go) and injected into
// all service constructors via coreservice.New.
type Authorizer struct {
	authzQ authzdb.Querier
	opReg  *authzapi.OperationRegistry
	pool   *pgxpool.Pool
	now    func() time.Time

	// The fields below replace one database check each, so unit tests can
	// drive the engine without a live Postgres. nil means the real check.
	// Only set them in tests (export_test.go).
	wildcardGrantFn func(ctx context.Context, actorEntityID int64, opIDs []int32) (bool, error)
	grantOrOwnFn    func(ctx context.Context, actorEntityID, targetEntityID int64, opIDs []int32) (bool, error)
	typeGrantFn     func(ctx context.Context, actorEntityID, typeID int64, opIDs []int32) (bool, error)
	ownedScopeFn    func(ctx context.Context, actorEntityID, typeID, ownerID int64, opIDs []int32) (bool, error)
	partyPolicyFn   func(ctx context.Context, q partyPolicyQuery) (bool, error)
}

// partyPolicyQuery is the input of the owning-party gate. Exactly one of
// TargetEntityID (Authorize: the gate parties are entities.owner_id and, when
// it is a legal_entity, the target itself) and OwnerID (AuthorizeTypeIn) is
// non-zero.
type partyPolicyQuery struct {
	ProfileID      int64
	SessionID      int64
	CredentialID   int64
	TargetEntityID int64
	OwnerID        int64
}

// ownerScope names the owner the owning-party gate resolves for one check:
// the owner of targetEntityID, or ownerID itself. The zero value names none.
type ownerScope struct {
	targetEntityID int64
	ownerID        int64
}

func (s ownerScope) isZero() bool { return s.targetEntityID <= 0 && s.ownerID <= 0 }

// New constructs an Authorizer.
//
//   - authzQ is the mod-authz querier.
//   - opReg provides each operation's satisfied-by closure and policy
//     (MinACR, MaxAuthAge).
//   - pool runs the grant, owned-scope and owning-party checks.
func New(authzQ authzdb.Querier, opReg *authzapi.OperationRegistry, pool *pgxpool.Pool) *Authorizer {
	return &Authorizer{authzQ: authzQ, opReg: opReg, pool: pool, now: time.Now}
}

// Authorize answers "may the effective actor perform operation on the entity
// whose entities.id is *target?" (nil: no specific entity). See the package
// documentation for the prelude and the arms.
//
// The target is ALWAYS an entities.id, never a types.id: a type id here would
// be matched against unrelated entities. Type-level checks use AuthorizeType or
// AuthorizeTypeIn. A nil target is allowed only by the wildcard arm.
func (a *Authorizer) Authorize(ctx context.Context, operation string, target *int64) error {
	var scope ownerScope
	if target != nil {
		scope.targetEntityID = *target
	}
	actorEntityID, opIDs, done, err := a.authorizePrelude(ctx, operation, scope)
	if done {
		return err
	}
	if target == nil {
		return ErrForbidden
	}

	// Entity, owned-scope and own arms in one query. A DB error propagates
	// rather than being swallowed into a denial.
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
// resources of type typeID?" (type-level create and list). typeID is a
// types.id, never an entities.id.
//
// Allowed by the wildcard arm, or by an entity-scope grant, with an operation
// in the closure, on the exact type's entity (types.entity_id of typeID) held
// by the actor chain directly or through target groups (checkTypeGrant).
// There is no ownership arm, no owned-scope arm and no subtype or parent-type
// walk, and typeID is never compared with an entities.id, so an instance grant
// or ownership of the entity whose id equals typeID never satisfies it.
//
// typeID <= 0 is ErrForbidden, even for a wildcard holder. types.deprecated_at
// is not consulted: creating an entity of a deprecated type is rejected by the
// data layer, and a deprecated type stays listable.
func (a *Authorizer) AuthorizeType(ctx context.Context, operation string, typeID int64) error {
	// Report a missing actor as 401-class even for a malformed typeID.
	if _, ok := effectiveActor(ctx); !ok {
		return ErrUnauthenticated
	}
	if typeID <= 0 {
		return ErrForbidden
	}

	actorEntityID, opIDs, done, err := a.authorizePrelude(ctx, operation, ownerScope{})
	if done {
		return err
	}

	granted, err := a.checkTypeGrantDispatch(ctx, actorEntityID, typeID, opIDs)
	if err != nil {
		return err
	}
	if granted {
		return nil
	}
	return ErrForbidden
}

// AuthorizeTypeIn answers "may the effective actor perform operation on
// resources of type typeID owned by ownerID?" (create and list within an
// owner). typeID is a types.id and ownerID an entities.id. Callers use
// mod-core's authz.AuthorizeTypeIn helper rather than this method.
//
// Order: the prelude, with ownerID as the owning party; the wildcard arm; an
// entity-scope grant on the exact type's entity (as AuthorizeType); an
// owned-scope grant with target_id = ownerID whose target_type_id is NULL,
// equals typeID, or, with include_subtypes, is an ancestor of typeID; the own
// arm when ownerID = profile_own_subject(actor) and the operation confers.
// typeID <= 0 or ownerID <= 0 is ErrForbidden.
func (a *Authorizer) AuthorizeTypeIn(ctx context.Context, operation string, typeID int64, ownerID int64) error {
	if _, ok := effectiveActor(ctx); !ok {
		return ErrUnauthenticated
	}
	if typeID <= 0 || ownerID <= 0 {
		return ErrForbidden
	}

	actorEntityID, opIDs, done, err := a.authorizePrelude(ctx, operation, ownerScope{ownerID: ownerID})
	if done {
		return err
	}

	granted, err := a.checkTypeGrantDispatch(ctx, actorEntityID, typeID, opIDs)
	if err != nil {
		return err
	}
	if granted {
		return nil
	}

	granted, err = a.checkOwnedScopeDispatch(ctx, actorEntityID, typeID, ownerID, opIDs)
	if err != nil {
		return err
	}
	if granted {
		return nil
	}
	return ErrForbidden
}

// authorizePrelude runs the steps every entry point shares, so they cannot
// drift: resolve the actor, compute the operation's satisfied-by closure (an
// unknown slug falls back to manage), the assurance gate, the owning-party
// gate when scope names an owner, and the wildcard arm.
//
// When done is true the outcome is final and err is it: nil means a wildcard
// grant allows the call; otherwise ErrUnauthenticated, ErrStepUpRequired
// (wrapped), ErrForbidden or a propagated DB error. When done is false,
// actorEntityID and opIDs are valid for the caller's remaining arms.
func (a *Authorizer) authorizePrelude(ctx context.Context, operation string, scope ownerScope) (actorEntityID int64, opIDs []int32, done bool, err error) {
	ac, ok := opctx.Auth(ctx)
	if !ok || ac.ProfileID <= 0 {
		return 0, nil, true, ErrUnauthenticated
	}
	actorEntityID = ac.ProfileID

	// An unknown slug is a defense-in-depth case (every in-tree caller passes
	// a registered slug): it is authorized as manage, so only a wildcard
	// manage grant allows it, behind manage's policy.
	policySlug := operation
	opIDs, sErr := a.opReg.SatisfiedBy(operation)
	unknown := sErr != nil
	if unknown {
		policySlug = "manage"
		var mErr error
		opIDs, mErr = a.opReg.SatisfiedBy(policySlug)
		if mErr != nil {
			return 0, nil, true, ErrForbidden // uninitialized registry
		}
	}

	if gated(ac) {
		if err := a.assuranceGate(ac, operation, policySlug); err != nil {
			return 0, nil, true, err
		}
		if !scope.isZero() {
			met, err := a.checkPartyPolicyDispatch(ctx, partyPolicyQuery{
				ProfileID:      ac.ProfileID,
				SessionID:      ac.SessionID,
				CredentialID:   ac.CredentialID,
				TargetEntityID: scope.targetEntityID,
				OwnerID:        scope.ownerID,
			})
			if err != nil {
				return 0, nil, true, err
			}
			if !met {
				return 0, nil, true, ErrForbidden
			}
		}
	}

	wildcardAllowed, wErr := a.checkWildcardGrantDispatch(ctx, actorEntityID, opIDs)
	if wErr != nil {
		return 0, nil, true, wErr
	}
	if wildcardAllowed {
		return 0, nil, true, nil
	}
	if unknown {
		return 0, nil, true, ErrForbidden
	}
	return actorEntityID, opIDs, false, nil
}

// gated reports whether the prelude's gates apply to ac: an interactive or
// non-interactive caller always carries a session or a credential; trusted
// system-actor contexts carry neither, and the anonymous context is exempt.
//
// Sessionless trust is inferred from absent fields (SessionID == 0 and
// CredentialID == 0 on a non-anonymous context), pending an explicit mod-core
// system-actor marker. mod-users itself builds such contexts for real users
// (withActingProfile and its callers in internal/service, and the idp_jit
// context in internal/handlers/auth), so none of them may ever reach
// Authorize, AuthorizeType or AuthorizeTypeIn. That is pinned by
// internal/service/sessionless_contexts_test.go; do not weaken the pin
// without replacing it with the marker.
func gated(ac opctx.AuthContext) bool {
	return !ac.Anonymous && (ac.SessionID != 0 || ac.CredentialID != 0)
}

// assuranceGate applies the operation-level assurance policy of policySlug to
// ac: ACR >= MinACR, and the authentication no older than MaxAuthAge when it
// is set. A policy lookup failure (a registry that knows the closure but not
// the policy) fails closed.
func (a *Authorizer) assuranceGate(ac opctx.AuthContext, operation, policySlug string) error {
	policy, err := a.opReg.Policy(policySlug)
	if err != nil {
		return ErrForbidden
	}
	stale := policy.MaxAuthAge > 0 && a.clock().Sub(ac.AuthTime) > policy.MaxAuthAge
	if ac.ACR < policy.MinACR || stale {
		return fmt.Errorf("authz: operation %q needs a stronger or more recent authentication: %w", operation, ErrStepUpRequired)
	}
	return nil
}

func (a *Authorizer) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// effectiveActor returns the acting profile's entity id from the
// AuthContext (opctx.ProfileID). A context without an AuthContext, or with a
// non-positive profile id, has no actor.
func effectiveActor(ctx context.Context) (int64, bool) {
	id, ok := opctx.ProfileID(ctx)
	if !ok || id <= 0 {
		return 0, false
	}
	return id, true
}

func (a *Authorizer) checkWildcardGrantDispatch(ctx context.Context, actorEntityID int64, opIDs []int32) (bool, error) {
	if a.wildcardGrantFn != nil {
		return a.wildcardGrantFn(ctx, actorEntityID, opIDs)
	}
	return a.checkWildcardGrant(ctx, actorEntityID, opIDs)
}

func (a *Authorizer) checkGrantOrOwnDispatch(ctx context.Context, actorEntityID, targetEntityID int64, opIDs []int32) (bool, error) {
	if a.grantOrOwnFn != nil {
		return a.grantOrOwnFn(ctx, actorEntityID, targetEntityID, opIDs)
	}
	return a.checkGrantOrOwn(ctx, actorEntityID, targetEntityID, opIDs)
}

func (a *Authorizer) checkTypeGrantDispatch(ctx context.Context, actorEntityID, typeID int64, opIDs []int32) (bool, error) {
	if a.typeGrantFn != nil {
		return a.typeGrantFn(ctx, actorEntityID, typeID, opIDs)
	}
	return a.checkTypeGrant(ctx, actorEntityID, typeID, opIDs)
}

func (a *Authorizer) checkOwnedScopeDispatch(ctx context.Context, actorEntityID, typeID, ownerID int64, opIDs []int32) (bool, error) {
	if a.ownedScopeFn != nil {
		return a.ownedScopeFn(ctx, actorEntityID, typeID, ownerID, opIDs)
	}
	return a.checkOwnedScope(ctx, actorEntityID, typeID, ownerID, opIDs)
}

func (a *Authorizer) checkPartyPolicyDispatch(ctx context.Context, q partyPolicyQuery) (bool, error) {
	if a.partyPolicyFn != nil {
		return a.partyPolicyFn(ctx, q)
	}
	return a.checkPartyPolicy(ctx, q)
}

// actorChainCTE walks UP from the acting profile ($1) through actor groups.
const actorChainCTE = `
    ActorChain AS (
        SELECT $1::bigint AS aid
        UNION
        SELECT agm.group_id
        FROM authz_actor_group_members agm
        JOIN ActorChain ac ON agm.member_id = ac.aid
    )`

// checkWildcardGrant reports whether the actor chain holds a wildcard grant:
// scope 'entity', target_id NULL, operation in opIDs.
func (a *Authorizer) checkWildcardGrant(ctx context.Context, actorEntityID int64, opIDs []int32) (bool, error) {
	const wildcardCheckSQL = `
WITH RECURSIVE` + actorChainCTE + `
SELECT EXISTS(
    SELECT 1 FROM grants g
    JOIN ActorChain ac ON g.actor_id = ac.aid
    WHERE g.operation_id = ANY($2::int[])
      AND g.scope = 'entity'
      AND g.target_id IS NULL
)`

	var exists bool
	if err := a.pool.QueryRow(ctx, wildcardCheckSQL, actorEntityID, opIDs).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// checkGrantOrOwn runs Authorize's entity, owned-scope and own arms for the
// target $2 in one query:
//
//   - Entity arm: an entity-scope grant held by the actor chain on a member
//     of TargetChain. TargetChain is seeded with the target and, unless the
//     target is itself a type entity, the entity of its exact fundamental
//     type (instance semantics), then walks UP through target groups. The
//     type-entity guard keeps a grant on the sentinel "type" entity from
//     conferring authority over every type entity. A target id with no
//     entities row seeds nothing and fails closed.
//   - Owned-scope arm: an owned-scope grant held by the actor chain with
//     target_id = the target's owner_id and target_type_id NULL, equal to the
//     target's fundamental type, or, with include_subtypes, a type the target's
//     type descends from (resolved by slug through type_is_or_descends_from,
//     as mod-core's generated functions do). e.id <> e.owner_id keeps it off
//     the owner's own record (mod-core D7), including a self-owned user.
//     Owned-scope grants do not walk target groups.
//   - Own arm: target.owner_id = public.profile_own_subject(actor) and
//     EXISTS (SELECT 1 FROM public.authz_operations o WHERE o.id = ANY(op_ids)
//     AND o.ownership_confers), the predicate of mod-core's generated
//     accessible_*_ids_for_actor functions. The closure-wide test equals "the
//     requested operation confers" by mod-authz's implication invariant.
//
// A NULL owner_id or a NULL own subject makes the owner comparisons NULL, not
// true, so such targets and actors match neither ownership arm.
func (a *Authorizer) checkGrantOrOwn(ctx context.Context, actorEntityID, targetEntityID int64, opIDs []int32) (bool, error) {
	const grantOrOwnCheckSQL = `
WITH RECURSIVE` + actorChainCTE + `,
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
          AND g.scope = 'entity'
    )
    OR EXISTS (
        SELECT 1 FROM entities e
        JOIN grants g ON g.target_id = e.owner_id
        JOIN ActorChain ac ON g.actor_id = ac.aid
        WHERE e.id = $2::bigint
          AND e.id <> e.owner_id
          AND g.scope = 'owned'
          AND g.operation_id = ANY($3::int[])
          AND (g.target_type_id IS NULL
            OR e.fundamental_type_id = g.target_type_id
            OR (g.include_subtypes AND EXISTS (
              SELECT 1 FROM types gt
              WHERE gt.id = g.target_type_id
                AND type_is_or_descends_from(e.fundamental_type_id, gt.slug))))
    )
    OR EXISTS (
        SELECT 1 FROM entities e
        WHERE e.id = $2::bigint
          AND e.owner_id = public.profile_own_subject($1::bigint)
          AND EXISTS (SELECT 1 FROM public.authz_operations o WHERE o.id = ANY($3::int[]) AND o.ownership_confers)
    )`

	var exists bool
	if err := a.pool.QueryRow(ctx, grantOrOwnCheckSQL, actorEntityID, targetEntityID, opIDs).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// checkTypeGrant reports whether the actor chain holds an entity-scope grant,
// with an operation in opIDs, on the entity of the exact type typeID, directly
// or on a target group reached by walking UP from that entity. typeID is
// compared only with types.id; there is no parent-type walk and no ownership
// arm. An unknown typeID gives an empty TargetChain and fails closed.
func (a *Authorizer) checkTypeGrant(ctx context.Context, actorEntityID, typeID int64, opIDs []int32) (bool, error) {
	const typeGrantCheckSQL = `
WITH RECURSIVE` + actorChainCTE + `,
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
      AND g.scope = 'entity'
)`

	var exists bool
	if err := a.pool.QueryRow(ctx, typeGrantCheckSQL, actorEntityID, typeID, opIDs).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// checkOwnedScope runs AuthorizeTypeIn's owner arms for resources of type $2
// owned by $3 in one query:
//
//   - Owned-scope arm: an owned-scope grant held by the actor chain with
//     target_id = ownerID and target_type_id NULL, equal to typeID, or, with
//     include_subtypes, a type typeID descends from.
//   - Own arm: ownerID = public.profile_own_subject(actor) and an operation
//     in the closure has ownership_confers (the same predicate as Authorize's
//     own arm).
func (a *Authorizer) checkOwnedScope(ctx context.Context, actorEntityID, typeID, ownerID int64, opIDs []int32) (bool, error) {
	const ownedScopeCheckSQL = `
WITH RECURSIVE` + actorChainCTE + `
SELECT
    EXISTS (
        SELECT 1 FROM grants g
        JOIN ActorChain ac ON g.actor_id = ac.aid
        WHERE g.scope = 'owned'
          AND g.target_id = $3::bigint
          AND g.operation_id = ANY($4::int[])
          AND (g.target_type_id IS NULL
            OR g.target_type_id = $2::bigint
            OR (g.include_subtypes AND EXISTS (
              SELECT 1 FROM types gt
              WHERE gt.id = g.target_type_id
                AND type_is_or_descends_from($2::bigint, gt.slug))))
    )
    OR (
        $3::bigint = public.profile_own_subject($1::bigint)
        AND EXISTS (SELECT 1 FROM public.authz_operations o WHERE o.id = ANY($4::int[]) AND o.ownership_confers)
    ) IS TRUE`

	var exists bool
	if err := a.pool.QueryRow(ctx, ownedScopeCheckSQL, actorEntityID, typeID, ownerID, opIDs).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// checkPartyPolicy runs the owning-party gate:
// mod_users.session_meets_party_policy over the context's session (or
// credential) and the acting profile, for each gate party of the check:
//
//   - AuthorizeTypeIn: q.OwnerID.
//   - Authorize: the owner_id of q.TargetEntityID (NULL for a target with no
//     owner or no entities row, which the function passes) and, when the
//     target is itself a legal_entity (a party), the target too (decision F:
//     a party is its own owning party, so its own record is gated by its own
//     policy, even when it has no owner). Both must be met: the owner's gate
//     is kept so a party-owned legal_entity, such as a managed user, stays
//     gated by the managing party. One level only, with no ancestor walk.
//
// Zero session and credential ids are passed as NULL.
func (a *Authorizer) checkPartyPolicy(ctx context.Context, q partyPolicyQuery) (bool, error) {
	const partyPolicySQL = `
SELECT CASE
  WHEN $5::bigint > 0 THEN
    mod_users.session_meets_party_policy(NULLIF($1::bigint, 0), NULLIF($2::bigint, 0), $3::bigint, $5::bigint)
  ELSE
    mod_users.session_meets_party_policy(NULLIF($1::bigint, 0), NULLIF($2::bigint, 0), $3::bigint,
      (SELECT e.owner_id FROM entities e WHERE e.id = $4::bigint))
    AND (NOT EXISTS (SELECT 1 FROM public.legal_entities le WHERE le.entity_id = $4::bigint)
      OR mod_users.session_meets_party_policy(NULLIF($1::bigint, 0), NULLIF($2::bigint, 0), $3::bigint, $4::bigint))
END`

	var met bool
	if err := a.pool.QueryRow(ctx, partyPolicySQL, q.SessionID, q.CredentialID, q.ProfileID, q.TargetEntityID, q.OwnerID).Scan(&met); err != nil {
		return false, err
	}
	return met, nil
}
