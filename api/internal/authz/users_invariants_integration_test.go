//go:build integration

package authz_test

// users_invariants_integration_test.go proves the MF103 user invariants of the
// baseline migration by writing directly through SQL, so the rules hold
// whatever the writer: the user type check, immutable managing_party_id,
// is_guest only true to false, the owner rule, default_profile_id held by the
// user, the public.profiles trigger and the deferred personal-profile check.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// inRolledBackTx runs fn in a transaction that is always rolled back, for
// statements expected to fail (a failed statement aborts the transaction).
func inRolledBackTx(t *testing.T, fn func(tx pgx.Tx)) {
	t.Helper()
	ctx := context.Background()
	tx, err := integPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // test cleanup
	fn(tx)
}

func rawExec(q fxQ, sql string, args ...any) error {
	_, err := q.Exec(context.Background(), sql, args...)
	return err
}

// MF103: the entity of a users row descends from `user`.
func TestInteg_MF103_UserTypeCheck(t *testing.T) {
	party := seedOrg(t, "MF103 type org")
	inRolledBackTx(t, func(tx pgx.Tx) {
		sa := fxEntity(t, tx, "service_account", &party)
		fxExec(t, tx, "insert principals", `INSERT INTO principals (entity_id) VALUES ($1)`, sa)
		err := rawExec(tx, `INSERT INTO mod_users.users (entity_id) VALUES ($1)`, sa)
		expectState(t, err, "MF103", "users row for a service account entity")
	})
}

// MF103: managing_party_id is immutable.
func TestInteg_MF103_ManagingPartyImmutable(t *testing.T) {
	party := seedOrg(t, "MF103 managing org")
	other := seedOrg(t, "MF103 other org")
	var managed int64
	fxMustTx(t, func(tx pgx.Tx) { managed, _ = fxUser(t, tx, userOpts{ManagingPartyID: &party}) })
	byo, _ := seedUserEntity(t)

	for name, c := range map[string]struct {
		user int64
		to   *int64
	}{
		"managed to another party": {managed, &other},
		"managed to NULL":          {managed, nil},
		"byo to a party":           {byo, &party},
	} {
		err := rawExec(integPool, `UPDATE mod_users.users SET managing_party_id = $2 WHERE entity_id = $1`, c.user, c.to)
		expectState(t, err, "MF103", "change managing_party_id: "+name)
	}
	// A no-op write of the same value is allowed.
	expectOK(t, rawExec(integPool, `UPDATE mod_users.users SET managing_party_id = $2 WHERE entity_id = $1`, managed, party),
		"re-set managing_party_id to the same value")
}

// MF103: is_guest only goes true -> false.
func TestInteg_MF103_GuestOnlyTrueToFalse(t *testing.T) {
	var guest int64
	fxMustTx(t, func(tx pgx.Tx) { guest, _ = fxUser(t, tx, userOpts{Guest: true}) })
	byo, _ := seedUserEntity(t)

	expectState(t, rawExec(integPool, `UPDATE mod_users.users SET is_guest = true WHERE entity_id = $1`, byo),
		"MF103", "non-guest becomes a guest")
	expectOK(t, rawExec(integPool, `UPDATE mod_users.users SET is_guest = false WHERE entity_id = $1`, guest),
		"guest upgrade (true to false)")
	expectState(t, rawExec(integPool, `UPDATE mod_users.users SET is_guest = true WHERE entity_id = $1`, guest),
		"MF103", "upgraded guest becomes a guest again")
}

// MF103: a guest is unlinked (CHECK users_guest_unlinked, not an MF103 raise).
func TestInteg_MF103_GuestUnlinkedCheck(t *testing.T) {
	party := seedOrg(t, "MF103 guest org")
	person := seedNaturalPerson(t)
	for name, o := range map[string]userOpts{
		"guest with a managing party": {Guest: true, ManagingPartyID: &party},
		"guest with a person":         {Guest: true, PersonID: &person},
	} {
		inRolledBackTx(t, func(tx pgx.Tx) {
			err := func() error {
				id := fxEntity(t, tx, "user", o.ManagingPartyID)
				fxExec(t, tx, "insert principals", `INSERT INTO principals (entity_id) VALUES ($1)`, id)
				return rawExec(tx, `INSERT INTO mod_users.users (entity_id, is_guest, managing_party_id, person_id) VALUES ($1, true, $2, $3)`,
					id, o.ManagingPartyID, o.PersonID)
			}()
			if sqlState(err) != "23514" {
				t.Errorf("%s: SQLSTATE %q (%v), want 23514", name, sqlState(err), err)
			}
		})
	}
}

// MF103: the user entity's owner is the user itself (bring-your-own) or the
// managing party (managed).
func TestInteg_MF103_OwnerIsSelfOrManagingParty(t *testing.T) {
	party := seedOrg(t, "MF103 owner org")
	other := seedOrg(t, "MF103 owner other org")

	inRolledBackTx(t, func(tx pgx.Tx) {
		id := fxEntity(t, tx, "user", &other) // owned by an unrelated party
		fxExec(t, tx, "insert principals", `INSERT INTO principals (entity_id) VALUES ($1)`, id)
		expectState(t, rawExec(tx, `INSERT INTO mod_users.users (entity_id) VALUES ($1)`, id),
			"MF103", "bring-your-own user owned by a party")
	})
	inRolledBackTx(t, func(tx pgx.Tx) {
		id := fxEntity(t, tx, "user", &other)
		fxExec(t, tx, "insert principals", `INSERT INTO principals (entity_id) VALUES ($1)`, id)
		expectState(t, rawExec(tx, `INSERT INTO mod_users.users (entity_id, managing_party_id) VALUES ($1, $2)`, id, party),
			"MF103", "managed user owned by another party")
	})
	inRolledBackTx(t, func(tx pgx.Tx) {
		id := fxEntity(t, tx, "user", nil) // defaults to self-owned
		fxExec(t, tx, "insert principals", `INSERT INTO principals (entity_id) VALUES ($1)`, id)
		expectState(t, rawExec(tx, `INSERT INTO mod_users.users (entity_id, managing_party_id) VALUES ($1, $2)`, id, party),
			"MF103", "managed user that owns itself")
	})
	// Controls.
	expectOK(t, fxTx(t, func(tx pgx.Tx) { fxUser(t, tx, userOpts{}) }), "bring-your-own user owns itself")
	expectOK(t, fxTx(t, func(tx pgx.Tx) { fxUser(t, tx, userOpts{ManagingPartyID: &party}) }), "managed user owned by its party")
}

// MF103: default_profile_id names a profile held by the user.
func TestInteg_MF103_DefaultProfileHeld(t *testing.T) {
	userA, profileA := seedUserEntity(t)
	_, profileB := seedUserEntity(t)

	expectState(t, rawExec(integPool, `UPDATE mod_users.users SET default_profile_id = $2 WHERE entity_id = $1`, userA, profileB),
		"MF103", "default profile held by another user")
	expectOK(t, rawExec(integPool, `UPDATE mod_users.users SET default_profile_id = $2 WHERE entity_id = $1`, userA, profileA),
		"default profile held by the user")

	inRolledBackTx(t, func(tx pgx.Tx) {
		id := fxEntity(t, tx, "user", nil)
		fxExec(t, tx, "insert principals", `INSERT INTO principals (entity_id) VALUES ($1)`, id)
		expectState(t, rawExec(tx, `INSERT INTO mod_users.users (entity_id, default_profile_id) VALUES ($1, $2)`, id, profileB),
			"MF103", "insert with a default profile held by another user")
	})
}

// MF103: the public.profiles trigger (profiles_users_holder_rules).
func TestInteg_MF103_ProfilesTrigger(t *testing.T) {
	party := seedOrg(t, "MF103 profile org")
	otherParty := seedOrg(t, "MF103 profile other org")
	var managed, guest int64
	fxMustTx(t, func(tx pgx.Tx) {
		managed, _ = fxUser(t, tx, userOpts{ManagingPartyID: &party})
		guest, _ = fxUser(t, tx, userOpts{Guest: true})
	})

	t.Run("no personal profile for a managed user", func(t *testing.T) {
		inRolledBackTx(t, func(tx pgx.Tx) {
			id := fxEntity(t, tx, "profile", &managed)
			expectState(t, rawExec(tx, `INSERT INTO profiles (entity_id, holder_id, party_id, status) VALUES ($1, $2, NULL, 'active')`, id, managed),
				"MF103", "personal profile of a managed user")
		})
	})
	t.Run("no foreign-party profile for a managed user", func(t *testing.T) {
		inRolledBackTx(t, func(tx pgx.Tx) {
			id := fxEntity(t, tx, "profile", &otherParty)
			expectState(t, rawExec(tx, `INSERT INTO profiles (entity_id, holder_id, party_id, status) VALUES ($1, $2, $3, 'active')`, id, managed, otherParty),
				"MF103", "party profile of a managed user in a foreign party")
		})
	})
	t.Run("managed user holds a profile in its managing party", func(t *testing.T) {
		expectOK(t, fxTx(t, func(tx pgx.Tx) { fxPartyProfile(t, tx, managed, party, "active") }),
			"party profile in the managing party")
	})
	t.Run("no party profile for a guest", func(t *testing.T) {
		inRolledBackTx(t, func(tx pgx.Tx) {
			id := fxEntity(t, tx, "profile", &party)
			expectState(t, rawExec(tx, `INSERT INTO profiles (entity_id, holder_id, party_id, status) VALUES ($1, $2, $3, 'active')`, id, guest, party),
				"MF103", "party profile of a guest")
		})
	})
	t.Run("a user must have its users row before its profiles", func(t *testing.T) {
		inRolledBackTx(t, func(tx pgx.Tx) {
			u := fxEntity(t, tx, "user", nil)
			fxExec(t, tx, "insert principals", `INSERT INTO principals (entity_id) VALUES ($1)`, u)
			id := fxEntity(t, tx, "profile", &u)
			expectState(t, rawExec(tx, `INSERT INTO profiles (entity_id, holder_id, party_id, status) VALUES ($1, $2, NULL, 'active')`, id, u),
				"MF103", "profile of a user without a users row")
		})
	})
}

// MF103: the deferred personal-profile check.
func TestInteg_MF103_DeferredPersonalProfile(t *testing.T) {
	party := seedOrg(t, "MF103 deferred org")

	t.Run("bring-your-own user without a personal profile fails at commit", func(t *testing.T) {
		err := fxTx(t, func(tx pgx.Tx) { fxUser(t, tx, userOpts{NoPersonalProfile: true}) })
		expectState(t, err, "MF103", "commit of a bring-your-own user without a personal profile")
	})
	t.Run("created in the same transaction as its personal profile succeeds", func(t *testing.T) {
		expectOK(t, fxTx(t, func(tx pgx.Tx) { fxUser(t, tx, userOpts{}) }), "user and personal profile in one transaction")
	})
	t.Run("a guest is bring-your-own and needs its personal profile too", func(t *testing.T) {
		expectState(t, fxTx(t, func(tx pgx.Tx) { fxUser(t, tx, userOpts{Guest: true, NoPersonalProfile: true}) }),
			"MF103", "commit of a guest without a personal profile")
		expectOK(t, fxTx(t, func(tx pgx.Tx) { fxUser(t, tx, userOpts{Guest: true}) }), "guest with its personal profile")
	})
	t.Run("managed user without a personal profile commits", func(t *testing.T) {
		expectOK(t, fxTx(t, func(tx pgx.Tx) { fxUser(t, tx, userOpts{ManagingPartyID: &party}) }), "managed user")
	})
	t.Run("managed user with a personal profile is rejected", func(t *testing.T) {
		// The profiles trigger rejects the personal profile as soon as it is
		// inserted (the users row must come first), so the deferred check is a
		// backstop that the immediate rule makes unreachable by insert order.
		inRolledBackTx(t, func(tx pgx.Tx) {
			id, _ := fxUser(t, tx, userOpts{ManagingPartyID: &party})
			pid := fxEntity(t, tx, "profile", &id)
			expectState(t, rawExec(tx, `INSERT INTO profiles (entity_id, holder_id, party_id, status) VALUES ($1, $2, NULL, 'active')`, pid, id),
				"MF103", "personal profile for a managed user")
		})
	})
	t.Run("deleting the personal profile of a bring-your-own user fails at commit", func(t *testing.T) {
		_, profile := seedUserEntity(t)
		err := fxTx(t, func(tx pgx.Tx) {
			fxExec(t, tx, "delete personal profile", `DELETE FROM profiles WHERE entity_id = $1`, profile)
		})
		expectState(t, err, "MF103", "commit after deleting the personal profile")
	})
}
