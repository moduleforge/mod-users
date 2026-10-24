//go:build integration

package authz_test

// assurance_liveness_integration_test.go proves mod_users.session_acr (single
// factor, two distinct factor classes, trust_acr handling) and the liveness
// functions session_liveness and credential_liveness (every status, and the
// impersonator principal).

import (
	"context"
	"testing"
)

func sessionACR(t *testing.T, sessionID int64) *int {
	t.Helper()
	var acr *int
	if err := integPool.QueryRow(context.Background(), `SELECT mod_users.session_acr($1)`, sessionID).Scan(&acr); err != nil {
		t.Fatalf("session_acr: %v", err)
	}
	return acr
}

func wantACR(t *testing.T, got *int, want int, what string) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s: session_acr %v, want %d", what, got, want)
	}
}

func TestInteg_SessionACR(t *testing.T) {
	t.Run("no factor is NULL", func(t *testing.T) {
		u, _ := seedUserEntity(t)
		s, _ := fxSession(t, integPool, u, sessionOpts{})
		if got := sessionACR(t, s); got != nil {
			t.Errorf("session without factors: session_acr %d, want NULL", *got)
		}
	})

	t.Run("single factor", func(t *testing.T) {
		u, _ := seedUserEntity(t)
		s := sessionWith(t, u, 1, fxPassword(t, integPool, u, nil))
		wantACR(t, sessionACR(t, s), 1, "password only")
	})

	t.Run("two distinct factor classes raise to 2", func(t *testing.T) {
		u, _ := seedUserEntity(t)
		pw := fxPassword(t, integPool, u, nil)
		ssh, _ := fxSSHKey(t, integPool, u, "SHA256:"+fxTag("fp"))
		s := sessionWith(t, u, 1, pw, ssh)
		wantACR(t, sessionACR(t, s), 2, "knowledge + possession")
	})

	t.Run("two factors of one class stay at 1", func(t *testing.T) {
		u, _ := seedUserEntity(t)
		a, _ := fxSSHKey(t, integPool, u, "SHA256:"+fxTag("fp"))
		b, _ := fxSSHKey(t, integPool, u, "SHA256:"+fxTag("fp"))
		s := sessionWith(t, u, 1, a, b)
		wantACR(t, sessionACR(t, s), 1, "possession + possession")
	})

	t.Run("a revoked factor does not count", func(t *testing.T) {
		u, _ := seedUserEntity(t)
		pw := fxPassword(t, integPool, u, nil)
		ssh, _ := fxSSHKey(t, integPool, u, "SHA256:"+fxTag("fp"))
		s := sessionWith(t, u, 1, pw, ssh)
		fxRevoke(t, integPool, ssh)
		wantACR(t, sessionACR(t, s), 1, "one of two factors revoked")
	})

	t.Run("trust_acr", func(t *testing.T) {
		trusted, untrusted := fxTag("idp-trusted"), fxTag("idp-untrusted")
		fxOIDCProvider(t, integPool, trusted, true)
		fxOIDCProvider(t, integPool, untrusted, false)
		acr := func(n int) *int { return &n }

		u, _ := seedUserEntity(t)
		c := fxOIDC(t, integPool, u, "https://idp.example", trusted, fxTag("sub"))
		s, _ := fxSession(t, integPool, u, sessionOpts{})
		fxFactor(t, integPool, s, c, acr(2))
		wantACR(t, sessionACR(t, s), 2, "trusted provider asserting 2")

		u2, _ := seedUserEntity(t)
		c2 := fxOIDC(t, integPool, u2, "https://idp.example", untrusted, fxTag("sub"))
		s2, _ := fxSession(t, integPool, u2, sessionOpts{})
		fxFactor(t, integPool, s2, c2, acr(3))
		wantACR(t, sessionACR(t, s2), 1, "untrusted provider asserting 3 is ignored")

		u3, _ := seedUserEntity(t)
		c3 := fxOIDC(t, integPool, u3, "https://idp.example", trusted, fxTag("sub"))
		s3, _ := fxSession(t, integPool, u3, sessionOpts{})
		fxFactor(t, integPool, s3, c3, acr(3))
		wantACR(t, sessionACR(t, s3), 3, "trusted provider asserting 3")
	})
}

type livenessRow struct {
	SessionID, PrincipalID, ProfileID, ImpersonatorPrincipalID *int64
	Status                                                     string
}

func entityUUID(t *testing.T, id int64) string {
	t.Helper()
	var u string
	if err := integPool.QueryRow(context.Background(), `SELECT uuid::text FROM entities WHERE id = $1`, id).Scan(&u); err != nil {
		t.Fatalf("entity uuid %d: %v", id, err)
	}
	return u
}

func sessionLive(t *testing.T, sessionUUID string, principal, profile int64) livenessRow {
	t.Helper()
	var r livenessRow
	if err := integPool.QueryRow(context.Background(),
		`SELECT session_id, principal_id, profile_id, impersonator_principal_id, status FROM mod_users.session_liveness($1::uuid, $2::uuid, $3::uuid)`,
		sessionUUID, entityUUID(t, principal), entityUUID(t, profile)).
		Scan(&r.SessionID, &r.PrincipalID, &r.ProfileID, &r.ImpersonatorPrincipalID, &r.Status); err != nil {
		t.Fatalf("session_liveness: %v", err)
	}
	return r
}

func wantStatus(t *testing.T, got, want, what string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: status %q, want %q", what, got, want)
	}
}

func TestInteg_SessionLiveness(t *testing.T) {
	u, personal := seedUserEntity(t)
	pw := fxPassword(t, integPool, u, nil)

	live, liveUUID := fxSession(t, integPool, u, sessionOpts{})
	fxFactor(t, integPool, live, pw, nil)
	wantStatus(t, sessionLive(t, liveUUID, u, personal).Status, "ok", "live session")

	t.Run("session_revoked", func(t *testing.T) {
		id, uuid := fxSession(t, integPool, u, sessionOpts{})
		fxExec(t, integPool, "revoke session", `UPDATE mod_users.sessions SET revoked_at = now(), revoke_reason = 'logout' WHERE id = $1`, id)
		wantStatus(t, sessionLive(t, uuid, u, personal).Status, "session_revoked", "revoked session")
		wantStatus(t, sessionLive(t, "00000000-0000-0000-0000-000000000000", u, personal).Status, "session_revoked", "unknown session")
	})

	t.Run("session_expired", func(t *testing.T) {
		_, uuid := fxSession(t, integPool, u, sessionOpts{ExpiresIn: "-1 minute"})
		wantStatus(t, sessionLive(t, uuid, u, personal).Status, "session_expired", "expired session")
	})

	t.Run("principal_mismatch", func(t *testing.T) {
		other, otherProfile := seedUserEntity(t)
		wantStatus(t, sessionLive(t, liveUUID, other, otherProfile).Status, "principal_mismatch", "token sub is another principal")
	})

	t.Run("lock reason is passed through", func(t *testing.T) {
		v, vProfile := seedUserEntity(t)
		id, uuid := fxSession(t, integPool, v, sessionOpts{})
		fxFactor(t, integPool, id, fxPassword(t, integPool, v, nil), nil)
		fxExec(t, integPool, "suspend profile", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, vProfile)
		wantStatus(t, sessionLive(t, uuid, v, vProfile).Status, "inactive", "suspended profile")
		wantStatus(t, sessionLive(t, liveUUID, u, vProfile).Status, "not_held", "profile held by another principal")
	})

	t.Run("impersonation", func(t *testing.T) {
		admin, _ := seedUserEntity(t)
		adminSession, _ := fxSession(t, integPool, admin, sessionOpts{})
		fxFactor(t, integPool, adminSession, fxPassword(t, integPool, admin, nil), nil)
		target, targetProfile := seedUserEntity(t)
		imp, impUUID := fxSession(t, integPool, target, sessionOpts{ParentID: &adminSession, ImpersonateProfile: &targetProfile})
		_ = imp

		r := sessionLive(t, impUUID, target, targetProfile)
		wantStatus(t, r.Status, "ok", "impersonation with a live parent")
		if r.ImpersonatorPrincipalID == nil || *r.ImpersonatorPrincipalID != admin {
			t.Errorf("impersonator_principal_id = %v, want the parent session's principal %d", r.ImpersonatorPrincipalID, admin)
		}
		if plain := sessionLive(t, liveUUID, u, personal); plain.ImpersonatorPrincipalID != nil {
			t.Errorf("non-impersonation session: impersonator_principal_id = %d, want NULL", *plain.ImpersonatorPrincipalID)
		}

		fxExec(t, integPool, "revoke parent session",
			`UPDATE mod_users.sessions SET revoked_at = now(), revoke_reason = 'logout' WHERE id = $1`, adminSession)
		wantStatus(t, sessionLive(t, impUUID, target, targetProfile).Status, "parent_session_ended", "parent revoked")
	})
}

func TestInteg_CredentialLiveness(t *testing.T) {
	u, personal := seedUserEntity(t)
	cred := func(c string, principal, profile int64) (string, *int, *int64) {
		var status string
		var acr *int
		var cid *int64
		if err := integPool.QueryRow(context.Background(),
			`SELECT status, acr, credential_id FROM mod_users.credential_liveness($1::uuid, $2::uuid, $3::uuid)`,
			c, entityUUID(t, principal), entityUUID(t, profile)).Scan(&status, &acr, &cid); err != nil {
			t.Fatalf("credential_liveness: %v", err)
		}
		return status, acr, cid
	}

	id, uuid := fxAPIKey(t, integPool, u)
	status, acr, cid := cred(uuid, u, personal)
	wantStatus(t, status, "ok", "live credential")
	wantACR(t, acr, 1, "credential acr is the kind base_acr")
	if cid == nil || *cid != id {
		t.Errorf("credential_id = %v, want %d", cid, id)
	}

	other, otherProfile := seedUserEntity(t)
	status, _, _ = cred(uuid, other, otherProfile)
	wantStatus(t, status, "principal_mismatch", "credential of another principal")

	status, _, _ = cred(uuid, u, otherProfile)
	wantStatus(t, status, "not_held", "profile held by another principal")

	status, _, _ = cred("00000000-0000-0000-0000-000000000000", u, personal)
	wantStatus(t, status, "credential_revoked", "unknown credential")

	expID, expUUID := fxAPIKey(t, integPool, u)
	fxExec(t, integPool, "expire credential", `UPDATE mod_users.credentials SET expires_at = now() - interval '1 minute' WHERE id = $1`, expID)
	status, _, _ = cred(expUUID, u, personal)
	wantStatus(t, status, "credential_expired", "expired credential")

	revID, revUUID := fxAPIKey(t, integPool, u)
	fxRevoke(t, integPool, revID)
	status, _, _ = cred(revUUID, u, personal)
	wantStatus(t, status, "credential_revoked", "revoked credential")

	fxExec(t, integPool, "suspend profile", `UPDATE profiles SET status = 'suspended' WHERE entity_id = $1`, otherProfile)
	_, otherUUID := fxAPIKey(t, integPool, other)
	status, _, _ = cred(otherUUID, other, otherProfile)
	wantStatus(t, status, "inactive", "lock reason passed through")
}
