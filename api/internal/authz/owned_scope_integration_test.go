//go:build integration

package authz_test

// owned_scope_integration_test.go proves the engine's owned-scope arm against
// the real composed schema. An owned-scope grant (scope = 'owned', target_id =
// an owner) confers its operation over every entity whose owner_id is that
// owner, narrowed by target_type_id (NULL: any type; otherwise exactly that
// type, or with include_subtypes that type and its descendants). It never
// reaches the owner's own record, never crosses to another owner's entities,
// and is read by neither the wildcard arm nor the entity arm.
//
// Rule-to-test mapping:
//
//	exact target_type_id: instance passes, subtype instance fails   TestInteg_OwnedScope_ExactType
//	include_subtypes passes for a subtype instance                  TestInteg_OwnedScope_IncludeSubtypes
//	target_type_id NULL passes every type the owner owns            TestInteg_OwnedScope_AnyType
//	the grant never reaches the owner's own record                  TestInteg_OwnedScope_NeverReachesOwnerRecord
//	a grant held through an actor group passes                      TestInteg_OwnedScope_ThroughActorGroups
//	a grant on owner A never reaches owner B's entities             TestInteg_OwnedScope_OwnerIsolation
//	operation closure, revocation                                   TestInteg_OwnedScope_OperationClosure, TestInteg_OwnedScope_Revocation
//	owned scope is not an entity grant or a wildcard                TestInteg_OwnedScopeGrantOnAPartyDoesNotAuthorizeThePartyEntity,
//	                                                                TestInteg_OwnedScopeGrantIsNotReadAsAWildcardGrant
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"errors"
	"testing"

	"github.com/moduleforge/core-api/apiresp"
)

// ownedGrant inserts an owned-scope grant: actor holds the operation over every
// entity owned by owner, of any type.
func ownedGrant(t *testing.T, actorID, ownerID int64, operationSlug string) {
	t.Helper()
	ownedGrantTyped(t, actorID, ownerID, operationSlug, nil, false)
}

// ownedGrantTyped inserts an owned-scope grant narrowed to typeID (NULL: any
// type), optionally including the type's descendants.
func ownedGrantTyped(t *testing.T, actorID, ownerID int64, operationSlug string, typeID *int64, includeSubtypes bool) {
	t.Helper()
	fxExec(t, integPool, "insert owned-scope grant",
		`INSERT INTO grants (actor_id, operation_id, target_id, scope, target_type_id, include_subtypes, granted_by)
		 VALUES ($1, $2, $3, 'owned', $4, $5, NULL)`,
		actorID, opID(t, operationSlug), ownerID, typeID, includeSubtypes)
}

// deleteOwnedGrant removes the owned-scope grants of (actor, operation, owner).
func deleteOwnedGrant(t *testing.T, actorID, ownerID int64, operationSlug string) {
	t.Helper()
	tag, err := integPool.Exec(context.Background(),
		`DELETE FROM grants WHERE scope = 'owned' AND actor_id = $1 AND target_id = $2 AND operation_id = $3`,
		actorID, ownerID, opID(t, operationSlug))
	if err != nil {
		t.Fatalf("delete owned-scope grant: %v", err)
	}
	if tag.RowsAffected() == 0 {
		t.Fatalf("delete owned-scope grant (actor=%d, owner=%d, %s): no row", actorID, ownerID, operationSlug)
	}
}

// seedOwnedInstance inserts an entity of the given type owned by ownerID from
// insert time and returns its id.
func seedOwnedInstance(t *testing.T, typeID, ownerID int64) int64 {
	t.Helper()
	return fxScanID(t, integPool, "insert owned instance",
		`INSERT INTO entities (fundamental_type_id, owner_id) VALUES ($1, $2) RETURNING id`, typeID, ownerID)
}

// ownedTypes is a concrete parent type, a concrete child of it, and the
// organization type (unrelated to both).
type ownedTypes struct {
	parent, child, other int64
}

func registerOwnedTypes(t *testing.T) ownedTypes {
	t.Helper()
	parent, _ := registerTestType(t, "owned_parent")
	child, _ := registerTestTypeUnder(t, "owned_child", typeSlugForID(t, parent))
	return ownedTypes{parent: parent, child: child, other: organizationTypeID(t)}
}

// requireOwnedAllowed and requireOwnedDenied assert one Authorize outcome; a
// denial must be ErrForbidden exactly (never a step-up or a database error).
func requireOwnedAllowed(t *testing.T, actor int64, op string, target int64, msg string) {
	t.Helper()
	requireAllowed(t, actorCtx(actor), op, target, msg)
}

func requireOwnedDenied(t *testing.T, actor int64, op string, target int64, msg string) {
	t.Helper()
	requireForbidden(t, actorCtx(actor), op, target, msg)
}

func TestInteg_OwnedScope_ExactType(t *testing.T) {
	ty := registerOwnedTypes(t)
	ownerA := seedOrg(t, "Owned Exact Owner A")
	parentInst := seedOwnedInstance(t, ty.parent, ownerA)
	childInst := seedOwnedInstance(t, ty.child, ownerA)
	otherInst := seedOwnedInstance(t, ty.other, ownerA)
	_, actor := seedUserEntity(t)

	ownedGrantTyped(t, actor, ownerA, "read", &ty.parent, false)

	requireOwnedAllowed(t, actor, "read", parentInst, "exact type: an instance of that type")
	requireOwnedDenied(t, actor, "read", childInst, "exact type: an instance of a subtype")
	requireOwnedDenied(t, actor, "read", otherInst, "exact type: an instance of an unrelated type")

	// Exact on the child reaches the child only, never its parent.
	_, childActor := seedUserEntity(t)
	ownedGrantTyped(t, childActor, ownerA, "read", &ty.child, false)
	requireOwnedAllowed(t, childActor, "read", childInst, "exact child type: an instance of the child")
	requireOwnedDenied(t, childActor, "read", parentInst, "exact child type: an instance of the parent")
}

func TestInteg_OwnedScope_IncludeSubtypes(t *testing.T) {
	ty := registerOwnedTypes(t)
	ownerA := seedOrg(t, "Owned Subtypes Owner A")
	parentInst := seedOwnedInstance(t, ty.parent, ownerA)
	childInst := seedOwnedInstance(t, ty.child, ownerA)
	otherInst := seedOwnedInstance(t, ty.other, ownerA)
	_, actor := seedUserEntity(t)

	ownedGrantTyped(t, actor, ownerA, "read", &ty.parent, true)

	requireOwnedAllowed(t, actor, "read", parentInst, "include_subtypes: an instance of the type itself")
	requireOwnedAllowed(t, actor, "read", childInst, "include_subtypes: an instance of a subtype")
	requireOwnedDenied(t, actor, "read", otherInst, "include_subtypes: an instance of an unrelated type")

	// include_subtypes on the child does not climb to its parent.
	_, childActor := seedUserEntity(t)
	ownedGrantTyped(t, childActor, ownerA, "read", &ty.child, true)
	requireOwnedAllowed(t, childActor, "read", childInst, "include_subtypes on the child: the child")
	requireOwnedDenied(t, childActor, "read", parentInst, "include_subtypes on the child: the parent")

	// A real subtype pair of the seed: legal_entity covers organization and
	// natural_person instances, but not a user-owned type outside its subtree.
	legalEntity := typeIDForSlug(t, "legal_entity")
	org := seedOwnedOrg(t, ownerA, "Owned Subtypes Seed Org")
	_, leActor := seedUserEntity(t)
	ownedGrantTyped(t, leActor, ownerA, "read", &legalEntity, true)
	requireOwnedAllowed(t, leActor, "read", org, "include_subtypes on legal_entity: an organization")
	requireOwnedDenied(t, leActor, "read", parentInst, "include_subtypes on legal_entity: a test type outside the subtree")
}

func TestInteg_OwnedScope_AnyType(t *testing.T) {
	ty := registerOwnedTypes(t)
	ownerA := seedOrg(t, "Owned Any Owner A")
	instances := map[string]int64{
		"test parent":  seedOwnedInstance(t, ty.parent, ownerA),
		"test child":   seedOwnedInstance(t, ty.child, ownerA),
		"organization": seedOwnedOrg(t, ownerA, "Owned Any Org"),
	}
	_, actor := seedUserEntity(t)

	ownedGrantTyped(t, actor, ownerA, "read", nil, false)

	for name, id := range instances {
		requireOwnedAllowed(t, actor, "read", id, "NULL target type: "+name)
	}

	// A NULL type is still bounded by the owner and by the operation.
	ownerB := seedOrg(t, "Owned Any Owner B")
	foreign := seedOwnedInstance(t, ty.parent, ownerB)
	requireOwnedDenied(t, actor, "read", foreign, "NULL target type: another owner's instance")
	for name, id := range instances {
		requireOwnedDenied(t, actor, "update", id, "NULL target type, read grant does not give update: "+name)
	}
}

func TestInteg_OwnedScope_NeverReachesOwnerRecord(t *testing.T) {
	ty := registerOwnedTypes(t)

	t.Run("self-owned user", func(t *testing.T) {
		ownerUser, _ := seedUserEntity(t) // a bring-your-own user owns itself
		owned := seedOwnedInstance(t, ty.parent, ownerUser)
		_, actor := seedUserEntity(t)
		ownedGrant(t, actor, ownerUser, "read")
		ownedGrantTyped(t, actor, ownerUser, "update", &ty.parent, true)

		requireOwnedAllowed(t, actor, "read", owned, "control: an entity the user owns")
		requireOwnedDenied(t, actor, "read", ownerUser, "NULL type: the owner's own record")
		requireOwnedDenied(t, actor, "update", ownerUser, "typed include_subtypes: the owner's own record")
	})

	t.Run("organization owner", func(t *testing.T) {
		org := seedOrg(t, "Owned Record Org")
		owned := seedOwnedInstance(t, ty.child, org)
		_, actor := seedUserEntity(t)
		ownedGrant(t, actor, org, "manage")

		requireOwnedAllowed(t, actor, "read", owned, "control: an entity the organization owns")
		for _, op := range []string{"read", "update", "manage"} {
			requireOwnedDenied(t, actor, op, org, op+" on the owner's own record")
		}
	})

	t.Run("owner owned by a third party", func(t *testing.T) {
		// The owner's record is owned by someone else, so the grant on the owner
		// matches neither it (owner_id differs) nor anything it does not own.
		holder, _ := seedUserEntity(t)
		org := seedOwnedOrg(t, holder, "Owned Record Held Org")
		_, actor := seedUserEntity(t)
		ownedGrant(t, actor, org, "read")
		requireOwnedDenied(t, actor, "read", org, "owner record owned by a user")
	})
}

func TestInteg_OwnedScope_ThroughActorGroups(t *testing.T) {
	ty := registerOwnedTypes(t)
	ownerA := seedOrg(t, "Owned Group Owner A")
	parentInst := seedOwnedInstance(t, ty.parent, ownerA)
	childInst := seedOwnedInstance(t, ty.child, ownerA)
	_, member := seedUserEntity(t)
	_, bystander := seedUserEntity(t)

	group := seedActorGroup(t, "owned-scope-group")
	addActorGroupMember(t, group, member)
	ownedGrantTyped(t, group, ownerA, "read", &ty.parent, true)

	requireOwnedAllowed(t, member, "read", parentInst, "group member: parent instance")
	requireOwnedAllowed(t, member, "read", childInst, "group member: subtype instance")
	requireOwnedDenied(t, bystander, "read", parentInst, "non-member")

	// Nested actor groups: the grant is on the outer group.
	_, nested := seedUserEntity(t)
	inner := seedActorGroup(t, "owned-scope-inner")
	outer := seedActorGroup(t, "owned-scope-outer")
	addActorGroupMember(t, inner, nested)
	addActorGroupMember(t, outer, inner)
	ownedGrant(t, outer, ownerA, "update")
	requireOwnedAllowed(t, nested, "update", childInst, "nested group member")
	requireOwnedDenied(t, bystander, "update", childInst, "non-member of the nested groups")
}

func TestInteg_OwnedScope_OwnerIsolation(t *testing.T) {
	ty := registerOwnedTypes(t)
	ownerA := seedOrg(t, "Owned Isolation Owner A")
	ownerB := seedOrg(t, "Owned Isolation Owner B")
	instA := seedOwnedInstance(t, ty.parent, ownerA)
	instB := seedOwnedInstance(t, ty.parent, ownerB)
	unowned := seedInstanceOfType(t, ty.parent)
	_, actor := seedUserEntity(t)

	ownedGrant(t, actor, ownerA, "read")

	requireOwnedAllowed(t, actor, "read", instA, "owner A's entity")
	requireOwnedDenied(t, actor, "read", instB, "owner B's entity")
	requireOwnedDenied(t, actor, "read", unowned, "an unowned entity")
	requireOwnedDenied(t, actor, "read", ownerB, "owner B's own record")

	// The mirror: a grant on B reaches B's entities and not A's.
	_, actorB := seedUserEntity(t)
	ownedGrant(t, actorB, ownerB, "read")
	requireOwnedAllowed(t, actorB, "read", instB, "mirror: owner B's entity")
	requireOwnedDenied(t, actorB, "read", instA, "mirror: owner A's entity")

	// A nil target is allowed only by the wildcard arm, never by an owned grant.
	nilTarget := integAZ.Authorize(actorCtx(actor), "read", nil)
	if !errors.Is(nilTarget, apiresp.ErrForbidden) {
		t.Errorf("owned-scope grant with a nil target: err = %v, want ErrForbidden (only the wildcard arm allows nil)", nilTarget)
	}
}

func TestInteg_OwnedScope_OperationClosure(t *testing.T) {
	ty := registerOwnedTypes(t)
	ownerA := seedOrg(t, "Owned Closure Owner A")
	inst := seedOwnedInstance(t, ty.parent, ownerA)

	t.Run("update implies read, not delete", func(t *testing.T) {
		_, actor := seedUserEntity(t)
		ownedGrant(t, actor, ownerA, "update")
		requireOwnedAllowed(t, actor, "update", inst, "update")
		requireOwnedAllowed(t, actor, "read", inst, "update implies read")
		requireOwnedDenied(t, actor, "delete", inst, "update does not imply delete")
		requireOwnedDenied(t, actor, "impersonate", inst, "update does not imply impersonate")
	})

	t.Run("an explicit manage grant is a grant, not ownership", func(t *testing.T) {
		_, actor := seedUserEntity(t)
		ownedGrant(t, actor, ownerA, "manage")
		for _, op := range []string{"read", "delete", "grant", "impersonate", "login"} {
			requireOwnedAllowed(t, actor, op, inst, "manage closure over owned entities: "+op)
		}
	})

	t.Run("an explicit impersonate grant", func(t *testing.T) {
		_, actor := seedUserEntity(t)
		ownedGrant(t, actor, ownerA, "impersonate")
		requireOwnedAllowed(t, actor, "impersonate", inst, "explicit impersonate")
		requireOwnedDenied(t, actor, "read", inst, "impersonate implies nothing")
	})
}

func TestInteg_OwnedScope_Revocation(t *testing.T) {
	ty := registerOwnedTypes(t)
	ownerA := seedOrg(t, "Owned Revoke Owner A")
	inst := seedOwnedInstance(t, ty.parent, ownerA)
	_, actor := seedUserEntity(t)

	ownedGrant(t, actor, ownerA, "read")
	requireOwnedAllowed(t, actor, "read", inst, "before revocation")
	deleteOwnedGrant(t, actor, ownerA, "read")
	requireOwnedDenied(t, actor, "read", inst, "after revocation")
}

func TestInteg_OwnedScopeGrantOnAPartyDoesNotAuthorizeThePartyEntity(t *testing.T) {
	party := seedOrg(t, "Owned Scope Party")
	member := seedOrg(t, "Owned Scope Bystander")
	_, actor := seedUserEntity(t)

	ownedGrant(t, actor, party, "update")

	// The grant covers entities owned by the party; it is not an entity grant on
	// the party itself, and it is not a wildcard.
	for _, tc := range []struct {
		name   string
		op     string
		target *int64
	}{
		{"update on the party entity", "update", &party},
		{"read on the party entity", "read", &party},
		{"update on an unrelated entity", "update", &member},
		{"update with no target (wildcard arm)", "update", nil},
		{"manage with no target (wildcard arm)", "manage", nil},
	} {
		err := integAZ.Authorize(actorCtx(actor), tc.op, tc.target)
		if err == nil {
			t.Errorf("%s: owned-scope grant authorized it, want ErrForbidden", tc.name)
			continue
		}
		if !errors.Is(err, apiresp.ErrForbidden) {
			t.Errorf("%s: err = %v, want ErrForbidden", tc.name, err)
		}
	}

	// Control: an entity-scope grant on the same party does authorize it, so the
	// denials above are about scope and not about the fixtures.
	targetedGrant(t, actor, party, "update")
	if err := integAZ.Authorize(actorCtx(actor), "update", &party); err != nil {
		t.Errorf("entity-scope grant on the party: err = %v, want nil", err)
	}
	if err := integAZ.Authorize(actorCtx(actor), "update", &member); err == nil {
		t.Errorf("entity-scope grant on the party authorized an unrelated entity")
	}
}

func TestInteg_OwnedScopeGrantIsNotReadAsAWildcardGrant(t *testing.T) {
	party := seedOrg(t, "Owned Scope Wildcard Party")
	target := seedOrg(t, "Owned Scope Wildcard Target")
	_, actor := seedUserEntity(t)

	// Even every operation owned-scope granted over the party leaves the actor
	// without any entity-level authority.
	for _, op := range []string{"read", "update", "manage"} {
		ownedGrant(t, actor, party, op)
	}
	for _, op := range []string{"read", "update", "manage"} {
		if err := integAZ.Authorize(actorCtx(actor), op, &target); err == nil {
			t.Errorf("%s on an arbitrary entity authorized by owned-scope grants", op)
		}
		if err := integAZ.Authorize(actorCtx(actor), op, &party); err == nil {
			t.Errorf("%s on the owner entity authorized by owned-scope grants", op)
		}
	}
}
