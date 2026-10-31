package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The unit tests in this package drive the policy-lock callers over fake
// queriers with a nil transaction, which the production locker refuses. Tests
// that need to get past the lock opt in per instance with noopPolicyLock; the
// rest run the fail-closed production locker. TestLockPartyPolicyShared_NilTxFailsClosed
// exercises the real function directly.
func noopPolicyLock(context.Context, pgx.Tx, int64) error { return nil }

func TestLockPartyPolicyShared_NilTxFailsClosed(t *testing.T) {
	t.Parallel()
	if err := lockPartyPolicyShared(context.Background(), nil, 5); !errors.Is(err, errNilPolicyLockTx) {
		t.Fatalf("lockPartyPolicyShared(nil tx) = %v, want errNilPolicyLockTx", err)
	}
}

func TestPolicySharedLocker_DefaultsToFailClosed(t *testing.T) {
	t.Parallel()
	var unset policySharedLocker
	if err := unset.orDefault()(context.Background(), nil, 5); !errors.Is(err, errNilPolicyLockTx) {
		t.Fatalf("default locker(nil tx) = %v, want errNilPolicyLockTx", err)
	}
}
