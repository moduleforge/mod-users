package handlers

// Tests for AppsHandler's membership methods (AssignUser, ListAppUsers,
// RemoveUser, UpdateUserRoles) covering the error-precedence order:
// stateless checks (path UUIDs, body decode and validation) give 400 before
// the nil-target Authorize gives 403, and the app lookup (GetAppByUUID, 404
// on a miss) runs only after Authorize passes.
//
// The nil coreQ is a canary: a nil *coredb.Queries panics on first query, so a
// clean 400 or 403 proves the app lookup was never reached.
//
// Scenarios are exercised without a live database: fakeCoreDBTX implements
// coredb.DBTX directly (pgx.Row is documented by pgx itself as an interface
// specifically to allow this kind of test double).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/entity"
	coredb "github.com/moduleforge/core-model/db"
)

// ---------------------------------------------------------------------------
// Stub Authorizer
// ---------------------------------------------------------------------------

// stubAppsAuthorizer is a minimal coreAuthz.Authorizer stub that records
// every call it receives and returns a pre-configured error.
type stubAppsAuthorizer struct {
	err    error
	calls  int
	target *int64
}

func (s *stubAppsAuthorizer) Authorize(_ context.Context, _ string, target *int64) error {
	s.calls++
	s.target = target
	return s.err
}

// ---------------------------------------------------------------------------
// Fake coredb.DBTX (no live database required)
// ---------------------------------------------------------------------------

// fakeCoreDBTX implements coredb.DBTX. QueryRow distinguishes
// GetEntityByUUID's query from GetAppByUUID's by inspecting the SQL text
// (the apps query is the only one of the two that reads "FROM apps").
type fakeCoreDBTX struct {
	entityErr error // error GetEntityByUUID's row.Scan should return (nil = success)
	entityID  int64 // id the entity and app rows report on success
	appErr    error // error GetAppByUUID's row.Scan should return (nil = success)
}

func (f *fakeCoreDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("fakeCoreDBTX: Exec not supported")
}

func (f *fakeCoreDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("fakeCoreDBTX: Query not supported")
}

func (f *fakeCoreDBTX) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if strings.Contains(sql, "FROM apps") {
		return fakeCoreRow{err: f.appErr, id: f.entityID}
	}
	return fakeCoreRow{err: f.entityErr, id: f.entityID}
}

// fakeCoreRow implements pgx.Row. Scan supports every destination type used
// by GetEntityByUUIDRow and GetAppByUUIDRow so the same fake serves both
// queries; only the ID column (position 0) is given a meaningful value.
type fakeCoreRow struct {
	err error
	id  int64
}

func (f fakeCoreRow) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	for i, d := range dest {
		switch v := d.(type) {
		case *int64:
			if i == 0 {
				*v = f.id
			} else {
				*v = 0
			}
		case *string:
			*v = ""
		case *uuid.UUID:
			*v = uuid.UUID{}
		case *pgtype.Timestamptz:
			*v = pgtype.Timestamptz{}
		case **time.Time:
			*v = nil
		default:
			return fmt.Errorf("fakeCoreRow.Scan: unsupported dest type %T", d)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// requestWithUUIDParam builds a request carrying uuid as the chi {uuid}
// path param (and, if userAccountUUID is non-empty, as {user_account_uuid}
// too), suitable for driving AppsHandler methods directly.
func requestWithUUIDParam(method, path, uuidParam, userAccountUUID string) *http.Request {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("uuid", uuidParam)
	if userAccountUUID != "" {
		chiCtx.URLParams.Add("user_account_uuid", userAccountUUID)
	}
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, chiCtx)
	return httptest.NewRequest(method, path, nil).WithContext(ctx)
}

// appsRequest is requestWithUUIDParam with a JSON body.
func appsRequest(method, path, uuidParam, userAccountUUID, body string) *http.Request {
	r := requestWithUUIDParam(method, path, uuidParam, userAccountUUID)
	r2 := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(r.Context())
	r2.Header.Set("Content-Type", "application/json")
	return r2
}

// callGuardingPanic invokes fn and turns any panic into a t.Fatal, so an
// authz-ordering regression (Authorize running after entity resolution,
// which panics against the nil-coreQ canary) fails with a clear message
// instead of crashing the test binary.
func callGuardingPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: panicked (Authorize did not run before entity resolution): %v", name, r)
		}
	}()
	fn()
}

// ---------------------------------------------------------------------------
// Error precedence: 400 (stateless) before 403 (authorize) before 404 (lookup)
// ---------------------------------------------------------------------------

type appsCase struct {
	name   string
	invoke func(h *AppsHandler, w http.ResponseWriter, r *http.Request)
	// build returns a request; appUUID and userUUID are the path values,
	// body the JSON body (ignored by routes without one).
	build func(appUUID, userUUID, body string) *http.Request
}

func appsCases() []appsCase {
	return []appsCase{
		{"AssignUser",
			func(h *AppsHandler, w http.ResponseWriter, r *http.Request) { h.AssignUser(w, r) },
			func(a, u, b string) *http.Request {
				return appsRequest(http.MethodPost, "/v1/apps/"+a+"/user-accounts", a, "", b)
			}},
		{"ListAppUsers",
			func(h *AppsHandler, w http.ResponseWriter, r *http.Request) { h.ListAppUsers(w, r) },
			func(a, u, b string) *http.Request {
				return requestWithUUIDParam(http.MethodGet, "/v1/apps/"+a+"/user-accounts", a, "")
			}},
		{"RemoveUser",
			func(h *AppsHandler, w http.ResponseWriter, r *http.Request) { h.RemoveUser(w, r) },
			func(a, u, b string) *http.Request {
				return requestWithUUIDParam(http.MethodDelete, "/v1/apps/"+a+"/user-accounts/"+u, a, u)
			}},
		{"UpdateUserRoles",
			func(h *AppsHandler, w http.ResponseWriter, r *http.Request) { h.UpdateUserRoles(w, r) },
			func(a, u, b string) *http.Request {
				return appsRequest(http.MethodPut, "/v1/apps/"+a+"/user-accounts/"+u+"/roles", a, u, b)
			}},
	}
}

// appsValidBody returns a valid body for the named handler.
func appsValidBody(name string) string {
	if name == "AssignUser" {
		return `{"user_uuid":"` + uuid.NewString() + `","roles":["member"]}`
	}
	return `{"roles":["member"]}`
}

func appsForbiddenHandler() (*AppsHandler, *stubAppsAuthorizer) {
	az := &stubAppsAuthorizer{err: apiresp.ErrForbidden}
	// coreQ is deliberately nil (canary, see file comment).
	return &AppsHandler{az: az, entityResolver: entity.NewResolver()}, az
}

func TestAppsHandler_Forbidden_ValidRequest_403(t *testing.T) {
	for _, c := range appsCases() {
		t.Run(c.name, func(t *testing.T) {
			h, az := appsForbiddenHandler()
			req := c.build(uuid.NewString(), uuid.NewString(), appsValidBody(c.name))
			rr := httptest.NewRecorder()

			callGuardingPanic(t, c.name, func() { c.invoke(h, rr, req) })

			if az.calls != 1 {
				t.Errorf("Authorize called %d times, want 1", az.calls)
			}
			if az.target != nil {
				t.Errorf("Authorize target = %v, want nil", az.target)
			}
			if rr.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403, body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestAppsHandler_Forbidden_StatelessFailure_400(t *testing.T) {
	for _, c := range appsCases() {
		valid := appsValidBody(c.name)
		failures := []struct {
			name, app, user, body string
		}{
			{"bad app uuid", "not-a-uuid", uuid.NewString(), valid},
		}
		if c.name == "RemoveUser" || c.name == "UpdateUserRoles" {
			failures = append(failures, struct{ name, app, user, body string }{"bad user_account_uuid", uuid.NewString(), "nope", valid})
		}
		switch c.name {
		case "AssignUser":
			failures = append(failures,
				struct{ name, app, user, body string }{"malformed body", uuid.NewString(), "", `{`},
				struct{ name, app, user, body string }{"missing user_uuid", uuid.NewString(), "", `{"roles":[]}`},
				struct{ name, app, user, body string }{"invalid user_uuid", uuid.NewString(), "", `{"user_uuid":"zzz"}`},
			)
		case "UpdateUserRoles":
			failures = append(failures,
				struct{ name, app, user, body string }{"malformed body", uuid.NewString(), uuid.NewString(), `{`},
			)
		}
		for _, f := range failures {
			t.Run(c.name+"/"+f.name, func(t *testing.T) {
				h, az := appsForbiddenHandler()
				req := c.build(f.app, f.user, f.body)
				rr := httptest.NewRecorder()

				callGuardingPanic(t, c.name, func() { c.invoke(h, rr, req) })

				if rr.Code != http.StatusBadRequest {
					t.Errorf("status = %d, want 400, body=%s", rr.Code, rr.Body.String())
				}
				if az.calls != 0 {
					t.Errorf("Authorize called %d times, want 0 (400 precedes authorization)", az.calls)
				}
			})
		}
	}
}

func TestAppsHandler_MissingUserUUID_KeepsDetailCode(t *testing.T) {
	h, _ := appsForbiddenHandler()
	a := uuid.NewString()
	req := appsRequest(http.MethodPost, "/v1/apps/"+a+"/user-accounts", a, "", `{}`)
	rr := httptest.NewRecorder()
	h.AssignUser(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "users.user_uuid_required") {
		t.Errorf("status=%d body=%s, want 400 with users.user_uuid_required", rr.Code, rr.Body.String())
	}
	req = appsRequest(http.MethodPost, "/v1/apps/"+a+"/user-accounts", a, "", `{"user_uuid":"zzz"}`)
	rr = httptest.NewRecorder()
	h.AssignUser(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_uuid") {
		t.Errorf("status=%d body=%s, want 400 with invalid_uuid", rr.Code, rr.Body.String())
	}
}

// An authorized admin gets 404 for an unknown app (or a non-app entity uuid):
// GetAppByUUID joins apps to entities, so both are a miss.
func TestAppsHandler_AdminUnknownApp_404(t *testing.T) {
	for _, c := range appsCases() {
		t.Run(c.name, func(t *testing.T) {
			az := &stubAppsAuthorizer{}
			h := &AppsHandler{
				az:             az,
				entityResolver: entity.NewResolver(),
				coreQ:          coredb.New(&fakeCoreDBTX{appErr: pgx.ErrNoRows}),
			}
			req := c.build(uuid.NewString(), uuid.NewString(), appsValidBody(c.name))
			rr := httptest.NewRecorder()

			c.invoke(h, rr, req)

			if az.calls != 1 {
				t.Errorf("Authorize called %d times, want 1", az.calls)
			}
			if rr.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404, body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestAppsHandler_LoadApp(t *testing.T) {
	const appID int64 = 99
	h := &AppsHandler{coreQ: coredb.New(&fakeCoreDBTX{entityID: appID})}
	a := uuid.New()
	req := requestWithUUIDParam(http.MethodGet, "/v1/apps/"+a.String()+"/user-accounts", a.String(), "")
	rr := httptest.NewRecorder()
	got, ok := h.loadApp(rr, req, a)
	if !ok || got != appID {
		t.Fatalf("loadApp = (%d, %v), want (%d, true), body=%s", got, ok, appID, rr.Body.String())
	}
}

// NewAppsHandler must not mutate the shared entity resolver: a plain Resolve
// miss for slug "app" keeps the default masked ErrForbidden.
func TestNewAppsHandler_NoAllowNotFoundSideEffect(t *testing.T) {
	resolver := entity.NewResolver()
	_ = NewAppsHandler(nil, nil, &stubAppsAuthorizer{}, nil, resolver, nil)

	q := coredb.New(&fakeCoreDBTX{entityErr: pgx.ErrNoRows})
	_, err := resolver.Resolve(context.Background(), q, uuid.New(), "app")
	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Fatalf("Resolve err = %v, want ErrForbidden", err)
	}
}
