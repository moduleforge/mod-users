//go:build integration

package authz_test

// instance_semantics_integration_test.go proves the Q1 instance-semantics arm
// of entity-level Authorize (checkGrantOrOwn): a grant over a type's entity also
// confers the operation over every instance of exactly that type, and never
// over type entities themselves.
//
// This is security-sensitive: manage on natural_person's type entity confers
// assume of every user account. The tests pin that, the exact-type (Q2)
// boundary, and the type-entity exclusion (a grant on the sentinel "type"
// entity must not reach any type entity).
//
// Every fixture target group holds only type entities or only instance
// entities (mod-authz's kind trigger). Ids are resolved at runtime. Run with
// the throwaway-Postgres recipe in authz_integration_test.go's header.
//
// Symmetry gap: mod-core's matching GrantTableGenerator (list side) arm has not
// landed, so this file deliberately asserts no list/single-row symmetry for
// type-grant holders.

import (
	"context"
	"errors"
	"testing"

	"github.com/moduleforge/mod-users/api/internal/authz"
)

func requireAllowed(t *testing.T, ctx context.Context, op string, target int64, msg string) {
	t.Helper()
	if err := integAZ.Authorize(ctx, op, &target); err != nil {
		t.Errorf("%s: Authorize(%s, %d): got %v, want nil", msg, op, target, err)
	}
}

func requireForbidden(t *testing.T, ctx context.Context, op string, target int64, msg string) {
	t.Helper()
	if err := integAZ.Authorize(ctx, op, &target); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("%s: Authorize(%s, %d): got %v, want ErrForbidden", msg, op, target, err)
	}
}

func TestInteg_InstanceSemantics_Matrix(t *testing.T) {
	corpEnt := typeEntityIDForSlug(t, "corporation")
	npEnt := typeEntityIDForSlug(t, "natural_person")
	legalEnt := typeEntityIDForSlug(t, "legal_entity")
	sentinelEnt := typeEntityIDForSlug(t, "type")
	corp := seedUnownedCorporation(t, "Instance Semantics Corp")
	otherUser := seedGrantHolder(t, "is-other-user")

	t.Run("read on type entity reads instances of exactly that type", func(t *testing.T) {
		u := seedGrantHolder(t, "is-read")
		targetedGrant(t, u, corpEnt, "read")
		ctx := actorCtx(u)
		requireAllowed(t, ctx, "read", corp, "read on corporation's type entity")
		requireForbidden(t, ctx, "update", corp, "read does not imply update")
		requireForbidden(t, ctx, "read", otherUser, "natural_person instance is a different type")
	})

	t.Run("list implies read", func(t *testing.T) {
		u := seedGrantHolder(t, "is-list")
		targetedGrant(t, u, corpEnt, "list")
		requireAllowed(t, actorCtx(u), "read", corp, "list on corporation's type entity")
	})

	t.Run("manage on natural_person type entity confers assume of any user", func(t *testing.T) {
		u := seedGrantHolder(t, "is-manage-np")
		targetedGrant(t, u, npEnt, "manage")
		ctx := actorCtx(u)
		requireAllowed(t, ctx, "assume", otherUser, "manage on natural_person's type entity")
		requireAllowed(t, ctx, "grant", otherUser, "manage implies grant over every instance")
		requireForbidden(t, ctx, "assume", corp, "natural_person grant does not reach a corporation")
	})

	t.Run("type-only target group", func(t *testing.T) {
		u := seedGrantHolder(t, "is-tgroup")
		g := seedTargetGroup(t, "instsem-tg")
		addTargetGroupMember(t, g, corpEnt)
		targetedGrant(t, u, g, "read")
		ctx := actorCtx(u)
		requireAllowed(t, ctx, "read", corp, "read on a type-only group holding corporation's entity")
		requireForbidden(t, ctx, "read", otherUser, "group does not hold natural_person's entity")
	})

	t.Run("nested type-only target groups", func(t *testing.T) {
		u := seedGrantHolder(t, "is-tgroup-nested")
		inner := seedTargetGroup(t, "instsem-tg-inner")
		outer := seedTargetGroup(t, "instsem-tg-outer")
		addTargetGroupMember(t, inner, corpEnt)
		addTargetGroupMember(t, outer, inner)
		targetedGrant(t, u, outer, "read")
		requireAllowed(t, actorCtx(u), "read", corp, "read on the outer group")
	})

	t.Run("Q2 no parent walk", func(t *testing.T) {
		u := seedGrantHolder(t, "is-q2")
		targetedGrant(t, u, legalEnt, "read")
		ctx := actorCtx(u)
		requireForbidden(t, ctx, "read", corp, "read on legal_entity, instance of corporation")
		requireForbidden(t, ctx, "read", otherUser, "read on legal_entity, instance of natural_person")
	})

	t.Run("no child walk", func(t *testing.T) {
		u := seedGrantHolder(t, "is-nochild")
		targetedGrant(t, u, corpEnt, "read")
		// The grant on the subtype does not confer anything over the parent
		// type's type entity or instances of the parent type.
		requireForbidden(t, actorCtx(u), "read", legalEnt, "corporation grant, legal_entity's type entity")
	})

	t.Run("actor group", func(t *testing.T) {
		u := seedGrantHolder(t, "is-agroup")
		g := seedActorGroup(t, "instsem-agroup")
		addActorGroupMember(t, g, u)
		targetedGrant(t, g, corpEnt, "read")
		requireAllowed(t, actorCtx(u), "read", corp, "grant held by the actor group")
		bystander := seedGrantHolder(t, "is-agroup-bystander")
		requireForbidden(t, actorCtx(bystander), "read", corp, "non-member")
	})

	t.Run("type entities are excluded as targets", func(t *testing.T) {
		u := seedGrantHolder(t, "is-sentinel")
		targetedGrant(t, u, sentinelEnt, "grant")
		targetedGrant(t, u, sentinelEnt, "read")
		ctx := actorCtx(u)
		requireForbidden(t, ctx, "grant", corpEnt, "grant on the sentinel type's entity, target corporation's type entity")
		requireForbidden(t, ctx, "read", corpEnt, "read on the sentinel type's entity, target corporation's type entity")
		requireForbidden(t, ctx, "read", npEnt, "read on the sentinel type's entity, target natural_person's type entity")
		// The sentinel entity itself is a type entity, so it is excluded too,
		// but a direct grant on it still applies to it (existing path).
		requireAllowed(t, ctx, "read", sentinelEnt, "direct grant on the sentinel's own entity")
	})

	t.Run("manage on the sentinel type entity confers nothing over type entities", func(t *testing.T) {
		u := seedGrantHolder(t, "is-sentinel-manage")
		targetedGrant(t, u, sentinelEnt, "manage")
		ctx := actorCtx(u)
		for name, ent := range map[string]int64{"corporation": corpEnt, "natural_person": npEnt, "legal_entity": legalEnt} {
			for _, op := range []string{"read", "grant", "manage"} {
				requireForbidden(t, ctx, op, ent, op+" over type entity "+name)
			}
		}
	})

	t.Run("direct grant on a type entity still applies to it", func(t *testing.T) {
		u := seedGrantHolder(t, "is-direct-type")
		targetedGrant(t, u, corpEnt, "read")
		requireAllowed(t, actorCtx(u), "read", corpEnt, "direct read grant, target is the type entity")
	})

	t.Run("instance grant never satisfies a type-level check", func(t *testing.T) {
		u := seedGrantHolder(t, "is-inst-type")
		targetedGrant(t, u, corp, "create")
		targetedGrant(t, u, corp, "list")
		ctx := actorCtx(u)
		requireTypeForbidden(t, ctx, "create", typeIDForSlug(t, "corporation"), "instance grant, AuthorizeType(create)")
		requireTypeForbidden(t, ctx, "list", typeIDForSlug(t, "corporation"), "instance grant, AuthorizeType(list)")
	})

	t.Run("type grant stays type-level and instance-level only for its exact type", func(t *testing.T) {
		u := seedGrantHolder(t, "is-type-both")
		targetedGrant(t, u, corpEnt, "create")
		ctx := actorCtx(u)
		requireTypeAllowed(t, ctx, "create", typeIDForSlug(t, "corporation"), "type-level check")
		requireAllowed(t, ctx, "create", corp, "same grant now confers create over instances")
	})

	t.Run("target id with no entities row", func(t *testing.T) {
		u := seedGrantHolder(t, "is-missing")
		targetedGrant(t, u, corpEnt, "manage")
		var maxID int64
		if err := integPool.QueryRow(context.Background(), `SELECT max(id) FROM entities`).Scan(&maxID); err != nil {
			t.Fatalf("max(entities.id): %v", err)
		}
		requireForbidden(t, actorCtx(u), "read", maxID+1_000_000, "no entities row")
	})

	t.Run("nil target stays wildcard-only", func(t *testing.T) {
		u := seedGrantHolder(t, "is-nil")
		targetedGrant(t, u, corpEnt, "manage")
		if err := integAZ.Authorize(actorCtx(u), "read", nil); !errors.Is(err, authz.ErrForbidden) {
			t.Errorf("nil target, type-grant holder: got %v, want ErrForbidden", err)
		}
	})

	t.Run("revocation", func(t *testing.T) {
		u := seedGrantHolder(t, "is-revoke")
		targetedGrant(t, u, corpEnt, "read")
		requireAllowed(t, actorCtx(u), "read", corp, "before revocation")
		deleteTargetedGrant(t, u, corpEnt, "read")
		requireForbidden(t, actorCtx(u), "read", corp, "after revocation")
	})
}

// TestInteg_InstanceSemantics_TestOnlyType proves the seed joins through
// types.entity_id and not types.id: the test type's types.id differs from its
// type entity's id, and a corporation instance placed at id == types.id is not
// covered by a grant on the test type's entity.
func TestInteg_InstanceSemantics_TestOnlyType(t *testing.T) {
	testTypeID, testTypeEntity := registerTestType(t, "instsem")

	var instance int64
	if err := integPool.QueryRow(context.Background(),
		`INSERT INTO entities (fundamental_type_id) VALUES ($1) RETURNING id`, testTypeID).Scan(&instance); err != nil {
		t.Fatalf("insert instance of test type: %v", err)
	}

	// A corporation instance at id == testType.types.id.
	seedEntityWithExplicitID(t, testTypeID, nil)

	u := seedGrantHolder(t, "is-testtype")
	targetedGrant(t, u, testTypeEntity, "read")
	ctx := actorCtx(u)

	requireAllowed(t, ctx, "read", instance, "instance of the test type")
	requireForbidden(t, ctx, "read", testTypeID, "corporation instance at id == testType.types.id")
	requireForbidden(t, ctx, "update", instance, "read does not imply update")
	// The test type's type entity is itself a type entity: direct grant only.
	requireAllowed(t, ctx, "read", testTypeEntity, "direct grant on the type entity")

	// Control: a grant on the instance at id == types.id is entity-level and
	// does not answer the type-level check (unchanged by this arm).
	v := seedGrantHolder(t, "is-testtype-inst")
	targetedGrant(t, v, testTypeID, "create")
	requireTypeForbidden(t, actorCtx(v), "create", testTypeID, "instance grant at id == types.id")
}
