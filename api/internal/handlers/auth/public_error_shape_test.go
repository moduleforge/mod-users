package auth

// Tests pinning the apiresp envelope for the public auth handlers' converted
// 4xx paths (status, top-level code, details). The credential-failure 401s
// and register 400/409 literals are intentionally unchanged and covered by
// their own tests.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/config"
)

type errEnvelope struct {
	Error struct {
		Code    string               `json:"code"`
		Message string               `json:"message"`
		Details []apiresp.FieldError `json:"details"`
	} `json:"error"`
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errEnvelope {
	t.Helper()
	var env errEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return env
}

func TestPublicAuth_RequiredFieldDetails(t *testing.T) {
	t.Parallel()

	h := &Handler{}
	cases := []struct {
		name   string
		fn     http.HandlerFunc
		body   string
		fields []string
		codes  []string
	}{
		{"login both", h.Login, `{}`, []string{"email", "password"}, []string{"users.email_required", "users.password_required"}},
		{"login password only", h.Login, `{"email":"a@example.com"}`, []string{"password"}, []string{"users.password_required"}},
		{"email-code verify both", h.EmailCodeVerify, `{}`, []string{"email", "code"}, []string{"users.email_required", "users.code_required"}},
		{"email-code verify code only", h.EmailCodeVerify, `{"email":"a@example.com"}`, []string{"code"}, []string{"users.code_required"}},
		{"reset confirm token", h.PasswordResetConfirm, `{"password":"longenoughpassword"}`, []string{"token"}, []string{"users.token_required"}},
		{"reset confirm short password", h.PasswordResetConfirm, `{"token":"t","password":"short"}`, []string{"password"}, []string{"users.password_too_short"}},
		{"anonymous device_id", h.Anonymous, `{}`, []string{"device_id"}, []string{"users.device_id_required"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			c.fn(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			env := decodeErr(t, rec)
			if env.Error.Code != "invalid_input" {
				t.Errorf("code = %q, want invalid_input", env.Error.Code)
			}
			if len(env.Error.Details) != len(c.fields) {
				t.Fatalf("details = %+v, want fields %v", env.Error.Details, c.fields)
			}
			for i, d := range env.Error.Details {
				if d.Field != c.fields[i] || d.Code != c.codes[i] {
					t.Errorf("detail[%d] = %+v, want field %q code %q", i, d, c.fields[i], c.codes[i])
				}
			}
		})
	}
}

func TestOIDC_ConvertedErrorShapes(t *testing.T) {
	registry := config.ProviderRegistry{
		"google": config.Provider{ID: "google", DisplayName: "Google"},
	}
	h := newTestHandler(t, registry)

	t.Run("start unknown provider is plain 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.Start(rec, setChiProvider(httptest.NewRequest(http.MethodGet, "/", nil), "unknown"))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		env := decodeErr(t, rec)
		if env.Error.Code != "not_found" || len(env.Error.Details) != 0 {
			t.Errorf("envelope = %+v, want not_found without details", env.Error)
		}
	})

	t.Run("start 400 does not echo internal error text", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/?return=https://evil.example/x", nil)
		h.Start(rec, setChiProvider(req, "google"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		env := decodeErr(t, rec)
		if env.Error.Code != "invalid_input" || env.Error.Message != "one or more fields are invalid" {
			t.Errorf("envelope = %+v, want invalid_input with the fixed public message", env.Error)
		}
		if len(env.Error.Details) != 1 || env.Error.Details[0].Code != "users.oidc_start_invalid" {
			t.Errorf("details = %+v, want one users.oidc_start_invalid", env.Error.Details)
		}
		if strings.Contains(rec.Body.String(), "oauth:") || strings.Contains(rec.Body.String(), "evil.example") {
			t.Errorf("body appears to echo internal error text: %s", rec.Body.String())
		}
	})

	t.Run("callback missing code and state", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.Callback(rec, setChiProvider(httptest.NewRequest(http.MethodGet, "/", nil), "google"))
		env := decodeErr(t, rec)
		if rec.Code != http.StatusBadRequest || env.Error.Code != "invalid_input" ||
			len(env.Error.Details) != 1 || env.Error.Details[0].Code != "users.oidc_code_or_state_required" {
			t.Errorf("status=%d envelope=%+v", rec.Code, env.Error)
		}
	})

	t.Run("callback missing state cookie", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.Callback(rec, setChiProvider(httptest.NewRequest(http.MethodGet, "/?code=x&state=y", nil), "google"))
		env := decodeErr(t, rec)
		if rec.Code != http.StatusBadRequest || env.Error.Code != "invalid_input" ||
			len(env.Error.Details) != 1 || env.Error.Details[0].Code != "users.oidc_state_cookie_required" {
			t.Errorf("status=%d envelope=%+v", rec.Code, env.Error)
		}
	})
}
