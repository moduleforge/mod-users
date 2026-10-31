//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"

	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// activePasswordID returns the id of the principal's active password credential.
func (s *stack) activePasswordID(t *testing.T, principalID int64) int64 {
	t.Helper()
	return s.scanInt64(t, "active password",
		`SELECT id FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'password' AND revoked_at IS NULL`,
		principalID)
}

// authorizedParties counts the party authorizations of a credential.
func (s *stack) authorizedParties(t *testing.T, credentialID int64) int64 {
	t.Helper()
	return s.scanInt64(t, "authorizations",
		`SELECT count(*) FROM mod_users.credential_party_authorizations WHERE credential_id = $1`, credentialID)
}

// seedAuthorizedPasswordParty gives the account a party profile in a party
// whose credential rule is 'authorized' and authorizes the account's password
// for it. It returns the party profile uuid and the password credential id.
func (s *stack) seedAuthorizedPasswordParty(t *testing.T, a account) (partyProfileUUID string, partyID, credID int64) {
	t.Helper()
	uid := s.principalID(t, a.Email)
	partyID = s.seedParty(t, "Supersede Party "+uniq("p"))
	profileID, profileUUID := s.seedPartyProfile(t, uid, partyID, "active")
	s.exec(t, "policy", `INSERT INTO mod_users.party_authn_policies (party_id, credential_rule) VALUES ($1, 'authorized')`, partyID)
	credID = s.activePasswordID(t, uid)
	s.exec(t, "authorize", `INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		credID, partyID, profileID)
	return profileUUID, partyID, credID
}

func partyProfileAvailable(t *testing.T, tk tokens, profileUUID string) bool {
	t.Helper()
	for _, p := range tk.Profiles {
		if p.UUID == profileUUID {
			return p.Available
		}
	}
	t.Fatalf("profile %s not listed", profileUUID)
	return false
}

func TestPassword_ResetKeepsPartyAuthorizedProfileAvailable(t *testing.T) {
	a := env.register(t)
	partyProfile, partyID, oldCred := env.seedAuthorizedPasswordParty(t, a)
	if !partyProfileAvailable(t, env.login(t, a), partyProfile) {
		t.Fatal("party profile unavailable before reset")
	}

	seen := env.mail.count(a.Email)
	env.do(t, http.MethodPost, "/v1/auth/password-reset/request", "", map[string]string{"email": a.Email}).wantStatus(t, http.StatusNoContent)
	token := env.resetTokenAfter(t, a.Email, seen)
	const newPassword = "a-brand-new-password"
	env.do(t, http.MethodPost, "/v1/auth/password-reset/confirm", "", map[string]string{"token": token, "password": newPassword}).
		wantStatus(t, http.StatusNoContent)

	newCred := env.activePasswordID(t, env.principalID(t, a.Email))
	if newCred == oldCred {
		t.Fatal("password was not superseded")
	}
	if got := env.scanInt64(t, "copied", `SELECT count(*) FROM mod_users.credential_party_authorizations WHERE credential_id = $1 AND party_id = $2`, newCred, partyID); got != 1 {
		t.Fatalf("new credential authorizations for party = %d, want 1", got)
	}
	tk := env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]string{"email": a.Email, "password": newPassword}).
		wantStatus(t, http.StatusOK).tok(t)
	if !partyProfileAvailable(t, tk, partyProfile) {
		t.Fatal("party profile with credential rule authorized lost availability after reset")
	}
}

func TestSupersedePassword_CopyAndSupersedeAreAtomic(t *testing.T) {
	a := env.register(t)
	_, partyID, oldCred := env.seedAuthorizedPasswordParty(t, a)
	uid := env.principalID(t, a.Email)
	ctx := context.Background()
	errRollback := errors.New("force rollback")

	// Success inside a transaction that then fails: nothing persists.
	err := pgx.BeginFunc(ctx, env.pool, func(tx pgx.Tx) error {
		actCtx, ok, err := svc.UnauthenticatedActingContext(ctx, tx, uid)
		if err != nil || !ok {
			t.Fatalf("acting context: ok=%v err=%v", ok, err)
		}
		created, err := env.creds.SupersedePasswordInTx(actCtx, tx, oldCred, svc.CreatePasswordInput{
			PrincipalID: uid, Password: "another-long-password",
		})
		if err != nil {
			return err
		}
		var n int64
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM mod_users.credential_party_authorizations WHERE credential_id = $1 AND party_id = $2`,
			created.ID, partyID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("copied authorizations inside the transaction = %d, want 1", n)
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("err = %v, want rollback sentinel", err)
	}
	if got := env.activePasswordID(t, uid); got != oldCred {
		t.Fatalf("active password = %d after rollback, want the original %d", got, oldCred)
	}
	if got := env.scanInt64(t, "credential count", `SELECT count(*) FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'password'`, uid); got != 1 {
		t.Fatalf("password credentials = %d after rollback, want 1", got)
	}
	if got := env.scanInt64(t, "authorization count", `SELECT count(*) FROM mod_users.credential_party_authorizations a JOIN mod_users.credentials c ON c.id = a.credential_id WHERE c.principal_id = $1`, uid); got != 1 {
		t.Fatalf("authorizations = %d after rollback, want only the original", got)
	}

	// A failing replacement (weak password) rolls back the revoke too.
	err = pgx.BeginFunc(ctx, env.pool, func(tx pgx.Tx) error {
		actCtx, ok, err := svc.UnauthenticatedActingContext(ctx, tx, uid)
		if err != nil || !ok {
			t.Fatalf("acting context: ok=%v err=%v", ok, err)
		}
		_, err = env.creds.SupersedePasswordInTx(actCtx, tx, oldCred, svc.CreatePasswordInput{PrincipalID: uid, Password: "short"})
		return err
	})
	if err == nil {
		t.Fatal("weak replacement password accepted")
	}
	if got := env.activePasswordID(t, uid); got != oldCred {
		t.Fatalf("active password = %d after failed supersede, want the original %d", got, oldCred)
	}
}
