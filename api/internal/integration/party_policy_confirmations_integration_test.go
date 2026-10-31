//go:build integration

package integration

// Confirmation purging on a policy change: a change of the required OIDC
// provider voids a party's profile_policy_confirmations like a rule change
// does, and PutAuthnPolicy serializes with the confirmation writers, which hold
// the party's policy row FOR SHARE (or the per-party advisory lock in shared
// mode when the party has no row).

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestPolicyConfirmations_ProviderChangePurges(t *testing.T) {
	g := env.newGateParty(t)
	provA := env.addProviderRow(t, "pc-a-"+uniq("idp"))
	provB := env.addProviderRow(t, "pc-b-"+uniq("idp"))
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provA}).wantStatus(t, http.StatusOK)
	m := env.newGateMember(t, g)
	env.exec(t, "confirm", `INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1)`, m.profileID, g.id)
	rows := func() int64 {
		return env.scanInt64(t, "confirmations", `SELECT count(*) FROM profile_policy_confirmations WHERE party_id = $1`, g.id)
	}

	// Neither the rule nor the provider changes: the confirmation stays.
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provA, "min_acr": 2}).wantStatus(t, http.StatusOK)
	if rows() != 1 {
		t.Fatalf("confirmations after a min_acr-only write = %d, want 1", rows())
	}

	// The provider changes A to B under the same rule: the confirmation goes.
	env.setPolicy(t, g, map[string]any{"credential_rule": "provider", "required_oidc_provider_id": provB, "min_acr": 2}).wantStatus(t, http.StatusOK)
	if rows() != 0 {
		t.Fatalf("confirmations after a provider change = %d, want 0", rows())
	}
}

// A confirmation writer holding the policy row FOR SHARE (having read the old
// rule) holds off a concurrent rule change until it commits, so its
// confirmation is purged rather than surviving the change.
func TestPolicyConfirmations_WriterSerializedWithRuleChange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		withRow   bool
		lockQuery string
	}{
		{"row", true, `SELECT 1 FROM mod_users.party_authn_policies WHERE party_id = $1 FOR SHARE`},
		{"no row", false, `SELECT pg_advisory_xact_lock_shared(hashtextextended('mod_users.party_authn_policy:' || $1::bigint::text, 0))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := env.newGateParty(t)
			if tc.withRow {
				env.setPolicy(t, g, map[string]any{"credential_rule": "authorized"}).wantStatus(t, http.StatusOK)
			}
			m := env.newGateMember(t, g)
			ctx := context.Background()
			tx, err := env.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx) //nolint:errcheck
			if _, err := tx.Exec(ctx, tc.lockQuery, g.id); err != nil {
				t.Fatalf("writer lock: %v", err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO profile_policy_confirmations (profile_id, party_id, confirmed_by_profile_id) VALUES ($1, $2, $1)`,
				m.profileID, g.id); err != nil {
				t.Fatalf("writer confirm: %v", err)
			}

			// With a row (rule `authorized`) the change is to `any`; with no
			// row (which gates by `any`) it is to `authorized`.
			body := map[string]any{"credential_rule": "any"}
			if !tc.withRow {
				body = map[string]any{"credential_rule": "authorized"}
			}
			done := make(chan *resp, 1)
			go func() { done <- env.setPolicy(t, g, body) }()
			select {
			case <-done:
				t.Fatal("the policy write completed while a confirmation writer held the party policy lock")
			case <-time.After(500 * time.Millisecond):
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("writer commit: %v", err)
			}
			select {
			case r := <-done:
				r.wantStatus(t, http.StatusOK)
			case <-time.After(10 * time.Second):
				t.Fatal("the policy write did not complete after the writer committed")
			}
			if n := env.scanInt64(t, "confirmations", `SELECT count(*) FROM profile_policy_confirmations WHERE party_id = $1`, g.id); n != 0 {
				t.Fatalf("confirmations after the rule change = %d, want 0", n)
			}
		})
	}
}
