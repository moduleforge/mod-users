//go:build integration

package integration

// Decision K: an impersonation session may not end a profile. Ending runs the
// departure cleanup, which removes the holder's credential authorizations for the
// party and revokes the party-confined credentials; an impersonator may do
// neither on anyone's behalf.

import (
	"net/http"
	"testing"
)

// The impersonated administrator holds `update` on a member's party profile and,
// without impersonation, could end it; through an impersonation session the same
// PATCH is a plain 403 and the profile, the holder's credential authorizations
// for the party and the holder's party-confined credentials are unchanged.
func TestProfileEnd_RefusedWhileImpersonating(t *testing.T) {
	acme := env.seedParty(t, "End Imp "+uniq("p"))
	target := env.newImpAdmin(t, acme) // the impersonated administrator
	m := env.newMember(t, acme)        // the holder whose profile the PATCH names

	// The authority the PATCH needs: read and update on the member's profile.
	for _, op := range []string{"read", "update"} {
		env.exec(t, op+" grant on the member profile",
			`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, $3, NULL)`,
			target.profileID, env.opID(t, op), m.profileID)
	}

	// The holder's password is authorized for the party and confined to it: the
	// two things the departure cleanup would remove.
	holderCred := env.scanInt64(t, "holder password credential",
		`SELECT id FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'password' AND revoked_at IS NULL`, m.userID)
	env.exec(t, "authorize holder credential",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		holderCred, acme, target.profileID)
	env.exec(t, "confine holder credential",
		`UPDATE mod_users.credentials SET confined_to_party_id = $2 WHERE id = $1`, holderCred, acme)

	opAcct := env.registerVerified(t)
	opTok, _ := env.makeOperator(t, opAcct)
	imp := env.startImp(t, opTok.access(), env.profileUUID(t, target.profileID), "support").
		wantStatus(t, http.StatusCreated).tok(t)

	path := "/v1/profiles/" + m.profUUID
	env.do(t, http.MethodPatch, path, imp.access(), map[string]string{"status": "ended", "end_reason": "offboarded"}).
		wantStatus(t, http.StatusForbidden)

	// A malformed body is still 400 first: the refusal follows validation.
	env.do(t, http.MethodPatch, path, imp.access(), map[string]string{"status": "ended"}).
		wantStatus(t, http.StatusBadRequest)

	if got := env.profileStatus(t, m.profUUID); got != "active" {
		t.Errorf("profile status after the refused end = %s, want active", got)
	}
	if n := env.count(t, "party authorizations",
		`SELECT count(*) FROM mod_users.credential_party_authorizations WHERE credential_id = $1 AND party_id = $2`, holderCred, acme); n != 1 {
		t.Errorf("credential authorizations for the party after the refused end = %d, want 1", n)
	}
	if n := env.count(t, "confined credentials",
		`SELECT count(*) FROM mod_users.credentials WHERE principal_id = $1 AND confined_to_party_id = $2 AND revoked_at IS NULL`, m.userID, acme); n != 1 {
		t.Errorf("unrevoked party-confined credentials after the refused end = %d, want 1", n)
	}
}
