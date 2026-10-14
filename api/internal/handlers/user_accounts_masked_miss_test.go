package handlers

// End-to-end (real handler + real service, stubbed querier and authorizer)
// checks that a missing user-account UUID and an existing-but-forbidden one
// are indistinguishable over HTTP: 403 with identical error.code and
// error.message.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/internal/auth"
	svc "github.com/moduleforge/mod-users/api/internal/service"
	db "github.com/moduleforge/mod-users/model/db"
)

type missQuerier struct {
	db.Querier // nil: any unexpected query panics loudly
	existing   db.UserAccount
}

func (q *missQuerier) GetUserAccountByUUID(_ context.Context, id uuid.UUID) (db.UserAccount, error) {
	if id == q.existing.Uuid {
		return q.existing, nil
	}
	return db.UserAccount{}, pgx.ErrNoRows
}

type denyAllAuthorizer struct{ targets []int64 }

func (a *denyAllAuthorizer) Authorize(_ context.Context, _ string, target *int64) error {
	if target != nil {
		a.targets = append(a.targets, *target)
	}
	return apiresp.ErrForbidden
}

func TestMaskedMiss_HTTP(t *testing.T) {
	existing := db.UserAccount{ID: 7, Uuid: uuid.New(), AccountHolder: 700}
	az := &denyAllAuthorizer{}
	svcReal := svc.NewUserAccountService(nil, &missQuerier{existing: existing}, nil, az, &observer.ObserverGroup{}, nil, nil, nil)
	uh := NewUserAccountsHandler(svcReal, nil, nil)
	ah := NewAssumeHandler(svcReal, "test-secret-test-secret-test-secret", "test")

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithUserContext(req.Context(), &auth.UserContext{EntityID: 42})
			next.ServeHTTP(w, req.WithContext(opctx.WithActor(ctx, 42)))
		})
	})
	r.Get("/v1/user-accounts/{uuid}", uh.Get)
	r.Put("/v1/user-accounts/{uuid}", uh.Update)
	r.Delete("/v1/user-accounts/{uuid}", uh.Delete)
	r.Post("/v1/user-accounts/{uuid}/assume", ah.Assume)

	cases := []struct {
		name, method, suffix, body string
	}{
		{"Get", http.MethodGet, "", ""},
		{"Update", http.MethodPut, "", `{"given_name":"x"}`},
		{"Delete", http.MethodDelete, "", ""},
		{"Assume", http.MethodPost, "/assume", ""},
	}
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			miss := do(tc.method, "/v1/user-accounts/"+uuid.New().String()+tc.suffix, tc.body)
			forbidden := do(tc.method, "/v1/user-accounts/"+existing.Uuid.String()+tc.suffix, tc.body)
			if miss.Code != http.StatusForbidden || forbidden.Code != http.StatusForbidden {
				t.Fatalf("status: miss=%d forbidden=%d, want 403/403", miss.Code, forbidden.Code)
			}
			if miss.Body.String() != forbidden.Body.String() {
				t.Errorf("envelopes differ:\nmiss=%s\nforbidden=%s", miss.Body.String(), forbidden.Body.String())
			}
		})
	}
}
