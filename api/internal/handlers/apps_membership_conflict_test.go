package handlers

// Post-authorization classification tests for the apps membership routes
// (AssignUser, RemoveUser, UpdateUserRoles): which referenced-value misses are
// 404 (the path names the missing resource) and which are 422 (the body names
// it), and the silent non-member successes that are recorded but not
// re-statused.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	coredb "github.com/moduleforge/core-model/db"
	db "github.com/moduleforge/mod-users/model/db"
)

// fakeUsersDBTX implements db.DBTX for the mod-users queries the membership
// handlers call: GetUserAccountByUUID (QueryRow) and the app-membership
// writes (Exec).
type fakeUsersDBTX struct {
	userErr   error // error GetUserAccountByUUID's Scan returns (nil = found)
	userID    int64
	execErr   error
	execCalls int
}

func (f *fakeUsersDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	f.execCalls++
	return pgconn.NewCommandTag("UPDATE 0"), f.execErr
}

func (f *fakeUsersDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("fakeUsersDBTX: Query not supported")
}

func (f *fakeUsersDBTX) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeUserRow{err: f.userErr, id: f.userID}
}

type fakeUserRow struct {
	err error
	id  int64
}

func (r fakeUserRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		switch v := d.(type) {
		case *int64:
			if i == 0 {
				*v = r.id
			}
		case *uuid.UUID, *pgtype.Text, *pgtype.Int8, *pgtype.Timestamptz:
			// zero value is fine
		default:
			// *time.Time pointer-to-pointer and anything else: leave zero.
		}
	}
	return nil
}

func membershipHandler(users *fakeUsersDBTX) (*AppsHandler, *stubAppsAuthorizer) {
	az := &stubAppsAuthorizer{}
	return &AppsHandler{
		az:    az,
		q:     db.New(users),
		coreQ: coredb.New(&fakeCoreDBTX{entityID: 42}),
	}, az
}

// A body user_uuid naming no account is a referenced value, so 422 on the
// field (not 404) once authorization has passed.
func TestAssignUser_UnknownBodyUser_422(t *testing.T) {
	t.Parallel()

	h, az := membershipHandler(&fakeUsersDBTX{userErr: pgx.ErrNoRows})
	a := uuid.NewString()
	body := `{"user_uuid":"` + uuid.NewString() + `","roles":["member"]}`
	rr := httptest.NewRecorder()
	h.AssignUser(rr, appsRequest(http.MethodPost, "/v1/apps/"+a+"/user-accounts", a, "", body))

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body=%s", rr.Code, rr.Body.String())
	}
	for _, want := range []string{`"unprocessable_entity"`, `"user_uuid"`, `"users.user_account_unknown"`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body missing %s: %s", want, rr.Body.String())
		}
	}
	if az.calls != 1 {
		t.Errorf("Authorize called %d times, want 1", az.calls)
	}
}

// A non-miss lookup failure stays a 500: only ErrNoRows is a 422.
func TestAssignUser_UserLookupFault_500(t *testing.T) {
	t.Parallel()

	h, _ := membershipHandler(&fakeUsersDBTX{userErr: fmt.Errorf("connection reset")})
	a := uuid.NewString()
	body := `{"user_uuid":"` + uuid.NewString() + `"}`
	rr := httptest.NewRecorder()
	h.AssignUser(rr, appsRequest(http.MethodPost, "/v1/apps/"+a+"/user-accounts", a, "", body))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body=%s", rr.Code, rr.Body.String())
	}
}

// The path {user_account_uuid} names the addressed resource, so a miss stays
// a 404 after authorization on Remove and UpdateRoles.
func TestMembership_PathUserMiss_Stays404(t *testing.T) {
	t.Parallel()

	for _, c := range appsCases() {
		if c.name != "RemoveUser" && c.name != "UpdateUserRoles" {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			users := &fakeUsersDBTX{userErr: pgx.ErrNoRows}
			h, az := membershipHandler(users)
			rr := httptest.NewRecorder()
			c.invoke(h, rr, c.build(uuid.NewString(), uuid.NewString(), appsValidBody(c.name)))

			if rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404, body=%s", rr.Code, rr.Body.String())
			}
			if az.calls != 1 {
				t.Errorf("Authorize called %d times, want 1", az.calls)
			}
			if users.execCalls != 0 {
				t.Errorf("write ran %d times for a missing user", users.execCalls)
			}
		})
	}
}

// Recorded, not re-statused: removing or re-roling an existing account that is
// not a member of the app affects zero rows and still succeeds (204 / 200).
func TestMembership_NonMember_SilentSuccess(t *testing.T) {
	t.Parallel()

	want := map[string]int{"RemoveUser": http.StatusNoContent, "UpdateUserRoles": http.StatusOK}
	for _, c := range appsCases() {
		code, ok := want[c.name]
		if !ok {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			users := &fakeUsersDBTX{userID: 7}
			h, _ := membershipHandler(users)
			rr := httptest.NewRecorder()
			c.invoke(h, rr, c.build(uuid.NewString(), uuid.NewString(), appsValidBody(c.name)))

			if rr.Code != code {
				t.Fatalf("status = %d, want %d, body=%s", rr.Code, code, rr.Body.String())
			}
			if users.execCalls != 1 {
				t.Errorf("write ran %d times, want 1", users.execCalls)
			}
		})
	}
}
