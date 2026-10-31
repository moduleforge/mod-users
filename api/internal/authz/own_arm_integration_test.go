//go:build integration

package authz_test

// own_arm_integration_test.go proves the engine's own arm against the real
// composed schema. The own arm admits a target whose owner_id equals
// profile_own_subject(actor), for an operation with
// authz_operations.ownership_confers. profile_own_subject is the holder of a
// user's personal profile and NULL for every other actor, so only a user's
// personal profile has own authority.
//
// Rule-to-test mapping:
//
//	only a user's personal profile has the own arm                 TestInteg_OwnArm_OnlyPersonalProfilesOfUsers
//	only operations with ownership_confers                         TestInteg_OwnArm_OnlyConferringOperations
//	impersonate is never conferred on an owned entity              TestInteg_OwnArm_ImpersonateNeverConferred
//	the own arm follows the acting profile (impersonation)         TestInteg_OwnArm_FollowsTheActingProfile
//	single-row agrees with the generated list functions            TestInteg_Parity_OwnArm (parity_integration_test.go)
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/opctx"
)

// conferralByOperation reads authz_operations.ownership_confers from the
// database, so the suite follows the seed rather than a copy of it.
func conferralByOperation(t *testing.T) (confers, nonConfers []string) {
	t.Helper()
	rows, err := integPool.Query(context.Background(), `SELECT slug, ownership_confers FROM authz_operations ORDER BY id`)
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		var conferred bool
		if err := rows.Scan(&slug, &conferred); err != nil {
			t.Fatalf("scan operation: %v", err)
		}
		if conferred {
			confers = append(confers, slug)
		} else {
			nonConfers = append(nonConfers, slug)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("operations rows: %v", err)
	}
	return confers, nonConfers
}

// ownArmWorld holds a personal user with the entities it owns, and one actor of
// every kind that must have no own authority over them.
type ownArmWorld struct {
	user, profile int64            // the owner: a bring-your-own user and its personal profile
	owned         map[string]int64 // entities owned by the user
	party         int64            // an organization the user also holds a party profile in
	partyProfile  int64            // the user's party profile
	saProfile     int64            // a service account's personal profile
	anonProfile   int64            // the anonymous system actor's profile
}

func newOwnArmWorld(t *testing.T) ownArmWorld {
	t.Helper()
	var w ownArmWorld
	w.user, w.profile = seedUserEntity(t)
	w.owned = map[string]int64{
		"organization": seedOwnedOrg(t, w.user, "Own Arm Org "+fxTag("o")),
		"test type":    seedOwnedInstance(t, mustTestType(t), w.user),
		"user record":  w.user, // a bring-your-own user owns itself
		"profile":      w.profile,
	}
	w.party = seedOrg(t, "Own Arm Party "+fxTag("p"))
	fxMustTx(t, func(tx pgx.Tx) { w.partyProfile = fxPartyProfile(t, tx, w.user, w.party, "active") })

	// A service account is never an owner (the owner domain is users and legal
	// entities), so there is nothing it owns for its profile to reach.
	_, w.saProfile = seedServiceAccount(t, w.party)
	w.anonProfile = anonymousProfileID(t)
	return w
}

func mustTestType(t *testing.T) int64 {
	t.Helper()
	id, _ := registerTestType(t, "own-arm")
	return id
}

func TestInteg_OwnArm_OnlyPersonalProfilesOfUsers(t *testing.T) {
	w := newOwnArmWorld(t)
	confers, _ := conferralByOperation(t)
	for _, want := range []string{"read", "update", "credentials"} {
		if !slices.Contains(confers, want) {
			t.Fatalf("premise: %s must confer ownership, conferring = %v", want, confers)
		}
	}

	// The personal profile of the user has the own arm for every conferring
	// operation over everything the user owns, its own user record included.
	for _, op := range confers {
		for name, id := range w.owned {
			requireAllowed(t, actorCtx(w.profile), op, id, "personal profile, "+op+" over "+name)
		}
	}

	// Every other actor kind has no own authority over the same entities.
	others := []struct {
		name  string
		actor int64
	}{
		{"party profile of the owning user", w.partyProfile},
		{"service account profile", w.saProfile},
		{"anonymous profile", w.anonProfile},
	}
	for _, o := range others {
		for _, op := range confers {
			for name, id := range w.owned {
				requireForbidden(t, actorCtx(o.actor), op, id, o.name+", "+op+" over "+name)
			}
		}
	}

	// ... nor over entities owned by their own ownership subject: the party for
	// a party profile, the service account's owner for its profile, and NULL
	// for the anonymous actor.
	partyOwned := seedOwnedOrg(t, w.party, "Own Arm Party Content "+fxTag("c"))
	unowned := seedUnownedOrg(t, "Own Arm Unowned "+fxTag("u"))
	for _, op := range confers {
		requireForbidden(t, actorCtx(w.partyProfile), op, partyOwned, "party profile, "+op+" over an entity its party owns")
		requireForbidden(t, actorCtx(w.saProfile), op, partyOwned, "service account profile, "+op+" over an entity its owner owns")
		requireForbidden(t, actorCtx(w.anonProfile), op, unowned, "anonymous profile, "+op+" over an unowned entity")
		anon := opctx.WithAuthContext(context.Background(), opctx.AuthContext{
			PrincipalID: ownerHolder(t, w.anonProfile), ProfileID: w.anonProfile, Anonymous: true,
		})
		requireForbidden(t, anon, op, unowned, "anonymous context, "+op+" over an unowned entity")
		requireForbidden(t, anon, op, w.owned["organization"], "anonymous context, "+op+" over a user's entity")
	}

	// Control: the party profile, inside the owning party, is not a stranger to
	// the party's content; it just has no own arm. An explicit grant works.
	targetedGrant(t, w.partyProfile, partyOwned, "read")
	requireAllowed(t, actorCtx(w.partyProfile), "read", partyOwned, "control: an explicit grant to the party profile")
}

// ownerHolder returns the holder principal of a profile.
func ownerHolder(t *testing.T, profileID int64) int64 {
	t.Helper()
	return fxScanID(t, integPool, "resolve profile holder", `SELECT holder_id FROM profiles WHERE entity_id = $1`, profileID)
}

func TestInteg_OwnArm_OnlyConferringOperations(t *testing.T) {
	w := newOwnArmWorld(t)
	confers, nonConfers := conferralByOperation(t)

	for _, want := range []string{"manage", "impersonate", "login"} {
		if !slices.Contains(nonConfers, want) {
			t.Fatalf("premise: %s must not confer ownership, non-conferring = %v", want, nonConfers)
		}
	}
	// The conferring operations the task names, spelled out beside the seed read.
	for _, op := range []string{"read", "update", "credentials"} {
		for name, id := range w.owned {
			requireAllowed(t, actorCtx(w.profile), op, id, "conferring "+op+" over "+name)
		}
	}
	for _, op := range nonConfers {
		for name, id := range w.owned {
			requireForbidden(t, actorCtx(w.profile), op, id, "non-conferring "+op+" over "+name)
		}
	}
	if len(confers) == 0 || len(nonConfers) == 0 {
		t.Fatalf("premise: the seed must hold both kinds (confer %v, non-confer %v)", confers, nonConfers)
	}

	// manage implies every conferring operation but does not itself confer, so
	// owning an entity never reaches it.
	requireForbidden(t, actorCtx(w.profile), "manage", w.owned["organization"], "manage is not conferred by ownership")
}

func TestInteg_OwnArm_ImpersonateNeverConferred(t *testing.T) {
	w := newOwnArmWorld(t)

	for name, id := range w.owned {
		requireForbidden(t, actorCtx(w.profile), "impersonate", id, "impersonate over the owned "+name)
	}

	// Not even over the user's own account or its own profile, by a fresh
	// session, with every other operation conferred.
	requireAllowed(t, actorCtx(w.profile), "credentials", w.user, "control: credentials over the user's own record")
	requireForbidden(t, actorCtx(w.profile), "impersonate", w.user, "impersonate over the user's own record")
	requireForbidden(t, actorCtx(w.profile), "impersonate", w.profile, "impersonate over the user's own profile")

	// AuthorizeTypeIn's own arm confers create within the owner, never
	// impersonate.
	orgType := organizationTypeID(t)
	if err := integAZ.AuthorizeTypeIn(actorCtx(w.profile), "create", orgType, w.user); err != nil {
		t.Errorf("control: AuthorizeTypeIn(create) within the user's own scope: %v", err)
	}
	if err := integAZ.AuthorizeTypeIn(actorCtx(w.profile), "impersonate", orgType, w.user); err == nil {
		t.Errorf("AuthorizeTypeIn(impersonate) within the user's own scope was allowed")
	}
}

// TestInteg_OwnArm_FollowsTheActingProfile proves that under impersonation the
// own arm is the impersonated profile's, never the administrator's.
func TestInteg_OwnArm_FollowsTheActingProfile(t *testing.T) {
	target := newOwnArmWorld(t)
	adminUser, adminProfile := seedUserEntity(t)
	adminOwned := seedOwnedOrg(t, adminUser, "Own Arm Admin Org "+fxTag("a"))

	ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID:             target.user,
		ProfileID:               target.profile,
		ImpersonatorPrincipalID: adminUser,
		SessionID:               1,
		ACR:                     1,
		AuthTime:                time.Now(),
	})
	requireAllowed(t, ctx, "read", target.owned["organization"], "impersonating: the impersonated profile's own entity")
	requireForbidden(t, ctx, "read", adminOwned, "impersonating: the administrator's own entity")
	requireForbidden(t, ctx, "impersonate", target.owned["organization"], "impersonating: impersonate is not conferred")

	// And the administrator acting as itself still owns its own entity.
	requireAllowed(t, actorCtx(adminProfile), "read", adminOwned, "control: the administrator acting as itself")
}
