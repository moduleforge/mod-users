package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// gateAuthorizer records the last Authorize call and answers err.
type gateAuthorizer struct {
	err    error
	calls  int
	op     string
	target *int64
	ac     opctx.AuthContext
}

func (g *gateAuthorizer) Authorize(ctx context.Context, op string, target *int64) error {
	g.calls++
	g.op, g.target = op, target
	g.ac, _ = opctx.Auth(ctx)
	return g.err
}

func inviteOnlySession() livenessResult {
	res := liveSession()
	res.InviteOnly = true
	return res
}

func TestAuthenticate_InviteOnlyAppGate(t *testing.T) {
	s, v, _ := newTestKeys(t)
	tok, _ := issueSession(t, s, nil)

	t.Run("an app that is not invite-only never reaches the authorizer", func(t *testing.T) {
		az := &gateAuthorizer{err: apiresp.ErrForbidden}
		a := newTestAuthenticator(v, config.AuthConfig{}, &stubLiveness{res: liveSession()})
		a.SetAppGate(az)
		if _, err := a.Authenticate(bearerRequest(tok)); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if az.calls != 0 {
			t.Errorf("authorizer called %d times for a public app", az.calls)
		}
	})

	t.Run("a login grant on the app admits the acting profile", func(t *testing.T) {
		az := &gateAuthorizer{}
		a := newTestAuthenticator(v, config.AuthConfig{}, &stubLiveness{res: inviteOnlySession()})
		a.SetAppGate(az)
		if _, err := a.Authenticate(bearerRequest(tok)); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if az.calls != 1 || az.op != "login" || az.target == nil || *az.target != 51 {
			t.Errorf("authorizer got op %q target %v (%d calls), want login on app 51", az.op, az.target, az.calls)
		}
		if az.ac.ProfileID != 31 || az.ac.PrincipalID != 21 || az.ac.SessionID != 11 {
			t.Errorf("candidate AuthContext = %+v, want the acting profile 31 of principal 21 in session 11", az.ac)
		}
	})

	for name, build := range map[string]func() *Authenticator{
		"a denial": func() *Authenticator {
			a := newTestAuthenticator(v, config.AuthConfig{}, &stubLiveness{res: inviteOnlySession()})
			a.SetAppGate(&gateAuthorizer{err: apiresp.ErrForbidden})
			return a
		},
		"no authorizer": func() *Authenticator {
			return newTestAuthenticator(v, config.AuthConfig{}, &stubLiveness{res: inviteOnlySession()})
		},
	} {
		t.Run(name+" is profile_unavailable with the gate's lock reason", func(t *testing.T) {
			_, err := build().Authenticate(bearerRequest(tok))
			var nl *NotLiveError
			if !errors.Is(err, ErrNotLive) || !errors.As(err, &nl) {
				t.Fatalf("err = %v, want a NotLiveError", err)
			}
			if nl.Code() != CodeProfileUnavailable || nl.LockReason() != "app_login_not_granted" {
				t.Errorf("code %q reason %q, want profile_unavailable with app_login_not_granted", nl.Code(), nl.LockReason())
			}
		})
	}

	t.Run("an engine fault is not a denial", func(t *testing.T) {
		fault := errors.New("database down")
		a := newTestAuthenticator(v, config.AuthConfig{}, &stubLiveness{res: inviteOnlySession()})
		a.SetAppGate(&gateAuthorizer{err: fault})
		_, err := a.Authenticate(bearerRequest(tok))
		if !errors.Is(err, fault) || errors.Is(err, ErrNotLive) {
			t.Fatalf("err = %v, want the fault propagated (a 500), not a 401", err)
		}
	})
}
