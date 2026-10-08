//go:build integration

package authz_test

// type_target_integration_test.go reproduces and pins the fix for the
// type-target confusion: a type-level authorization check ("may this actor
// create resources of type T?") used to be answered by entity-level ownership
// and grants, because every call site passed the types.id as the target of
// Authorize, which always interprets its target as an entities.id.
//
// types.id and entities.id are independent BIGSERIAL sequences that both start
// at 1, so Authorize(ctx, "create", &typeID) passes for any actor who owns the
// entity whose id happens to equal typeID, or holds a targeted grant over it.
// AuthorizeType must answer from wildcard grants only.
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/moduleforge/mod-users/api/internal/authz"
)

// typeIDForSlug resolves types.id for slug.
func typeIDForSlug(t *testing.T, slug string) int64 {
	t.Helper()
	var id int64
	if err := integPool.QueryRow(context.Background(), `SELECT id FROM types WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("typeIDForSlug(%q): %v", slug, err)
	}
	return id
}

// entityExists reports whether entities has a row with the given id.
func entityExists(t *testing.T, id int64) bool {
	t.Helper()
	var exists bool
	if err := integPool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM entities WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("entityExists(%d): %v", id, err)
	}
	return exists
}

// seedEntityWithExplicitID inserts a corporation-typed entity with an explicit
// id owned by ownerEntityID (owner_id must be set at INSERT time; it is
// immutable afterwards), then advances the entities id sequence so later
// inserts cannot collide with the explicit id. The sequence is never moved
// backwards.
func seedEntityWithExplicitID(t *testing.T, id, ownerEntityID int64) {
	t.Helper()
	ctx := context.Background()

	const insertSQL = `INSERT INTO entities (id, fundamental_type_id, owner_id) VALUES ($1, $2, $3)`
	if _, err := integPool.Exec(ctx, insertSQL, id, corporationTypeID(t), ownerEntityID); err != nil {
		t.Fatalf("seedEntityWithExplicitID: insert entity %d: %v", id, err)
	}

	var seq string
	if err := integPool.QueryRow(ctx, `SELECT pg_get_serial_sequence('entities', 'id')`).Scan(&seq); err != nil {
		t.Fatalf("seedEntityWithExplicitID: resolve sequence: %v", err)
	}
	// seq is catalog-derived (never caller input), so formatting it is safe.
	advanceSQL := fmt.Sprintf(
		`SELECT setval($1::regclass, GREATEST((SELECT max(id) FROM entities), (SELECT last_value FROM %s)))`, seq)
	var ignored int64
	if err := integPool.QueryRow(ctx, advanceSQL, seq).Scan(&ignored); err != nil {
		t.Fatalf("seedEntityWithExplicitID: advance sequence: %v", err)
	}
}

// grantEntityLevelAuthorityOver gives actorID entity-level authority over the
// entity whose id is entityID, and returns the arm exercised. If the entity
// exists, a targeted grant is inserted (the TargetChain arm of checkGrantOrOwn).
// Otherwise an entity row with that explicit id is created owned by the actor
// (the ownership arm).
func grantEntityLevelAuthorityOver(t *testing.T, actorID, entityID int64) string {
	t.Helper()
	if entityExists(t, entityID) {
		targetedGrant(t, actorID, entityID, "create")
		return "targeted-grant (TargetChain arm)"
	}
	seedEntityWithExplicitID(t, entityID, actorID)
	return "ownership (e.owner_id arm)"
}

// TestInteg_TypeTarget_EntityAuthorityDoesNotAnswerTypeCheck is the
// reproduction: an ordinary user with entity-level authority over entity T
// (T = the types.id of the resource type) must NOT pass a type-level create,
// while Authorize, which treats its target as an entities.id by contract,
// still does. Which arm of checkGrantOrOwn is exercised depends on whether an
// entity with id T already exists in the freshly migrated database (it does
// when the migrations seeded enough entities or earlier tests created them):
// if so the targeted-grant arm is used, otherwise the ownership arm is
// exercised deterministically via an explicit-id entity owned by the user.
func TestInteg_TypeTarget_EntityAuthorityDoesNotAnswerTypeCheck(t *testing.T) {
	for _, slug := range []string{"authz_actor_group", "natural_person"} {
		t.Run(slug, func(t *testing.T) {
			typeID := typeIDForSlug(t, slug)
			userU := seedUser(t, "typetarget-u-"+slug+"@example.com", false)

			arm := grantEntityLevelAuthorityOver(t, userU, typeID)
			t.Logf("type %q has types.id=%d; entity-level authority over entity %d via %s", slug, typeID, typeID, arm)

			ctxU := actorCtx(userU)

			// Characterization: Authorize treats its target as entities.id by
			// contract, so U legitimately passes for entity T. This is exactly
			// why type-level callers must use AuthorizeType: passing a types.id
			// here is a caller bug Authorize cannot detect.
			if err := integAZ.Authorize(ctxU, "create", &typeID); err != nil {
				t.Errorf("characterization: Authorize(create, &%d) with entity-level authority: got %v, want nil (entity semantics must not change)", typeID, err)
			}

			// The fix: the type-level check ignores entity-level authority.
			if err := integAZ.AuthorizeType(ctxU, "create", typeID); !errors.Is(err, authz.ErrForbidden) {
				t.Errorf("AuthorizeType(create, %d) for entity-%d authority holder: got %v, want ErrForbidden", typeID, typeID, err)
			}
			if err := integAZ.AuthorizeType(ctxU, "list", typeID); !errors.Is(err, authz.ErrForbidden) {
				t.Errorf("AuthorizeType(list, %d) for entity-%d authority holder: got %v, want ErrForbidden", typeID, typeID, err)
			}

			// A second, unrelated non-admin user is also forbidden.
			userV := seedUser(t, "typetarget-v-"+slug+"@example.com", false)
			if err := integAZ.AuthorizeType(actorCtx(userV), "create", typeID); !errors.Is(err, authz.ErrForbidden) {
				t.Errorf("AuthorizeType(create, %d) for unrelated user: got %v, want ErrForbidden", typeID, err)
			}
		})
	}
}

// TestInteg_TypeTarget_OwnershipArm_Deterministic forces the ownership arm
// regardless of database state: it creates a fresh corporation-typed entity
// owned by the user, then uses that entity's own (real) id as the "type id"
// passed to AuthorizeType. Whatever the id numerically is, AuthorizeType must
// not consult entity ownership, so the owner is forbidden, while Authorize
// (entity semantics) allows the same call.
func TestInteg_TypeTarget_OwnershipArm_Deterministic(t *testing.T) {
	userU := seedUser(t, "typetarget-own-u@example.com", false)
	ownedID := seedOwnedCorporation(t, userU, "Type Target Ownership Corp")
	ctxU := actorCtx(userU)

	if err := integAZ.Authorize(ctxU, "create", &ownedID); err != nil {
		t.Errorf("characterization: Authorize(create, &%d) as owner: got %v, want nil", ownedID, err)
	}
	if err := integAZ.AuthorizeType(ctxU, "create", ownedID); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("AuthorizeType(create, %d) where %d is an entity owned by the actor: got %v, want ErrForbidden", ownedID, ownedID, err)
	}
}

// TestInteg_TypeTarget_TargetedGrantArm_Deterministic forces the TargetChain
// arm regardless of database state: a targeted create grant over a freshly
// created entity E, then AuthorizeType with typeID == E.
func TestInteg_TypeTarget_TargetedGrantArm_Deterministic(t *testing.T) {
	userU := seedUser(t, "typetarget-grant-u@example.com", false)
	other := seedUser(t, "typetarget-grant-e@example.com", false)
	targetedGrant(t, userU, other, "create")
	ctxU := actorCtx(userU)

	if err := integAZ.Authorize(ctxU, "create", &other); err != nil {
		t.Errorf("characterization: Authorize(create, &%d) with targeted grant: got %v, want nil", other, err)
	}
	if err := integAZ.AuthorizeType(ctxU, "create", other); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("AuthorizeType(create, %d) with a targeted grant over entity %d: got %v, want ErrForbidden", other, other, err)
	}
}

// TestInteg_TypeTarget_WildcardHolders_Allowed verifies the only legitimate
// path: wildcard grants whose operation is in the requested operation's
// SatisfiedBy closure.
func TestInteg_TypeTarget_WildcardHolders_Allowed(t *testing.T) {
	typeID := typeIDForSlug(t, "authz_actor_group")

	manager := seedUser(t, "typetarget-wc-manage@example.com", true) // wildcard manage
	creator := seedUser(t, "typetarget-wc-create@example.com", false)
	seedWildcardGrant(t, creator, "create")
	reader := seedUser(t, "typetarget-wc-read@example.com", false)
	seedWildcardGrant(t, reader, "read")

	if err := integAZ.AuthorizeType(actorCtx(manager), "create", typeID); err != nil {
		t.Errorf("wildcard manage holder: AuthorizeType(create): got %v, want nil", err)
	}
	if err := integAZ.AuthorizeType(actorCtx(manager), "list", typeID); err != nil {
		t.Errorf("wildcard manage holder: AuthorizeType(list): got %v, want nil", err)
	}
	if err := integAZ.AuthorizeType(actorCtx(creator), "create", typeID); err != nil {
		t.Errorf("wildcard create holder: AuthorizeType(create): got %v, want nil", err)
	}

	// A wildcard create grant does not imply list; a wildcard read grant does
	// not imply create.
	if err := integAZ.AuthorizeType(actorCtx(creator), "list", typeID); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("wildcard create holder: AuthorizeType(list): got %v, want ErrForbidden", err)
	}
	if err := integAZ.AuthorizeType(actorCtx(reader), "create", typeID); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("wildcard read holder: AuthorizeType(create): got %v, want ErrForbidden", err)
	}

	// Fail closed on a non-positive type id even for a wildcard manager.
	for _, bad := range []int64{0, -1} {
		if err := integAZ.AuthorizeType(actorCtx(manager), "create", bad); !errors.Is(err, authz.ErrForbidden) {
			t.Errorf("wildcard manage holder: AuthorizeType(create, %d): got %v, want ErrForbidden", bad, err)
		}
	}

	// No actor on the context.
	if err := integAZ.AuthorizeType(context.Background(), "create", typeID); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("no actor: AuthorizeType(create): got %v, want ErrUnauthenticated", err)
	}
}
