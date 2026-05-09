package authz

// This file exports internal fields and helpers for use by the external test
// package (package authz_test). It is compiled only during testing.

import (
	"context"

	authzapi "github.com/moduleforge/authz-api/authz"
)

// SetWildcardGrantFn sets the wildcard-grant override function on the Authorizer.
// Used by unit tests to inject a wildcard grant outcome without a live Postgres.
func (a *Authorizer) SetWildcardGrantFn(fn func(ctx context.Context, actorEntityID int64, opIDs []int32) (bool, error)) {
	a.wildcardGrantFn = fn
}

// stubOpReg is a minimal OperationRegistry that accepts any operation slug and
// returns a slice containing a dummy op ID. Used by unit tests that need to
// exercise the Authorize flow without a real authz_operations table.
type stubOpReg struct{}

func (stubOpReg) SatisfiedBy(op string) ([]int32, error) {
	// Return a dummy op ID for any known operation.
	// Return an error for "unknown_op" to test the unknown-operation denial path.
	if op == "unknown_op" {
		return nil, errUnknownOp
	}
	return []int32{1}, nil
}

// errUnknownOp is a sentinel error returned by stubOpReg for unknown operations.
var errUnknownOp = ErrForbidden

// opRegAdapter adapts stubOpReg to the *authzapi.OperationRegistry interface
// that Authorizer.opReg expects. Since OperationRegistry is a concrete struct
// (not an interface), we embed the functionality via wildcardGrantFn.
//
// The Authorizer calls opReg.SatisfiedBy(operation) directly. We cannot inject
// a stub opReg without changing the field type to an interface. Instead, we use
// a workaround: set opReg to nil and override the behaviour via a wrapper.
//
// NewWithStubOpReg returns an Authorizer where:
//   - opReg is a real (zero-operations) registry, BUT
//   - wildcardGrantFn captures the caller's stub AND handles the opReg step.
//
// This works because the stubbed wildcardGrantFn is called after opReg.SatisfiedBy.
// We can't intercept SatisfiedBy directly, but we can use a real (empty) registry
// and rely on the fact that our unit tests don't exercise paths that require
// specific op IDs — they only check allow/deny at the Authorize level.

// NewWithStubOpReg builds an Authorizer suitable for unit tests.
// It uses a real OperationRegistry loaded from in-memory stubs, and sets
// wildcardGrantFn to the given function. Pool is nil; tests that reach
// checkGrant or checkTagOwnership will panic (those paths require a DB).
//
// The stub OperationRegistry contains the standard seed operations so that
// SatisfiedBy("read"), SatisfiedBy("manage"), etc. all return non-error results.
func NewWithStubOpReg(wildcardFn func(ctx context.Context, actor int64, opIDs []int32) (bool, error)) *Authorizer {
	// Build an OperationRegistry from a hand-rolled slice of operations.
	// NewOperationRegistry requires a Querier; use a stub via the registry's
	// exported constructor.
	opReg := buildStubOpReg()
	az := &Authorizer{
		opReg:           opReg,
		wildcardGrantFn: wildcardFn,
	}
	return az
}

// buildStubOpReg builds an *authzapi.OperationRegistry for unit tests using
// the standard seed operations. This avoids needing a live DB in unit tests.
func buildStubOpReg() *authzapi.OperationRegistry {
	reg, err := authzapi.NewRegistryFromSeed(standardOps)
	if err != nil {
		panic("buildStubOpReg: " + err.Error())
	}
	return reg
}

// standardOps mirrors the seed data from 0500_authz_operations.sql.
// IDs must match the seeded values so that SatisfiedBy closures are correct.
var standardOps = []authzapi.SeedOperation{
	{ID: 1, Slug: "read", Implies: nil},
	{ID: 2, Slug: "sread", Implies: []int32{1}},
	{ID: 3, Slug: "list", Implies: []int32{1}},
	{ID: 4, Slug: "update", Implies: []int32{1}},
	{ID: 5, Slug: "delete", Implies: []int32{1}},
	{ID: 6, Slug: "swrite", Implies: []int32{2, 4}},
	{ID: 7, Slug: "manage", Implies: []int32{1, 2, 3, 4, 5, 6, 8, 9, 10, 11}},
	{ID: 8, Slug: "assume", Implies: nil},
	{ID: 9, Slug: "login", Implies: nil},
	{ID: 10, Slug: "grant", Implies: nil},
	{ID: 11, Slug: "revoke", Implies: nil},
}
