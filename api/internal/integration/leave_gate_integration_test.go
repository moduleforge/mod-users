//go:build integration

package integration

// The holder's leave path is behind the party gate, and the last active
// operator administrator cannot be ended, by leaving or by an administrator.

import (
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	svc "github.com/moduleforge/mod-users/api/internal/service"
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

// suspendedLeaveFixture is a member holding a suspended profile in a party,
// with the email credential authorized for the party and confined to it, and a
// password-only session: the state in which the party gate decides whether the
// holder may leave.
type suspendedLeaveFixture struct {
	g         gateParty
	m         gateMember
	personal  tokens
	emailUUID string
	pwUUID    string
	leave     string
}

// authorizePassword also authorizes the password credential (while the profile
// is still active: authorizations need an active profile), so the gate is met.
func newSuspendedLeaveFixture(t *testing.T, policy map[string]any, authorizePassword bool) suspendedLeaveFixture {
	t.Helper()
	g := env.newGateParty(t)
	m := env.newGateMember(t, g)
	env.verifyRegisteredEmail(t, m.acct)
	f := suspendedLeaveFixture{
		g: g, m: m,
		pwUUID:    env.credentialUUID(t, m.acct.tk.access(), "password"),
		emailUUID: env.credentialUUID(t, m.acct.tk.access(), "email_otp"),
		leave:     "/v1/me/profiles/" + m.profUUID + "/leave",
	}
	env.setPolicy(t, g, policy).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodPut, authPath(g, f.emailUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	if authorizePassword {
		env.do(t, http.MethodPut, authPath(g, f.pwUUID), g.adminTok.access(), nil).wantStatus(t, http.StatusOK)
	}
	env.exec(t, "confine email credential",
		`UPDATE mod_users.credentials SET confined_to_party_id = $2 WHERE uuid = $1::uuid`, f.emailUUID, g.id)
	f.personal = env.login(t, m.acct)
	env.exec(t, "suspend profile", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, m.profileID)
	return f
}

// wantUnchanged fails unless the suspended profile, its authorization and its
// confined credential are as the fixture left them.
func (f suspendedLeaveFixture) wantUnchanged(t *testing.T) {
	t.Helper()
	if got := env.profileStatus(t, f.m.profUUID); got != "suspended" {
		t.Errorf("status after a refused leave = %s, want suspended", got)
	}
	if n := env.authorizationCount(t, f.emailUUID, f.g.id); n != 1 {
		t.Errorf("authorizations after a refused leave = %d, want 1", n)
	}
	if got := env.confinedTo(t, f.emailUUID); got != f.g.id {
		t.Errorf("confinement after a refused leave = %d, want %d", got, f.g.id)
	}
	if n := env.scanInt64(t, "revoked count",
		`SELECT count(*) FROM mod_users.credentials WHERE uuid = $1::uuid AND revoked_at IS NOT NULL`, f.emailUUID); n != 0 {
		t.Errorf("confined credential revoked by a refused leave")
	}
}

// A suspended profile is not exempt from the party gate on leave: a session the
// gate locks out is refused with the gate's reason, and nothing changes.
func TestLeaveGate_SuspendedProfileLockedSessionCannotLeave(t *testing.T) {
	// An unauthorized password under the 'authorized' rule.
	f := newSuspendedLeaveFixture(t, map[string]any{"credential_rule": "authorized"}, false)
	env.do(t, http.MethodPost, f.leave, f.personal.access(), nil).
		wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "credential_not_authorized")
	f.wantUnchanged(t)

	// A policy requiring an OIDC provider the session does not hold.
	prov := env.addProviderRow(t, uniq("leave-prov"))
	f = newSuspendedLeaveFixture(t, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": prov}, false)
	env.do(t, http.MethodPost, f.leave, f.personal.access(), nil).
		wantError(t, http.StatusUnauthorized, "users.profile_unavailable", "provider_required")
	f.wantUnchanged(t)
}

// A suspended holder whose gate is met can still leave, and the departure
// cleanup runs.
func TestLeaveGate_SuspendedProfileGateMetCanLeave(t *testing.T) {
	f := newSuspendedLeaveFixture(t, map[string]any{"credential_rule": "authorized"}, true)

	env.do(t, http.MethodPost, f.leave, f.personal.access(), nil).wantStatus(t, http.StatusNoContent)
	if got := env.profileStatus(t, f.m.profUUID); got != "ended" {
		t.Errorf("status after leave = %s, want ended", got)
	}
	if n := env.authorizationCount(t, f.emailUUID, f.g.id); n != 0 {
		t.Errorf("authorizations after leave = %d, want 0 (departure cleanup)", n)
	}
	if n := env.scanInt64(t, "revoked count",
		`SELECT count(*) FROM mod_users.credentials WHERE uuid = $1::uuid AND revoked_at IS NOT NULL`, f.emailUUID); n != 1 {
		t.Errorf("confined credential not revoked by the departure cleanup")
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
// An administrator whose principal is disabled is not a usable administrator,
// so it does not count toward the remaining administrators.
func TestLeaveGate_DisabledPrincipalAdminDoesNotCountAsRemaining(t *testing.T) {
	f := bootstrapOperator(t)
	s := f.s
	_, secondUUID := f.secondAdmin(t)
	s.exec(t, "disable second admin principal",
		`UPDATE principals SET disabled_at = now() WHERE entity_id = (SELECT p.holder_id FROM profiles p JOIN entities e ON e.id = p.entity_id WHERE e.uuid = $1::uuid)`, secondUUID)

	leave := "/v1/me/profiles/" + f.adminUUID + "/leave"
	s.do(t, http.MethodPost, leave, f.adminTok.access(), nil).wantError(t, http.StatusConflict, "conflict", "users.last_operator_admin")
	s.do(t, http.MethodPatch, "/v1/profiles/"+f.adminUUID, f.adminTok.access(), map[string]string{"status": "ended", "end_reason": "test"}).
		wantError(t, http.StatusConflict, "conflict", "users.last_operator_admin")
	if got := s.profileStatus(t, f.adminUUID); got != "active" {
		t.Fatalf("admin status after refused endings = %s, want active", got)
	}
}

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

// Suspending the last active operator administrator is refused; with a second
// administrator present, suspending one succeeds.
func TestLeaveGate_LastOperatorAdminCannotBeSuspended(t *testing.T) {
	f := bootstrapOperator(t)
	s := f.s
	suspend := map[string]string{"status": "suspended"}

	s.do(t, http.MethodPatch, "/v1/profiles/"+f.adminUUID, f.adminTok.access(), suspend).
		wantError(t, http.StatusConflict, "conflict", "users.last_operator_admin")
	if got := s.profileStatus(t, f.adminUUID); got != "active" {
		t.Fatalf("admin status after refused suspension = %s, want active", got)
	}

	_, secondUUID := f.secondAdmin(t)
	s.do(t, http.MethodPatch, "/v1/profiles/"+secondUUID, f.adminTok.access(), suspend).wantStatus(t, http.StatusOK)
	if got := s.profileStatus(t, secondUUID); got != "suspended" {
		t.Errorf("second admin status = %s, want suspended", got)
	}
	// The remaining administrator is now the last: refused again.
	s.do(t, http.MethodPatch, "/v1/profiles/"+f.adminUUID, f.adminTok.access(), suspend).
		wantError(t, http.StatusConflict, "conflict", "users.last_operator_admin")
}

// The exported EndInTx primitive applies the same guard as the HTTP paths.
func TestLeaveGate_EndInTxRefusesTheLastOperatorAdmin(t *testing.T) {
	f := bootstrapOperator(t)
	s := f.s

	ctx := s.authCtx(t, f.adminTok.access())
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return s.profiles.EndInTx(ctx, tx, f.adminProf, "test")
	})
	if !errors.Is(err, svc.ErrLastOperatorAdmin) {
		t.Fatalf("EndInTx err = %v, want ErrLastOperatorAdmin (users.last_operator_admin)", err)
	}
	if got := s.profileStatus(t, f.adminUUID); got != "active" {
		t.Errorf("admin status after refused EndInTx = %s, want active", got)
	}
}

// An ending (through the exported EndInTx) and a suspension (over HTTP) of the
// last two administrators serialize: they cannot both commit.
func TestLeaveGate_ConcurrentEndAndSuspendLeaveOneAdministrator(t *testing.T) {
	f := bootstrapOperator(t)
	s := f.s
	_, secondUUID := f.secondAdmin(t)
	secondProf := s.scanInt64(t, "second admin profile", `SELECT id FROM entities WHERE uuid = $1`, secondUUID)

	ctx := s.authCtx(t, f.adminTok.access())
	var endErr error
	var suspended *resp
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		endErr = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			return s.profiles.EndInTx(ctx, tx, secondProf, "test")
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		suspended = s.do(t, http.MethodPatch, "/v1/profiles/"+f.adminUUID, f.adminTok.access(), map[string]string{"status": "suspended"})
	}()
	close(start)
	wg.Wait()

	endOK := endErr == nil
	suspendOK := suspended.status == http.StatusOK
	if endOK == suspendOK {
		t.Fatalf("ended ok=%v (err %v), suspended ok=%v (status %d: %s); want exactly one to succeed", endOK, endErr, suspendOK, suspended.status, suspended.raw)
	}
	if !endOK && !errors.Is(endErr, svc.ErrLastOperatorAdmin) {
		t.Errorf("EndInTx err = %v, want ErrLastOperatorAdmin", endErr)
	}
	if !suspendOK && suspended.status != http.StatusConflict {
		t.Errorf("suspend status = %d: %s, want 409", suspended.status, suspended.raw)
	}
}

// Disabling one operator administrator's principal while the other
// administrator is suspended in a concurrent transaction cannot leave the
// operator party with no effective administrator: the two serialize on the
// platform_settings row lock and the second is refused (409) or loses its
// session to the first.
func TestLeaveGate_ConcurrentDisableAndSuspendLeaveAnEffectiveAdministrator(t *testing.T) {
	f := bootstrapOperator(t)
	s := f.s
	secondTok, secondUUID := f.secondAdmin(t)
	secondProf := s.scanInt64(t, "second admin profile",
		`SELECT p.entity_id FROM profiles p JOIN entities e ON e.id = p.entity_id WHERE e.uuid = $1::uuid`, secondUUID)
	secondHolder := s.scanInt64(t, "second admin holder", `SELECT holder_id FROM profiles WHERE entity_id = $1`, secondProf)

	// The first administrator disables the second's principal while the second
	// suspends the first's profile.
	results := make([]*resp, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		results[0] = s.do(t, http.MethodPost, "/v1/principals/"+s.entityUUID(t, secondHolder)+"/disable",
			f.adminTok.access(), map[string]string{"reason": "race"})
	}()
	go func() {
		defer wg.Done()
		<-start
		results[1] = s.do(t, http.MethodPatch, "/v1/profiles/"+f.adminUUID, secondTok.access(), map[string]string{"status": "suspended"})
	}()
	close(start)
	wg.Wait()

	if ok0, ok1 := results[0].status/100 == 2, results[1].status/100 == 2; ok0 && ok1 {
		t.Fatalf("both the disable (%d) and the suspension (%d) succeeded; bodies: %s / %s",
			results[0].status, results[1].status, results[0].raw, results[1].raw)
	}
	if n := s.scanInt64(t, "effective administrators",
		`SELECT count(*) FROM profiles p JOIN principals pr ON pr.entity_id = p.holder_id AND pr.disabled_at IS NULL
		  WHERE p.party_id = $1 AND p.status = 'active'`, f.partyID); n < 1 {
		t.Fatalf("effective operator administrators = %d, want at least 1", n)
	}
}
