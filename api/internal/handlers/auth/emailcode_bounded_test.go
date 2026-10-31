package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until it holds or five seconds pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func dispatchReq() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/v1/auth/email-code/request", nil)
}

func TestDispatch_PanickingWorkIsRecoveredAndReleasesItsSlot(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	var ran atomic.Int32
	h.dispatch(dispatchReq(), "panic", func(context.Context) {
		ran.Add(1)
		panic("boom: secret-code-123456")
	})
	waitFor(t, "the job to run", func() bool { return ran.Load() == 1 })
	waitFor(t, "the slot to be released", func() bool { return len(h.emailSlotChan()) == 0 })
}

func TestRunBounded_RecoversPanic(t *testing.T) {
	t.Parallel()
	runBounded(context.Background(), time.Second, "panic", func(context.Context) { panic("x") })
}

func TestRunBounded_DeadlineCancelsBlockedWork(t *testing.T) {
	t.Parallel()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runBounded(context.Background(), 50*time.Millisecond, "blocked", func(ctx context.Context) {
			<-ctx.Done()
			done <- ctx.Err()
		})
	}()
	select {
	case err := <-done:
		if err != context.DeadlineExceeded {
			t.Errorf("err = %v, want DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked work never observed cancellation")
	}
	<-finished
}

func TestDispatch_SaturatedBoundDropsWork(t *testing.T) {
	t.Parallel()
	h := &Handler{emailConcurrency: 1}
	release := make(chan struct{})
	var started, dropped atomic.Int32
	h.dispatch(dispatchReq(), "first", func(context.Context) {
		started.Add(1)
		<-release
	})
	waitFor(t, "the first job to start", func() bool { return started.Load() == 1 })

	h.dispatch(dispatchReq(), "second", func(context.Context) { dropped.Add(1) })
	time.Sleep(100 * time.Millisecond)
	if dropped.Load() != 0 {
		t.Error("a job dispatched while every slot was busy still ran")
	}
	close(release)
	waitFor(t, "the slot to be released", func() bool { return len(h.emailSlotChan()) == 0 })

	// With the slot free again a new job runs.
	h.dispatch(dispatchReq(), "third", func(context.Context) { dropped.Add(1) })
	waitFor(t, "the third job to run", func() bool { return dropped.Load() == 1 })
}

func TestAnswerAfterFloor_HoldsTheAnswerForTheFloor(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	start := time.Now()
	answerAfterFloor(rec, dispatchReq(), start)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if elapsed := time.Since(start); elapsed < antiEnumerationFloor-20*time.Millisecond {
		t.Errorf("answered after %v, want at least about %v", elapsed, antiEnumerationFloor)
	}
}
