package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/mod-users/api/internal/config"
	db "github.com/moduleforge/mod-users/model/db"
)

func headerOf(t *testing.T, tok string) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var h map[string]any
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	return h
}

func TestEdDSASigner_IssuesVerifiableTokensWithKid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSigner(config.LocalAuthConfig{JWTAlg: "EdDSA", JWTSigningKey: priv, JWTSigningKID: "key-1"})
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	ua := db.UserAccount{Uuid: uuid.New()}
	actor := db.UserAccount{Uuid: uuid.New()}

	issuers := map[string]func() (string, error){
		"local":     func() (string, error) { return IssueLocalJWTWithSigner(ua, s, "iss") },
		"anonymous": func() (string, error) { return IssueAnonymousJWTWithSigner(ua, s, "iss") },
		"assume":    func() (string, error) { return IssueAssumeJWTWithSigner(ua, actor, s, "iss") },
	}
	for name, issue := range issuers {
		t.Run(name, func(t *testing.T) {
			tok, err := issue()
			if err != nil {
				t.Fatalf("issue: %v", err)
			}
			h := headerOf(t, tok)
			if h["alg"] != "EdDSA" || h["kid"] != "key-1" {
				t.Errorf("header = %v, want alg=EdDSA kid=key-1", h)
			}
			parsed, err := jwt.Parse(tok, func(*jwt.Token) (any, error) { return pub, nil },
				jwt.WithValidMethods([]string{"EdDSA"}))
			if err != nil || !parsed.Valid {
				t.Errorf("token does not verify with public key: %v", err)
			}
		})
	}
}

func TestHS256Default_Unchanged(t *testing.T) {
	const secret = "a-test-secret-of-sufficient-length-xx"
	ua := db.UserAccount{Uuid: uuid.New()}

	s, err := NewSigner(config.LocalAuthConfig{JWTSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]func() (string, error){
		"legacy": func() (string, error) { return IssueLocalJWT(ua, secret, "iss") },
		"signer": func() (string, error) { return IssueLocalJWTWithSigner(ua, s, "iss") },
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := tok()
			if err != nil {
				t.Fatal(err)
			}
			// Byte-for-byte: the header is exactly what the pre-signer code produced.
			hdr := strings.Split(raw, ".")[0]
			want := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
			if hdr != want {
				t.Errorf("header segment = %s, want %s", hdr, want)
			}
			parsed, err := jwt.Parse(raw, func(*jwt.Token) (any, error) { return []byte(secret), nil },
				jwt.WithValidMethods([]string{"HS256"}))
			if err != nil || !parsed.Valid {
				t.Errorf("does not verify: %v", err)
			}
		})
	}
}

func TestHS256Signer_KidWhenConfigured(t *testing.T) {
	s, err := NewSigner(config.LocalAuthConfig{JWTAlg: "HS256", JWTSecret: "secret", JWTSigningKID: "hs-1"})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := IssueLocalJWTWithSigner(db.UserAccount{Uuid: uuid.New()}, s, "iss")
	if err != nil {
		t.Fatal(err)
	}
	if h := headerOf(t, tok); h["alg"] != "HS256" || h["kid"] != "hs-1" {
		t.Errorf("header = %v", h)
	}
}

func TestNewSigner_Errors(t *testing.T) {
	for name, lc := range map[string]config.LocalAuthConfig{
		"EdDSA without key": {JWTAlg: "EdDSA"},
		"EdDSA short key":   {JWTAlg: "EdDSA", JWTSigningKey: ed25519.PrivateKey("short")},
		"unsupported alg":   {JWTAlg: "RS256"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSigner(lc); err == nil {
				t.Error("expected error")
			}
		})
	}
	var nilSigner *Signer
	if _, err := nilSigner.Sign(jwt.RegisteredClaims{}); err == nil {
		t.Error("nil signer must fail, not panic")
	}
}
