//go:build integration

package authz_test

// email_code_integration_test.go exercises the email-code attempt limiting,
// request cooldown, and single-use consumption against a real Postgres through
// the real db.Querier (and, for the end-to-end cases, the real auth.Handler),
// proving the SQL guarantees that the in-memory handler tests only mirror.
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	coredb "github.com/moduleforge/core-model/db"
	"golang.org/x/crypto/bcrypt"

	"github.com/moduleforge/mod-users/api/internal/handlers/auth"
	db "github.com/moduleforge/mod-users/model/db"
)

const (
	ecConcurrency = 16
	ecPlainCode   = "123456"
)

// ecUser seeds a natural-person user account and returns its user_accounts.id.
func ecUser(t *testing.T, email string) int64 {
	t.Helper()
	seedUser(t, email, false)
	var id int64
	if err := integPool.QueryRow(context.Background(),
		`SELECT id FROM user_accounts WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatalf("ecUser: resolve user_account id: %v", err)
	}
	return id
}

func ecHash(t *testing.T, plain string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(h)
}

func ecExpiry() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now().Add(5 * time.Minute), Valid: true}
}

// ecMint inserts a live code directly (bypassing the cooldown).
func ecMint(t *testing.T, q *db.Queries, userID int64, purpose, plain string) db.EmailCode {
	t.Helper()
	c, err := q.CreateEmailCode(context.Background(), db.CreateEmailCodeParams{
		UserAccountID: userID, CodeHash: ecHash(t, plain), Purpose: purpose, ExpiresAt: ecExpiry(),
	})
	if err != nil {
		t.Fatalf("CreateEmailCode: %v", err)
	}
	return c
}

// ecBackdate moves every code of (user, purpose) created_at into the past.
func ecBackdate(t *testing.T, userID int64, purpose string, by time.Duration) {
	t.Helper()
	if _, err := integPool.Exec(context.Background(),
		`UPDATE email_codes SET created_at = created_at - make_interval(secs => $3)
		 WHERE user_account_id = $1 AND purpose = $2`, userID, purpose, by.Seconds()); err != nil {
		t.Fatalf("ecBackdate: %v", err)
	}
}

func ecCountCodes(t *testing.T, userID int64, purpose string) int {
	t.Helper()
	var n int
	if err := integPool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_codes WHERE user_account_id = $1 AND purpose = $2`,
		userID, purpose).Scan(&n); err != nil {
		t.Fatalf("count codes: %v", err)
	}
	return n
}

// ecRunConcurrently releases n goroutines simultaneously and waits for them.
func ecRunConcurrently(n int, fn func(i int)) {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer done.Done()
			ready.Done()
			<-start
			fn(i)
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
}

func TestInteg_EmailCode_ClaimAttemptNeverExceedsMax(t *testing.T) {
	q := db.New(integPool)
	uid := ecUser(t, "ec-claim@example.com")
	code := ecMint(t, q, uid, "login", ecPlainCode)

	const max = int32(auth.MaxEmailCodeAttempts)
	var claimed, noRows, other atomic.Int32
	ecRunConcurrently(ecConcurrency, func(int) {
		_, err := q.ClaimEmailCodeAttempt(context.Background(),
			db.ClaimEmailCodeAttemptParams{ID: code.ID, MaxAttempts: max})
		switch {
		case err == nil:
			claimed.Add(1)
		case errors.Is(err, pgx.ErrNoRows):
			noRows.Add(1)
		default:
			other.Add(1)
			t.Errorf("claim: unexpected error: %v", err)
		}
	})

	if got := claimed.Load(); got != max {
		t.Errorf("successful claims = %d, want exactly %d", got, max)
	}
	if got := noRows.Load(); got != ecConcurrency-max {
		t.Errorf("no-row claims = %d, want %d", got, ecConcurrency-max)
	}
	var attempts int32
	var consumed pgtype.Timestamptz
	if err := integPool.QueryRow(context.Background(),
		`SELECT failed_attempts, consumed_at FROM email_codes WHERE id = $1`, code.ID).Scan(&attempts, &consumed); err != nil {
		t.Fatalf("read code: %v", err)
	}
	if attempts != max {
		t.Errorf("failed_attempts = %d, want %d (never above the bound)", attempts, max)
	}
	if !consumed.Valid {
		t.Error("code not consumed at the attempt bound")
	}
	if _, err := q.GetActiveEmailCode(context.Background(),
		db.GetActiveEmailCodeParams{UserAccountID: uid, Purpose: "login"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetActiveEmailCode after exhaustion: got %v, want ErrNoRows", err)
	}
}

func TestInteg_EmailCode_CooldownCoversExhaustedAndConsumedCodes(t *testing.T) {
	q := db.New(integPool)
	ctx := context.Background()
	mint := func(uid int64) (db.EmailCode, error) {
		return q.CreateEmailCodeWithCooldown(ctx, db.CreateEmailCodeWithCooldownParams{
			UserAccountID: uid, CodeHash: ecHash(t, ecPlainCode), Purpose: "login",
			ExpiresAt: ecExpiry(), CooldownSecs: int32(auth.EmailCodeRequestCooldown / time.Second),
		})
	}

	t.Run("exhausted", func(t *testing.T) {
		uid := ecUser(t, "ec-cool-exhausted@example.com")
		first, err := mint(uid)
		if err != nil {
			t.Fatalf("first mint: %v", err)
		}
		for i := 0; i < auth.MaxEmailCodeAttempts; i++ {
			if _, err := q.ClaimEmailCodeAttempt(ctx, db.ClaimEmailCodeAttemptParams{
				ID: first.ID, MaxAttempts: auth.MaxEmailCodeAttempts}); err != nil {
				t.Fatalf("claim %d: %v", i+1, err)
			}
		}
		if _, err := mint(uid); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("re-mint after exhaustion inside window: got %v, want ErrNoRows", err)
		}
		if n := ecCountCodes(t, uid, "login"); n != 1 {
			t.Errorf("codes after blocked re-mint = %d, want 1", n)
		}
		ecBackdate(t, uid, "login", auth.EmailCodeRequestCooldown+time.Second)
		if _, err := mint(uid); err != nil {
			t.Fatalf("re-mint after window: %v", err)
		}
		if n := ecCountCodes(t, uid, "login"); n != 2 {
			t.Errorf("codes after allowed re-mint = %d, want 2", n)
		}
	})

	t.Run("consumed by success", func(t *testing.T) {
		uid := ecUser(t, "ec-cool-consumed@example.com")
		first, err := mint(uid)
		if err != nil {
			t.Fatalf("first mint: %v", err)
		}
		if n, err := q.ConsumeEmailCode(ctx, first.ID); err != nil || n != 1 {
			t.Fatalf("consume: n=%d err=%v", n, err)
		}
		if _, err := mint(uid); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("re-mint after consume inside window: got %v, want ErrNoRows", err)
		}
	})

	t.Run("other purpose and other user unaffected", func(t *testing.T) {
		uid := ecUser(t, "ec-cool-scope@example.com")
		other := ecUser(t, "ec-cool-scope-other@example.com")
		if _, err := mint(uid); err != nil {
			t.Fatalf("mint: %v", err)
		}
		if _, err := mint(other); err != nil {
			t.Errorf("mint for a different user blocked: %v", err)
		}
		if _, err := q.CreateEmailCodeWithCooldown(ctx, db.CreateEmailCodeWithCooldownParams{
			UserAccountID: uid, CodeHash: ecHash(t, ecPlainCode), Purpose: "verify_email",
			ExpiresAt: ecExpiry(), CooldownSecs: 60}); err != nil {
			t.Errorf("mint for a different purpose blocked: %v", err)
		}
	})
}

func TestInteg_EmailCode_ConcurrentCooldownInsertsYieldOneCode(t *testing.T) {
	q := db.New(integPool)
	uid := ecUser(t, "ec-cool-concurrent@example.com")

	var created, skipped atomic.Int32
	ecRunConcurrently(ecConcurrency, func(int) {
		_, err := q.CreateEmailCodeWithCooldown(context.Background(), db.CreateEmailCodeWithCooldownParams{
			UserAccountID: uid, CodeHash: ecHash(t, ecPlainCode), Purpose: "login",
			ExpiresAt: ecExpiry(), CooldownSecs: 60,
		})
		switch {
		case err == nil:
			created.Add(1)
		case errors.Is(err, pgx.ErrNoRows):
			skipped.Add(1)
		default:
			t.Errorf("mint: unexpected error: %v", err)
		}
	})

	if created.Load() != 1 || skipped.Load() != ecConcurrency-1 {
		t.Errorf("created=%d skipped=%d, want 1 and %d", created.Load(), skipped.Load(), ecConcurrency-1)
	}
	if n := ecCountCodes(t, uid, "login"); n != 1 {
		t.Errorf("rows inserted = %d, want 1", n)
	}
}

func TestInteg_EmailCode_ConsumeYieldsOneWinner(t *testing.T) {
	q := db.New(integPool)
	uid := ecUser(t, "ec-consume@example.com")
	code := ecMint(t, q, uid, "login", ecPlainCode)

	var affected atomic.Int64
	ecRunConcurrently(ecConcurrency, func(int) {
		n, err := q.ConsumeEmailCode(context.Background(), code.ID)
		if err != nil {
			t.Errorf("consume: %v", err)
			return
		}
		affected.Add(n)
	})
	if affected.Load() != 1 {
		t.Errorf("rows consumed across %d concurrent consumers = %d, want 1", ecConcurrency, affected.Load())
	}
}

// ---------------------------------------------------------------------------
// Handler-level (real auth.Handler over the real Querier)
// ---------------------------------------------------------------------------

type ecSender struct {
	mu     sync.Mutex
	bodies []string
}

func (s *ecSender) Send(_ context.Context, _, _, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, body)
	return nil
}

func (s *ecSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

var ecCodeRe = regexp.MustCompile(`code is: (\d{6})`)

func (s *ecSender) lastCode(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		t.Fatal("no email sent")
	}
	m := ecCodeRe.FindStringSubmatch(s.bodies[len(s.bodies)-1])
	if m == nil {
		t.Fatalf("no code in email body %q", s.bodies[len(s.bodies)-1])
	}
	return m[1]
}

func ecHandler(sender auth.Sender) *auth.Handler {
	return auth.New(integPool, db.New(integPool), coredb.New(integPool),
		"integration-test-jwt-secret-0123456789abcdef", "mod-users-test", sender, "http://localhost")
}

func ecRequest(h *auth.Handler, email string) int {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/email-code/request",
		bytes.NewBufferString(`{"email":"`+email+`","purpose":"login"}`))
	rec := httptest.NewRecorder()
	h.EmailCodeRequest(rec, req)
	return rec.Code
}

func ecVerify(h *auth.Handler, email, code string) int {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/email-code/verify",
		bytes.NewBufferString(`{"email":"`+email+`","code":"`+code+`","purpose":"login"}`))
	rec := httptest.NewRecorder()
	h.EmailCodeVerify(rec, req)
	return rec.Code
}

func TestInteg_EmailCode_Handler_ExhaustThenRequestEndToEnd(t *testing.T) {
	const email = "ec-e2e@example.com"
	uid := ecUser(t, email)
	sender := &ecSender{}
	h := ecHandler(sender)

	if code := ecRequest(h, email); code != http.StatusNoContent {
		t.Fatalf("request: status %d, want 204", code)
	}
	if sender.count() != 1 {
		t.Fatalf("emails after first request = %d, want 1", sender.count())
	}
	real := sender.lastCode(t)
	wrong := "000000"
	if real == wrong {
		wrong = "000001"
	}

	for i := 0; i < auth.MaxEmailCodeAttempts; i++ {
		if code := ecVerify(h, email, wrong); code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d: status %d, want 401", i+1, code)
		}
	}
	if code := ecVerify(h, email, real); code != http.StatusUnauthorized {
		t.Fatalf("correct code after exhaustion: status %d, want 401", code)
	}

	// Re-requesting inside the cooldown must not mint a fresh guess budget.
	if code := ecRequest(h, email); code != http.StatusNoContent {
		t.Fatalf("re-request: status %d, want 204", code)
	}
	if sender.count() != 1 {
		t.Errorf("emails after in-window re-request = %d, want 1 (no new code)", sender.count())
	}
	if n := ecCountCodes(t, uid, "login"); n != 1 {
		t.Errorf("codes after in-window re-request = %d, want 1", n)
	}
	if code := ecVerify(h, email, real); code != http.StatusUnauthorized {
		t.Errorf("verify after in-window re-request: status %d, want 401", code)
	}

	// After the window a new code is minted and works.
	ecBackdate(t, uid, "login", auth.EmailCodeRequestCooldown+time.Second)
	if code := ecRequest(h, email); code != http.StatusNoContent {
		t.Fatalf("post-window request: status %d, want 204", code)
	}
	if sender.count() != 2 {
		t.Fatalf("emails after post-window request = %d, want 2", sender.count())
	}
	if code := ecVerify(h, email, sender.lastCode(t)); code != http.StatusOK {
		t.Errorf("verify with fresh code: status %d, want 200", code)
	}
}

func TestInteg_EmailCode_Handler_ConcurrentCorrectSubmissionsSingleSuccess(t *testing.T) {
	const email = "ec-single-use@example.com"
	uid := ecUser(t, email)
	q := db.New(integPool)
	ecMint(t, q, uid, "login", ecPlainCode)
	h := ecHandler(&ecSender{})

	// Fewer submitters than MaxEmailCodeAttempts, so a single success is due
	// to single-use consumption and not to attempt exhaustion.
	n := auth.MaxEmailCodeAttempts - 1
	var ok, unauth atomic.Int32
	ecRunConcurrently(n, func(int) {
		switch code := ecVerify(h, email, ecPlainCode); code {
		case http.StatusOK:
			ok.Add(1)
		case http.StatusUnauthorized:
			unauth.Add(1)
		default:
			t.Errorf("unexpected status %d", code)
		}
	})
	if ok.Load() != 1 || unauth.Load() != int32(n-1) {
		t.Errorf("200s=%d 401s=%d, want 1 and %d", ok.Load(), unauth.Load(), n-1)
	}
	if code := ecVerify(h, email, ecPlainCode); code != http.StatusUnauthorized {
		t.Errorf("replay after success: status %d, want 401", code)
	}
}
