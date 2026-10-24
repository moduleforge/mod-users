//go:build integration

package integration

// The token carrier and the authentication middleware over the composed
// stack: claims of API surface section 5 only, the algorithm pinned to EdDSA,
// audience checks, and an AuthContext whose acr and auth_time come from the
// session row.

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
)

// claimsOf decodes the (unverified) payload of a compact JWT.
func claimsOf(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a compact JWT: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// headerOf decodes the header of a compact JWT.
func headerOf(t *testing.T, token string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// forge signs claims with the stack's real signing key.
func (s *stack) forge(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(s.signKey)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// cloneClaims copies the claims of a real token and applies edits (nil
// deletes the claim).
func cloneClaims(t *testing.T, token string, edits map[string]any) jwt.MapClaims {
	t.Helper()
	c := jwt.MapClaims{}
	for k, v := range claimsOf(t, token) {
		c[k] = v
	}
	for k, v := range edits {
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

func TestCarrier_ClaimsAreExactlyThoseOfAPISurface5(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)
	token := tk.access()

	if h := headerOf(t, token); h["alg"] != "EdDSA" {
		t.Fatalf("alg = %v, want EdDSA", h["alg"])
	}
	c := claimsOf(t, token)
	allowed := map[string]bool{"iss": true, "aud": true, "sub": true, "sid": true, "prof": true,
		"acr": true, "amr": true, "auth_time": true, "iat": true, "exp": true}
	for k := range c {
		if !allowed[k] {
			t.Errorf("unexpected claim %q in an interactive token", k)
		}
	}
	for k := range allowed {
		if _, ok := c[k]; !ok {
			t.Errorf("claim %q missing from an interactive token", k)
		}
	}
	for _, forbidden := range []string{"email", "roles", "sudo_user_uuid", "is_anonymous", "cid", "act"} {
		if _, ok := c[forbidden]; ok {
			t.Errorf("forbidden claim %q present", forbidden)
		}
	}
	if c["iss"] != integIssuer || !audContains(c["aud"], config.DefaultAudience) {
		t.Errorf("iss/aud = %v / %v", c["iss"], c["aud"])
	}
	if c["sid"] != tk.Session.UUID {
		t.Errorf("sid = %v, want session uuid %s", c["sid"], tk.Session.UUID)
	}
	if c["prof"] != tk.Profile.UUID {
		t.Errorf("prof = %v, want %s", c["prof"], tk.Profile.UUID)
	}
	if c["acr"] != "1" {
		t.Errorf("acr = %v, want the string \"1\"", c["acr"])
	}
	if amr, _ := c["amr"].([]any); len(amr) != 1 || amr[0] != "pwd" {
		t.Errorf("amr = %v, want [pwd]", c["amr"])
	}
	// exp - iat is the access token TTL, capped by the session.
	iat, exp := c["iat"].(float64), c["exp"].(float64)
	if d := time.Duration(exp-iat) * time.Second; d != env.cfg.Auth.AccessTokenTTL {
		t.Errorf("exp - iat = %v, want %v", d, env.cfg.Auth.AccessTokenTTL)
	}
	// sub is the principal's uuid.
	if want := env.entityUUID(t, env.principalID(t, a.Email)); c["sub"] != want {
		t.Errorf("sub = %v, want principal uuid %s", c["sub"], want)
	}
}

func audContains(aud any, want string) bool {
	switch v := aud.(type) {
	case string:
		return v == want
	case []any:
		for _, x := range v {
			if x == want {
				return true
			}
		}
	}
	return false
}

func TestCarrier_RejectedTokens(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)
	good := tk.access()
	base := claimsOf(t, good)
	pub := env.signKey.Public().(ed25519.PublicKey)

	hmacSign := func(secret []byte, claims jwt.MapClaims) string {
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	_, otherKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherSigned, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims(base)).SignedString(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	// alg=none, signature empty.
	noneHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	noneBody, _ := json.Marshal(base)
	noneToken := noneHeader + "." + base64.RawURLEncoding.EncodeToString(noneBody) + "."
	// HS256 whose MAC key is the public key bytes (the classic confusion).
	confusion := func() string {
		hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
		body := base64.RawURLEncoding.EncodeToString(noneBody)
		mac := hmac.New(sha256.New, pub)
		mac.Write([]byte(hdr + "." + body))
		return hdr + "." + body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}()
	// Tampered signature: flip the last character.
	tampered := good[:len(good)-2] + "AA"
	if tampered == good {
		tampered = good[:len(good)-2] + "BB"
	}

	tests := []struct {
		name  string
		token string
	}{
		{"HS256 with a random secret", hmacSign([]byte("0123456789abcdef0123456789abcdef"), jwt.MapClaims(base))},
		{"HS256 keyed with the public key (algorithm confusion)", confusion},
		{"alg none", noneToken},
		{"EdDSA signed by an unknown key", otherSigned},
		{"tampered signature", tampered},
		{"wrong issuer", env.forge(t, cloneClaims(t, good, map[string]any{"iss": "someone-else"}))},
		{"wrong audience", env.forge(t, cloneClaims(t, good, map[string]any{"aud": "not-an-audience"}))},
		{"expired", env.forge(t, cloneClaims(t, good, map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}))},
		{"neither sid nor cid", env.forge(t, cloneClaims(t, good, map[string]any{"sid": nil}))},
		{"both sid and cid", env.forge(t, cloneClaims(t, good, map[string]any{"cid": uuid.NewString()}))},
		{"unknown session", env.forge(t, cloneClaims(t, good, map[string]any{"sid": uuid.NewString()}))},
		{"not a JWT", "not-a-token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env.do(t, http.MethodGet, "/v1/probe", tc.token, nil).wantStatus(t, http.StatusUnauthorized)
		})
	}
	// No credentials at all, and a non-bearer scheme.
	env.do(t, http.MethodGet, "/v1/probe", "", nil).wantStatus(t, http.StatusUnauthorized)
	req := env.doRaw(t, http.MethodGet, "/v1/probe", map[string]string{"Authorization": "Basic " + good})
	if req.status != http.StatusUnauthorized {
		t.Errorf("Basic scheme status = %d, want 401", req.status)
	}
	// The unmodified token still works, so the rejections above were the point.
	if status, _ := env.probe(t, "/v1/probe", good); status != http.StatusOK {
		t.Fatalf("good token = %d", status)
	}
}

func TestCarrier_NoRouteReadsACookie(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)
	r := env.doRaw(t, http.MethodGet, "/v1/probe", map[string]string{
		"Cookie": "access_token=" + tk.access() + "; token=" + tk.access() + "; session=" + tk.access(),
	})
	if r.status != http.StatusUnauthorized {
		t.Fatalf("a token in a cookie authenticated the request: %d", r.status)
	}
}

func TestCarrier_AudiencePolicy(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)

	// An explicit accepted-audience list replaces the default audience.
	cfg := env.cfg.Auth
	cfg.AcceptedAudiences = []string{"partner-audience"}
	verifier, err := auth.NewLocalVerifier(t.Context(), env.cfg.LocalAuth)
	if err != nil {
		t.Fatal(err)
	}
	authn := auth.NewAuthenticator(env.pool, verifier, cfg)
	r := chi.NewRouter()
	r.With(auth.NewRequireAuth(authn)).Get("/probe", probeHandler)
	call := func(token string) int {
		req := newRequest(http.MethodGet, "/probe", token)
		rec := newRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := call(tk.access()); got != http.StatusUnauthorized {
		t.Errorf("default-audience token with AcceptedAudiences set = %d, want 401", got)
	}
	partner := env.forge(t, cloneClaims(t, tk.access(), map[string]any{"aud": "partner-audience"}))
	if got := call(partner); got != http.StatusOK {
		t.Errorf("accepted-audience token = %d, want 200", got)
	}

	// With no list the default audience is accepted and an unregistered app
	// slug is not.
	if status, _ := env.probe(t, "/v1/probe", tk.access()); status != http.StatusOK {
		t.Errorf("default audience = %d", status)
	}
	unregistered := env.forge(t, cloneClaims(t, tk.access(), map[string]any{"aud": "no-such-app"}))
	env.do(t, http.MethodGet, "/v1/probe", unregistered, nil).wantStatus(t, http.StatusUnauthorized)
}

func TestMiddleware_AcrAndAuthTimeComeFromTheSessionRow(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)
	env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '3 hours' WHERE uuid = $1::uuid`, tk.Session.UUID)

	// A correctly signed token that claims acr 3 and a fresh auth_time.
	forged := env.forge(t, cloneClaims(t, tk.access(), map[string]any{
		"acr": "3", "auth_time": time.Now().Unix(),
	}))
	status, p := env.probe(t, "/v1/probe", forged)
	if status != http.StatusOK {
		t.Fatalf("forged-claims token = %d", status)
	}
	if p.ACR != 1 || p.IdentityACR != 1 {
		t.Errorf("AuthContext acr = %d (identity %d), want the row's 1, not the token's 3", p.ACR, p.IdentityACR)
	}
	if time.Since(p.AuthTime) < 2*time.Hour {
		t.Errorf("AuthContext auth_time = %v, want the row's three-hour-old value, not the token's", p.AuthTime)
	}
}

func TestMiddleware_AuthContextForAuthenticatedAndAnonymousRequests(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	tk := env.login(t, a)

	status, p := env.probe(t, "/v1/probe", tk.access())
	if status != http.StatusOK {
		t.Fatalf("authenticated probe = %d", status)
	}
	if !p.Authenticated || p.Anonymous || p.PrincipalID != uid || p.ProfileID != env.personalProfileID(t, uid) {
		t.Errorf("authenticated AuthContext = %+v, want principal %d acting as its personal profile", p, uid)
	}
	if p.SessionID == 0 || p.CredentialID != 0 || p.ImpersonatorP != 0 || p.Kind != "user" {
		t.Errorf("authenticated AuthContext = %+v, want a session, no credential, no impersonator", p)
	}

	// A request with no Authorization header gets the anonymous AuthContext
	// behind ResolveOrAnonymous, and never a 401.
	status, anon := env.probe(t, "/v1/probe-anon", "")
	if status != http.StatusOK || !anon.Anonymous || anon.Authenticated || anon.ProfileID == 0 || anon.SessionID != 0 {
		t.Errorf("anonymous probe = %d %+v", status, anon)
	}
	// With a token the same route authenticates.
	status, both := env.probe(t, "/v1/probe-anon", tk.access())
	if status != http.StatusOK || !both.Authenticated || both.PrincipalID != uid {
		t.Errorf("token on the anonymous-capable route = %d %+v", status, both)
	}
	// A bad token is a 401, never silently downgraded to anonymous.
	env.do(t, http.MethodGet, "/v1/probe-anon", "garbage", nil).wantStatus(t, http.StatusUnauthorized)

	// The anonymous system actor holds no grants and is not authenticated:
	// the protected probe still refuses a request without a token.
	env.do(t, http.MethodGet, "/v1/probe", "", nil).wantStatus(t, http.StatusUnauthorized)
}

func TestMiddleware_RequireVerifiedEmail(t *testing.T) {
	a := env.register(t)
	env.do(t, http.MethodGet, "/v1/probe-verified", a.tk.access(), nil).wantAction(t, http.StatusForbidden, "users.email_unverified")

	env.verifyRegisteredEmail(t, a)
	// The verified flag is read per request, so the same token now passes.
	env.do(t, http.MethodGet, "/v1/probe-verified", a.tk.access(), nil).wantStatus(t, http.StatusOK)
}
