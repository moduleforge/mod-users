package handlers

// Tests for step-up code attempt limiting and the request cooldown, driving the
// real StepUpVerify / sendStepUpCode through the injected emailCodeStore.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	authhandlers "github.com/moduleforge/mod-users/api/internal/handlers/auth"
	db "github.com/moduleforge/mod-users/model/db"
)

const (
	stepUpAttemptUserID = int64(1)
	stepUpAttemptCode   = "123456"
)

func newStepUpAttemptHandler(t *testing.T) (*IdentitiesHandler, *stepUpFakeQueries, *fakeSender) {
	t.Helper()
	q := newStepUpFakeQueries()
	sender := &fakeSender{}
	h := &IdentitiesHandler{
		codes:     q,
		sender:    sender,
		jwtSecret: stepUpTestSecret,
		consumed:  &sync.Map{},
	}
	return h, q, sender
}

// seedStepUpCode stores a live credential_change code created at createdAt.
func seedStepUpCode(t *testing.T, q *stepUpFakeQueries, plain string, createdAt time.Time) db.EmailCode {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	id := q.nextCodeID
	q.nextCodeID++
	c := db.EmailCode{
		ID: id, UserAccountID: stepUpAttemptUserID, CodeHash: string(hash), Purpose: "credential_change",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(5 * time.Minute), Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
	}
	q.emailCodes[emailCodeKey(stepUpAttemptUserID, "credential_change")] = c
	return c
}

func stepUpVerifyCall(h *IdentitiesHandler, code string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/self/credential/step-up/verify",
		strings.NewReader(`{"code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withUC(req, newTestUC(stepUpAttemptUserID, 10))
	rec := httptest.NewRecorder()
	h.StepUpVerify(rec, req)
	return rec
}

func TestAttemptLimitConstantsMatchAuthPackage(t *testing.T) {
	t.Parallel()
	if maxEmailCodeAttempts != authhandlers.MaxEmailCodeAttempts {
		t.Errorf("maxEmailCodeAttempts = %d, auth.MaxEmailCodeAttempts = %d; must match",
			maxEmailCodeAttempts, authhandlers.MaxEmailCodeAttempts)
	}
	if emailCodeRequestCooldown != authhandlers.EmailCodeRequestCooldown {
		t.Errorf("emailCodeRequestCooldown = %v, auth.EmailCodeRequestCooldown = %v; must match",
			emailCodeRequestCooldown, authhandlers.EmailCodeRequestCooldown)
	}
}

func TestStepUpVerify_ExhaustedCodeRejectsCorrectGuess(t *testing.T) {
	t.Parallel()
	h, q, _ := newStepUpAttemptHandler(t)
	seedStepUpCode(t, q, stepUpAttemptCode, time.Now())

	var wrongBody string
	for i := 0; i < maxEmailCodeAttempts; i++ {
		rec := stepUpVerifyCall(h, "000000")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d: status = %d, want 401", i+1, rec.Code)
		}
		wrongBody = rec.Body.String()
	}

	rec := stepUpVerifyCall(h, stepUpAttemptCode)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("correct code after exhaustion: status = %d, want 401", rec.Code)
	}
	if rec.Body.String() != wrongBody {
		t.Errorf("lockout body %q differs from wrong-code body %q", rec.Body.String(), wrongBody)
	}
}

func TestStepUpVerify_CorrectCodeBeforeExhaustionSucceeds(t *testing.T) {
	t.Parallel()
	h, q, _ := newStepUpAttemptHandler(t)
	seedStepUpCode(t, q, stepUpAttemptCode, time.Now())

	for i := 0; i < maxEmailCodeAttempts-1; i++ {
		if rec := stepUpVerifyCall(h, "000000"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d: status = %d, want 401", i+1, rec.Code)
		}
	}
	rec := stepUpVerifyCall(h, stepUpAttemptCode)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct code before exhaustion: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "step_up_token") {
		t.Errorf("body lacks step_up_token: %s", rec.Body.String())
	}
}

func TestStepUpVerify_FailedAttemptsDoNotBlockFreshCodeAfterCooldown(t *testing.T) {
	t.Parallel()
	h, q, _ := newStepUpAttemptHandler(t)
	seedStepUpCode(t, q, stepUpAttemptCode, time.Now().Add(-2*emailCodeRequestCooldown))
	for i := 0; i < maxEmailCodeAttempts; i++ {
		stepUpVerifyCall(h, "000000")
	}
	if rec := stepUpVerifyCall(h, stepUpAttemptCode); rec.Code != http.StatusUnauthorized {
		t.Fatalf("exhausted code accepted: status = %d", rec.Code)
	}

	// The exhausted code is consumed, so a new request mints a fresh one.
	h.sendStepUpCode(withUC(httptest.NewRequest(http.MethodPost, "/", nil), newTestUC(stepUpAttemptUserID, 10)),
		newTestUC(stepUpAttemptUserID, 10))
	q.mu.Lock()
	_, minted := q.emailCodes[emailCodeKey(stepUpAttemptUserID, "credential_change")]
	q.mu.Unlock()
	if !minted {
		t.Fatal("no fresh code minted after the exhausted one was consumed")
	}
}

func TestSendStepUpCode_CooldownSkipsSecondMint(t *testing.T) {
	t.Parallel()
	h, _, sender := newStepUpAttemptHandler(t)
	uc := newTestUC(stepUpAttemptUserID, 10)
	r := withUC(httptest.NewRequest(http.MethodPost, "/", nil), uc)

	h.sendStepUpCode(r, uc)
	h.sendStepUpCode(r, uc)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("emails sent within cooldown = %d, want 1", len(sender.sent))
	}
}

func TestSendStepUpCode_MintsAgainAfterCooldown(t *testing.T) {
	t.Parallel()
	h, q, sender := newStepUpAttemptHandler(t)
	seedStepUpCode(t, q, stepUpAttemptCode, time.Now().Add(-emailCodeRequestCooldown-time.Second))
	uc := newTestUC(stepUpAttemptUserID, 10)

	h.sendStepUpCode(withUC(httptest.NewRequest(http.MethodPost, "/", nil), uc), uc)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("emails sent after cooldown = %d, want 1", len(sender.sent))
	}
}

func TestStepUpVerify_ConcurrentGuessesNeverExceedMax(t *testing.T) {
	t.Parallel()
	h, q, _ := newStepUpAttemptHandler(t)
	seedStepUpCode(t, q, stepUpAttemptCode, time.Now())

	// Count claims that were granted by wrapping the fake.
	counting := &claimCountingStore{stepUpFakeQueries: q}
	h.codes = counting

	const n = 40
	var wg sync.WaitGroup
	var unauthorized atomic.Int32
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if rec := stepUpVerifyCall(h, "000000"); rec.Code == http.StatusUnauthorized {
				unauthorized.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := counting.granted.Load(); got != maxEmailCodeAttempts {
		t.Errorf("granted claims = %d, want exactly %d", got, maxEmailCodeAttempts)
	}
	if int(unauthorized.Load()) != n {
		t.Errorf("401 responses = %d, want %d", unauthorized.Load(), n)
	}
}

// claimCountingStore counts ClaimEmailCodeAttempt calls that returned a row.
type claimCountingStore struct {
	*stepUpFakeQueries
	granted atomic.Int32
}

func (c *claimCountingStore) ClaimEmailCodeAttempt(ctx context.Context, arg db.ClaimEmailCodeAttemptParams) (db.EmailCode, error) {
	row, err := c.stepUpFakeQueries.ClaimEmailCodeAttempt(ctx, arg)
	if err == nil {
		c.granted.Add(1)
	}
	return row, err
}

func TestSendStepUpCode_CooldownCoversExhaustedCode(t *testing.T) {
	t.Parallel()
	h, q, sender := newStepUpAttemptHandler(t)
	seedStepUpCode(t, q, stepUpAttemptCode, time.Now())
	for i := 0; i < maxEmailCodeAttempts; i++ {
		stepUpVerifyCall(h, "000000")
	}
	if rec := stepUpVerifyCall(h, stepUpAttemptCode); rec.Code != http.StatusUnauthorized {
		t.Fatalf("exhausted code accepted: status = %d", rec.Code)
	}
	uc := newTestUC(stepUpAttemptUserID, 10)

	h.sendStepUpCode(withUC(httptest.NewRequest(http.MethodPost, "/", nil), uc), uc)
	sender.mu.Lock()
	sent := len(sender.sent)
	sender.mu.Unlock()
	q.mu.Lock()
	creates := q.creates
	q.mu.Unlock()
	if creates != 0 || sent != 0 {
		t.Fatalf("after exhaustion within cooldown: creates=%d emails=%d, want 0/0", creates, sent)
	}

	// Age the exhausted code past the cooldown: a new code is minted.
	q.mu.Lock()
	q.lastCreated[emailCodeKey(stepUpAttemptUserID, "credential_change")] = time.Now().Add(-emailCodeRequestCooldown - time.Second)
	q.mu.Unlock()
	h.sendStepUpCode(withUC(httptest.NewRequest(http.MethodPost, "/", nil), uc), uc)
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("emails sent after cooldown = %d, want 1", len(sender.sent))
	}
}

func TestSendStepUpCode_ConcurrentRequestsMintOnce(t *testing.T) {
	t.Parallel()
	h, q, sender := newStepUpAttemptHandler(t)
	uc := newTestUC(stepUpAttemptUserID, 10)

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h.sendStepUpCode(withUC(httptest.NewRequest(http.MethodPost, "/", nil), uc), uc)
		}()
	}
	close(start)
	wg.Wait()

	q.mu.Lock()
	creates := q.creates
	q.mu.Unlock()
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if creates != 1 || len(sender.sent) != 1 {
		t.Errorf("creates=%d emails=%d, want exactly 1/1", creates, len(sender.sent))
	}
}
