//go:build integration

package integration

// Decision K: an impersonation session may not create a managed user. Creating
// one sets the user's password and, under the `authorized` credential rule,
// writes a credential_party_authorizations row; an impersonator may do neither.

import (
	"net/http"
	"testing"
)

// The impersonated profile administers the party (and, without impersonation,
// provisions a managed user whose password it authorizes); through an
// impersonation session the same request is 403 users.impersonation_not_allowed
// and writes no user, identifier, credential or credential authorization.
func TestManagedUser_RefusedWhileImpersonating(t *testing.T) {
	acme := env.seedParty(t, "Managed Imp "+uniq("p"))
	target := env.newImpAdmin(t, acme) // the impersonated administrator
	// The administrator's own password is authorized for the party, so its
	// profile stays available once the rule is `authorized`.
	adminCred := env.scanInt64(t, "admin password credential",
		`SELECT id FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'password' AND revoked_at IS NULL`, target.userID)
	env.exec(t, "authorize admin credential",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		adminCred, acme, target.profileID)
	// `update` on the party itself: the authority that also authorizes the new
	// user's password for the party.
	env.exec(t, "update grant on the party",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, $3, NULL)`,
		target.profileID, env.opID(t, "update"), acme)
	env.setPartyPolicy(t, acme, "authorized", 1)
	path := "/v1/parties/" + env.entityUUID(t, acme) + "/managed-users"

	managedUsers := func() int64 {
		return env.count(t, "managed users", `SELECT count(*) FROM mod_users.users WHERE managing_party_id = $1`, acme)
	}
	managedCredentials := func() int64 {
		return env.count(t, "managed user credentials", `
SELECT count(*) FROM mod_users.credentials c JOIN mod_users.users u ON u.entity_id = c.principal_id
 WHERE u.managing_party_id = $1`, acme)
	}
	authorizations := func() int64 {
		return env.count(t, "party authorizations", `SELECT count(*) FROM mod_users.credential_party_authorizations WHERE party_id = $1`, acme)
	}

	// Without impersonation the administrator provisions a managed user and,
	// holding `update` on the party, authorizes its password.
	env.do(t, http.MethodPost, path, target.tok.access(), map[string]string{"email": uniq("managed") + "@example.test", "password": testPassword}).
		wantStatus(t, http.StatusCreated)
	usersBefore, credsBefore, authsBefore := managedUsers(), managedCredentials(), authorizations()
	// The party's authorizations: the administrator's own and the managed user's.
	if usersBefore != 1 || credsBefore != 1 || authsBefore != 2 {
		t.Fatalf("control provisioning left %d managed users, %d credentials, %d party authorizations; want 1, 1, 2",
			usersBefore, credsBefore, authsBefore)
	}

	opAcct := env.registerVerified(t)
	opTok, _ := env.makeOperator(t, opAcct)
	imp := env.startImp(t, opTok.access(), env.profileUUID(t, target.profileID), "support").
		wantStatus(t, http.StatusCreated).tok(t)

	email := uniq("refused") + "@example.test"
	env.do(t, http.MethodPost, path, imp.access(), map[string]string{"email": email, "password": testPassword}).
		wantError(t, http.StatusForbidden, "users.impersonation_not_allowed", "")

	// A malformed body is still 400 first: the refusal follows validation.
	env.do(t, http.MethodPost, path, imp.access(), map[string]string{"email": "not-an-email", "password": testPassword}).
		wantStatus(t, http.StatusBadRequest)

	if n := env.count(t, "refused identifier", `SELECT count(*) FROM mod_users.user_identifiers WHERE value_canon = $1`, email); n != 0 {
		t.Errorf("the refused call created %d identifiers for %s", n, email)
	}
	if got := managedUsers(); got != usersBefore {
		t.Errorf("managed users after the refused call = %d, want %d", got, usersBefore)
	}
	if got := managedCredentials(); got != credsBefore {
		t.Errorf("managed user credentials after the refused call = %d, want %d", got, credsBefore)
	}
	if got := authorizations(); got != authsBefore {
		t.Errorf("credential authorizations for the party after the refused call = %d, want %d", got, authsBefore)
	}
}
