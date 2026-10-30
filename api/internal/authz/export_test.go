package authz

// This file exports internal fields and helpers for use by the external test
// package (package authz_test). It is compiled only during testing.

import (
	"context"
	"time"

	authzapi "github.com/moduleforge/authz-api/authz"
)

// SetWildcardGrantFn stubs the wildcard arm (an entity-scope grant with no
// target held by the actor chain), so unit tests need no live Postgres.
func (a *Authorizer) SetWildcardGrantFn(fn func(ctx context.Context, actorEntityID int64, opIDs []int32) (bool, error)) {
	a.wildcardGrantFn = fn
}

// SetGrantOrOwnFn stubs Authorize's single-row arms after the prelude: the
// entity arm (with instance semantics), the owned-scope arm on the target's
// owner and the own arm.
func (a *Authorizer) SetGrantOrOwnFn(fn func(ctx context.Context, actorEntityID, targetEntityID int64, opIDs []int32) (bool, error)) {
	a.grantOrOwnFn = fn
}

// SetTypeGrantFn stubs the type-grant arm of AuthorizeType and
// AuthorizeTypeIn (an entity-scope grant on the exact type's entity).
func (a *Authorizer) SetTypeGrantFn(fn func(ctx context.Context, actorEntityID, typeID int64, opIDs []int32) (bool, error)) {
	a.typeGrantFn = fn
}

// SetOwnedScopeFn stubs AuthorizeTypeIn's owner arms: an owned-scope grant
// with target_id = ownerID and a type match, or the own arm (ownerID is the
// actor's own subject and the operation confers).
func (a *Authorizer) SetOwnedScopeFn(fn func(ctx context.Context, actorEntityID, typeID, ownerID int64, opIDs []int32) (bool, error)) {
	a.ownedScopeFn = fn
}

// PartyPolicyQuery is the owning-party gate's input, exported for stubs.
type PartyPolicyQuery = partyPolicyQuery

// SetPartyPolicyFn stubs the owning-party gate
// (mod_users.session_meets_party_policy). It runs for every gated context
// (anything but a system actor, opctx.IsSystemActor, and the anonymous
// context), and only when the check names an owner.
func (a *Authorizer) SetPartyPolicyFn(fn func(ctx context.Context, q PartyPolicyQuery) (bool, error)) {
	a.partyPolicyFn = fn
}

// SetNow replaces the clock the assurance gate measures authentication age
// against.
func (a *Authorizer) SetNow(now func() time.Time) {
	a.now = now
}

// partyPolicyMet is the default owning-party stub of NewWithStubOpReg: every
// session meets every owning party's policy.
func partyPolicyMet(context.Context, partyPolicyQuery) (bool, error) { return true, nil }

// NewWithStubOpReg builds an Authorizer for unit tests: a real
// OperationRegistry built from standardOps (closures and policies), the given
// wildcard stub, and an owning-party stub that always passes (override it
// with SetPartyPolicyFn). The pool is nil, so a test that reaches a database
// arm without its stub panics.
func NewWithStubOpReg(wildcardFn func(ctx context.Context, actor int64, opIDs []int32) (bool, error)) *Authorizer {
	return &Authorizer{
		opReg:           buildStubOpReg(),
		now:             time.Now,
		wildcardGrantFn: wildcardFn,
		partyPolicyFn:   partyPolicyMet,
	}
}

// buildStubOpReg builds an *authzapi.OperationRegistry from standardOps.
func buildStubOpReg() *authzapi.OperationRegistry {
	reg, err := authzapi.NewRegistryFromSeed(standardOps)
	if err != nil {
		panic("buildStubOpReg: " + err.Error())
	}
	return reg
}

// StandardOpRegistry returns the *authzapi.OperationRegistry built from
// standardOps, for use by the external test package (package authz_test).
func StandardOpRegistry() *authzapi.OperationRegistry {
	return buildStubOpReg()
}

// standardOps mirrors the seed of mod-authz's
// model/migrations/0500_authz_operations.sql: ids, implies, ownership_confers,
// min_acr and max_auth_age. IDs must match the seeded values so that
// SatisfiedBy closures are correct.
var standardOps = []authzapi.SeedOperation{
	{ID: 1, Slug: "read", Implies: nil, OwnershipConfers: true, MinACR: 1},
	{ID: 2, Slug: "sread", Implies: []int32{1}, OwnershipConfers: true, MinACR: 1},
	{ID: 3, Slug: "list", Implies: []int32{1}, OwnershipConfers: true, MinACR: 1},
	{ID: 4, Slug: "update", Implies: []int32{1}, OwnershipConfers: true, MinACR: 1},
	{ID: 5, Slug: "delete", Implies: []int32{1}, OwnershipConfers: true, MinACR: 1},
	{ID: 6, Slug: "supdate", Implies: []int32{2, 4}, OwnershipConfers: true, MinACR: 1},
	{ID: 7, Slug: "manage", Implies: []int32{1, 2, 3, 4, 5, 6, 8, 9, 10, 11, 12, 13, 17}, OwnershipConfers: false, MinACR: 1},
	{ID: 8, Slug: "impersonate", Implies: nil, OwnershipConfers: false, MinACR: 1, MaxAuthAge: 10 * time.Minute},
	{ID: 9, Slug: "login", Implies: nil, OwnershipConfers: false, MinACR: 1},
	{ID: 10, Slug: "grant", Implies: nil, OwnershipConfers: true, MinACR: 1},
	{ID: 11, Slug: "revoke", Implies: nil, OwnershipConfers: true, MinACR: 1},
	{ID: 12, Slug: "connect", Implies: nil, OwnershipConfers: true, MinACR: 1},
	{ID: 13, Slug: "create", Implies: nil, OwnershipConfers: true, MinACR: 1},
	{ID: 17, Slug: "credentials", Implies: nil, OwnershipConfers: true, MinACR: 1, MaxAuthAge: 10 * time.Minute},
}
