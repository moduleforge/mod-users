//go:build integration

package integration

// The `authorized` credential rule at invite time: an invitation is accepted
// when the holder has any usable interactive credential (no credential can be
// authorized for a party before a profile exists), the member stays locked
// until a credential is authorized, a confirmation needs `update` on the party,
// and a credential_rule change clears the party's confirmations.

import (
	"net/http"
	"slices"
	"testing"
)

func TestAuthorizedInvite_SatisfiedAtInviteThenGatedUntilAuthorized(t *testing.T) {
	skipOperatorUnderOwningPartyGate(t)
	g := env.newGateParty(t)
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	a := env.registerVerified(t)

	// Invited without confirmation: the holder's password is a usable
	// interactive credential, so the rule is met at invite time.
	v := g.invite(t, map[string]any{"holder_email": a.Email}).wantStatus(t, http.StatusCreated).obj(t)
	profUUID := v["uuid"].(string)
	if n := env.scanInt64(t, "confirmations", `SELECT count(*) FROM profile_policy_confirmations WHERE party_id = $1`, g.id); n != 0 {
		t.Fatalf("the invitation wrote %d confirmation(s), want none", n)
	}
	env.do(t, http.MethodPost, "/v1/me/profiles/"+profUUID+"/accept", a.tk.access(), nil).wantStatus(t, http.StatusOK)

	// After acceptance no credential is authorized: the rule gates the member.
	tk := env.login(t, a)
	wantLocked(t, env.myProfiles(t, tk.access()), profUUID, "credential_not_authorized", "")
	env.switchTo(t, tk.access(), profUUID).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")

	// An administrator authorizes the credential; the member is unlocked.
	cred := env.credentialUUID(t, tk.access(), "password")
	env.do(t, http.MethodPut, authPath(g, cred), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	tk = env.login(t, a)
	wantAvailable(t, env.myProfiles(t, tk.access()), profUUID)
	env.switchTo(t, tk.access(), profUUID).wantStatus(t, http.StatusOK)
}

func TestAuthorizedInvite_NoUsableInteractiveCredentialStillNeedsConfirmation(t *testing.T) {
	skipOperatorUnderOwningPartyGate(t)
	g := env.newGateParty(t)
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	a := env.registerVerified(t)
	uid := env.principalID(t, a.Email)
	env.exec(t, "revoke all", `UPDATE mod_users.credentials SET revoked_at = now(), revoke_reason = 'user' WHERE principal_id = $1`, uid)

	if reason, acr := env.mismatch(t, uid, g.id); reason != "credential_not_authorized" || acr != 0 {
		t.Fatalf("mismatch = %q, acr %d; want credential_not_authorized, 0", reason, acr)
	}
	g.invite(t, map[string]any{"holder_email": a.Email}).wantMismatch(t, "credential_not_authorized")
	if n := env.profileCount(t, uid, g.id); n != 0 {
		t.Fatalf("a refused invitation left %d profile(s)", n)
	}
}

func TestAuthorizedInvite_ConfirmationNeedsUpdateOnTheParty(t *testing.T) {
	skipOperatorUnderOwningPartyGate(t)
	g := env.newGateParty(t)
	provider := env.addProviderRow(t, "inv-"+uniq("idp"))
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).wantStatus(t, http.StatusOK)

	// An operator-party inviter that may read and create profiles in the party,
	// but not update it (a type-entity grant needs an operator actor).
	acct := env.register(t)
	inviterUser := env.principalID(t, acct.Email)
	inviterProf, inviterUUID := env.seedPartyProfile(t, inviterUser, env.operatorPartyID(t), "active")
	env.exec(t, "inviter type grants",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by)
		 SELECT $1, o.id, t.entity_id, NULL FROM authz_operations o, types t WHERE o.slug IN ('create', 'read') AND t.slug = 'profile'`, inviterProf)
	tok := env.do(t, http.MethodPost, "/v1/session/switch", acct.tk.access(), map[string]string{"profile_uuid": inviterUUID}).
		wantStatus(t, http.StatusOK).tok(t)
	invite := func(body map[string]any) *resp {
		return env.do(t, http.MethodPost, g.path("/profiles"), tok.access(), body)
	}

	a := env.registerVerified(t)
	uid := env.principalID(t, a.Email)
	// Unconfirmed: still the 409 (the request is not affected by the new check).
	invite(map[string]any{"holder_email": a.Email}).wantMismatch(t, "provider_required")
	// Confirmed without `update` on the party: 403, nothing created.
	invite(map[string]any{"holder_email": a.Email, "confirm_policy_mismatch": true}).wantStatus(t, http.StatusForbidden)
	if n := env.profileCount(t, uid, g.id); n != 0 {
		t.Fatalf("a refused confirmed invitation left %d profile(s)", n)
	}
	// With `update` on the party the same request is honored.
	var updateOp int64
	updateOp = env.scanInt64(t, "update op", `SELECT id FROM authz_operations WHERE slug = 'update'`)
	env.exec(t, "inviter update grant", `INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, $3, NULL)`,
		inviterProf, updateOp, g.id)
	invite(map[string]any{"holder_email": a.Email, "confirm_policy_mismatch": true}).wantStatus(t, http.StatusCreated)
	if n := env.profileCount(t, uid, g.id); n != 1 {
		t.Fatalf("profiles = %d, want 1", n)
	}
}

func TestAuthorizedInvite_RuleChangeDropsPolicyConfirmations(t *testing.T) {
	g := env.newGateParty(t)
	other := env.newGateParty(t)
	provider := env.addProviderRow(t, "drop-"+uniq("idp"))
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).wantStatus(t, http.StatusOK)
	m := env.newGateMember(t, g)
	om := env.newGateMember(t, other)
	confirm := func() {
		env.exec(t, "confirm", `INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1) ON CONFLICT DO NOTHING`,
			m.profileID, g.id)
		env.exec(t, "confirm other", `INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1) ON CONFLICT DO NOTHING`,
			om.profileID, other.id)
	}
	rows := func(partyID int64) int64 {
		return env.scanInt64(t, "confirmations", `SELECT count(*) FROM profile_policy_confirmations WHERE party_id = $1`, partyID)
	}
	confirm()

	// The rule is unchanged: an identical write and a min_acr-only write keep them.
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).wantStatus(t, http.StatusOK)
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider, "min_acr": 2}).wantStatus(t, http.StatusOK)
	if rows(g.id) != 1 {
		t.Fatalf("confirmations after unchanged-rule writes = %d, want 1", rows(g.id))
	}

	// The rule changes: the party's confirmations go, another party's stay.
	env.obs.reset()
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	if rows(g.id) != 0 || rows(other.id) != 1 {
		t.Fatalf("confirmations after rule change = %d (party), %d (other); want 0, 1", rows(g.id), rows(other.id))
	}
	if got := env.obs.pairs(); !slices.Contains(got, "delete profile_policy_confirmation") || !slices.Contains(got, "update party_authn_policy") {
		t.Errorf("observations = %v, want the policy update and the confirmation delete", got)
	}

	// A party with no policy row gates by `any`: setting a rule counts as a change.
	confirm()
	env.setPolicy(t, other, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	if rows(other.id) != 0 {
		t.Fatalf("confirmations after the first rule write = %d, want 0", rows(other.id))
	}
}

func TestAuthorizedManagedUser_AutoAuthorizationNeedsUpdateOnTheParty(t *testing.T) {
	skipOperatorUnderOwningPartyGate(t)
	g := env.newGateParty(t)
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)

	// An operator-party caller that may read and create profiles and users in
	// the party, but not update it.
	acct := env.register(t)
	callerUser := env.principalID(t, acct.Email)
	callerProf, callerUUID := env.seedPartyProfile(t, callerUser, env.operatorPartyID(t), "active")
	env.exec(t, "caller type grants",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by)
		 SELECT $1, o.id, t.entity_id, NULL FROM authz_operations o, types t WHERE o.slug IN ('create', 'read') AND t.slug IN ('profile', 'user')`, callerProf)
	tok := env.do(t, http.MethodPost, "/v1/session/switch", acct.tk.access(), map[string]string{"profile_uuid": callerUUID}).
		wantStatus(t, http.StatusOK).tok(t)

	email := uniq("managed") + "@example.test"
	created := env.do(t, http.MethodPost, g.path("/managed-users"), tok.access(), map[string]string{"email": email, "password": testPassword}).
		wantStatus(t, http.StatusCreated).obj(t)
	profileUUID := created["profile"].(map[string]any)["uuid"].(string)

	// The user is provisioned, but without `update` its password is not authorized.
	uid := env.principalID(t, email)
	if n := env.count(t, "authorizations", `
SELECT count(*) FROM mod_users.credential_party_authorizations a JOIN mod_users.credentials c ON c.id = a.credential_id
 WHERE c.principal_id = $1 AND a.party_id = $2`, uid, g.id); n != 0 {
		t.Fatalf("authorizations of the managed user's password = %d, want 0", n)
	}
	// Signing in succeeds, but the only profile is locked: no profile is
	// selected and the lock is credential_not_authorized.
	login := env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]string{"email": email, "password": testPassword}).wantStatus(t, http.StatusOK).obj(t)
	if login["profile"] != nil || login["access_token"] != nil {
		t.Fatalf("login selected a profile: %v", login)
	}
	profiles, _ := login["profiles"].([]any)
	if len(profiles) != 1 {
		t.Fatalf("profiles = %v, want the single party profile", profiles)
	}
	p := profiles[0].(map[string]any)
	lock, _ := p["lock"].(map[string]any)
	if p["uuid"] != profileUUID || p["available"] != false || lock["reason"] != "credential_not_authorized" {
		t.Fatalf("profile = %v, want %s locked with credential_not_authorized", p, profileUUID)
	}
}
