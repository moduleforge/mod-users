//go:build integration

package integration

// The party profile lifecycle over HTTP, against the real services, database
// triggers and the real mod-audit observer: invitations, the holder's accept,
// decline and leave, the administrator's suspend, resume and end, departure
// cleanup, managed users, lifecycle observers and the masking and error order
// of the routes. Party administration is reachable only by operator-party
// administrators until the authorization-engine phase (decision U27), so every
// administrator here is an operator-party profile with a wildcard grant
// (makeOperator).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// pgxTx abbreviates pgx.Tx.
type pgxTx = pgx.Tx

// inTx runs fn in a transaction on the pool.
func (s *stack) inTx(ctx context.Context, fn func(tx pgxTx) error) error {
	return pgx.BeginFunc(ctx, s.pool, fn)
}

// lifecycleRecorder is a ProfileLifecycleObserver that records every change and
// can be told to fail.
type lifecycleRecorder struct {
	mu      sync.Mutex
	changes []svc.ProfileStatusChange
	fail    error
}

func (l *lifecycleRecorder) ProfileStatusChanged(_ context.Context, _ pgxTx, ch svc.ProfileStatusChange) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail != nil {
		return l.fail
	}
	l.changes = append(l.changes, ch)
	return nil
}

func (l *lifecycleRecorder) reset() {
	l.mu.Lock()
	l.changes, l.fail = nil, nil
	l.mu.Unlock()
}

func (l *lifecycleRecorder) failWith(err error) {
	l.mu.Lock()
	l.fail = err
	l.mu.Unlock()
}

func (l *lifecycleRecorder) seen() []svc.ProfileStatusChange {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]svc.ProfileStatusChange(nil), l.changes...)
}

var (
	lifecycle     = &lifecycleRecorder{}
	lifecycleOnce sync.Once
)

// watchLifecycle registers the shared recorder on first use and clears it.
func (s *stack) watchLifecycle() *lifecycleRecorder {
	lifecycleOnce.Do(func() { s.profiles.AddLifecycleObserver(lifecycle) })
	lifecycle.reset()
	return lifecycle
}

// partyFixture is a party with an operator administrator.
type partyFixture struct {
	partyID   int64
	partyUUID string
	partyName string
	admin     account
	adminTok  tokens
	adminProf int64
}

func (s *stack) newParty(t *testing.T) partyFixture {
	t.Helper()
	f := partyFixture{admin: s.register(t)}
	f.adminTok, f.adminProf = s.makeOperator(t, f.admin)
	f.partyName = "Profile Party " + uniq("p")
	f.partyID = s.seedParty(t, f.partyName)
	f.partyUUID = s.entityUUID(t, f.partyID)
	return f
}

func (f partyFixture) profilesPath() string { return "/v1/parties/" + f.partyUUID + "/profiles" }

// invite invites by email as the administrator and returns the response.
func (s *stack) inviteEmail(t *testing.T, f partyFixture, email string) *resp {
	t.Helper()
	return s.do(t, http.MethodPost, f.profilesPath(), f.adminTok.access(), map[string]any{"holder_email": email})
}

// invited invites a fresh account and returns it with the new profile's uuid.
func (s *stack) invited(t *testing.T, f partyFixture) (account, string) {
	t.Helper()
	a := s.register(t)
	v := s.inviteEmail(t, f, a.Email).wantStatus(t, http.StatusCreated).obj(t)
	return a, v["uuid"].(string)
}

// accepted invites a fresh account and has it accept; it returns the account
// and the profile uuid.
func (s *stack) accepted(t *testing.T, f partyFixture) (account, string) {
	t.Helper()
	a, id := s.invited(t, f)
	s.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/accept", a.tk.access(), nil).wantStatus(t, http.StatusOK)
	return a, id
}

// profileRow reads the status, source, end reason and owner of a profile by uuid.
func (s *stack) profileRow(t *testing.T, profileUUID string) (status, source, endReason string, ownerID, invitedBy int64, startedSet, endedSet bool) {
	t.Helper()
	var reason *string
	var inv *int64
	var started, ended *string
	if err := s.pool.QueryRow(context.Background(), `
SELECT p.status, p.source, p.end_reason, e.owner_id, p.invited_by_profile_id, p.started_at::text, p.ended_at::text
  FROM profiles p JOIN entities e ON e.id = p.entity_id WHERE e.uuid = $1::uuid`, profileUUID).
		Scan(&status, &source, &reason, &ownerID, &inv, &started, &ended); err != nil {
		t.Fatalf("profile row %s: %v", profileUUID, err)
	}
	if reason != nil {
		endReason = *reason
	}
	if inv != nil {
		invitedBy = *inv
	}
	return status, source, endReason, ownerID, invitedBy, started != nil, ended != nil
}

func (s *stack) profileStatus(t *testing.T, profileUUID string) string {
	t.Helper()
	status, _, _, _, _, _, _ := s.profileRow(t, profileUUID)
	return status
}

// noInternalIDs fails when an object body carries an internal id key.
func noInternalIDs(t *testing.T, raw []byte) {
	t.Helper()
	for _, banned := range []string{`"id":`, `"holder_id"`, `"party_id"`, `"entity_id"`, `"principal_id"`, `"invited_by_profile_id"`} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("response leaks an internal id (%s): %s", banned, raw)
		}
	}
}

// ---------------------------------------------------------------------------
// invitation
// ---------------------------------------------------------------------------

func TestProfiles_InviteCreatesAnInvitedProfile(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)
	invitee := env.register(t)
	inviteeID := env.principalID(t, invitee.Email)

	env.obs.reset()
	r := env.inviteEmail(t, f, "  "+strings.ToUpper(invitee.Email)+" ").wantStatus(t, http.StatusCreated)
	noInternalIDs(t, r.raw)
	var view struct {
		UUID        string `json:"uuid"`
		Kind        string `json:"kind"`
		Status      string `json:"status"`
		Source      string `json:"source"`
		DisplayName string `json:"display_name"`
		Party       struct {
			UUID string `json:"uuid"`
			Kind string `json:"kind"`
		} `json:"party"`
		Holder struct {
			UUID    string `json:"uuid"`
			Managed bool   `json:"managed"`
		} `json:"holder"`
		InvitedBy *string `json:"invited_by_profile_uuid"`
	}
	r.into(t, &view)
	if view.Kind != "party" || view.Status != "invited" || view.Source != "manual" || view.Party.UUID != f.partyUUID || view.Party.Kind != "organization" {
		t.Fatalf("view = %+v", view)
	}
	if view.Holder.UUID != env.entityUUID(t, inviteeID) || view.Holder.Managed {
		t.Errorf("holder = %+v", view.Holder)
	}
	if view.InvitedBy == nil || *view.InvitedBy != env.entityUUID(t, f.adminProf) {
		t.Errorf("invited_by_profile_uuid = %v, want the administrator's profile", view.InvitedBy)
	}
	if view.DisplayName != "Test User @ "+f.partyName {
		t.Errorf("display_name = %q", view.DisplayName)
	}

	status, source, _, owner, invitedBy, started, ended := env.profileRow(t, view.UUID)
	if status != "invited" || source != "manual" || owner != f.partyID || invitedBy != f.adminProf || started || ended {
		t.Errorf("row = %s %s owner=%d invited_by=%d started=%v ended=%v", status, source, owner, invitedBy, started, ended)
	}
	env.checkObservations(t, "invite", "create profile")
	got := lc.seen()
	if len(got) != 1 || got[0].From != "" || got[0].To != "invited" || got[0].PartyID != f.partyID || got[0].HolderPrincipalID != inviteeID {
		t.Errorf("lifecycle = %+v, want one creation as invited", got)
	}

	// The invitation is in the holder's own list, locked until accepted.
	var list struct {
		Profiles []map[string]any `json:"profiles"`
	}
	env.do(t, http.MethodGet, "/v1/me/profiles", invitee.tk.access(), nil).wantStatus(t, http.StatusOK).into(t, &list)
	var found map[string]any
	for _, p := range list.Profiles {
		if p["uuid"] == view.UUID {
			found = p
		}
	}
	if found == nil {
		t.Fatalf("the invited profile is not in GET /v1/me/profiles: %v", list.Profiles)
	}
	lock, _ := found["lock"].(map[string]any)
	remedy, _ := lock["remedy"].(map[string]any)
	if found["status"] != "invited" || found["available"] != false || lock["reason"] != "inactive" || remedy["kind"] != "accept_invitation" {
		t.Errorf("invited profile as the holder sees it = %v", found)
	}
}

func TestProfiles_InviteByUserUUIDAndPolicyConfirmationFlag(t *testing.T) {
	f := env.newParty(t)
	a := env.register(t)
	r := env.do(t, http.MethodPost, f.profilesPath(), f.adminTok.access(), map[string]any{
		"holder_user_uuid": env.entityUUID(t, env.principalID(t, a.Email)), "confirm_policy_mismatch": true,
	}).wantStatus(t, http.StatusCreated)
	if r.obj(t)["status"] != "invited" {
		t.Fatalf("body = %s", r.raw)
	}
}

func TestProfiles_InviteRefusals(t *testing.T) {
	f := env.newParty(t)
	otherParty := env.seedParty(t, "Other Party "+uniq("p"))

	t.Run("a second open profile in the party is users.profile_exists", func(t *testing.T) {
		a, _ := env.invited(t, f)
		env.inviteEmail(t, f, a.Email).wantError(t, http.StatusConflict, "conflict", "users.profile_exists")
		// An accepted one is open too.
		b, _ := env.accepted(t, f)
		env.inviteEmail(t, f, b.Email).wantError(t, http.StatusConflict, "conflict", "users.profile_exists")
	})

	t.Run("an unknown email is not found and no account is created", func(t *testing.T) {
		before := env.count(t, "identifiers", `SELECT count(*) FROM mod_users.user_identifiers`)
		users := env.count(t, "users", `SELECT count(*) FROM mod_users.users`)
		env.inviteEmail(t, f, uniq("nobody")+"@example.test").wantError(t, http.StatusNotFound, "not_found", "")
		if env.count(t, "identifiers", `SELECT count(*) FROM mod_users.user_identifiers`) != before ||
			env.count(t, "users", `SELECT count(*) FROM mod_users.users`) != users {
			t.Error("an invitation created an account")
		}
	})

	t.Run("an unknown user uuid is not found", func(t *testing.T) {
		env.do(t, http.MethodPost, f.profilesPath(), f.adminTok.access(), map[string]any{
			"holder_user_uuid": "44444444-4444-4444-4444-444444444444",
		}).wantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("a guest must upgrade first", func(t *testing.T) {
		g := env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": uniq("dev")}).
			wantStatus(t, http.StatusCreated).tok(t)
		guestUUID := env.do(t, http.MethodGet, "/v1/me", g.access(), nil).wantStatus(t, http.StatusOK).obj(t)["uuid"].(string)
		env.do(t, http.MethodPost, f.profilesPath(), f.adminTok.access(), map[string]any{"holder_user_uuid": guestUUID}).
			wantError(t, http.StatusConflict, "conflict", "users.guest_upgrade_required")
	})

	t.Run("a managed user of another party is not admissible", func(t *testing.T) {
		email := uniq("managed") + "@example.test"
		env.seedManagedUser(t, otherParty, email, testPassword)
		env.inviteEmail(t, f, email).wantError(t, http.StatusConflict, "conflict", "users.holder_not_admissible")
	})

	t.Run("a managed user of this party can be invited", func(t *testing.T) {
		email := uniq("managed") + "@example.test"
		env.seedManagedUser(t, f.partyID, email, testPassword)
		env.inviteEmail(t, f, email).wantStatus(t, http.StatusCreated)
	})

	t.Run("a service account is not admissible", func(t *testing.T) {
		sa := env.seedServiceAccount(t)
		env.do(t, http.MethodPost, f.profilesPath(), f.adminTok.access(), map[string]any{"holder_user_uuid": sa.UUID}).
			wantError(t, http.StatusConflict, "conflict", "users.holder_not_admissible")
	})

	t.Run("the body is validated before the party is looked up", func(t *testing.T) {
		unknown := "/v1/parties/55555555-5555-5555-5555-555555555555/profiles"
		for name, body := range map[string]any{
			"neither holder": map[string]any{},
			"both holders":   map[string]any{"holder_email": "a@example.test", "holder_user_uuid": "44444444-4444-4444-4444-444444444444"},
			"bad email":      map[string]any{"holder_email": "nope"},
			"bad uuid":       map[string]any{"holder_user_uuid": "nope"},
		} {
			r := env.do(t, http.MethodPost, unknown, f.adminTok.access(), body)
			if r.status != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400: %s", name, r.status, r.raw)
			}
		}
		env.do(t, http.MethodPost, f.profilesPath(), f.adminTok.access(), `{"holder_email":`).wantStatus(t, http.StatusBadRequest)
	})

	t.Run("an ended profile does not block a new invitation", func(t *testing.T) {
		a, id := env.invited(t, f)
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/decline", a.tk.access(), nil).wantStatus(t, http.StatusNoContent)
		again := env.inviteEmail(t, f, a.Email).wantStatus(t, http.StatusCreated).obj(t)
		if again["uuid"] == id {
			t.Error("the new invitation reused the ended profile")
		}
	})
}

// ---------------------------------------------------------------------------
// holder actions
// ---------------------------------------------------------------------------

func TestProfiles_AcceptDeclineLeave(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)

	t.Run("accept activates the profile and returns the holder's view", func(t *testing.T) {
		a, id := env.invited(t, f)
		holderID := env.principalID(t, a.Email)
		env.obs.reset()
		lc.reset()
		r := env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/accept", a.tk.access(), nil).wantStatus(t, http.StatusOK)
		noInternalIDs(t, r.raw)
		var v profileView
		r.into(t, &v)
		if v.UUID != id || v.Status != "active" || v.Kind != "party" || v.Party == nil || v.Party.UUID != f.partyUUID || !v.Available || v.Lock != nil {
			t.Fatalf("view = %+v", v)
		}
		status, _, _, _, _, started, _ := env.profileRow(t, id)
		if status != "active" || !started {
			t.Errorf("row status = %s started = %v", status, started)
		}
		env.checkObservations(t, "accept", "update profile")
		if got := lc.seen(); len(got) != 1 || got[0].From != "invited" || got[0].To != "active" || got[0].HolderPrincipalID != holderID {
			t.Errorf("lifecycle = %+v", got)
		}
		// Accepting twice is a transition error, not a second write.
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/accept", a.tk.access(), nil).
			wantError(t, http.StatusConflict, "conflict", "users.profile_transition_invalid")
	})

	t.Run("decline ends the invitation with the reason declined", func(t *testing.T) {
		a, id := env.invited(t, f)
		env.obs.reset()
		lc.reset()
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/decline", a.tk.access(), nil).wantStatus(t, http.StatusNoContent)
		status, _, reason, _, _, _, ended := env.profileRow(t, id)
		if status != "ended" || reason != "declined" || !ended {
			t.Errorf("row = %s %q ended=%v", status, reason, ended)
		}
		env.checkObservations(t, "decline", "update profile")
		if got := lc.seen(); len(got) != 1 || got[0].From != "invited" || got[0].To != "ended" {
			t.Errorf("lifecycle = %+v", got)
		}
		// It no longer appears in the holder's list.
		var list struct {
			Profiles []profileView `json:"profiles"`
		}
		env.do(t, http.MethodGet, "/v1/me/profiles", a.tk.access(), nil).wantStatus(t, http.StatusOK).into(t, &list)
		for _, p := range list.Profiles {
			if p.UUID == id {
				t.Error("a declined profile is still listed")
			}
		}
	})

	t.Run("leave ends an active profile with the reason left", func(t *testing.T) {
		a, id := env.accepted(t, f)
		env.obs.reset()
		lc.reset()
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/leave", a.tk.access(), nil).wantStatus(t, http.StatusNoContent)
		status, _, reason, _, _, _, _ := env.profileRow(t, id)
		if status != "ended" || reason != "left" {
			t.Errorf("row = %s %q", status, reason)
		}
		env.checkObservations(t, "leave", "update profile")
		if got := lc.seen(); len(got) != 1 || got[0].From != "active" || got[0].To != "ended" {
			t.Errorf("lifecycle = %+v", got)
		}
	})

	t.Run("a suspended profile can be left", func(t *testing.T) {
		a, id := env.accepted(t, f)
		env.do(t, http.MethodPatch, "/v1/profiles/"+id, f.adminTok.access(), map[string]string{"status": "suspended"}).wantStatus(t, http.StatusOK)
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/leave", a.tk.access(), nil).wantStatus(t, http.StatusNoContent)
		if got := env.profileStatus(t, id); got != "ended" {
			t.Errorf("status = %s", got)
		}
	})

	t.Run("the transition table is enforced for the holder", func(t *testing.T) {
		a, invitedID := env.invited(t, f)
		// Leaving an invitation, or declining it after accepting, is refused.
		env.do(t, http.MethodPost, "/v1/me/profiles/"+invitedID+"/leave", a.tk.access(), nil).
			wantError(t, http.StatusConflict, "conflict", "users.profile_transition_invalid")
		env.do(t, http.MethodPost, "/v1/me/profiles/"+invitedID+"/accept", a.tk.access(), nil).wantStatus(t, http.StatusOK)
		env.do(t, http.MethodPost, "/v1/me/profiles/"+invitedID+"/decline", a.tk.access(), nil).
			wantError(t, http.StatusConflict, "conflict", "users.profile_transition_invalid")
		// Ended is terminal: every holder action on it is refused.
		env.do(t, http.MethodPost, "/v1/me/profiles/"+invitedID+"/leave", a.tk.access(), nil).wantStatus(t, http.StatusNoContent)
		for _, action := range []string{"accept", "decline", "leave"} {
			env.do(t, http.MethodPost, "/v1/me/profiles/"+invitedID+"/"+action, a.tk.access(), nil).
				wantError(t, http.StatusConflict, "conflict", "users.profile_transition_invalid")
		}
	})

	t.Run("a profile the caller does not hold is a masked 404", func(t *testing.T) {
		_, id := env.invited(t, f)
		other := env.register(t)
		missing := "66666666-6666-6666-6666-666666666666"
		for _, action := range []string{"accept", "decline", "leave"} {
			foreign := env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/"+action, other.tk.access(), nil)
			absent := env.do(t, http.MethodPost, "/v1/me/profiles/"+missing+"/"+action, other.tk.access(), nil)
			if foreign.status != http.StatusNotFound || absent.status != http.StatusNotFound || string(foreign.raw) != string(absent.raw) {
				t.Errorf("%s: foreign = %d %s, missing = %d %s; want identical 404s", action, foreign.status, foreign.raw, absent.status, absent.raw)
			}
		}
		// The administrator is not the holder either.
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/accept", f.adminTok.access(), nil).wantStatus(t, http.StatusNotFound)
		// A personal profile has no lifecycle.
		personal := env.profileUUID(t, env.personalProfileID(t, env.principalID(t, other.Email)))
		env.do(t, http.MethodPost, "/v1/me/profiles/"+personal+"/leave", other.tk.access(), nil).wantStatus(t, http.StatusNotFound)
	})

	t.Run("a stale authentication does not gate the holder's own actions", func(t *testing.T) {
		a, id := env.invited(t, f)
		env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '3 hours' WHERE uuid = $1::uuid`, a.tk.Session.UUID)
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/accept", a.tk.access(), nil).wantStatus(t, http.StatusOK)
	})
}

// ---------------------------------------------------------------------------
// administrator actions
// ---------------------------------------------------------------------------

func TestProfiles_AdminStatusChanges(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)
	patch := func(id string, body any) *resp {
		return env.do(t, http.MethodPatch, "/v1/profiles/"+id, f.adminTok.access(), body)
	}

	t.Run("suspend, resume and end", func(t *testing.T) {
		a, id := env.accepted(t, f)
		holderID := env.principalID(t, a.Email)

		env.obs.reset()
		lc.reset()
		r := patch(id, map[string]string{"status": "suspended"}).wantStatus(t, http.StatusOK)
		noInternalIDs(t, r.raw)
		if r.obj(t)["status"] != "suspended" {
			t.Fatalf("body = %s", r.raw)
		}
		env.checkObservations(t, "suspend", "update profile")

		patch(id, map[string]string{"status": "active"}).wantStatus(t, http.StatusOK)
		r = patch(id, map[string]string{"status": "ended", "end_reason": "contract over"}).wantStatus(t, http.StatusOK)
		view := r.obj(t)
		if view["status"] != "ended" || view["end_reason"] != "contract over" || view["ended_at"] == nil {
			t.Fatalf("ended view = %s", r.raw)
		}
		got := lc.seen()
		want := [][2]string{{"active", "suspended"}, {"suspended", "active"}, {"active", "ended"}}
		if len(got) != len(want) {
			t.Fatalf("lifecycle = %+v", got)
		}
		for i, w := range want {
			if got[i].From != w[0] || got[i].To != w[1] || got[i].HolderPrincipalID != holderID || got[i].PartyID != f.partyID {
				t.Errorf("lifecycle[%d] = %+v, want %v", i, got[i], w)
			}
		}
		// A suspended profile cannot be used to sign in.
		_, _, reason, _, _, _, _ := env.profileRow(t, id)
		if reason != "contract over" {
			t.Errorf("end_reason = %q", reason)
		}
	})

	t.Run("a suspended profile is locked for its holder", func(t *testing.T) {
		a, id := env.accepted(t, f)
		patch(id, map[string]string{"status": "suspended"}).wantStatus(t, http.StatusOK)
		var list struct {
			Profiles []profileView `json:"profiles"`
		}
		env.login(t, a) // a new session sees the lock
		env.do(t, http.MethodGet, "/v1/me/profiles", a.tk.access(), nil).wantStatus(t, http.StatusOK).into(t, &list)
		for _, p := range list.Profiles {
			if p.UUID == id && (p.Available || p.Status != "suspended") {
				t.Errorf("suspended profile = %+v, want unavailable", p)
			}
		}
	})

	t.Run("the transition table is enforced for the administrator", func(t *testing.T) {
		_, invitedID := env.invited(t, f)
		_, activeID := env.accepted(t, f)
		_, endedID := env.accepted(t, f)
		patch(endedID, map[string]string{"status": "ended", "end_reason": "gone"}).wantStatus(t, http.StatusOK)
		conflict := "users.profile_transition_invalid"
		cases := []struct {
			name, id string
			body     map[string]string
		}{
			{"accept an invitation on the holder's behalf", invitedID, map[string]string{"status": "active"}},
			{"suspend an invitation", invitedID, map[string]string{"status": "suspended"}},
			{"resume an active profile", activeID, map[string]string{"status": "active"}},
			{"suspend twice", activeID, map[string]string{"status": "suspended"}},
			{"re-activate an ended profile", endedID, map[string]string{"status": "active"}},
			{"end an ended profile", endedID, map[string]string{"status": "ended", "end_reason": "again"}},
			{"suspend an ended profile", endedID, map[string]string{"status": "suspended"}},
		}
		for i, c := range cases {
			if i == 3 {
				patch(activeID, map[string]string{"status": "suspended"}).wantStatus(t, http.StatusOK) // set up "suspend twice"
			}
			patch(c.id, c.body).wantError(t, http.StatusConflict, "conflict", conflict)
		}
		// Revoking an invitation is allowed.
		patch(invitedID, map[string]string{"status": "ended", "end_reason": "withdrawn"}).wantStatus(t, http.StatusOK)
	})

	t.Run("shape errors are 400 before any lookup", func(t *testing.T) {
		_, id := env.accepted(t, f)
		unknown := "77777777-7777-7777-7777-777777777777"
		for name, body := range map[string]any{
			"no status":                map[string]string{},
			"unknown status":           map[string]string{"status": "banished"},
			"invited is not a target":  map[string]string{"status": "invited"},
			"end without a reason":     map[string]string{"status": "ended"},
			"end with a blank reason":  map[string]string{"status": "ended", "end_reason": "   "},
			"reason on a suspend":      map[string]string{"status": "suspended", "end_reason": "x"},
			"reason with control byte": map[string]string{"status": "ended", "end_reason": "bad\u0007"},
			"reason too long":          map[string]string{"status": "ended", "end_reason": strings.Repeat("x", 501)},
		} {
			for _, target := range []string{id, unknown} {
				if r := patch(target, body); r.status != http.StatusBadRequest {
					t.Errorf("%s (%s): status = %d, want 400: %s", name, target, r.status, r.raw)
				}
			}
		}
		patch(id, `{"status":`).wantStatus(t, http.StatusBadRequest)
		env.do(t, http.MethodPatch, "/v1/profiles/not-a-uuid", f.adminTok.access(), map[string]string{"status": "suspended"}).wantStatus(t, http.StatusBadRequest)
		patch(unknown, map[string]string{"status": "suspended"}).wantError(t, http.StatusNotFound, "not_found", "")
		if got := env.profileStatus(t, id); got != "active" {
			t.Errorf("a refused change altered the profile: %s", got)
		}
	})

	t.Run("a personal profile is not found", func(t *testing.T) {
		a := env.register(t)
		personal := env.profileUUID(t, env.personalProfileID(t, env.principalID(t, a.Email)))
		patch(personal, map[string]string{"status": "suspended"}).wantStatus(t, http.StatusNotFound)
	})

	t.Run("a stale authentication needs a step-up, only after authorization", func(t *testing.T) {
		_, id := env.accepted(t, f)
		stale := env.register(t)
		staleTok, _ := env.makeOperator(t, stale)
		env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '3 hours' WHERE uuid = $1::uuid`, staleTok.Session.UUID)
		env.do(t, http.MethodPatch, "/v1/profiles/"+id, staleTok.access(), map[string]string{"status": "suspended"}).
			wantAction(t, http.StatusConflict, "users.step_up_required")
		if got := env.profileStatus(t, id); got != "active" {
			t.Errorf("a stale-session change was applied: %s", got)
		}
		// A caller without authority learns nothing of the step-up.
		ordinary := env.register(t)
		env.exec(t, "age auth_time", `UPDATE mod_users.sessions SET auth_time = now() - interval '3 hours' WHERE uuid = $1::uuid`, ordinary.tk.Session.UUID)
		env.do(t, http.MethodPatch, "/v1/profiles/"+id, ordinary.tk.access(), map[string]string{"status": "suspended"}).
			wantError(t, http.StatusNotFound, "not_found", "")
		// Inviting is not behind the freshness gate.
		env.do(t, http.MethodPost, f.profilesPath(), staleTok.access(), map[string]any{"holder_email": env.register(t).Email}).
			wantStatus(t, http.StatusCreated)
	})
}

// ---------------------------------------------------------------------------
// masking and listing
// ---------------------------------------------------------------------------

func TestProfiles_UnauthorizedCallersSeeNotFound(t *testing.T) {
	f := env.newParty(t)
	_, profileID := env.accepted(t, f)
	ordinary := env.register(t)
	unknownParty := "88888888-8888-8888-8888-888888888888"
	unknownProfile := "99999999-9999-9999-9999-999999999999"

	same := func(name, method, existing, missing string, body any) {
		t.Helper()
		ex := env.do(t, method, existing, ordinary.tk.access(), body)
		mi := env.do(t, method, missing, ordinary.tk.access(), body)
		if ex.status != http.StatusNotFound || mi.status != http.StatusNotFound {
			t.Errorf("%s: existing = %d, missing = %d, want 404 for both; %s | %s", name, ex.status, mi.status, ex.raw, mi.raw)
			return
		}
		if string(ex.raw) != string(mi.raw) {
			t.Errorf("%s: a refused existing resource is distinguishable from a missing one:\n%s\n%s", name, ex.raw, mi.raw)
		}
	}
	same("list profiles", http.MethodGet, f.profilesPath(), "/v1/parties/"+unknownParty+"/profiles", nil)
	same("invite", http.MethodPost, f.profilesPath(), "/v1/parties/"+unknownParty+"/profiles", map[string]any{"holder_email": ordinary.Email})
	same("managed user", http.MethodPost, "/v1/parties/"+f.partyUUID+"/managed-users", "/v1/parties/"+unknownParty+"/managed-users",
		map[string]string{"email": uniq("m") + "@example.test", "password": testPassword})
	same("patch profile", http.MethodPatch, "/v1/profiles/"+profileID, "/v1/profiles/"+unknownProfile, map[string]string{"status": "suspended"})

	// A user's own party-less uuid is not a party either.
	env.do(t, http.MethodGet, "/v1/parties/"+env.entityUUID(t, env.principalID(t, ordinary.Email))+"/profiles", f.adminTok.access(), nil).
		wantStatus(t, http.StatusNotFound)

	// A refused invitation must not have created anything.
	if got := env.profileStatus(t, profileID); got != "active" {
		t.Errorf("status = %s", got)
	}
	if n := env.count(t, "ordinary profiles", `SELECT count(*) FROM profiles WHERE holder_id = $1 AND party_id = $2`,
		env.principalID(t, ordinary.Email), f.partyID); n != 0 {
		t.Errorf("the ordinary user holds %d profiles in the party", n)
	}
}

func TestProfiles_ListPartyProfiles(t *testing.T) {
	f := env.newParty(t)
	a1, id1 := env.accepted(t, f)
	_, id2 := env.invited(t, f)
	a3, id3 := env.invited(t, f)
	env.do(t, http.MethodPost, "/v1/me/profiles/"+id3+"/decline", a3.tk.access(), nil).wantStatus(t, http.StatusNoContent)

	type item struct {
		UUID   string `json:"uuid"`
		Status string `json:"status"`
		Holder struct {
			DisplayName string `json:"display_name"`
		} `json:"holder"`
	}
	var page struct {
		Profiles []item `json:"profiles"`
		Total    int64  `json:"total"`
		Limit    int32  `json:"limit"`
		Offset   int32  `json:"offset"`
	}
	r := env.do(t, http.MethodGet, f.profilesPath(), f.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	noInternalIDs(t, r.raw)
	r.into(t, &page)
	if page.Total != 2 || len(page.Profiles) != 2 || page.Profiles[0].UUID != id1 || page.Profiles[1].UUID != id2 {
		t.Fatalf("default list = %+v, want the two open profiles, oldest first", page)
	}
	if page.Profiles[0].Holder.DisplayName != "Test User" {
		t.Errorf("holder display name = %q", page.Profiles[0].Holder.DisplayName)
	}

	env.do(t, http.MethodGet, f.profilesPath()+"?status=ended", f.adminTok.access(), nil).wantStatus(t, http.StatusOK).into(t, &page)
	if page.Total != 1 || len(page.Profiles) != 1 || page.Profiles[0].UUID != id3 || page.Profiles[0].Status != "ended" {
		t.Errorf("ended list = %+v", page)
	}
	env.do(t, http.MethodGet, f.profilesPath()+"?status=invited", f.adminTok.access(), nil).wantStatus(t, http.StatusOK).into(t, &page)
	if page.Total != 1 || page.Profiles[0].UUID != id2 {
		t.Errorf("invited list = %+v", page)
	}
	env.do(t, http.MethodGet, f.profilesPath()+"?limit=1&offset=1", f.adminTok.access(), nil).wantStatus(t, http.StatusOK).into(t, &page)
	if page.Total != 2 || len(page.Profiles) != 1 || page.Profiles[0].UUID != id2 || page.Limit != 1 || page.Offset != 1 {
		t.Errorf("paged list = %+v", page)
	}
	env.do(t, http.MethodGet, f.profilesPath()+"?status=banished", f.adminTok.access(), nil).wantStatus(t, http.StatusBadRequest)
	env.do(t, http.MethodGet, f.profilesPath()+"?limit=-3", f.adminTok.access(), nil).wantStatus(t, http.StatusBadRequest)
	_ = a1
}

// ---------------------------------------------------------------------------
// departure cleanup
// ---------------------------------------------------------------------------

// departureSetup gives a holder an active profile in the party with: a
// password credential confined to the party and authorized for it (so a session
// holding it is dependent on the party), and an unconfined ssh key.
func (s *stack) departureSetup(t *testing.T, f partyFixture) (holder account, profileUUID string, passwordID, sshID int64) {
	t.Helper()
	holder, profileUUID = s.accepted(t, f)
	uid := s.principalID(t, holder.Email)
	passwordID = s.activePasswordID(t, uid)
	s.exec(t, "authorize", `INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		passwordID, f.partyID, f.adminProf)
	// Landing on the party profile needs the confinement set before sign-in.
	s.exec(t, "confine", `UPDATE mod_users.credentials SET confined_to_party_id = $2 WHERE id = $1`, passwordID, f.partyID)
	tk := s.login(t, holder)
	if tk.Profile == nil || tk.Profile.UUID != profileUUID {
		t.Fatalf("a confined password should land on the party profile, got %+v", tk.Profile)
	}
	holder.tk = tk
	key := s.do(t, http.MethodPost, "/v1/me/credentials/ssh-keys", tk.access(), map[string]string{"public_key": newSSHPublicKey(t)}).
		wantStatus(t, http.StatusCreated).obj(t)
	sshID = s.scanInt64(t, "ssh credential", `SELECT id FROM mod_users.credentials WHERE uuid = $1::uuid`, key["uuid"].(string))
	return holder, profileUUID, passwordID, sshID
}

func TestProfiles_DepartureCleanup(t *testing.T) {
	f := env.newParty(t)
	check := func(t *testing.T, holder account, passwordID, sshID int64) {
		t.Helper()
		if n := env.authorizedParties(t, passwordID); n != 0 {
			t.Errorf("credential authorizations for the party after departure = %d, want 0", n)
		}
		var revoked *string
		var reason *string
		if err := env.pool.QueryRow(context.Background(),
			`SELECT revoked_at::text, revoke_reason FROM mod_users.credentials WHERE id = $1`, passwordID).Scan(&revoked, &reason); err != nil {
			t.Fatal(err)
		}
		if revoked == nil || reason == nil || *reason != "departure" {
			t.Errorf("confined credential: revoked=%v reason=%v, want revoked with reason departure", revoked, reason)
		}
		if n := env.count(t, "ssh key", `SELECT count(*) FROM mod_users.credentials WHERE id = $1 AND revoked_at IS NULL`, sshID); n != 1 {
			t.Error("a credential not confined to the party was revoked")
		}
		var sessionReason *string
		if err := env.pool.QueryRow(context.Background(),
			`SELECT revoke_reason FROM mod_users.sessions WHERE uuid = $1::uuid`, holder.tk.Session.UUID).Scan(&sessionReason); err != nil {
			t.Fatal(err)
		}
		if sessionReason == nil || *sessionReason != "credential_revoked" {
			t.Errorf("the session holding the confined credential: revoke_reason = %v, want credential_revoked", sessionReason)
		}
		env.do(t, http.MethodGet, "/v1/me", holder.tk.access(), nil).wantStatus(t, http.StatusUnauthorized)
		if n := env.count(t, "disabled", `SELECT count(*) FROM principals WHERE entity_id = $1 AND disabled_at IS NOT NULL`,
			env.principalID(t, holder.Email)); n != 0 {
			t.Error("a bring-your-own user was disabled by leaving a party")
		}
	}
	wantPairs := []string{"update profile", "delete credential_party_authorization", "revoke credential", "revoke session"}

	t.Run("leaving", func(t *testing.T) {
		holder, id, passwordID, sshID := env.departureSetup(t, f)
		env.obs.reset()
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/leave", holder.tk.access(), nil).wantStatus(t, http.StatusNoContent)
		env.checkObservations(t, "leave cleanup", wantPairs...)
		check(t, holder, passwordID, sshID)
	})

	t.Run("an administrator ending the profile", func(t *testing.T) {
		holder, id, passwordID, sshID := env.departureSetup(t, f)
		env.obs.reset()
		env.do(t, http.MethodPatch, "/v1/profiles/"+id, f.adminTok.access(), map[string]string{"status": "ended", "end_reason": "offboarded"}).
			wantStatus(t, http.StatusOK)
		env.checkObservations(t, "admin end cleanup", wantPairs...)
		check(t, holder, passwordID, sshID)
	})

	t.Run("another party's authorizations and credentials are untouched", func(t *testing.T) {
		holder, id, passwordID, _ := env.departureSetup(t, f)
		other := env.seedParty(t, "Bystander "+uniq("p"))
		otherProfile, _ := env.seedPartyProfile(t, env.principalID(t, holder.Email), other, "active")
		env.exec(t, "authorize other", `INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
			passwordID, other, otherProfile)
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/leave", holder.tk.access(), nil).wantStatus(t, http.StatusNoContent)
		if n := env.count(t, "other authorization", `SELECT count(*) FROM mod_users.credential_party_authorizations WHERE credential_id = $1 AND party_id = $2`,
			passwordID, other); n != 1 {
			t.Errorf("the other party's authorization count = %d, want 1", n)
		}
	})

	t.Run("declining an invitation runs the same cleanup", func(t *testing.T) {
		a, id := env.invited(t, f)
		passwordID := env.activePasswordID(t, env.principalID(t, a.Email))
		env.exec(t, "authorize", `INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
			passwordID, f.partyID, f.adminProf)
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/decline", a.tk.access(), nil).wantStatus(t, http.StatusNoContent)
		if n := env.authorizedParties(t, passwordID); n != 0 {
			t.Errorf("authorizations after decline = %d", n)
		}
	})
}

// ---------------------------------------------------------------------------
// managed users
// ---------------------------------------------------------------------------

func TestProfiles_ManagedUsers(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)
	path := "/v1/parties/" + f.partyUUID + "/managed-users"

	t.Run("creation", func(t *testing.T) {
		email := uniq("managed") + "@example.test"
		env.obs.reset()
		lc.reset()
		r := env.do(t, http.MethodPost, path, f.adminTok.access(), map[string]string{"email": strings.ToUpper(email), "password": testPassword}).
			wantStatus(t, http.StatusCreated)
		noInternalIDs(t, r.raw)
		var body struct {
			User struct {
				UUID              string  `json:"uuid"`
				Kind              string  `json:"kind"`
				Email             *string `json:"email"`
				Managed           bool    `json:"managed"`
				ManagingPartyUUID *string `json:"managing_party_uuid"`
				IsGuest           bool    `json:"is_guest"`
			} `json:"user"`
			Profile struct {
				UUID   string `json:"uuid"`
				Status string `json:"status"`
				Source string `json:"source"`
				Kind   string `json:"kind"`
			} `json:"profile"`
		}
		r.into(t, &body)
		if body.User.Kind != "user" || !body.User.Managed || body.User.IsGuest || body.User.Email == nil || *body.User.Email != email ||
			body.User.ManagingPartyUUID == nil || *body.User.ManagingPartyUUID != f.partyUUID {
			t.Fatalf("user = %+v", body.User)
		}
		if body.Profile.Status != "active" || body.Profile.Source != "manual" || body.Profile.Kind != "party" {
			t.Fatalf("profile = %+v", body.Profile)
		}

		uid := env.principalID(t, email)
		// Owned by the party, no personal profile, no person, an unverified primary email.
		if owner := env.scanInt64(t, "owner", `SELECT owner_id FROM entities WHERE id = $1`, uid); owner != f.partyID {
			t.Errorf("owner = %d, want the party %d", owner, f.partyID)
		}
		if n := env.count(t, "personal", `SELECT count(*) FROM profiles WHERE holder_id = $1 AND party_id IS NULL`, uid); n != 0 {
			t.Errorf("personal profiles = %d, want 0", n)
		}
		if n := env.count(t, "person", `SELECT count(*) FROM mod_users.users WHERE entity_id = $1 AND person_id IS NULL AND managing_party_id = $2 AND NOT is_guest`, uid, f.partyID); n != 1 {
			t.Error("the managed user has a person record, or is not managed by the party")
		}
		if n := env.count(t, "identifier", `SELECT count(*) FROM mod_users.user_identifiers WHERE user_id = $1 AND kind = 'email' AND is_primary AND verified_at IS NULL AND value_canon = $2`, uid, email); n != 1 {
			t.Error("the email identifier is not an unverified primary address")
		}
		if n := env.count(t, "credentials", `
SELECT count(*) FROM mod_users.credentials c JOIN mod_users.cred_password p ON p.credential_id = c.id
 WHERE c.principal_id = $1 AND c.revoked_at IS NULL AND p.identifier_id IS NOT NULL
   AND c.default_profile_id = (SELECT id FROM entities WHERE uuid = $2::uuid)`, uid, body.Profile.UUID); n != 1 {
			t.Error("the initial credential is not a password bound to the identifier, landing on the party profile")
		}
		if n := env.count(t, "credentials", `SELECT count(*) FROM mod_users.credentials WHERE principal_id = $1`, uid); n != 1 {
			t.Errorf("credentials = %d, want exactly the initial password", n)
		}

		env.checkObservations(t, "managed user", "create user", "create user_identifier", "create profile", "create credential")
		if got := lc.seen(); len(got) != 1 || got[0].From != "" || got[0].To != "active" || got[0].HolderPrincipalID != uid || got[0].PartyID != f.partyID {
			t.Errorf("lifecycle = %+v, want one creation as active", got)
		}

		// The managed user signs in with the initial password and lands on the party profile.
		tk := env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]string{"email": email, "password": testPassword}).wantStatus(t, http.StatusOK).tok(t)
		if tk.Profile == nil || tk.Profile.UUID != body.Profile.UUID || !tk.Profile.Available {
			t.Fatalf("landing = %+v, want the available party profile", tk.Profile)
		}
	})

	t.Run("a registered address is a conflict and nothing is left behind", func(t *testing.T) {
		taken := env.register(t)
		users := env.count(t, "users", `SELECT count(*) FROM mod_users.users`)
		profiles := env.count(t, "profiles", `SELECT count(*) FROM profiles`)
		env.do(t, http.MethodPost, path, f.adminTok.access(), map[string]string{"email": taken.Email, "password": testPassword}).
			wantError(t, http.StatusConflict, "conflict", "users.identifier_taken")
		if env.count(t, "users", `SELECT count(*) FROM mod_users.users`) != users || env.count(t, "profiles", `SELECT count(*) FROM profiles`) != profiles {
			t.Error("a refused managed-user creation left rows behind")
		}
	})

	t.Run("shape errors", func(t *testing.T) {
		for name, body := range map[string]any{
			"no email":       map[string]string{"password": testPassword},
			"bad email":      map[string]string{"email": "nope", "password": testPassword},
			"no password":    map[string]string{"email": uniq("m") + "@example.test"},
			"short password": map[string]string{"email": uniq("m") + "@example.test", "password": "short"},
		} {
			if r := env.do(t, http.MethodPost, path, f.adminTok.access(), body); r.status != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400: %s", name, r.status, r.raw)
			}
		}
		unknown := "/v1/parties/55555555-5555-5555-5555-555555555555/managed-users"
		if r := env.do(t, http.MethodPost, unknown, f.adminTok.access(), map[string]string{"email": "nope"}); r.status != http.StatusBadRequest {
			t.Errorf("400 must precede the party lookup: %d", r.status)
		}
	})

	t.Run("ending the last profile disables the managed user", func(t *testing.T) {
		email := uniq("managed") + "@example.test"
		created := env.do(t, http.MethodPost, path, f.adminTok.access(), map[string]string{"email": email, "password": testPassword}).
			wantStatus(t, http.StatusCreated).obj(t)
		profileID := created["profile"].(map[string]any)["uuid"].(string)
		uid := env.principalID(t, email)
		tk := env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]string{"email": email, "password": testPassword}).wantStatus(t, http.StatusOK).tok(t)
		env.do(t, http.MethodGet, "/v1/me", tk.access(), nil).wantStatus(t, http.StatusOK)

		env.obs.reset()
		env.do(t, http.MethodPatch, "/v1/profiles/"+profileID, f.adminTok.access(), map[string]string{"status": "ended", "end_reason": "left the company"}).
			wantStatus(t, http.StatusOK)
		env.checkObservations(t, "managed user departure", "update profile", "update user", "revoke session")

		if n := env.count(t, "disabled", `SELECT count(*) FROM principals WHERE entity_id = $1 AND disabled_at IS NOT NULL`, uid); n != 1 {
			t.Error("the managed user was not disabled")
		}
		env.do(t, http.MethodGet, "/v1/me", tk.access(), nil).wantStatus(t, http.StatusUnauthorized)
		env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]string{"email": email, "password": testPassword}).
			wantStatus(t, http.StatusUnauthorized)
	})

	t.Run("leaving disables a managed user too", func(t *testing.T) {
		email := uniq("managed") + "@example.test"
		created := env.do(t, http.MethodPost, path, f.adminTok.access(), map[string]string{"email": email, "password": testPassword}).
			wantStatus(t, http.StatusCreated).obj(t)
		profileID := created["profile"].(map[string]any)["uuid"].(string)
		tk := env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]string{"email": email, "password": testPassword}).wantStatus(t, http.StatusOK).tok(t)
		env.do(t, http.MethodPost, "/v1/me/profiles/"+profileID+"/leave", tk.access(), nil).wantStatus(t, http.StatusNoContent)
		if n := env.count(t, "disabled", `SELECT count(*) FROM principals WHERE entity_id = $1 AND disabled_at IS NOT NULL`, env.principalID(t, email)); n != 1 {
			t.Error("the managed user was not disabled")
		}
	})
}

// ---------------------------------------------------------------------------
// observers
// ---------------------------------------------------------------------------

func TestProfiles_ObserverErrorAbortsTheTransaction(t *testing.T) {
	lc := env.watchLifecycle()
	f := env.newParty(t)
	boom := errors.New("seat release failed")

	t.Run("invite", func(t *testing.T) {
		a := env.register(t)
		uid := env.principalID(t, a.Email)
		lc.failWith(boom)
		defer lc.reset()
		env.inviteEmail(t, f, a.Email).wantStatus(t, http.StatusInternalServerError)
		if n := env.count(t, "profiles", `SELECT count(*) FROM profiles WHERE holder_id = $1 AND party_id = $2`, uid, f.partyID); n != 0 {
			t.Errorf("a failed observer left %d profiles", n)
		}
		lc.reset()
		env.inviteEmail(t, f, a.Email).wantStatus(t, http.StatusCreated)
	})

	t.Run("accept and the cleanup that follows a leave", func(t *testing.T) {
		a, id := env.invited(t, f)
		lc.failWith(boom)
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/accept", a.tk.access(), nil).wantStatus(t, http.StatusInternalServerError)
		lc.reset()
		if got := env.profileStatus(t, id); got != "invited" {
			t.Errorf("status after a failed accept = %s, want invited", got)
		}
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/accept", a.tk.access(), nil).wantStatus(t, http.StatusOK)

		// A failing observer on the end rolls back the departure cleanup as well.
		passwordID := env.activePasswordID(t, env.principalID(t, a.Email))
		env.exec(t, "authorize", `INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
			passwordID, f.partyID, f.adminProf)
		lc.failWith(boom)
		env.do(t, http.MethodPost, "/v1/me/profiles/"+id+"/leave", a.tk.access(), nil).wantStatus(t, http.StatusInternalServerError)
		lc.reset()
		if got := env.profileStatus(t, id); got != "active" {
			t.Errorf("status after a failed leave = %s, want active", got)
		}
		if n := env.authorizedParties(t, passwordID); n != 1 {
			t.Errorf("authorizations after a failed leave = %d, want 1 (rolled back)", n)
		}
	})

	t.Run("managed user creation", func(t *testing.T) {
		email := uniq("managed") + "@example.test"
		users := env.count(t, "users", `SELECT count(*) FROM mod_users.users`)
		lc.failWith(boom)
		env.do(t, http.MethodPost, "/v1/parties/"+f.partyUUID+"/managed-users", f.adminTok.access(), map[string]string{"email": email, "password": testPassword}).
			wantStatus(t, http.StatusInternalServerError)
		lc.reset()
		if env.count(t, "users", `SELECT count(*) FROM mod_users.users`) != users {
			t.Error("a failed observer left a managed user behind")
		}
	})
}

// ---------------------------------------------------------------------------
// the in-transaction primitives
// ---------------------------------------------------------------------------

func TestProfiles_EndInTxIsIdempotentAndRequiresAReason(t *testing.T) {
	f := env.newParty(t)
	_, id := env.accepted(t, f)
	profileID := env.scanInt64(t, "profile id", `SELECT id FROM entities WHERE uuid = $1::uuid`, id)
	ctx := env.authCtx(t, f.adminTok.access())

	run := func(reason string) error {
		return env.inTx(ctx, func(tx pgxTx) error { return env.profiles.EndInTx(ctx, tx, profileID, reason) })
	}
	if err := run("  "); err == nil || !strings.Contains(fmt.Sprint(err), "invalid") {
		t.Fatalf("blank reason = %v, want invalid input", err)
	}
	if err := run("trusted flow"); err != nil {
		t.Fatalf("EndInTx: %v", err)
	}
	env.obs.reset()
	if err := run("again"); err != nil {
		t.Fatalf("EndInTx on an ended profile: %v", err)
	}
	if n := len(env.obs.all()); n != 0 {
		t.Errorf("ending an ended profile wrote %d observations", n)
	}
	if _, _, reason, _, _, _, _ := env.profileRow(t, id); reason != "trusted flow" {
		t.Errorf("end_reason = %q, want the first one", reason)
	}
}
