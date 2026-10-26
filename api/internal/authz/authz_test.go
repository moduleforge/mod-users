package authz_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	authzapi "github.com/moduleforge/authz-api/authz"
	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/internal/authz"
)

// --- helpers ---

func ptr[T any](v T) *T { return &v }

// ctxAsProfile returns a context whose AuthContext acts as the profile with
// entity id profileID in an ordinary session. The principal id deliberately
// differs from the profile id, so a test that sees the profile id proves the
// Authorizer reads opctx.ProfileID, never the principal.
func ctxAsProfile(profileID int64) context.Context {
	return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: profileID + 1000,
		ProfileID:   profileID,
		SessionID:   1,
		ACR:         1,
	})
}

// ctxImpersonating returns a context whose AuthContext is an impersonation
// session: the administrator (principal impersonatorPrincipalID) acts as the
// target profile profileID.
func ctxImpersonating(impersonatorPrincipalID, profileID int64) context.Context {
	return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID:             profileID + 1000,
		ProfileID:               profileID,
		ImpersonatorPrincipalID: impersonatorPrincipalID,
		SessionID:               2,
		ACR:                     1,
	})
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

	ctx := ctxAsProfile(1)

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

	ctx := ctxAsProfile(7)

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

	ctx := ctxAsProfile(1)
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
	ctx := ctxAsProfile(1)

	err := az.Authorize(ctx, "unknown_op", ptr(int64(1)))
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("non-admin with unknown operation should return ErrForbidden, got: %v", err)
	}
}

// TestAuthorize_UnknownOperation_WildcardAdmin verifies that a wildcard-manage-grant
// actor can perform unknown operation slugs (the manage fallback allows it).
func TestAuthorize_UnknownOperation_WildcardAdmin(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardAllowFn)
	ctx := ctxAsProfile(1)

	// Wildcard admin: unknown op → fallback to manage opIDs → wildcard check → allowed.
	err := az.Authorize(ctx, "unknown_op", ptr(int64(1)))
	if err != nil {
		t.Errorf("wildcard admin with unknown operation should be allowed, got: %v", err)
	}
}

// TestAuthorize_Impersonation_WildcardDoesNotEscalate verifies that under
// impersonation the impersonated profile's permissions apply, never the
// administrator's.
//
// Scenario: the administrator (principal 1) is a wildcard admin. The
// impersonated profile (entity 50) is not. The effective actor is profile 50,
// so wildcard admin privileges do NOT apply.
func TestAuthorize_Impersonation_WildcardDoesNotEscalate(t *testing.T) {
	// wildcardGrantFn returns true only for id 1 (the administrator), false
	// for profile 50. Since the effective actor is the impersonated profile,
	// no wildcard grant should be found.
	az := authz.NewWithStubOpReg(func(_ context.Context, actor int64, _ []int32) (bool, error) {
		return actor == 1, nil // only id 1 is wildcard admin
	})

	ctx := ctxImpersonating(1, 50)

	err := az.Authorize(ctx, "create", nil)
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("impersonated non-admin profile should be forbidden from create: got %v", err)
	}
}

// TestAuthorize_ProfileIsTheActor verifies the grant-or-own arm receives the
// acting profile id from the AuthContext, not the principal id.
func TestAuthorize_ProfileIsTheActor(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardDenyFn)
	var gotActor int64
	az.SetGrantOrOwnFn(func(_ context.Context, actor, _ int64, _ []int32) (bool, error) {
		gotActor = actor
		return true, nil
	})

	if err := az.Authorize(ctxAsProfile(7), "read", ptr(int64(99))); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if gotActor != 7 {
		t.Errorf("grant-or-own actor: got %d, want the profile id 7", gotActor)
	}
}

// TestAuthorize_ZeroProfile_Unauthenticated verifies an AuthContext without a
// profile id is treated as no actor.
func TestAuthorize_ZeroProfile_Unauthenticated(t *testing.T) {
	az := newTestAuthorizer(wildcardAllowFn)
	ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: 3})

	if err := az.Authorize(ctx, "read", ptr(int64(1))); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got %v", err)
	}
	if err := az.AuthorizeType(ctx, "create", 7); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("AuthorizeType: expected ErrUnauthenticated, got %v", err)
	}
}

// TestAuthorize_OwnEntity_Allowed verifies that when the single-row arms
// (stubbed through grantOrOwnFn) report the target owned, Authorize allows it
// for conferring operations, CRUD and non-CRUD alike. The own arm's
// ownership_confers gate lives in the SQL and is proved by the integration
// suite.
func TestAuthorize_OwnEntity_Allowed(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardDenyFn)
	az.SetGrantOrOwnFn(grantOrOwnAllowFn)

	ctx := ctxAsProfile(1)

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

	ctx := ctxAsProfile(1)
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

	ctx := ctxAsProfile(1)
	err := az.Authorize(ctx, "read", ptr(int64(100)))
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("expected ErrForbidden for NULL-owner target, got: %v", err)
	}
}

// TestAuthorize_GrantOrOwnDBError verifies that a genuine DB error from the
// combined grant-or-own check propagates to the caller rather than being
// silently mapped to ErrForbidden (the old removed per-type tag-ownership
// query used to swallow its lookup error into a deny; this replacement does
// not).
func TestAuthorize_GrantOrOwnDBError(t *testing.T) {
	dbErr := errors.New("pool connection lost")
	az := authz.NewWithStubOpReg(wildcardDenyFn)
	az.SetGrantOrOwnFn(grantOrOwnErrFn(dbErr))

	ctx := ctxAsProfile(1)
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
	ctx := ctxAsProfile(1)

	if err := az.Authorize(ctx, "create", nil); err != nil {
		t.Errorf(`expected wildcard-manage actor to be allowed "create", got: %v`, err)
	}
}

// --- AuthorizeType (type-level) tests ---
//
// AuthorizeType answers "may the effective actor perform operation on
// resources of type typeID?" typeID is a types.id, never an entities.id. The
// call is allowed by a wildcard grant, or by a grant on the exact type's entity
// (the type-grant arm, stubbed here with SetTypeGrantFn), held directly or
// through target groups by the actor or its actor groups. Entity-level
// ownership and targeted grants over an entity whose id equals typeID must
// never influence the result: the entity-level grant-or-own check is never
// invoked, and typeID is never treated as an entities.id.

// typeGrantCall records one invocation of a type-grant stub.
type typeGrantCall struct {
	actor  int64
	typeID int64
	opIDs  []int32
}

// recordingTypeGrantFn returns a type-grant stub that returns (result, err) and
// appends every invocation to *calls.
func recordingTypeGrantFn(calls *[]typeGrantCall, result bool, err error) func(context.Context, int64, int64, []int32) (bool, error) {
	return func(_ context.Context, actor, typeID int64, opIDs []int32) (bool, error) {
		*calls = append(*calls, typeGrantCall{actor: actor, typeID: typeID, opIDs: opIDs})
		return result, err
	}
}

// countingGrantOrOwnFn returns a grant-or-own stub that reports true exactly
// when targetEntityID == matchID (simulating owning, or holding a grant over,
// the entity whose id happens to equal a type id) and counts its invocations.
func countingGrantOrOwnFn(matchID int64, calls *int) func(context.Context, int64, int64, []int32) (bool, error) {
	return func(_ context.Context, _, targetEntityID int64, _ []int32) (bool, error) {
		*calls++
		return targetEntityID == matchID, nil
	}
}

// TestAuthorizeType_EntityOwnerOfIDEqualToTypeID_Denied is the unit-level
// reproduction of the type-target confusion: an actor with entity-level
// authority over entity N, where N equals the type id, must be denied a
// type-level create when it holds no type grant, and the entity-level check
// must never be consulted.
func TestAuthorizeType_EntityOwnerOfIDEqualToTypeID_Denied(t *testing.T) {
	const typeID = int64(7)

	for _, op := range []string{"create", "list"} {
		t.Run(op, func(t *testing.T) {
			az := authz.NewWithStubOpReg(wildcardDenyFn)
			calls := 0
			az.SetGrantOrOwnFn(countingGrantOrOwnFn(typeID, &calls))
			var typeCalls []typeGrantCall
			az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, false, nil))

			err := az.AuthorizeType(ctxAsProfile(1), op, typeID)
			if !errors.Is(err, authz.ErrForbidden) {
				t.Errorf("expected ErrForbidden for entity-N owner with typeID==N, got: %v", err)
			}
			if calls != 0 {
				t.Errorf("grant-or-own check must never be invoked by AuthorizeType, got %d call(s)", calls)
			}
			if len(typeCalls) != 1 {
				t.Errorf("type-grant arm should run exactly once after a wildcard miss, got %d call(s)", len(typeCalls))
			}
		})
	}
}

// TestAuthorizeType_WildcardHolder_Allowed verifies a wildcard holder is
// allowed without consulting the entity-level check or the type-grant arm.
func TestAuthorizeType_WildcardHolder_Allowed(t *testing.T) {
	for _, op := range []string{"create", "list"} {
		t.Run(op, func(t *testing.T) {
			az := authz.NewWithStubOpReg(wildcardAllowFn)
			calls := 0
			az.SetGrantOrOwnFn(countingGrantOrOwnFn(7, &calls))
			var typeCalls []typeGrantCall
			az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, false, nil))

			if err := az.AuthorizeType(ctxAsProfile(1), op, 7); err != nil {
				t.Errorf("wildcard holder should be allowed %q, got: %v", op, err)
			}
			if calls != 0 {
				t.Errorf("grant-or-own check must never be invoked, got %d call(s)", calls)
			}
			if len(typeCalls) != 0 {
				t.Errorf("wildcard allow must short-circuit the type-grant arm, got %d call(s)", len(typeCalls))
			}
		})
	}
}

// TestAuthorizeType_TypeGrant_Allowed verifies a wildcard miss followed by a
// type-grant hit allows, and that the arm receives the effective actor, the
// exact typeID and the operation's SatisfiedBy closure.
func TestAuthorizeType_TypeGrant_Allowed(t *testing.T) {
	const typeID = int64(7)

	cases := []struct {
		name string
		ctx  context.Context
		op   string
		want int64 // effective actor
	}{
		{"actor/create", ctxAsProfile(1), "create", 1},
		{"actor/list", ctxAsProfile(1), "list", 1},
		{"impersonating", ctxImpersonating(1, 50), "create", 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			az := authz.NewWithStubOpReg(wildcardDenyFn)
			az.SetGrantOrOwnFn(grantOrOwnDenyFn)
			var typeCalls []typeGrantCall
			az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, true, nil))

			if err := az.AuthorizeType(tc.ctx, tc.op, typeID); err != nil {
				t.Fatalf("type-grant holder should be allowed %q, got: %v", tc.op, err)
			}
			if len(typeCalls) != 1 {
				t.Fatalf("type-grant arm calls: got %d, want 1", len(typeCalls))
			}
			got := typeCalls[0]
			if got.actor != tc.want {
				t.Errorf("type-grant arm actor: got %d, want effective actor %d", got.actor, tc.want)
			}
			if got.typeID != typeID {
				t.Errorf("type-grant arm typeID: got %d, want %d", got.typeID, typeID)
			}
			wantIDs, err := authz.StandardOpRegistry().SatisfiedBy(tc.op)
			if err != nil {
				t.Fatalf("SatisfiedBy(%q): %v", tc.op, err)
			}
			if !reflect.DeepEqual(got.opIDs, wantIDs) {
				t.Errorf("type-grant arm opIDs: got %v, want the SatisfiedBy closure %v", got.opIDs, wantIDs)
			}
		})
	}
}

// TestAuthorizeType_TypeGrantMiss_Forbidden verifies a wildcard miss plus a
// type-grant miss returns ErrForbidden.
func TestAuthorizeType_TypeGrantMiss_Forbidden(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardDenyFn)
	az.SetGrantOrOwnFn(grantOrOwnAllowFn) // entity-level authority must not help
	var typeCalls []typeGrantCall
	az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, false, nil))

	err := az.AuthorizeType(ctxAsProfile(1), "create", 7)
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got: %v", err)
	}
	if len(typeCalls) != 1 {
		t.Errorf("type-grant arm calls: got %d, want 1", len(typeCalls))
	}
}

// TestAuthorizeType_TypeGrantDBError verifies a type-grant arm error is
// returned unchanged rather than swallowed into a denial.
func TestAuthorizeType_TypeGrantDBError(t *testing.T) {
	dbErr := errors.New("type grant query failed")
	az := authz.NewWithStubOpReg(wildcardDenyFn)
	var typeCalls []typeGrantCall
	az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, false, dbErr))

	err := az.AuthorizeType(ctxAsProfile(1), "create", 7)
	if !errors.Is(err, dbErr) {
		t.Errorf("expected type-grant DB error to propagate, got: %v", err)
	}
	if errors.Is(err, authz.ErrForbidden) {
		t.Errorf("a DB error must not be reported as a denial, got: %v", err)
	}
}

// TestAuthorizeType_NoActor verifies an unauthenticated context returns
// ErrUnauthenticated and never reaches the type-grant arm.
func TestAuthorizeType_NoActor(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardAllowFn)
	var typeCalls []typeGrantCall
	az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, true, nil))

	err := az.AuthorizeType(context.Background(), "create", 7)
	if !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("expected ErrUnauthenticated, got: %v", err)
	}
	if len(typeCalls) != 0 {
		t.Errorf("type-grant arm must not run without an actor, got %d call(s)", len(typeCalls))
	}
}

// TestAuthorizeType_Impersonation_WildcardCheckedAgainstProfile verifies the
// wildcard check receives the impersonated profile id, so a wildcard-holding
// administrator does not escalate a non-admin impersonated profile.
func TestAuthorizeType_Impersonation_WildcardCheckedAgainstProfile(t *testing.T) {
	var seen []int64
	az := authz.NewWithStubOpReg(func(_ context.Context, actor int64, _ []int32) (bool, error) {
		seen = append(seen, actor)
		return actor == 1, nil // only the administrator (1) is a wildcard admin
	})
	az.SetGrantOrOwnFn(grantOrOwnDenyFn)
	var typeCalls []typeGrantCall
	az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, false, nil))

	err := az.AuthorizeType(ctxImpersonating(1, 50), "create", 7)
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("impersonated non-admin profile should be forbidden from type-level create: got %v", err)
	}
	if len(seen) != 1 || seen[0] != 50 {
		t.Errorf("wildcard check should receive the impersonated profile id 50, got %v", seen)
	}
}

// TestAuthorizeType_UnknownOperation mirrors Authorize's unknown-slug
// fallback: a non-admin is forbidden, a wildcard-manage holder is allowed, and
// the type-grant arm is never consulted (the closure is unknown).
func TestAuthorizeType_UnknownOperation(t *testing.T) {
	t.Run("non-admin", func(t *testing.T) {
		az := authz.NewWithStubOpReg(wildcardDenyFn)
		var typeCalls []typeGrantCall
		az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, true, nil))
		err := az.AuthorizeType(ctxAsProfile(1), "unknown_op", 7)
		if !errors.Is(err, authz.ErrForbidden) {
			t.Errorf("non-admin with unknown operation should return ErrForbidden, got: %v", err)
		}
		if len(typeCalls) != 0 {
			t.Errorf("type-grant arm must not run for an unknown operation, got %d call(s)", len(typeCalls))
		}
	})
	t.Run("wildcard-manage", func(t *testing.T) {
		az := authz.NewWithStubOpReg(wildcardAllowFn)
		var typeCalls []typeGrantCall
		az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, false, nil))
		if err := az.AuthorizeType(ctxAsProfile(1), "unknown_op", 7); err != nil {
			t.Errorf("wildcard-manage holder with unknown operation should be allowed, got: %v", err)
		}
		if len(typeCalls) != 0 {
			t.Errorf("type-grant arm must not run for an unknown operation, got %d call(s)", len(typeCalls))
		}
	})
}

// TestAuthorizeType_BadTypeID verifies typeID <= 0 fails closed, even for a
// wildcard holder, and never reaches the type-grant arm.
func TestAuthorizeType_BadTypeID(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardAllowFn)
	var typeCalls []typeGrantCall
	az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, true, nil))

	for _, typeID := range []int64{0, -1} {
		err := az.AuthorizeType(ctxAsProfile(1), "create", typeID)
		if !errors.Is(err, authz.ErrForbidden) {
			t.Errorf("typeID=%d should return ErrForbidden even for a wildcard holder, got: %v", typeID, err)
		}
	}
	if len(typeCalls) != 0 {
		t.Errorf("type-grant arm must not run for typeID <= 0, got %d call(s)", len(typeCalls))
	}
}

// TestAuthorizeType_WildcardDBError verifies a wildcard-check DB error
// propagates rather than being swallowed into a denial, and that the
// type-grant arm is not consulted afterwards.
func TestAuthorizeType_WildcardDBError(t *testing.T) {
	dbErr := errors.New("pool connection lost")
	az := authz.NewWithStubOpReg(wildcardErrFn(dbErr))
	var typeCalls []typeGrantCall
	az.SetTypeGrantFn(recordingTypeGrantFn(&typeCalls, true, nil))

	err := az.AuthorizeType(ctxAsProfile(1), "create", 7)
	if !errors.Is(err, dbErr) {
		t.Errorf("expected DB error to propagate, got: %v", err)
	}
	if len(typeCalls) != 0 {
		t.Errorf("type-grant arm must not run after a wildcard-check error, got %d call(s)", len(typeCalls))
	}
}

// --- Prelude: assurance gate (U14) ---

// recordingWildcardFn returns a wildcard stub that reports result and counts
// its invocations, so a test can prove a gate refused before the wildcard arm.
func recordingWildcardFn(calls *int, result bool) func(context.Context, int64, []int32) (bool, error) {
	return func(context.Context, int64, []int32) (bool, error) {
		*calls++
		return result, nil
	}
}

// requireStepUp asserts err is the shared action-required step-up refusal.
func requireStepUp(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, authz.ErrStepUpRequired) {
		t.Fatalf("expected ErrStepUpRequired, got: %v", err)
	}
	var ae *apiresp.ActionRequiredError
	if !errors.As(err, &ae) || ae.Action().Code != "users.step_up_required" || ae.Action().Status != http.StatusConflict {
		t.Fatalf("expected the 409 users.step_up_required action, got: %v", err)
	}
	if errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a step-up refusal must not be a plain 403, got: %v", err)
	}
}

func TestAuthorize_AssuranceGate(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	session := func(acr int16, authAge time.Duration) context.Context {
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: 1001, ProfileID: 1, SessionID: 9, ACR: acr, AuthTime: now.Add(-authAge),
		})
	}
	credential := func(acr int16) context.Context {
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: 1001, ProfileID: 1, CredentialID: 4, ACR: acr, AuthTime: now,
		})
	}
	impersonating := func(authAge time.Duration) context.Context {
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: 1001, ProfileID: 1, ImpersonatorPrincipalID: 7, SessionID: 9, ACR: 1, AuthTime: now.Add(-authAge),
		})
	}

	cases := []struct {
		name     string
		ctx      context.Context
		op       string
		wantStep bool
	}{
		{"session meets min_acr", session(1, time.Hour), "read", false},
		{"session below min_acr", session(0, 0), "read", true},
		{"credential below min_acr", credential(0), "update", true},
		{"fresh session on max_auth_age op", session(1, 9*time.Minute), "impersonate", false},
		{"stale session on max_auth_age op", session(1, 11*time.Minute), "impersonate", true},
		{"stale session on credentials", session(2, time.Hour), "credentials", true},
		{"no auth_time on max_auth_age op", opctx.WithAuthContext(context.Background(), opctx.AuthContext{ProfileID: 1, SessionID: 9, ACR: 1}), "credentials", true},
		{"impersonation is never exempt", impersonating(time.Hour), "impersonate", true},
		{"system actor skips the gate", opctx.WithSystemActor(context.Background(), 5, 6), "impersonate", false},
		{"anonymous context skips the gate", opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: 2, ProfileID: 3, SessionID: 9, Anonymous: true}), "credentials", false},
		{"unknown slug is gated as manage", session(0, 0), "unknown_op", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			az := authz.NewWithStubOpReg(recordingWildcardFn(&calls, true))
			az.SetNow(func() time.Time { return now })

			err := az.Authorize(tc.ctx, tc.op, ptr(int64(99)))
			if tc.wantStep {
				requireStepUp(t, err)
				if calls != 0 {
					t.Errorf("the assurance gate must run before the wildcard arm, got %d wildcard call(s)", calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected the wildcard holder to pass, got: %v", err)
			}
		})
	}
}

// TestAuthorizeType_AssuranceGate verifies AuthorizeType and AuthorizeTypeIn
// share the gate.
func TestAuthorizeType_AssuranceGate(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardAllowFn)
	ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{ProfileID: 1, SessionID: 9, ACR: 0})

	requireStepUp(t, az.AuthorizeType(ctx, "create", 7))
	requireStepUp(t, az.AuthorizeTypeIn(ctx, "create", 7, 40))
}

// TestAuthorize_AssuranceGate_ClientCredentials covers a client-credentials
// context (CredentialID != 0, SessionID == 0): it is gated like a session, by
// ACR against min_acr and by AuthTime against max_auth_age, on every entry
// point, and a refusal comes before any grant arm.
func TestAuthorize_AssuranceGate_ClientCredentials(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	credential := func(acr int16, authAge time.Duration) context.Context {
		return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: 1001, ProfileID: 1, CredentialID: 4, SessionID: 0, ACR: acr, AuthTime: now.Add(-authAge),
		})
	}

	cases := []struct {
		name     string
		ctx      context.Context
		op       string
		wantStep bool
	}{
		{"meets min_acr, op without max_auth_age", credential(1, 24*time.Hour), "read", false},
		{"below min_acr", credential(0, 0), "read", true},
		{"fresh on impersonate", credential(1, time.Minute), "impersonate", false},
		{"fresh on credentials", credential(1, time.Minute), "credentials", false},
		{"exactly max_auth_age old is still fresh", credential(1, 10*time.Minute), "credentials", false},
		{"one nanosecond past max_auth_age on credentials", credential(1, 10*time.Minute+time.Nanosecond), "credentials", true},
		{"stale on impersonate", credential(1, time.Hour), "impersonate", true},
		{"zero AuthTime on credentials", opctx.WithAuthContext(context.Background(),
			opctx.AuthContext{PrincipalID: 1001, ProfileID: 1, CredentialID: 4, ACR: 1}), "credentials", true},
	}
	entryPoints := []struct {
		name string
		call func(az *authz.Authorizer, ctx context.Context, op string) error
	}{
		{"Authorize", func(az *authz.Authorizer, ctx context.Context, op string) error {
			return az.Authorize(ctx, op, ptr(int64(99)))
		}},
		{"AuthorizeType", func(az *authz.Authorizer, ctx context.Context, op string) error {
			return az.AuthorizeType(ctx, op, 7)
		}},
		{"AuthorizeTypeIn", func(az *authz.Authorizer, ctx context.Context, op string) error {
			return az.AuthorizeTypeIn(ctx, op, 7, 40)
		}},
	}
	for _, ep := range entryPoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				calls := 0
				az := authz.NewWithStubOpReg(recordingWildcardFn(&calls, true))
				az.SetNow(func() time.Time { return now })

				err := ep.call(az, tc.ctx, tc.op)
				if tc.wantStep {
					requireStepUp(t, err)
					if calls != 0 {
						t.Errorf("the assurance gate must run before the wildcard arm, got %d wildcard call(s)", calls)
					}
					return
				}
				if err != nil {
					t.Fatalf("expected the wildcard holder to pass, got: %v", err)
				}
			})
		}
	}
}

// TestAuthorize_AssuranceGate_SkippedContexts verifies the system-actor and
// anonymous contexts skip the gate on every entry point, even for the
// operations with a max_auth_age and with no AuthTime at all.
func TestAuthorize_AssuranceGate_SkippedContexts(t *testing.T) {
	contexts := map[string]context.Context{
		"system actor": opctx.WithSystemActor(context.Background(), 5, 6),
		"anonymous": opctx.WithAuthContext(context.Background(),
			opctx.AuthContext{PrincipalID: 2, ProfileID: 3, Anonymous: true}),
		"anonymous with a stale session id": opctx.WithAuthContext(context.Background(),
			opctx.AuthContext{PrincipalID: 2, ProfileID: 3, SessionID: 9, ACR: 0, Anonymous: true}),
	}
	for name, ctx := range contexts {
		for _, op := range []string{"read", "impersonate", "credentials", "manage"} {
			t.Run(name+"/"+op, func(t *testing.T) {
				az := authz.NewWithStubOpReg(wildcardAllowFn)
				if err := az.Authorize(ctx, op, ptr(int64(99))); err != nil {
					t.Errorf("Authorize: %v, want the gate skipped", err)
				}
				if err := az.AuthorizeType(ctx, op, 7); err != nil {
					t.Errorf("AuthorizeType: %v, want the gate skipped", err)
				}
				if err := az.AuthorizeTypeIn(ctx, op, 7, 40); err != nil {
					t.Errorf("AuthorizeTypeIn: %v, want the gate skipped", err)
				}
			})
		}
	}
}

// TestAuthorize_StepUp_IsTheSharedActionRequiredError verifies the refusal
// satisfies mod-core's apiresp action-required type with errors.As, including
// when another layer wraps it, and that apiresp.WriteError renders it as a 409
// users.step_up_required action from every entry point.
func TestAuthorize_StepUp_IsTheSharedActionRequiredError(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardAllowFn)
	ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{ProfileID: 1, SessionID: 9, ACR: 0})

	refusals := map[string]error{
		"Authorize":       az.Authorize(ctx, "read", ptr(int64(99))),
		"AuthorizeType":   az.AuthorizeType(ctx, "create", 7),
		"AuthorizeTypeIn": az.AuthorizeTypeIn(ctx, "create", 7, 40),
	}
	for name, err := range refusals {
		for form, e := range map[string]error{
			"direct":         err,
			"wrapped":        fmt.Errorf("remove login method: %w", err),
			"double-wrapped": fmt.Errorf("service: %w", fmt.Errorf("authorize: %w", err)),
		} {
			t.Run(name+"/"+form, func(t *testing.T) {
				requireStepUp(t, e)

				rec := httptest.NewRecorder()
				apiresp.WriteError(rec, httptest.NewRequest(http.MethodPost, "/", nil), e)
				if rec.Code != http.StatusConflict {
					t.Fatalf("WriteError status = %d, want %d", rec.Code, http.StatusConflict)
				}
				var body struct {
					Action struct {
						Code string `json:"code"`
					} `json:"action"`
					Error json.RawMessage `json:"error"`
				}
				if jerr := json.Unmarshal(rec.Body.Bytes(), &body); jerr != nil {
					t.Fatalf("decode body %q: %v", rec.Body.String(), jerr)
				}
				if body.Action.Code != "users.step_up_required" {
					t.Errorf("rendered action code = %q, want users.step_up_required (body %s)", body.Action.Code, rec.Body.String())
				}
				if len(body.Error) != 0 {
					t.Errorf("a step-up is an action, not an error envelope: %s", rec.Body.String())
				}
			})
		}
	}
}

// --- Prelude: owning-party gate (decision C) ---

func TestAuthorize_PartyGate(t *testing.T) {
	t.Run("refusal is a plain 403 before the wildcard arm", func(t *testing.T) {
		calls := 0
		az := authz.NewWithStubOpReg(recordingWildcardFn(&calls, true))
		var got []authz.PartyPolicyQuery
		az.SetPartyPolicyFn(func(_ context.Context, q authz.PartyPolicyQuery) (bool, error) {
			got = append(got, q)
			return false, nil
		})

		err := az.Authorize(ctxAsProfile(7), "read", ptr(int64(99)))
		if !errors.Is(err, authz.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got: %v", err)
		}
		if errors.Is(err, authz.ErrStepUpRequired) {
			t.Fatalf("an owning-party refusal is not an action, got: %v", err)
		}
		if calls != 0 {
			t.Errorf("the owning-party gate must run before the wildcard arm, got %d wildcard call(s)", calls)
		}
		want := authz.PartyPolicyQuery{ProfileID: 7, SessionID: 1, TargetEntityID: 99}
		if len(got) != 1 || got[0] != want {
			t.Errorf("party gate query: got %+v, want [%+v]", got, want)
		}
	})

	t.Run("credential context passes the credential", func(t *testing.T) {
		az := authz.NewWithStubOpReg(wildcardAllowFn)
		var got authz.PartyPolicyQuery
		az.SetPartyPolicyFn(func(_ context.Context, q authz.PartyPolicyQuery) (bool, error) {
			got = q
			return false, nil
		})
		ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: 3, ProfileID: 8, CredentialID: 4, ACR: 1})

		if err := az.Authorize(ctx, "read", ptr(int64(99))); !errors.Is(err, authz.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got: %v", err)
		}
		if want := (authz.PartyPolicyQuery{ProfileID: 8, CredentialID: 4, TargetEntityID: 99}); got != want {
			t.Errorf("party gate query: got %+v, want %+v", got, want)
		}
	})

	t.Run("met gate continues to the arms", func(t *testing.T) {
		az := authz.NewWithStubOpReg(wildcardDenyFn)
		az.SetPartyPolicyFn(func(context.Context, authz.PartyPolicyQuery) (bool, error) { return true, nil })
		az.SetGrantOrOwnFn(grantOrOwnAllowFn)
		if err := az.Authorize(ctxAsProfile(7), "read", ptr(int64(99))); err != nil {
			t.Fatalf("expected allow, got: %v", err)
		}
	})

	t.Run("DB error propagates", func(t *testing.T) {
		dbErr := errors.New("party policy query failed")
		az := authz.NewWithStubOpReg(wildcardAllowFn)
		az.SetPartyPolicyFn(func(context.Context, authz.PartyPolicyQuery) (bool, error) { return false, dbErr })
		if err := az.Authorize(ctxAsProfile(7), "read", ptr(int64(99))); !errors.Is(err, dbErr) {
			t.Fatalf("expected the DB error, got: %v", err)
		}
	})

	t.Run("skipped without an owner or without a session or credential", func(t *testing.T) {
		az := authz.NewWithStubOpReg(wildcardAllowFn)
		calls := 0
		az.SetPartyPolicyFn(func(context.Context, authz.PartyPolicyQuery) (bool, error) {
			calls++
			return false, nil
		})
		az.SetTypeGrantFn(func(context.Context, int64, int64, []int32) (bool, error) { return false, nil })

		if err := az.Authorize(ctxAsProfile(7), "list", nil); err != nil {
			t.Errorf("nil target: %v", err)
		}
		if err := az.AuthorizeType(ctxAsProfile(7), "create", 3); err != nil {
			t.Errorf("AuthorizeType: %v", err)
		}
		if err := az.Authorize(opctx.WithSystemActor(context.Background(), 5, 6), "read", ptr(int64(99))); err != nil {
			t.Errorf("system actor: %v", err)
		}
		anon := opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: 2, ProfileID: 3, Anonymous: true})
		if err := az.Authorize(anon, "read", ptr(int64(99))); err != nil {
			t.Errorf("anonymous: %v", err)
		}
		if calls != 0 {
			t.Errorf("party gate consulted %d time(s), want 0", calls)
		}
	})
}

// --- AuthorizeTypeIn ---

func TestAuthorizeTypeIn(t *testing.T) {
	const typeID, ownerID = int64(7), int64(40)

	type outcome struct{ wildcard, typeGrant, ownedScope bool }
	cases := []struct {
		name      string
		arms      outcome
		wantAllow bool
		wantCalls []string
	}{
		{"wildcard", outcome{wildcard: true}, true, []string{"wildcard"}},
		{"type-entity grant", outcome{typeGrant: true}, true, []string{"wildcard", "type"}},
		{"owned scope or own", outcome{ownedScope: true}, true, []string{"wildcard", "type", "owned"}},
		{"nothing", outcome{}, false, []string{"wildcard", "type", "owned"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			az := authz.NewWithStubOpReg(func(context.Context, int64, []int32) (bool, error) {
				calls = append(calls, "wildcard")
				return tc.arms.wildcard, nil
			})
			az.SetGrantOrOwnFn(func(context.Context, int64, int64, []int32) (bool, error) {
				t.Error("AuthorizeTypeIn must never run Authorize's entity-level arms")
				return true, nil
			})
			az.SetTypeGrantFn(func(_ context.Context, _, gotType int64, _ []int32) (bool, error) {
				calls = append(calls, "type")
				if gotType != typeID {
					t.Errorf("type arm typeID: got %d, want %d", gotType, typeID)
				}
				return tc.arms.typeGrant, nil
			})
			az.SetOwnedScopeFn(func(_ context.Context, actor, gotType, gotOwner int64, _ []int32) (bool, error) {
				calls = append(calls, "owned")
				if actor != 1 || gotType != typeID || gotOwner != ownerID {
					t.Errorf("owned-scope arm: got (actor %d, type %d, owner %d), want (1, %d, %d)", actor, gotType, gotOwner, typeID, ownerID)
				}
				return tc.arms.ownedScope, nil
			})
			var gate []authz.PartyPolicyQuery
			az.SetPartyPolicyFn(func(_ context.Context, q authz.PartyPolicyQuery) (bool, error) {
				gate = append(gate, q)
				return true, nil
			})

			// Through mod-core's helper, which must find the capability.
			err := coreAuthz.AuthorizeTypeIn(ctxAsProfile(1), az, "create", typeID, ptr(ownerID))
			if tc.wantAllow && err != nil {
				t.Fatalf("expected allow, got: %v", err)
			}
			if !tc.wantAllow && !errors.Is(err, authz.ErrForbidden) {
				t.Fatalf("expected ErrForbidden, got: %v", err)
			}
			if !reflect.DeepEqual(calls, tc.wantCalls) {
				t.Errorf("arm order: got %v, want %v", calls, tc.wantCalls)
			}
			if want := (authz.PartyPolicyQuery{ProfileID: 1, SessionID: 1, OwnerID: ownerID}); len(gate) != 1 || gate[0] != want {
				t.Errorf("party gate query: got %+v, want [%+v]", gate, want)
			}
		})
	}
}

func TestAuthorizeTypeIn_Refusals(t *testing.T) {
	az := authz.NewWithStubOpReg(wildcardAllowFn)

	if err := az.AuthorizeTypeIn(context.Background(), "create", 7, 40); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("no actor: expected ErrUnauthenticated, got %v", err)
	}
	for _, ids := range [][2]int64{{0, 40}, {7, 0}, {-1, 40}, {7, -1}} {
		if err := az.AuthorizeTypeIn(ctxAsProfile(1), "create", ids[0], ids[1]); !errors.Is(err, authz.ErrForbidden) {
			t.Errorf("typeID %d ownerID %d: expected ErrForbidden even for a wildcard holder, got %v", ids[0], ids[1], err)
		}
	}

	az.SetPartyPolicyFn(func(context.Context, authz.PartyPolicyQuery) (bool, error) { return false, nil })
	if err := az.AuthorizeTypeIn(ctxAsProfile(1), "create", 7, 40); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("owning-party refusal: expected ErrForbidden, got %v", err)
	}

	dbErr := errors.New("owned scope query failed")
	az = authz.NewWithStubOpReg(wildcardDenyFn)
	az.SetTypeGrantFn(func(context.Context, int64, int64, []int32) (bool, error) { return false, nil })
	az.SetOwnedScopeFn(func(context.Context, int64, int64, int64, []int32) (bool, error) { return false, dbErr })
	if err := az.AuthorizeTypeIn(ctxAsProfile(1), "create", 7, 40); !errors.Is(err, dbErr) {
		t.Errorf("owned-scope DB error: expected it to propagate, got %v", err)
	}
}

// TestStandardOpRegistry_Policies pins the policy columns the gate reads.
func TestStandardOpRegistry_Policies(t *testing.T) {
	reg := authz.StandardOpRegistry()
	for slug, want := range map[string]authzapi.OperationPolicy{
		"read":        {ID: 1, OwnershipConfers: true, MinACR: 1},
		"manage":      {ID: 7, OwnershipConfers: false, MinACR: 1},
		"impersonate": {ID: 8, OwnershipConfers: false, MinACR: 1, MaxAuthAge: 10 * time.Minute},
		"login":       {ID: 9, OwnershipConfers: false, MinACR: 1},
		"credentials": {ID: 17, OwnershipConfers: true, MinACR: 1, MaxAuthAge: 10 * time.Minute},
	} {
		got, err := reg.Policy(slug)
		if err != nil {
			t.Fatalf("Policy(%q): %v", slug, err)
		}
		if got != want {
			t.Errorf("Policy(%q) = %+v, want %+v", slug, got, want)
		}
	}
}
