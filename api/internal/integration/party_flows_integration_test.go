//go:build integration

package integration

// The cross-cutting party flows of the identity model, end to end through the
// HTTP routes: the architecture decision record's worked example (John, Acme
// and Beta), the status-token interplay, gating and confinement, outside
// collaborators and the trusted admission path. Each flow reuses the stack,
// the fake identity provider (OAuth.ExchangeFn) and the fixtures of the other
// suites; the per-route detail of invitations, transitions, managed users and
// the policy administration lives in profiles_, party_policy_ and
// collaborator_integration_test.go.
//
// Until the authorization-engine phase the party routes' type grants and the
// wildcard fallback go only to operator actors (decision U27), so in the worked
// example Acme is the deployment's operator party and its administrator Alice
// holds the bootstrap wildcard grant; Beta stays a non-operator party whose
// profiles Alice administers through that wildcard.

import (
	"context"
	"net/http"
	"testing"

	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// lockedProfile is a profile of GET /v1/me/profiles with the lock remedy.
type lockedProfile struct {
	UUID      string `json:"uuid"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Available bool   `json:"available"`
	Lock      *struct {
		Reason string `json:"reason"`
		Remedy *struct {
			Kind       string `json:"kind"`
			ProviderID string `json:"provider_id"`
		} `json:"remedy"`
	} `json:"lock"`
}

// myProfiles lists the caller's profiles with the availability for its session.
func (s *stack) myProfiles(t *testing.T, token string) map[string]lockedProfile {
	t.Helper()
	var body struct {
		Profiles []lockedProfile `json:"profiles"`
	}
	s.do(t, http.MethodGet, "/v1/me/profiles", token, nil).wantStatus(t, http.StatusOK).into(t, &body)
	out := make(map[string]lockedProfile, len(body.Profiles))
	for _, p := range body.Profiles {
		out[p.UUID] = p
	}
	return out
}

// wantLocked fails unless the profile is listed, unavailable, with the lock reason
// and (when remedy is non-empty) the remedy kind.
func wantLocked(t *testing.T, profiles map[string]lockedProfile, uuid, reason, remedy string) lockedProfile {
	t.Helper()
	p, ok := profiles[uuid]
	if !ok {
		t.Fatalf("profile %s is not listed: %+v", uuid, profiles)
	}
	if p.Available || p.Lock == nil || p.Lock.Reason != reason {
		t.Fatalf("profile %s = %+v (lock %+v), want locked with %s", uuid, p, p.Lock, reason)
	}
	if remedy != "" && (p.Lock.Remedy == nil || p.Lock.Remedy.Kind != remedy) {
		t.Fatalf("profile %s remedy = %+v, want %s", uuid, p.Lock.Remedy, remedy)
	}
	return p
}

// wantAvailable fails unless the profile is listed and available.
func wantAvailable(t *testing.T, profiles map[string]lockedProfile, uuid string) {
	t.Helper()
	if p, ok := profiles[uuid]; !ok || !p.Available || p.Lock != nil {
		t.Fatalf("profile %s = %+v (listed %v), want available", uuid, p, ok)
	}
}

// switchTo switches the session of token to the profile.
func (s *stack) switchTo(t *testing.T, token, profileUUID string) *resp {
	t.Helper()
	return s.do(t, http.MethodPost, "/v1/session/switch", token, map[string]string{"profile_uuid": profileUUID})
}

// acmeFixture is Acme, the deployment's operator party, with its administrator
// Alice (an operator-party profile with the bootstrap wildcard grant).
type acmeFixture struct {
	gateParty
	aliceProfile int64
}

// newAcme returns Acme and Alice. Setting a policy on the operator party would
// leak into every later test that makes an operator, so the policy row is
// removed again when the test ends.
func (s *stack) newAcme(t *testing.T) acmeFixture {
	t.Helper()
	alice := s.registerVerified(t)
	tok, profileID := s.makeOperator(t, alice)
	party := s.operatorPartyID(t)
	t.Cleanup(func() {
		s.exec(t, "reset the operator party policy", `DELETE FROM mod_users.party_authn_policies WHERE party_id = $1`, party)
	})
	return acmeFixture{
		gateParty:    gateParty{id: party, uuid: s.entityUUID(t, party), admin: alice, adminTok: tok},
		aliceProfile: profileID,
	}
}

// requireAuthorizedCredentials gives Acme the credential rule `authorized`,
// first authorizing Alice's own password so her session keeps acting as Acme.
func (s *stack) requireAuthorizedCredentials(t *testing.T, a acmeFixture) {
	t.Helper()
	alicePassword := s.credentialUUID(t, a.admin.tk.access(), "password")
	s.do(t, http.MethodPut, authPath(a.gateParty, alicePassword), a.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	s.setPolicy(t, a.gateParty, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	if status, _ := s.probe(t, "/v1/probe", a.adminTok.access()); status != http.StatusOK {
		t.Fatalf("Alice lost her own session under the authorized rule: %d", status)
	}
}

// john is a user who signed up with a personal OIDC login (credential C1) and
// belongs to Acme, with a password (C2) bound to his Acme address.
type john struct {
	provider  string
	identity  auth.ExternalIdentity
	homeEmail string
	acmeEmail string
	c1        tokens // the sign-up session, acting as PR1
	userID    int64
	pr1       string // personal profile uuid
	pr2       string // Acme profile uuid
	c1UUID    string
	c2UUID    string
}

func (j john) c2Account() account { return account{Email: j.acmeEmail, Password: testPassword} }

// signUpJohn (provider is a registered bring-your-own provider, registered by the
// top-level test so it outlives a subtest) performs the first two steps of the worked example up to John's
// admission to Acme: he signs up through OIDC (user, C1, PR1, person), Alice
// invites him and he accepts from the C1 session (PR2 active). Acme keeps its
// default policy for now: an invitation under the `authorized` rule to a
// holder with no authorized credential is an outside-collaborator mismatch that
// needs a confirmation, and a confirmation would exempt the rule.
func (s *stack) signUpJohn(t *testing.T, a acmeFixture, provider string) john {
	t.Helper()
	j := john{
		homeEmail: uniq("john") + "@home.example",
		acmeEmail: uniq("john") + "@acme.example",
	}
	j.provider = provider
	j.identity = idpIdentity(j.homeEmail, true)
	s.idpReturns(t, j.identity, auth.StatePayload{})
	j.c1 = s.signIn(t, j.provider)
	if j.c1.Profile == nil || j.c1.Profile.Kind != "personal" {
		t.Fatalf("the sign-up session = %+v, want the personal profile", j.c1.Profile)
	}
	j.pr1 = j.c1.Profile.UUID
	j.userID = s.principalID(t, j.homeEmail)
	j.c1UUID = s.credentialUUID(t, j.c1.access(), "oidc")

	// The user owns his person record and the personal profile belongs to him.
	if n := s.count(t, "person", `
SELECT count(*) FROM mod_users.users u JOIN entities pe ON pe.id = u.person_id
 WHERE u.entity_id = $1 AND pe.owner_id = $1`, j.userID); n != 1 {
		t.Fatalf("John's person record is not owned by John")
	}

	// Alice invites John to Acme; he accepts from the C1 session.
	invited := s.do(t, http.MethodPost, a.path("/profiles"), a.adminTok.access(), map[string]any{"holder_email": j.homeEmail}).
		wantStatus(t, http.StatusCreated).obj(t)
	j.pr2 = invited["uuid"].(string)
	s.do(t, http.MethodPost, "/v1/me/profiles/"+j.pr2+"/accept", j.c1.access(), nil).wantStatus(t, http.StatusOK)
	return j
}

// addAcmePassword gives John the password C2, bound to his verified Acme
// address, and points C2's landing profile at PR2.
func (s *stack) addAcmePassword(t *testing.T, j *john) {
	t.Helper()
	added := s.do(t, http.MethodPost, "/v1/me/identifiers", j.c1.access(), map[string]string{"value": j.acmeEmail}).
		wantStatus(t, http.StatusCreated).obj(t)
	identifier := added["uuid"].(string)
	seen := s.mail.count(j.acmeEmail)
	s.do(t, http.MethodPost, "/v1/me/identifiers/"+identifier+"/verification", j.c1.access(), nil).wantStatus(t, http.StatusNoContent)
	s.do(t, http.MethodPost, "/v1/me/identifiers/"+identifier+"/verify", j.c1.access(),
		map[string]string{"code": s.mail.codeAfter(t, j.acmeEmail, seen)}).wantStatus(t, http.StatusOK)

	c2 := s.do(t, http.MethodPost, "/v1/me/credentials/password", j.c1.access(),
		map[string]any{"password": testPassword, "identifier_uuid": identifier}).wantStatus(t, http.StatusCreated).obj(t)
	j.c2UUID = c2["uuid"].(string)
	s.do(t, http.MethodPatch, "/v1/me/credentials/"+j.c2UUID, j.c1.access(), map[string]any{"default_profile_uuid": j.pr2}).
		wantStatus(t, http.StatusOK)
}

// joinBeta has Alice invite John to Beta and John accept from the C1 session.
func (s *stack) joinBeta(t *testing.T, a acmeFixture, j john, beta int64) string {
	t.Helper()
	invited := s.do(t, http.MethodPost, "/v1/parties/"+s.entityUUID(t, beta)+"/profiles", a.adminTok.access(),
		map[string]any{"holder_email": j.homeEmail}).wantStatus(t, http.StatusCreated).obj(t)
	pr3 := invited["uuid"].(string)
	s.do(t, http.MethodPost, "/v1/me/profiles/"+pr3+"/accept", j.c1.access(), nil).wantStatus(t, http.StatusOK)
	return pr3
}

// signInAgain signs John in again through the C1 identity.
func (s *stack) signInAgain(t *testing.T, j john) tokens {
	t.Helper()
	s.idpReturns(t, j.identity, auth.StatePayload{})
	return s.signIn(t, j.provider)
}

// TestPartyFlows_ADRWorkedExample is the worked example of the identity-model
// architecture decision record ("Party profiles"): John, Acme and Beta.
func TestPartyFlows_ADRWorkedExample(t *testing.T) {
	acme := env.newAcme(t)
	beta := env.seedParty(t, "Beta "+uniq("p")) // credential_rule 'any' (no policy row)
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	var j john
	var pr3 string

	t.Run("1 John signs up with a personal OIDC login", func(t *testing.T) {
		j = env.signUpJohn(t, acme, provider)
		if n := env.count(t, "C1", `
SELECT count(*) FROM mod_users.credentials c JOIN mod_users.cred_oidc o ON o.credential_id = c.id
 WHERE c.principal_id = $1 AND c.uuid = $2::uuid AND c.revoked_at IS NULL AND c.confined_to_party_id IS NULL`,
			j.userID, j.c1UUID); n != 1 {
			t.Fatalf("John has no unconfined OIDC credential C1")
		}
		if n := env.count(t, "PR1", `
SELECT count(*) FROM profiles p JOIN entities e ON e.id = p.entity_id
 WHERE e.uuid = $1::uuid AND p.holder_id = $2 AND p.party_id IS NULL AND p.status = 'active'`, j.pr1, j.userID); n != 1 {
			t.Fatalf("John has no active personal profile PR1")
		}
	})

	t.Run("2 Alice invites John, who accepts from a C1 session", func(t *testing.T) {
		if got := env.profileStatus(t, j.pr2); got != "active" {
			t.Fatalf("PR2 = %s, want active", got)
		}
		// Acme now requires an authorized credential; Alice's own is authorized first.
		env.requireAuthorizedCredentials(t, acme)
		pol := env.do(t, http.MethodGet, acme.path("/authn-policy"), acme.adminTok.access(), nil).wantStatus(t, http.StatusOK).obj(t)
		if pol["credential_rule"] != "authorized" {
			t.Fatalf("Acme policy = %v", pol)
		}
	})

	t.Run("3 John adds password C2 for his Acme address, Alice authorizes it", func(t *testing.T) {
		env.addAcmePassword(t, &j)
		auths := env.authorizationCount(t, j.c2UUID, acme.id)
		if auths != 0 {
			t.Fatalf("C2 is authorized before Alice acted")
		}
		env.do(t, http.MethodPut, authPath(acme.gateParty, j.c2UUID), acme.adminTok.access(), nil).wantStatus(t, http.StatusOK)
		if env.authorizationCount(t, j.c2UUID, acme.id) != 1 {
			t.Fatalf("C2 is not authorized for Acme")
		}
		if env.authorizationCount(t, j.c1UUID, acme.id) != 0 {
			t.Fatalf("C1 must stay unauthorized for Acme")
		}
	})

	t.Run("4 C1 lands on PR1 with PR2 locked, C2 lands on PR2", func(t *testing.T) {
		c1 := env.signInAgain(t, j)
		if c1.Profile == nil || c1.Profile.UUID != j.pr1 {
			t.Fatalf("C1 landed on %+v, want PR1 %s", c1.Profile, j.pr1)
		}
		profiles := env.myProfiles(t, c1.access())
		wantAvailable(t, profiles, j.pr1)
		wantLocked(t, profiles, j.pr2, "credential_not_authorized", svc.RemedySignInWithAuthorizedCredential)
		env.switchTo(t, c1.access(), j.pr2).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")

		c2 := env.login(t, j.c2Account())
		if c2.Profile == nil || c2.Profile.UUID != j.pr2 || !c2.Profile.Available {
			t.Fatalf("C2 landed on %+v, want PR2 %s", c2.Profile, j.pr2)
		}
		if status, p := env.probe(t, "/v1/probe", c2.access()); status != http.StatusOK || p.ProfileID != env.entityIDOf(t, j.pr2) {
			t.Fatalf("C2 token probe = %d %+v", status, p)
		}
	})

	t.Run("5 Beta's profile PR3 is reachable from the C1 session", func(t *testing.T) {
		pr3 = env.joinBeta(t, acme, j, beta)
		c1 := env.signInAgain(t, j)
		wantAvailable(t, env.myProfiles(t, c1.access()), pr3)
		sw := env.switchTo(t, c1.access(), pr3).wantStatus(t, http.StatusOK).tok(t)
		if status, p := env.probe(t, "/v1/probe", sw.access()); status != http.StatusOK || p.ProfileID != env.entityIDOf(t, pr3) {
			t.Fatalf("PR3 probe = %d %+v", status, p)
		}
	})

	t.Run("6 John leaves Acme", func(t *testing.T) {
		c2 := env.login(t, j.c2Account())
		if status, _ := env.probe(t, "/v1/probe", c2.access()); status != http.StatusOK {
			t.Fatalf("the PR2 token before leaving = %d", status)
		}
		c1 := env.signInAgain(t, j)
		// The C1 session is locked out of PR2 by Acme's gate, so it cannot leave it;
		// the C2 session, which the gate admits, can.
		env.do(t, http.MethodPost, "/v1/me/profiles/"+j.pr2+"/leave", c1.access(), nil).
			wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "credential_not_authorized")
		env.do(t, http.MethodPost, "/v1/me/profiles/"+j.pr2+"/leave", c2.access(), nil).wantStatus(t, http.StatusNoContent)

		env.do(t, http.MethodGet, "/v1/probe", c2.access(), nil).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
		if n := env.authorizationCount(t, j.c2UUID, acme.id); n != 0 {
			t.Errorf("C2's Acme authorization survived the departure (%d)", n)
		}
		// PR1 and PR3 still work.
		if status, p := env.probe(t, "/v1/probe", c1.access()); status != http.StatusOK || p.ProfileID != env.entityIDOf(t, j.pr1) {
			t.Errorf("PR1 after the departure = %d %+v", status, p)
		}
		env.switchTo(t, c1.access(), pr3).wantStatus(t, http.StatusOK)
		profiles := env.myProfiles(t, c1.access())
		if _, listed := profiles[j.pr2]; listed {
			t.Errorf("the ended PR2 is still listed")
		}
	})
}

// TestPartyFlows_ADRWorkedExample_ConfinedVariant is the worked example's
// variant: C2 is confined to Acme instead of authorized for it.
func TestPartyFlows_ADRWorkedExample_ConfinedVariant(t *testing.T) {
	acme := env.newAcme(t)
	beta := env.seedParty(t, "Beta "+uniq("p"))
	j := env.signUpJohn(t, acme, env.addProvider(t, config.Provider{JITMode: config.JITModeBYO}))
	env.requireAuthorizedCredentials(t, acme)
	env.addAcmePassword(t, &j)
	pr3 := env.joinBeta(t, acme, j, beta)
	// Written directly: C2 is the collaborator's own credential, which the
	// administrator door of the confinement route does not reach.
	env.exec(t, "confine C2", `UPDATE mod_users.credentials SET confined_to_party_id = $1 WHERE uuid = $2::uuid`, acme.id, j.c2UUID)
	if got := env.confinedTo(t, j.c2UUID); got != acme.id {
		t.Fatalf("C2 confined to %d, want Acme %d", got, acme.id)
	}

	// A confined credential counts as authorized: C2 lands on PR2 and nowhere else.
	c2 := env.login(t, j.c2Account())
	if c2.Profile == nil || c2.Profile.UUID != j.pr2 {
		t.Fatalf("C2 landed on %+v, want PR2", c2.Profile)
	}
	profiles := env.myProfiles(t, c2.access())
	wantAvailable(t, profiles, j.pr2)
	for _, other := range []string{j.pr1, pr3} {
		p, ok := profiles[other]
		if !ok || p.Available {
			t.Errorf("profile %s as C2 sees it = %+v (listed %v), want listed and unavailable", other, p, ok)
		}
		env.switchTo(t, c2.access(), other).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
	}

	// C1 is unconfined: PR1 and PR3 work, PR2 is locked (C1 is not authorized).
	c1 := env.signInAgain(t, j)
	wantAvailable(t, env.myProfiles(t, c1.access()), pr3)
	wantLocked(t, env.myProfiles(t, c1.access()), j.pr2, "credential_not_authorized", svc.RemedySignInWithAuthorizedCredential)

	// Leaving, from the C2 session the gate admits, revokes C2 (reason
	// departure) and, with it, the C2 session.
	env.do(t, http.MethodPost, "/v1/me/profiles/"+j.pr2+"/leave", c2.access(), nil).wantStatus(t, http.StatusNoContent)
	var revoked, reason *string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT revoked_at::text, revoke_reason FROM mod_users.credentials WHERE uuid = $1::uuid`, j.c2UUID).Scan(&revoked, &reason); err != nil {
		t.Fatal(err)
	}
	if revoked == nil || reason == nil || *reason != "departure" {
		t.Fatalf("C2: revoked=%v reason=%v, want revoked with departure", revoked, reason)
	}
	env.do(t, http.MethodGet, "/v1/probe", c2.access(), nil).wantStatus(t, http.StatusUnauthorized)
	if got := env.sessionRow(t, c2.Session.UUID); got.RevokedAt == nil {
		t.Errorf("the C2 session was not revoked")
	}
	// C1 is untouched.
	if status, _ := env.probe(t, "/v1/probe", c1.access()); status != http.StatusOK {
		t.Errorf("the C1 session after the departure = %d", status)
	}
}

// entityIDOf resolves an entity uuid to its id.
func (s *stack) entityIDOf(t *testing.T, entityUUID string) int64 {
	t.Helper()
	return s.scanInt64(t, "entity id", `SELECT id FROM entities WHERE uuid = $1::uuid`, entityUUID)
}

// TestPartyFlows_SuspendedProfileTokenIsRefusedUntilResumed covers the live
// effect of a status change: the profile's token is refused on the very next
// request after a suspension and accepted again, unchanged, after the resume.
func TestPartyFlows_SuspendedProfileTokenIsRefusedUntilResumed(t *testing.T) {
	f := env.newParty(t)
	a, id := env.accepted(t, f)
	tok := env.switchTo(t, env.login(t, a).access(), id).wantStatus(t, http.StatusOK).tok(t)
	if status, _ := env.probe(t, "/v1/probe", tok.access()); status != http.StatusOK {
		t.Fatalf("probe as the party profile = %d", status)
	}
	patch := func(status string) {
		t.Helper()
		env.do(t, http.MethodPatch, "/v1/profiles/"+id, f.adminTok.access(), map[string]string{"status": status}).wantStatus(t, http.StatusOK)
	}
	patch("suspended")
	env.do(t, http.MethodGet, "/v1/probe", tok.access(), nil).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
	wantLocked(t, env.myProfiles(t, env.login(t, a).access()), id, "inactive", "")
	patch("active")
	if status, p := env.probe(t, "/v1/probe", tok.access()); status != http.StatusOK || p.ProfileID != env.entityIDOf(t, id) {
		t.Fatalf("probe after the resume = %d %+v", status, p)
	}
}

// TestPartyFlows_HolderAndAdministratorAuthority separates the two doors: the
// holder cannot administer the profile and the administrator cannot act as the
// holder.
func TestPartyFlows_HolderAndAdministratorAuthority(t *testing.T) {
	f := env.newParty(t)
	holder, id := env.invited(t, f)

	// The administrator cannot accept (or decline) on the holder's behalf.
	for _, action := range []string{"accept", "decline", "leave"} {
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/"+action, f.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)
	}
	if got := env.profileStatus(t, id); got != "invited" {
		t.Fatalf("an administrator acted as the holder: %s", got)
	}
	env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/accept", holder.tk.access(), nil).wantStatus(t, http.StatusOK)

	// The holder has no authority over the party's profiles, not even their own.
	for _, body := range []map[string]string{{"status": "suspended"}, {"status": "ended", "end_reason": "x"}} {
		r := env.do(t, http.MethodPatch, "/v1/profiles/"+id, holder.tk.access(), body)
		if r.status != http.StatusNotFound && r.status != http.StatusForbidden {
			t.Errorf("holder PATCH %v = %d, want a refusal; %s", body, r.status, r.raw)
		}
	}
	env.do(t, http.MethodGet, f.profilesPath(), holder.tk.access(), nil).wantStatus(t, http.StatusNotFound)
	env.do(t, http.MethodPost, f.profilesPath(), holder.tk.access(), map[string]any{"holder_email": env.registerVerified(t).Email}).
		wantStatus(t, http.StatusNotFound)
	if got := env.profileStatus(t, id); got != "active" {
		t.Fatalf("the holder administered the profile: %s", got)
	}
}

// TestPartyFlows_ManagedUserCreatedThroughTheRouteIsNotInvitable shows that a
// managed user created through the party route cannot join another party.
func TestPartyFlows_ManagedUserCreatedThroughTheRouteIsNotInvitable(t *testing.T) {
	owner := env.newParty(t)
	other := env.newParty(t)
	email := uniq("managed") + "@example.test"
	env.do(t, http.MethodPost, "/v1/parties/"+owner.partyUUID+"/managed-users", owner.adminTok.access(),
		map[string]string{"email": email, "password": testPassword}).wantStatus(t, http.StatusCreated)

	// An invitation resolves only a verified address, so verify it to reach the
	// admissibility check.
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE mod_users.user_identifiers SET verified_at = now() WHERE kind = 'email' AND value_canon = $1`, email); err != nil {
		t.Fatalf("verify %s: %v", email, err)
	}
	env.inviteEmail(t, other, email).wantError(t, http.StatusConflict, "conflict", "users.holder_not_admissible")
	uid := env.principalID(t, email)
	if n := env.profileCount(t, uid, other.partyID); n != 0 {
		t.Errorf("the managed user holds %d profile(s) in the other party", n)
	}
	// It lands on its party profile and has no personal profile to fall back on.
	tk := env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]string{"email": email, "password": testPassword}).
		wantStatus(t, http.StatusOK).tok(t)
	if tk.Profile == nil || tk.Profile.Kind != "party" || tk.Profile.Party == nil || tk.Profile.Party.UUID != owner.partyUUID {
		t.Fatalf("landing = %+v, want the owning party's profile", tk.Profile)
	}
	if len(tk.Profiles) != 1 {
		t.Errorf("profiles = %+v, want only the party profile", tk.Profiles)
	}
}

// TestPartyFlows_ProviderPolicyRemedy shows the `provider` rule: a password
// session sees the profile locked with the sign-in-with-provider remedy naming
// the provider, and a session through that provider reaches it.
func TestPartyFlows_ProviderPolicyRemedy(t *testing.T) {
	g := env.newGateParty(t)
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	env.addProviderRow(t, provider)
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).wantStatus(t, http.StatusOK)

	// A password-only member: locked, with a remedy that names the provider.
	pw := env.newGateMember(t, g)
	tk := env.login(t, pw.acct)
	p := wantLocked(t, env.myProfiles(t, tk.access()), pw.profUUID, "provider_required", svc.RemedySignInWithProvider)
	if p.Lock.Remedy.ProviderID != provider {
		t.Errorf("remedy provider = %q, want %q", p.Lock.Remedy.ProviderID, provider)
	}
	env.switchTo(t, tk.access(), pw.profUUID).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")

	// A member who signs in through the provider reaches the profile.
	email := uniq("oidc") + "@example.test"
	id := idpIdentity(email, true)
	env.idpReturns(t, id, auth.StatePayload{})
	env.signIn(t, provider)
	uid := env.principalID(t, email)
	profileID, profUUID := env.seedPartyProfile(t, uid, g.id, "active")
	again := env.signIn(t, provider)
	wantAvailable(t, env.myProfiles(t, again.access()), profUUID)
	sw := env.switchTo(t, again.access(), profUUID).wantStatus(t, http.StatusOK).tok(t)
	if status, pr := env.probe(t, "/v1/probe", sw.access()); status != http.StatusOK || pr.ProfileID != profileID {
		t.Fatalf("provider session probe = %d %+v", status, pr)
	}
}

// TestPartyFlows_MinACRLockAndStepUpUnlock shows min_acr 2: a password session
// is locked with the step-up remedy, and an emailed code stepped up in the same
// session adds a second factor class and unlocks the profile.
func TestPartyFlows_MinACRLockAndStepUpUnlock(t *testing.T) {
	g := env.newGateParty(t)
	env.setPolicy(t, g, map[string]any{"credential_rule": "any", "min_acr": 2}).wantStatus(t, http.StatusOK)
	m := env.newGateMember(t, g)
	env.verifyRegisteredEmail(t, m.acct)

	tk := env.login(t, m.acct)
	wantLocked(t, env.myProfiles(t, tk.access()), m.profUUID, "acr_too_low", svc.RemedyStepUp)
	env.switchTo(t, tk.access(), m.profUUID).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")

	seen := env.mail.count(m.acct.Email)
	env.do(t, http.MethodPost, "/v1/session/step-up/email-code/request", tk.access(), map[string]string{"email": m.acct.Email}).
		wantStatus(t, http.StatusNoContent)
	env.do(t, http.MethodPost, "/v1/session/step-up/email-code/verify", tk.access(),
		map[string]string{"email": m.acct.Email, "code": env.mail.codeAfter(t, m.acct.Email, seen)}).wantStatus(t, http.StatusOK)

	// The same session, now with two factor classes.
	wantAvailable(t, env.myProfiles(t, tk.access()), m.profUUID)
	sw := env.switchTo(t, tk.access(), m.profUUID).wantStatus(t, http.StatusOK).tok(t)
	if sw.Session == nil || sw.Session.UUID != tk.Session.UUID {
		t.Errorf("the unlock did not stay in the same session: %+v vs %+v", sw.Session, tk.Session)
	}
	if sw.Session != nil && sw.Session.ACR < 2 {
		t.Errorf("session acr = %d, want at least 2", sw.Session.ACR)
	}
}

// TestPartyFlows_ConfinedFactorNeverListsThePersonalProfileAsAvailable shows
// that a session holding a factor confined to a party reaches that party's
// profiles only.
func TestPartyFlows_ConfinedFactorNeverListsThePersonalProfileAsAvailable(t *testing.T) {
	g := env.newGateParty(t)
	other := env.seedParty(t, "Other "+uniq("p"))
	m := env.newGateMember(t, g)
	_, otherProf := env.seedPartyProfile(t, m.userID, other, "active")
	pw := env.credentialUUID(t, m.acct.tk.access(), "password")
	// Written directly: the member's own credential is outside the
	// administrator door of the confinement route.
	env.exec(t, "confine password", `UPDATE mod_users.credentials SET confined_to_party_id = $1 WHERE uuid = $2::uuid`, g.id, pw)

	tk := env.login(t, m.acct)
	if tk.Profile == nil || tk.Profile.UUID != m.profUUID {
		t.Fatalf("landing = %+v, want the confining party's profile", tk.Profile)
	}
	personal := env.profileUUID(t, env.personalProfileID(t, m.userID))
	profiles := env.myProfiles(t, tk.access())
	wantAvailable(t, profiles, m.profUUID)
	for _, u := range []string{personal, otherProf} {
		if p, ok := profiles[u]; ok && p.Available {
			t.Errorf("profile %s is available to a session with a confined factor", u)
		}
		env.switchTo(t, tk.access(), u).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
	}
}

// TestPartyFlows_OutsideCollaboratorThroughTheRoutes follows a confirmed
// outside collaborator end to end: the invitation needs the confirmation, the
// profile is then reachable despite the credential rule, and raising the
// party's min_acr locks it on the next request.
func TestPartyFlows_OutsideCollaboratorThroughTheRoutes(t *testing.T) {
	skipOperatorUnderOwningPartyGate(t)
	g := env.newGateParty(t)
	provider := env.addProviderRow(t, "collab-"+uniq("idp"))
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider}).wantStatus(t, http.StatusOK)
	outsider := env.registerVerified(t)

	g.invite(t, map[string]any{"holder_email": outsider.Email}).wantMismatch(t, "provider_required")
	v := g.invite(t, map[string]any{"holder_email": outsider.Email, "confirm_policy_mismatch": true}).
		wantStatus(t, http.StatusCreated).obj(t)
	profUUID := v["uuid"].(string)
	env.do(t, http.MethodPost, "/v1/me/profiles/"+profUUID+"/accept", outsider.tk.access(), nil).wantStatus(t, http.StatusOK)

	// Reachable although the outsider has no credential of the required provider.
	tk := env.login(t, outsider)
	wantAvailable(t, env.myProfiles(t, tk.access()), profUUID)
	party := env.switchTo(t, tk.access(), profUUID).wantStatus(t, http.StatusOK).tok(t)
	if status, _ := env.probe(t, "/v1/probe", party.access()); status != http.StatusOK {
		t.Fatalf("the confirmed collaborator's token = %d", status)
	}

	// The confirmation never exempts min_acr: raising it locks the same token.
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provider, "min_acr": 2}).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodGet, "/v1/probe", party.access(), nil).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
	wantLocked(t, env.myProfiles(t, env.login(t, outsider).access()), profUUID, "acr_too_low", svc.RemedyStepUp)
}

// TestPartyFlows_TrustedAdmissionIsReachableThroughTheRoutes admits an invited
// holder through AdmitInTx and uses the profile through the routes; a suspended
// profile is refused and stays unusable.
func TestPartyFlows_TrustedAdmissionIsReachableThroughTheRoutes(t *testing.T) {
	f := env.newParty(t)

	a, id := env.invited(t, f)
	uid := env.principalID(t, a.Email)
	p, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid})
	if err != nil {
		t.Fatalf("AdmitInTx: %v", err)
	}
	if p.UUID.String() != id || p.Status != "active" {
		t.Fatalf("admitted profile = %+v, want %s active", p, id)
	}
	tk := env.login(t, a)
	wantAvailable(t, env.myProfiles(t, tk.access()), id)
	sw := env.switchTo(t, tk.access(), id).wantStatus(t, http.StatusOK).tok(t)
	if status, pr := env.probe(t, "/v1/probe", sw.access()); status != http.StatusOK || pr.ProfileID != env.entityIDOf(t, id) {
		t.Fatalf("admitted profile probe = %d %+v", status, pr)
	}

	// A suspended profile is refused by the admission and stays locked.
	env.do(t, http.MethodPatch, "/v1/profiles/"+id, f.adminTok.access(), map[string]string{"status": "suspended"}).wantStatus(t, http.StatusOK)
	if _, err := env.admit(t, f, svc.AdmitInput{PartyID: f.partyID, HolderUserID: uid}); err == nil {
		t.Fatal("AdmitInTx admitted a suspended profile")
	}
	if got := env.profileStatus(t, id); got != "suspended" {
		t.Errorf("status after the refused admission = %s", got)
	}
	env.do(t, http.MethodGet, "/v1/probe", sw.access(), nil).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
}
