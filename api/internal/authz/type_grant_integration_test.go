//go:build integration

package authz_test

// type_grant_integration_test.go proves the AuthorizeType type-grant arm
// against the real composed schema (mod-core type entities, mod-authz's
// target-group kind trigger, mod-users). It holds the helpers, the
// kind-separation guard, the full matrix, and the
// type-id-never-read-as-an-entity-id regression in its test-only-type form.
//
// Every fixture target group holds only type entities or only instance
// entities, counting nested groups (mod-authz's trg_target_group_members_kind
// rejects a mixed group with SQLSTATE P0001). Test types are append-only and
// persist for the whole run, so every registered slug is unique per call.
//
// Ids are resolved at runtime and never hard-coded. Fixtures insert through SQL
// or the sqlc queries, never through mod-authz's services.
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/internal/authz"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var testTypeCounter atomic.Int64

// seedTargetGroup inserts entity -> authz_target_groups and returns the group's
// entity id.
func seedTargetGroup(t *testing.T, name string) int64 {
	t.Helper()
	ctx := context.Background()

	var id int64
	const entitySQL = `INSERT INTO entities (fundamental_type_id) VALUES ($1) RETURNING id`
	if err := integPool.QueryRow(ctx, entitySQL, typeIDForSlug(t, "authz_target_group")).Scan(&id); err != nil {
		t.Fatalf("seedTargetGroup(%q): insert entity: %v", name, err)
	}
	const groupSQL = `INSERT INTO authz_target_groups (entity_id, name) VALUES ($1, $2)`
	if _, err := integPool.Exec(ctx, groupSQL, id, name); err != nil {
		t.Fatalf("seedTargetGroup(%q): insert group: %v", name, err)
	}
	return id
}

// addTargetGroupMemberErr inserts a target-group membership and returns the
// raw error (for the kind-guard test).
func addTargetGroupMemberErr(groupID, memberID int64) error {
	const sql = `INSERT INTO authz_target_group_members (group_id, member_id) VALUES ($1, $2)`
	_, err := integPool.Exec(context.Background(), sql, groupID, memberID)
	return err
}

func addTargetGroupMember(t *testing.T, groupID, memberID int64) {
	t.Helper()
	if err := addTargetGroupMemberErr(groupID, memberID); err != nil {
		t.Fatalf("addTargetGroupMember(group=%d, member=%d): %v", groupID, memberID, err)
	}
}

func addActorGroupMember(t *testing.T, groupID, memberID int64) {
	t.Helper()
	const sql = `INSERT INTO authz_actor_group_members (group_id, member_id) VALUES ($1, $2)`
	if _, err := integPool.Exec(context.Background(), sql, groupID, memberID); err != nil {
		t.Fatalf("addActorGroupMember(group=%d, member=%d): %v", groupID, memberID, err)
	}
}

// deleteTargetedGrant removes a targeted grant (actor, op, target).
func deleteTargetedGrant(t *testing.T, actorID, targetID int64, operationSlug string) {
	t.Helper()
	const sql = `
DELETE FROM grants
WHERE actor_id = $1 AND target_id = $2
  AND operation_id = (SELECT id FROM authz_operations WHERE slug = $3)`
	tag, err := integPool.Exec(context.Background(), sql, actorID, targetID, operationSlug)
	if err != nil {
		t.Fatalf("deleteTargetedGrant: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("deleteTargetedGrant(actor=%d, target=%d, %s): deleted %d rows, want 1", actorID, targetID, operationSlug, tag.RowsAffected())
	}
}

// registerTestType registers a concrete test-only type under 'entity' (or, via
// registerTestTypeUnder, under a named parent) with a
// unique slug derived from slugHint, after advancing the types id sequence well
// past max(entities.id) (never backwards). The new type's types.id therefore
// cannot be the id of any existing entity, and differs from its type entity's
// id; both are asserted. It returns types.id and types.entity_id.
func registerTestType(t *testing.T, slugHint string) (typeID, typeEntityID int64) {
	t.Helper()
	return registerTestTypeUnder(t, slugHint, "entity")
}

// registerTestTypeUnder is registerTestType with an explicit parent type,
// named by slug. The parent may itself be concrete (for example another test
// type), which lets a test build a concrete parent and a concrete child.
func registerTestTypeUnder(t *testing.T, slugHint, parentSlug string) (typeID, typeEntityID int64) {
	t.Helper()
	ctx := context.Background()

	var seq string
	if err := integPool.QueryRow(ctx, `SELECT pg_get_serial_sequence('types', 'id')`).Scan(&seq); err != nil {
		t.Fatalf("registerTestType: resolve types sequence: %v", err)
	}
	// seq is catalog-derived (never caller input), so formatting it is safe.
	advanceSQL := fmt.Sprintf(
		`SELECT setval($1::regclass, GREATEST((SELECT max(id) FROM entities) + 1000, (SELECT last_value FROM %s)))`, seq)
	var ignored int64
	if err := integPool.QueryRow(ctx, advanceSQL, seq).Scan(&ignored); err != nil {
		t.Fatalf("registerTestType: advance types sequence: %v", err)
	}

	slug := fmt.Sprintf("itest_%s_%d", slugHint, testTypeCounter.Add(1))
	const insertSQL = `
INSERT INTO types (slug, parent_id, concrete, name, description)
SELECT $1, id, true, $1, 'test-only type' FROM types WHERE slug = $2
RETURNING id`
	if err := integPool.QueryRow(ctx, insertSQL, slug, parentSlug).Scan(&typeID); err != nil {
		t.Fatalf("registerTestType(%q): insert type: %v", slug, err)
	}
	typeEntityID = typeEntityIDForSlug(t, slug)
	if typeID == typeEntityID {
		t.Fatalf("registerTestType(%q): types.id and types.entity_id are both %d; the type-id-as-entity-id fixtures need them to differ", slug, typeID)
	}
	return typeID, typeEntityID
}

// requireTypeAllowed and requireTypeForbidden assert one AuthorizeType outcome.
func requireTypeAllowed(t *testing.T, ctx context.Context, op string, typeID int64, msg string) {
	t.Helper()
	if err := integAZ.AuthorizeType(ctx, op, typeID); err != nil {
		t.Errorf("%s: AuthorizeType(%s, %d): got %v, want nil", msg, op, typeID, err)
	}
}

func requireTypeForbidden(t *testing.T, ctx context.Context, op string, typeID int64, msg string) {
	t.Helper()
	if err := integAZ.AuthorizeType(ctx, op, typeID); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("%s: AuthorizeType(%s, %d): got %v, want ErrForbidden", msg, op, typeID, err)
	}
}

// seedGrantHolder seeds a fresh non-admin user.
func seedGrantHolder(t *testing.T, tag string) int64 {
	t.Helper()
	return seedUser(t, "typegrant-"+tag+"-"+fmt.Sprint(testTypeCounter.Add(1))+"@example.com", false)
}

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// ---------------------------------------------------------------------------
// Kind-separation guard
// ---------------------------------------------------------------------------

// TestInteg_TypeGrant_KindTriggerGuard proves the composed schema carries
// mod-authz's kind trigger: a type-only target group refuses an instance
// member with SQLSTATE P0001. It also pins the reverse (an instance group
// refuses a type entity), and that nesting counts.
func TestInteg_TypeGrant_KindTriggerGuard(t *testing.T) {
	corpTypeEntity := typeEntityIDForSlug(t, "corporation")
	instance := seedGrantHolder(t, "kind-instance")

	typeGroup := seedTargetGroup(t, "kind-type-only")
	addTargetGroupMember(t, typeGroup, corpTypeEntity)
	if err := addTargetGroupMemberErr(typeGroup, instance); sqlState(err) != "P0001" {
		t.Errorf("instance into a type-only group: got %v (SQLSTATE %q), want SQLSTATE P0001", err, sqlState(err))
	}

	instGroup := seedTargetGroup(t, "kind-instance-only")
	addTargetGroupMember(t, instGroup, instance)
	if err := addTargetGroupMemberErr(instGroup, corpTypeEntity); sqlState(err) != "P0001" {
		t.Errorf("type entity into an instance-only group: got %v (SQLSTATE %q), want SQLSTATE P0001", err, sqlState(err))
	}

	// Nesting counts: an instance-only group cannot join a type-only group.
	if err := addTargetGroupMemberErr(typeGroup, instGroup); sqlState(err) != "P0001" {
		t.Errorf("instance-only group into a type-only group: got %v (SQLSTATE %q), want SQLSTATE P0001", err, sqlState(err))
	}
}

// ---------------------------------------------------------------------------
// Matrix
// ---------------------------------------------------------------------------

func TestInteg_TypeGrant_Matrix(t *testing.T) {
	corpID := typeIDForSlug(t, "corporation")
	npID := typeIDForSlug(t, "natural_person")
	corpEnt := typeEntityIDForSlug(t, "corporation")
	npEnt := typeEntityIDForSlug(t, "natural_person")

	t.Run("direct create grant", func(t *testing.T) {
		u := seedGrantHolder(t, "direct")
		targetedGrant(t, u, corpEnt, "create")
		ctx := actorCtx(u)
		requireTypeAllowed(t, ctx, "create", corpID, "create on corporation's type entity")
		requireTypeForbidden(t, ctx, "list", corpID, "create does not imply list")
		requireTypeForbidden(t, ctx, "create", npID, "a different type")
	})

	t.Run("manage closure", func(t *testing.T) {
		u := seedGrantHolder(t, "manage")
		targetedGrant(t, u, corpEnt, "manage")
		ctx := actorCtx(u)
		requireTypeAllowed(t, ctx, "create", corpID, "manage implies create")
		requireTypeAllowed(t, ctx, "list", corpID, "manage implies list")
		requireTypeForbidden(t, ctx, "create", npID, "manage on corporation does not cover natural_person")
	})

	t.Run("read does not imply create", func(t *testing.T) {
		u := seedGrantHolder(t, "read")
		targetedGrant(t, u, corpEnt, "read")
		requireTypeForbidden(t, actorCtx(u), "create", corpID, "read on corporation's type entity")
	})

	t.Run("actor group nested two levels", func(t *testing.T) {
		u := seedGrantHolder(t, "agroup")
		inner := seedActorGroup(t, "typegrant-inner")
		outer := seedActorGroup(t, "typegrant-outer")
		addActorGroupMember(t, inner, u)
		addActorGroupMember(t, outer, inner)
		targetedGrant(t, outer, corpEnt, "create")
		requireTypeAllowed(t, actorCtx(u), "create", corpID, "grant held by the outer actor group")
		bystander := seedGrantHolder(t, "agroup-bystander")
		requireTypeForbidden(t, actorCtx(bystander), "create", corpID, "non-member of the group")
	})

	t.Run("type-only target group", func(t *testing.T) {
		u := seedGrantHolder(t, "tgroup")
		g := seedTargetGroup(t, "typegrant-tg")
		addTargetGroupMember(t, g, corpEnt)
		targetedGrant(t, u, g, "create")
		ctx := actorCtx(u)
		requireTypeAllowed(t, ctx, "create", corpID, "grant on a group containing the type entity")
		requireTypeForbidden(t, ctx, "create", npID, "group does not contain natural_person's entity")
		requireTypeForbidden(t, ctx, "list", corpID, "create does not imply list through a group")
	})

	t.Run("nested type-only target groups", func(t *testing.T) {
		u := seedGrantHolder(t, "tgroup-nested")
		inner := seedTargetGroup(t, "typegrant-tg-inner")
		outer := seedTargetGroup(t, "typegrant-tg-outer")
		addTargetGroupMember(t, inner, corpEnt)
		addTargetGroupMember(t, outer, inner)
		targetedGrant(t, u, outer, "create")
		requireTypeAllowed(t, actorCtx(u), "create", corpID, "grant on the outer group")
	})

	t.Run("type-only group without this type", func(t *testing.T) {
		u := seedGrantHolder(t, "tgroup-miss")
		g := seedTargetGroup(t, "typegrant-tg-miss")
		addTargetGroupMember(t, g, npEnt)
		targetedGrant(t, u, g, "create")
		requireTypeForbidden(t, actorCtx(u), "create", corpID, "group holds only natural_person's entity")
		requireTypeAllowed(t, actorCtx(u), "create", npID, "control: the group does hold natural_person's entity")
	})

	t.Run("no parent type walk", func(t *testing.T) {
		legalEnt := typeEntityIDForSlug(t, "legal_entity")
		entEnt := typeEntityIDForSlug(t, "entity")

		u := seedGrantHolder(t, "noparent-legal")
		targetedGrant(t, u, legalEnt, "create")
		requireTypeForbidden(t, actorCtx(u), "create", npID, "create on legal_entity, checking natural_person")
		requireTypeForbidden(t, actorCtx(u), "create", corpID, "create on legal_entity, checking corporation")

		v := seedGrantHolder(t, "noparent-entity")
		targetedGrant(t, v, entEnt, "create")
		requireTypeForbidden(t, actorCtx(v), "create", corpID, "create on entity, checking corporation")

		w := seedGrantHolder(t, "noparent-reverse")
		targetedGrant(t, w, npEnt, "create")
		requireTypeForbidden(t, actorCtx(w), "create", typeIDForSlug(t, "legal_entity"), "create on natural_person, checking legal_entity")
		requireTypeAllowed(t, actorCtx(w), "create", npID, "control: the exact type")
	})

	t.Run("sudo effective actor", func(t *testing.T) {
		holder := seedGrantHolder(t, "sudo-holder")
		other := seedGrantHolder(t, "sudo-other")
		targetedGrant(t, holder, corpEnt, "create")

		realHolds := opctx.WithSudoActor(actorCtx(holder), other)
		requireTypeForbidden(t, realHolds, "create", corpID, "real actor holds the grant, sudo actor does not")

		sudoHolds := opctx.WithSudoActor(actorCtx(other), holder)
		requireTypeAllowed(t, sudoHolds, "create", corpID, "sudo actor holds the grant")
	})

	t.Run("unknown type id", func(t *testing.T) {
		u := seedGrantHolder(t, "unknown")
		targetedGrant(t, u, corpEnt, "manage")
		var maxID int64
		if err := integPool.QueryRow(context.Background(), `SELECT max(id) FROM types`).Scan(&maxID); err != nil {
			t.Fatalf("max(types.id): %v", err)
		}
		requireTypeForbidden(t, actorCtx(u), "create", maxID+1000, "unknown type id")
	})

	t.Run("deprecated type", func(t *testing.T) {
		typeID, typeEntity := registerTestType(t, "deprecated")
		u := seedGrantHolder(t, "deprecated")
		targetedGrant(t, u, typeEntity, "create")
		targetedGrant(t, u, typeEntity, "list")

		if _, err := integPool.Exec(context.Background(), `UPDATE types SET deprecated_at = now() WHERE id = $1`, typeID); err != nil {
			t.Fatalf("deprecate type %d: %v", typeID, err)
		}
		ctx := actorCtx(u)
		requireTypeAllowed(t, ctx, "create", typeID, "deprecated type, type-grant holder")
		requireTypeAllowed(t, ctx, "list", typeID, "deprecated type, type-grant holder")

		// The write stays blocked by the data layer (mod-core's MF001).
		_, err := integPool.Exec(context.Background(), `INSERT INTO entities (fundamental_type_id) VALUES ($1)`, typeID)
		if sqlState(err) != "MF001" {
			t.Errorf("insert entity of deprecated type: got %v (SQLSTATE %q), want SQLSTATE MF001", err, sqlState(err))
		}
	})

	t.Run("revocation", func(t *testing.T) {
		u := seedGrantHolder(t, "revoke")
		targetedGrant(t, u, corpEnt, "create")
		requireTypeAllowed(t, actorCtx(u), "create", corpID, "before revocation")
		deleteTargetedGrant(t, u, corpEnt, "create")
		requireTypeForbidden(t, actorCtx(u), "create", corpID, "after revocation")
	})

	t.Run("wildcard holders unchanged", func(t *testing.T) {
		manager := seedUser(t, "typegrant-wc-manage@example.com", true)
		creator := seedGrantHolder(t, "wc-create")
		seedWildcardGrant(t, creator, "create")

		requireTypeAllowed(t, actorCtx(manager), "create", corpID, "wildcard manage")
		requireTypeAllowed(t, actorCtx(manager), "list", corpID, "wildcard manage")
		requireTypeAllowed(t, actorCtx(creator), "create", corpID, "wildcard create")
		requireTypeForbidden(t, actorCtx(creator), "list", corpID, "wildcard create does not imply list")
	})

	t.Run("ownership never applies", func(t *testing.T) {
		owner := seedGrantHolder(t, "owner")
		ownedID := seedOwnedCorporation(t, owner, "Type Grant Ownership Corp")
		if err := integAZ.Authorize(actorCtx(owner), "create", &ownedID); err != nil {
			t.Fatalf("characterization: Authorize(create, &%d) as owner: got %v, want nil", ownedID, err)
		}
		requireTypeForbidden(t, actorCtx(owner), "create", corpID, "owner of an instance of corporation")
		requireTypeForbidden(t, actorCtx(owner), "list", corpID, "owner of an instance of corporation")
	})
}

// ---------------------------------------------------------------------------
// Type id never read as an entity id, test-only-type form
// ---------------------------------------------------------------------------

// TestInteg_TypeGrant_TypeIDNeverReadAsEntityID_TestOnlyType places a corporation instance at
// id == testType.types.id and shows that grants and ownership on that
// instance never answer AuthorizeType, while grants on the test type's own
// type entity do.
func TestInteg_TypeGrant_TypeIDNeverReadAsEntityID_TestOnlyType(t *testing.T) {
	testTypeID, testTypeEntity := registerTestType(t, "typeid")

	owner := seedGrantHolder(t, "typeid-owner")
	// Instance at id == types.id of the test type, owned by owner from insert.
	seedEntityWithExplicitID(t, testTypeID, &owner)
	if entityOwnerIsNull(t, testTypeID) {
		t.Fatalf("precondition: instance %d should be owned", testTypeID)
	}
	if testTypeEntity == testTypeID {
		t.Fatalf("precondition: type entity must differ from types.id")
	}

	direct := seedGrantHolder(t, "typeid-direct")
	viaGroup := seedGrantHolder(t, "typeid-group")
	typeHolder := seedGrantHolder(t, "typeid-typeholder")

	for _, op := range []string{"create", "list"} {
		targetedGrant(t, direct, testTypeID, op)
		targetedGrant(t, typeHolder, testTypeEntity, op)
	}
	instGroup := seedTargetGroup(t, "typeid-instance-only")
	addTargetGroupMember(t, instGroup, testTypeID)
	for _, op := range []string{"create", "list"} {
		targetedGrant(t, viaGroup, instGroup, op)
	}

	for name, actor := range map[string]int64{"direct grant": direct, "instance-only group grant": viaGroup, "ownership": owner} {
		ctx := actorCtx(actor)
		for _, op := range []string{"create", "list"} {
			requireTypeForbidden(t, ctx, op, testTypeID, "instance authority via "+name)
		}
	}

	// Control: Authorize treats its target as an entities.id, so it admits all
	// three actors for the instance. This is why call sites must never pass
	// &typeID.
	for name, actor := range map[string]int64{"direct grant": direct, "instance-only group grant": viaGroup, "ownership": owner} {
		for _, op := range []string{"create", "list"} {
			id := testTypeID
			if err := integAZ.Authorize(actorCtx(actor), op, &id); err != nil {
				t.Errorf("control via %s: Authorize(%s, &%d): got %v, want nil", name, op, id, err)
			}
		}
	}

	// The type entity's holder is admitted: the arm reads types.entity_id.
	for _, op := range []string{"create", "list"} {
		requireTypeAllowed(t, actorCtx(typeHolder), op, testTypeID, "grant on the test type's type entity")
	}
	// ... and does not gain instance authority from it.
	if err := integAZ.Authorize(actorCtx(typeHolder), "create", &testTypeID); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("type-entity holder: Authorize(create, &%d) on the unrelated instance: got %v, want ErrForbidden", testTypeID, err)
	}
}
