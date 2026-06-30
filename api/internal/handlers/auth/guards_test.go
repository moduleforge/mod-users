package auth

// Tests for anonymous-account guard behaviour in the Login, EmailCodeRequest,
// and PasswordResetRequest handlers.
//
// Now that Handler.queries is the narrow authQuerier interface, these tests
// inject a stub querier to control what GetUserAccountByEmail returns, letting
// us drive the nil-email guard without a real database.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/moduleforge/mod-users/model/db"
)

// ---------------------------------------------------------------------------
// stubAuthQuerier — minimal authQuerier for handler guard tests
// ---------------------------------------------------------------------------

// stubAuthQuerier implements authQuerier for unit tests. Only the methods
// exercised by the handlers under test need real implementations; all others
// return zero values or ErrNoRows so they are safe but unreachable.
type stubAuthQuerier struct {
	// GetUserAccountByEmail outcome.
	accountByEmail    *db.UserAccount
	accountByEmailErr error

	// GetAuthLocal outcome.
	authLocal    *db.AuthLocal
	authLocalErr error

	// Track whether sender-path methods were called.
	createEmailCodeCalled     bool
	createPasswordResetCalled bool
}

func (s *stubAuthQuerier) GetUserAccountByEmail(_ context.Context, _ string) (db.UserAccount, error) {
	if s.accountByEmailErr != nil {
		return db.UserAccount{}, s.accountByEmailErr
	}
	if s.accountByEmail == nil {
		return db.UserAccount{}, pgx.ErrNoRows
	}
	return *s.accountByEmail, nil
}

func (s *stubAuthQuerier) GetAuthLocal(_ context.Context, _ int64) (db.AuthLocal, error) {
	if s.authLocalErr != nil {
		return db.AuthLocal{}, s.authLocalErr
	}
	if s.authLocal == nil {
		return db.AuthLocal{}, pgx.ErrNoRows
	}
	return *s.authLocal, nil
}

func (s *stubAuthQuerier) CreateEmailCode(_ context.Context, _ db.CreateEmailCodeParams) (db.EmailCode, error) {
	s.createEmailCodeCalled = true
	return db.EmailCode{}, nil
}

func (s *stubAuthQuerier) GetActiveEmailCode(_ context.Context, _ db.GetActiveEmailCodeParams) (db.EmailCode, error) {
	return db.EmailCode{}, pgx.ErrNoRows
}

func (s *stubAuthQuerier) ConsumeEmailCode(_ context.Context, _ int64) error {
	return nil
}

func (s *stubAuthQuerier) UpdateUserAccount(_ context.Context, _ db.UpdateUserAccountParams) error {
	return nil
}

func (s *stubAuthQuerier) CreatePasswordReset(_ context.Context, _ db.CreatePasswordResetParams) (db.PasswordReset, error) {
	s.createPasswordResetCalled = true
	return db.PasswordReset{}, nil
}

func (s *stubAuthQuerier) GetActivePasswordReset(_ context.Context, _ string) (db.PasswordReset, error) {
	return db.PasswordReset{}, pgx.ErrNoRows
}

func (s *stubAuthQuerier) UpsertAuthLocal(_ context.Context, _ db.UpsertAuthLocalParams) error {
	return nil
}

func (s *stubAuthQuerier) ConsumePasswordReset(_ context.Context, _ int64) error {
	return nil
}

// ---------------------------------------------------------------------------
// recordingSender — captures Send calls for assertion
// ---------------------------------------------------------------------------

type recordingSender struct {
	mu    sync.Mutex
	calls []string // recipient addresses
}

func (r *recordingSender) Send(_ context.Context, to, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, to)
	return nil
}

func (r *recordingSender) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newGuardTestHandler builds a Handler wired with stub queries and a
// recording sender. pool, coreQ are nil — the handlers under test do not
// use them.
func newGuardTestHandler(q authQuerier, sender Sender) *Handler {
	return &Handler{
		queries:   q,
		jwtSecret: "test-secret-at-least-32-bytes-xx",
		issuer:    "test-issuer",
		sender:    sender,
		guiBase:   "http://gui.test",
	}
}

// anonAccount returns a db.UserAccount with Email.Valid == false (anonymous).
func anonAccount() db.UserAccount {
	return db.UserAccount{
		ID:    42,
		Email: pgtype.Text{Valid: false},
	}
}

// namedAccount returns a db.UserAccount with a real email address.
func namedAccount(email string) db.UserAccount {
	return db.UserAccount{
		ID:    42,
		Email: pgtype.Text{String: email, Valid: true},
	}
}

// ---------------------------------------------------------------------------
// TestLogin_AnonymousAccount
// ---------------------------------------------------------------------------

// TestLogin_AnonymousAccount confirms that POST /v1/auth/login with an empty
// email is rejected with 400 before any DB query is issued. This validates
// that the existing guard (email == "" check) is in place.
func TestLogin_AnonymousAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "empty email returns 400",
			body:       `{"email":"","password":"somepassword"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "bad_request",
		},
		{
			name:       "missing email returns 400",
			body:       `{"password":"somepassword"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "bad_request",
		},
		{
			name:       "empty password returns 400",
			body:       `{"email":"user@example.com","password":""}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "bad_request",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// queries is nil — the guard fires before any DB access.
			h := &Handler{
				jwtSecret: "test-secret-at-least-32-bytes-xx",
				issuer:    "test-issuer",
			}

			req := httptest.NewRequest(http.MethodPost, "/v1/auth/login",
				bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.Login(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			errObj, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("error field missing: %v", body)
			}
			if errObj["code"] != tc.wantCode {
				t.Errorf("error.code = %v, want %s", errObj["code"], tc.wantCode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestEmailCodeRequest_NilEmail
// ---------------------------------------------------------------------------

// TestEmailCodeRequest_NilEmail confirms that sendEmailCode does not call
// sender.Send when the db.UserAccount has Email.Valid == false (anonymous
// account guard added in Phase 2 task 001).
//
// The request flow:
//  1. EmailCodeRequest fires sendEmailCode in a goroutine.
//  2. sendEmailCode calls GetUserAccountByEmail → returns anonymous account.
//  3. The guard `!ua.Email.Valid` fires before sender.Send — no email sent.
//  4. EmailCodeRequest always returns 204 (anti-enumeration).
func TestEmailCodeRequest_NilEmail(t *testing.T) {
	t.Parallel()

	anon := anonAccount()
	q := &stubAuthQuerier{accountByEmail: &anon}
	sender := &recordingSender{}
	h := newGuardTestHandler(q, sender)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/email-code/request",
		bytes.NewBufferString(`{"email":"device-001@anon.invalid","purpose":"login"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.EmailCodeRequest(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if n := sender.callCount(); n != 0 {
		t.Errorf("sender.Send called %d time(s) for anonymous account; want 0", n)
	}
}

// TestEmailCodeRequest_NamedAccount confirms that sendEmailCode DOES call
// sender.Send for a named (non-anonymous) account. CreateEmailCode is stubbed
// to succeed so the flow reaches the sender.
func TestEmailCodeRequest_NamedAccount(t *testing.T) {
	t.Parallel()

	named := namedAccount("user@example.com")
	q := &stubAuthQuerier{accountByEmail: &named}
	sender := &recordingSender{}
	h := newGuardTestHandler(q, sender)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/email-code/request",
		bytes.NewBufferString(`{"email":"user@example.com","purpose":"login"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.EmailCodeRequest(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if n := sender.callCount(); n != 1 {
		t.Errorf("sender.Send called %d time(s); want 1", n)
	}
}

// ---------------------------------------------------------------------------
// TestPasswordResetRequest_NilEmail
// ---------------------------------------------------------------------------

// TestPasswordResetRequest_NilEmail confirms that the PasswordResetRequest
// goroutine does not call sender.Send when the resolved account has
// Email.Valid == false (anonymous account guard added in Phase 2 task 001).
//
// Note: the guard in PasswordResetRequest fires AFTER CreatePasswordReset —
// the reset row is inserted (wasted work, but safe), and only the Send call
// is skipped. The assertion here is that sender.Send is not invoked.
//
// The handler always returns 204 immediately (anti-enumeration); we wait for
// the goroutine by sleeping briefly after the response.
func TestPasswordResetRequest_NilEmail(t *testing.T) {
	t.Parallel()

	anon := anonAccount()
	q := &stubAuthQuerier{accountByEmail: &anon}
	sender := &recordingSender{}
	h := newGuardTestHandler(q, sender)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/password-reset/request",
		bytes.NewBufferString(`{"email":"device-001@anon.invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.PasswordResetRequest(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}

	// The goroutine is fire-and-forget; give it time to complete before
	// asserting. A short sleep is sufficient here: the goroutine does only
	// in-memory work (stub query) with no I/O.
	time.Sleep(50 * time.Millisecond)

	// The guard `!ua.Email.Valid` fires before sender.Send — no email sent.
	if n := sender.callCount(); n != 0 {
		t.Errorf("sender.Send called %d time(s) for anonymous account; want 0", n)
	}
}

// TestPasswordResetRequest_NamedAccount confirms that the goroutine DOES call
// sender.Send for a named account. CreatePasswordReset is stubbed to succeed.
func TestPasswordResetRequest_NamedAccount(t *testing.T) {
	t.Parallel()

	named := namedAccount("user@example.com")
	q := &stubAuthQuerier{accountByEmail: &named}
	sender := &recordingSender{}
	h := newGuardTestHandler(q, sender)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/password-reset/request",
		bytes.NewBufferString(`{"email":"user@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.PasswordResetRequest(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}

	// Wait for the goroutine.
	time.Sleep(50 * time.Millisecond)

	if n := sender.callCount(); n != 1 {
		t.Errorf("sender.Send called %d time(s); want 1", n)
	}
}
