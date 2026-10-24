//go:build integration

package integration

// Credential and identifier flows over the composed stack: password, email
// OTP (created on identifier verification), SSH keys, the PUT /v1/me shape,
// self-service freshness and the guest upgrade.

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/moduleforge/mod-users/api/internal/service"
)

// resetTokenAfter waits for the (seen+1)th mail to the address and returns the
// token in its reset link.
func (s *stack) resetTokenAfter(t *testing.T, to string, seen int) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.mail.mu.Lock()
		var got []mail
		for _, m := range s.mail.msgs {
			if m.To == to {
				got = append(got, m)
			}
		}
		s.mail.mu.Unlock()
		if len(got) > seen {
			body := got[len(got)-1].Body
			i := strings.Index(body, "token=")
			if i < 0 {
				t.Fatalf("no reset link in %q", body)
			}
			rest := body[i+len("token="):]
			if j := strings.IndexAny(rest, " \n"); j >= 0 {
				rest = rest[:j]
			}
			tok, err := url.QueryUnescape(rest)
			if err != nil {
				t.Fatal(err)
			}
			return tok
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no reset mail to %s", to)
	return ""
}

func TestPassword_WrongPasswordAndUnknownEmailAreIndistinguishable(t *testing.T) {
	a := env.register(t)
	wrong := env.loginRaw(t, a.Email, "definitely-not-it-1234", nil)
	unknown := env.loginRaw(t, uniq("nobody")+"@example.test", "whatever-password-1", nil)
	wrong.wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
	unknown.wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
	if string(wrong.raw) != string(unknown.raw) {
		t.Errorf("responses differ:\n%s\n%s", wrong.raw, unknown.raw)
	}
}

func TestPassword_SecondPasswordOnTheSameIdentifierIsRefused(t *testing.T) {
	a := env.register(t)
	env.do(t, http.MethodPost, "/v1/me/credentials/password", a.tk.access(), map[string]string{"password": "another-long-password"}).
		wantError(t, http.StatusConflict, "conflict", "users.password_exists")
}

func TestPassword_ResetSupersedesRevokesSessionsAndIsSingleUse(t *testing.T) {
	a := env.register(t)
	live := env.login(t, a)
	seen := env.mail.count(a.Email)

	env.do(t, http.MethodPost, "/v1/auth/password-reset/request", "", map[string]string{"email": a.Email}).wantStatus(t, http.StatusNoContent)
	token := env.resetTokenAfter(t, a.Email, seen)

	// A bad token and a weak password are refused before anything changes.
	env.do(t, http.MethodPost, "/v1/auth/password-reset/confirm", "", map[string]string{"token": "nope", "password": "a-new-long-password"}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
	env.do(t, http.MethodPost, "/v1/auth/password-reset/confirm", "", map[string]string{"token": token, "password": "short"}).
		wantStatus(t, http.StatusBadRequest)

	const newPassword = "a-brand-new-password"
	env.do(t, http.MethodPost, "/v1/auth/password-reset/confirm", "", map[string]string{"token": token, "password": newPassword}).
		wantStatus(t, http.StatusNoContent)

	// The token is spent.
	env.do(t, http.MethodPost, "/v1/auth/password-reset/confirm", "", map[string]string{"token": token, "password": newPassword}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")

	// The old password is gone, the new one signs in, and sessions that held
	// the old password were revoked.
	env.loginRaw(t, a.Email, a.Password, nil).wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
	env.loginRaw(t, a.Email, newPassword, nil).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodGet, "/v1/probe", live.access(), nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
}

func TestPassword_ResetRequestAnswersTheSameForUnknownEmails(t *testing.T) {
	a := env.register(t)
	for _, email := range []string{a.Email, uniq("ghost") + "@example.test"} {
		start := time.Now()
		env.do(t, http.MethodPost, "/v1/auth/password-reset/request", "", map[string]string{"email": email}).wantStatus(t, http.StatusNoContent)
		if time.Since(start) < 150*time.Millisecond {
			t.Errorf("reset request for %s answered in %v, want at least the anti-enumeration floor", email, time.Since(start))
		}
	}
}

func TestEmailOTP_IsCreatedOnIdentifierVerification(t *testing.T) {
	a := env.register(t)

	// Before verification there is no email_otp credential, so a code sign-in
	// is refused and nothing is mailed.
	var before struct {
		Credentials []struct{ Kind string } `json:"credentials"`
	}
	env.do(t, http.MethodGet, "/v1/me/credentials", a.tk.access(), nil).wantStatus(t, http.StatusOK).into(t, &before)
	for _, c := range before.Credentials {
		if c.Kind == "email_otp" {
			t.Fatalf("email_otp credential exists before verification: %+v", before.Credentials)
		}
	}
	env.mail.codeAfter(t, a.Email, 0) // the registration verification mail
	sent := env.mail.count(a.Email)
	env.do(t, http.MethodPost, "/v1/auth/email-code/request", "", map[string]string{"email": a.Email}).wantStatus(t, http.StatusNoContent)
	time.Sleep(300 * time.Millisecond)
	if got := env.mail.count(a.Email); got != sent {
		t.Fatalf("a login code was mailed for an unverified email (%d -> %d messages)", sent, got)
	}
	env.do(t, http.MethodPost, "/v1/auth/email-code/verify", "", map[string]string{"email": a.Email, "code": "123456"}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")

	// Verifying the identifier creates the credential.
	env.verifyRegisteredEmail(t, a)
	_ = env.credentialUUID(t, a.tk.access(), "email_otp")

	// Now the code sign-in works. The credential's amr is otp.
	seen := env.mail.count(a.Email)
	env.do(t, http.MethodPost, "/v1/auth/email-code/request", "", map[string]string{"email": a.Email}).wantStatus(t, http.StatusNoContent)
	code := env.mail.codeAfter(t, a.Email, seen)
	env.do(t, http.MethodPost, "/v1/auth/email-code/verify", "", map[string]string{"email": a.Email, "code": wrongCode(code)}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
	tk := env.do(t, http.MethodPost, "/v1/auth/email-code/verify", "", map[string]string{"email": a.Email, "code": code}).
		wantStatus(t, http.StatusOK).tok(t)
	if tk.Session == nil || len(tk.Session.AMR) != 1 || tk.Session.AMR[0] != "otp" || tk.Session.ACR != 1 {
		t.Errorf("session = %+v, want amr [otp] acr 1", tk.Session)
	}
	// The code is single use.
	env.do(t, http.MethodPost, "/v1/auth/email-code/verify", "", map[string]string{"email": a.Email, "code": code}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
}

func wrongCode(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}

func TestEmailOTP_CodeIsExhaustedAfterFiveWrongGuesses(t *testing.T) {
	a := env.register(t)
	env.verifyRegisteredEmail(t, a)
	seen := env.mail.count(a.Email)
	env.do(t, http.MethodPost, "/v1/auth/email-code/request", "", map[string]string{"email": a.Email}).wantStatus(t, http.StatusNoContent)
	code := env.mail.codeAfter(t, a.Email, seen)

	for i := 0; i < service.MaxCodeAttempts; i++ {
		env.do(t, http.MethodPost, "/v1/auth/email-code/verify", "", map[string]string{"email": a.Email, "code": wrongCode(code)}).
			wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
	}
	// Even the right code no longer works once the attempts are used up.
	env.do(t, http.MethodPost, "/v1/auth/email-code/verify", "", map[string]string{"email": a.Email, "code": code}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")

	// A second request inside the cooldown mints and mails nothing.
	count := env.mail.count(a.Email)
	env.do(t, http.MethodPost, "/v1/auth/email-code/request", "", map[string]string{"email": a.Email}).wantStatus(t, http.StatusNoContent)
	time.Sleep(300 * time.Millisecond)
	if got := env.mail.count(a.Email); got != count {
		t.Errorf("a code was mailed inside the cooldown (%d -> %d)", count, got)
	}
}

func TestEmailCode_OnlyTheLoginPurposeIsServed(t *testing.T) {
	a := env.register(t)
	env.verifyRegisteredEmail(t, a)
	sent := env.mail.count(a.Email)
	env.do(t, http.MethodPost, "/v1/auth/email-code/request", "", map[string]string{"email": a.Email, "purpose": "verify_email"}).
		wantStatus(t, http.StatusNoContent)
	time.Sleep(300 * time.Millisecond)
	if got := env.mail.count(a.Email); got != sent {
		t.Errorf("a code was mailed for a non-login purpose")
	}
	env.do(t, http.MethodPost, "/v1/auth/email-code/verify", "", map[string]string{"email": a.Email, "code": "123456", "purpose": "verify_email"}).
		wantError(t, http.StatusBadRequest, "invalid_input", "users.purpose_invalid")
}

func TestIdentifiers_AddVerifySetPrimaryAndRemove(t *testing.T) {
	a := env.register(t)
	second := uniq("second") + "@example.test"
	added := env.do(t, http.MethodPost, "/v1/me/identifiers", a.tk.access(), map[string]string{"value": second}).
		wantStatus(t, http.StatusCreated).obj(t)
	id := added["uuid"].(string)
	if added["verified"] != false || added["is_primary"] != false {
		t.Fatalf("new identifier = %v, want unverified and not primary", added)
	}

	// An email that another account owns is a 409.
	other := env.register(t)
	env.do(t, http.MethodPost, "/v1/me/identifiers", a.tk.access(), map[string]string{"value": other.Email}).
		wantError(t, http.StatusConflict, "conflict", "")

	// Verification: request, wrong code (400 field error), right code.
	seen := env.mail.count(second)
	env.do(t, http.MethodPost, "/v1/me/identifiers/"+id+"/verification", a.tk.access(), nil).wantStatus(t, http.StatusNoContent)
	code := env.mail.codeAfter(t, second, seen)
	env.do(t, http.MethodPost, "/v1/me/identifiers/"+id+"/verify", a.tk.access(), map[string]string{"code": wrongCode(code)}).
		wantError(t, http.StatusBadRequest, "invalid_input", "users.code_invalid")
	verified := env.do(t, http.MethodPost, "/v1/me/identifiers/"+id+"/verify", a.tk.access(), map[string]string{"code": code}).
		wantStatus(t, http.StatusOK).obj(t)
	if verified["verified"] != true {
		t.Fatalf("identifier after verification = %v", verified)
	}
	// Verification creates the email_otp credential bound to that identifier.
	if n := env.scanInt64(t, "email_otp count",
		`SELECT count(*) FROM mod_users.cred_email_otp o JOIN mod_users.user_identifiers i ON i.id = o.identifier_id
		 WHERE i.uuid = $1::uuid`, id); n != 1 {
		t.Errorf("email_otp credentials for the identifier = %d, want 1", n)
	}

	// Make it primary, then remove the original (a password is bound to it, so
	// the schema keeps it: removal is refused).
	primary := env.do(t, http.MethodPut, "/v1/me/identifiers/"+id+"/primary", a.tk.access(), nil).wantStatus(t, http.StatusOK).obj(t)
	if primary["is_primary"] != true {
		t.Fatalf("identifier after set-primary = %v", primary)
	}
	orig := env.identifierUUID(t, a.tk.access(), a.Email)
	env.do(t, http.MethodDelete, "/v1/me/identifiers/"+orig, a.tk.access(), nil).
		wantError(t, http.StatusConflict, "conflict", "users.identifier_in_use")
}

func TestSSHKey_AddListRevokeAndResolve(t *testing.T) {
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	pub, line := newSSHKey(t)

	view := env.do(t, http.MethodPost, "/v1/me/credentials/ssh-keys", a.tk.access(), map[string]any{"public_key": line, "label": "laptop"}).
		wantStatus(t, http.StatusCreated).obj(t)
	if view["kind"] != "ssh_key" || view["label"] != "laptop" || view["fingerprint"] == "" {
		t.Fatalf("ssh key view = %v", view)
	}
	credUUID := view["uuid"].(string)

	// The same key twice is a conflict; garbage is a 400.
	env.do(t, http.MethodPost, "/v1/me/credentials/ssh-keys", a.tk.access(), map[string]string{"public_key": line}).
		wantStatus(t, http.StatusConflict)
	env.do(t, http.MethodPost, "/v1/me/credentials/ssh-keys", a.tk.access(), map[string]string{"public_key": "not a key"}).
		wantStatus(t, http.StatusBadRequest)

	// The resolver finds it and builds the holder's AuthContext for a user key.
	r := service.NewSSHKeyResolver(env.q, env.coreQ)
	cred, err := r.ResolveCredential(context.Background(), pub)
	if err != nil {
		t.Fatalf("ResolveCredential: %v", err)
	}
	if cred.PrincipalID != uid {
		t.Fatalf("resolved principal %d, want %d", cred.PrincipalID, uid)
	}
	ac, err := r.AuthContextForOwner(context.Background(), cred, nil)
	if err != nil {
		t.Fatalf("AuthContextForOwner: %v", err)
	}
	if ac.PrincipalID != uid || ac.ProfileID != env.personalProfileID(t, uid) || ac.CredentialID != cred.CredentialID || ac.SessionID != 0 {
		t.Errorf("AuthContext = %+v, want principal %d acting as its personal profile via credential %d, no session", ac, uid, cred.CredentialID)
	}

	// Revoking the credential makes the key unknown on the next lookup.
	env.do(t, http.MethodDelete, "/v1/me/credentials/"+credUUID, a.tk.access(), nil).wantStatus(t, http.StatusNoContent)
	if _, err := r.ResolveCredential(context.Background(), pub); err == nil {
		t.Fatal("a revoked key still resolves")
	}
}

func TestMePut_UpdatesTheCallersOwnRecord(t *testing.T) {
	a := env.register(t)
	before := env.do(t, http.MethodGet, "/v1/me", a.tk.access(), nil).wantStatus(t, http.StatusOK).obj(t)
	if before["given_name"] != "Test" || before["family_name"] != "User" || before["is_guest"] != false || before["email"] != a.Email {
		t.Fatalf("GET /v1/me = %v", before)
	}
	env.obs.reset()

	after := env.do(t, http.MethodPut, "/v1/me", a.tk.access(), map[string]any{"given_name": "Renamed", "family_name": "Person"}).
		wantStatus(t, http.StatusOK).obj(t)
	if after["given_name"] != "Renamed" || after["family_name"] != "Person" || after["uuid"] != before["uuid"] {
		t.Fatalf("PUT /v1/me = %v", after)
	}
	if after["display_name"] == before["display_name"] {
		t.Errorf("display_name did not follow the rename: %v", after["display_name"])
	}
	if got := env.obs.pairs(); len(got) == 0 {
		t.Errorf("PUT /v1/me wrote no observation")
	}
	for _, p := range env.obs.pairs() {
		if p != "update user" && p != "update natural_person" {
			t.Errorf("PUT /v1/me observed %q", p)
		}
	}

	// A partial body leaves the other names alone; null clears a default.
	part := env.do(t, http.MethodPut, "/v1/me", a.tk.access(), map[string]any{"given_name": "OnlyGiven", "default_profile_uuid": nil}).
		wantStatus(t, http.StatusOK).obj(t)
	if part["given_name"] != "OnlyGiven" || part["family_name"] != "Person" || part["default_profile_uuid"] != nil {
		t.Fatalf("partial PUT = %v", part)
	}

	// Stateless shape errors: 400 with field details.
	for name, body := range map[string]string{
		"blank given name": `{"given_name":"  "}`,
		"unknown field":    `{"email":"x@example.test"}`,
		"bad uuid":         `{"default_profile_uuid":"nope"}`,
		"malformed":        `{"given_name":`,
	} {
		if r := env.do(t, http.MethodPut, "/v1/me", a.tk.access(), body); r.status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, r.status, r.raw)
		}
	}
	// A profile the caller does not hold cannot become the default.
	r := env.do(t, http.MethodPut, "/v1/me", a.tk.access(), map[string]any{"default_profile_uuid": "11111111-1111-1111-1111-111111111111"})
	r.wantError(t, http.StatusNotFound, "not_found", "")

	// Anonymous: 401 first.
	env.do(t, http.MethodPut, "/v1/me", "", `{"given_name":`).wantStatus(t, http.StatusUnauthorized)
}

func TestSelfService_WritesAreRefusedPastMaxAuthAge(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)
	env.exec(t, "age auth_time",
		`UPDATE mod_users.sessions SET auth_time = now() - $2::interval WHERE uuid = $1::uuid`,
		tk.Session.UUID, (env.cfg.Auth.SelfServiceMaxAuthAge + time.Minute).String())
	other := env.login(t, a)
	_ = other
	env.exec(t, "age only the first", `UPDATE mod_users.sessions SET auth_time = now() WHERE uuid = $1::uuid`, other.Session.UUID)

	// Reads are unaffected.
	env.do(t, http.MethodGet, "/v1/me", tk.access(), nil).wantStatus(t, http.StatusOK)
	env.do(t, http.MethodGet, "/v1/me/credentials", tk.access(), nil).wantStatus(t, http.StatusOK)

	cred := env.credentialUUID(t, tk.access(), "password")
	writes := []struct {
		name, method, path string
		body               any
	}{
		{"PUT /v1/me", http.MethodPut, "/v1/me", map[string]string{"given_name": "X"}},
		{"add identifier", http.MethodPost, "/v1/me/identifiers", map[string]string{"value": uniq("late") + "@example.test"}},
		{"add ssh key", http.MethodPost, "/v1/me/credentials/ssh-keys", map[string]string{"public_key": newSSHPublicKey(t)}},
		{"patch credential", http.MethodPatch, "/v1/me/credentials/" + cred, map[string]string{"label": "x"}},
		{"delete credential", http.MethodDelete, "/v1/me/credentials/" + cred, nil},
		{"delete session", http.MethodDelete, "/v1/me/sessions/" + other.Session.UUID, nil},
	}
	for _, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			// The response is the shared apiresp action-required envelope as
			// rendered by WriteError: status 409, action code step_up_required.
			env.do(t, w.method, w.path, tk.access(), w.body).wantAction(t, http.StatusConflict, "users.step_up_required")
		})
	}
	// Shape errors still come first: a malformed body is 400 even when stale.
	env.do(t, http.MethodPut, "/v1/me", tk.access(), `{"given_name":`).wantStatus(t, http.StatusBadRequest)

	// The other, fresh session may write.
	env.do(t, http.MethodPut, "/v1/me", other.access(), map[string]string{"given_name": "Fresh"}).wantStatus(t, http.StatusOK)

	// After a step-up the stale session may write too.
	up := env.do(t, http.MethodPost, "/v1/session/step-up/password", tk.access(), map[string]string{"password": a.Password}).
		wantStatus(t, http.StatusOK).tok(t)
	env.do(t, http.MethodPut, "/v1/me", up.access(), map[string]string{"given_name": "AfterStepUp"}).wantStatus(t, http.StatusOK)
}

func TestSelfService_LastInteractiveCredentialCannotBeRemoved(t *testing.T) {
	a := env.register(t)
	pw := env.credentialUUID(t, a.tk.access(), "password")
	env.do(t, http.MethodDelete, "/v1/me/credentials/"+pw, a.tk.access(), nil).
		wantError(t, http.StatusConflict, "conflict", "users.last_credential")
	// Nothing changed: the session and the password still work.
	if status, _ := env.probe(t, "/v1/probe", a.tk.access()); status != http.StatusOK {
		t.Fatalf("session after the refused removal = %d", status)
	}
	// An SSH key is not an interactive credential, so adding one does not free the password.
	env.do(t, http.MethodPost, "/v1/me/credentials/ssh-keys", a.tk.access(), map[string]string{"public_key": newSSHPublicKey(t)}).
		wantStatus(t, http.StatusCreated)
	env.do(t, http.MethodDelete, "/v1/me/credentials/"+pw, a.tk.access(), nil).
		wantError(t, http.StatusConflict, "conflict", "users.last_credential")
}

func TestSessions_ListAndRevokeAnother(t *testing.T) {
	a := env.register(t)
	tk := env.login(t, a)
	other := env.login(t, a)

	var list struct {
		Sessions []struct {
			UUID    string `json:"uuid"`
			Current bool   `json:"current"`
		} `json:"sessions"`
	}
	env.do(t, http.MethodGet, "/v1/me/sessions", tk.access(), nil).wantStatus(t, http.StatusOK).into(t, &list)
	var sawCurrent, sawOther bool
	for _, s := range list.Sessions {
		if s.UUID == tk.Session.UUID && s.Current {
			sawCurrent = true
		}
		if s.UUID == other.Session.UUID && !s.Current {
			sawOther = true
		}
	}
	if !sawCurrent || !sawOther {
		t.Fatalf("sessions = %+v, want the current one marked and the other listed", list.Sessions)
	}

	env.do(t, http.MethodDelete, "/v1/me/sessions/"+other.Session.UUID, tk.access(), nil).wantStatus(t, http.StatusNoContent)
	env.do(t, http.MethodGet, "/v1/probe", other.access(), nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
	if status, _ := env.probe(t, "/v1/probe", tk.access()); status != http.StatusOK {
		t.Fatalf("the revoking session = %d", status)
	}
	// Another principal's session is not visible: 404.
	stranger := env.register(t)
	env.do(t, http.MethodDelete, "/v1/me/sessions/"+stranger.tk.Session.UUID, tk.access(), nil).wantStatus(t, http.StatusNotFound)
}

func TestGuest_AnonymousSignInAndUpgrade(t *testing.T) {
	deviceID := uniq("device")
	g := env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": deviceID}).wantStatus(t, http.StatusCreated)
	var guest tokens
	g.into(t, &guest)
	if guest.DeviceToken == "" || guest.AccessToken == nil {
		t.Fatalf("guest response = %s", g.raw)
	}
	me := env.do(t, http.MethodGet, "/v1/me", guest.access(), nil).wantStatus(t, http.StatusOK).obj(t)
	if me["is_guest"] != true || me["display_name"] != "Guest" {
		t.Fatalf("guest /v1/me = %v", me)
	}

	// The device token signs the guest back in; a wrong device id or token does not.
	back := env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": deviceID, "device_token": guest.DeviceToken})
	back.wantStatus(t, http.StatusOK)
	env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": "other-device", "device_token": guest.DeviceToken}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")
	env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": deviceID, "device_token": "wrong"}).
		wantError(t, http.StatusUnauthorized, "users.invalid_credentials", "")

	// A guest cannot link a provider or add a password before upgrading, and an
	// upgrade request is validated first.
	env.do(t, http.MethodPost, "/v1/me/upgrade", guest.access(), map[string]string{"email": "bad"}).wantStatus(t, http.StatusBadRequest)

	email := uniq("upgraded") + "@example.test"
	up := env.do(t, http.MethodPost, "/v1/me/upgrade", guest.access(), map[string]string{
		"email": email, "password": testPassword, "given_name": "Grace", "family_name": "Guest",
	}).wantStatus(t, http.StatusOK).obj(t)
	if up["is_guest"] != false || up["email"] != email || up["given_name"] != "Grace" {
		t.Fatalf("upgrade response = %v", up)
	}

	// The anon credential is revoked: its session ends and the device token no
	// longer signs in.
	env.do(t, http.MethodGet, "/v1/probe", guest.access(), nil).wantError(t, http.StatusUnauthorized, "users.session_revoked", "")
	env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": deviceID, "device_token": guest.DeviceToken}).
		wantStatus(t, http.StatusUnauthorized)
	// The new password signs in as the same principal.
	tk := env.loginRaw(t, email, testPassword, nil).wantStatus(t, http.StatusOK).tok(t)
	if status, p := env.probe(t, "/v1/probe", tk.access()); status != http.StatusOK || p.PrincipalID != env.principalID(t, email) {
		t.Fatalf("upgraded login = %d %+v", status, p)
	}
	// A registered user cannot upgrade.
	env.do(t, http.MethodPost, "/v1/me/upgrade", tk.access(), map[string]string{
		"email": uniq("again") + "@example.test", "password": testPassword, "given_name": "A", "family_name": "B",
	}).wantError(t, http.StatusConflict, "conflict", "users.not_guest")
}

func TestRegister_BYODisabledByPlatformSettingIsRefused(t *testing.T) {
	var prev bool
	if err := env.pool.QueryRow(context.Background(), `SELECT allow_byo_identity FROM platform_settings`).Scan(&prev); err != nil {
		t.Fatal(err)
	}
	env.exec(t, "disable byo", `UPDATE platform_settings SET allow_byo_identity = false`)
	defer env.exec(t, "restore byo", `UPDATE platform_settings SET allow_byo_identity = $1`, prev)

	env.do(t, http.MethodPost, "/v1/auth/register", "", map[string]string{
		"email": uniq("blocked") + "@example.test", "password": testPassword, "given_name": "B", "family_name": "L",
	}).wantError(t, http.StatusForbidden, "users.byo_identity_disabled", "")
}

func TestRegister_DuplicateEmailAndWeakPassword(t *testing.T) {
	a := env.register(t)
	env.do(t, http.MethodPost, "/v1/auth/register", "", map[string]string{
		"email": a.Email, "password": testPassword, "given_name": "D", "family_name": "U",
	}).wantStatus(t, http.StatusConflict)
	env.do(t, http.MethodPost, "/v1/auth/register", "", map[string]string{
		"email": uniq("weak") + "@example.test", "password": "short", "given_name": "W", "family_name": "K",
	}).wantStatus(t, http.StatusBadRequest)
}
