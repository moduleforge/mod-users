package handlers

// Table test for the server.Decode wrapper driven through real handlers:
// UserAccountsHandler.Create (default 1 MiB cap) and SSHKeysHandler.register
// (32 KiB cap). Every decode failure must give 400 invalid_input with the
// apiresp detail code, before the handler touches its service.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDecodeWrapper_RealHandlers(t *testing.T) {
	t.Parallel()

	const defaultCap = 1 << 20
	bigDefault := `{"email":"` + strings.Repeat("a", defaultCap) + `"}`
	bigSSH := `{"public_key":"` + strings.Repeat("a", sshKeyRegisterBodyMaxBytes) + `"}`

	create := func(w http.ResponseWriter, r *http.Request) {
		(&UserAccountsHandler{}).Create(w, r)
	}
	register := func(w http.ResponseWriter, r *http.Request) {
		(&SSHKeysHandler{}).register(w, r, uuid.New(), false)
	}

	tests := []struct {
		name     string
		handler  http.HandlerFunc
		body     string
		wantCode string
	}{
		{"create unknown field", create, `{"email":"a@example.com","bogus":1}`, "invalid_json_body"},
		{"create trailing data", create, `{"email":"a@example.com"} {}`, "invalid_json_body"},
		{"create over default cap", create, bigDefault, "body_too_large"},
		{"create empty body", create, ``, "invalid_json_body"},
		{"ssh register unknown field", register, `{"public_key":"k","bogus":1}`, "invalid_json_body"},
		{"ssh register trailing data", register, `{"public_key":"k"} {}`, "invalid_json_body"},
		{"ssh register just over 32KiB cap", register, bigSSH, "body_too_large"},
		{"ssh register empty body", register, ``, "invalid_json_body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			tc.handler(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if got := decodeErrorCode(t, rec.Body.Bytes()); got != tc.wantCode {
				t.Errorf("detail code = %q, want %q; body=%s", got, tc.wantCode, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"invalid_input"`) {
				t.Errorf("top-level code is not invalid_input; body=%s", rec.Body.String())
			}
		})
	}
}
