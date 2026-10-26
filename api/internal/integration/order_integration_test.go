//go:build integration

package integration

// Error-precedence tests (apiresp/ordertest) for the authenticated route
// groups, driven with mod-core AuthContext identities instead of bearer
// tokens: 401 for a request with no AuthContext, then 400 for a stateless
// shape error, then authorization and existence (403/404), with 409 only after
// authorization. ordertest.Anonymous keeps meaning "no AuthContext".
//
// The routes are mounted without the production RequireAuth (ordertest installs
// the identity on the request context directly); a stand-in installs the
// module's auth.Identity from the AuthContext, the way the real middleware
// derives both from the same live rows.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/moduleforge/core-api/apiresp/ordertest"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/handlers/me"
	"github.com/moduleforge/mod-users/api/internal/handlers/parties"
	"github.com/moduleforge/mod-users/api/internal/handlers/principals"
)

// identityFromAuthContext installs the module's auth.Identity for the
// AuthContext ordertest put on the request. It does nothing for a request
// without one, so authz.RequireAuth (mounted before it) answers 401.
func (s *stack) identityFromAuthContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac, ok := opctx.Auth(r.Context())
		if !ok || ac.Anonymous {
			next.ServeHTTP(w, r)
			return
		}
		id := &auth.Identity{
			PrincipalID:   ac.PrincipalID,
			PrincipalKind: auth.PrincipalKindUser,
			ProfileID:     ac.ProfileID,
			SessionID:     ac.SessionID,
			ACR:           ac.ACR,
			AuthTime:      ac.AuthTime,
		}
		var sessionUUID, principalUUID, profileUUID string
		if err := s.pool.QueryRow(r.Context(), `
SELECT coalesce((SELECT uuid::text FROM mod_users.sessions WHERE id = $1), ''),
       (SELECT uuid::text FROM entities WHERE id = $2),
       (SELECT uuid::text FROM entities WHERE id = $3)`, ac.SessionID, ac.PrincipalID, ac.ProfileID).
			Scan(&sessionUUID, &principalUUID, &profileUUID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		id.SessionUUID, id.PrincipalUUID, id.ProfileUUID = parseUUID(sessionUUID), parseUUID(principalUUID), parseUUID(profileUUID)
		next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), id)))
	})
}

// orderRouter mounts the /v1/me and /v1/principals groups behind the
// stand-in.
func (s *stack) orderRouter() http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/me", func(r chi.Router) {
		r.Use(coreAuthz.RequireAuth, s.identityFromAuthContext)
		me.RegisterMeRoutes(r, me.NewMeHandler(s.users, s.profiles))
	})
	r.Route("/v1/principals", func(r chi.Router) {
		r.Use(coreAuthz.RequireAuth, s.identityFromAuthContext)
		principals.RegisterPrincipalRoutes(r, principals.NewPrincipalsHandler(s.users))
	})
	partiesH := parties.NewPartiesHandler(s.profiles, s.partyPolicy)
	r.Route("/v1/parties", func(r chi.Router) {
		r.Use(coreAuthz.RequireAuth, s.identityFromAuthContext)
		parties.RegisterPartyRoutes(r, partiesH)
	})
	r.Route("/v1/profiles", func(r chi.Router) {
		r.Use(coreAuthz.RequireAuth, s.identityFromAuthContext)
		parties.RegisterProfileRoutes(r, partiesH)
	})
	return r
}

// asSession is the ordertest identity of a live session of an account: the
// AuthContext its middleware would install, with a fresh auth_time so
// self-service writes pass the freshness gate.
func (s *stack) asSession(t *testing.T, a account, profileID int64) ordertest.Identity {
	t.Helper()
	uid := s.principalID(t, a.Email)
	sessionID := s.scanInt64(t, "session id", `SELECT id FROM mod_users.sessions WHERE uuid = $1::uuid`, a.tk.Session.UUID)
	return func(ctx context.Context) context.Context {
		return opctx.WithAuthContext(ctx, opctx.AuthContext{
			PrincipalID: uid, ProfileID: profileID, SessionID: sessionID, ACR: 1, AuthTime: time.Now(),
		})
	}
}

func TestOrder_PrincipalsRoutes(t *testing.T) {
	h := env.orderRouter()
	admin := env.registerVerified(t)
	_, opProfile := env.makeOperator(t, admin)
	operator := env.asSession(t, admin, opProfile)

	user := env.registerVerified(t)
	userProfile := env.personalProfileID(t, env.principalID(t, user.Email))
	ordinary := env.asSession(t, user, userProfile)

	target := func() string {
		return "/v1/principals/" + env.entityUUID(t, env.principalID(t, env.registerVerified(t).Email))
	}
	missing := "/v1/principals/" + "22222222-2222-2222-2222-222222222222"

	ordertest.Run(t,
		ordertest.Route{
			Name: "GET /v1/principals/{uuid}", Handler: h, Method: http.MethodGet,
			ExistingPath: target(), MissingPath: missing,
			Authorized: operator,
		},
		ordertest.Route{
			Name: "POST /v1/principals/{uuid}/disable", Handler: h, Method: http.MethodPost,
			ExistingPath: target() + "/disable", MissingPath: missing + "/disable",
			GoodBody: []byte(`{"reason":"order test"}`), BadBody: []byte(`{"reason":`),
			Authorized: operator,
		},
		ordertest.Route{
			Name: "POST /v1/principals/{uuid}/enable", Handler: h, Method: http.MethodPost,
			ExistingPath: target() + "/enable", MissingPath: missing + "/enable",
			Authorized: operator,
		},
	)

	// Authorization masks existence: a caller who may not read or update a
	// principal gets the same 404 for a principal that exists and one that does
	// not, and a stateless shape error is still 400 before that.
	existing := target()
	for _, c := range []struct {
		name, method, suffix string
		body                 string
	}{
		{"read", http.MethodGet, "", ""},
		{"disable", http.MethodPost, "/disable", `{"reason":"x"}`},
		{"enable", http.MethodPost, "/enable", ""},
	} {
		t.Run("unauthorized "+c.name+" is masked", func(t *testing.T) {
			var body []byte
			if c.body != "" {
				body = []byte(c.body)
			}
			rt := ordertest.Route{Method: c.method, Handler: h, Name: c.name}
			_ = rt
			ex := doAs(t, h, ordinary, c.method, existing+c.suffix, body)
			mi := doAs(t, h, ordinary, c.method, missing+c.suffix, body)
			if ex.status != http.StatusNotFound || mi.status != http.StatusNotFound {
				t.Fatalf("existing = %d, missing = %d, want 404 for both; %s | %s", ex.status, mi.status, ex.raw, mi.raw)
			}
			if string(ex.raw) != string(mi.raw) {
				t.Errorf("a refused read of an existing principal is distinguishable from a missing one:\n%s\n%s", ex.raw, mi.raw)
			}
		})
	}
	t.Run("unauthorized malformed body is 400 before authorization", func(t *testing.T) {
		r := doAs(t, h, ordinary, http.MethodPost, existing+"/disable", []byte(`{"reason":`))
		if r.status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", r.status, r.raw)
		}
	})
	t.Run("malformed path uuid is 400 for an authenticated caller and 401 for none", func(t *testing.T) {
		if r := doAs(t, h, ordinary, http.MethodGet, "/v1/principals/not-a-uuid", nil); r.status != http.StatusBadRequest {
			t.Errorf("authenticated: %d, want 400", r.status)
		}
		if r := doAs(t, h, ordertest.Anonymous, http.MethodGet, "/v1/principals/not-a-uuid", nil); r.status != http.StatusUnauthorized {
			t.Errorf("anonymous: %d, want 401", r.status)
		}
	})
	t.Run("list shows an ordinary user only itself and an operator everyone", func(t *testing.T) {
		if r := doAs(t, h, ordertest.Anonymous, http.MethodGet, "/v1/principals", nil); r.status != http.StatusUnauthorized {
			t.Fatalf("anonymous list = %d, want 401", r.status)
		}
		ord := doAs(t, h, ordinary, http.MethodGet, "/v1/principals", nil)
		if ord.status != http.StatusOK {
			t.Fatalf("ordinary list = %d: %s", ord.status, ord.raw)
		}
		var page struct {
			Principals []map[string]any `json:"principals"`
			Total      int64            `json:"total"`
		}
		ord.into(t, &page)
		// A user owns itself, so the row scope returns exactly its own record.
		if page.Total != 1 || len(page.Principals) != 1 || page.Principals[0]["uuid"] != env.entityUUID(t, env.principalID(t, user.Email)) {
			t.Errorf("an ordinary user listed %+v, want only itself", page)
		}
		op := doAs(t, h, operator, http.MethodGet, "/v1/principals?kind=user&limit=5", nil)
		op.wantStatus(t, http.StatusOK).into(t, &page)
		if page.Total == 0 || len(page.Principals) == 0 || len(page.Principals) > 5 {
			t.Errorf("operator list = %d of %d, want a non-empty page of at most 5", len(page.Principals), page.Total)
		}
		doAs(t, h, operator, http.MethodGet, "/v1/principals?kind=banana", nil).wantStatus(t, http.StatusBadRequest)
		doAs(t, h, operator, http.MethodGet, "/v1/principals?limit=-1", nil).wantStatus(t, http.StatusBadRequest)
	})
}

func TestOrder_MeRoutes(t *testing.T) {
	h := env.orderRouter()
	a := env.registerVerified(t)
	env.verifyRegisteredEmail(t, a)
	uid := env.principalID(t, a.Email)
	self := env.asSession(t, a, env.personalProfileID(t, uid))

	// Fresh objects per route, since the authorized calls change them.
	sshCred := func() string {
		v := env.do(t, http.MethodPost, "/v1/me/credentials/ssh-keys", a.tk.access(), map[string]string{"public_key": newSSHPublicKey(t)}).
			wantStatus(t, http.StatusCreated).obj(t)
		return v["uuid"].(string)
	}
	otherSession := func() string { return env.login(t, a).Session.UUID }
	identifier := func() string {
		v := env.do(t, http.MethodPost, "/v1/me/identifiers", a.tk.access(), map[string]string{"value": uniq("order") + "@example.test"}).
			wantStatus(t, http.StatusCreated).obj(t)
		return v["uuid"].(string)
	}
	missing := "33333333-3333-3333-3333-333333333333"

	ordertest.Run(t,
		ordertest.Route{
			Name: "PUT /v1/me", Handler: h, Method: http.MethodPut,
			ExistingPath: "/v1/me", MissingPath: "/v1/me", AuthorizedMissingStatus: http.StatusOK,
			GoodBody: []byte(`{"given_name":"Order"}`), BadBody: []byte(`{"given_name":`),
			Authorized: self,
		},
		ordertest.Route{
			Name: "PATCH /v1/me/credentials/{uuid}", Handler: h, Method: http.MethodPatch,
			ExistingPath: "/v1/me/credentials/" + sshCred(), MissingPath: "/v1/me/credentials/" + missing,
			GoodBody: []byte(`{"label":"ordered"}`), BadBody: []byte(`{}`),
			Authorized: self,
		},
		ordertest.Route{
			Name: "DELETE /v1/me/credentials/{uuid}", Handler: h, Method: http.MethodDelete,
			ExistingPath: "/v1/me/credentials/" + sshCred(), MissingPath: "/v1/me/credentials/" + missing,
			Authorized: self,
		},
		ordertest.Route{
			Name: "DELETE /v1/me/sessions/{uuid}", Handler: h, Method: http.MethodDelete,
			ExistingPath: "/v1/me/sessions/" + otherSession(), MissingPath: "/v1/me/sessions/" + missing,
			Authorized: self,
		},
		ordertest.Route{
			Name: "POST /v1/me/identifiers", Handler: h, Method: http.MethodPost,
			ExistingPath: "/v1/me/identifiers", MissingPath: "/v1/me/identifiers", AuthorizedMissingStatus: http.StatusCreated,
			GoodBody: []byte(`{"value":"` + uniq("order") + `@example.test"}`), BadBody: []byte(`{"value":`),
			Authorized: self,
			Late: []ordertest.Late{
				{Name: "email taken", Path: "/v1/me/identifiers", Body: []byte(`{"value":"` + a.Email + `"}`), WantStatus: http.StatusConflict},
			},
		},
		ordertest.Route{
			Name: "PUT /v1/me/identifiers/{uuid}/primary", Handler: h, Method: http.MethodPut,
			ExistingPath: "/v1/me/identifiers/" + env.identifierUUID(t, a.tk.access(), a.Email) + "/primary",
			MissingPath:  "/v1/me/identifiers/" + missing + "/primary",
			Authorized:   self,
		},
		ordertest.Route{
			Name: "DELETE /v1/me/identifiers/{uuid}", Handler: h, Method: http.MethodDelete,
			ExistingPath: "/v1/me/identifiers/" + identifier(), MissingPath: "/v1/me/identifiers/" + missing,
			Authorized: self,
		},
		ordertest.Route{
			Name: "GET /v1/me/credentials", Handler: h, Method: http.MethodGet,
			ExistingPath: "/v1/me/credentials", MissingPath: "/v1/me/credentials", AuthorizedMissingStatus: http.StatusOK,
			Authorized: self,
		},
	)

	// Self-service authority is structural: another user's object is the same
	// 404 as a missing one (never a distinguishable 403), and a stale session
	// gets the step-up action only after the object check passes shape.
	other := env.registerVerified(t)
	otherCred := env.do(t, http.MethodPost, "/v1/me/credentials/ssh-keys", other.tk.access(), map[string]string{"public_key": newSSHPublicKey(t)}).
		wantStatus(t, http.StatusCreated).obj(t)["uuid"].(string)
	for _, c := range []struct{ method, path string }{
		{http.MethodDelete, "/v1/me/credentials/"}, {http.MethodPatch, "/v1/me/credentials/"},
	} {
		body := []byte(nil)
		if c.method == http.MethodPatch {
			body = []byte(`{"label":"stolen"}`)
		}
		foreign := doAs(t, h, self, c.method, c.path+otherCred, body)
		absent := doAs(t, h, self, c.method, c.path+missing, body)
		if foreign.status != http.StatusNotFound || absent.status != http.StatusNotFound || string(foreign.raw) != string(absent.raw) {
			t.Errorf("%s %s: foreign = %d %s, missing = %d %s; want identical 404s", c.method, c.path, foreign.status, foreign.raw, absent.status, absent.raw)
		}
	}
	if r := doAs(t, h, self, http.MethodDelete, "/v1/me/sessions/"+other.tk.Session.UUID, nil); r.status != http.StatusNotFound {
		t.Errorf("revoking another principal's session = %d, want 404", r.status)
	}
}

// doAs issues a request through h with the identity installed.
func doAs(t *testing.T, h http.Handler, id ordertest.Identity, method, path string, body []byte) *resp {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytesReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req = req.WithContext(id(req.Context()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return &resp{status: rec.Code, header: rec.Header(), raw: rec.Body.Bytes()}
}

func TestOrder_PartyRoutes(t *testing.T) {
	h := env.orderRouter()
	f := env.newParty(t)
	operator := env.asSession(t, f.admin, f.adminProf)
	missing := "22222222-2222-2222-2222-222222222222"

	freshHolder := func() string { return env.registerVerified(t).Email }
	invitedEmail := freshHolder()
	env.inviteEmail(t, f, invitedEmail).wantStatus(t, http.StatusCreated)
	takenEmail := env.registerVerified(t).Email

	activeProfile := func() string {
		uid := env.principalID(t, env.registerVerified(t).Email)
		_, id := env.seedPartyProfile(t, uid, f.partyID, "active")
		return id
	}
	endedProfile := func() string {
		uid := env.principalID(t, env.registerVerified(t).Email)
		pid, id := env.seedPartyProfile(t, uid, f.partyID, "active")
		env.exec(t, "end profile", `UPDATE profiles SET status = 'ended', ended_at = now(), end_reason = 'seeded' WHERE entity_id = $1`, pid)
		return id
	}

	ordertest.Run(t,
		ordertest.Route{
			Name: "GET /v1/parties/{uuid}/profiles", Handler: h, Method: http.MethodGet,
			ExistingPath: f.profilesPath(), MissingPath: "/v1/parties/" + missing + "/profiles",
			Authorized: operator,
		},
		ordertest.Route{
			Name: "POST /v1/parties/{uuid}/profiles", Handler: h, Method: http.MethodPost,
			ExistingPath: f.profilesPath(), MissingPath: "/v1/parties/" + missing + "/profiles",
			GoodBody: []byte(`{"holder_email":"` + freshHolder() + `"}`), BadBody: []byte(`{"holder_email":`),
			Authorized: operator,
			Late: []ordertest.Late{
				{Name: "profile exists", Path: f.profilesPath(), Body: []byte(`{"holder_email":"` + invitedEmail + `"}`), WantStatus: http.StatusConflict},
			},
		},
		ordertest.Route{
			Name: "POST /v1/parties/{uuid}/managed-users", Handler: h, Method: http.MethodPost,
			ExistingPath: "/v1/parties/" + f.partyUUID + "/managed-users", MissingPath: "/v1/parties/" + missing + "/managed-users",
			GoodBody: []byte(`{"email":"` + uniq("order") + `@example.test","password":"` + testPassword + `"}`), BadBody: []byte(`{"email":`),
			Authorized: operator,
			Late: []ordertest.Late{
				{Name: "email taken", Path: "/v1/parties/" + f.partyUUID + "/managed-users",
					Body: []byte(`{"email":"` + takenEmail + `","password":"` + testPassword + `"}`), WantStatus: http.StatusConflict},
			},
		},
		ordertest.Route{
			Name: "PATCH /v1/profiles/{uuid}", Handler: h, Method: http.MethodPatch,
			ExistingPath: "/v1/profiles/" + activeProfile(), MissingPath: "/v1/profiles/" + missing,
			GoodBody: []byte(`{"status":"suspended"}`), BadBody: []byte(`{"status":`),
			Authorized: operator,
			Late: []ordertest.Late{
				{Name: "ended is terminal", Path: "/v1/profiles/" + endedProfile(), Body: []byte(`{"status":"active"}`), WantStatus: http.StatusConflict},
			},
		},
	)
}

func TestOrder_MeProfileRoutes(t *testing.T) {
	h := env.orderRouter()
	a := env.registerVerified(t)
	uid := env.principalID(t, a.Email)
	self := env.asSession(t, a, env.personalProfileID(t, uid))
	missing := "33333333-3333-3333-3333-333333333333"

	// A fresh party profile of the account per call, since the authorized calls change it.
	profile := func(status string) string {
		party := env.seedParty(t, "Order Party "+uniq("p"))
		_, id := env.seedPartyProfile(t, uid, party, status)
		return "/v1/me/profiles/" + id
	}

	ordertest.Run(t,
		ordertest.Route{
			Name: "POST /v1/me/profiles/{uuid}/accept", Handler: h, Method: http.MethodPost,
			ExistingPath: profile("invited") + "/accept", MissingPath: "/v1/me/profiles/" + missing + "/accept",
			Authorized: self,
			Late: []ordertest.Late{
				{Name: "not an invitation", Path: profile("active") + "/accept", WantStatus: http.StatusConflict},
			},
		},
		ordertest.Route{
			Name: "POST /v1/me/profiles/{uuid}/decline", Handler: h, Method: http.MethodPost,
			ExistingPath: profile("invited") + "/decline", MissingPath: "/v1/me/profiles/" + missing + "/decline",
			Authorized: self,
			Late: []ordertest.Late{
				{Name: "not an invitation", Path: profile("active") + "/decline", WantStatus: http.StatusConflict},
			},
		},
		ordertest.Route{
			Name: "POST /v1/me/profiles/{uuid}/leave", Handler: h, Method: http.MethodPost,
			ExistingPath: profile("active") + "/leave", MissingPath: "/v1/me/profiles/" + missing + "/leave",
			Authorized: self,
			Late: []ordertest.Late{
				{Name: "still an invitation", Path: profile("invited") + "/leave", WantStatus: http.StatusConflict},
			},
		},
	)

	// Another account's profile is the same 404 as a missing one.
	other := env.registerVerified(t)
	foreign := profile("invited")
	otherSession := env.asSession(t, other, env.personalProfileID(t, env.principalID(t, other.Email)))
	for _, action := range []string{"/accept", "/decline", "/leave"} {
		ex := doAs(t, h, otherSession, http.MethodPost, foreign+action, nil)
		mi := doAs(t, h, otherSession, http.MethodPost, "/v1/me/profiles/"+missing+action, nil)
		if ex.status != http.StatusNotFound || mi.status != http.StatusNotFound || string(ex.raw) != string(mi.raw) {
			t.Errorf("%s: foreign = %d %s, missing = %d %s; want identical 404s", action, ex.status, ex.raw, mi.status, mi.raw)
		}
	}
	// A malformed uuid is 400 for the authenticated and 401 for none.
	if r := doAs(t, h, self, http.MethodPost, "/v1/me/profiles/not-a-uuid/accept", nil); r.status != http.StatusBadRequest {
		t.Errorf("malformed uuid = %d, want 400", r.status)
	}
	if r := doAs(t, h, ordertest.Anonymous, http.MethodPost, "/v1/me/profiles/not-a-uuid/accept", nil); r.status != http.StatusUnauthorized {
		t.Errorf("anonymous malformed uuid = %d, want 401", r.status)
	}
}
