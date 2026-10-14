package handlers

// Error-order tests for the whole /v1/oidc-config group, mounted through the
// real RegisterOIDCConfigRoutes (apiresp/ordertest cannot model these routes:
// they are public or authenticate by an admin session, the X-Setup-Token
// header, or a body setup_token, never by RequireAuth). Helpers are prefixed
// orderOIDC. The finer per-handler decode/credential cases live in
// oidc_credential_order_test.go; this file pins the per-route order through
// the registrar and proves no route of the group sits behind RequireAuth.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	authhandlers "github.com/moduleforge/mod-users/api/internal/handlers/auth"

	"github.com/moduleforge/mod-users/api/internal/config"
	db "github.com/moduleforge/mod-users/model/db"
)

type orderOIDCWho int

const (
	orderOIDCNone   orderOIDCWho = iota // no credential anywhere
	orderOIDCAdmin                      // AdminChecker says admin
	orderOIDCHeader                     // valid X-Setup-Token header
)

type orderOIDCFixture struct {
	confirmer *OIDCConfigHandler
	providers *ProvidersHandler
	fp        *fakeProvidersQuerier
	token     string
	router    http.Handler
}

func orderOIDCNewFixture(t *testing.T) *orderOIDCFixture {
	t.Helper()
	fq := newFakeQuerier(db.OidcConfig{ID: 1})
	fp := newFakeProvidersQuerier()
	confirmer := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      fq,
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  config.ProviderRegistry{},
		TokenDisplay: config.TokenDisplayBoth,
		AdminChecker: func(r *http.Request) (bool, error) {
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
	// No RequireAuth and no identity: the group must work without either.
	r.Route("/v1/oidc-config", func(r chi.Router) {
		RegisterOIDCConfigRoutes(r, confirmer, providers)
	})
	return &orderOIDCFixture{confirmer: confirmer, providers: providers, fp: fp, token: token, router: r}
}

func (f *orderOIDCFixture) do(who orderOIDCWho, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/oidc-config"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	switch who {
	case orderOIDCAdmin:
		req.Header.Set("X-Test-Admin", "yes")
	case orderOIDCHeader:
		req.Header.Set("X-Setup-Token", f.token)
	}
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

func orderOIDCDetailCode(t *testing.T, rr *httptest.ResponseRecorder) (code, detail string) {
	t.Helper()
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Code string `json:"code"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body %q: %v", rr.Body.String(), err)
	}
	if len(e.Error.Details) > 0 {
		detail = e.Error.Details[0].Code
	}
	return e.Error.Code, detail
}

// TestOrderOIDC_PublicRoutes_NoAuthRequired: status, saved and setup-token are
// public. An anonymous request is never 401 and keeps today's status.
func TestOrderOIDC_PublicRoutes_NoAuthRequired(t *testing.T) {
	f := orderOIDCNewFixture(t)
	for _, p := range []string{"/status", "/saved"} {
		if rr := f.do(orderOIDCNone, http.MethodGet, p, ""); rr.Code != http.StatusOK {
			t.Errorf("GET %s anonymous: got %d, want 200, body=%s", p, rr.Code, rr.Body.String())
		}
	}
	// setup-token is loopback-gated: a non-loopback anonymous caller gets 403
	// (never 401), a loopback one gets the token.
	rr := f.do(orderOIDCNone, http.MethodGet, "/setup-token", "")
	if rr.Code != http.StatusForbidden {
		t.Errorf("GET /setup-token non-loopback: got %d, want 403", rr.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/oidc-config/setup-token", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	rr = httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("GET /setup-token loopback: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
}

// orderOIDCCredRoutes are the 5 credential-authenticated routes.
var orderOIDCCredRoutes = []struct {
	name, method, path string
	body               string // well-formed body
	invalidID          string // path with an invalid id; "" when the route has no id
}{
	{"confirm", http.MethodPost, "/confirm", `{"opt_out":true}`, ""},
	{"create", http.MethodPost, "/providers", `{"id":"keycloak","client_id":"c"}`, ""},
	{"get", http.MethodGet, "/providers/keycloak", "", "/providers/-bad"},
	{"update", http.MethodPut, "/providers/keycloak", `{"client_id":"c"}`, "/providers/-bad"},
	{"revert", http.MethodDelete, "/providers/keycloak", "", "/providers/-bad"},
}

// TestOrderOIDC_CredentialRoutes_Order pins credential, decode, id, handler
// order for each credential route through RegisterOIDCConfigRoutes.
func TestOrderOIDC_CredentialRoutes_Order(t *testing.T) {
	for _, rt := range orderOIDCCredRoutes {
		rt := rt
		t.Run(rt.name, func(t *testing.T) {
			t.Run("no credential with a well-formed body is 401", func(t *testing.T) {
				f := orderOIDCNewFixture(t)
				if rr := f.do(orderOIDCNone, rt.method, rt.path, rt.body); rr.Code != http.StatusUnauthorized {
					t.Fatalf("got %d, want 401, body=%s", rr.Code, rr.Body.String())
				}
			})

			if rt.body != "" {
				t.Run("header or admin caller with a malformed body is 400", func(t *testing.T) {
					for _, who := range []orderOIDCWho{orderOIDCAdmin, orderOIDCHeader} {
						f := orderOIDCNewFixture(t)
						if rr := f.do(who, rt.method, rt.path, `{"client_id":`); rr.Code != http.StatusBadRequest {
							t.Errorf("who=%d: got %d, want 400, body=%s", who, rr.Code, rr.Body.String())
						}
					}
				})

				t.Run("documented exception: no header or admin and a malformed body is 400", func(t *testing.T) {
					// The only possible credential is the body setup_token, so
					// the body must be decoded before a missing credential can
					// be reported: 400 before 401 here, by design (D8).
					f := orderOIDCNewFixture(t)
					if rr := f.do(orderOIDCNone, rt.method, rt.path, `{"client_id":`); rr.Code != http.StatusBadRequest {
						t.Errorf("got %d, want 400 (documented exception), body=%s", rr.Code, rr.Body.String())
					}
				})
			}

			if rt.invalidID != "" {
				t.Run("valid credential with an invalid id is 400", func(t *testing.T) {
					for _, who := range []orderOIDCWho{orderOIDCAdmin, orderOIDCHeader} {
						f := orderOIDCNewFixture(t)
						if rr := f.do(who, rt.method, rt.invalidID, rt.body); rr.Code != http.StatusBadRequest {
							t.Errorf("who=%d: got %d, want 400, body=%s", who, rr.Code, rr.Body.String())
						}
					}
				})
				t.Run("invalid id without a credential is 401", func(t *testing.T) {
					f := orderOIDCNewFixture(t)
					if rr := f.do(orderOIDCNone, rt.method, rt.invalidID, rt.body); rr.Code != http.StatusUnauthorized {
						t.Errorf("got %d, want 401", rr.Code)
					}
				})
			}

			t.Run("valid credential and request reaches the handler", func(t *testing.T) {
				for _, who := range []orderOIDCWho{orderOIDCAdmin, orderOIDCHeader} {
					f := orderOIDCNewFixture(t)
					rr := f.do(who, rt.method, rt.path, rt.body)
					if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusBadRequest || rr.Code >= 500 {
						t.Errorf("who=%d: got %d, want a handler success, body=%s", who, rr.Code, rr.Body.String())
					}
				}
			})
		})
	}
}

// TestOrderOIDC_Create_Existing_409: an existing id on create is 409 with the
// users.oidc_provider_exists detail, after the credential.
func TestOrderOIDC_Create_Existing_409(t *testing.T) {
	f := orderOIDCNewFixture(t)
	f.fp.rows["keycloak"] = db.OidcProvider{ID: "keycloak", Enabled: true}
	if rr := f.do(orderOIDCNone, http.MethodPost, "/providers", `{"id":"keycloak"}`); rr.Code != http.StatusUnauthorized {
		t.Errorf("no credential: got %d, want 401 (credential precedes the conflict)", rr.Code)
	}
	for _, who := range []orderOIDCWho{orderOIDCAdmin, orderOIDCHeader} {
		rr := f.do(who, http.MethodPost, "/providers", `{"id":"keycloak"}`)
		if rr.Code != http.StatusConflict {
			t.Fatalf("who=%d: got %d, want 409, body=%s", who, rr.Code, rr.Body.String())
		}
		if code, detail := orderOIDCDetailCode(t, rr); code != "conflict" || detail != "users.oidc_provider_exists" {
			t.Errorf("who=%d: code=%q detail=%q, want conflict / users.oidc_provider_exists", who, code, detail)
		}
	}
}

// TestOrderOIDC_ComposedRouter_NoRequireAuth composes the oidc-config and
// auth groups with every authenticated registrar on one /v1 router, as mfgen
// does, and shows the authenticated registrars' own RequireAuth does not wrap
// the public groups: an anonymous caller (no identity in the context) reaches
// public routes, and a header-token caller reaches a credential route.
func TestOrderOIDC_ComposedRouter_NoRequireAuth(t *testing.T) {
	f := orderOIDCNewFixture(t)
	oauth := newEmptyOAuth(t)
	cfg := &config.Config{
		LocalAuth: config.LocalAuthConfig{JWTSecret: "test-secret-at-least-32-bytes-xx", LocalIssuer: "test-issuer"},
	}
	authH := authhandlers.New(nil, nil, nil, "test-secret-at-least-32-bytes-xx", "test-issuer", nil, "http://gui.test")
	oidcH := authhandlers.NewOIDCHandler(nil, oauth, nil, nil, cfg)

	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Use(recoverToSentinel)
		RegisterAccountRoutes(r, &UserAccountsHandler{}, &AssumeHandler{}, &AppsHandler{})
		RegisterSelfGetRoute(r, &SelfHandler{})
		RegisterSelfPutRoute(r, &SelfHandler{})
		RegisterSelfIdentitiesReadRoute(r, &IdentitiesHandler{})
		RegisterSelfIdentitiesWriteRoutes(r, &IdentitiesHandler{})
		RegisterSelfSSHKeysReadRoute(r, &SSHKeysHandler{})
		RegisterSelfSSHKeysWriteRoutes(r, &SSHKeysHandler{})
		RegisterUserAccountSSHKeyRoutes(r, &SSHKeysHandler{})
		r.Route("/oidc-config", func(r chi.Router) { RegisterOIDCConfigRoutes(r, f.confirmer, f.providers) })
		r.Route("/auth", func(r chi.Router) { authhandlers.RegisterRoutes(r, authH, oidcH) })
	})
	do := func(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr
	}

	// Sanity: an authenticated registrar on the same router does gate.
	if rr := do(http.MethodGet, "/v1/self", "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/self anonymous: got %d, want 401 (RequireAuth must still gate it)", rr.Code)
	}
	for _, p := range []string{"/v1/auth/providers", "/v1/oidc-config/status", "/v1/oidc-config/saved"} {
		if rr := do(http.MethodGet, p, "", nil); rr.Code != http.StatusOK {
			t.Errorf("GET %s anonymous: got %d, want 200, body=%s", p, rr.Code, rr.Body.String())
		}
	}
	// A credential route with only the setup-token header and no identity
	// succeeds; under RequireAuth it would be 401.
	rr := do(http.MethodPost, "/v1/oidc-config/confirm", `{"opt_out":true}`, map[string]string{"X-Setup-Token": f.token})
	if rr.Code != http.StatusOK {
		t.Errorf("POST /v1/oidc-config/confirm with header token: got %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
}
