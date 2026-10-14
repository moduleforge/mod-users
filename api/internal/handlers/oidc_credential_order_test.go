package handlers

// Error-order tests for the credential-authenticated OIDC config routes
// (POST /oidc-config/confirm, POST /oidc-config/providers, PUT/GET/DELETE
// /oidc-config/providers/{id}). All identifiers here are prefixed oidccfg.
//
// These routes authenticate by an admin session (AdminChecker), the
// X-Setup-Token header, or a setup_token body field, so they must never sit
// behind RequireAuth. The order under test:
//
//  1. body-independent credentials (admin, then the header token); an
//     AdminChecker fault is a 500;
//  2. the body decode (400);
//  3. the body setup_token when no body-independent credential
//     authenticated (401);
//  4. the stateless shape checks (id pattern, required id), 400;
//  5. state-dependent outcomes (409, 404).
//
// The documented exception to 401-before-400 is a caller with no admin
// session and no header token whose body is malformed: it gets 400, because
// its only possible credential is in that body.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/moduleforge/mod-users/api/internal/config"
	db "github.com/moduleforge/mod-users/model/db"
)

// oidccfgWho names how a caller authenticates in these tests.
type oidccfgWho int

const (
	oidccfgNone      oidccfgWho = iota // no credential anywhere
	oidccfgAdmin                       // AdminChecker says admin
	oidccfgHeader                      // valid X-Setup-Token header
	oidccfgBodyToken                   // valid setup_token injected into the body
)

type oidccfgFixture struct {
	confirmer *OIDCConfigHandler
	providers *ProvidersHandler
	fq        *fakeQuerier
	fp        *fakeProvidersQuerier
	token     string
	router    http.Handler
}

// oidccfgNewFixture wires the confirm and provider handlers with an
// AdminChecker that treats the X-Test-Admin: yes header as an admin session
// (or fails with adminErr when set) and a freshly minted setup token.
func oidccfgNewFixture(t *testing.T, adminErr error) *oidccfgFixture {
	t.Helper()
	fq := newFakeQuerier(db.OidcConfig{ID: 1})
	fp := newFakeProvidersQuerier()
	confirmer := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      fq,
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  config.ProviderRegistry{},
		TokenDisplay: config.TokenDisplayBoth,
		AdminChecker: func(r *http.Request) (bool, error) {
			if adminErr != nil {
				return false, adminErr
			}
			return r.Header.Get("X-Test-Admin") == "yes", nil
		},
	})
	if err := confirmer.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}
	token, err := confirmer.EnsureSetupToken(context.Background())
	if err != nil || token == "" {
		t.Fatalf("EnsureSetupToken: err=%v token=%q", err, token)
	}
	providers := NewProvidersHandler(ProvidersDeps{
		Queries:      fp,
		EnvRegistry:  config.ProviderRegistry{},
		OAuth:        confirmer.deps.OAuth,
		RedirectBase: "http://localhost:8080",
		Confirmer:    confirmer,
	})
	r := chi.NewRouter()
	r.Route("/v1/oidc-config", func(r chi.Router) {
		r.Post("/confirm", confirmer.Confirm)
		r.Post("/providers", providers.Create)
		r.Get("/providers/{id}", providers.Get)
		r.Put("/providers/{id}", providers.Update)
		r.Delete("/providers/{id}", providers.Revert)
	})
	return &oidccfgFixture{confirmer: confirmer, providers: providers, fq: fq, fp: fp, token: token, router: r}
}

// do sends the request as the given caller. For oidccfgBodyToken the token is
// spliced into a JSON-object body ("{" prefix); other callers send body as is.
func (f *oidccfgFixture) do(who oidccfgWho, method, path, body string) *httptest.ResponseRecorder {
	if who == oidccfgBodyToken && strings.HasPrefix(body, "{") {
		sep := ","
		if strings.HasPrefix(body, "{}") {
			sep = ""
		}
		body = `{"setup_token":"` + f.token + `"` + sep + body[1:]
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	switch who {
	case oidccfgAdmin:
		req.Header.Set("X-Test-Admin", "yes")
	case oidccfgHeader:
		req.Header.Set("X-Setup-Token", f.token)
	}
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

type oidccfgErrBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Field   string `json:"field"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"details"`
	} `json:"error"`
}

func oidccfgErr(t *testing.T, rr *httptest.ResponseRecorder) oidccfgErrBody {
	t.Helper()
	var e oidccfgErrBody
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body %q: %v", rr.Body.String(), err)
	}
	return e
}

// oidccfgBadBodies are bodies the decode layer rejects, with the detail
// code DecodeJSON (or parseBody's unknown-key check) must report.
func oidccfgBadBodies() []struct {
	name, body, wantCode, wantField string
} {
	overCap := `{"display_name":"` + strings.Repeat("x", 1<<20) + `"}`
	return []struct{ name, body, wantCode, wantField string }{
		{"malformed", `{"client_id":`, "invalid_json_body", ""},
		{"not-an-object", `[1,2]`, "invalid_json_body", ""},
		{"trailing-data", `{"client_id":"c"} {"x":1}`, "invalid_json_body", ""},
		{"over-cap", overCap, "body_too_large", ""},
	}
}

var oidccfgWriteRoutes = []struct {
	name, method, path string
	bodyID             string // injected into the body for POST
}{
	{"create", http.MethodPost, "/v1/oidc-config/providers", `"id":"keycloak",`},
	{"update", http.MethodPut, "/v1/oidc-config/providers/keycloak", ""},
}

// --- Confirm ---------------------------------------------------------

// TestOidccfg_Confirm_Order covers the body-carried-credential order on
// POST /oidc-config/confirm.
func TestOidccfg_Confirm_Order(t *testing.T) {
	const path = "/v1/oidc-config/confirm"
	good := `{"opt_out":true}`

	t.Run("body-independent callers with a malformed body get 400", func(t *testing.T) {
		for _, who := range []oidccfgWho{oidccfgAdmin, oidccfgHeader} {
			f := oidccfgNewFixture(t, nil)
			rr := f.do(who, http.MethodPost, path, "not json")
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("who=%d: got %d, want 400, body=%s", who, rr.Code, rr.Body.String())
			}
			if f.fq.row.SavedAt.Valid {
				t.Errorf("who=%d: config persisted despite 400", who)
			}
		}
	})

	t.Run("no credential and a well-formed body without a token is 401", func(t *testing.T) {
		f := oidccfgNewFixture(t, nil)
		rr := f.do(oidccfgNone, http.MethodPost, path, good)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401, body=%s", rr.Code, rr.Body.String())
		}
		if f.fq.row.SavedAt.Valid {
			t.Error("config persisted despite 401")
		}
	})

	t.Run("documented exception: no header or admin and a malformed body is 400", func(t *testing.T) {
		// The credential may only be in the body, so the body must be
		// decoded before it can be rejected for lack of a credential. This
		// is the same class as POST /auth/login (400 before 401).
		f := oidccfgNewFixture(t, nil)
		rr := f.do(oidccfgNone, http.MethodPost, path, "not json")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400 (documented exception), body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("wrong body token is still 401 and persists nothing", func(t *testing.T) {
		f := oidccfgNewFixture(t, nil)
		rr := f.do(oidccfgNone, http.MethodPost, path, `{"setup_token":"wrong-`+f.token+`","opt_out":true}`)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401, body=%s", rr.Code, rr.Body.String())
		}
		if f.fq.row.SavedAt.Valid {
			t.Error("config persisted despite 401")
		}
	})

	t.Run("wrong header with a malformed body falls to the body path and is 400", func(t *testing.T) {
		f := oidccfgNewFixture(t, nil)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("not json"))
		req.Header.Set("X-Setup-Token", "wrong-"+f.token)
		rr := httptest.NewRecorder()
		f.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400, body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("wrong header with a well-formed body and no token is 401", func(t *testing.T) {
		f := oidccfgNewFixture(t, nil)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(good))
		req.Header.Set("X-Setup-Token", "wrong-"+f.token)
		rr := httptest.NewRecorder()
		f.router.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401, body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("every credential still authenticates a good body", func(t *testing.T) {
		for _, who := range []oidccfgWho{oidccfgAdmin, oidccfgHeader, oidccfgBodyToken} {
			f := oidccfgNewFixture(t, nil)
			rr := f.do(who, http.MethodPost, path, good)
			if rr.Code != http.StatusOK {
				t.Fatalf("who=%d: got %d, want 200, body=%s", who, rr.Code, rr.Body.String())
			}
			if !f.fq.row.OptOut {
				t.Errorf("who=%d: opt_out not persisted", who)
			}
		}
	})

	t.Run("unknown key, trailing data and over-cap bodies are 400", func(t *testing.T) {
		cases := []struct{ name, body string }{
			{"unknown-key", `{"bogus":1}`},
			{"trailing-data", good + ` {}`},
			{"over-cap", `{"enabled_providers":["` + strings.Repeat("x", 1<<20) + `"]}`},
		}
		for _, c := range cases {
			for _, who := range []oidccfgWho{oidccfgAdmin, oidccfgHeader, oidccfgNone} {
				f := oidccfgNewFixture(t, nil)
				rr := f.do(who, http.MethodPost, path, c.body)
				if rr.Code != http.StatusBadRequest {
					t.Errorf("%s who=%d: got %d, want 400", c.name, who, rr.Code)
				}
			}
		}
	})

	t.Run("AdminChecker fault is 500 even with a malformed body", func(t *testing.T) {
		f := oidccfgNewFixture(t, errors.New("auth backend down"))
		rr := f.do(oidccfgNone, http.MethodPost, path, "not json")
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("got %d, want 500, body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("setup token is cleared after a confirming reconfigure", func(t *testing.T) {
		// Preserves the pre-existing one-shot semantics: once confirmed the
		// stored hash is cleared, so the same token no longer authenticates.
		f := oidccfgNewFixture(t, nil)
		rr := f.do(oidccfgHeader, http.MethodPost, path, good)
		if rr.Code != http.StatusOK {
			t.Fatalf("confirm: got %d, want 200, body=%s", rr.Code, rr.Body.String())
		}
		if !f.confirmer.CurrentState().Confirmed() {
			t.Fatal("state should be confirmed after opt-out")
		}
		rr = f.do(oidccfgHeader, http.MethodPost, path, good)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("reuse of cleared token: got %d, want 401", rr.Code)
		}
	})
}

// --- Provider Create / Update ----------------------------------------

// TestOidccfg_ProviderWrites_Order covers POST and PUT: credential-in-body
// ordering, the decode failures, the documented exception, and the shape
// checks that follow the credential.
func TestOidccfg_ProviderWrites_Order(t *testing.T) {
	for _, rt := range oidccfgWriteRoutes {
		rt := rt
		t.Run(rt.name, func(t *testing.T) {
			good := `{` + rt.bodyID + `"client_id":"c"}`

			t.Run("body-independent callers with a bad body get 400", func(t *testing.T) {
				for _, who := range []oidccfgWho{oidccfgAdmin, oidccfgHeader} {
					for _, c := range oidccfgBadBodies() {
						f := oidccfgNewFixture(t, nil)
						rr := f.do(who, rt.method, rt.path, c.body)
						if rr.Code != http.StatusBadRequest {
							t.Errorf("who=%d %s: got %d, want 400", who, c.name, rr.Code)
							continue
						}
						e := oidccfgErr(t, rr)
						if e.Error.Code != "invalid_input" || len(e.Error.Details) != 1 || e.Error.Details[0].Code != c.wantCode {
							t.Errorf("who=%d %s: error = %+v, want invalid_input with detail %s", who, c.name, e.Error, c.wantCode)
						}
						if len(f.fp.rows) != 0 {
							t.Errorf("who=%d %s: row written despite 400", who, c.name)
						}
					}
				}
			})

			t.Run("no credential and a well-formed body without a token is 401", func(t *testing.T) {
				f := oidccfgNewFixture(t, nil)
				rr := f.do(oidccfgNone, rt.method, rt.path, good)
				if rr.Code != http.StatusUnauthorized {
					t.Fatalf("got %d, want 401, body=%s", rr.Code, rr.Body.String())
				}
				if len(f.fp.rows) != 0 {
					t.Error("row written despite 401")
				}
			})

			t.Run("documented exception: no header or admin and a bad body is 400", func(t *testing.T) {
				// The body carries the credential, so it must be decoded
				// before the missing credential can be reported.
				for _, c := range oidccfgBadBodies() {
					f := oidccfgNewFixture(t, nil)
					rr := f.do(oidccfgNone, rt.method, rt.path, c.body)
					if rr.Code != http.StatusBadRequest {
						t.Errorf("%s: got %d, want 400 (documented exception)", c.name, rr.Code)
					}
				}
			})

			t.Run("wrong body token is 401", func(t *testing.T) {
				f := oidccfgNewFixture(t, nil)
				body := `{` + rt.bodyID + `"setup_token":"wrong-` + f.token + `"}`
				rr := f.do(oidccfgNone, rt.method, rt.path, body)
				if rr.Code != http.StatusUnauthorized {
					t.Fatalf("got %d, want 401, body=%s", rr.Code, rr.Body.String())
				}
				if len(f.fp.rows) != 0 {
					t.Error("row written despite 401")
				}
			})

			t.Run("every credential authenticates a good body", func(t *testing.T) {
				for _, who := range []oidccfgWho{oidccfgAdmin, oidccfgHeader, oidccfgBodyToken} {
					f := oidccfgNewFixture(t, nil)
					rr := f.do(who, rt.method, rt.path, good)
					if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
						t.Fatalf("who=%d: got %d, want 200/201, body=%s", who, rr.Code, rr.Body.String())
					}
					if _, ok := f.fp.rows["keycloak"]; !ok {
						t.Errorf("who=%d: row not written", who)
					}
				}
			})

			t.Run("unknown key is 400 with the key as the field", func(t *testing.T) {
				for _, who := range []oidccfgWho{oidccfgAdmin, oidccfgHeader, oidccfgNone} {
					f := oidccfgNewFixture(t, nil)
					rr := f.do(who, rt.method, rt.path, `{`+rt.bodyID+`"bogus_key":1,"client_id":"c"}`)
					if rr.Code != http.StatusBadRequest {
						t.Fatalf("who=%d: got %d, want 400, body=%s", who, rr.Code, rr.Body.String())
					}
					e := oidccfgErr(t, rr)
					if e.Error.Code != "invalid_input" || len(e.Error.Details) != 1 {
						t.Fatalf("who=%d: error = %+v", who, e.Error)
					}
					d := e.Error.Details[0]
					if d.Field != "bogus_key" || d.Code != "invalid_json_body" || d.Message != "request body contains an unknown field" {
						t.Errorf("who=%d: detail = %+v", who, d)
					}
					if len(f.fp.rows) != 0 {
						t.Errorf("who=%d: row written despite 400", who)
					}
				}
			})

			t.Run("every key the handlers read is accepted", func(t *testing.T) {
				f := oidccfgNewFixture(t, nil)
				body := `{` + rt.bodyID + `"display_name":"K","issuer_url":"https://kc.example.com/realms/main",` +
					`"client_id":"c","client_secret":"s","claim_style":"keycloak","scopes":["openid"],"enabled":true,"setup_token":"` + f.token + `"}`
				rr := f.do(oidccfgNone, rt.method, rt.path, body)
				if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
					t.Fatalf("got %d, want 200/201, body=%s", rr.Code, rr.Body.String())
				}
			})

			t.Run("AdminChecker fault is 500 even with a bad body", func(t *testing.T) {
				f := oidccfgNewFixture(t, errors.New("auth backend down"))
				rr := f.do(oidccfgNone, rt.method, rt.path, `{"client_id":`)
				if rr.Code != http.StatusInternalServerError {
					t.Fatalf("got %d, want 500, body=%s", rr.Code, rr.Body.String())
				}
			})
		})
	}
}

// TestOidccfg_Create_ShapeAfterCredential: the stateless id checks run after
// the credential; a caller without one gets 401, not 400.
func TestOidccfg_Create_ShapeAfterCredential(t *testing.T) {
	const path = "/v1/oidc-config/providers"
	for _, body := range []string{`{}`, `{"id":""}`, `{"id":"BadUpper"}`, `{"id":"a"}`} {
		f := oidccfgNewFixture(t, nil)
		if rr := f.do(oidccfgNone, http.MethodPost, path, body); rr.Code != http.StatusUnauthorized {
			t.Errorf("no credential, body %s: got %d, want 401", body, rr.Code)
		}
		for _, who := range []oidccfgWho{oidccfgAdmin, oidccfgHeader, oidccfgBodyToken} {
			if rr := f.do(who, http.MethodPost, path, body); rr.Code != http.StatusBadRequest {
				t.Errorf("who=%d body %s: got %d, want 400, resp=%s", who, body, rr.Code, rr.Body.String())
			}
		}
	}
}

// TestOidccfg_Update_InvalidIDAfterCredential: PUT with a bad id pattern is
// 400 for an authenticated caller and 401 for an unauthenticated one.
func TestOidccfg_Update_InvalidIDAfterCredential(t *testing.T) {
	const path = "/v1/oidc-config/providers/-bad"
	f := oidccfgNewFixture(t, nil)
	if rr := f.do(oidccfgNone, http.MethodPut, path, `{"client_id":"c"}`); rr.Code != http.StatusUnauthorized {
		t.Errorf("no credential: got %d, want 401", rr.Code)
	}
	for _, who := range []oidccfgWho{oidccfgAdmin, oidccfgHeader, oidccfgBodyToken} {
		if rr := f.do(who, http.MethodPut, path, `{"client_id":"c"}`); rr.Code != http.StatusBadRequest {
			t.Errorf("who=%d: got %d, want 400", who, rr.Code)
		}
	}
}

// TestOidccfg_Update_EmptyBodyAccepted: an empty (zero-byte) PUT body is an
// empty map, not a 400, for a body-independent caller; with no credential it
// is 401 (there is no token anywhere).
func TestOidccfg_Update_EmptyBodyAccepted(t *testing.T) {
	const path = "/v1/oidc-config/providers/keycloak"
	for _, who := range []oidccfgWho{oidccfgAdmin, oidccfgHeader} {
		f := oidccfgNewFixture(t, nil)
		rr := f.do(who, http.MethodPut, path, "")
		if rr.Code != http.StatusOK {
			t.Errorf("who=%d: got %d, want 200, body=%s", who, rr.Code, rr.Body.String())
		}
		if _, ok := f.fp.rows["keycloak"]; !ok {
			t.Errorf("who=%d: empty PUT should still upsert the row", who)
		}
	}
	f := oidccfgNewFixture(t, nil)
	if rr := f.do(oidccfgNone, http.MethodPut, path, ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("no credential, empty body: got %d, want 401", rr.Code)
	}
}

// TestOidccfg_Create_Existing_409 pins the 409 and its new detail.
func TestOidccfg_Create_Existing_409(t *testing.T) {
	f := oidccfgNewFixture(t, nil)
	f.fp.rows["keycloak"] = db.OidcProvider{ID: "keycloak", Enabled: true}
	rr := f.do(oidccfgAdmin, http.MethodPost, "/v1/oidc-config/providers", `{"id":"keycloak"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409, body=%s", rr.Code, rr.Body.String())
	}
	e := oidccfgErr(t, rr)
	if e.Error.Code != "conflict" {
		t.Errorf("code = %q, want conflict", e.Error.Code)
	}
	if len(e.Error.Details) != 1 {
		t.Fatalf("details = %+v, want one", e.Error.Details)
	}
	d := e.Error.Details[0]
	if d.Field != "id" || d.Code != "users.oidc_provider_exists" || d.Message != "provider id already exists; use PUT to update" {
		t.Errorf("detail = %+v", d)
	}
}

// --- Provider Get / Delete (bodyless) --------------------------------

// TestOidccfg_GetDelete_CredentialThenIDPattern: credential (401) first,
// then the id pattern (400, not 404).
func TestOidccfg_GetDelete_CredentialThenIDPattern(t *testing.T) {
	f := oidccfgNewFixture(t, nil)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		const bad = "/v1/oidc-config/providers/-bad"
		if rr := f.do(oidccfgNone, method, bad, ""); rr.Code != http.StatusUnauthorized {
			t.Errorf("%s no credential: got %d, want 401", method, rr.Code)
		}
		for _, who := range []oidccfgWho{oidccfgAdmin, oidccfgHeader} {
			if rr := f.do(who, method, bad, ""); rr.Code != http.StatusBadRequest {
				t.Errorf("%s who=%d: got %d, want 400", method, who, rr.Code)
			}
		}
	}
	// A body setup_token is not a credential on a bodyless route.
	req := httptest.NewRequest(http.MethodGet, "/v1/oidc-config/providers/keycloak",
		strings.NewReader(`{"setup_token":"`+f.token+`"}`))
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("GET with body token only: got %d, want 401", rr.Code)
	}
}

// TestOidccfg_GetDelete_AdminCheckerFault_500 keeps internal faults 500.
func TestOidccfg_GetDelete_AdminCheckerFault_500(t *testing.T) {
	f := oidccfgNewFixture(t, errors.New("auth backend down"))
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		if rr := f.do(oidccfgNone, method, "/v1/oidc-config/providers/keycloak", ""); rr.Code != http.StatusInternalServerError {
			t.Errorf("%s: got %d, want 500", method, rr.Code)
		}
	}
}
