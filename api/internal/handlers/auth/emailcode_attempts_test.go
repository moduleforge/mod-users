package auth

// Tests for email-code attempt limiting, the request cooldown, and the
// constant-time dummy comparison on the verify paths.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	db "github.com/moduleforge/mod-users/model/db"
)

// attemptQuerier is a stateful in-memory email_codes store mirroring the SQL
// semantics of GetActiveEmailCode, ClaimEmailCodeAttempt, ConsumeEmailCode,
// CreateEmailCode, and GetLatestEmailCodeCreatedAt.
type attemptQuerier struct {
	stubAuthQuerier

	mu      sync.Mutex
	codes   []db.EmailCode
	nextID  int64
	creates int
}

func (q *attemptQuerier) live(c db.EmailCode) bool {
	return c.ConsumedAt == nil && c.ExpiresAt.Time.After(time.Now())
}

func (q *attemptQuerier) CreateEmailCode(_ context.Context, arg db.CreateEmailCodeParams) (db.EmailCode, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nextID++
	q.creates++
	c := db.EmailCode{
		ID: q.nextID, UserAccountID: arg.UserAccountID, CodeHash: arg.CodeHash,
		Purpose: arg.Purpose, ExpiresAt: arg.ExpiresAt,
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	q.codes = append(q.codes, c)
	return c, nil
}

func (q *attemptQuerier) newest(userID int64, purpose string) (int, bool) {
	idx := -1
	for i, c := range q.codes {
		if c.UserAccountID == userID && c.Purpose == purpose && q.live(c) {
			if idx < 0 || c.CreatedAt.Time.After(q.codes[idx].CreatedAt.Time) || c.ID > q.codes[idx].ID {
				idx = i
			}
		}
	}
	return idx, idx >= 0
}

func (q *attemptQuerier) GetActiveEmailCode(_ context.Context, arg db.GetActiveEmailCodeParams) (db.EmailCode, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if i, ok := q.newest(arg.UserAccountID, arg.Purpose); ok {
		return q.codes[i], nil
	}
	return db.EmailCode{}, pgx.ErrNoRows
}

func (q *attemptQuerier) GetLatestEmailCodeCreatedAt(_ context.Context, arg db.GetLatestEmailCodeCreatedAtParams) (pgtype.Timestamptz, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if i, ok := q.newest(arg.UserAccountID, arg.Purpose); ok {
		return q.codes[i].CreatedAt, nil
	}
	return pgtype.Timestamptz{}, pgx.ErrNoRows
}

func (q *attemptQuerier) ConsumeEmailCode(_ context.Context, id int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.codes {
		if q.codes[i].ID == id && q.codes[i].ConsumedAt == nil {
			now := time.Now()
			q.codes[i].ConsumedAt = &now
		}
	}
	return nil
}

func (q *attemptQuerier) ClaimEmailCodeAttempt(_ context.Context, arg db.ClaimEmailCodeAttemptParams) (db.EmailCode, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.codes {
		c := &q.codes[i]
		if c.ID != arg.ID || c.ConsumedAt != nil || c.FailedAttempts >= arg.MaxAttempts {
			continue
		}
		c.FailedAttempts++
		if c.FailedAttempts >= arg.MaxAttempts {
			now := time.Now()
			c.ConsumedAt = &now
		}
		return *c, nil
	}
	return db.EmailCode{}, pgx.ErrNoRows
}

func (q *attemptQuerier) attempts(id int64) int32 {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, c := range q.codes {
		if c.ID == id {
			return c.FailedAttempts
		}
	}
	return -1
}

const attemptTestCode = "123456"

// seedCode inserts a live code with the given plaintext and creation time.
func (q *attemptQuerier) seedCode(t *testing.T, userID int64, purpose, plain string, createdAt time.Time) db.EmailCode {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nextID++
	c := db.EmailCode{
		ID: q.nextID, UserAccountID: userID, CodeHash: string(hash), Purpose: purpose,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(5 * time.Minute), Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
	}
	q.codes = append(q.codes, c)
	return c
}

func newAttemptFixture(t *testing.T) (*Handler, *attemptQuerier, *recordingSender) {
	t.Helper()
	ua := verifiedAccount("a@example.com", nil)
	q := &attemptQuerier{stubAuthQuerier: stubAuthQuerier{accountByEmail: &ua}}
	sender := &recordingSender{}
	return newGuardTestHandler(q, sender), q, sender
}

func verifyCode(h *Handler, email, code string) *httptest.ResponseRecorder {
	body := `{"email":"` + email + `","code":"` + code + `","purpose":"login"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/email-code/verify", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.EmailCodeVerify(rec, req)
	return rec
}

func TestEmailCodeVerify_ExhaustedCodeRejectsCorrectGuess(t *testing.T) {
	t.Parallel()
	h, q, _ := newAttemptFixture(t)
	q.seedCode(t, 42, "login", attemptTestCode, time.Now())

	var wrongBody string
	for i := 0; i < MaxEmailCodeAttempts; i++ {
		rec := verifyCode(h, "a@example.com", "000000")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d: status = %d, want 401", i+1, rec.Code)
		}
		wrongBody = rec.Body.String()
	}

	rec := verifyCode(h, "a@example.com", attemptTestCode)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("correct code after exhaustion: status = %d, want 401", rec.Code)
	}
	if rec.Body.String() != wrongBody {
		t.Errorf("lockout body %q differs from wrong-code body %q", rec.Body.String(), wrongBody)
	}
}

func TestEmailCodeVerify_CorrectCodeBeforeExhaustionSucceeds(t *testing.T) {
	t.Parallel()
	h, q, _ := newAttemptFixture(t)
	q.seedCode(t, 42, "login", attemptTestCode, time.Now())

	for i := 0; i < MaxEmailCodeAttempts-1; i++ {
		if rec := verifyCode(h, "a@example.com", "000000"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d: status = %d, want 401", i+1, rec.Code)
		}
	}
	if rec := verifyCode(h, "a@example.com", attemptTestCode); rec.Code != http.StatusOK {
		t.Fatalf("correct code before exhaustion: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestEmailCodeVerify_FailedAttemptsDoNotBlockFreshCodeAfterCooldown(t *testing.T) {
	t.Parallel()
	h, q, _ := newAttemptFixture(t)
	// Old code, past the cooldown, then exhausted.
	q.seedCode(t, 42, "login", attemptTestCode, time.Now().Add(-2*EmailCodeRequestCooldown))
	for i := 0; i < MaxEmailCodeAttempts; i++ {
		verifyCode(h, "a@example.com", "000000")
	}
	if rec := verifyCode(h, "a@example.com", attemptTestCode); rec.Code != http.StatusUnauthorized {
		t.Fatalf("exhausted code accepted: status = %d", rec.Code)
	}

	// A fresh request mints a new code (the exhausted one is consumed).
	h.sendEmailCode(httptest.NewRequest(http.MethodPost, "/", nil), "a@example.com", "login")
	if q.creates != 1 {
		t.Fatalf("creates = %d, want 1 fresh code", q.creates)
	}
	// Swap the minted code's hash for a known plaintext so the test can verify it.
	hash, err := bcrypt.GenerateFromPassword([]byte("654321"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	q.mu.Lock()
	fresh := &q.codes[len(q.codes)-1]
	fresh.CodeHash = string(hash)
	freshID := fresh.ID
	q.mu.Unlock()
	if rec := verifyCode(h, "a@example.com", "654321"); rec.Code != http.StatusOK {
		t.Fatalf("fresh code: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := q.attempts(freshID); got != 1 {
		t.Errorf("fresh code attempts = %d, want 1 (counter is per code)", got)
	}
}

func TestSendEmailCode_CooldownSkipsSecondMint(t *testing.T) {
	t.Parallel()
	h, q, sender := newAttemptFixture(t)
	r := httptest.NewRequest(http.MethodPost, "/", nil)

	h.sendEmailCode(r, "a@example.com", "login")
	h.sendEmailCode(r, "a@example.com", "login")
	if q.creates != 1 {
		t.Fatalf("creates within cooldown = %d, want 1", q.creates)
	}
	if sender.callCount() != 1 {
		t.Errorf("emails sent = %d, want 1", sender.callCount())
	}
}

func TestSendEmailCode_MintsAgainAfterCooldown(t *testing.T) {
	t.Parallel()
	h, q, _ := newAttemptFixture(t)
	q.seedCode(t, 42, "login", attemptTestCode, time.Now().Add(-EmailCodeRequestCooldown-time.Second))

	h.sendEmailCode(httptest.NewRequest(http.MethodPost, "/", nil), "a@example.com", "login")
	if q.creates != 1 {
		t.Fatalf("creates after cooldown = %d, want 1", q.creates)
	}
}

func TestSendEmailCode_CooldownIsPerPurpose(t *testing.T) {
	t.Parallel()
	h, q, _ := newAttemptFixture(t)
	q.seedCode(t, 42, "verify_email", attemptTestCode, time.Now())

	h.sendEmailCode(httptest.NewRequest(http.MethodPost, "/", nil), "a@example.com", "login")
	if q.creates != 1 {
		t.Fatalf("creates for other purpose = %d, want 1", q.creates)
	}
}

// TestEmailCodeVerify_DummyCompareOnNonCodePaths asserts the dummy bcrypt
// comparison runs for the unknown-email, no-active-code, and exhausted paths.
// It swaps a package variable, so it must not run in parallel.
func TestEmailCodeVerify_DummyCompareOnNonCodePaths(t *testing.T) {
	var calls atomic.Int32
	orig := dummyCodeCompare
	dummyCodeCompare = func(hash, pw []byte) error {
		calls.Add(1)
		return orig(hash, pw)
	}
	t.Cleanup(func() { dummyCodeCompare = orig })

	t.Run("unknown email", func(t *testing.T) {
		q := &attemptQuerier{} // no account -> ErrNoRows
		h := newGuardTestHandler(q, &recordingSender{})
		calls.Store(0)
		if rec := verifyCode(h, "nobody@example.com", attemptTestCode); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if calls.Load() != 1 {
			t.Errorf("dummy compares = %d, want 1", calls.Load())
		}
	})
	t.Run("no active code", func(t *testing.T) {
		h, _, _ := newAttemptFixture(t)
		calls.Store(0)
		if rec := verifyCode(h, "a@example.com", attemptTestCode); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if calls.Load() != 1 {
			t.Errorf("dummy compares = %d, want 1", calls.Load())
		}
	})
	t.Run("exhausted claim", func(t *testing.T) {
		h, q, _ := newAttemptFixture(t)
		q.seedCode(t, 42, "login", attemptTestCode, time.Now())
		// Stale read: the code looks active to GetActive but the claim finds
		// it exhausted (concurrent consumption).
		q.mu.Lock()
		q.codes[0].FailedAttempts = MaxEmailCodeAttempts
		q.mu.Unlock()
		calls.Store(0)
		if rec := verifyCode(h, "a@example.com", attemptTestCode); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if calls.Load() != 1 {
			t.Errorf("dummy compares = %d, want 1", calls.Load())
		}
	})
}

func TestEmailCodeVerify_ConcurrentGuessesNeverExceedMax(t *testing.T) {
	t.Parallel()
	h, q, _ := newAttemptFixture(t)
	code := q.seedCode(t, 42, "login", attemptTestCode, time.Now())

	const n = 40
	var wg sync.WaitGroup
	var wrong atomic.Int32
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if rec := verifyCode(h, "a@example.com", "000000"); rec.Code == http.StatusUnauthorized {
				wrong.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := q.attempts(code.ID); got != MaxEmailCodeAttempts {
		t.Errorf("claimed attempts = %d, want exactly %d", got, MaxEmailCodeAttempts)
	}
	if int(wrong.Load()) != n {
		t.Errorf("401 responses = %d, want %d", wrong.Load(), n)
	}
	if rec := verifyCode(h, "a@example.com", attemptTestCode); rec.Code != http.StatusUnauthorized {
		t.Errorf("correct code after concurrent exhaustion: status = %d, want 401", rec.Code)
	}
}
