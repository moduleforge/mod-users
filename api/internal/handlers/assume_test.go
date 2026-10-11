package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/moduleforge/mod-users/model/db"
)

// stubAssumeSvc returns fixed accounts from Assume.
type stubAssumeSvc struct {
	sudo, actor db.UserAccount
}

func (s stubAssumeSvc) Assume(_ context.Context, _ uuid.UUID) (db.UserAccount, db.UserAccount, error) {
	return s.sudo, s.actor, nil
}

// TestAssume_EmailVerifiedAtAlwaysPresent asserts the assume payload's user
// object always carries email_verified_at: a timestamp when verified, an
// explicit JSON null when not.
func TestAssume_EmailVerifiedAtAlwaysPresent(t *testing.T) {
	t.Parallel()
	verifiedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	cases := []struct {
		name       string
		verifiedAt *time.Time
		want       any
	}{
		{"verified", &verifiedAt, "2026-01-02T03:04:05Z"},
		{"unverified", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actor := db.UserAccount{
				Uuid:            uuid.New(),
				Email:           pgtype.Text{String: "a@example.com", Valid: true},
				EmailVerifiedAt: tc.verifiedAt,
			}
			sudo := db.UserAccount{Uuid: uuid.New()}
			h := NewAssumeHandler(stubAssumeSvc{sudo: sudo, actor: actor}, "test-secret-test-secret-test-secret", "test")

			r := chi.NewRouter()
			r.Post("/v1/user-accounts/{uuid}/assume", h.Assume)
			req := httptest.NewRequest(http.MethodPost, "/v1/user-accounts/"+uuid.NewString()+"/assume", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var body struct {
				User map[string]any `json:"user"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			got, ok := body.User["email_verified_at"]
			if !ok {
				t.Fatalf("email_verified_at key missing: %v", body.User)
			}
			if got != tc.want {
				t.Errorf("email_verified_at = %v, want %v", got, tc.want)
			}
		})
	}
}
