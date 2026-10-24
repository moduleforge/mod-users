//go:build integration

package integration

// Apps: the audience of a session's access token follows its app, and an
// invite-only app admits only the profiles its gate admits.

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
)

// seedApp inserts an app and returns its entity id.
func (s *stack) seedApp(t *testing.T, slug string, inviteOnly bool) int64 {
	t.Helper()
	var id int64
	err := pgx.BeginFunc(context.Background(), s.pool, func(tx pgx.Tx) error {
		id = s.entityOf(t, tx, "app", nil)
		_, err := tx.Exec(context.Background(), `INSERT INTO apps (id, slug, name, invite_only) VALUES ($1, $2, $3, $4)`, id, slug, "App "+slug, inviteOnly)
		return err
	})
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	return id
}

func TestApp_AudienceFollowsTheSessionsApp(t *testing.T) {
	slug := uniq("app-")
	env.seedApp(t, slug, false)
	a := env.register(t)

	tk := env.loginRaw(t, a.Email, a.Password, map[string]string{"app": slug}).wantStatus(t, http.StatusOK).tok(t)
	if aud := claimsOf(t, tk.access())["aud"]; !audContains(aud, slug) {
		t.Fatalf("aud = %v, want the app slug %q", aud, slug)
	}
	// The slug of a registered app is an accepted audience with no explicit list.
	if status, _ := env.probe(t, "/v1/probe", tk.access()); status != http.StatusOK {
		t.Fatalf("token for a registered app = %d", status)
	}
	// The same session's switch and fork keep the app's audience.
	fk := env.do(t, http.MethodPost, "/v1/session/fork", tk.access(), nil).wantStatus(t, http.StatusOK).tok(t)
	if aud := claimsOf(t, fk.access())["aud"]; !audContains(aud, slug) {
		t.Errorf("forked aud = %v, want %q", aud, slug)
	}
	// No app: the default audience.
	plain := env.login(t, a)
	if aud := claimsOf(t, plain.access())["aud"]; !audContains(aud, "mod-users") {
		t.Errorf("aud without an app = %v, want mod-users", aud)
	}

	// An unknown or archived app is refused before any session exists.
	before := env.count(t, "sessions", `SELECT count(*) FROM mod_users.sessions`)
	env.loginRaw(t, a.Email, a.Password, map[string]string{"app": "no-such-app"}).wantError(t, http.StatusBadRequest, "invalid_input", "users.app_unknown")
	archived := uniq("archived-")
	appID := env.seedApp(t, archived, false)
	env.exec(t, "archive app", `UPDATE entities SET archived_at = now() WHERE id = $1`, appID)
	env.loginRaw(t, a.Email, a.Password, map[string]string{"app": archived}).wantError(t, http.StatusBadRequest, "invalid_input", "users.app_unknown")
	if got := env.count(t, "sessions", `SELECT count(*) FROM mod_users.sessions`); got != before {
		t.Errorf("a refused app created a session")
	}
}

func TestApp_InviteOnlyGate(t *testing.T) {
	slug := uniq("invite-")
	env.seedApp(t, slug, true)

	// An ordinary user holds no grant: every profile is gate-locked, so the
	// sign-in is refused outright.
	user := env.register(t)
	env.loginRaw(t, user.Email, user.Password, map[string]string{"app": slug}).
		wantError(t, http.StatusForbidden, "users.app_login_not_granted", "")
	// The same user signs in to a public app or no app.
	env.login(t, user)

	// An operator holds a wildcard grant on its operator-party profile: that
	// profile is admitted and is the only available one, so it is landed on.
	op := env.register(t)
	_, opProfileID := env.makeOperator(t, op)
	opProfile := env.profileUUID(t, opProfileID)
	personal := env.profileUUID(t, env.personalProfileID(t, env.principalID(t, op.Email)))

	tk := env.loginRaw(t, op.Email, op.Password, map[string]string{"app": slug}).wantStatus(t, http.StatusOK).tok(t)
	if tk.Profile == nil || tk.Profile.UUID != opProfile {
		t.Fatalf("landed on %+v, want the admitted operator profile %s", tk.Profile, opProfile)
	}
	for _, p := range tk.Profiles {
		switch p.UUID {
		case personal:
			if p.Available || p.Lock == nil || p.Lock.Reason != "app_login_not_granted" {
				t.Errorf("personal profile = %+v, want gate-locked with app_login_not_granted", p)
			}
		case opProfile:
			if !p.Available {
				t.Errorf("operator profile = %+v, want admitted", p)
			}
		}
	}
	// Switching to the gate-locked profile is refused with the gate's code.
	env.do(t, http.MethodPost, "/v1/session/switch", tk.access(), map[string]string{"profile_uuid": personal}).
		wantError(t, http.StatusForbidden, "users.app_login_not_granted", "")
}
