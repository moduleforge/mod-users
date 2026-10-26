package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// partyPolicyLockKey derives the transaction-scoped advisory-lock key of a
// party's authentication policy. It stands in for the policy row's lock while
// the party has no row yet. The key is computed in SQL from the party id so no
// caller-supplied text reaches it.
const partyPolicyLockKey = `hashtextextended('mod_users.party_authn_policy:' || $1::bigint::text, 0)`

// lockPartyPolicyExclusive serializes the caller, inside tx, with every other
// writer of the party's authentication policy and with every confirmation
// writer (lockPartyPolicyShared). It takes the per-party advisory lock
// exclusively, which covers a party with no policy row yet, then locks the
// policy row FOR UPDATE when one exists, so a reader holding the row FOR SHARE
// is waited out. Both locks release at the end of tx.
func lockPartyPolicyExclusive(ctx context.Context, tx pgx.Tx, partyID int64) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(`+partyPolicyLockKey+`)`, partyID); err != nil {
		return fmt.Errorf("users: lock party policy: %w", err)
	}
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM mod_users.party_authn_policies WHERE party_id = $1 FOR UPDATE`, partyID).Scan(&one)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("users: lock party policy row: %w", err)
	}
	return nil
}

// lockPartyPolicyShared is the shared counterpart taken by the confirmation
// writers before they read the party's policy: the policy row FOR SHARE, or,
// when the party has no row, the per-party advisory lock in shared mode. It
// waits for an in-flight PutAuthnPolicy and holds it off until tx ends, so a
// confirmation recorded under the old policy cannot outlive a policy change.
//
// A nil tx locks nothing: it occurs only in unit tests that drive the callers
// over fake queriers, never in production, where every caller holds a real
// transaction.
func lockPartyPolicyShared(ctx context.Context, tx pgx.Tx, partyID int64) error {
	if tx == nil {
		return nil
	}
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM mod_users.party_authn_policies WHERE party_id = $1 FOR SHARE`, partyID).Scan(&one)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("users: lock party policy row: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(`+partyPolicyLockKey+`)`, partyID); err != nil {
		return fmt.Errorf("users: lock party policy: %w", err)
	}
	return nil
}
