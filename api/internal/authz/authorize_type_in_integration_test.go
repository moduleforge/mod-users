//go:build integration

package authz_test

// authorize_type_in_integration_test.go proves AuthorizeTypeIn(op, typeID,
// ownerID), the owner-scoped type-level check behind create and list within an
// owner, against the real composed schema. Each arm is exercised in isolation,
// with an actor that holds only that arm's authority:
//
//	wildcard                     TestInteg_AuthorizeTypeIn_Wildcard
//	exact type-entity grant      TestInteg_AuthorizeTypeIn_TypeEntityGrant
//	supertype type-entity grant  TestInteg_AuthorizeTypeIn_TypeEntityGrant (no parent walk)
//	owned scope, exact           TestInteg_AuthorizeTypeIn_OwnedScope
//	owned scope, include_subtypes TestInteg_AuthorizeTypeIn_OwnedScope
//	own arm, ownerID = / != own subject  TestInteg_AuthorizeTypeIn_OwnArm
//	deny                         TestInteg_AuthorizeTypeIn_Deny
//
// A grant on the entity of a supertype does not pass for a subtype: the engine
// matches the exact type's entity (types.entity_id of typeID) and walks no
// parent types, as AuthorizeType does.
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"errors"
	"testing"

	"github.com/moduleforge/core-api/apiresp"
)

func requireTypeInAllowed(t *testing.T, ctx context.Context, op string, typeID, ownerID int64, msg string) {
	t.Helper()
	if err := integAZ.AuthorizeTypeIn(ctx, op, typeID, ownerID); err != nil {
		t.Errorf("%s: AuthorizeTypeIn(%s, type %d, owner %d): got %v, want nil", msg, op, typeID, ownerID, err)
	}
}

func requireTypeInForbidden(t *testing.T, ctx context.Context, op string, typeID, ownerID int64, msg string) {
	t.Helper()
	if err := integAZ.AuthorizeTypeIn(ctx, op, typeID, ownerID); !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("%s: AuthorizeTypeIn(%s, type %d, owner %d): got %v, want ErrForbidden", msg, op, typeID, ownerID, err)
	}
}

func TestInteg_AuthorizeTypeIn_Wildcard(t *testing.T) {
	ty := registerOwnedTypes(t)
	ownerA := seedOrg(t, "TypeIn Wildcard Owner A")
	ownerB := seedOrg(t, "TypeIn Wildcard Owner B")

	creator := seedOperator(t, "create")
	ctx := actorCtx(creator)
	for _, typeID := range []int64{ty.parent, ty.child, ty.other} {
		for _, owner := range []int64{ownerA, ownerB} {
			requireTypeInAllowed(t, ctx, "create", typeID, owner, "wildcard create, any type under any owner")
		}
	}
	requireTypeInForbidden(t, ctx, "list", ty.parent, ownerA, "wildcard create does not imply list")

	manager := seedOperator(t, "manage")
	requireTypeInAllowed(t, actorCtx(manager), "create", ty.parent, ownerA, "wildcard manage implies create")
	requireTypeInAllowed(t, actorCtx(manager), "list", ty.child, ownerB, "wildcard manage implies list")
}

func TestInteg_AuthorizeTypeIn_TypeEntityGrant(t *testing.T) {
	ty := registerOwnedTypes(t)
	parentSlug := typeSlugForID(t, ty.parent)
	ownerA := seedOrg(t, "TypeIn TypeEntity Owner A")
	ownerB := seedOrg(t, "TypeIn TypeEntity Owner B")

	t.Run("exact type, any owner", func(t *testing.T) {
		holder := seedOperator(t)
		targetedGrant(t, holder, typeEntityIDForSlug(t, parentSlug), "create")
		ctx := actorCtx(holder)
		requireTypeInAllowed(t, ctx, "create", ty.parent, ownerA, "type-entity grant, owner A")
		requireTypeInAllowed(t, ctx, "create", ty.parent, ownerB, "type-entity grant, owner B")
		requireTypeInForbidden(t, ctx, "create", ty.child, ownerA, "type-entity grant on the parent, subtype typeID")
		requireTypeInForbidden(t, ctx, "create", ty.other, ownerA, "type-entity grant, unrelated type")
		requireTypeInForbidden(t, ctx, "list", ty.parent, ownerA, "create does not imply list")
	})

	t.Run("supertype grant does not pass", func(t *testing.T) {
		holder := seedOperator(t)
		targetedGrant(t, holder, typeEntityIDForSlug(t, "legal_entity"), "manage")
		targetedGrant(t, holder, typeEntityIDForSlug(t, "entity"), "manage")
		ctx := actorCtx(holder)
		requireTypeInForbidden(t, ctx, "create", ty.other, ownerA, "manage on legal_entity's entity, organization typeID")
		requireTypeInForbidden(t, ctx, "create", ty.parent, ownerA, "manage on entity's entity, test type typeID")
		// The grant does cover its own exact type.
		requireTypeInAllowed(t, ctx, "create", typeIDForSlug(t, "legal_entity"), ownerA, "control: the exact type legal_entity")
	})

	t.Run("subtype grant does not climb", func(t *testing.T) {
		holder := seedOperator(t)
		targetedGrant(t, holder, typeEntityIDForSlug(t, "organization"), "create")
		requireTypeInForbidden(t, actorCtx(holder), "create", typeIDForSlug(t, "legal_entity"), ownerA, "grant on organization, legal_entity typeID")
	})

	t.Run("type-only target group and actor group", func(t *testing.T) {
		member := seedOperator(t)
		agroup := seedActorGroup(t, "typein-actor-group")
		addActorGroupMember(t, agroup, member)
		tgroup := seedTargetGroup(t, "typein-target-group")
		addTargetGroupMember(t, tgroup, typeEntityIDForSlug(t, parentSlug))
		targetedGrant(t, agroup, tgroup, "create")
		requireTypeInAllowed(t, actorCtx(member), "create", ty.parent, ownerA, "grant through actor and target groups")
		requireTypeInForbidden(t, actorCtx(member), "create", ty.child, ownerA, "group holds only the parent type entity")
		requireTypeInForbidden(t, actorCtx(seedOperator(t)), "create", ty.parent, ownerA, "non-member")
	})

	t.Run("an instance grant is not a type grant", func(t *testing.T) {
		_, actor := seedUserEntity(t)
		inst := seedOwnedInstance(t, ty.parent, ownerA)
		targetedGrant(t, actor, inst, "create")
		requireTypeInForbidden(t, actorCtx(actor), "create", ty.parent, ownerA, "entity grant on an instance of the type")
	})
}

func TestInteg_AuthorizeTypeIn_OwnedScope(t *testing.T) {
	ty := registerOwnedTypes(t)
	ownerA := seedOrg(t, "TypeIn Owned Owner A")
	ownerB := seedOrg(t, "TypeIn Owned Owner B")

	t.Run("exact type", func(t *testing.T) {
		_, actor := seedUserEntity(t)
		ownedGrantTyped(t, actor, ownerA, "create", &ty.parent, false)
		ctx := actorCtx(actor)
		requireTypeInAllowed(t, ctx, "create", ty.parent, ownerA, "exact type under the owner")
		requireTypeInForbidden(t, ctx, "create", ty.child, ownerA, "subtype without include_subtypes")
		requireTypeInForbidden(t, ctx, "create", ty.other, ownerA, "unrelated type")
		requireTypeInForbidden(t, ctx, "create", ty.parent, ownerB, "another owner")
		requireTypeInForbidden(t, ctx, "list", ty.parent, ownerA, "create does not imply list")
	})

	t.Run("include_subtypes", func(t *testing.T) {
		_, actor := seedUserEntity(t)
		ownedGrantTyped(t, actor, ownerA, "create", &ty.parent, true)
		ctx := actorCtx(actor)
		requireTypeInAllowed(t, ctx, "create", ty.parent, ownerA, "include_subtypes: the type itself")
		requireTypeInAllowed(t, ctx, "create", ty.child, ownerA, "include_subtypes: a subtype")
		requireTypeInForbidden(t, ctx, "create", ty.other, ownerA, "include_subtypes: an unrelated type")
		requireTypeInForbidden(t, ctx, "create", ty.child, ownerB, "include_subtypes: another owner")

		_, childActor := seedUserEntity(t)
		ownedGrantTyped(t, childActor, ownerA, "create", &ty.child, true)
		requireTypeInAllowed(t, actorCtx(childActor), "create", ty.child, ownerA, "include_subtypes on the child: the child")
		requireTypeInForbidden(t, actorCtx(childActor), "create", ty.parent, ownerA, "include_subtypes on the child: the parent")
	})

	t.Run("NULL type", func(t *testing.T) {
		_, actor := seedUserEntity(t)
		ownedGrant(t, actor, ownerA, "list")
		ctx := actorCtx(actor)
		for _, typeID := range []int64{ty.parent, ty.child, ty.other} {
			requireTypeInAllowed(t, ctx, "list", typeID, ownerA, "NULL type: any type under the owner")
		}
		requireTypeInForbidden(t, ctx, "list", ty.parent, ownerB, "NULL type: another owner")
		requireTypeInAllowed(t, ctx, "read", ty.parent, ownerA, "list implies read")
	})

	t.Run("through an actor group", func(t *testing.T) {
		_, member := seedUserEntity(t)
		group := seedActorGroup(t, "typein-owned-group")
		addActorGroupMember(t, group, member)
		ownedGrantTyped(t, group, ownerA, "create", &ty.parent, true)
		requireTypeInAllowed(t, actorCtx(member), "create", ty.child, ownerA, "group member")
		_, bystander := seedUserEntity(t)
		requireTypeInForbidden(t, actorCtx(bystander), "create", ty.child, ownerA, "non-member")
	})

	t.Run("a grant on one owner gives nothing under another", func(t *testing.T) {
		_, actor := seedUserEntity(t)
		ownedGrant(t, actor, ownerA, "create")
		requireTypeInForbidden(t, actorCtx(actor), "create", ty.parent, ownerB, "owner B holds no grant")
	})
}

func TestInteg_AuthorizeTypeIn_OwnArm(t *testing.T) {
	w := newOwnArmWorld(t)
	orgType := organizationTypeID(t)
	otherUser, _ := seedUserEntity(t)
	ctx := actorCtx(w.profile)

	t.Run("ownerID equal to profile_own_subject(actor)", func(t *testing.T) {
		if got := ownerSubject(t, w.profile); got != w.user {
			t.Fatalf("premise: profile_own_subject(%d) = %d, want the holder %d", w.profile, got, w.user)
		}
		confers, nonConfers := conferralByOperation(t)
		for _, op := range confers {
			requireTypeInAllowed(t, ctx, op, orgType, w.user, "own arm, conferring "+op)
		}
		for _, op := range nonConfers {
			requireTypeInForbidden(t, ctx, op, orgType, w.user, "own arm, non-conferring "+op)
		}
	})

	t.Run("ownerID different from profile_own_subject(actor)", func(t *testing.T) {
		requireTypeInForbidden(t, ctx, "create", orgType, otherUser, "another user's scope")
		requireTypeInForbidden(t, ctx, "create", orgType, w.party, "a party's scope")
		// The owner is the holder user, not the personal profile's own entity id.
		requireTypeInForbidden(t, ctx, "create", orgType, w.profile, "the profile id is not the ownership subject")
	})

	t.Run("actors without an own subject", func(t *testing.T) {
		for _, a := range []struct {
			name  string
			actor int64
			owner int64
		}{
			{"party profile in its holder's scope", w.partyProfile, w.user},
			{"party profile in its party's scope", w.partyProfile, w.party},
			{"service account profile in its owner's scope", w.saProfile, w.party},
			{"anonymous profile in the user's scope", w.anonProfile, w.user},
		} {
			requireTypeInForbidden(t, actorCtx(a.actor), "create", orgType, a.owner, a.name)
		}
	})
}

func TestInteg_AuthorizeTypeIn_Deny(t *testing.T) {
	ty := registerOwnedTypes(t)
	ownerA := seedOrg(t, "TypeIn Deny Owner A")
	_, nothing := seedUserEntity(t)
	ctx := actorCtx(nothing)

	for _, op := range []string{"create", "list", "read", "manage"} {
		requireTypeInForbidden(t, ctx, op, ty.parent, ownerA, "no authority at all: "+op)
	}

	// A holder of other arms' authority still falls through to ErrForbidden.
	_, other := seedUserEntity(t)
	ownedGrantTyped(t, other, ownerA, "create", &ty.other, false)
	targetedGrant(t, other, seedOwnedInstance(t, ty.parent, ownerA), "create")
	requireTypeInForbidden(t, actorCtx(other), "create", ty.parent, ownerA, "authority over other types and instances only")

	// Fail closed on ids and context.
	operator := seedOperator(t, "manage")
	requireTypeInForbidden(t, actorCtx(operator), "create", 0, ownerA, "typeID 0 even for a wildcard holder")
	requireTypeInForbidden(t, actorCtx(operator), "create", ty.parent, 0, "ownerID 0 even for a wildcard holder")
	requireTypeInForbidden(t, actorCtx(operator), "create", -1, ownerA, "negative typeID")
	requireTypeInForbidden(t, actorCtx(operator), "create", ty.parent, -1, "negative ownerID")
	if err := integAZ.AuthorizeTypeIn(context.Background(), "create", ty.parent, ownerA); !errors.Is(err, apiresp.ErrUnauthenticated) {
		t.Errorf("no AuthContext: got %v, want ErrUnauthenticated", err)
	}
	// An unknown type id gives an empty target chain and fails closed.
	var maxType int64
	if err := integPool.QueryRow(context.Background(), `SELECT max(id) FROM types`).Scan(&maxType); err != nil {
		t.Fatalf("max(types.id): %v", err)
	}
	requireTypeInForbidden(t, actorCtx(nothing), "create", maxType+1000, ownerA, "unknown type id")
	// An owner id that names no entity is simply an owner nobody holds a grant on.
	requireTypeInForbidden(t, actorCtx(nothing), "create", ty.parent, ownerA+1_000_000, "unknown owner id")
}
