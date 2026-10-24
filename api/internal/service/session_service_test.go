package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

func TestLockFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, reason, status, provider string
		wantRemedy                     string
		wantProvider                   string
	}{
		{"provider required carries the provider", "provider_required", "active", "okta", RemedySignInWithProvider, "okta"},
		{"provider required without a provider has no remedy", "provider_required", "active", "", "", ""},
		{"acr too low asks for a step-up", "acr_too_low", "active", "", RemedyStepUp, ""},
		{"unauthorized credential asks for another credential", "credential_not_authorized", "active", "", RemedySignInWithAuthorizedCredential, ""},
		{"an invited profile can be accepted", "inactive", "invited", "", RemedyAcceptInvitation, ""},
		{"a suspended profile has no remedy", "inactive", "suspended", "", "", ""},
		{"not held has no remedy", "not_held", "", "", "", ""},
		{"principal disabled has no remedy", "principal_disabled", "active", "", "", ""},
		{"confined has no remedy", "confined", "active", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lock := lockFor(tc.reason, tc.status, tc.provider)
			if lock.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", lock.Reason, tc.reason)
			}
			switch {
			case tc.wantRemedy == "" && lock.Remedy != nil:
				t.Errorf("remedy = %+v, want none", lock.Remedy)
			case tc.wantRemedy != "" && (lock.Remedy == nil || lock.Remedy.Kind != tc.wantRemedy || lock.Remedy.ProviderID != tc.wantProvider):
				t.Errorf("remedy = %+v, want %s/%s", lock.Remedy, tc.wantRemedy, tc.wantProvider)
			}
		})
	}
}

func TestUnavailableError(t *testing.T) {
	t.Parallel()

	var gate *AuthError
	if err := unavailableError(ProfileView{Lock: &ProfileLock{Reason: LockAppLoginNotGranted}}); !errors.Is(err, ErrAppLoginNotGranted) {
		t.Errorf("gate-locked profile: err = %v, want ErrAppLoginNotGranted", err)
	} else if !errors.As(err, &gate) || gate.Status != http.StatusForbidden {
		t.Errorf("gate refusal = %+v, want 403", gate)
	}

	for name, view := range map[string]ProfileView{
		"locked":    {Lock: &ProfileLock{Reason: "acr_too_low"}},
		"no lock":   {},
		"suspended": {Lock: &ProfileLock{Reason: "inactive"}},
	} {
		err := unavailableError(view)
		var ae *AuthError
		if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized || ae.Code != CodeProfileUnavailable {
			t.Fatalf("%s: err = %v, want 401 %s", name, err, CodeProfileUnavailable)
		}
		want := "not_held"
		if view.Lock != nil {
			want = view.Lock.Reason
		}
		if len(ae.Details) != 1 || ae.Details[0].Code != want || ae.Details[0].Field != "profile" {
			t.Errorf("%s: details = %+v, want one profile detail with code %q", name, ae.Details, want)
		}
	}
}

func TestFindViewAndAvailability(t *testing.T) {
	t.Parallel()
	a, b := uuid.New(), uuid.New()
	views := []ProfileView{
		{UUID: a, id: 10, Available: false, Lock: &ProfileLock{Reason: LockAppLoginNotGranted}},
		{UUID: b, id: 20, Available: true},
	}
	if v, ok := findView(views, b); !ok || v.id != 20 {
		t.Errorf("findView(b) = %+v %v", v, ok)
	}
	if _, ok := findView(views, uuid.New()); ok {
		t.Error("findView found an unknown uuid")
	}
	if v, ok := findViewByID(views, 10); !ok || v.UUID != a {
		t.Errorf("findViewByID(10) = %+v %v", v, ok)
	}
	if _, ok := findViewByID(views, 0); ok {
		t.Error("findViewByID(0) must never match: 0 means no default profile")
	}
	if !anyAvailable(views) || anyAvailable(views[:1]) {
		t.Error("anyAvailable is wrong")
	}
	if !anyGateLocked(views) || anyGateLocked(views[1:]) {
		t.Error("anyGateLocked is wrong")
	}
}

func TestSessionView(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("x", 3600))
	sess := usersdb.Session{
		Uuid: uuid.New(), Acr: 2, AuthTime: pgtype.Timestamptz{Time: now, Valid: true},
	}
	v := sessionView(sess)
	if v.UUID != sess.Uuid || v.ACR != 2 || v.Impersonating {
		t.Errorf("view = %+v", v)
	}
	if v.AMR == nil || len(v.AMR) != 0 {
		t.Errorf("nil amr must be an empty list for JSON, got %#v", v.AMR)
	}
	if v.AuthTime.Location() != time.UTC || !v.AuthTime.Equal(now) {
		t.Errorf("auth_time = %v, want %v in UTC", v.AuthTime, now)
	}
	sess.ImpersonatorSessionID = pgtype.Int8{Int64: 5, Valid: true}
	sess.Amr = []string{"pwd", "otp"}
	if v := sessionView(sess); !v.Impersonating || len(v.AMR) != 2 {
		t.Errorf("view = %+v, want impersonating with two amr values", v)
	}
}

func newTestSessionService(t *testing.T, cfg SessionConfig) *SessionService {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := localauth.NewEdDSASigner(key, "kid-1")
	if err != nil {
		t.Fatal(err)
	}
	return &SessionService{signer: signer, cfg: cfg, now: time.Now}
}

func TestTokenResponse(t *testing.T) {
	t.Parallel()
	cfg := SessionConfig{Issuer: "iss", DefaultAudience: "default-aud", AccessTokenTTL: 30 * time.Minute}
	s := newTestSessionService(t, cfg)
	now := time.Now()
	sc := sessionContext{
		sess: usersdb.Session{
			Uuid: uuid.New(), Acr: 1, Amr: []string{"pwd"},
			AuthTime:  pgtype.Timestamptz{Time: now, Valid: true},
			ExpiresAt: pgtype.Timestamptz{Time: now.Add(24 * time.Hour), Valid: true},
		},
		principalUUID: uuid.New(),
	}
	profile := ProfileView{UUID: uuid.New(), Kind: ProfileKindPersonal, Available: true}
	refresh := "mfr_x"

	t.Run("a landed profile gets an access token", func(t *testing.T) {
		t.Parallel()
		resp, err := s.tokenResponse(sc, &profile, nil, &refresh)
		if err != nil {
			t.Fatal(err)
		}
		if resp.AccessToken == nil || resp.Profile == nil || resp.SelectionRequired || resp.TokenType != "Bearer" {
			t.Fatalf("response = %+v", resp)
		}
		if resp.RefreshToken == nil || *resp.RefreshToken != refresh {
			t.Errorf("refresh token = %v", resp.RefreshToken)
		}
		if resp.Profiles == nil || len(resp.Profiles) != 0 {
			t.Errorf("profiles must be an empty list, not null: %#v", resp.Profiles)
		}
		if resp.ExpiresIn < 29*60 || resp.ExpiresIn > 30*60 {
			t.Errorf("expires_in = %d, want about the access token ttl", resp.ExpiresIn)
		}
		claims := parseUnverified(t, *resp.AccessToken)
		if claims["aud"] != "default-aud" || claims["iss"] != "iss" || claims["sid"] != sc.sess.Uuid.String() ||
			claims["prof"] != profile.UUID.String() || claims["sub"] != sc.principalUUID.String() || claims["acr"] != "1" {
			t.Errorf("claims = %v", claims)
		}
	})

	t.Run("an app's slug is the audience", func(t *testing.T) {
		t.Parallel()
		withApp := sc
		withApp.appSlug = "my-app"
		resp, err := s.tokenResponse(withApp, &profile, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if aud := parseUnverified(t, *resp.AccessToken)["aud"]; aud != "my-app" {
			t.Errorf("aud = %v, want my-app", aud)
		}
		if resp.RefreshToken != nil {
			t.Error("no refresh token was minted, so none may be returned")
		}
	})

	t.Run("no landed profile means selection is required and no token", func(t *testing.T) {
		t.Parallel()
		resp, err := s.tokenResponse(sc, nil, []ProfileView{profile}, &refresh)
		if err != nil {
			t.Fatal(err)
		}
		if resp.AccessToken != nil || resp.Profile != nil || !resp.SelectionRequired || len(resp.Profiles) != 1 || resp.ExpiresIn != 0 {
			t.Errorf("response = %+v", resp)
		}
		if resp.RefreshToken == nil {
			t.Error("a selection-required response must carry the refresh token the client chooses with")
		}
	})

	t.Run("the token never outlives the session", func(t *testing.T) {
		t.Parallel()
		short := sc
		short.sess.ExpiresAt = pgtype.Timestamptz{Time: now.Add(5 * time.Minute), Valid: true}
		resp, err := s.tokenResponse(short, &profile, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.ExpiresIn > 5*60 {
			t.Errorf("expires_in = %d, want at most the session's remaining five minutes", resp.ExpiresIn)
		}
	})

	t.Run("a service with no signer cannot issue", func(t *testing.T) {
		t.Parallel()
		bare := &SessionService{cfg: cfg, now: time.Now}
		if _, err := bare.tokenResponse(sc, &profile, nil, nil); err == nil {
			t.Fatal("tokenResponse without a signer succeeded")
		}
	})
}

func parseUnverified(t *testing.T, token string) map[string]any {
	t.Helper()
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		t.Fatalf("parse %q: %v", token, err)
	}
	return claims
}

func TestSessionConfigFrom(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		LocalAuth: config.LocalAuthConfig{LocalIssuer: "the-issuer"},
		Auth: config.AuthConfig{
			DefaultAudience: "aud", AccessTokenTTL: time.Hour, SessionMaxAge: 2 * time.Hour,
			SessionIdleTimeout: 3 * time.Hour, ClientCredentialsTTL: 4 * time.Minute,
		},
	}
	got := SessionConfigFrom(cfg)
	want := SessionConfig{
		Issuer: "the-issuer", DefaultAudience: "aud", AccessTokenTTL: time.Hour, SessionMaxAge: 2 * time.Hour,
		IdleTimeout: 3 * time.Hour, ClientCredentialsTTL: 4 * time.Minute,
	}
	if got != want {
		t.Errorf("SessionConfigFrom = %+v, want %+v", got, want)
	}
}

func TestAssertedACR(t *testing.T) {
	t.Parallel()
	for in, valid := range map[int16]bool{-1: false, 0: false, 1: true, 2: true, 3: true} {
		got := assertedACR(in)
		if got.Valid != valid || (valid && got.Int16 != in) {
			t.Errorf("assertedACR(%d) = %+v", in, got)
		}
	}
}
