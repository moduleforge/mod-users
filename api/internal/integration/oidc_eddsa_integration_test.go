//go:build integration

package integration

// The OIDC browser login against an identity provider that signs its ID
// tokens with EdDSA. Unlike the other OIDC tests, OAuth.ExchangeFn stays
// unset: the provider state is built by the real discovery and verifier
// setup, the token endpoint is an Ed25519 httptest issuer, and the code
// exchange, ID token verification, nonce check and claim mapping all run
// before the resolver and the handoff against the database.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
)

const (
	eddsaProviderID = "mfidp"
	eddsaClientID   = "mfidp-client"
	eddsaKID        = "k1"
)

// edIssuer is an httptest OIDC issuer advertising EdDSA, publishing one
// Ed25519 key as an OKP JWKS entry and returning idToken from /token.
type edIssuer struct {
	*httptest.Server
	priv ed25519.PrivateKey

	mu      sync.Mutex
	idToken string
}

func newEdIssuer(t *testing.T) *edIssuer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	iss := &edIssuer{priv: priv}
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"issuer":                                iss.URL,
			"authorization_endpoint":                iss.URL + "/authorize",
			"token_endpoint":                        iss.URL + "/token",
			"jwks_uri":                              iss.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"EdDSA"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"keys": []map[string]any{{
			"kty": "OKP", "crv": "Ed25519", "use": "sig", "kid": eddsaKID,
			"x": base64.RawURLEncoding.EncodeToString(pub),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		iss.mu.Lock()
		tok := iss.idToken
		iss.mu.Unlock()
		writeJSON(w, map[string]any{"access_token": "at", "id_token": tok, "token_type": "Bearer", "expires_in": 3600})
	})
	iss.Server = httptest.NewServer(mux)
	t.Cleanup(iss.Close)
	return iss
}

func (i *edIssuer) claims(sub, nonce string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": i.URL, "sub": sub, "aud": eddsaClientID, "nonce": nonce,
		"email": "owner@example.test", "email_verified": true,
		"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
	}
}

// edDSAToken is an EdDSA aud+jwt ID token signed by the issuer's key.
func (i *edIssuer) edDSAToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["typ"] = "aud+jwt"
	tok.Header["kid"] = eddsaKID
	s, err := tok.SignedString(i.priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// hs256Token is an aud+jwt ID token carrying the issuer's kid but signed with
// a shared secret.
func (i *edIssuer) hs256Token(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["typ"] = "aud+jwt"
	tok.Header["kid"] = eddsaKID
	s, err := tok.SignedString([]byte("a-shared-secret-of-sufficient-length"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (i *edIssuer) setIDToken(tok string) {
	i.mu.Lock()
	i.idToken = tok
	i.mu.Unlock()
}

// useEdDSAProvider builds provider state for eddsaProviderID through the
// orchestrator's own initialization against iss, with just-in-time creation
// off, and restores the previous provider states when the test ends.
func (s *stack) useEdDSAProvider(t *testing.T, iss *edIssuer) {
	t.Helper()
	if s.oauth.ExchangeFn != nil {
		t.Fatal("ExchangeFn is set; the real exchange must run")
	}
	prev := make(map[string]*auth.ProviderState, len(s.oauth.States))
	for k, v := range s.oauth.States {
		prev[k] = v
	}
	reg := config.ProviderRegistry{eddsaProviderID: config.Provider{
		ID: eddsaProviderID, DisplayName: "MFIdP", IssuerURL: iss.URL, ClientID: eddsaClientID,
		ClientSecret: "secret", ClaimStyle: "google", Scopes: []string{"openid", "email"},
		JITMode: config.JITModeNone,
	}}
	if err := s.oauth.Rebuild(context.Background(), reg); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	t.Cleanup(func() { s.oauth.States = prev })
	if st := s.oauth.States[eddsaProviderID]; st == nil || !st.InitOK {
		t.Fatalf("provider state not initialized: %+v", st)
	}
}

// startLogin drives /start and returns the authorize-URL nonce, the state
// query value and the state cookie.
func (s *stack) startLogin(t *testing.T, iss *edIssuer) (nonce, state string, cookie *http.Cookie) {
	t.Helper()
	r := s.doRaw(t, http.MethodGet, "/v1/auth/oidc/"+eddsaProviderID+"/start?return=/home&handoff_challenge="+auth.HandoffChallenge(handoffVerifier), nil)
	if r.status != http.StatusFound {
		t.Fatalf("start = %d %s", r.status, r.raw)
	}
	u, err := url.Parse(r.header.Get("Location"))
	if err != nil || u.Scheme+"://"+u.Host != iss.URL || u.Path != "/authorize" {
		t.Fatalf("start redirects to %q (%v), want the issuer's authorize endpoint", r.header.Get("Location"), err)
	}
	nonce, state = u.Query().Get("nonce"), u.Query().Get("state")
	if nonce == "" || state == "" {
		t.Fatalf("authorize URL lacks nonce or state: %q", u)
	}
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == "oidc_state" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("start set no oidc_state cookie")
	}
	return nonce, state, cookie
}

// loginCallback performs the provider callback with the cookie from start and
// returns the redirect fragment.
func (s *stack) loginCallback(t *testing.T, state string, cookie *http.Cookie) (status int, frag url.Values) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/"+eddsaProviderID+"/callback?code=c&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", loc, err)
	}
	frag, err = url.ParseQuery(u.Fragment)
	if err != nil {
		t.Fatalf("parse fragment %q: %v", u.Fragment, err)
	}
	return rec.Code, frag
}

func TestOIDC_EdDSA_LoginFlow(t *testing.T) {
	iss := newEdIssuer(t)
	env.useEdDSAProvider(t, iss)

	// A user whose (issuer, sub) is pre-linked signs in through the real
	// callback and handoff.
	linked := env.register(t)
	uid := env.principalID(t, linked.Email)
	sub := uniq("eddsa-sub-")
	credID := env.scanInt64(t, "oidc credential", `INSERT INTO mod_users.credentials (principal_id, kind) VALUES ($1, 'oidc') RETURNING id`, uid)
	env.exec(t, "cred_oidc", `INSERT INTO mod_users.cred_oidc (credential_id, issuer, provider_id, subject) VALUES ($1, $2, $3, $4)`,
		credID, iss.URL, eddsaProviderID, sub)

	t.Run("linked identity gets a session", func(t *testing.T) {
		nonce, state, cookie := env.startLogin(t, iss)
		iss.setIDToken(iss.edDSAToken(t, iss.claims(sub, nonce)))
		status, frag := env.loginCallback(t, state, cookie)
		code := frag.Get("handoff")
		if status != http.StatusFound || code == "" {
			t.Fatalf("callback = %d %v, want a redirect with a handoff code", status, frag)
		}
		tk := env.do(t, http.MethodPost, "/v1/auth/handoff", "", handoffBody(code)).wantStatus(t, http.StatusOK).tok(t)
		if _, p := env.probe(t, "/v1/probe", tk.access()); p.PrincipalID != uid {
			t.Errorf("session principal = %d, want the linked user %d", p.PrincipalID, uid)
		}
	})

	t.Run("unlinked identity under jit none is refused", func(t *testing.T) {
		users := env.count(t, "users", `SELECT count(*) FROM mod_users.users`)
		nonce, state, cookie := env.startLogin(t, iss)
		iss.setIDToken(iss.edDSAToken(t, iss.claims(uniq("eddsa-unlinked-"), nonce)))
		status, frag := env.loginCallback(t, state, cookie)
		if status != http.StatusFound || frag.Get("error") != "no_account" || frag.Get("handoff") != "" {
			t.Fatalf("callback = %d %v, want error=no_account", status, frag)
		}
		if got := env.count(t, "users", `SELECT count(*) FROM mod_users.users`); got != users {
			t.Errorf("a refused sign-in created a user")
		}
	})

	t.Run("token with the wrong nonce is refused", func(t *testing.T) {
		_, state, cookie := env.startLogin(t, iss)
		iss.setIDToken(iss.edDSAToken(t, iss.claims(sub, "not-the-nonce")))
		status, frag := env.loginCallback(t, state, cookie)
		if status != http.StatusFound || frag.Get("error") != "authentication_failed" || frag.Get("handoff") != "" {
			t.Fatalf("callback = %d %v, want error=authentication_failed", status, frag)
		}
	})

	t.Run("HS256 token from the same issuer is refused", func(t *testing.T) {
		nonce, state, cookie := env.startLogin(t, iss)
		iss.setIDToken(iss.hs256Token(t, iss.claims(sub, nonce)))
		status, frag := env.loginCallback(t, state, cookie)
		if status != http.StatusFound || frag.Get("error") != "authentication_failed" || frag.Get("handoff") != "" {
			t.Fatalf("callback = %d %v, want error=authentication_failed", status, frag)
		}
	})
}
