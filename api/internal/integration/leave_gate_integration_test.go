//go:build integration

package integration

// The holder's leave path is behind the party gate, and the last active
// operator administrator cannot be ended, by leaving or by an administrator.

import (
	"net/http"
	"sync"
	"testing"
)

// A session the party gate locks out of the profile cannot leave it: the
// refusal is 401 users.profile_unavailable with the lock reason, and neither
// the profile nor its credential authorizations change. Accept and decline stay
// open to such a session.
func TestLeaveGate_LockedSessionCannotLeave(t *testing.T) {
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	env.verifyRegisteredEmail(t, m.acct)
	pwUUID := env.credentialUUID(t, m.acct.tk.access(), "password")
	emailUUID := env.credentialUUID(t, m.acct.tk.access(), "email_otp")

	// Only the email credential is authorized, so a password-only session is
	// locked out of the party profile.
	env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodPut, authPath(g, emailUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)

	personal := env.login(t, m.acct)
	leave := "/v1/me/profiles/" + m.profUUID + "/leave"
	env.do(t, http.MethodPost, leave, personal.access(), nil).
		wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "credential_not_authorized")
	if got := env.profileStatus(t, m.profUUID); got != "active" {
		t.Errorf("status after a refused leave = %s, want active", got)
	}
	if n := env.authorizationCount(t, emailUUID, g.id); n != 1 {
		t.Errorf("authorizations after a refused leave = %d, want 1", n)
	}

	// Once the session satisfies the gate, the same call succeeds.
	env.do(t, http.MethodPut, authPath(g, pwUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodPost, leave, personal.access(), nil).wantStatus(t, http.StatusNoContent)
	if got := env.profileStatus(t, m.profUUID); got != "ended" {
		t.Errorf("status after leave = %s, want ended", got)
	}
	if n := env.authorizationCount(t, emailUUID, g.id); n != 0 {
		t.Errorf("authorizations after leave = %d, want 0 (departure cleanup)", n)
	}
}

// operatorFixture is a bootstrapped deployment: its operator party and the
// bootstrap administrator.
type operatorFixture struct {
	s         *stack
	partyID   int64
	adminTok  tokens
	adminProf int64
	adminUUID string
}

func bootstrapOperator(t *testing.T) operatorFixture {
	t.Helper()
	s := freshStack(t)
	token := s.mintSetupToken(t)
	s.confirmOptOut(t, token, "").wantStatus(t, http.StatusOK)
	email := "admin@operator.test"
	tk := s.postBootstrap(t, token, bootstrapBody(email, orgOperator())).wantStatus(t, http.StatusCreated).tok(t)
	party := s.scanInt64(t, "operator party", `SELECT operator_party_id FROM platform_settings WHERE id = 1`)
	prof := s.scanInt64(t, "admin profile", `SELECT entity_id FROM profiles WHERE holder_id = $1 AND party_id = $2`,
		s.principalID(t, email), party)
	return operatorFixture{s: s, partyID: party, adminTok: tk, adminProf: prof, adminUUID: s.profileUUID(t, prof)}
}

// secondAdmin adds a second operator administrator (active profile, wildcard
// manage, password authorized) and returns its session token and profile uuid.
func (f operatorFixture) secondAdmin(t *testing.T) (tokens, string) {
	t.Helper()
	s := f.s
	a := s.register(t)
	userID := s.principalID(t, a.Email)
	profID, profUUID := s.seedPartyProfile(t, userID, f.partyID, "active")
	s.exec(t, "wildcard grant",
		`INSERT INTO grants (actor_id, operation_id, target_id) SELECT $1, id, NULL FROM authz_operations WHERE slug = 'manage'`, profID)
	s.exec(t, "authorize password",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id)
		 SELECT c.id, $2, $3 FROM mod_users.credentials c WHERE c.principal_id = $1 AND c.kind = 'password'`,
		userID, f.partyID, f.adminProf)
	tk := s.do(t, http.MethodPost, "/v1/session/switch", a.tk.access(), map[string]string{"profile_uuid": profUUID}).
		wantStatus(t, http.StatusOK).tok(t)
	return tk, profUUID
}

func TestLeaveGate_LastOperatorAdminCannotLeaveOrBeEnded(t *testing.T) {
	f := bootstrapOperator(t)
	s := f.s
	leave := "/v1/me/profiles/" + f.adminUUID + "/leave"

	// Alone, the bootstrap administrator can neither leave nor be ended.
	s.do(t, http.MethodPost, leave, f.adminTok.access(), nil).wantError(t, http.StatusConflict, "conflict", "users.last_operator_admin")
	s.do(t, http.MethodPatch, "/v1/profiles/"+f.adminUUID, f.adminTok.access(), map[string]string{"status": "ended", "end_reason": "test"}).
		wantError(t, http.StatusConflict, "conflict", "users.last_operator_admin")
	if got := s.profileStatus(t, f.adminUUID); got != "active" {
		t.Fatalf("admin status after refused endings = %s, want active", got)
	}

	// With a second administrator present, the administrator can leave.
	_, secondUUID := f.secondAdmin(t)
	s.do(t, http.MethodPost, leave, f.adminTok.access(), nil).wantStatus(t, http.StatusNoContent)
	if got := s.profileStatus(t, f.adminUUID); got != "ended" {
		t.Errorf("admin status after leave = %s, want ended", got)
	}
	if got := s.profileStatus(t, secondUUID); got != "active" {
		t.Errorf("second admin status = %s, want active", got)
	}
}

func TestLeaveGate_AdministratorMayEndAnAdminWhileAnotherRemains(t *testing.T) {
	f := bootstrapOperator(t)
	s := f.s
	_, secondUUID := f.secondAdmin(t)

	s.do(t, http.MethodPatch, "/v1/profiles/"+secondUUID, f.adminTok.access(), map[string]string{"status": "ended", "end_reason": "test"}).
		wantStatus(t, http.StatusOK)
	if got := s.profileStatus(t, secondUUID); got != "ended" {
		t.Errorf("second admin status = %s, want ended", got)
	}
	// The remaining administrator is now the last: refused again.
	s.do(t, http.MethodPatch, "/v1/profiles/"+f.adminUUID, f.adminTok.access(), map[string]string{"status": "ended", "end_reason": "test"}).
		wantError(t, http.StatusConflict, "conflict", "users.last_operator_admin")
}

// Two administrators leaving at once cannot both succeed.
func TestLeaveGate_ConcurrentEndingsLeaveOneAdministrator(t *testing.T) {
	f := bootstrapOperator(t)
	s := f.s
	secondTok, secondUUID := f.secondAdmin(t)

	type call struct {
		tk   tokens
		uuid string
	}
	calls := []call{{f.adminTok, f.adminUUID}, {secondTok, secondUUID}}
	results := make([]*resp, len(calls))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = s.do(t, http.MethodPost, "/v1/me/profiles/"+c.uuid+"/leave", c.tk.access(), nil)
		}()
	}
	close(start)
	wg.Wait()

	var left, refused int
	for _, r := range results {
		switch r.status {
		case http.StatusNoContent:
			left++
		case http.StatusConflict:
			if _, detail := r.errCode(t); detail != "users.last_operator_admin" {
				t.Errorf("refusal detail = %q; body=%s", detail, r.raw)
			}
			refused++
		default:
			t.Errorf("unexpected status %d: %s", r.status, r.raw)
		}
	}
	if left != 1 || refused != 1 {
		t.Fatalf("left %d refused %d, want one of each", left, refused)
	}
}
