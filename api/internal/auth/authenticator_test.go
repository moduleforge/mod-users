package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// stubLiveness records its calls and returns res, err.
type stubLiveness struct {
	res   livenessResult
	err   error
	calls int
	slugs []string
	c     *carrier
}

func (s *stubLiveness) fn(_ context.Context, c *carrier, appSlugs []string) (livenessResult, error) {
	s.calls++
	s.c = c
	s.slugs = appSlugs
	return s.res, s.err
}

func liveSession() livenessResult {
	return livenessResult{
		Status: "ok", SessionID: 11, PrincipalID: 21, ProfileID: 31, PartyID: 41,
		AppID: 51, ACR: 2, AMR: []string{"pwd", "otp"}, AuthTime: time.Unix(1_700_000_000, 0),
		IsUser: true, EmailVerified: true, AudienceRegistered: true,
	}
}

func newTestAuthenticator(v *Verifier, cfg config.AuthConfig, live *stubLiveness) *Authenticator {
	if cfg.DefaultAudience == "" {
		cfg.DefaultAudience = "mod-users"
	}
	return &Authenticator{verifier: v, audience: newAudiencePolicy(cfg), liveness: live.fn, now: time.Now}
}

func bearerRequest(tok string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	return r
}

func issueSession(t *testing.T, s *Signer, mutate func(*SessionTokenParams)) (string, SessionTokenParams) {
	t.Helper()
	p := sessionParams()
	if mutate != nil {
		mutate(&p)
	}
	tok, _, err := IssueSessionToken(s, p, time.Now())
	if err != nil {
		t.Fatalf("IssueSessionToken: %v", err)
	}
	return tok, p
}

func TestAuthenticate_LiveSession(t *testing.T) {
	s, v, _ := newTestKeys(t)
	tok, p := issueSession(t, s, nil)
	live := &stubLiveness{res: liveSession()}
	a := newTestAuthenticator(v, config.AuthConfig{}, live)

	id, err := a.Authenticate(bearerRequest(tok))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if live.calls != 1 || live.slugs != nil {
		t.Errorf("liveness calls = %d, slugs = %v; want 1 call and no slug lookup", live.calls, live.slugs)
	}
	if live.c.SessionUUID != p.SessionUUID || live.c.PrincipalUUID != p.PrincipalUUID || live.c.ProfileUUID != p.ProfileUUID {
		t.Error("liveness did not receive the token's sid/sub/prof")
	}
	want := Identity{
		PrincipalID: 21, PrincipalUUID: p.PrincipalUUID, PrincipalKind: PrincipalKindUser,
		ProfileID: 31, ProfileUUID: p.ProfileUUID, PartyID: 41, SessionID: 11,
		SessionUUID: p.SessionUUID, AppID: 51, ACR: 2, AuthTime: time.Unix(1_700_000_000, 0),
		EmailVerified: true,
	}
	want.AMR = []string{"pwd", "otp"}
	if !reflect.DeepEqual(*id, want) {
		t.Errorf("Identity = %+v\nwant      %+v", *id, want)
	}
	// acr and auth_time come from the row, not the token.
	if id.ACR != 2 || !id.AuthTime.Equal(time.Unix(1_700_000_000, 0)) {
		t.Errorf("acr/auth_time not taken from the liveness row")
	}
}

func TestAuthenticate_ClientCredentials(t *testing.T) {
	s, v, _ := newTestKeys(t)
	now := time.Now()
	tok, _, err := IssueClientCredentialsToken(s, ClientCredentialsTokenParams{
		Issuer: testIssuer, Audience: "mod-users", PrincipalUUID: uuid.New(),
		CredentialUUID: uuid.New(), ProfileUUID: uuid.New(), ACR: 1, TTL: time.Minute,
	}, now)
	if err != nil {
		t.Fatalf("IssueClientCredentialsToken: %v", err)
	}
	live := &stubLiveness{res: livenessResult{
		Status: "ok", CredentialID: 9, PrincipalID: 8, ProfileID: 7, ACR: 1, AudienceRegistered: true,
	}}
	id, err := newTestAuthenticator(v, config.AuthConfig{}, live).Authenticate(bearerRequest(tok))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if id.PrincipalKind != PrincipalKindServiceAccount || id.CredentialID != 9 || id.SessionID != 0 {
		t.Errorf("unexpected identity %+v", id)
	}
	if len(id.AMR) != 1 || id.AMR[0] != "swk" || id.AuthTime.Unix() != now.Unix() {
		t.Errorf("amr/auth_time = %v/%v, want [swk]/issue time", id.AMR, id.AuthTime)
	}
}

func TestAuthenticate_Errors(t *testing.T) {
	s, v, _ := newTestKeys(t)
	tok, _ := issueSession(t, s, nil)
	appTok, _ := issueSession(t, s, func(p *SessionTokenParams) { p.Audience = "billing" })

	tests := []struct {
		name       string
		req        *http.Request
		cfg        config.AuthConfig
		live       *stubLiveness
		wantErr    error
		wantCalls  int
		wantStatus string
	}{
		{name: "no header", req: bearerRequest(""), live: &stubLiveness{}, wantErr: ErrNoAuthHeader},
		{name: "basic auth", req: func() *http.Request {
			r := bearerRequest("")
			r.Header.Set("Authorization", "Basic abc")
			return r
		}(), live: &stubLiveness{}, wantErr: ErrNoAuthHeader},
		{name: "garbage token", req: bearerRequest("x.y.z"), live: &stubLiveness{}, wantErr: ErrInvalidToken},
		{name: "revoked session", req: bearerRequest(tok),
			live:    &stubLiveness{res: livenessResult{Status: "session_revoked", AudienceRegistered: true}},
			wantErr: ErrNotLive, wantCalls: 1, wantStatus: "session_revoked"},
		{name: "locked profile", req: bearerRequest(tok),
			live:    &stubLiveness{res: livenessResult{Status: "confined", AudienceRegistered: true}},
			wantErr: ErrNotLive, wantCalls: 1, wantStatus: "confined"},
		{name: "unregistered app audience", req: bearerRequest(appTok),
			live:    &stubLiveness{res: livenessResult{Status: "ok", AudienceRegistered: false}},
			wantErr: ErrInvalidToken, wantCalls: 1},
		{name: "accepted audiences exclude token", req: bearerRequest(appTok),
			cfg: config.AuthConfig{AcceptedAudiences: []string{"other"}}, live: &stubLiveness{},
			wantErr: ErrInvalidToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newTestAuthenticator(v, tt.cfg, tt.live).Authenticate(tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.live.calls != tt.wantCalls {
				t.Errorf("liveness calls = %d, want %d", tt.live.calls, tt.wantCalls)
			}
			var nl *NotLiveError
			if tt.wantStatus != "" && (!errors.As(err, &nl) || nl.Status != tt.wantStatus) {
				t.Errorf("NotLiveError status = %v, want %q", err, tt.wantStatus)
			}
		})
	}

	t.Run("app audience is looked up", func(t *testing.T) {
		live := &stubLiveness{res: liveSession()}
		if _, err := newTestAuthenticator(v, config.AuthConfig{}, live).Authenticate(bearerRequest(appTok)); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if len(live.slugs) != 1 || live.slugs[0] != "billing" {
			t.Errorf("slugs = %v, want [billing]", live.slugs)
		}
	})

	t.Run("database fault", func(t *testing.T) {
		live := &stubLiveness{err: errors.New("db down")}
		_, err := newTestAuthenticator(v, config.AuthConfig{}, live).Authenticate(bearerRequest(tok))
		if err == nil || errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrNotLive) {
			t.Fatalf("err = %v, want an internal error", err)
		}
	})
}

func TestNotLiveError_Code(t *testing.T) {
	for status, want := range map[string]string{
		"session_revoked": CodeSessionRevoked, "session_expired": CodeSessionRevoked,
		"parent_session_ended": CodeSessionRevoked, "principal_mismatch": CodeSessionRevoked,
		"credential_revoked": CodeSessionRevoked, "credential_expired": CodeSessionRevoked,
		"not_held": CodeProfileUnavailable, "acr_too_low": CodeProfileUnavailable,
		"provider_required": CodeProfileUnavailable,
	} {
		e := &NotLiveError{Status: status}
		if e.Code() != want {
			t.Errorf("Code(%q) = %q, want %q", status, e.Code(), want)
		}
		if (want == CodeProfileUnavailable) != (e.LockReason() == status) {
			t.Errorf("LockReason(%q) = %q", status, e.LockReason())
		}
	}
}

// recorder captures the context the wrapped handler saw.
type recorder struct {
	called bool
	ctx    context.Context
}

func (h *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	h.ctx = r.Context()
	w.WriteHeader(http.StatusNoContent)
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Details []struct {
			Field string `json:"field"`
			Code  string `json:"code"`
		} `json:"details"`
	} `json:"error"`
	Action struct {
		Code string `json:"code"`
	} `json:"action"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var b errorBody
	if err := json.NewDecoder(rec.Body).Decode(&b); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return b
}

func TestNewRequireAuth(t *testing.T) {
	s, v, _ := newTestKeys(t)
	tok, _ := issueSession(t, s, nil)

	t.Run("live installs Identity and AuthContext", func(t *testing.T) {
		res := liveSession()
		res.ImpersonatorPrincipalID = 99
		next := &recorder{}
		rec := httptest.NewRecorder()
		NewRequireAuth(newTestAuthenticator(v, config.AuthConfig{}, &stubLiveness{res: res}))(next).
			ServeHTTP(rec, bearerRequest(tok))

		if !next.called {
			t.Fatalf("next not called; status %d", rec.Code)
		}
		id, ok := IdentityFromContext(next.ctx)
		if !ok || id.ProfileID != 31 {
			t.Fatalf("Identity missing or wrong: %+v", id)
		}
		ac, ok := opctx.Auth(next.ctx)
		want := opctx.AuthContext{PrincipalID: 21, ProfileID: 31, ImpersonatorPrincipalID: 99,
			SessionID: 11, ACR: 2, AuthTime: time.Unix(1_700_000_000, 0)}
		if !ok || ac != want {
			t.Errorf("AuthContext = %+v, want %+v", ac, want)
		}
		if IsAnonymousActor(next.ctx) {
			t.Error("authenticated request must not be anonymous")
		}
	})

	cases := []struct {
		name       string
		req        *http.Request
		live       *stubLiveness
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{"missing header", bearerRequest(""), &stubLiveness{}, http.StatusUnauthorized, "unauthorized", ""},
		{"revoked", bearerRequest(tok), &stubLiveness{res: livenessResult{Status: "session_expired", AudienceRegistered: true}},
			http.StatusUnauthorized, CodeSessionRevoked, ""},
		{"profile locked", bearerRequest(tok), &stubLiveness{res: livenessResult{Status: "acr_too_low", AudienceRegistered: true}},
			http.StatusUnauthorized, CodeProfileUnavailable, "acr_too_low"},
		{"db fault", bearerRequest(tok), &stubLiveness{err: errors.New("boom")},
			http.StatusInternalServerError, "internal_error", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := &recorder{}
			rec := httptest.NewRecorder()
			NewRequireAuth(newTestAuthenticator(v, config.AuthConfig{}, tc.live))(next).ServeHTTP(rec, tc.req)
			if next.called {
				t.Fatal("next must not be called")
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			b := decodeError(t, rec)
			if b.Error.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", b.Error.Code, tc.wantCode)
			}
			if tc.wantDetail != "" && (len(b.Error.Details) != 1 || b.Error.Details[0].Code != tc.wantDetail) {
				t.Errorf("details = %+v, want lock reason %q", b.Error.Details, tc.wantDetail)
			}
		})
	}
}

func TestNewResolveOrAnonymous(t *testing.T) {
	s, v, _ := newTestKeys(t)
	tok, _ := issueSession(t, s, nil)
	anon := &AnonymousActor{principalID: 5, profileID: 6}

	t.Run("absent header is anonymous", func(t *testing.T) {
		live := &stubLiveness{}
		next := &recorder{}
		NewResolveOrAnonymous(newTestAuthenticator(v, config.AuthConfig{}, live), anon)(next).
			ServeHTTP(httptest.NewRecorder(), bearerRequest(""))
		if !next.called {
			t.Fatal("next not called")
		}
		ac, ok := opctx.Auth(next.ctx)
		if !ok || ac != (opctx.AuthContext{PrincipalID: 5, ProfileID: 6, Anonymous: true}) {
			t.Errorf("AuthContext = %+v", ac)
		}
		if !IsAnonymousActor(next.ctx) {
			t.Error("IsAnonymousActor should be true")
		}
		if _, ok := IdentityFromContext(next.ctx); ok {
			t.Error("no Identity on the anonymous branch")
		}
		if live.calls != 0 {
			t.Error("no liveness query for an anonymous request")
		}
	})

	t.Run("malformed header is 401", func(t *testing.T) {
		r := bearerRequest("")
		r.Header.Set("Authorization", "Token abc")
		next := &recorder{}
		rec := httptest.NewRecorder()
		NewResolveOrAnonymous(newTestAuthenticator(v, config.AuthConfig{}, &stubLiveness{}), anon)(next).ServeHTTP(rec, r)
		if next.called || rec.Code != http.StatusUnauthorized {
			t.Errorf("called=%v status=%d, want 401", next.called, rec.Code)
		}
	})

	t.Run("revoked session never downgrades", func(t *testing.T) {
		next := &recorder{}
		rec := httptest.NewRecorder()
		live := &stubLiveness{res: livenessResult{Status: "session_revoked", AudienceRegistered: true}}
		NewResolveOrAnonymous(newTestAuthenticator(v, config.AuthConfig{}, live), anon)(next).ServeHTTP(rec, bearerRequest(tok))
		if next.called || rec.Code != http.StatusUnauthorized {
			t.Errorf("called=%v status=%d, want 401", next.called, rec.Code)
		}
	})

	t.Run("valid token authenticates", func(t *testing.T) {
		next := &recorder{}
		NewResolveOrAnonymous(newTestAuthenticator(v, config.AuthConfig{}, &stubLiveness{res: liveSession()}), anon)(next).
			ServeHTTP(httptest.NewRecorder(), bearerRequest(tok))
		if _, ok := IdentityFromContext(next.ctx); !ok || IsAnonymousActor(next.ctx) {
			t.Error("expected an authenticated, non-anonymous context")
		}
	})
}

func TestNewRequireVerifiedEmail(t *testing.T) {
	mw := NewRequireVerifiedEmail()
	run := func(ctx context.Context) (*recorder, *httptest.ResponseRecorder) {
		next := &recorder{}
		rec := httptest.NewRecorder()
		mw(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil).WithContext(ctx))
		return next, rec
	}

	if next, rec := run(context.Background()); next.called || rec.Code != http.StatusInternalServerError {
		t.Errorf("missing Identity: called=%v status=%d, want 500", next.called, rec.Code)
	}
	if next, rec := run(WithIdentity(context.Background(), &Identity{ProfileID: 1})); next.called || rec.Code != http.StatusForbidden {
		t.Errorf("unverified: called=%v status=%d, want 403", next.called, rec.Code)
	} else if b := decodeError(t, rec); b.Action.Code != "users.email_unverified" {
		t.Errorf("action code = %q", b.Action.Code)
	}
	if next, _ := run(WithIdentity(context.Background(), &Identity{ProfileID: 1, EmailVerified: true})); !next.called {
		t.Error("verified: next not called")
	}
}

func TestNewAnonymousActor(t *testing.T) {
	okLookup := func(_ context.Context, slug string) (coredb.GetSystemActorBySlugRow, error) {
		if slug != anonymousActorSlug {
			t.Errorf("slug = %q", slug)
		}
		return coredb.GetSystemActorBySlugRow{EntityID: 70, Slug: slug, PersonalProfileID: 71}, nil
	}

	t.Run("resolves principal and profile", func(t *testing.T) {
		var checked []int64
		a, err := newAnonymousActor(context.Background(), okLookup, func(_ context.Context, ids []int64) (bool, error) {
			checked = ids
			return false, nil
		})
		if err != nil {
			t.Fatalf("newAnonymousActor: %v", err)
		}
		if a.PrincipalID() != 70 || a.ProfileID() != 71 {
			t.Errorf("ids = %d/%d, want 70/71", a.PrincipalID(), a.ProfileID())
		}
		if len(checked) != 2 || checked[0] != 70 || checked[1] != 71 {
			t.Errorf("grant check ids = %v, want [70 71]", checked)
		}
	})
	t.Run("holding a grant refuses to start", func(t *testing.T) {
		if _, err := newAnonymousActor(context.Background(), okLookup,
			func(context.Context, []int64) (bool, error) { return true, nil }); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("grant check error fails closed", func(t *testing.T) {
		if _, err := newAnonymousActor(context.Background(), okLookup,
			func(context.Context, []int64) (bool, error) { return false, errors.New("boom") }); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("missing actor", func(t *testing.T) {
		_, err := newAnonymousActor(context.Background(),
			func(context.Context, string) (coredb.GetSystemActorBySlugRow, error) {
				return coredb.GetSystemActorBySlugRow{}, pgx.ErrNoRows
			},
			func(context.Context, []int64) (bool, error) {
				t.Error("grant check must not run")
				return false, nil
			})
		if err == nil {
			t.Fatal("expected an error")
		}
	})
}
