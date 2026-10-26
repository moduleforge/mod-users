package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// The unit tests in this package drive the policy-lock callers over fake
// queriers with a nil transaction, which the production locker refuses. This
// test-only init swaps the seam for a no-op; TestLockPartyPolicyShared_NilTxFailsClosed
// exercises the real function directly.
func init() {
	partyPolicySharedLocker = func(context.Context, pgx.Tx, int64) error { return nil }
}

func TestLockPartyPolicyShared_NilTxFailsClosed(t *testing.T) {
	if err := lockPartyPolicyShared(context.Background(), nil, 5); !errors.Is(err, errNilPolicyLockTx) {
		t.Fatalf("lockPartyPolicyShared(nil tx) = %v, want errNilPolicyLockTx", err)
	}
}
