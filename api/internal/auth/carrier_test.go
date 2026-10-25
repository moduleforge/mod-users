package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/mod-users/api/internal/config"
)

const testIssuer = "users-module-test"

// newTestKeys returns a fresh signer and the local verifier for its key.
func newTestKeys(t *testing.T) (*Signer, *Verifier, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	lc := config.LocalAuthConfig{JWTSigningKey: priv, JWTSigningKID: "k1", LocalIssuer: testIssuer}
	s, err := NewSigner(lc)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	v, err := NewLocalVerifier(context.Background(), lc)
	if err != nil {
		t.Fatalf("NewLocalVerifier: %v", err)
	}
	return s, v, priv
}

func sessionParams() SessionTokenParams {
	return SessionTokenParams{
		Issuer:           testIssuer,
		Audience:         "mod-users",
		PrincipalUUID:    uuid.New(),
		SessionUUID:      uuid.New(),
		ProfileUUID:      uuid.New(),
		ACR:              2,
		AMR:              []string{"pwd", "otp"},
		AuthTime:         time.Now().Add(-5 * time.Minute),
		TTL:              time.Hour,
		SessionExpiresAt: time.Now().Add(24 * time.Hour),
	}
}

// decodePayload returns the token's claims without verification.
func decodePayload(t *testing.T, tok string) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return m
}

func TestIssueSessionToken_RoundTrip(t *testing.T) {
	s, v, _ := newTestKeys(t)
	p := sessionParams()
	now := time.Now()

	tok, exp, err := IssueSessionToken(s, p, now)
	if err != nil {
		t.Fatalf("IssueSessionToken: %v", err)
	}
	if got, want := exp.Unix(), now.Truncate(time.Second).Add(time.Hour).Unix(); got != want {
		t.Errorf("exp = %d, want %d", got, want)
	}

	claims := decodePayload(t, tok)
	for _, forbidden := range []string{"email", "roles", "is_anonymous", "act", "cid"} {
		if _, present := claims[forbidden]; present {
			t.Errorf("claim %q must not be present", forbidden)
		}
	}
	if claims["acr"] != "2" {
		t.Errorf("acr = %v, want \"2\"", claims["acr"])
	}
	if claims["aud"] != "mod-users" {
		t.Errorf("aud = %v, want mod-users", claims["aud"])
	}

	c, err := v.verifyCarrier(tok, now)
	if err != nil {
		t.Fatalf("verifyCarrier: %v", err)
	}
	if c.SessionUUID != p.SessionUUID || c.CredentialUUID != uuid.Nil {
		t.Errorf("sid/cid = %v/%v, want %v/nil", c.SessionUUID, c.CredentialUUID, p.SessionUUID)
	}
	if c.PrincipalUUID != p.PrincipalUUID || c.ProfileUUID != p.ProfileUUID {
		t.Errorf("sub/prof mismatch")
	}
	if c.AuthTime.Unix() != p.AuthTime.Unix() {
		t.Errorf("auth_time = %v, want %v", c.AuthTime, p.AuthTime)
	}
	if len(c.Audience) != 1 || c.Audience[0] != "mod-users" {
		t.Errorf("aud = %v", c.Audience)
	}
}

func TestIssueSessionToken_ExpiryBounds(t *testing.T) {
	s, _, _ := newTestKeys(t)
	now := time.Now()

	t.Run("session expiry caps exp", func(t *testing.T) {
		p := sessionParams()
		p.SessionExpiresAt = now.Add(10 * time.Minute)
		_, exp, err := IssueSessionToken(s, p, now)
		if err != nil {
			t.Fatalf("IssueSessionToken: %v", err)
		}
		if exp.Unix() != p.SessionExpiresAt.Truncate(time.Second).Unix() {
			t.Errorf("exp = %v, want session expiry %v", exp, p.SessionExpiresAt)
		}
	})
	t.Run("ttl capped at one hour", func(t *testing.T) {
		p := sessionParams()
		p.TTL = 5 * time.Hour
		_, exp, err := IssueSessionToken(s, p, now)
		if err != nil {
			t.Fatalf("IssueSessionToken: %v", err)
		}
		if got := exp.Sub(now.Truncate(time.Second)); got != config.MaxAccessTokenTTL {
			t.Errorf("lifetime = %v, want %v", got, config.MaxAccessTokenTTL)
		}
	})
	t.Run("expired session refused", func(t *testing.T) {
		p := sessionParams()
		p.SessionExpiresAt = now.Add(-time.Minute)
		if _, _, err := IssueSessionToken(s, p, now); err == nil {
			t.Fatal("expected an error for an expired session")
		}
	})
	t.Run("invalid acr refused", func(t *testing.T) {
		p := sessionParams()
		p.ACR = 4
		if _, _, err := IssueSessionToken(s, p, now); err == nil {
			t.Fatal("expected an error for acr 4")
		}
	})
}

func TestIssueSessionToken_Impersonation(t *testing.T) {
	s, _, _ := newTestKeys(t)
	p := sessionParams()
	p.ImpersonatorPrincipalUUID = uuid.New()

	tok, _, err := IssueSessionToken(s, p, time.Now())
	if err != nil {
		t.Fatalf("IssueSessionToken: %v", err)
	}
	act, ok := decodePayload(t, tok)["act"].(map[string]any)
	if !ok {
		t.Fatal("act claim missing under impersonation")
	}
	if act["sub"] != p.ImpersonatorPrincipalUUID.String() {
		t.Errorf("act.sub = %v, want %v", act["sub"], p.ImpersonatorPrincipalUUID)
	}
}

func TestIssueClientCredentialsToken_RoundTrip(t *testing.T) {
	s, v, _ := newTestKeys(t)
	now := time.Now()
	p := ClientCredentialsTokenParams{
		Issuer:         testIssuer,
		Audience:       "billing",
		PrincipalUUID:  uuid.New(),
		CredentialUUID: uuid.New(),
		ProfileUUID:    uuid.New(),
		ACR:            1,
		TTL:            15 * time.Minute,
	}
	tok, exp, err := IssueClientCredentialsToken(s, p, now)
	if err != nil {
		t.Fatalf("IssueClientCredentialsToken: %v", err)
	}
	if got := exp.Sub(now.Truncate(time.Second)); got != 15*time.Minute {
		t.Errorf("lifetime = %v, want 15m", got)
	}
	c, err := v.verifyCarrier(tok, now)
	if err != nil {
		t.Fatalf("verifyCarrier: %v", err)
	}
	if c.CredentialUUID != p.CredentialUUID || c.SessionUUID != uuid.Nil {
		t.Errorf("cid/sid = %v/%v", c.CredentialUUID, c.SessionUUID)
	}
	if len(c.AMR) != 1 || c.AMR[0] != "swk" {
		t.Errorf("amr = %v, want [swk]", c.AMR)
	}
}

func TestVerifyCarrier_Rejects(t *testing.T) {
	s, v, priv := newTestKeys(t)
	now := time.Now()

	sign := func(claims jwt.MapClaims) string {
		t.Helper()
		tok, err := s.Sign(claims)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		return tok
	}
	base := func() jwt.MapClaims {
		return jwt.MapClaims{
			"iss": testIssuer, "aud": "mod-users", "sub": uuid.NewString(),
			"sid": uuid.NewString(), "prof": uuid.NewString(), "acr": "1",
			"amr": []string{"pwd"}, "auth_time": now.Unix(), "iat": now.Unix(),
			"exp": now.Add(time.Hour).Unix(),
		}
	}
	with := func(k string, val any) string {
		c := base()
		if val == nil {
			delete(c, k)
		} else {
			c[k] = val
		}
		return sign(c)
	}

	hs, err := jwt.NewWithClaims(jwt.SigningMethodHS256, base()).SignedString([]byte(priv.Seed()))
	if err != nil {
		t.Fatalf("hmac sign: %v", err)
	}
	audTok, err := IssueAudienceToken(s, testIssuer, uuid.New(), "relay", time.Minute, nil)
	if err != nil {
		t.Fatalf("IssueAudienceToken: %v", err)
	}
	both := base()
	both["cid"] = uuid.NewString()

	cases := map[string]string{
		"hmac algorithm":      hs,
		"wrong issuer":        with("iss", "someone-else"),
		"expired":             with("exp", now.Add(-time.Minute).Unix()),
		"missing exp":         with("exp", nil),
		"missing aud":         with("aud", nil),
		"bad sub":             with("sub", "not-a-uuid"),
		"bad prof":            with("prof", nil),
		"neither sid nor cid": with("sid", nil),
		"both sid and cid":    sign(both),
		"audience token":      audTok,
		"garbage":             "a.b.c",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := v.verifyCarrier(tok, now); err == nil {
				t.Fatal("expected verifyCarrier to reject the token")
			}
		})
	}
}

func TestIssueAudienceToken(t *testing.T) {
	s, v, _ := newTestKeys(t)
	principal := uuid.New()

	tok, err := IssueAudienceToken(s, testIssuer, principal, "app-x", time.Minute, map[string]any{"nonce": "n1"})
	if err != nil {
		t.Fatalf("IssueAudienceToken: %v", err)
	}
	claims, err := v.verifyLocal(tok)
	if err != nil {
		t.Fatalf("verifyLocal: %v", err)
	}
	if claims["sub"] != principal.String() || claims["aud"] != "app-x" || claims["nonce"] != "n1" {
		t.Errorf("unexpected claims %v", claims)
	}

	if typ := rawTokenTyp(tok); typ != "aud+jwt" {
		t.Errorf("typ header = %q, want aud+jwt", typ)
	}
	if _, err := v.verifyCarrier(tok, time.Now()); err == nil {
		t.Error("access-token verifier should reject an audience token")
	}

	for _, reserved := range []string{"iss", "sub", "aud", "exp", "iat", "nbf", "sid", "cid", "prof", "act", "acr", "amr", "auth_time"} {
		if _, err := IssueAudienceToken(s, testIssuer, principal, "app-x", time.Minute, map[string]any{reserved: "x"}); err == nil {
			t.Errorf("extra claim %q should be refused", reserved)
		}
	}
	if _, err := IssueAudienceToken(s, testIssuer, principal, "", time.Minute, nil); err == nil {
		t.Error("empty audience should be refused")
	}
}

func TestAudiencePolicy(t *testing.T) {
	open := newAudiencePolicy(config.AuthConfig{DefaultAudience: "mod-users"})
	fixed := newAudiencePolicy(config.AuthConfig{DefaultAudience: "mod-users", AcceptedAudiences: []string{"a", "b"}})

	tests := []struct {
		name      string
		p         audiencePolicy
		aud       []string
		wantOK    bool
		wantSlugs []string
	}{
		{"default audience", open, []string{"mod-users"}, true, nil},
		{"app slug needs lookup", open, []string{"billing"}, false, []string{"billing"}},
		{"accepted list hit", fixed, []string{"b"}, true, nil},
		{"accepted list miss, no lookup", fixed, []string{"mod-users"}, false, nil},
		{"empty value", open, []string{""}, false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, slugs := tt.p.check(tt.aud)
			if ok != tt.wantOK || strings.Join(slugs, ",") != strings.Join(tt.wantSlugs, ",") {
				t.Errorf("check(%v) = %v, %v; want %v, %v", tt.aud, ok, slugs, tt.wantOK, tt.wantSlugs)
			}
		})
	}
}

func TestNewSigner(t *testing.T) {
	s, _, _ := newTestKeys(t)
	if s.Alg() != "EdDSA" {
		t.Errorf("Alg = %q, want EdDSA", s.Alg())
	}
	if _, err := NewSigner(config.LocalAuthConfig{}); err == nil {
		t.Error("NewSigner without a key should fail")
	}
	if _, err := NewLocalVerifier(context.Background(), config.LocalAuthConfig{LocalIssuer: testIssuer}); err == nil {
		t.Error("NewLocalVerifier without a key should fail")
	}
}
