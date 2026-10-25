//go:build integration

package integration

// The audit vocabulary (decision U21): every observation mod-users emits uses
// an op and resource pair of API surface section 10.1, an acting profile that
// exists and whose holder is the observed principal, and never both a session
// and a credential. When mod-audit is linked the real observer runs in the
// group, so a pair outside its set would fail the operation itself.

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
)

// section101 is the op and resource pairs of API surface section 10.1 that the
// identity core emits. "update service_account" is the enable and disable of a
// service account (the table has no resource for it; recorded in the task that
// added it) and "create natural_person" is mod-core's person-record
// observation that the sign-up flows run through.
var section101 = map[string]bool{
	"create user": true, "update user": true,
	"create user_identifier": true, "update user_identifier": true, "delete user_identifier": true,
	"create credential": true, "update credential": true, "revoke credential": true,
	"login session": true, "update session": true, "revoke session": true,
	"create profile": true, "update profile": true,
	"delete credential_party_authorization": true,
	"create natural_person":                 true,
	"update service_account":                true,
}

// mod-audit's closed op set.
var auditOps = map[string]bool{"create": true, "update": true, "delete": true, "impersonate": true, "login": true, "grant": true, "revoke": true}

// checkObservations asserts every recorded observation is well formed and that
// the distinct pairs equal want (order-insensitive). With no want pairs it
// asserts that nothing was observed.
func (s *stack) checkObservations(t *testing.T, flow string, want ...string) {
	t.Helper()
	got := map[string]bool{}
	for _, o := range s.obs.all() {
		pair := o.Op + " " + o.Resource
		got[pair] = true
		if !auditOps[o.Op] {
			t.Errorf("%s: op %q is not in mod-audit's set (%s)", flow, o.Op, pair)
		}
		if !section101[pair] {
			t.Errorf("%s: pair %q is not in API surface section 10.1", flow, pair)
		}
		if !o.HasAuth || o.Auth.ProfileID == 0 || o.Auth.PrincipalID == 0 {
			t.Errorf("%s: %s observed without an acting profile and principal: %+v", flow, pair, o.Auth)
			continue
		}
		var holder int64
		if err := s.pool.QueryRow(context.Background(), `SELECT holder_id FROM profiles WHERE entity_id = $1`, o.Auth.ProfileID).Scan(&holder); err != nil {
			t.Errorf("%s: %s acting profile %d is not a profile: %v", flow, pair, o.Auth.ProfileID, err)
			continue
		}
		if holder != o.Auth.PrincipalID {
			t.Errorf("%s: %s principal %d is not the holder %d of profile %d", flow, pair, o.Auth.PrincipalID, holder, o.Auth.ProfileID)
		}
		if o.Auth.SessionID != 0 && o.Auth.CredentialID != 0 {
			t.Errorf("%s: %s carries both a session (%d) and a credential (%d)", flow, pair, o.Auth.SessionID, o.Auth.CredentialID)
		}
	}
	var pairs []string
	for p := range got {
		pairs = append(pairs, p)
	}
	sort.Strings(pairs)
	w := append([]string(nil), want...)
	sort.Strings(w)
	if strings.Join(pairs, ",") != strings.Join(w, ",") {
		t.Errorf("%s: observed pairs\n  %v\nwant\n  %v", flow, pairs, w)
	}
}

func TestAudit_Vocabulary_SignInFlows(t *testing.T) {
	// Registration.
	env.obs.reset()
	email := uniq("audit") + "@example.test"
	r := env.do(t, http.MethodPost, "/v1/auth/register", "", map[string]string{
		"email": email, "password": testPassword, "given_name": "Au", "family_name": "Dit",
	}).wantStatus(t, http.StatusCreated)
	a := account{Email: email, Password: testPassword, tk: r.tok(t)}
	env.checkObservations(t, "register",
		"create natural_person", "create user", "create user_identifier", "create credential", "login session")
	uid := env.principalID(t, email)
	profile := env.personalProfileID(t, uid)
	for _, o := range env.obs.all() {
		// Registration runs before any AuthContext exists: the flow installs the
		// new user's personal profile for each observation.
		if o.Auth.ProfileID != profile || o.Auth.PrincipalID != uid {
			t.Errorf("register: %s %s observed as profile %d principal %d, want the new personal profile %d of %d",
				o.Op, o.Resource, o.Auth.ProfileID, o.Auth.PrincipalID, profile, uid)
		}
		if o.Op == "login" && (o.Auth.SessionID == 0 || o.Auth.CredentialID != 0) {
			t.Errorf("register: login observed with %+v, want the new session and no credential", o.Auth)
		}
	}

	// Password login; a failed one writes nothing.
	env.obs.reset()
	env.loginRaw(t, email, "wrong-password-123", nil).wantStatus(t, http.StatusUnauthorized)
	env.checkObservations(t, "failed login")
	env.obs.reset()
	tk := env.login(t, a)
	env.checkObservations(t, "password login", "login session")

	// Email-code login.
	env.verifyRegisteredEmail(t, a)
	seen := env.mail.count(email)
	env.do(t, http.MethodPost, "/v1/auth/email-code/request", "", map[string]string{"email": email}).wantStatus(t, http.StatusNoContent)
	code := env.mail.codeAfter(t, email, seen)
	env.obs.reset()
	env.do(t, http.MethodPost, "/v1/auth/email-code/verify", "", map[string]string{"email": email, "code": code}).wantStatus(t, http.StatusOK)
	env.checkObservations(t, "email-code login", "login session")

	// Anonymous sign-in.
	env.obs.reset()
	env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": uniq("dev")}).wantStatus(t, http.StatusCreated)
	env.checkObservations(t, "anonymous", "create user", "create credential", "login session")

	// Rotation, fork and switch write nothing.
	env.obs.reset()
	rot := env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(tk)).wantStatus(t, http.StatusOK).tok(t)
	forked := env.do(t, http.MethodPost, "/v1/session/fork", rot.access(), nil).wantStatus(t, http.StatusOK).tok(t)
	env.do(t, http.MethodPost, "/v1/session/switch", forked.access(), map[string]string{"profile_uuid": forked.Profile.UUID}).wantStatus(t, http.StatusOK)
	env.checkObservations(t, "rotation, fork, switch")

	// Refresh reuse revokes the session.
	env.obs.reset()
	env.do(t, http.MethodPost, "/v1/auth/token", "", refreshReq(tk)).wantError(t, http.StatusUnauthorized, "users.refresh_reuse", "")
	env.checkObservations(t, "refresh reuse", "revoke session")

	// Logout.
	out := env.login(t, a)
	env.obs.reset()
	env.do(t, http.MethodPost, "/v1/session/logout", out.access(), nil).wantStatus(t, http.StatusNoContent)
	env.checkObservations(t, "logout", "revoke session")

	// Step-up.
	su := env.login(t, a)
	env.obs.reset()
	env.do(t, http.MethodPost, "/v1/session/step-up/password", su.access(), map[string]string{"password": a.Password}).wantStatus(t, http.StatusOK)
	env.checkObservations(t, "step-up", "update session")
}

func TestAudit_Vocabulary_CredentialAndIdentifierFlows(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)

	env.obs.reset()
	ssh := env.do(t, http.MethodPost, "/v1/me/credentials/ssh-keys", tk.access(), map[string]string{"public_key": newSSHPublicKey(t)}).
		wantStatus(t, http.StatusCreated).obj(t)
	env.checkObservations(t, "add ssh key", "create credential")

	env.obs.reset()
	env.do(t, http.MethodPatch, "/v1/me/credentials/"+ssh["uuid"].(string), tk.access(), map[string]string{"label": "renamed"}).wantStatus(t, http.StatusOK)
	env.checkObservations(t, "label change", "update credential")

	env.obs.reset()
	env.do(t, http.MethodDelete, "/v1/me/credentials/"+ssh["uuid"].(string), tk.access(), nil).wantStatus(t, http.StatusNoContent)
	env.checkObservations(t, "revoke ssh key", "revoke credential")

	env.obs.reset()
	second := uniq("second") + "@example.test"
	ident := env.do(t, http.MethodPost, "/v1/me/identifiers", tk.access(), map[string]string{"value": second}).wantStatus(t, http.StatusCreated).obj(t)
	env.checkObservations(t, "add identifier", "create user_identifier")

	id := ident["uuid"].(string)
	seen := env.mail.count(second)
	env.do(t, http.MethodPost, "/v1/me/identifiers/"+id+"/verification", tk.access(), nil).wantStatus(t, http.StatusNoContent)
	code := env.mail.codeAfter(t, second, seen)
	env.obs.reset()
	env.do(t, http.MethodPost, "/v1/me/identifiers/"+id+"/verify", tk.access(), map[string]string{"code": code}).wantStatus(t, http.StatusOK)
	env.checkObservations(t, "verify identifier", "update user_identifier", "create credential")

	env.obs.reset()
	env.do(t, http.MethodPut, "/v1/me/identifiers/"+id+"/primary", tk.access(), nil).wantStatus(t, http.StatusOK)
	env.checkObservations(t, "set primary identifier", "update user_identifier")

	// An unbound, unverified identifier can be removed.
	third := env.do(t, http.MethodPost, "/v1/me/identifiers", tk.access(), map[string]string{"value": uniq("third") + "@example.test"}).
		wantStatus(t, http.StatusCreated).obj(t)
	env.obs.reset()
	env.do(t, http.MethodDelete, "/v1/me/identifiers/"+third["uuid"].(string), tk.access(), nil).wantStatus(t, http.StatusNoContent)
	env.checkObservations(t, "remove identifier", "delete user_identifier")

	env.obs.reset()
	env.do(t, http.MethodPut, "/v1/me", tk.access(), map[string]string{"given_name": "Audited"}).wantStatus(t, http.StatusOK)
	got := map[string]bool{}
	for _, o := range env.obs.all() {
		got[o.Op+" "+o.Resource] = true
	}
	if !got["update user"] {
		t.Errorf("PUT /v1/me observed %v, want an update user", env.obs.pairs())
	}

	// Revoking the password credential (the email_otp keeps the account usable)
	// ends the session that held it: a credential and a session revocation.
	b := env.register(t)
	env.verifyRegisteredEmail(t, b)
	btk := env.login(t, b)
	pw := env.credentialUUID(t, btk.access(), "password")
	env.obs.reset()
	env.do(t, http.MethodDelete, "/v1/me/credentials/"+pw, btk.access(), nil).wantStatus(t, http.StatusNoContent)
	env.checkObservations(t, "revoke credential held by a session", "revoke credential", "revoke session")

	// Password reset supersedes the password.
	c := env.register(t)
	seen = env.mail.count(c.Email)
	env.do(t, http.MethodPost, "/v1/auth/password-reset/request", "", map[string]string{"email": c.Email}).wantStatus(t, http.StatusNoContent)
	token := env.resetTokenAfter(t, c.Email, seen)
	env.obs.reset()
	env.do(t, http.MethodPost, "/v1/auth/password-reset/confirm", "", map[string]string{"token": token, "password": "a-brand-new-password"}).wantStatus(t, http.StatusNoContent)
	env.checkObservations(t, "password reset", "revoke credential", "create credential", "revoke session")

	// Disabling a principal.
	admin := env.register(t)
	opTok, _ := env.makeOperator(t, admin)
	victim := env.register(t)
	env.obs.reset()
	env.do(t, http.MethodPost, "/v1/principals/"+env.entityUUID(t, env.principalID(t, victim.Email))+"/disable", opTok.access(),
		map[string]string{"reason": "audit"}).wantStatus(t, http.StatusOK)
	env.checkObservations(t, "disable principal", "update user", "revoke session")
}

func TestAudit_Vocabulary_OIDCJustInTime(t *testing.T) {
	provider := env.addProvider(t, config.Provider{JITMode: config.JITModeBYO})
	env.idpReturns(t, idpIdentity(uniq("audit-jit")+"@example.test", true), auth.StatePayload{})
	env.obs.reset()
	env.signIn(t, provider)
	env.checkObservations(t, "oidc jit byo",
		"create natural_person", "create user", "create user_identifier", "create credential", "login session")

	party := env.seedParty(t, "Audit Managed "+uniq("p"))
	managed := env.addProvider(t, config.Provider{JITMode: config.JITModeManaged, PartyID: party})
	env.idpReturns(t, idpIdentity(uniq("audit-mjit")+"@example.test", true), auth.StatePayload{})
	env.obs.reset()
	env.signIn(t, managed)
	env.checkObservations(t, "oidc jit managed", "create user", "create credential", "login session")

	// A second sign-in on the known path observes only the session.
	env.obs.reset()
	env.signIn(t, managed)
	env.checkObservations(t, "oidc known identity", "login session")
}

// TestAudit_RealObserverAcceptsTheRows runs when mod-audit is linked: the
// sign-in rows were accepted by the real audit_log (CHECKs and the principal
// trigger), and each names the profile and holder of the flow.
func TestAudit_RealObserverAcceptsTheRows(t *testing.T) {
	if !env.auditApplied {
		t.Skip("mod-audit is not linked: audit_log was not applied")
	}
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	profile := env.personalProfileID(t, uid)

	rows, err := env.pool.Query(context.Background(),
		`SELECT op, resource, actor_entity_id, principal_id, session_id, credential_id FROM audit_log WHERE principal_id = $1 ORDER BY id`, uid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var logins int
	for rows.Next() {
		var op, resource string
		var actor, principal int64
		var session, cred *int64
		if err := rows.Scan(&op, &resource, &actor, &principal, &session, &cred); err != nil {
			t.Fatal(err)
		}
		if actor != profile || principal != uid {
			t.Errorf("audit row %s %s: actor %d principal %d, want profile %d of %d", op, resource, actor, principal, profile, uid)
		}
		if session != nil && cred != nil {
			t.Errorf("audit row %s %s carries both a session and a credential", op, resource)
		}
		if op == "login" && resource == "session" {
			logins++
			if session == nil {
				t.Errorf("login row has no session id")
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if logins != 1 {
		t.Errorf("login rows for the new user = %d, want 1", logins)
	}
}
