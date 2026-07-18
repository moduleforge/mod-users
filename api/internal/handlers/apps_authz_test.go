package handlers

// Regression tests for two phase-gate review findings against AppsHandler's
// membership methods (AssignUser, ListAppUsers, RemoveUser, UpdateUserRoles):
//
//  1. Authz-ordering: h.az.Authorize must run BEFORE loadAppByUUIDParam, so a
//     caller lacking the required grant is rejected with 403 before any
//     cross-module entity resolution occurs (see apps.go's method-level
//     comments). Each ordering test below uses a canary: coreQ is left nil,
//     which panics if entity resolution is ever attempted (a nil *coredb.
//     Queries dereferences a nil field on first query). If Authorize denies
//     first, as required, that code path is never reached and no panic
//     occurs.
//
//  2. Type-scoping: entityResolver.Resolve only confirms a uuid exists
//     somewhere in the shared, cross-module entities table — it has no
//     notion of "app". loadAppByUUIDParam must supplement that with a check
//     against mod-core's apps table (coreQ.GetAppByUUID) so a uuid naming a
//     real but differently-typed entity (natural_person, corporation, etc.)
//     still surfaces as ErrNotFound rather than resolving to a wrong-type id.
//
// Both scenarios are exercised without a live database: fakeCoreDBTX
// implements coredb.DBTX directly (pgx.Row is documented by pgx itself as an
// interface specifically to allow this kind of test double).

import (
	"context"
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
	entityID  int64 // id GetEntityByUUID's row should report on success
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
		return fakeCoreRow{err: f.appErr}
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
// Finding 1: authz-ordering — Authorize must run before loadAppByUUIDParam
// ---------------------------------------------------------------------------

func TestAppsHandler_AuthzRunsBeforeEntityResolution_Forbidden(t *testing.T) {
	methods := []struct {
		name   string
		method string
		path   string
		invoke func(h *AppsHandler, w http.ResponseWriter, r *http.Request)
	}{
		{"AssignUser", http.MethodPost, "/v1/apps/%s/user-accounts", func(h *AppsHandler, w http.ResponseWriter, r *http.Request) { h.AssignUser(w, r) }},
		{"ListAppUsers", http.MethodGet, "/v1/apps/%s/user-accounts", func(h *AppsHandler, w http.ResponseWriter, r *http.Request) { h.ListAppUsers(w, r) }},
		{"RemoveUser", http.MethodDelete, "/v1/apps/%s/user-accounts/%s", func(h *AppsHandler, w http.ResponseWriter, r *http.Request) { h.RemoveUser(w, r) }},
		{"UpdateUserRoles", http.MethodPut, "/v1/apps/%s/user-accounts/%s/roles", func(h *AppsHandler, w http.ResponseWriter, r *http.Request) { h.UpdateUserRoles(w, r) }},
	}

	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			az := &stubAppsAuthorizer{err: apiresp.ErrForbidden}
			// coreQ is deliberately nil: reaching entity resolution before
			// Authorize denies would panic (nil *coredb.Queries), so a
			// clean 403 here proves Authorize ran first.
			h := &AppsHandler{
				az:             az,
				entityResolver: entity.NewResolver().AllowNotFound("app"),
				coreQ:          nil,
			}

			appUUID := uuid.New().String()
			userUUID := uuid.New().String()
			path := m.path
			if strings.Contains(path, "%s/user-accounts/%s") {
				path = fmt.Sprintf(path, appUUID, userUUID)
			} else {
				path = fmt.Sprintf(path, appUUID)
			}
			req := requestWithUUIDParam(m.method, path, appUUID, userUUID)
			rr := httptest.NewRecorder()

			callGuardingPanic(t, m.name, func() { m.invoke(h, rr, req) })

			if az.calls != 1 {
				t.Errorf("%s: Authorize called %d times, want 1", m.name, az.calls)
			}
			if az.target != nil {
				t.Errorf("%s: Authorize target = %v, want nil", m.name, az.target)
			}
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want %d, body=%s", m.name, rr.Code, http.StatusForbidden, rr.Body.String())
			}
		})
	}
}

// TestAppsHandler_AuthzOK_ProceedsToEntityResolution confirms that once
// Authorize allows the request, the handler does continue on to
// loadAppByUUIDParam (an unresolvable uuid still yields 404, preserving the
// pre-existing not-found behavior end-to-end through the reordered code).
func TestAppsHandler_AuthzOK_ProceedsToEntityResolution(t *testing.T) {
	az := &stubAppsAuthorizer{err: nil}
	fakeDB := &fakeCoreDBTX{entityErr: pgx.ErrNoRows}
	h := &AppsHandler{
		az:             az,
		entityResolver: entity.NewResolver().AllowNotFound("app"),
		coreQ:          coredb.New(fakeDB),
	}

	appUUID := uuid.New().String()
	req := requestWithUUIDParam(http.MethodGet, "/v1/apps/"+appUUID+"/user-accounts", appUUID, "")
	rr := httptest.NewRecorder()

	h.ListAppUsers(rr, req)

	if az.calls != 1 {
		t.Fatalf("Authorize called %d times, want 1", az.calls)
	}
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d (unknown app uuid should still 404), body=%s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Finding 2: loadAppByUUIDParam must type-scope through mod-core's apps table
// ---------------------------------------------------------------------------

func TestLoadAppByUUIDParam_TypeScoping(t *testing.T) {
	const resolvedID int64 = 99

	tests := []struct {
		name      string
		entityErr error
		appErr    error
		wantOK    bool
		wantCode  int
	}{
		{
			name:      "uuid unknown to entities table -> not found",
			entityErr: pgx.ErrNoRows,
			wantOK:    false,
			wantCode:  http.StatusNotFound,
		},
		{
			name:      "uuid resolves but is not an app (e.g. natural_person) -> not found",
			entityErr: nil,
			appErr:    pgx.ErrNoRows,
			wantOK:    false,
			wantCode:  http.StatusNotFound,
		},
		{
			name:      "uuid resolves and is an app -> ok",
			entityErr: nil,
			appErr:    nil,
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeDB := &fakeCoreDBTX{entityErr: tt.entityErr, entityID: resolvedID, appErr: tt.appErr}
			h := &AppsHandler{
				entityResolver: entity.NewResolver().AllowNotFound("app"),
				coreQ:          coredb.New(fakeDB),
			}

			appUUID := uuid.New()
			req := requestWithUUIDParam(http.MethodGet, "/v1/apps/"+appUUID.String()+"/user-accounts", appUUID.String(), "")
			rr := httptest.NewRecorder()

			gotID, gotUUID, ok := h.loadAppByUUIDParam(rr, req)

			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v, body=%s", ok, tt.wantOK, rr.Body.String())
			}
			if tt.wantOK {
				if gotID != resolvedID {
					t.Errorf("appID = %d, want %d", gotID, resolvedID)
				}
				if gotUUID != appUUID {
					t.Errorf("appUUID = %v, want %v", gotUUID, appUUID)
				}
				return
			}
			if rr.Code != tt.wantCode {
				t.Errorf("status = %d, want %d, body=%s", rr.Code, tt.wantCode, rr.Body.String())
			}
		})
	}
}
