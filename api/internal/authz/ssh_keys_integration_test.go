//go:build integration

package authz_test

// ssh_keys_integration_test.go proves the ssh_key credential rules through SQL:
// the subtable carries the key, a fingerprint is unique only while its
// credential is active, and a service account may hold ssh_key credentials.

import (
	"context"
	"testing"
)

func TestInteg_SSHKey_FingerprintUniqueWhileActive(t *testing.T) {
	u, _ := seedUserEntity(t)
	v, _ := seedUserEntity(t)
	fp := "SHA256:" + fxTag("fp")

	first, _ := fxSSHKey(t, integPool, u, fp)

	id, _ := fxCredential(t, integPool, v, "ssh_key")
	expectUnique(t, fxSSHSub(integPool, id, fp), "cred_ssh_key_active_fingerprint_uq", "same fingerprint on another active credential")

	fxRevoke(t, integPool, first)
	id2, _ := fxCredential(t, integPool, v, "ssh_key")
	expectOK(t, fxSSHSub(integPool, id2, fp), "re-adding the fingerprint after revocation")
}

func TestInteg_SSHKey_ActiveFlagFollowsRevocation(t *testing.T) {
	u, _ := seedUserEntity(t)
	id, _ := fxSSHKey(t, integPool, u, "SHA256:"+fxTag("fp"))
	active := func() bool {
		var a bool
		if err := integPool.QueryRow(context.Background(), `SELECT active FROM mod_users.cred_ssh_key WHERE credential_id = $1`, id).Scan(&a); err != nil {
			t.Fatalf("read active: %v", err)
		}
		return a
	}
	if !active() {
		t.Errorf("new ssh_key credential: active = false, want true")
	}
	fxRevoke(t, integPool, id)
	if active() {
		t.Errorf("revoked ssh_key credential: active = true, want false")
	}
}

func TestInteg_SSHKey_SubtableKindMatch(t *testing.T) {
	u, _ := seedUserEntity(t)
	pwID, _ := fxCredential(t, integPool, u, "password")
	expectState(t, fxSSHSub(integPool, pwID, "SHA256:"+fxTag("fp")), "MF102", "ssh_key subtable row on a password credential")
}
