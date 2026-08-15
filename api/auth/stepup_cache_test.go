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

	// The two preceding tests in this file each start their own janitor
	// goroutine(s) and cancel them via `defer cancel()` at return, but that
	// cancellation only closes the context's Done channel — the janitor
	// goroutines themselves exit asynchronously, on their own schedule. If
	// this test samples its baseline before those goroutines have actually
	// unwound, the baseline is polluted by soon-to-exit goroutines that
	// belong to a *different* cache/janitor than the one under test here:
	// as they exit during this test's own polling windows below, their
	// exits can mask (or spuriously satisfy) this test's own goroutine-count
	// deltas, independent of whether this test's own janitor is actually
	// behaving correctly. Wait for the ambient goroutine count to settle
	// before sampling baseline, so it reflects only steady-state goroutines
	// and this test's own delta is observed cleanly.
	baseline := waitForStableGoroutineCount(t)

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

// waitForStableGoroutineCount polls runtime.NumGoroutine until it reports the
// same value across a short run of consecutive samples (or a 2-second
// deadline elapses) and returns that value. It exists to settle out
// goroutines left over from previously run tests in this file — e.g. janitor
// goroutines whose owning test already called cancel() but which have not
// yet been scheduled to actually exit — so a subsequent baseline read isn't
// polluted by goroutines unrelated to the test taking the reading.
func waitForStableGoroutineCount(t *testing.T) int {
	t.Helper()
	const stableSamples = 5
	const pollInterval = 5 * time.Millisecond
	deadline := time.Now().Add(2 * time.Second)

	last := runtime.NumGoroutine()
	seenStable := 1
	for {
		time.Sleep(pollInterval)
		n := runtime.NumGoroutine()
		if n == last {
			seenStable++
			if seenStable >= stableSamples {
				return n
			}
		} else {
			last = n
			seenStable = 1
		}
		if time.Now().After(deadline) {
			t.Logf("goroutine count did not stabilize within deadline; using last observed value %d as baseline", n)
			return n
		}
	}
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
