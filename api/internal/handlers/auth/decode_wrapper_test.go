package auth

// Table test for the server.Decode wrapper through the real public auth
// handlers. Decode runs first, so a zero Handler is enough.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeWrapper_PublicAuthHandlers(t *testing.T) {
	t.Parallel()

	h := &Handler{}
	handlers := map[string]http.HandlerFunc{
		"login":                  h.Login,
		"register":               h.Register,
		"anonymous":              h.Anonymous,
		"email-code request":     h.EmailCodeRequest,
		"email-code verify":      h.EmailCodeVerify,
		"password-reset request": h.PasswordResetRequest,
		"password-reset confirm": h.PasswordResetConfirm,
	}
	bodies := []struct{ name, body, want string }{
		{"unknown field", `{"email":"a@example.com","bogus":1}`, "invalid_json_body"},
		{"trailing data", `{"email":"a@example.com"} {}`, "invalid_json_body"},
		{"over cap", `{"email":"` + strings.Repeat("a", 1<<20) + `"}`, "body_too_large"},
		{"empty body", ``, "invalid_json_body"},
	}
	for hn, fn := range handlers {
		for _, b := range bodies {
			t.Run(hn+"/"+b.name, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(b.body))
				rec := httptest.NewRecorder()
				fn(rec, req)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
				}
				out := rec.Body.String()
				if !strings.Contains(out, `"invalid_input"`) || !strings.Contains(out, `"`+b.want+`"`) {
					t.Errorf("want invalid_input with detail %q; body=%s", b.want, out)
				}
			})
		}
	}
}
