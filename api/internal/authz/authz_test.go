package authz_test

import (
	"context"
	"errors"
	"testing"

	authzapi "github.com/moduleforge/authz-api/authz"
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
// The Authorizer's pool and opReg are nil — tests that need a real
// OperationRegistry (e.g. to exercise the checkGrantOrOwn path with an
// operation slug like "read" or "grant") should use NewWithStubOpReg
// instead, pairing it with SetGrantOrOwnFn to inject an outcome without a
// live Postgres. This helper is only for paths that need neither, using
// wildcardGrantFn to inject outcomes.
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

// grantOrOwnAllowFn is a grantOrOwnFn that always reports the combined
// grant-or-own check satisfied. Used to simulate an actor who owns the
// target entity (entities.owner_id = actor) with no grant present.
func grantOrOwnAllowFn(_ context.Context, _, _ int64, _ []int32) (bool, error) {
	return true, nil
}

// grantOrOwnDenyFn is a grantOrOwnFn that always reports the combined
// grant-or-own check unsatisfied. Used both for a plain non-owner/no-grant
// actor and for a NULL-owner target: real e.owner_id = $1 evaluates to NULL
// (not true) when owner_id IS NULL, which is exactly "no ownership" as far
// as this seam's boolean result is concerned.
func grantOrOwnDenyFn(_ context.Context, _, _ int64, _ []int32) (bool, error) {
	return false, nil
}

// grantOrOwnErrFn is a grantOrOwnFn that returns an error (DB fault), used
// to verify the error propagates instead of being swallowed into a denial.
func grantOrOwnErrFn(wantErr error) func(context.Context, int64, int64, []int32) (bool, error) {
	return func(_ context.Context, _, _ int64, _ []int32) (bool, error) {
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

// TestAuthorize_OwnEntity_Allowed verifies that an actor who owns the target
// entity (entities.owner_id = actor, reported here via the grantOrOwnFn
// seam) is allowed, for every operation on that entity — not just reads.
// This proves the own predicate is operation-agnostic: read, update, and
// delete are CRUD verbs; "grant" is a non-CRUD verb included specifically to
// prove the predicate is not gated on the operation or on opIDs.
func TestAuthorize_OwnEntity_Allowed(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardDenyFn)
	az.SetGrantOrOwnFn(grantOrOwnAllowFn)

	ctx := ctxWithActor(1)

	for _, op := range []string{"read", "update", "delete", "grant"} {
		t.Run(op, func(t *testing.T) {
			if err := az.Authorize(ctx, op, ptr(int64(99))); err != nil {
				t.Errorf("owner should be allowed for operation=%q: got %v", op, err)
			}
		})
	}
}

// TestAuthorize_NonOwner_NoGrant_Denied verifies that an actor with no grant
// and no ownership of the target (grantOrOwnFn reports false) is denied.
func TestAuthorize_NonOwner_NoGrant_Denied(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardDenyFn)
	az.SetGrantOrOwnFn(grantOrOwnDenyFn)

	ctx := ctxWithActor(1)
	err := az.Authorize(ctx, "read", ptr(int64(99)))
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("expected ErrForbidden for non-owner with no grant, got: %v", err)
	}
}

// TestAuthorize_NullOwnerTarget_Denied verifies that a target entity with a
// NULL owner_id (corporation, authz_actor_group, authz_target_group by
// design) is never matched by the own predicate: owner_id IS NULL makes
// e.owner_id = $1 evaluate to NULL rather than true, so the real query
// reports "not owned" exactly as grantOrOwnDenyFn does here — this test
// documents that specific NULL-owner scenario (Requirement 3) even though it
// exercises the same Authorize-level denial path as
// TestAuthorize_NonOwner_NoGrant_Denied.
func TestAuthorize_NullOwnerTarget_Denied(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardDenyFn)
	az.SetGrantOrOwnFn(grantOrOwnDenyFn)

	ctx := ctxWithActor(1)
	err := az.Authorize(ctx, "read", ptr(int64(100)))
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("expected ErrForbidden for NULL-owner target, got: %v", err)
	}
}

// TestAuthorize_GrantOrOwnDBError verifies that a genuine DB error from the
// combined grant-or-own check propagates to the caller rather than being
// silently mapped to ErrForbidden (the old checkTagOwnership swallow-and-
// deny behavior this task removes).
func TestAuthorize_GrantOrOwnDBError(t *testing.T) {
	dbErr := errors.New("pool connection lost")
	az := authz.NewWithStubOpReg(wildcardDenyFn)
	az.SetGrantOrOwnFn(grantOrOwnErrFn(dbErr))

	ctx := ctxWithActor(1)
	err := az.Authorize(ctx, "read", ptr(int64(99)))
	if !errors.Is(err, dbErr) {
		t.Errorf("expected DB error to propagate, got: %v", err)
	}
}

// TestStandardOpRegistry_Create_Registered is a registry-level regression test
// for the "create" operation registered by mod-authz migration
// 0506_authz_create_operation.sql. It asserts that standardOps (the
// hand-maintained mirror of the migration's seed data) registers "create" —
// SatisfiedBy("create") must not return authz.ErrUnknownOperation — and that
// the returned closure includes both create's own ID and manage's ID (7),
// which is the piece a missing manage-implies update would break.
func TestStandardOpRegistry_Create_Registered(t *testing.T) {
	reg := authz.StandardOpRegistry()

	ids, err := reg.SatisfiedBy("create")
	if errors.Is(err, authzapi.ErrUnknownOperation) {
		t.Fatalf(`SatisfiedBy("create") returned ErrUnknownOperation: %v`, err)
	}
	if err != nil {
		t.Fatalf(`SatisfiedBy("create") returned unexpected error: %v`, err)
	}

	createID, err := reg.IDForSlug("create")
	if err != nil {
		t.Fatalf(`IDForSlug("create") returned error: %v`, err)
	}
	manageID, err := reg.IDForSlug("manage")
	if err != nil {
		t.Fatalf(`IDForSlug("manage") returned error: %v`, err)
	}
	if manageID != 7 {
		t.Fatalf("expected manage's ID to be 7, got %d", manageID)
	}

	got := make(map[int32]bool, len(ids))
	for _, id := range ids {
		got[id] = true
	}
	if !got[createID] {
		t.Errorf(`SatisfiedBy("create") = %v; missing create's own ID %d`, ids, createID)
	}
	if !got[manageID] {
		t.Errorf(`SatisfiedBy("create") = %v; missing manage's ID %d (manage-implies-create update)`, ids, manageID)
	}
}

// TestAuthorize_WildcardAdmin_Create_InspectsOpIDs is an authorizer-level
// regression test that, unlike TestAuthorize_WildcardAdmin_AllowsAnything's
// "create nil-target" case, actually inspects opIDs in its wildcard stub: it
// returns true only when opIDs contains manage's ID (7). This is the shape
// needed to catch a missing manage-implies-create update — a stub that
// ignores opIDs (like wildcardAllowFn) would pass even if manage's implies
// never included create's ID, silently masking the regression task 001's
// migration fixes.
func TestAuthorize_WildcardAdmin_Create_InspectsOpIDs(t *testing.T) {
	const manageID = int32(7)

	wildcardGrantFn := func(_ context.Context, _ int64, opIDs []int32) (bool, error) {
		for _, id := range opIDs {
			if id == manageID {
				return true, nil
			}
		}
		return false, nil
	}

	az := authz.NewWithStubOpReg(wildcardGrantFn)
	ctx := ctxWithActor(1)

	if err := az.Authorize(ctx, "create", nil); err != nil {
		t.Errorf(`expected wildcard-manage actor to be allowed "create", got: %v`, err)
	}
}
