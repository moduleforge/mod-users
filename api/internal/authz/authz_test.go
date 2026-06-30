package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/internal/authz"
)

// --- helpers ---

func ptr[T any](v T) *T { return &v }

// ctxWithActor returns a context with actor entity ID set.
func ctxWithActor(entityID int64) context.Context {
	return opctx.WithActor(context.Background(), entityID)
}

// ctxWithSudoActor returns a context with both actor and sudo actor set.
func ctxWithSudoActor(actorID, sudoID int64) context.Context {
	ctx := opctx.WithActor(context.Background(), actorID)
	return opctx.WithSudoActor(ctx, sudoID)
}

// newTestAuthorizer builds an Authorizer suitable for unit tests.
//
// The Authorizer's pool and opReg are nil — tests that exercise the database-
// driven paths (checkGrant, checkTagOwnership) require a live Postgres and belong
// in the integration test suite. This helper is only for paths that can be
// exercised without DB access, using wildcardGrantFn to inject outcomes.
//
// wildcardFn: controls what checkWildcardGrant returns. Pass nil to simulate
// "no wildcard grant" (returns false, nil).
func newTestAuthorizer(wildcardFn func(ctx context.Context, actor int64, opIDs []int32) (bool, error)) *authz.Authorizer {
	az := authz.New(nil, nil, nil)
	if wildcardFn != nil {
		az.SetWildcardGrantFn(wildcardFn)
	}
	return az
}

// wildcardAllowFn is a wildcardGrantFn that always returns true (wildcard admin).
func wildcardAllowFn(_ context.Context, _ int64, _ []int32) (bool, error) {
	return true, nil
}

// wildcardDenyFn is a wildcardGrantFn that always returns false (no wildcard grant).
func wildcardDenyFn(_ context.Context, _ int64, _ []int32) (bool, error) {
	return false, nil
}

// wildcardErrFn is a wildcardGrantFn that returns an error (DB fault).
func wildcardErrFn(wantErr error) func(context.Context, int64, []int32) (bool, error) {
	return func(_ context.Context, _ int64, _ []int32) (bool, error) {
		return false, wantErr
	}
}

// --- tests ---

// TestAuthorize_NoActor verifies that an unauthenticated context returns ErrUnauthenticated.
func TestAuthorize_NoActor(t *testing.T) {
	az := newTestAuthorizer(nil)

	err := az.Authorize(context.Background(), "read", ptr(int64(1)))
	if !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got: %v", err)
	}
}

// TestAuthorize_WildcardAdmin_AllowsAnything verifies that a wildcard-manage-grant
// actor can perform any operation, including nil-target operations.
func TestAuthorize_WildcardAdmin_AllowsAnything(t *testing.T) {
	// opReg is nil here; opReg.SatisfiedBy would panic. We need a stub opReg.
	// Use the real opReg or a stub that returns a non-error for any slug.
	// Since opReg is nil, Authorize will return ErrForbidden at opReg.SatisfiedBy.
	// To test wildcard, we need a real or stubbed opReg. Use a stub via the
	// wildcard function that is called *after* opReg.SatisfiedBy.
	//
	// Problem: the flow is:
	//   1. effectiveActor
	//   2. opReg.SatisfiedBy  ← panics if opReg is nil
	//   3. checkWildcardGrant
	//
	// So we cannot test wildcard without opReg. Use a stub opReg.
	az := authz.NewWithStubOpReg(wildcardAllowFn)

	ctx := ctxWithActor(1)

	tests := []struct {
		name      string
		operation string
		target    *int64
	}{
		{"read other", "read", ptr(int64(99))},
		{"create nil-target", "create", nil},
		{"list nil-target", "list", nil},
		{"delete any", "delete", ptr(int64(42))},
		{"update own", "update", ptr(int64(1))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := az.Authorize(ctx, tc.operation, tc.target); err != nil {
				t.Errorf("wildcard admin should be allowed for operation=%q: got %v", tc.operation, err)
			}
		})
	}
}

// TestAuthorize_NonWildcard_NilTargetDenied verifies that an actor with no wildcard
// grant is denied for nil-target operations (create, list, admin-only).
func TestAuthorize_NonWildcard_NilTargetDenied(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardDenyFn)

	ctx := ctxWithActor(7)

	for _, op := range []string{"create", "list", "manage"} {
		t.Run(op, func(t *testing.T) {
			err := az.Authorize(ctx, op, nil)
			if !errors.Is(err, authz.ErrForbidden) {
				t.Errorf("expected ErrForbidden for non-wildcard %q with nil target, got: %v", op, err)
			}
		})
	}
}

// TestAuthorize_WildcardDBError propagates a DB error from the wildcard check.
func TestAuthorize_WildcardDBError(t *testing.T) {
	dbErr := errors.New("pool connection lost")
	az := authz.NewWithStubOpReg(wildcardErrFn(dbErr))

	ctx := ctxWithActor(1)
	err := az.Authorize(ctx, "read", ptr(int64(99)))
	if !errors.Is(err, dbErr) {
		t.Errorf("expected DB error to propagate, got: %v", err)
	}
}

// TestAuthorize_UnknownOperation_NonAdmin verifies that a non-wildcard actor
// with an unknown operation slug is denied.
func TestAuthorize_UnknownOperation_NonAdmin(t *testing.T) {
	// wildcardDenyFn: actor has no wildcard grant. Unknown op → fallback to
	// manage opIDs wildcard check → also denied → ErrForbidden.
	az := authz.NewWithStubOpReg(wildcardDenyFn)
	ctx := ctxWithActor(1)

	err := az.Authorize(ctx, "unknown_op", ptr(int64(1)))
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("non-admin with unknown operation should return ErrForbidden, got: %v", err)
	}
}

// TestAuthorize_UnknownOperation_WildcardAdmin verifies that a wildcard-manage-grant
// actor can perform unknown operation slugs (the manage fallback allows it).
func TestAuthorize_UnknownOperation_WildcardAdmin(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardAllowFn)
	ctx := ctxWithActor(1)

	// Wildcard admin: unknown op → fallback to manage opIDs → wildcard check → allowed.
	err := az.Authorize(ctx, "unknown_op", ptr(int64(1)))
	if err != nil {
		t.Errorf("wildcard admin with unknown operation should be allowed, got: %v", err)
	}
}

// TestAuthorize_SudoActor verifies that when an actor assumes another user's
// identity, the sudo user's permissions apply (not the real actor's).
//
// Scenario: real actor (entity 1) is a wildcard admin. Sudo user (entity 50)
// is NOT a wildcard admin. When real actor assumes entity 50, the effective
// actor is entity 50, so wildcard admin privileges do NOT apply.
func TestAuthorize_SudoActor_WildcardDoesNotEscalate(t *testing.T) {
	// wildcardGrantFn returns true only for actor entity 1 (the real admin),
	// false for entity 50 (the sudo user). Since the effective actor is the
	// sudo user, no wildcard grant should be found.
	az := authz.NewWithStubOpReg(func(_ context.Context, actor int64, _ []int32) (bool, error) {
		return actor == 1, nil // only entity 1 is wildcard admin
	})

	// Real actor (1) assumes sudo user (50).
	ctx := ctxWithSudoActor(1, 50)

	// The effective actor is 50 (sudo user). Sudo user is NOT wildcard admin,
	// so nil-target operations must be denied.
	err := az.Authorize(ctx, "create", nil)
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("sudo non-admin user should be forbidden from create: got %v", err)
	}
}

// TestAuthorize_OwnEntity_Allowed is documented as requiring a live Postgres
// because the own-predicate check (target == actor) runs after checkGrant,
// which requires the pool. This scenario is covered by the integration test suite.
func TestAuthorize_OwnEntity_Allowed(t *testing.T) {
	t.Skip("own-predicate check (target == actor) runs after checkGrant which requires a live pool; covered by integration tests")
}
