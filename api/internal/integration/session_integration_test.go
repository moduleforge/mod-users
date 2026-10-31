//go:build integration

package integration

// Session behavior over the composed stack: the landing chain, refresh
// rotation and reuse, fork, switch, per-request revocation and step-up.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// credentialUUID returns the uuid of the account's password credential from
// GET /v1/me/credentials.
func (s *stack) credentialUUID(t *testing.T, token, kind string) string {
	t.Helper()
	var body struct {
		Credentials []struct {
			UUID string `json:"uuid"`
			Kind string `json:"kind"`
		} `json:"credentials"`
	}
	s.do(t, http.MethodGet, "/v1/me/credentials", token, nil).wantStatus(t, http.StatusOK).into(t, &body)
	for _, c := range body.Credentials {
		if c.Kind == kind {
			return c.UUID
		}
	}
	t.Fatalf("no %s credential in %+v", kind, body.Credentials)
	return ""
}

// refreshReq is the body of the refresh grant for tk's current refresh token,
// naming the profile tk acts as.
func refreshReq(tk tokens) map[string]string {
	return map[string]string{
		"grant_type": "refresh_token", "refresh_token": tk.refresh(), "profile_uuid": tk.Profile.UUID,
	}
}

// refreshWith is the refresh grant for an explicit token and profile.
func refreshWith(refresh, profile string) map[string]string {
	return map[string]string{"grant_type": "refresh_token", "refresh_token": refresh, "profile_uuid": profile}
}

// loginRaw signs in and returns the response without requiring a token.
func (s *stack) loginRaw(t *testing.T, email, password string, extra map[string]string) *resp {
	t.Helper()
	body := map[string]string{"email": email, "password": password}
	for k, v := range extra {
		body[k] = v
	}
	return s.do(t, http.MethodPost, "/v1/auth/login", "", body)
}

// profileOf returns the uuid of the profile an access token acts as.
func (s *stack) profileOf(t *testing.T, token string) string {
	t.Helper()
	var info struct {
		Profile *profileView `json:"profile"`
	}
	s.do(t, http.MethodGet, "/v1/session", token, nil).wantStatus(t, http.StatusOK).into(t, &info)
	if info.Profile == nil {
		t.Fatalf("session has no profile")
	}
	return info.Profile.UUID
}

func TestLogin_Landing_PersonalIsTheDefault(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	personal := env.profileUUID(t, env.personalProfileID(t, uid))
	party := env.seedParty(t, "Landing Party "+uniq("p"))
	env.seedPartyProfile(t, uid, party, "active")

	tk := env.login(t, a)
	if tk.Profile == nil || tk.Profile.UUID != personal || tk.Profile.Kind != "personal" {
		t.Fatalf("landed on %+v, want the personal profile %s", tk.Profile, personal)
	}
	if len(tk.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(tk.Profiles))
	}
	if tk.SelectionRequired {
		t.Fatal("selection_required with a personal profile")
	}
}

func TestLogin_Landing_UserDefaultBeatsPersonal(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	party := env.seedParty(t, "Landing Party "+uniq("p"))
	_, partyProfile := env.seedPartyProfile(t, uid, party, "active")

	env.do(t, http.MethodPut, "/v1/me", a.tk.access(), map[string]any{"default_profile_uuid": partyProfile}).wantStatus(t, http.StatusOK)

	tk := env.login(t, a)
	if tk.Profile == nil || tk.Profile.UUID != partyProfile || tk.Profile.Kind != "party" {
		t.Fatalf("landed on %+v, want the user default %s", tk.Profile, partyProfile)
	}
	if got := env.profileOf(t, tk.access()); got != partyProfile {
		t.Fatalf("token acts as %s, want %s", got, partyProfile)
	}
}

func TestLogin_Landing_CredentialDefaultBeatsUserDefault(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	personal := env.profileUUID(t, env.personalProfileID(t, uid))
	party1 := env.seedParty(t, "Landing Party "+uniq("p"))
	party2 := env.seedParty(t, "Landing Party "+uniq("p"))
	_, profA := env.seedPartyProfile(t, uid, party1, "active")
	_, profB := env.seedPartyProfile(t, uid, party2, "active")

	// The user default is profile A; the password's own default is profile B.
	env.do(t, http.MethodPut, "/v1/me", a.tk.access(), map[string]any{"default_profile_uuid": profA}).wantStatus(t, http.StatusOK)
	cred := env.credentialUUID(t, a.tk.access(), "password")
	env.do(t, http.MethodPatch, "/v1/me/credentials/"+cred, a.tk.access(),
		map[string]any{"default_profile_uuid": profB}).wantStatus(t, http.StatusOK)

	tk := env.login(t, a)
	if tk.Profile == nil || tk.Profile.UUID != profB {
		t.Fatalf("landed on %+v, want the credential default %s (not user default %s or personal %s)", tk.Profile, profB, profA, personal)
	}
}

func TestLogin_Landing_OnlyAvailableThenSelectionRequired(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	personalID := env.personalProfileID(t, uid)
	party1 := env.seedParty(t, "Landing Party "+uniq("p"))
	party2 := env.seedParty(t, "Landing Party "+uniq("p"))
	_, first := env.seedPartyProfile(t, uid, party1, "active")
	secondID, second := env.seedPartyProfile(t, uid, party2, "active")
	// The personal profile cannot be landed on, so the chain falls past it.
	env.exec(t, "suspend personal", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, personalID)

	// Two available profiles and no default: nothing to land on.
	r := env.loginRaw(t, a.Email, a.Password, nil).wantStatus(t, http.StatusOK)
	var sel tokens
	r.into(t, &sel)
	if sel.AccessToken != nil || !sel.SelectionRequired || len(sel.Profiles) != 3 {
		t.Fatalf("response = %s, want selection_required with no access token and three profiles", r.raw)
	}
	if sel.RefreshToken == nil {
		t.Fatalf("selection_required response carries no refresh token to choose with: %s", r.raw)
	}
	for _, p := range sel.Profiles {
		wantAvailable := p.UUID != env.profileUUID(t, personalID)
		if p.Available != wantAvailable {
			t.Errorf("profile %s available = %v, want %v", p.UUID, p.Available, wantAvailable)
		}
	}

	// The refresh grant with a chosen profile lands on it.
	chosen := env.do(t, http.MethodPost, "/v1/auth/token", "", refreshWith(*sel.RefreshToken, second)).
		wantStatus(t, http.StatusOK).tok(t)
	if chosen.Profile == nil || chosen.Profile.UUID != second {
		t.Fatalf("chose %s, landed on %+v", second, chosen.Profile)
	}
	if got := env.profileOf(t, chosen.access()); got != second {
		t.Fatalf("token acts as %s, want %s", got, second)
	}

	// With one of the two gone, the other is the only available profile.
	env.exec(t, "suspend second", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, secondID)
	tk := env.loginRaw(t, a.Email, a.Password, nil).wantStatus(t, http.StatusOK).tok(t)
	if tk.Profile == nil || tk.Profile.UUID != first {
		t.Fatalf("landed on %+v, want the only available profile %s", tk.Profile, first)
	}
}

func TestLogin_ManagedUserLandsOnItsPartyProfile(t *testing.T) {
	party := env.seedParty(t, "Managed Party "+uniq("p"))
	email := uniq("managed") + "@example.test"
	uid := env.seedManagedUser(t, party, email, testPassword)

	// A managed user with no profile has nothing to act as.
	env.loginRaw(t, email, testPassword, nil).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "not_held")

	profID, only := env.seedPartyProfile(t, uid, party, "active")
	tk := env.loginRaw(t, email, testPassword, nil).wantStatus(t, http.StatusOK).tok(t)
	if tk.Profile == nil || tk.Profile.UUID != only || tk.Profile.Kind != "party" {
		t.Fatalf("landed on %+v, want the party profile %s", tk.Profile, only)
	}
	if status, p := env.probe(t, "/v1/probe", tk.access()); status != http.StatusOK || p.ProfileID != profID {
		t.Fatalf("probe = %d %+v, want profile %d", status, p, profID)
	}

	// A suspended profile is listed, unavailable and carries its lock reason.
	env.exec(t, "suspend", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, profID)
	r := env.loginRaw(t, email, testPassword, nil).wantStatus(t, http.StatusOK)
	var sel tokens
	r.into(t, &sel)
	if sel.AccessToken != nil || !sel.SelectionRequired || len(sel.Profiles) != 1 || sel.Profiles[0].Available || sel.Profiles[0].Lock == nil {
		t.Fatalf("response = %s, want selection_required listing one locked profile", r.raw)
	}
}

func TestLogin_RequestedUnavailableProfileIsSelectionRequired(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	party := env.seedParty(t, "Landing Party "+uniq("p"))
	_, suspended := env.seedPartyProfile(t, uid, party, "suspended")

	// The requested profile is unavailable: no fallback to another profile.
	r := env.loginRaw(t, a.Email, a.Password, map[string]string{"profile_uuid": suspended}).wantStatus(t, http.StatusOK)
	var sel tokens
	r.into(t, &sel)
	if sel.AccessToken != nil || !sel.SelectionRequired {
		t.Fatalf("response = %s, want selection_required", r.raw)
	}
}

func TestRefresh_RotationAndReuseRevokesTheSession(t *testing.T) {
	a := env.register(t)
	first := env.login(t, a)

	second := env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(first)).wantStatus(t, http.StatusOK).tok(t)
	if second.refresh() == first.refresh() {
		t.Fatal("refresh token was not rotated")
	}
	if status, _ := env.probe(t, "/v1/probe", second.access()); status != http.StatusOK {
		t.Fatalf("rotated access token refused: %d", status)
	}

	// Presenting the spent token again is reuse: 401 and the session is revoked.
	env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(first)).wantError(t, http.StatusUnauthorized, "users.refresh_reuse", "")

	// Every token of the session now fails on its next request, the newest
	// refresh token included.
	for name, token := range map[string]string{"first": first.access(), "second": second.access()} {
		env.do(t, http.MethodGet, "/v1/probe", token, nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
		_ = name
	}
	env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(second)).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")

	var reason string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT revoke_reason FROM mod_users.sessions WHERE uuid = $1::uuid`, first.Session.UUID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "refresh_reuse" {
		t.Errorf("revoke_reason = %q, want refresh_reuse", reason)
	}
}

func TestRefresh_UnknownTokenIsSessionRevoked(t *testing.T) {
	env.do(t, http.MethodPost, "/v1/auth/token", "",
		refreshWith("mfr_"+strings.Repeat("0", 64), "11111111-1111-1111-1111-111111111111")).
		wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
}

func TestRefresh_ConcurrentDoubleRefreshOneWinsOneIsReuse(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)
	body := refreshReq(tk)

	const n = 2
	codes := make(chan int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start
			codes <- env.do(t, http.MethodPost, "/v1/auth/token", "", body).status
		}()
	}
	close(start)
	got := map[int]int{}
	for i := 0; i < n; i++ {
		got[<-codes]++
	}
	if got[http.StatusOK] != 1 || got[http.StatusUnauthorized] != 1 {
		t.Fatalf("statuses = %v, want exactly one 200 and one 401", got)
	}
}

func TestRefresh_AgainstUnavailableProfileLeavesTheTokenUsable(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	party := env.seedParty(t, "Landing Party "+uniq("p"))
	profID, profUUID := env.seedPartyProfile(t, uid, party, "suspended")
	tk := env.login(t, a)

	env.do(t, http.MethodPost, "/v1/auth/token", "", refreshWith(tk.refresh(), profUUID)).
		wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")

	// The refusal rolled back: the same token still works once the profile is available.
	env.exec(t, "activate", `UPDATE profiles SET status = 'active', started_at = now() WHERE entity_id = $1`, profID)
	got := env.do(t, http.MethodPost, "/v1/auth/token", "", refreshWith(tk.refresh(), profUUID)).wantStatus(t, http.StatusOK).tok(t)
	if got.Profile == nil || got.Profile.UUID != profUUID {
		t.Fatalf("refresh landed on %+v, want %s", got.Profile, profUUID)
	}
}

func TestFork_MintsAnIndependentLineage(t *testing.T) {
	a := env.register(t)
	tabA := env.login(t, a)
	tabB := env.do(t, http.MethodPost, "/v1/session/fork", tabA.access(), nil).wantStatus(t, http.StatusOK).tok(t)

	if tabB.Session == nil || tabB.Session.UUID != tabA.Session.UUID {
		t.Fatalf("fork changed the session: %+v vs %+v", tabB.Session, tabA.Session)
	}
	if tabB.RefreshToken == nil || tabB.refresh() == tabA.refresh() {
		t.Fatalf("fork did not mint its own refresh token: %v", tabB.RefreshToken)
	}

	// Each lineage rotates on its own: rotating A does not spend B's token.
	a2 := env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(tabA)).wantStatus(t, http.StatusOK).tok(t)
	b2 := env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(tabB)).wantStatus(t, http.StatusOK).tok(t)
	a3 := env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(a2)).wantStatus(t, http.StatusOK).tok(t)
	if a3.refresh() == b2.refresh() {
		t.Fatal("lineages share a token")
	}

	// Reuse in either lineage revokes the shared session.
	env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(tabA)).wantError(t, http.StatusUnauthorized, "users.refresh_reuse", "")
	env.do(t, http.MethodGet, "/v1/probe", b2.access(), nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
}

func TestSwitch_WritesNothingToTheSessionRow(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	party := env.seedParty(t, "Switch Party "+uniq("p"))
	_, partyProfile := env.seedPartyProfile(t, uid, party, "active")
	tk := env.login(t, a)

	snapshot := func() string {
		var s string
		if err := env.pool.QueryRow(context.Background(), `
SELECT to_jsonb(s)::text || '|' ||
       (SELECT coalesce(jsonb_agg(to_jsonb(f) ORDER BY f.credential_id)::text, '') FROM mod_users.session_factors f WHERE f.session_id = s.id) || '|' ||
       (SELECT count(*)::text FROM mod_users.session_refresh_tokens r WHERE r.session_id = s.id)
FROM mod_users.sessions s WHERE s.uuid = $1::uuid`, tk.Session.UUID).Scan(&s); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		return s
	}
	before := snapshot()
	env.obs.reset()

	sw := env.do(t, http.MethodPost, "/v1/session/switch", tk.access(), map[string]string{"profile_uuid": partyProfile}).
		wantStatus(t, http.StatusOK).tok(t)
	if sw.Profile == nil || sw.Profile.UUID != partyProfile {
		t.Fatalf("switch landed on %+v", sw.Profile)
	}
	if sw.RefreshToken != nil {
		t.Errorf("switch minted a refresh token")
	}
	if got := env.profileOf(t, sw.access()); got != partyProfile {
		t.Fatalf("switched token acts as %s", got)
	}
	if after := snapshot(); after != before {
		t.Fatalf("switch changed the session row:\nbefore %s\nafter  %s", before, after)
	}
	if obs := env.obs.pairs(); len(obs) != 0 {
		t.Errorf("switch wrote observations %v, want none", obs)
	}
	// An unavailable target is 401 profile_unavailable.
	env.exec(t, "suspend", `UPDATE profiles SET status = 'suspended' WHERE entity_id = (SELECT id FROM entities WHERE uuid = $1::uuid)`, partyProfile)
	env.do(t, http.MethodPost, "/v1/session/switch", tk.access(), map[string]string{"profile_uuid": partyProfile}).
		wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
}

func TestRevocation_LogoutTakesEffectOnTheNextRequest(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)
	if status, _ := env.probe(t, "/v1/probe", tk.access()); status != http.StatusOK {
		t.Fatalf("probe before logout = %d", status)
	}
	env.do(t, http.MethodPost, "/v1/session/logout", tk.access(), nil).wantStatus(t, http.StatusNoContent)
	env.do(t, http.MethodGet, "/v1/probe", tk.access(), nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
	env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(tk)).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
}

func TestRevocation_CredentialRevocationEndsSessionsHoldingIt(t *testing.T) {
	a := env.register(t)
	// Verifying the email creates the email_otp credential: a second
	// interactive credential, so the password is not the last one.
	env.verifyRegisteredEmail(t, a)
	tk := env.login(t, a)
	other := env.login(t, a)

	pw := env.credentialUUID(t, tk.access(), "password")
	env.do(t, http.MethodDelete, "/v1/me/credentials/"+pw, tk.access(), nil).wantStatus(t, http.StatusNoContent)

	// Both sessions held the password as a factor.
	for _, token := range []string{tk.access(), other.access()} {
		env.do(t, http.MethodGet, "/v1/probe", token, nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
	}
	var reason string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT revoke_reason FROM mod_users.sessions WHERE uuid = $1::uuid`, tk.Session.UUID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "credential_revoked" {
		t.Errorf("revoke_reason = %q, want credential_revoked", reason)
	}
}

func TestRevocation_PrincipalDisableEndsSessions(t *testing.T) {
	admin := env.register(t)
	opTok, _ := env.makeOperator(t, admin)

	victim := env.register(t)
	vTok := env.login(t, victim)
	vUUID := env.entityUUID(t, env.principalID(t, victim.Email))

	env.do(t, http.MethodPost, "/v1/principals/"+vUUID+"/disable", opTok.access(), map[string]string{"reason": "test"}).
		wantStatus(t, http.StatusOK)

	r := env.do(t, http.MethodGet, "/v1/probe", vTok.access(), nil)
	if r.status != http.StatusUnauthorized {
		t.Fatalf("disabled principal's token: %d %s", r.status, r.raw)
	}
	// And the principal cannot sign in again.
	env.loginRaw(t, victim.Email, victim.Password, nil).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "principal_disabled")

	env.do(t, http.MethodPost, "/v1/principals/"+vUUID+"/enable", opTok.access(), nil).wantStatus(t, http.StatusOK)
	env.loginRaw(t, victim.Email, victim.Password, nil).wantStatus(t, http.StatusOK)
}

func TestRevocation_ProfileEndedLocksTheProfileOnTheNextRequest(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	party := env.seedParty(t, "Ending Party "+uniq("p"))
	profID, profUUID := env.seedPartyProfile(t, uid, party, "active")
	base := env.login(t, a)
	acting := env.do(t, http.MethodPost, "/v1/session/switch", base.access(), map[string]string{"profile_uuid": profUUID}).
		wantStatus(t, http.StatusOK).tok(t)
	if status, p := env.probe(t, "/v1/probe", acting.access()); status != http.StatusOK || p.ProfileID != profID {
		t.Fatalf("probe as party profile = %d %+v", status, p)
	}

	env.exec(t, "end profile", `UPDATE profiles SET status = 'ended', ended_at = now(), end_reason = 'test' WHERE entity_id = $1`, profID)

	env.do(t, http.MethodGet, "/v1/probe", acting.access(), nil).wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "")
	// The session itself is alive: the personal profile's token still works.
	if status, _ := env.probe(t, "/v1/probe", base.access()); status != http.StatusOK {
		t.Fatalf("personal token after the party profile ended = %d", status)
	}
}

func TestStepUp_PasswordRefreshesAuthTime(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)
	env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '2 hours' WHERE uuid = $1::uuid`, tk.Session.UUID)
	_, before := env.probe(t, "/v1/probe", tk.access())
	if time.Since(before.AuthTime) < time.Hour {
		t.Fatalf("auth_time not aged: %v", before.AuthTime)
	}

	// A wrong password is the 409 step-up action, never a 401.
	wrong := env.do(t, http.MethodPost, "/v1/session/step-up/password", tk.access(), map[string]string{"password": "not-the-password"})
	wrong.wantAction(t, http.StatusConflict, "users.step_up_required")

	up := env.do(t, http.MethodPost, "/v1/session/step-up/password", tk.access(), map[string]string{"password": a.Password}).
		wantStatus(t, http.StatusOK).tok(t)
	if up.RefreshToken != nil {
		t.Error("step-up minted a refresh token")
	}
	_, after := env.probe(t, "/v1/probe", up.access())
	if time.Since(after.AuthTime) > time.Minute {
		t.Errorf("auth_time after step-up = %v, want about now", after.AuthTime)
	}
	// The old token reads the same row.
	_, oldView := env.probe(t, "/v1/probe", tk.access())
	if !oldView.AuthTime.Equal(after.AuthTime) {
		t.Errorf("old token auth_time %v differs from the row's %v", oldView.AuthTime, after.AuthTime)
	}
}

func TestStepUp_SecondFactorClassRaisesAcr(t *testing.T) {
	a := env.register(t)
	// Verify the email (creates the email_otp credential).
	env.verifyRegisteredEmail(t, a)
	tk := env.login(t, a)
	if tk.Session.ACR != 1 {
		t.Fatalf("password sign-in acr = %d, want 1", tk.Session.ACR)
	}
	env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '2 hours' WHERE uuid = $1::uuid`, tk.Session.UUID)

	seen := env.mail.count(a.Email)
	env.do(t, http.MethodPost, "/v1/session/step-up/email-code/request", tk.access(), map[string]string{"email": a.Email}).wantStatus(t, http.StatusNoContent)
	code := env.mail.codeAfter(t, a.Email, seen)

	up := env.do(t, http.MethodPost, "/v1/session/step-up/email-code/verify", tk.access(),
		map[string]string{"email": a.Email, "code": code}).wantStatus(t, http.StatusOK).tok(t)
	if up.Session == nil || up.Session.ACR != 2 {
		t.Fatalf("acr after password+email_otp = %+v, want 2", up.Session)
	}
	_, p := env.probe(t, "/v1/probe", up.access())
	if p.ACR != 2 || time.Since(p.AuthTime) > time.Minute {
		t.Fatalf("AuthContext after step-up = acr %d auth_time %v, want 2 and now", p.ACR, p.AuthTime)
	}
}

func TestRefresh_ReuseStillRevokesWhenThePrincipalHoldsNoLiveProfile(t *testing.T) {
	party := env.seedParty(t, "Reuse Party "+uniq("p"))
	email := uniq("reuse") + "@example.test"
	uid := env.seedManagedUser(t, party, email, testPassword)
	profID, _ := env.seedPartyProfile(t, uid, party, "active")

	tk := env.loginRaw(t, email, testPassword, nil).wantStatus(t, http.StatusOK).tok(t)
	rotated := env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(tk)).wantStatus(t, http.StatusOK).tok(t)

	// The profile ends, so the principal has no non-ended profile left to
	// observe the revocation as: the revocation is logged rather than observed,
	// and it still commits.
	env.exec(t, "end profile", `UPDATE profiles SET status = 'ended', ended_at = now(), end_reason = 'test' WHERE entity_id = $1`, profID)
	env.obs.reset()
	env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(tk)).wantError(t, http.StatusUnauthorized, "users.refresh_reuse", "")

	var reason string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT revoke_reason FROM mod_users.sessions WHERE uuid = $1::uuid`, tk.Session.UUID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "refresh_reuse" {
		t.Errorf("revoke_reason = %q, want refresh_reuse", reason)
	}
	if got := env.obs.pairs(); len(got) != 0 {
		t.Errorf("observations = %v, want none (no acting profile exists)", got)
	}
	env.do(t, http.MethodGet, "/v1/probe", rotated.access(), nil).wantStatus(t, http.StatusUnauthorized)
}

func TestPassword_ResetKeepsTheCredentialsLandingProfile(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	party := env.seedParty(t, "Reset Party "+uniq("p"))
	_, profile := env.seedPartyProfile(t, uid, party, "active")

	cred := env.credentialUUID(t, a.tk.access(), "password")
	env.do(t, http.MethodPatch, "/v1/me/credentials/"+cred, a.tk.access(),
		map[string]any{"default_profile_uuid": profile, "label": "work laptop"}).wantStatus(t, http.StatusOK)

	seen := env.mail.count(a.Email)
	env.do(t, http.MethodPost, "/v1/auth/password-reset/request", "", map[string]string{"email": a.Email}).wantStatus(t, http.StatusNoContent)
	token := env.resetTokenAfter(t, a.Email, seen)
	env.do(t, http.MethodPost, "/v1/auth/password-reset/confirm", "", map[string]string{"token": token, "password": "reset-keeps-landing-1"}).
		wantStatus(t, http.StatusNoContent)

	// The new password is a new credential that keeps the label and the default profile.
	tk := env.loginRaw(t, a.Email, "reset-keeps-landing-1", nil).wantStatus(t, http.StatusOK).tok(t)
	if tk.Profile == nil || tk.Profile.UUID != profile {
		t.Fatalf("landed on %+v, want the kept default profile %s", tk.Profile, profile)
	}
	var list struct {
		Credentials []struct {
			UUID               string  `json:"uuid"`
			Kind               string  `json:"kind"`
			Label              string  `json:"label"`
			DefaultProfileUUID *string `json:"default_profile_uuid"`
		} `json:"credentials"`
	}
	env.do(t, http.MethodGet, "/v1/me/credentials", tk.access(), nil).wantStatus(t, http.StatusOK).into(t, &list)
	var found bool
	for _, c := range list.Credentials {
		if c.Kind != "password" {
			continue
		}
		found = true
		if c.UUID == cred {
			t.Errorf("the old password credential is still listed")
		}
		if c.Label != "work laptop" || c.DefaultProfileUUID == nil || *c.DefaultProfileUUID != profile {
			t.Errorf("new password credential = %+v, want label and default profile kept", c)
		}
	}
	if !found {
		t.Error("no password credential after the reset")
	}
}
