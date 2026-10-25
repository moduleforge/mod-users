//go:build integration

package integration

// The OIDC flows over the composed stack. The identity provider is replaced by
// OAuth.ExchangeFn, which stands in for the code exchange and id_token check;
// everything after that (the resolver, just-in-time creation, the link and
// step-up callbacks, the handoff exchange) is the production code against a
// real database.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
)

const idpIssuer = "https://idp.example.test"

// addProvider registers an initialized provider with the stack's OAuth
// orchestrator and returns its id.
func (s *stack) addProvider(t *testing.T, p config.Provider) string {
	t.Helper()
	if p.ID == "" {
		p.ID = uniq("idp")
	}
	if p.DisplayName == "" {
		p.DisplayName = "IdP " + p.ID
	}
	s.oauth.States[p.ID] = &auth.ProviderState{
		ID: p.ID, Provider: p, InitOK: true,
		OAuthCfg: &oauth2.Config{
			ClientID:     "client-" + p.ID,
			ClientSecret: "secret",
			Endpoint:     oauth2.Endpoint{AuthURL: "https://idp.example.test/authorize", TokenURL: "https://idp.example.test/token"},
			RedirectURL:  "http://api.test/v1/auth/oidc/" + p.ID + "/callback",
			Scopes:       []string{"openid", "email"},
		},
	}
	// session_acr reads the provider's trust_acr from its row, so a trusting
	// provider needs one.
	if p.TrustACR {
		s.exec(t, "provider row", `INSERT INTO mod_users.oidc_providers (id, trust_acr) VALUES ($1, true)`, p.ID)
	}
	t.Cleanup(func() { delete(s.oauth.States, p.ID) })
	return p.ID
}

// idpReturns makes the next callbacks verify as identity under payload.
func (s *stack) idpReturns(t *testing.T, id auth.ExternalIdentity, payload auth.StatePayload) {
	t.Helper()
	prev := s.oauth.ExchangeFn
	s.oauth.ExchangeFn = func(_ context.Context, providerID, _, _, _ string) (auth.ExternalIdentity, auth.StatePayload, error) {
		p := payload
		p.Provider = providerID
		p.Expires = time.Now().Add(5 * time.Minute).Unix()
		return id, p, nil
	}
	t.Cleanup(func() { s.oauth.ExchangeFn = prev })
}

// callback performs the provider callback and returns the redirect fragment.
func (s *stack) callback(t *testing.T, providerID string) (status int, frag url.Values, loc string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/"+providerID+"/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "oidc_state", Value: "s"})
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	loc = rec.Header().Get("Location")
	if loc != "" {
		u, err := url.Parse(loc)
		if err != nil {
			t.Fatalf("parse Location %q: %v", loc, err)
		}
		frag, err = url.ParseQuery(u.Fragment)
		if err != nil {
			t.Fatalf("parse fragment %q: %v", u.Fragment, err)
		}
	}
	return rec.Code, frag, loc
}

// wantRedirectError performs the callback and requires `#error=code`.
func (s *stack) wantRedirectError(t *testing.T, providerID, code string) {
	t.Helper()
	status, frag, loc := s.callback(t, providerID)
	if status != http.StatusFound || frag.Get("error") != code {
		t.Fatalf("callback = %d %q, want a redirect with error=%s", status, loc, code)
	}
	if frag.Get("handoff") != "" {
		t.Fatalf("a refused callback still carried a handoff code: %q", loc)
	}
}

// signIn performs the callback and exchanges the handoff code.
func (s *stack) signIn(t *testing.T, providerID string) tokens {
	t.Helper()
	status, frag, loc := s.callback(t, providerID)
	code := frag.Get("handoff")
	if status != http.StatusFound || code == "" {
		t.Fatalf("callback = %d %q, want a redirect with a handoff code", status, loc)
	}
	return s.do(t, http.MethodPost, "/v1/auth/handoff", "", map[string]string{"code": code}).wantStatus(t, http.StatusOK).tok(t)
}

func idpIdentity(email string, verified bool) auth.ExternalIdentity {
	return auth.ExternalIdentity{
		Issuer: idpIssuer, Subject: uniq("sub-"), Email: email, EmailVerified: verified,
		GivenName: "Olivia", FamilyName: "Oidc",
	}
}

func (s *stack) count(t *testing.T, what, sql string, args ...any) int64 {
	t.Helper()
	return s.scanInt64(t, what, sql, args...)
}

func TestOIDC_NeverLinksByEmail(t *testing.T) {
	a := env.register(t)
	env.verifyRegisteredEmail(t, a)
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	// A verified IdP email equal to an existing account's email.
	id := idpIdentity(strings.ToUpper(a.Email), true)
	env.idpReturns(t, id, auth.StatePayload{})

	users := env.count(t, "users", `SELECT count(*) FROM mod_users.users`)
	oidcCreds := env.count(t, "oidc credentials", `SELECT count(*) FROM mod_users.cred_oidc`)

	env.wantRedirectError(t, provider, "account_exists_sign_in_and_link")

	if got := env.count(t, "users", `SELECT count(*) FROM mod_users.users`); got != users {
		t.Errorf("a user was created: %d -> %d", users, got)
	}
	if got := env.count(t, "oidc credentials", `SELECT count(*) FROM mod_users.cred_oidc`); got != oidcCreds {
		t.Errorf("an oidc credential was created: %d -> %d", oidcCreds, got)
	}
	if got := env.count(t, "account oidc", `SELECT count(*) FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'oidc'`,
		env.principalID(t, a.Email)); got != 0 {
		t.Errorf("the existing account gained an oidc credential")
	}
}

func TestOIDC_JIT_BYOCreatesAUserAndSignsInAgainOnTheKnownPath(t *testing.T) {
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	email := uniq("jit") + "@example.test"
	id := idpIdentity(email, true)
	env.idpReturns(t, id, auth.StatePayload{ReturnPath: "/dashboard"})
	env.obs.reset()

	status, frag, loc := env.callback(t, provider)
	if status != http.StatusFound || frag.Get("handoff") == "" || frag.Get("return") != "/dashboard" {
		t.Fatalf("callback = %d %q", status, loc)
	}
	// Nothing sensitive in the redirect: no token of any kind.
	for _, bad := range []string{"access_token", "refresh_token", "token=", "eyJ"} {
		if strings.Contains(loc, bad) {
			t.Errorf("redirect %q carries %q", loc, bad)
		}
	}
	tk := env.do(t, http.MethodPost, "/v1/auth/handoff", "", map[string]string{"code": frag.Get("handoff")}).wantStatus(t, http.StatusOK).tok(t)
	if tk.Profile == nil || tk.Profile.Kind != "personal" || tk.RefreshToken == nil {
		t.Fatalf("handoff token response = %+v", tk)
	}

	uid := env.principalID(t, email)
	// The email identifier is verified because the IdP asserted it, and the
	// oidc credential holds the identity.
	if n := env.count(t, "verified identifier", `SELECT count(*) FROM mod_users.user_identifiers WHERE user_id = $1 AND verified_at IS NOT NULL`, uid); n != 1 {
		t.Errorf("verified identifiers = %d, want 1", n)
	}
	if n := env.count(t, "oidc credential", `SELECT count(*) FROM mod_users.credentials c JOIN mod_users.cred_oidc o ON o.credential_id = c.id
		WHERE c.principal_id = $1 AND c.revoked_at IS NULL AND o.issuer = $2 AND o.subject = $3 AND c.confined_to_party_id IS NULL`,
		uid, id.Issuer, id.Subject); n != 1 {
		t.Errorf("active oidc credentials for the identity = %d, want 1", n)
	}
	if n := env.count(t, "is_guest", `SELECT count(*) FROM mod_users.users WHERE entity_id = $1 AND NOT is_guest AND managing_party_id IS NULL`, uid); n != 1 {
		t.Errorf("created user is not a plain bring-your-own user")
	}
	// The observation vocabulary of a JIT sign-up.
	for _, p := range env.obs.pairs() {
		switch p {
		case "create user", "create user_identifier", "create credential", "login session", "create natural_person":
		default:
			t.Errorf("unexpected observation %q in a JIT sign-up", p)
		}
	}

	// The second sign-in with the same identity logs the same principal in; it
	// creates nothing, whatever email the IdP asserts now.
	users := env.count(t, "users", `SELECT count(*) FROM mod_users.users`)
	id2 := id
	id2.Email = "changed-" + email
	env.idpReturns(t, id2, auth.StatePayload{})
	again := env.signIn(t, provider)
	if status, p := env.probe(t, "/v1/probe", again.access()); status != http.StatusOK || p.PrincipalID != uid {
		t.Fatalf("second sign-in = %d %+v, want principal %d", status, p, uid)
	}
	if got := env.count(t, "users", `SELECT count(*) FROM mod_users.users`); got != users {
		t.Errorf("the known-identity sign-in created a user")
	}
}

func TestOIDC_JIT_BYOUnverifiedEmailStaysUnverified(t *testing.T) {
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	email := uniq("unverified") + "@example.test"
	env.idpReturns(t, idpIdentity(email, false), auth.StatePayload{})
	env.signIn(t, provider)
	if n := env.count(t, "verified", `SELECT count(*) FROM mod_users.user_identifiers WHERE value_canon = $1 AND verified_at IS NULL`, email); n != 1 {
		t.Errorf("identifier of an unasserted email must be unverified")
	}
}

func TestOIDC_JIT_RefusalsCreateNothing(t *testing.T) {
	users := env.count(t, "users", `SELECT count(*) FROM mod_users.users`)

	none := env.addProvider(t, config.Provider{JITMode: config.JITModeNone})
	env.idpReturns(t, idpIdentity(uniq("none")+"@example.test", true), auth.StatePayload{})
	env.wantRedirectError(t, none, "no_account")

	byo := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	env.idpReturns(t, idpIdentity("", false), auth.StatePayload{})
	env.wantRedirectError(t, byo, "missing_email")

	// A managed provider with no party is a configuration the resolver refuses.
	orphan := env.addProvider(t, config.Provider{JITMode: config.JITModeManaged})
	env.idpReturns(t, idpIdentity(uniq("orphan")+"@example.test", true), auth.StatePayload{})
	env.wantRedirectError(t, orphan, "no_account")

	// An identity without issuer or subject is never usable.
	env.idpReturns(t, auth.ExternalIdentity{Email: "x@example.test"}, auth.StatePayload{})
	env.wantRedirectError(t, byo, "authentication_failed")

	if got := env.count(t, "users", `SELECT count(*) FROM mod_users.users`); got != users {
		t.Errorf("a refused sign-in created a user: %d -> %d", users, got)
	}
}

func TestOIDC_JIT_BYOIsRefusedWhenThePlatformDisallowsIt(t *testing.T) {
	var prev bool
	if err := env.pool.QueryRow(context.Background(), `SELECT allow_byo_identity FROM platform_settings`).Scan(&prev); err != nil {
		t.Fatal(err)
	}
	env.exec(t, "disable byo", `UPDATE platform_settings SET allow_byo_identity = false`)
	defer env.exec(t, "restore byo", `UPDATE platform_settings SET allow_byo_identity = $1`, prev)

	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	env.idpReturns(t, idpIdentity(uniq("blocked")+"@example.test", true), auth.StatePayload{})
	env.wantRedirectError(t, provider, "no_account")
}

func TestOIDC_JIT_ManagedCreatesAConfinedCredentialAndAnIdPJITProfile(t *testing.T) {
	party := env.seedParty(t, "Managed Org "+uniq("p"))
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeManaged, PartyID: party})
	email := uniq("managed-jit") + "@example.test"
	id := idpIdentity(email, true)
	env.idpReturns(t, id, auth.StatePayload{})
	env.obs.reset()

	tk := env.signIn(t, provider)
	if tk.Profile == nil || tk.Profile.Kind != "party" || tk.Profile.Party == nil {
		t.Fatalf("managed sign-in landed on %+v, want the party profile", tk.Profile)
	}

	_, p := env.probe(t, "/v1/probe", tk.access())
	uid := p.PrincipalID
	if n := env.count(t, "managed user", `SELECT count(*) FROM mod_users.users WHERE entity_id = $1 AND managing_party_id = $2 AND NOT is_guest`, uid, party); n != 1 {
		t.Errorf("created user is not a managed user of the party")
	}
	if n := env.count(t, "idp_jit profile", `SELECT count(*) FROM profiles WHERE holder_id = $1 AND party_id = $2 AND source = 'idp_jit' AND status = 'active'`, uid, party); n != 1 {
		t.Errorf("active idp_jit profiles = %d, want 1", n)
	}
	if n := env.count(t, "no personal profile", `SELECT count(*) FROM profiles WHERE holder_id = $1 AND party_id IS NULL`, uid); n != 0 {
		t.Errorf("a managed user has a personal profile")
	}
	if n := env.count(t, "confined credential", `SELECT count(*) FROM mod_users.credentials c JOIN mod_users.cred_oidc o ON o.credential_id = c.id
		WHERE c.principal_id = $1 AND c.confined_to_party_id = $2 AND o.issuer = $3 AND o.subject = $4`, uid, party, id.Issuer, id.Subject); n != 1 {
		t.Errorf("confined oidc credentials = %d, want 1", n)
	}
	if n := env.count(t, "no email identifier", `SELECT count(*) FROM mod_users.user_identifiers WHERE user_id = $1`, uid); n != 0 {
		t.Errorf("a managed JIT user got an email identifier")
	}
	for _, o := range env.obs.all() {
		if o.Op == "create" && o.Resource == "user" && (!o.HasAuth || o.Auth.ProfileID == 0 || o.Auth.PrincipalID != uid) {
			t.Errorf("user creation observed with %+v, want the new user's profile", o.Auth)
		}
	}
	// The same identity signs in again on the known path with no new user.
	users := env.count(t, "users", `SELECT count(*) FROM mod_users.users`)
	again := env.signIn(t, provider)
	if _, p2 := env.probe(t, "/v1/probe", again.access()); p2.PrincipalID != uid {
		t.Errorf("second managed sign-in principal %d, want %d", p2.PrincipalID, uid)
	}
	if got := env.count(t, "users", `SELECT count(*) FROM mod_users.users`); got != users {
		t.Errorf("the second sign-in created a user")
	}
}

func TestOIDC_Handoff_IsSingleUseAndExpires(t *testing.T) {
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	env.idpReturns(t, idpIdentity(uniq("handoff")+"@example.test", true), auth.StatePayload{})

	_, frag, _ := env.callback(t, provider)
	code := frag.Get("handoff")
	if code == "" {
		t.Fatal("no handoff code")
	}
	env.do(t, http.MethodPost, "/v1/auth/handoff", "", map[string]string{"code": code}).wantStatus(t, http.StatusOK)
	// Spent.
	env.do(t, http.MethodPost, "/v1/auth/handoff", "", map[string]string{"code": code}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")

	// A fresh code that expired is refused like an unknown one.
	env.idpReturns(t, idpIdentity(uniq("handoff")+"@example.test", true), auth.StatePayload{})
	_, frag2, _ := env.callback(t, provider)
	expired := frag2.Get("handoff")
	var ttl time.Duration
	if err := env.pool.QueryRow(context.Background(), `
SELECT expires_at - created_at FROM mod_users.auth_challenges WHERE kind = 'login_handoff' ORDER BY id DESC LIMIT 1`).Scan(&ttl); err != nil {
		t.Fatal(err)
	}
	if ttl < 59*time.Second || ttl > 61*time.Second {
		t.Errorf("handoff lifetime = %v, want about 60s", ttl)
	}
	env.exec(t, "expire handoff", `UPDATE mod_users.auth_challenges SET expires_at = now() - interval '1 second'
		WHERE id = (SELECT max(id) FROM mod_users.auth_challenges WHERE kind = 'login_handoff')`)
	env.do(t, http.MethodPost, "/v1/auth/handoff", "", map[string]string{"code": expired}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")

	// Unknown and empty codes.
	env.do(t, http.MethodPost, "/v1/auth/handoff", "", map[string]string{"code": "nope"}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
	env.do(t, http.MethodPost, "/v1/auth/handoff", "", map[string]string{}).wantStatus(t, http.StatusBadRequest)

	// A code whose session was revoked before redemption is refused as a dead session.
	env.idpReturns(t, idpIdentity(uniq("handoff")+"@example.test", true), auth.StatePayload{})
	_, frag3, _ := env.callback(t, provider)
	env.exec(t, "revoke session", `UPDATE mod_users.sessions SET revoked_at = now(), revoke_reason = 'logout'
		WHERE id = (SELECT session_id FROM mod_users.auth_challenges WHERE kind = 'login_handoff' ORDER BY id DESC LIMIT 1)`)
	env.do(t, http.MethodPost, "/v1/auth/handoff", "", map[string]string{"code": frag3.Get("handoff")}).
		wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
}

func TestOIDC_ProviderAndStateErrors(t *testing.T) {
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})

	// The provider's own error parameter is mapped, never echoed.
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/"+provider+"/callback?error=access_denied&error_description=secret+text", nil)
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusFound || !strings.Contains(loc, "#error=authentication_failed") || strings.Contains(loc, "secret") {
		t.Errorf("provider error callback = %d %q", rec.Code, loc)
	}

	// An unknown provider is a 404 on both legs.
	env.do(t, http.MethodGet, "/v1/auth/oidc/nope/start", "", nil).wantStatus(t, http.StatusNotFound)
	env.do(t, http.MethodGet, "/v1/auth/oidc/nope/callback?code=c&state=s", "", nil).wantStatus(t, http.StatusNotFound)

	// The unauthenticated start redirects to the provider with a signed state
	// cookie; a return path outside the site is refused.
	start := env.doRaw(t, http.MethodGet, "/v1/auth/oidc/"+provider+"/start?return=/home", nil)
	if start.status != http.StatusFound || !strings.HasPrefix(start.header.Get("Location"), "https://idp.example.test/authorize") {
		t.Fatalf("start = %d %q", start.status, start.header.Get("Location"))
	}
	if c := start.header.Get("Set-Cookie"); !strings.Contains(c, "oidc_state=") || !strings.Contains(c, "HttpOnly") {
		t.Errorf("start cookie = %q", c)
	}
	env.do(t, http.MethodGet, "/v1/auth/oidc/"+provider+"/start?return=https://evil.example/", "", nil).wantStatus(t, http.StatusBadRequest)
}

func TestOIDC_LinkAndStepUpStartsRequireAnAuthenticatedSession(t *testing.T) {
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	env.do(t, http.MethodPost, "/v1/me/credentials/oidc/"+provider+"/start", "", nil).wantStatus(t, http.StatusUnauthorized)
	env.do(t, http.MethodPost, "/v1/session/step-up/oidc/"+provider+"/start", "", nil).wantStatus(t, http.StatusUnauthorized)
}

// stateFor builds the state of a link or step-up flow for a session.
func (s *stack) stateFor(t *testing.T, flow auth.Flow, sessionUUID string, principalID int64) auth.StatePayload {
	t.Helper()
	return auth.StatePayload{Flow: flow, SessionUUID: sessionUUID, PrincipalUUID: s.entityUUID(t, principalID)}
}

func TestOIDC_LinkCallbackRefusedWithoutALiveSessionOfTheStatedPrincipal(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	tk := env.login(t, a)
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	id := idpIdentity(uniq("forged")+"@example.test", true)
	creds := env.count(t, "oidc credentials", `SELECT count(*) FROM mod_users.cred_oidc`)

	cases := map[string]auth.StatePayload{
		"unknown session":      env.stateFor(t, auth.FlowLink, "11111111-1111-1111-1111-111111111111", uid),
		"malformed session":    {Flow: auth.FlowLink, SessionUUID: "nope", PrincipalUUID: env.entityUUID(t, uid)},
		"principal mismatch":   env.stateFor(t, auth.FlowLink, tk.Session.UUID, env.principalID(t, env.register(t).Email)),
		"missing session uuid": {Flow: auth.FlowLink, PrincipalUUID: env.entityUUID(t, uid)},
	}
	for name, st := range cases {
		t.Run(name, func(t *testing.T) {
			env.idpReturns(t, id, st)
			// A state that names no live session of the stated principal never links.
			env.wantRedirectError(t, provider, "authentication_failed")
		})
	}

	// A revoked session cannot link either.
	env.do(t, http.MethodPost, "/v1/session/logout", tk.access(), nil).wantStatus(t, http.StatusNoContent)
	env.idpReturns(t, id, env.stateFor(t, auth.FlowLink, tk.Session.UUID, uid))
	env.wantRedirectError(t, provider, "authentication_failed")

	if got := env.count(t, "oidc credentials", `SELECT count(*) FROM mod_users.cred_oidc`); got != creds {
		t.Errorf("a refused link created a credential: %d -> %d", creds, got)
	}
}

func TestOIDC_Link_BindsTheIdentityToTheSessionPrincipalOnly(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	tk := env.login(t, a)
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeNone})

	// The authenticated start answers {authorize_url} with a signed state cookie.
	start := env.do(t, http.MethodPost, "/v1/me/credentials/oidc/"+provider+"/start?return=/settings", tk.access(), nil).wantStatus(t, http.StatusOK)
	authURL := start.obj(t)["authorize_url"].(string)
	if !strings.HasPrefix(authURL, "https://idp.example.test/authorize") || !strings.Contains(authURL, "client_id=client-"+provider) {
		t.Errorf("authorize_url = %q", authURL)
	}
	if c := start.header.Get("Set-Cookie"); !strings.Contains(c, "oidc_state=") {
		t.Errorf("no state cookie: %q", c)
	}

	// The IdP's email is some other person's address: it must play no part.
	other := env.register(t)
	id := idpIdentity(other.Email, true)
	env.idpReturns(t, id, env.stateFor(t, auth.FlowLink, tk.Session.UUID, uid))
	env.obs.reset()
	status, frag, loc := env.callback(t, provider)
	if status != http.StatusFound || frag.Get("linked") != "1" || frag.Get("handoff") != "" {
		t.Fatalf("link callback = %d %q", status, loc)
	}
	if n := env.count(t, "linked credential", `SELECT count(*) FROM mod_users.credentials c JOIN mod_users.cred_oidc o ON o.credential_id = c.id
		WHERE c.principal_id = $1 AND o.issuer = $2 AND o.subject = $3 AND c.revoked_at IS NULL`, uid, id.Issuer, id.Subject); n != 1 {
		t.Fatalf("linked credentials on the session principal = %d, want 1", n)
	}
	if n := env.count(t, "other untouched", `SELECT count(*) FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'oidc'`,
		env.principalID(t, other.Email)); n != 0 {
		t.Errorf("the email owner gained a credential")
	}
	if got := env.obs.pairs(); len(got) != 1 || got[0] != "create credential" {
		t.Errorf("link observations = %v, want [create credential]", got)
	}

	// Linking again is idempotent; another principal linking it is refused.
	env.idpReturns(t, id, env.stateFor(t, auth.FlowLink, tk.Session.UUID, uid))
	if _, f, l := env.callback(t, provider); f.Get("linked") != "1" {
		t.Errorf("re-link = %q, want linked=1", l)
	}
	if n := env.count(t, "still one", `SELECT count(*) FROM mod_users.cred_oidc WHERE issuer = $1 AND subject = $2`, id.Issuer, id.Subject); n != 1 {
		t.Errorf("credentials for the identity = %d after re-link, want 1", n)
	}
	otherTk := env.login(t, other)
	env.idpReturns(t, id, env.stateFor(t, auth.FlowLink, otherTk.Session.UUID, env.principalID(t, other.Email)))
	env.wantRedirectError(t, provider, "identity_in_use")

	// The linked identity now signs in as the linking principal (jit none).
	env.idpReturns(t, id, auth.StatePayload{})
	signed := env.signIn(t, provider)
	if _, p := env.probe(t, "/v1/probe", signed.access()); p.PrincipalID != uid {
		t.Errorf("sign-in with the linked identity = principal %d, want %d", p.PrincipalID, uid)
	}
}

func TestOIDC_Link_ConfinesTheCredentialToTheProviderParty(t *testing.T) {
	party := env.seedParty(t, "Link Confine Org "+uniq("p"))
	cases := []struct {
		name     string
		provider config.Provider
		confined bool
	}{
		{"party-bound provider", config.Provider{JITMode: config.JITModeNone, PartyID: party}, true},
		{"unbound provider", config.Provider{JITMode: config.JITModeNone}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := env.register(t)
			uid := env.principalID(t, a.Email)
			tk := env.login(t, a)
			provider := env.addProvider(t, tc.provider)
			id := idpIdentity(uniq("linkc")+"@example.test", true)
			env.idpReturns(t, id, env.stateFor(t, auth.FlowLink, tk.Session.UUID, uid))
			if status, frag, loc := env.callback(t, provider); status != http.StatusFound || frag.Get("linked") != "1" {
				t.Fatalf("link callback = %d %q", status, loc)
			}
			const q = `SELECT count(*) FROM mod_users.credentials c JOIN mod_users.cred_oidc o ON o.credential_id = c.id
				WHERE c.principal_id = $1 AND o.issuer = $2 AND o.subject = $3 AND c.revoked_at IS NULL AND `
			if tc.confined {
				if n := env.count(t, "confined", q+`c.confined_to_party_id = $4`, uid, id.Issuer, id.Subject, party); n != 1 {
					t.Errorf("credentials confined to the provider party = %d, want 1", n)
				}
			} else if n := env.count(t, "unconfined", q+`c.confined_to_party_id IS NULL`, uid, id.Issuer, id.Subject); n != 1 {
				t.Errorf("unconfined credentials = %d, want 1", n)
			}
		})
	}
}

func TestOIDC_Link_NeverLinksToGuestsManagedUsersOrStaleSessions(t *testing.T) {
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeNone})

	// A guest: 409 users.guest_upgrade_required at the start; a forged callback refuses.
	g := env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": uniq("dev")}).wantStatus(t, http.StatusCreated)
	var guest tokens
	g.into(t, &guest)
	env.do(t, http.MethodPost, "/v1/me/credentials/oidc/"+provider+"/start", guest.access(), nil).
		wantError(t, http.StatusConflict, "conflict", "users.guest_upgrade_required")
	guestID := idpIdentity(uniq("g")+"@example.test", true)
	_, gp := env.probe(t, "/v1/probe", guest.access())
	env.idpReturns(t, guestID, env.stateFor(t, auth.FlowLink, guest.Session.UUID, gp.PrincipalID))
	env.wantRedirectError(t, provider, "authentication_failed")

	// A managed user: 403 at the start; a forged callback refuses.
	party := env.seedParty(t, "Link Party "+uniq("p"))
	email := uniq("mgd") + "@example.test"
	muid := env.seedManagedUser(t, party, email, testPassword)
	env.seedPartyProfile(t, muid, party, "active")
	mtk := env.loginRaw(t, email, testPassword, nil).wantStatus(t, http.StatusOK).tok(t)
	env.do(t, http.MethodPost, "/v1/me/credentials/oidc/"+provider+"/start", mtk.access(), nil).wantStatus(t, http.StatusForbidden)
	mgdID := idpIdentity(uniq("m")+"@example.test", true)
	env.idpReturns(t, mgdID, env.stateFor(t, auth.FlowLink, mtk.Session.UUID, muid))
	env.wantRedirectError(t, provider, "authentication_failed")

	for _, id := range []auth.ExternalIdentity{guestID, mgdID} {
		if n := env.count(t, "no link", `SELECT count(*) FROM mod_users.cred_oidc WHERE subject = $1`, id.Subject); n != 0 {
			t.Errorf("identity %s was linked to a guest or managed principal", id.Subject)
		}
	}

	// A session older than AUTH_SELF_SERVICE_MAX_AUTH_AGE must step up first.
	a := env.register(t)
	tk := env.login(t, a)
	env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '1 hour' WHERE uuid = $1::uuid`, tk.Session.UUID)
	env.do(t, http.MethodPost, "/v1/me/credentials/oidc/"+provider+"/start", tk.access(), nil).wantAction(t, http.StatusConflict, "users.step_up_required")
}

func TestOIDC_StepUp_RaisesAcrAndAuthTimeAndNeedsAnOIDCCredential(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeNone, TrustACR: false})

	// No oidc credential for the provider: the start is the step-up action.
	tk := env.login(t, a)
	env.do(t, http.MethodPost, "/v1/session/step-up/oidc/"+provider+"/start", tk.access(), nil).wantAction(t, http.StatusConflict, "users.step_up_required")

	// Link, then step up through the provider.
	id := idpIdentity(uniq("su")+"@example.test", true)
	id.AssertedACR = 3
	env.idpReturns(t, id, env.stateFor(t, auth.FlowLink, tk.Session.UUID, uid))
	if _, f, l := env.callback(t, provider); f.Get("linked") != "1" {
		t.Fatalf("link = %q", l)
	}
	tk = env.login(t, a)
	env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '2 hours' WHERE uuid = $1::uuid`, tk.Session.UUID)
	env.do(t, http.MethodPost, "/v1/session/step-up/oidc/"+provider+"/start", tk.access(), nil).wantStatus(t, http.StatusOK)

	env.idpReturns(t, id, env.stateFor(t, auth.FlowStepUp, tk.Session.UUID, uid))
	env.obs.reset()
	status, frag, loc := env.callback(t, provider)
	if status != http.StatusFound || frag.Get("stepped_up") != "1" {
		t.Fatalf("step-up callback = %d %q", status, loc)
	}
	if strings.Contains(loc, "access_token") || frag.Get("handoff") != "" {
		t.Errorf("step-up redirect carries a token or handoff: %q", loc)
	}
	if got := env.obs.pairs(); len(got) != 1 || got[0] != "update session" {
		t.Errorf("step-up observations = %v, want [update session]", got)
	}

	// The tab takes a fresh access token from switch; the row is raised: a
	// password plus a federated factor is two classes (acr 2), the provider
	// does not trust the IdP's asserted acr 3, and auth_time is now.
	sw := env.do(t, http.MethodPost, "/v1/session/switch", tk.access(), map[string]string{"profile_uuid": tk.Profile.UUID}).
		wantStatus(t, http.StatusOK).tok(t)
	if sw.Session.ACR != 2 {
		t.Errorf("acr after password + oidc = %d, want 2 (asserted acr 3 not trusted)", sw.Session.ACR)
	}
	if _, p := env.probe(t, "/v1/probe", sw.access()); time.Since(p.AuthTime) > time.Minute {
		t.Errorf("auth_time after step-up = %v", p.AuthTime)
	}

	// A step-up with another principal's credential, or from a dead session, is refused.
	other := env.register(t)
	env.idpReturns(t, id, env.stateFor(t, auth.FlowStepUp, other.tk.Session.UUID, env.principalID(t, other.Email)))
	env.wantRedirectError(t, provider, "authentication_failed")
}

func TestOIDC_StepUp_TrustedProviderCountsTheAssertedAcr(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeNone, TrustACR: true})
	tk := env.login(t, a)
	id := idpIdentity(uniq("trust")+"@example.test", true)
	id.AssertedACR = 3
	env.idpReturns(t, id, env.stateFor(t, auth.FlowLink, tk.Session.UUID, uid))
	if _, f, l := env.callback(t, provider); f.Get("linked") != "1" {
		t.Fatalf("link = %q", l)
	}
	env.idpReturns(t, id, env.stateFor(t, auth.FlowStepUp, tk.Session.UUID, uid))
	if _, f, l := env.callback(t, provider); f.Get("stepped_up") != "1" {
		t.Fatalf("step-up = %q", l)
	}
	sw := env.do(t, http.MethodPost, "/v1/session/switch", tk.access(), map[string]string{"profile_uuid": tk.Profile.UUID}).
		wantStatus(t, http.StatusOK).tok(t)
	if sw.Session.ACR != 3 {
		t.Errorf("acr with a trusted asserted acr 3 = %d, want 3", sw.Session.ACR)
	}
}
