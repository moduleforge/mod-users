//go:build integration

package authz_test

// anonymous_actor_integration_test.go proves, against the composed schema, that
// mod-core's seeded anonymous system actor is zero-authority:
//
//   - it is seeded exactly once, with an active personal profile;
//   - its profile can never hold a grant (mod-authz MF501 actor_anonymous);
//   - its profile can never join an actor group (MF502 member_anonymous);
//   - it holds zero grants and is invisible to the list-side access functions;
//   - Authorize on its profile returns ErrForbidden, never ErrUnauthenticated.
//
// It reuses authz_integration_test.go's TestMain and the shared fixtures.

import (
	"context"
	"errors"
	"testing"

	"github.com/moduleforge/mod-users/api/internal/authz"
)

// anonymousProfileID resolves the anonymous system actor's personal profile.
func anonymousProfileID(t *testing.T) int64 {
	t.Helper()
	return fxScanID(t, integPool, "resolve anonymous profile", `
SELECT p.entity_id FROM profiles p
JOIN system_actors sa ON sa.entity_id = p.holder_id
WHERE sa.slug = 'anonymous' AND p.party_id IS NULL`)
}

// TestInteg_AnonymousActor_SeededState: one system actor 'anonymous', no owner,
// and one active personal profile.
func TestInteg_AnonymousActor_SeededState(t *testing.T) {
	ctx := context.Background()
	var count int
	if err := integPool.QueryRow(ctx, `SELECT count(*) FROM system_actors WHERE slug = 'anonymous'`).Scan(&count); err != nil {
		t.Fatalf("count anonymous: %v", err)
	}
	if count != 1 {
		t.Errorf("anonymous system_actors rows = %d, want 1", count)
	}
	var owner *int64
	if err := integPool.QueryRow(ctx,
		`SELECT e.owner_id FROM entities e JOIN system_actors sa ON sa.entity_id = e.id WHERE sa.slug = 'anonymous'`).Scan(&owner); err != nil {
		t.Fatalf("anonymous owner: %v", err)
	}
	if owner != nil {
		t.Errorf("anonymous owner_id = %d, want NULL", *owner)
	}
	var status string
	if err := integPool.QueryRow(ctx, `SELECT status FROM profiles WHERE entity_id = $1`, anonymousProfileID(t)).Scan(&status); err != nil {
		t.Fatalf("anonymous profile status: %v", err)
	}
	if status != "active" {
		t.Errorf("anonymous profile status = %q, want active", status)
	}
}

// TestInteg_AnonymousActor_CannotHoldGrant: a grant to the anonymous profile is
// rejected for wildcard, targeted and type-entity shapes alike.
func TestInteg_AnonymousActor_CannotHoldGrant(t *testing.T) {
	anon := anonymousProfileID(t)
	target := seedOrg(t, "Anon Grant Target")
	read := opID(t, "read")
	_, err := integPool.Exec(context.Background(),
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, $3, NULL)`, anon, read, target)
	expectState(t, err, "MF501", "targeted grant to anonymous")
	_, err = integPool.Exec(context.Background(),
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, NULL, NULL)`, anon, read)
	expectState(t, err, "MF501", "wildcard grant to anonymous")
}

// TestInteg_AnonymousActor_ActorGroupMembershipRejected: the anonymous profile
// cannot join an actor group.
func TestInteg_AnonymousActor_ActorGroupMembershipRejected(t *testing.T) {
	group := seedActorGroup(t, "Anon Group")
	_, err := integPool.Exec(context.Background(),
		`INSERT INTO authz_actor_group_members (group_id, member_id) VALUES ($1, $2)`, group, anonymousProfileID(t))
	expectState(t, err, "MF502", "anonymous in actor group")
}

// TestInteg_AnonymousActor_AuthorizeForbiddenNotUnauthenticated: a context
// carrying the anonymous profile passes the actor check and is denied.
func TestInteg_AnonymousActor_AuthorizeForbiddenNotUnauthenticated(t *testing.T) {
	ctx := actorCtx(anonymousProfileID(t))
	target := seedOrg(t, "Anon Authorize Target")
	for _, op := range []string{"read", "update", "manage"} {
		err := integAZ.Authorize(ctx, op, &target)
		if !errors.Is(err, authz.ErrForbidden) {
			t.Errorf("anonymous Authorize(%s): got %v, want ErrForbidden", op, err)
		}
	}
	if err := integAZ.Authorize(ctx, "manage", nil); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("anonymous Authorize(manage, nil): got %v, want ErrForbidden", err)
	}
}

// TestInteg_AnonymousActor_ZeroPlatformAuthority: no grants, and the list-side
// access function returns nothing for it.
func TestInteg_AnonymousActor_ZeroPlatformAuthority(t *testing.T) {
	anon := anonymousProfileID(t)
	_ = seedUnownedOrg(t, "Anon List Org")
	var n int
	if err := integPool.QueryRow(context.Background(), `SELECT count(*) FROM grants WHERE actor_id = $1`, anon).Scan(&n); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if n != 0 {
		t.Errorf("anonymous holds %d grants, want 0", n)
	}
	readIDs, err := integOpReg.SatisfiedBy("read")
	if err != nil {
		t.Fatalf("SatisfiedBy(read): %v", err)
	}
	if got := accessibleIDs(t, "organization", anon, readIDs); len(got) != 0 {
		t.Errorf("accessible_organization_ids_for_actor(anonymous) = %v, want empty", got)
	}
}
