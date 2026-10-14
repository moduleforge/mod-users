package handlers

// Order tests (apiresp/ordertest) for the 12 routes of RegisterAccountRoutes:
// the 7 user-account routes, assume, and the 4 apps-membership routes. The
// real service and handlers run behind fakes (see order_helpers_test.go).
//
// Expected order: 401, then 400 (stateless shape), then 403 (authorization,
// masking existence), then 404, with 409/422 only after authorization.
// Per D2 the user-account {uuid} routes mask a missing account as 403 even for
// an authorized caller (AuthorizedMissingStatus 403). Per D3 the apps routes
// authorize with a nil target first, so a missing app is 404 for an authorized
// caller. Task 4.1's outcomes sit in Late: duplicate email 409 on create and
// update, unknown body user_uuid 422 on assign.
//
// Routes ordertest cannot model with a path id (list and create have none) use
// the same path for ExistingPath and MissingPath with the success status as
// AuthorizedMissingStatus. Malformed path UUIDs are covered by a plain table
// test (TestOrderAccountRoutes_MalformedPathUUID) because ordertest's BadBody
// only models body shape.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp/ordertest"
)

func TestOrderAccountRoutes(t *testing.T) {
	router, _, w := orderAccountRouter()

	authorized := ordertest.Actor(orderAdminID)
	stranger := ordertest.Actor(orderStrangerID)

	// route fills the fields common to every route.
	route := func(name, method, existing, missing string, good, bad []byte, missingStatus int, late ...ordertest.Late) ordertest.Route {
		return ordertest.Route{
			Name:                    name,
			Handler:                 router,
			Method:                  method,
			ExistingPath:            existing,
			MissingPath:             missing,
			GoodBody:                good,
			BadBody:                 bad,
			Authorized:              authorized,
			Unauthorized:            stranger,
			AuthorizedMissingStatus: missingStatus,
			Late:                    late,
		}
	}

	accounts := "/v1/user-accounts"
	acct := accounts + "/" + w.account.Uuid.String()
	acctMissing := accounts + "/" + w.missing.String()
	apps := "/v1/apps/" + w.appUUID.String() + "/user-accounts"
	appsMissingApp := "/v1/apps/" + w.missing.String() + "/user-accounts"
	member := apps + "/" + w.account.Uuid.String()
	memberMissingApp := appsMissingApp + "/" + w.account.Uuid.String()
	memberMissingUser := apps + "/" + w.missing2.String()

	malformed := []byte(`{"email":`)
	createBody := func(email, given, family string) []byte {
		return []byte(`{"email":"` + email + `","given_name":"` + given + `","family_name":"` + family + `"}`)
	}
	goodCreate := createBody("new@example.com", "New", "Person")

	routes := []ordertest.Route{
		// --- user-account routes ---
		route("GET /user-accounts", http.MethodGet, accounts, accounts, nil, nil, http.StatusOK),

		route("POST /user-accounts", http.MethodPost, accounts, accounts, goodCreate, malformed, http.StatusCreated,
			ordertest.Late{Name: "duplicate email 409", Body: createBody(orderTakenEmail, "Dup", "Licate"), WantStatus: http.StatusConflict}),
		route("POST /user-accounts [unknown field]", http.MethodPost, accounts, accounts, goodCreate,
			[]byte(`{"email":"a@example.com","given_name":"A","family_name":"B","bogus":1}`), http.StatusCreated),
		route("POST /user-accounts [email required]", http.MethodPost, accounts, accounts, goodCreate,
			createBody("  ", "A", "B"), http.StatusCreated),
		route("POST /user-accounts [given_name required]", http.MethodPost, accounts, accounts, goodCreate,
			createBody("a@example.com", " ", "B"), http.StatusCreated),
		route("POST /user-accounts [family_name required]", http.MethodPost, accounts, accounts, goodCreate,
			createBody("a@example.com", "A", " "), http.StatusCreated),
		route("POST /user-accounts [password too short]", http.MethodPost, accounts, accounts, goodCreate,
			[]byte(`{"email":"a@example.com","given_name":"A","family_name":"B","password":"short"}`), http.StatusCreated),

		route("GET /user-accounts/{uuid}", http.MethodGet, acct, acctMissing, nil, nil, http.StatusForbidden),

		route("PUT /user-accounts/{uuid}", http.MethodPut, acct, acctMissing, []byte(`{"given_name":"Renamed"}`), malformed, http.StatusForbidden,
			ordertest.Late{Name: "duplicate email 409", Body: []byte(`{"email":"` + orderTakenEmail + `"}`), WantStatus: http.StatusConflict}),
		route("PUT /user-accounts/{uuid} [unknown field]", http.MethodPut, acct, acctMissing, []byte(`{"given_name":"Renamed"}`),
			[]byte(`{"bogus":1}`), http.StatusForbidden),
		route("PUT /user-accounts/{uuid} [blank email]", http.MethodPut, acct, acctMissing, []byte(`{"given_name":"Renamed"}`),
			[]byte(`{"email":" "}`), http.StatusForbidden),
		route("PUT /user-accounts/{uuid} [blank given_name]", http.MethodPut, acct, acctMissing, []byte(`{"given_name":"Renamed"}`),
			[]byte(`{"given_name":""}`), http.StatusForbidden),
		route("PUT /user-accounts/{uuid} [blank family_name]", http.MethodPut, acct, acctMissing, []byte(`{"given_name":"Renamed"}`),
			[]byte(`{"family_name":" "}`), http.StatusForbidden),

		route("DELETE /user-accounts/{uuid}", http.MethodDelete, acct, acctMissing, nil, nil, http.StatusForbidden),

		// grant-admin and revoke-admin authorize (nil-target manage) inside the
		// composition-root closure, then look the account up: a miss is a
		// post-authorization 404.
		route("POST /user-accounts/{uuid}/grant-admin", http.MethodPost, acct+"/grant-admin", acctMissing+"/grant-admin", nil, nil, http.StatusNotFound),
		route("POST /user-accounts/{uuid}/revoke-admin", http.MethodPost, acct+"/revoke-admin", acctMissing+"/revoke-admin", nil, nil, http.StatusNotFound),

		route("POST /user-accounts/{uuid}/assume", http.MethodPost, acct+"/assume", acctMissing+"/assume", nil, nil, http.StatusForbidden),

		// --- apps membership routes ---
		route("POST /apps/{uuid}/user-accounts", http.MethodPost, apps, appsMissingApp,
			[]byte(`{"user_uuid":"`+w.account.Uuid.String()+`","roles":["member"]}`), malformed, http.StatusNotFound,
			ordertest.Late{Name: "unknown user_uuid 422", Body: []byte(`{"user_uuid":"` + w.missing2.String() + `"}`), WantStatus: http.StatusUnprocessableEntity}),
		route("POST /apps/{uuid}/user-accounts [unknown field]", http.MethodPost, apps, appsMissingApp,
			[]byte(`{"user_uuid":"`+w.account.Uuid.String()+`"}`), []byte(`{"user_uuid":"`+w.account.Uuid.String()+`","bogus":1}`), http.StatusNotFound),
		route("POST /apps/{uuid}/user-accounts [user_uuid required]", http.MethodPost, apps, appsMissingApp,
			[]byte(`{"user_uuid":"`+w.account.Uuid.String()+`"}`), []byte(`{"roles":["member"]}`), http.StatusNotFound),
		route("POST /apps/{uuid}/user-accounts [user_uuid not a uuid]", http.MethodPost, apps, appsMissingApp,
			[]byte(`{"user_uuid":"`+w.account.Uuid.String()+`"}`), []byte(`{"user_uuid":"not-a-uuid"}`), http.StatusNotFound),

		route("GET /apps/{uuid}/user-accounts", http.MethodGet, apps, appsMissingApp, nil, nil, http.StatusNotFound),

		route("DELETE /apps/{uuid}/user-accounts/{user_account_uuid}", http.MethodDelete, member, memberMissingApp, nil, nil, http.StatusNotFound,
			ordertest.Late{Name: "unknown user_account_uuid 404", Path: memberMissingUser, WantStatus: http.StatusNotFound}),

		route("PUT /apps/{uuid}/user-accounts/{user_account_uuid}/roles", http.MethodPut, member+"/roles", memberMissingApp+"/roles",
			[]byte(`{"roles":["member"]}`), malformed, http.StatusNotFound,
			ordertest.Late{Name: "unknown user_account_uuid 404", Path: memberMissingUser + "/roles", Body: []byte(`{"roles":["member"]}`), WantStatus: http.StatusNotFound}),
		route("PUT /apps/{uuid}/user-accounts/{user_account_uuid}/roles [unknown field]", http.MethodPut, member+"/roles", memberMissingApp+"/roles",
			[]byte(`{"roles":["member"]}`), []byte(`{"bogus":1}`), http.StatusNotFound),
	}

	ordertest.Run(t, routes...)
}

// TestOrderAccountRoutes_MalformedPathUUID covers the stateless path-UUID
// slot, which ordertest cannot model: authentication (401) precedes shape
// (400), and shape precedes authorization, so a stranger who would be denied
// still sees 400.
func TestOrderAccountRoutes_MalformedPathUUID(t *testing.T) {
	router, _, w := orderAccountRouter()
	acct := w.account.Uuid.String()
	app := w.appUUID.String()
	const bad = "not-a-uuid"

	cases := []struct {
		name, method, path, body string
	}{
		{"GET account", http.MethodGet, "/v1/user-accounts/" + bad, ""},
		{"PUT account", http.MethodPut, "/v1/user-accounts/" + bad, `{"given_name":"x"}`},
		{"DELETE account", http.MethodDelete, "/v1/user-accounts/" + bad, ""},
		{"grant-admin", http.MethodPost, "/v1/user-accounts/" + bad + "/grant-admin", ""},
		{"revoke-admin", http.MethodPost, "/v1/user-accounts/" + bad + "/revoke-admin", ""},
		{"assume", http.MethodPost, "/v1/user-accounts/" + bad + "/assume", ""},
		{"assign app uuid", http.MethodPost, "/v1/apps/" + bad + "/user-accounts", `{"user_uuid":"` + acct + `"}`},
		{"list members app uuid", http.MethodGet, "/v1/apps/" + bad + "/user-accounts", ""},
		{"remove app uuid", http.MethodDelete, "/v1/apps/" + bad + "/user-accounts/" + acct, ""},
		{"remove user_account_uuid", http.MethodDelete, "/v1/apps/" + app + "/user-accounts/" + bad, ""},
		{"roles app uuid", http.MethodPut, "/v1/apps/" + bad + "/user-accounts/" + acct + "/roles", `{"roles":[]}`},
		{"roles user_account_uuid", http.MethodPut, "/v1/apps/" + app + "/user-accounts/" + bad + "/roles", `{"roles":[]}`},
	}
	ids := []struct {
		name string
		id   ordertest.Identity
		want int
	}{
		{"anonymous", ordertest.Anonymous, http.StatusUnauthorized},
		{"stranger", ordertest.Actor(orderStrangerID), http.StatusBadRequest},
		{"authorized", ordertest.Actor(orderAdminID), http.StatusBadRequest},
	}
	for _, tc := range cases {
		for _, id := range ids {
			t.Run(tc.name+"/"+id.name, func(t *testing.T) {
				req := orderRequest(tc.method, tc.path, tc.body)
				req = req.WithContext(id.id(req.Context()))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != id.want {
					t.Fatalf("status = %d, want %d; body: %s", rec.Code, id.want, rec.Body.String())
				}
			})
		}
	}
}

// TestOrderAccountRoutes_MaskedMissProbesTargetZero proves a masked
// user-account miss calls Authorize(op, 0) exactly once (and never with a real
// target), while a hit calls it once with the account holder.
func TestOrderAccountRoutes_MaskedMissProbesTargetZero(t *testing.T) {
	router, az, w := orderAccountRouter()
	admin := ordertest.Actor(orderAdminID)

	cases := []struct {
		name, method, suffix, body, op string
	}{
		{"Get", http.MethodGet, "", "", "read"},
		{"Update", http.MethodPut, "", `{"given_name":"x"}`, "update"},
		{"Delete", http.MethodDelete, "", "", "delete"},
		{"Assume", http.MethodPost, "/assume", "", "assume"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			do := func(id uuid.UUID) int {
				req := orderRequest(tc.method, "/v1/user-accounts/"+id.String()+tc.suffix, tc.body)
				req = req.WithContext(admin(req.Context()))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				return rec.Code
			}

			az.reset()
			if got := do(w.missing); got != http.StatusForbidden {
				t.Fatalf("miss status = %d, want 403", got)
			}
			if n := az.callsWith(tc.op, 0); n != 1 {
				t.Errorf("Authorize(%q, 0) calls on a masked miss = %d, want exactly 1", tc.op, n)
			}
			if n := len(az.calls); n != 1 {
				t.Errorf("total Authorize calls on a masked miss = %d, want 1", n)
			}

			az.reset()
			if got := do(w.account.Uuid); got == http.StatusForbidden || got == http.StatusUnauthorized {
				t.Fatalf("hit status = %d, want neither 401 nor 403", got)
			}
			if n := az.callsWith(tc.op, 0); n != 0 {
				t.Errorf("Authorize(%q, 0) calls on a hit = %d, want 0", tc.op, n)
			}
			if n := az.callsWith(tc.op, orderHolderID); n != 1 {
				t.Errorf("Authorize(%q, holder) calls on a hit = %d, want 1", tc.op, n)
			}
		})
	}
}

// TestOrderAuthorizer_Target0Denies pins the harness authorizer's contract:
// the target-0 probe is denied, for the admin too, and never errors loudly.
func TestOrderAuthorizer_Target0Denies(t *testing.T) {
	az := &orderAuthorizer{}
	var zero int64
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := ordertest.Actor(orderAdminID)(req.Context())
	if err := az.Authorize(ctx, "read", &zero); err == nil {
		t.Fatal("target 0 was allowed for the admin; it must be denied")
	}
	holder := orderHolderID
	if err := az.Authorize(ctx, "read", &holder); err != nil {
		t.Fatalf("admin on a real target: %v, want nil", err)
	}
}
