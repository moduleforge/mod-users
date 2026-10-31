//go:build integration

package integration

// Managed-user provisioning serializes with PutAuthnPolicy: the provisioning
// transaction reads the party's policy under the shared per-party policy lock,
// so a concurrent switch to `authorized` either waits for the provisioning to
// commit or is seen by it.

import (
	"context"
	"net/http"
	"testing"
	"time"

	svc "github.com/moduleforge/mod-users/api/internal/service"
)

const lockWait = 500 * time.Millisecond

func (s *stack) managedPasswordAuthorizations(t *testing.T, email string, partyID int64) int64 {
	t.Helper()
	uid := s.principalID(t, email)
	return s.count(t, "authorizations", `
SELECT count(*) FROM mod_users.credential_party_authorizations a JOIN mod_users.credentials c ON c.id = a.credential_id
 WHERE c.principal_id = $1 AND a.party_id = $2`, uid, partyID)
}

// A PutAuthnPolicy switching a party with no policy row to `authorized` waits
// for an in-flight provisioning, which read the policy as `any`: the user is
// provisioned entirely before the switch, never half under each rule.
func TestManagedUserProvisioning_SerializesWithPolicySwitch_NoRow(t *testing.T) {
	g := env.newGateParty(t)
	ctx := env.authCtx(t, g.adminTok.access())
	email := uniq("managed") + "@example.test"

	provisioned, release := make(chan struct{}), make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- env.inTx(ctx, func(tx pgxTx) error {
			if _, err := env.profiles.ProvisionManagedUserInTx(ctx, tx, svc.ManagedUserInput{PartyID: g.id, Email: email, Password: testPassword}); err != nil {
				close(provisioned)
				return err
			}
			close(provisioned)
			<-release
			return nil
		})
	}()
	<-provisioned

	putDone := make(chan *resp, 1)
	go func() {
		putDone <- env.do(t, http.MethodPut, g.path("/authn-policy"), g.adminTok.access(), map[string]any{"credential_rule": "authorized"})
	}()
	select {
	case <-putDone:
		close(release)
		t.Fatal("PutAuthnPolicy completed while the provisioning transaction was still open")
	case <-time.After(lockWait):
	}
	close(release)
	if err := <-txDone; err != nil {
		t.Fatalf("provisioning: %v", err)
	}
	(<-putDone).wantStatus(t, http.StatusOK)

	// The user was provisioned under the rule in force at the locked read
	// (`any`); the switch committed after it.
	if n := env.managedPasswordAuthorizations(t, email, g.id); n != 0 {
		t.Errorf("authorizations = %d, want 0 (provisioned before the switch)", n)
	}
}

// A provisioning that starts while a switch to `authorized` is uncommitted on
// an existing policy row waits for it and then authorizes the new password.
func TestManagedUserProvisioning_SerializesWithPolicySwitch_ExistingRow(t *testing.T) {
	g := env.newGateParty(t)
	env.setPolicy(t, g, map[string]any{"credential_rule": "any"}).wantStatus(t, http.StatusOK)
	ctx := env.authCtx(t, g.adminTok.access())
	email := uniq("managed") + "@example.test"

	bg := context.Background()
	tx, err := env.pool.Begin(bg)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(bg) //nolint:errcheck
	if _, err := tx.Exec(bg, `UPDATE mod_users.party_authn_policies SET credential_rule = 'authorized' WHERE party_id = $1`, g.id); err != nil {
		t.Fatal(err)
	}

	provDone := make(chan error, 1)
	go func() {
		provDone <- env.inTx(ctx, func(tx pgxTx) error {
			_, err := env.profiles.ProvisionManagedUserInTx(ctx, tx, svc.ManagedUserInput{PartyID: g.id, Email: email, Password: testPassword})
			return err
		})
	}()
	select {
	case err := <-provDone:
		t.Fatalf("provisioning completed while the policy switch was uncommitted: %v", err)
	case <-time.After(lockWait):
	}
	if err := tx.Commit(bg); err != nil {
		t.Fatal(err)
	}
	if err := <-provDone; err != nil {
		t.Fatalf("provisioning: %v", err)
	}
	if n := env.managedPasswordAuthorizations(t, email, g.id); n != 1 {
		t.Errorf("authorizations = %d, want 1 (the committed `authorized` rule was read)", n)
	}
}
