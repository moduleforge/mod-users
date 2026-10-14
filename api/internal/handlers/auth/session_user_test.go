package auth

// Tests for the "user" object returned by Login and EmailCodeVerify: it must
// carry email_verified_at with GET /v1/self semantics (always present;
// timestamp when verified, explicit null when not).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	db "github.com/moduleforge/mod-users/model/db"
)

// codeQuerier extends stubAuthQuerier with an active email code.
type codeQuerier struct {
	stubAuthQuerier
	code db.EmailCode
}

func (c *codeQuerier) GetActiveEmailCode(_ context.Context, _ db.GetActiveEmailCodeParams) (db.EmailCode, error) {
	return c.code, nil
}

// ClaimEmailCodeAttempt grants the attempt and returns the configured code, as
// the real query does for a live code.
func (c *codeQuerier) ClaimEmailCodeAttempt(_ context.Context, _ db.ClaimEmailCodeAttemptParams) (db.EmailCode, error) {
	return c.code, nil
}

func verifiedAccount(email string, verifiedAt *time.Time) db.UserAccount {
	ua := namedAccount(email)
	ua.Uuid = uuid.New()
	ua.EmailVerifiedAt = verifiedAt
	return ua
}

// userObject decodes the response body and returns its "user" object plus the
// raw body for key-presence assertions.
func userObject(t *testing.T, rec *httptest.ResponseRecorder) (map[string]any, string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Token string         `json:"token"`
		User  map[string]any `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Token == "" {
		t.Fatal("token missing")
	}
	return body.User, rec.Body.String()
}

func assertEmailVerifiedAt(t *testing.T, user map[string]any, raw string, want *time.Time) {
	t.Helper()
	v, present := user["email_verified_at"]
	if !present {
		t.Fatalf("email_verified_at key missing; want always present: %s", raw)
	}
	if want == nil {
		if v != nil {
			t.Errorf("email_verified_at = %#v, want explicit null", v)
		}
		if !strings.Contains(raw, `"email_verified_at":null`) {
			t.Errorf("body %s lacks email_verified_at:null", raw)
		}
		return
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("email_verified_at = %#v, want RFC 3339 string", v)
	}
	got, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse email_verified_at %q: %v", s, err)
	}
	if !got.Equal(*want) {
		t.Errorf("email_verified_at = %v, want %v", got, *want)
	}
}

func TestLogin_UserCarriesEmailVerifiedAt(t *testing.T) {
	t.Parallel()

	hash, err := localauth.HashPassword("correct-horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name     string
		verified *time.Time
	}{
		{"verified account", &at},
		{"unverified account", nil},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ua := verifiedAccount("a@example.com", tc.verified)
			q := &stubAuthQuerier{accountByEmail: &ua, authLocal: &db.AuthLocal{PasswordHash: hash}}
			h := newGuardTestHandler(q, &recordingSender{})

			req := httptest.NewRequest(http.MethodPost, "/v1/auth/login",
				bytes.NewBufferString(`{"email":"a@example.com","password":"correct-horse"}`))
			rec := httptest.NewRecorder()
			h.Login(rec, req)

			user, raw := userObject(t, rec)
			if user["uuid"] != ua.Uuid.String() || user["email"] != "a@example.com" {
				t.Errorf("unexpected user identity fields: %v", user)
			}
			assertEmailVerifiedAt(t, user, raw, tc.verified)
		})
	}
}

func TestEmailCodeVerify_UserCarriesEmailVerifiedAt(t *testing.T) {
	t.Parallel()

	codeHash, err := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	tests := []struct {
		name     string
		verified *time.Time
	}{
		{"verified account", &at},
		{"unverified account", nil},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ua := verifiedAccount("a@example.com", tc.verified)
			q := &codeQuerier{
				stubAuthQuerier: stubAuthQuerier{accountByEmail: &ua},
				code: db.EmailCode{
					ID:        7,
					CodeHash:  string(codeHash),
					Purpose:   "login",
					ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true},
				},
			}
			h := newGuardTestHandler(q, &recordingSender{})

			req := httptest.NewRequest(http.MethodPost, "/v1/auth/email-code/verify",
				bytes.NewBufferString(`{"email":"a@example.com","code":"123456","purpose":"login"}`))
			rec := httptest.NewRecorder()
			h.EmailCodeVerify(rec, req)

			user, raw := userObject(t, rec)
			if user["uuid"] != ua.Uuid.String() || user["email"] != "a@example.com" {
				t.Errorf("unexpected user identity fields: %v", user)
			}
			assertEmailVerifiedAt(t, user, raw, tc.verified)
		})
	}
}
