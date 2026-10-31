//go:build integration

package authz_test

// credentials_invariants_integration_test.go proves the MF102 credential
// invariants and the active-uniqueness indexes of the baseline migration,
// writing directly through SQL.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// seedServiceAccount inserts a service account owned by ownerID with its one
// personal profile, and returns the account id and profile id.
func seedServiceAccount(t *testing.T, ownerID int64) (id, profileID int64) {
	t.Helper()
	fxMustTx(t, func(tx pgx.Tx) {
		id = fxEntity(t, tx, "service_account", &ownerID)
		fxExec(t, tx, "insert principals", `INSERT INTO principals (entity_id) VALUES ($1)`, id)
		fxExec(t, tx, "insert service_accounts", `INSERT INTO service_accounts (entity_id, label) VALUES ($1, $2)`, id, fxTag("svc"))
		profileID = fxEntity(t, tx, "profile", &ownerID)
		fxExec(t, tx, "insert personal profile",
			`INSERT INTO profiles (entity_id, holder_id, party_id, status, started_at) VALUES ($1, $2, NULL, 'active', now())`, profileID, id)
	})
	return id, profileID
}

// MF102: credential kinds per principal type.
func TestInteg_MF102_KindsPerPrincipalType(t *testing.T) {
	owner := seedOrg(t, "MF102 kinds org")
	svc, _ := seedServiceAccount(t, owner)
	var systemActor int64
	if err := integPool.QueryRow(context.Background(), `SELECT core_ensure_system_actor('mf102_actor', 'MF102 actor')`).Scan(&systemActor); err != nil {
		t.Fatalf("ensure system actor: %v", err)
	}
	user, _ := seedUserEntity(t)

	t.Run("service account holds only service_allowed kinds", func(t *testing.T) {
		for _, kind := range []string{"ssh_key", "api_key"} {
			_, err := credInsert(integPool, svc, kind)
			expectOK(t, err, "service account "+kind)
		}
		for _, kind := range []string{"password", "email_otp", "oidc", "anon"} {
			_, err := credInsert(integPool, svc, kind)
			expectState(t, err, "MF102", "service account "+kind)
		}
	})
	t.Run("system actor holds none", func(t *testing.T) {
		for _, kind := range []string{"password", "ssh_key", "api_key", "anon"} {
			_, err := credInsert(integPool, systemActor, kind)
			expectState(t, err, "MF102", "system actor "+kind)
		}
	})
	t.Run("a user holds interactive and non-interactive kinds", func(t *testing.T) {
		for _, kind := range []string{"password", "email_otp", "oidc", "ssh_key", "api_key"} {
			_, err := credInsert(integPool, user, kind)
			expectOK(t, err, "user "+kind)
		}
	})
	t.Run("a user principal needs a users row", func(t *testing.T) {
		inRolledBackTx(t, func(tx pgx.Tx) {
			u := fxEntity(t, tx, "user", nil)
			fxExec(t, tx, "insert principals", `INSERT INTO principals (entity_id) VALUES ($1)`, u)
			_, err := credInsert(tx, u, "password")
			expectState(t, err, "MF102", "credential for a user without a users row")
		})
	})
}

// MF102: a guest holds only anon; a non-guest gains no new anon.
func TestInteg_MF102_GuestOnlyAnon(t *testing.T) {
	var guest int64
	fxMustTx(t, func(tx pgx.Tx) { guest, _ = fxUser(t, tx, userOpts{Guest: true}) })
	byo, _ := seedUserEntity(t)

	_, err := credInsert(integPool, guest, "anon")
	expectOK(t, err, "guest anon")
	for _, kind := range []string{"password", "email_otp", "oidc", "ssh_key", "api_key"} {
		_, err := credInsert(integPool, guest, kind)
		expectState(t, err, "MF102", "guest "+kind)
	}
	_, err = credInsert(integPool, byo, "anon")
	expectState(t, err, "MF102", "non-guest gains anon")

	// A guest upgraded to a full user keeps its anon credential but gains no
	// new one.
	expectOK(t, rawExec(integPool, `UPDATE mod_users.users SET is_guest = false WHERE entity_id = $1`, guest), "upgrade guest")
	_, err = credInsert(integPool, guest, "anon")
	expectState(t, err, "MF102", "upgraded guest gains a new anon credential")
	_, err = credInsert(integPool, guest, "password")
	expectOK(t, err, "upgraded guest gains a password")
}

// MF102: principal_id and kind are immutable.
func TestInteg_MF102_PrincipalAndKindImmutable(t *testing.T) {
	user, _ := seedUserEntity(t)
	other, _ := seedUserEntity(t)
	id, _ := fxCredential(t, integPool, user, "api_key")

	expectState(t, rawExec(integPool, `UPDATE mod_users.credentials SET principal_id = $2 WHERE id = $1`, id, other),
		"MF102", "change principal_id")
	expectState(t, rawExec(integPool, `UPDATE mod_users.credentials SET kind = 'ssh_key' WHERE id = $1`, id),
		"MF102", "change kind")
	expectOK(t, rawExec(integPool, `UPDATE mod_users.credentials SET label = 'renamed' WHERE id = $1`, id), "change label")
}

// MF102: default_profile_id names a profile held by the credential's principal.
func TestInteg_MF102_DefaultProfileHeld(t *testing.T) {
	user, profile := seedUserEntity(t)
	_, otherProfile := seedUserEntity(t)

	_, err := integPool.Exec(context.Background(),
		`INSERT INTO mod_users.credentials (principal_id, kind, default_profile_id) VALUES ($1, 'api_key', $2)`, user, otherProfile)
	expectState(t, err, "MF102", "insert with a default profile held by another principal")
	_, err = integPool.Exec(context.Background(),
		`INSERT INTO mod_users.credentials (principal_id, kind, default_profile_id) VALUES ($1, 'api_key', $2)`, user, profile)
	expectOK(t, err, "insert with its own default profile")

	id, _ := fxCredential(t, integPool, user, "api_key")
	expectState(t, rawExec(integPool, `UPDATE mod_users.credentials SET default_profile_id = $2 WHERE id = $1`, id, otherProfile),
		"MF102", "update to a default profile held by another principal")
	expectOK(t, rawExec(integPool, `UPDATE mod_users.credentials SET default_profile_id = $2 WHERE id = $1`, id, profile),
		"update to its own default profile")
}

// MF102: the cred_<kind> row's kind matches the credential, and a bound
// identifier belongs to the principal.
func TestInteg_MF102_SubtableKindAndIdentifier(t *testing.T) {
	user, _ := seedUserEntity(t)
	other, _ := seedUserEntity(t)
	ident := fxIdentifier(t, integPool, user, "email", fxTag("sub")+"@example.com")
	foreign := fxIdentifier(t, integPool, other, "email", fxTag("sub")+"@example.com")

	sshCred, _ := fxCredential(t, integPool, user, "ssh_key")
	expectState(t, fxPasswordSub(integPool, sshCred, nil), "MF102", "cred_password row on an ssh_key credential")
	expectState(t, fxAPIKeySub(integPool, sshCred), "MF102", "cred_api_key row on an ssh_key credential")
	expectState(t, fxOIDCSub(integPool, sshCred, "https://i", "p", "s"), "MF102", "cred_oidc row on an ssh_key credential")
	expectOK(t, fxSSHSub(integPool, sshCred, "SHA256:"+fxTag("kind")), "cred_ssh_key row on an ssh_key credential")

	pw, _ := fxCredential(t, integPool, user, "password")
	expectState(t, fxPasswordSub(integPool, pw, &foreign), "MF102", "password bound to another user's identifier")
	expectOK(t, fxPasswordSub(integPool, pw, &ident), "password bound to its own identifier")

	otp, _ := fxCredential(t, integPool, user, "email_otp")
	expectState(t, fxEmailOTPSub(integPool, otp, foreign), "MF102", "email_otp bound to another user's identifier")
	expectOK(t, fxEmailOTPSub(integPool, otp, ident), "email_otp bound to its own identifier")
}

// Active uniqueness: OIDC issuer/subject and SSH fingerprint are unique only
// while active.
func TestInteg_MF102_ActiveUniqueness_OIDCAndSSH(t *testing.T) {
	a, _ := seedUserEntity(t)
	b, _ := seedUserEntity(t)

	t.Run("oidc issuer and subject", func(t *testing.T) {
		issuer, subject := "https://"+fxTag("iss"), fxTag("sub")
		first := fxOIDC(t, integPool, a, issuer, "prov", subject)

		dup, _ := fxCredential(t, integPool, b, "oidc")
		expectUnique(t, fxOIDCSub(integPool, dup, issuer, "prov", subject), "cred_oidc_active_issuer_subject_uq", "active duplicate")

		fxRevoke(t, integPool, first)
		again, _ := fxCredential(t, integPool, b, "oidc")
		expectOK(t, fxOIDCSub(integPool, again, issuer, "prov", subject), "re-adding after revocation")

		// Re-activating the revoked credential would duplicate the active one.
		expectUnique(t, rawExec(integPool, `UPDATE mod_users.credentials SET revoked_at = NULL, revoke_reason = NULL WHERE id = $1`, first),
			"cred_oidc_active_issuer_subject_uq", "re-activation of a revoked credential")
	})
	t.Run("ssh fingerprint", func(t *testing.T) {
		fp := "SHA256:" + fxTag("fp")
		first, _ := fxSSHKey(t, integPool, a, fp)

		dup, _ := fxCredential(t, integPool, b, "ssh_key")
		expectUnique(t, fxSSHSub(integPool, dup, fp), "cred_ssh_key_active_fingerprint_uq", "active duplicate")

		fxRevoke(t, integPool, first)
		again, _ := fxCredential(t, integPool, b, "ssh_key")
		expectOK(t, fxSSHSub(integPool, again, fp), "re-adding after revocation")
	})
}

// Active uniqueness: passwords per identifier, unbound passwords per user,
// email OTP per identifier.
func TestInteg_MF102_ActiveUniqueness_PasswordAndOTP(t *testing.T) {
	user, _ := seedUserEntity(t)
	ident := fxIdentifier(t, integPool, user, "email", fxTag("uq")+"@example.com")

	t.Run("one active password per identifier", func(t *testing.T) {
		first := fxPassword(t, integPool, user, &ident)
		dup, _ := fxCredential(t, integPool, user, "password")
		expectUnique(t, fxPasswordSub(integPool, dup, &ident), "cred_password_active_identifier_uq", "second active password")
		fxRevoke(t, integPool, first)
		again, _ := fxCredential(t, integPool, user, "password")
		expectOK(t, fxPasswordSub(integPool, again, &ident), "password after revoking the first")
	})
	t.Run("one unbound password per user", func(t *testing.T) {
		u, _ := seedUserEntity(t)
		first := fxPassword(t, integPool, u, nil)
		dup, _ := fxCredential(t, integPool, u, "password")
		expectUnique(t, fxPasswordSub(integPool, dup, nil), "cred_password_active_unbound_uq", "second unbound password")
		fxRevoke(t, integPool, first)
		again, _ := fxCredential(t, integPool, u, "password")
		expectOK(t, fxPasswordSub(integPool, again, nil), "unbound password after revoking the first")
		// Another user is unaffected.
		v, _ := seedUserEntity(t)
		fxPassword(t, integPool, v, nil)
	})
	t.Run("one active email OTP per identifier", func(t *testing.T) {
		u, _ := seedUserEntity(t)
		id := fxIdentifier(t, integPool, u, "email", fxTag("otp")+"@example.com")
		first, _ := fxCredential(t, integPool, u, "email_otp")
		expectOK(t, fxEmailOTPSub(integPool, first, id), "first email_otp")
		dup, _ := fxCredential(t, integPool, u, "email_otp")
		expectUnique(t, fxEmailOTPSub(integPool, dup, id), "cred_email_otp_active_identifier_uq", "second active email_otp")
		fxRevoke(t, integPool, first)
		again, _ := fxCredential(t, integPool, u, "email_otp")
		expectOK(t, fxEmailOTPSub(integPool, again, id), "email_otp after revoking the first")
	})
}
