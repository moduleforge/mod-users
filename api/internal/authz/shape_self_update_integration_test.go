//go:build integration

package authz_test

// shape_self_update_integration_test.go pins the authorization facts that
// PUT /v1/self relies on once mod-core's NaturalPersonService.UpdateByEntityUUID
// authorizes "update" on the target entity: a non-admin must still be able to
// update their own account holder entity, and in an assume (sudo) session the
// effective actor is the assumed user, not the admin.

import (
	"context"
	"testing"

	"github.com/moduleforge/core-api/opctx"
)

// TestInteg_ShapeSelfUpdate_NonAdminOwnEntityAllowed asserts a caller with no
// grants at all may "update" their own natural_person entity (the
// entities_owner_default_self trigger makes the entity own itself).
func TestInteg_ShapeSelfUpdate_NonAdminOwnEntityAllowed(t *testing.T) {
	selfID := seedUser(t, "shape-self-nonadmin@example.com", false)
	otherID := seedUser(t, "shape-self-other@example.com", false)
	ctx := actorCtx(selfID)

	if err := integAZ.Authorize(ctx, "update", &selfID); err != nil {
		t.Errorf("non-admin update own entity: got %v, want nil", err)
	}
	if err := integAZ.Authorize(ctx, "update", &otherID); err == nil {
		t.Errorf("non-admin update another user's entity: got nil, want ErrForbidden")
	}
}

// TestInteg_ShapeSelfUpdate_AssumeSessionEffectiveActor models an assume
// session: the JWT subject (admin) is the authenticated actor and the assumed
// user is the sudo actor, which is the effective actor for authorization. The
// wildcard held by the admin does not apply, so "update" on the admin's own
// entity (what PUT /self targets, since it loads uc.UserAccountID) is denied,
// while the assumed user's own entity is allowed.
func TestInteg_ShapeSelfUpdate_AssumeSessionEffectiveActor(t *testing.T) {
	adminID := seedUser(t, "shape-assume-admin@example.com", true)
	assumedID := seedUser(t, "shape-assume-target@example.com", false)
	ctx := opctx.WithSudoActor(opctx.WithActor(context.Background(), adminID), assumedID)

	if err := integAZ.Authorize(ctx, "update", &adminID); err == nil {
		t.Errorf("assume session: update admin's own entity: got nil, want ErrForbidden")
	}
	if err := integAZ.Authorize(ctx, "update", &assumedID); err != nil {
		t.Errorf("assume session: update assumed user's entity: got %v, want nil", err)
	}
}
