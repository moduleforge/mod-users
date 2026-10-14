package auth

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type panicSender struct{ called atomic.Int32 }

func (p *panicSender) Send(context.Context, string, string, string) error {
	p.called.Add(1)
	panic("boom: secret-code-123456")
}

type deadlineSender struct {
	started chan struct{}
	release chan struct{}
	done    chan error
}

func (b *deadlineSender) Send(ctx context.Context, _, _, _ string) error {
	b.started <- struct{}{}
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		b.done <- ctx.Err()
		return ctx.Err()
	}
}

func emailCodeReq() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/email-code/request",
		bytes.NewBufferString(`{"email":"user@example.com","purpose":"login"}`))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestEmailCodeRequest_PanickingSenderRecovered(t *testing.T) {
	t.Parallel()
	named := namedAccount("user@example.com")
	ps := &panicSender{}
	h := newGuardTestHandler(&stubAuthQuerier{accountByEmail: &named}, ps)

	rec := httptest.NewRecorder()
	h.EmailCodeRequest(rec, emailCodeReq())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for ps.called.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ps.called.Load() != 1 {
		t.Fatalf("sender called %d times, want 1", ps.called.Load())
	}
	// Slot must be released after the panic.
	deadline = time.Now().Add(5 * time.Second)
	for len(h.emailCodeSlotChan()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(h.emailCodeSlotChan()); n != 0 {
		t.Errorf("slot not released after panic: %d held", n)
	}
}

func TestRunBoundedEmailWork_RecoversPanic(t *testing.T) {
	t.Parallel()
	runBoundedEmailWork(context.Background(), time.Second, func(context.Context) { panic("x") })
}

func TestRunBoundedEmailWork_DeadlineCancelsBlockedWork(t *testing.T) {
	t.Parallel()
	bs := &deadlineSender{started: make(chan struct{}, 1), release: make(chan struct{}), done: make(chan error, 1)}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runBoundedEmailWork(context.Background(), 50*time.Millisecond, func(ctx context.Context) {
			_ = bs.Send(ctx, "", "", "")
		})
	}()
	select {
	case err := <-bs.done:
		if err != context.DeadlineExceeded {
			t.Errorf("err = %v, want DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked sender never observed cancellation")
	}
	<-finished
}

func TestEmailCodeRequest_SaturatedBoundDropsWork(t *testing.T) {
	t.Parallel()
	named := namedAccount("user@example.com")
	bs := &deadlineSender{started: make(chan struct{}, 4), release: make(chan struct{}), done: make(chan error, 4)}
	h := newGuardTestHandler(&stubAuthQuerier{accountByEmail: &named}, bs)
	h.emailCodeConcurrency = 1

	rec := httptest.NewRecorder()
	h.EmailCodeRequest(rec, emailCodeReq())
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first status = %d, want 204", rec.Code)
	}
	select {
	case <-bs.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first send never started")
	}

	start := time.Now()
	rec2 := httptest.NewRecorder()
	h.EmailCodeRequest(rec2, emailCodeReq())
	elapsed := time.Since(start)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("saturated status = %d, want 204", rec2.Code)
	}
	if elapsed < 150*time.Millisecond || elapsed > 600*time.Millisecond {
		t.Errorf("saturated latency = %v, want about 200ms", elapsed)
	}
	select {
	case <-bs.started:
		t.Error("dropped request still started a send")
	case <-time.After(100 * time.Millisecond):
	}
	close(bs.release)
}
