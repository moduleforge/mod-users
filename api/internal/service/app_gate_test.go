package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/opctx"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

type loginAuthorizer struct {
	err    error
	op     string
	target *int64
	ac     opctx.AuthContext
	calls  int
}

func (a *loginAuthorizer) Authorize(ctx context.Context, op string, target *int64) error {
	a.calls++
	a.op, a.target = op, target
	a.ac, _ = opctx.Auth(ctx)
	return a.err
}

func gateSession(appID int64, inviteOnly bool) sessionContext {
	sess := usersdb.Session{ID: 11, PrincipalID: 21, Acr: 2}
	if appID > 0 {
		sess.AppID = pgtype.Int8{Int64: appID, Valid: true}
	}
	return sessionContext{sess: sess, inviteOnly: inviteOnly}
}

// The invite-only gate is Authorize(candidate profile, login, app entity id);
// an app that is not invite-only has no gate and costs no query.
func TestSessionGate_AuthorizesLoginOnTheApp(t *testing.T) {
	t.Parallel()

	az := &loginAuthorizer{}
	s := &SessionService{az: az}
	if g := s.gateFor(gateSession(51, false)); g != nil {
		t.Fatalf("an app that is not invite-only has a gate")
	}
	if g := s.gateFor(gateSession(0, false)); g != nil {
		t.Fatalf("a session without an app has a gate")
	}

	gate := s.gateFor(gateSession(51, true))
	if gate == nil {
		t.Fatal("an invite-only app has no gate")
	}
	ok, err := gate(context.Background(), 31)
	if err != nil || !ok {
		t.Fatalf("gate = %v, %v; want admitted", ok, err)
	}
	if az.op != "login" || az.target == nil || *az.target != 51 {
		t.Errorf("Authorize(%q, %v), want login on the app entity 51 (never a nil target)", az.op, az.target)
	}
	if az.ac.ProfileID != 31 || az.ac.PrincipalID != 21 || az.ac.SessionID != 11 {
		t.Errorf("candidate AuthContext = %+v, want profile 31 of principal 21 in session 11", az.ac)
	}
}

func TestSessionGate_DenialAndFailureModes(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		az      *loginAuthorizer
		noAz    bool
		wantOK  bool
		wantErr bool
	}{
		"forbidden is a denial":       {az: &loginAuthorizer{err: apiresp.ErrForbidden}},
		"unauthenticated is denial":   {az: &loginAuthorizer{err: apiresp.ErrUnauthenticated}},
		"no authorizer admits nobody": {noAz: true},
		"an engine fault propagates":  {az: &loginAuthorizer{err: errors.New("db down")}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := &SessionService{}
			if !tc.noAz {
				s.az = tc.az
			}
			ok, err := s.gateFor(gateSession(51, true))(context.Background(), 31)
			if ok != tc.wantOK || (err != nil) != tc.wantErr {
				t.Fatalf("gate = %v, %v; want ok=%v err=%v", ok, err, tc.wantOK, tc.wantErr)
			}
		})
	}
}
