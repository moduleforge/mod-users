//go:build integration

package authz_test

// type_target_integration_test.go pins the type-target confusion fix: a
// type-level authorization check ("may this actor create resources of type
// T?") used to be answered by entity-level ownership and grants, because every
// call site passed the types.id as the target of Authorize, which always
// interprets its target as an entities.id.
//
// types.id and entities.id are independent BIGSERIAL sequences that both start
// at 1, so Authorize(ctx, "create", &typeID) passes for any actor who owns the
// entity whose id happens to equal typeID, or holds a targeted grant over it.
// AuthorizeType must not be answered that way. It answers from wildcard grants
// and from grants on the exact type's entity (types.entity_id), held directly
// or through target groups; it never consults ownership and never compares
// typeID with an entity id.
//
// Since every type is an entity, the entity whose id equals a given types.id
// may be that type's own entity (a legitimate type-grant target) or another
// entity entirely. These tests resolve types.entity_id at runtime and never
// assume a layout.
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

// typeEntityIDForSlug resolves types.entity_id (the type's own entity) for slug.
func typeEntityIDForSlug(t *testing.T, slug string) int64 {
	t.Helper()
	var id int64
	if err := integPool.QueryRow(context.Background(), `SELECT entity_id FROM types WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("typeEntityIDForSlug(%q): %v", slug, err)
	}
	return id
}

// entityOwnerIsNull reports whether the entity with the given id has a NULL
// owner_id.
func entityOwnerIsNull(t *testing.T, id int64) bool {
	t.Helper()
	var unowned bool
	if err := integPool.QueryRow(context.Background(), `SELECT owner_id IS NULL FROM entities WHERE id = $1`, id).Scan(&unowned); err != nil {
		t.Fatalf("entityOwnerIsNull(%d): %v", id, err)
	}
	return unowned
}

// seedEntityWithExplicitID inserts a corporation-typed entity with an explicit
// id, then advances the entities id sequence so later inserts cannot collide
// with the explicit id. The sequence is never moved backwards. A nil ownerID
// leaves the entity unowned; otherwise owner_id is set at insert time (it is
// immutable afterwards).
func seedEntityWithExplicitID(t *testing.T, id int64, ownerID *int64) {
	t.Helper()
	ctx := context.Background()

	const insertSQL = `INSERT INTO entities (id, fundamental_type_id, owner_id) VALUES ($1, $2, $3)`
	if _, err := integPool.Exec(ctx, insertSQL, id, corporationTypeID(t), ownerID); err != nil {
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

// TestInteg_TypeTarget_EntityAuthorityDoesNotAnswerTypeCheck pins how
// AuthorizeType treats a create grant on "the entity whose id equals the
// types.id". Two cases are possible, resolved at runtime from types.entity_id:
//
//   - The entity at id == typeID is that type's own entity. The grant is then a
//     legitimate type grant: AuthorizeType allows create (and only create: it
//     does not imply list), and the type entity is unowned, so the allow cannot
//     come from an ownership arm.
//   - The entity at id == typeID is some other entity. The grant is then
//     entity-level authority over an unrelated entity (the confusion of a type id with an entity id) and
//     AuthorizeType must deny it.
//
// Which case applies depends on id layout, so the test logs it. The
// deterministic forms of both cases use a test-only type and belong to the
// integration matrix; the two tests after this one pin the unknown-type path.
func TestInteg_TypeTarget_EntityAuthorityDoesNotAnswerTypeCheck(t *testing.T) {
	for _, slug := range []string{"authz_actor_group", "natural_person"} {
		t.Run(slug, func(t *testing.T) {
			typeID := typeIDForSlug(t, slug)
			typeEntityID := typeEntityIDForSlug(t, slug)
			if !entityExists(t, typeID) {
				t.Skipf("no entity has id %d (= types.id of %q); nothing to confuse with the type id", typeID, slug)
			}
			userU := seedUser(t, "typetarget-u-"+slug+"@example.com", false)
			targetedGrant(t, userU, typeID, "create")
			ctxU := actorCtx(userU)

			ownType := typeEntityID == typeID
			if ownType {
				t.Logf("type %q: types.id=%d equals its own type entity id; the create grant on entity %d is a type grant", slug, typeID, typeID)
			} else {
				t.Logf("type %q: types.id=%d, types.entity_id=%d; the create grant on entity %d is entity-level authority over an unrelated entity", slug, typeID, typeEntityID, typeID)
			}

			// Characterization: Authorize treats its target as an entities.id by
			// contract, so U legitimately passes for entity typeID.
			if err := integAZ.Authorize(ctxU, "create", &typeID); err != nil {
				t.Errorf("characterization: Authorize(create, &%d) with a grant on entity %d: got %v, want nil (entity semantics must not change)", typeID, typeID, err)
			}

			if ownType {
				if !entityOwnerIsNull(t, typeID) {
					t.Errorf("type entity %d must be unowned", typeID)
				}
				if err := integAZ.AuthorizeType(ctxU, "create", typeID); err != nil {
					t.Errorf("AuthorizeType(create, %d) with a create grant on the type's own entity: got %v, want nil", typeID, err)
				}
				// create does not imply list.
				if err := integAZ.AuthorizeType(ctxU, "list", typeID); !errors.Is(err, authz.ErrForbidden) {
					t.Errorf("AuthorizeType(list, %d) with only a create type grant: got %v, want ErrForbidden", typeID, err)
				}
			} else {
				if err := integAZ.AuthorizeType(ctxU, "create", typeID); !errors.Is(err, authz.ErrForbidden) {
					t.Errorf("AuthorizeType(create, %d) with a grant on unrelated entity %d: got %v, want ErrForbidden", typeID, typeID, err)
				}
				if err := integAZ.AuthorizeType(ctxU, "list", typeID); !errors.Is(err, authz.ErrForbidden) {
					t.Errorf("AuthorizeType(list, %d) with a grant on unrelated entity %d: got %v, want ErrForbidden", typeID, typeID, err)
				}
			}

			// A second, unrelated non-admin user holds nothing and is forbidden.
			userV := seedUser(t, "typetarget-v-"+slug+"@example.com", false)
			if err := integAZ.AuthorizeType(actorCtx(userV), "create", typeID); !errors.Is(err, authz.ErrForbidden) {
				t.Errorf("AuthorizeType(create, %d) for unrelated user: got %v, want ErrForbidden", typeID, err)
			}
		})
	}
}

// requireNoTypeRow fails the test unless no types row has the given id. The
// two deterministic tests below pass an entity id as the type id; this pins
// their precondition so they cannot silently start testing something else (for
// example if types and entities ids ever came to overlap at that value).
func requireNoTypeRow(t *testing.T, id int64) {
	t.Helper()
	var none bool
	if err := integPool.QueryRow(context.Background(), `SELECT NOT EXISTS (SELECT 1 FROM types WHERE id = $1)`, id).Scan(&none); err != nil {
		t.Fatalf("requireNoTypeRow(%d): %v", id, err)
	}
	if !none {
		t.Fatalf("precondition: a types row has id %d; this test needs an id no type uses", id)
	}
}

// TestInteg_TypeTarget_OwnershipArm_Deterministic creates a fresh
// corporation-typed entity owned by the user, then uses that entity's own
// (real) id as the "type id" passed to AuthorizeType. It now exercises the
// unknown-type-id path: type entities consume low entity ids, and a fresh
// entity's id is beyond every types.id, so no types row has that id (asserted
// below). AuthorizeType must not consult entity ownership, so the owner is
// forbidden, while Authorize (entity semantics) allows the same call.
func TestInteg_TypeTarget_OwnershipArm_Deterministic(t *testing.T) {
	userU := seedUser(t, "typetarget-own-u@example.com", false)
	ownedID := seedOwnedCorporation(t, userU, "Type Target Ownership Corp")
	requireNoTypeRow(t, ownedID)
	ctxU := actorCtx(userU)

	if err := integAZ.Authorize(ctxU, "create", &ownedID); err != nil {
		t.Errorf("characterization: Authorize(create, &%d) as owner: got %v, want nil", ownedID, err)
	}
	if err := integAZ.AuthorizeType(ctxU, "create", ownedID); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("AuthorizeType(create, %d) where %d is an entity owned by the actor: got %v, want ErrForbidden", ownedID, ownedID, err)
	}
}

// TestInteg_TypeTarget_TargetedGrantArm_Deterministic gives the user a
// targeted create grant over a freshly created entity E, then calls
// AuthorizeType with typeID == E. Like the ownership test above, it exercises
// the unknown-type-id path: no types row has id E (asserted below), so the
// type-grant arm finds no type entity and denies.
func TestInteg_TypeTarget_TargetedGrantArm_Deterministic(t *testing.T) {
	userU := seedUser(t, "typetarget-grant-u@example.com", false)
	other := seedUser(t, "typetarget-grant-e@example.com", false)
	requireNoTypeRow(t, other)
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
