package auth

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	inner "github.com/moduleforge/mod-users/api/internal/auth"
)

// TestNewStepUpConsumedCache_ReturnsDistinctNonNilInstances confirms
// NewStepUpConsumedCache returns a usable, non-nil *sync.Map and that two
// calls do not share state — each call must construct its own cache and
// start its own janitor.
func TestNewStepUpConsumedCache_ReturnsDistinctNonNilInstances(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := NewStepUpConsumedCache(ctx)
	if first == nil {
		t.Fatal("NewStepUpConsumedCache returned nil")
	}

	second := NewStepUpConsumedCache(ctx)
	if second == nil {
		t.Fatal("NewStepUpConsumedCache returned nil on second call")
	}

	if first == second {
		t.Fatal("two calls to NewStepUpConsumedCache returned the same *sync.Map instance, want distinct instances")
	}

	// Prove the two instances are actually independent stores, not aliases
	// of shared underlying state.
	first.Store("marker", int64(0))
	if _, ok := second.Load("marker"); ok {
		t.Fatal("storing into the first cache is visible in the second cache; instances are not independent")
	}
}

// TestNewStepUpConsumedCache_LiveSingleUseStore drives a full
// IssueStepUpToken/VerifyStepUpToken round-trip against the map returned by
// NewStepUpConsumedCache, proving it is the same live single-use store the
// janitor and handler share: a token verifies once successfully, and a
// second verification of the same token is rejected with
// inner.ErrStepUpRequired.
func TestNewStepUpConsumedCache_LiveSingleUseStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	consumed := NewStepUpConsumedCache(ctx)

	secret := []byte("test-secret")
	const userAccountID int64 = 42

	token, _, _, err := inner.IssueStepUpToken(secret, userAccountID, inner.StepUpTTL)
	if err != nil {
		t.Fatalf("IssueStepUpToken returned unexpected error: %v", err)
	}

	if err := inner.VerifyStepUpToken(secret, token, userAccountID, consumed); err != nil {
		t.Fatalf("first VerifyStepUpToken call returned unexpected error: %v", err)
	}

	err = inner.VerifyStepUpToken(secret, token, userAccountID, consumed)
	if !errors.Is(err, inner.ErrStepUpRequired) {
		t.Fatalf("second VerifyStepUpToken call (replay) = %v, want %v", err, inner.ErrStepUpRequired)
	}
}

// TestNewStepUpConsumedCache_JanitorStopsOnCancel confirms the constructor
// accepts a cancellable context, returns immediately without blocking or
// panicking, and that the janitor goroutine it starts actually terminates
// once ctx is cancelled (observed via a goroutine-count delta, since
// StartStepUpJanitor exposes no other exit signal). Pruning-on-tick timing
// is out of scope: StartStepUpJanitor uses a hardcoded 1-minute ticker with
// no injection seam — this test only asserts the goroutine exits on cancel,
// not that it prunes on a tick.
func TestNewStepUpConsumedCache_JanitorStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	baseline := runtime.NumGoroutine()

	returned := make(chan struct{})
	var consumed any
	go func() {
		consumed = NewStepUpConsumedCache(ctx)
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("NewStepUpConsumedCache blocked instead of returning immediately")
	}
	if consumed == nil {
		t.Fatal("NewStepUpConsumedCache returned nil")
	}

	// The janitor goroutine StartStepUpJanitor launches is scheduled
	// asynchronously, so poll for the goroutine count to rise above baseline
	// (rather than asserting on the very next NumGoroutine call, which can
	// race the scheduler) before proceeding to the cancellation check below.
	waitForGoroutineCount(t, func(n int) bool { return n > baseline }, baseline,
		"goroutine count never rose above baseline after starting the janitor")

	// Cancelling ctx must not panic or hang the janitor goroutine it started,
	// and the goroutine must actually exit — poll NumGoroutine back down to
	// (at most) the pre-start baseline rather than asserting on a fixed
	// sleep.
	cancel()

	waitForGoroutineCount(t, func(n int) bool { return n <= baseline }, baseline,
		"janitor goroutine did not exit within the deadline after ctx cancellation")
}

// waitForGoroutineCount polls runtime.NumGoroutine until want returns true or
// a 2-second deadline elapses, at which point it fails the test with msg and
// the last observed count (plus baseline, for context).
func waitForGoroutineCount(t *testing.T, want func(n int) bool, baseline int, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if want(n) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: baseline=%d, last observed=%d", msg, baseline, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
