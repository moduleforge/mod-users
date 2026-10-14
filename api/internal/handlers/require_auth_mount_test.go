package handlers

// Tests that every authenticated registrar enforces mod-core authz.RequireAuth
// itself (defense in depth), so 401 is the first rejection whatever the
// composer mounts around it.
//
// The registrars are mounted the way mfgen composes them: several registrars
// merged onto one shared router under a /v1 route, with handlers built as
// zero values (the 401 gate fires before any handler code runs). For the
// sudo-only case the handlers do run and either dereference nil dependencies
// or call auth.MustFromContext without a UserContext, so the test recovers
// handler panics (recoverToSentinel, shared with self_routes_test.go) instead
// of installing a UserContext: reaching the handler is reported as
// handlerReachedStatus, distinct from the 401 the gate writes.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	coreOpctx "github.com/moduleforge/core-api/opctx"
)

const requireAuthMountUUID = "11111111-1111-1111-1111-111111111111"

// requireAuthMountRouter mounts all 8 authenticated registrars onto one shared
// /v1 router, as mfgen does for merged manifest entries. recoverToSentinel
// stands in for the composer middleware; it deliberately does not populate
// opctx.
func requireAuthMountRouter() http.Handler {
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
	})
	return r
}

// requireAuthStandIn models the module's token-verifying requireAuth for
// tests that exercise routing and handler behavior rather than the 401 gate:
// it installs an opctx actor so the registrars' own RequireAuth passes.
func requireAuthStandIn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(coreOpctx.WithActor(r.Context(), 1)))
	})
}

type requireAuthMountRoute struct {
	method string
	// path is the route with every UUID parameter filled with a valid UUID.
	path string
	// badPath, when non-empty, is the same route with a malformed path UUID.
	badPath string
	// body marks routes that decode a JSON request body.
	body bool
}

func requireAuthMountRoutes() []requireAuthMountRoute {
	u, k := requireAuthMountUUID, requireAuthMountUUID
	return []requireAuthMountRoute{
		{method: "GET", path: "/user-accounts"},
		{method: "POST", path: "/user-accounts", body: true},
		{method: "GET", path: "/user-accounts/" + u, badPath: "/user-accounts/not-a-uuid"},
		{method: "PUT", path: "/user-accounts/" + u, badPath: "/user-accounts/not-a-uuid", body: true},
		{method: "DELETE", path: "/user-accounts/" + u, badPath: "/user-accounts/not-a-uuid"},
		{method: "POST", path: "/user-accounts/" + u + "/grant-admin", badPath: "/user-accounts/not-a-uuid/grant-admin"},
		{method: "POST", path: "/user-accounts/" + u + "/revoke-admin", badPath: "/user-accounts/not-a-uuid/revoke-admin"},
		{method: "POST", path: "/user-accounts/" + u + "/assume", badPath: "/user-accounts/not-a-uuid/assume"},
		{method: "POST", path: "/apps/" + u + "/user-accounts", badPath: "/apps/not-a-uuid/user-accounts", body: true},
		{method: "GET", path: "/apps/" + u + "/user-accounts", badPath: "/apps/not-a-uuid/user-accounts"},
		{method: "DELETE", path: "/apps/" + u + "/user-accounts/" + u, badPath: "/apps/" + u + "/user-accounts/not-a-uuid"},
		{method: "PUT", path: "/apps/" + u + "/user-accounts/" + u + "/roles", badPath: "/apps/" + u + "/user-accounts/not-a-uuid/roles", body: true},
		{method: "GET", path: "/self"},
		{method: "PUT", path: "/self", body: true},
		{method: "GET", path: "/self/identities"},
		{method: "POST", path: "/self/identities/oidc/google/start"},
		{method: "DELETE", path: "/self/identities/" + u, badPath: "/self/identities/not-a-uuid"},
		{method: "POST", path: "/self/credential/password", body: true},
		{method: "DELETE", path: "/self/credential/password"},
		{method: "POST", path: "/self/credential/step-up"},
		{method: "POST", path: "/self/credential/step-up/verify", body: true},
		{method: "GET", path: "/self/ssh-keys"},
		{method: "POST", path: "/self/ssh-keys", body: true},
		{method: "DELETE", path: "/self/ssh-keys/" + k, badPath: "/self/ssh-keys/not-a-uuid"},
		{method: "GET", path: "/user-accounts/" + u + "/ssh-keys", badPath: "/user-accounts/not-a-uuid/ssh-keys"},
		{method: "POST", path: "/user-accounts/" + u + "/ssh-keys", badPath: "/user-accounts/not-a-uuid/ssh-keys", body: true},
		{method: "DELETE", path: "/user-accounts/" + u + "/ssh-keys/" + k, badPath: "/user-accounts/not-a-uuid/ssh-keys/" + k},
	}
}

func requireAuthMountDo(t *testing.T, method, path, body string, sudoOnly bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/v1"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if sudoOnly {
		req = req.WithContext(coreOpctx.WithSudoActor(req.Context(), 42))
	}
	rec := httptest.NewRecorder()
	requireAuthMountRouter().ServeHTTP(rec, req)
	return rec
}

func requireAuthMountAssert401(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %q)", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (body %q)", err, rec.Body.String())
	}
	if env.Error.Code != "unauthenticated" {
		t.Fatalf("error.code = %q, want unauthenticated", env.Error.Code)
	}
}

func TestRequireAuthMount_AnonymousValidRequest_401(t *testing.T) {
	for _, rt := range requireAuthMountRoutes() {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			body := ""
			if rt.body {
				body = `{}`
			}
			requireAuthMountAssert401(t, requireAuthMountDo(t, rt.method, rt.path, body, false))
		})
	}
}

func TestRequireAuthMount_AnonymousMalformedBody_401(t *testing.T) {
	for _, rt := range requireAuthMountRoutes() {
		if !rt.body {
			continue
		}
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			requireAuthMountAssert401(t, requireAuthMountDo(t, rt.method, rt.path, `{not json`, false))
		})
	}
}

func TestRequireAuthMount_AnonymousMalformedPathUUID_401(t *testing.T) {
	for _, rt := range requireAuthMountRoutes() {
		if rt.badPath == "" {
			continue
		}
		t.Run(rt.method+" "+rt.badPath, func(t *testing.T) {
			body := ""
			if rt.body {
				body = `{}`
			}
			requireAuthMountAssert401(t, requireAuthMountDo(t, rt.method, rt.badPath, body, false))
		})
	}
}

// A sudo-only identity (opctx.WithSudoActor, no regular actor) counts as
// authenticated: the gate must let it through to the handler. Handler panics
// from the zero-value dependencies are recovered into handlerReachedStatus.
func TestRequireAuthMount_SudoOnlyIdentity_NotRejected(t *testing.T) {
	for _, rt := range requireAuthMountRoutes() {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			body := ""
			if rt.body {
				body = `{}`
			}
			rec := requireAuthMountDo(t, rt.method, rt.path, body, true)
			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("sudo-only identity got 401 (body %q)", rec.Body.String())
			}
		})
	}
}
