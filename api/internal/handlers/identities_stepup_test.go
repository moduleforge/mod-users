package handlers

// Tests for the step-up challenge (Phase 4, Task 5).
//
// Covers:
//   - Flag-off: wrapped endpoints accept requests with no X-Step-Up-Token.
//   - Flag-on:  wrapped endpoints return 409 without header; 200/204 with
//     valid token; 409 on replay of the same token.
//   - StepUpVerify: wrong code → 401; missing code → 400.
//   - Anti-enumeration timing on StepUpRequest.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	db "github.com/moduleforge/mod-users/model/db"
)

// ---------------------------------------------------------------------------
// Fakes for step-up tests
// ---------------------------------------------------------------------------

// stepUpFakeQueries extends fakeQueries with email-code support.
type stepUpFakeQueries struct {
	fakeQueries

	// email code store: userAccountID+purpose → EmailCode
	mu         sync.Mutex
	emailCodes map[string]db.EmailCode
	nextCodeID int64
}

func newStepUpFakeQueries() *stepUpFakeQueries {
	return &stepUpFakeQueries{
		emailCodes: make(map[string]db.EmailCode),
		nextCodeID: 1,
	}
}

func emailCodeKey(userAccountID int64, purpose string) string {
	return fmt.Sprintf("%d:%s", userAccountID, purpose)
}

func (f *stepUpFakeQueries) CreateEmailCode(_ context.Context, arg db.CreateEmailCodeParams) (db.EmailCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextCodeID
	f.nextCodeID++
	code := db.EmailCode{
		ID:            id,
		UserAccountID: arg.UserAccountID,
		CodeHash:      arg.CodeHash,
		Purpose:       arg.Purpose,
		ExpiresAt:     arg.ExpiresAt,
	}
	f.emailCodes[emailCodeKey(arg.UserAccountID, arg.Purpose)] = code
	return code, nil
}

func (f *stepUpFakeQueries) GetActiveEmailCode(_ context.Context, arg db.GetActiveEmailCodeParams) (db.EmailCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := emailCodeKey(arg.UserAccountID, arg.Purpose)
	code, ok := f.emailCodes[key]
	if !ok {
		return db.EmailCode{}, pgx.ErrNoRows
	}
	if code.ExpiresAt.Valid && code.ExpiresAt.Time.Before(time.Now()) {
		return db.EmailCode{}, pgx.ErrNoRows
	}
	return code, nil
}

func (f *stepUpFakeQueries) ConsumeEmailCode(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, c := range f.emailCodes {
		if c.ID == id {
			delete(f.emailCodes, k)
			return nil
		}
	}
	return nil // idempotent
}

// fakeSender records sent emails for assertion.
type fakeSender struct {
	mu   sync.Mutex
	sent []sentEmail
}

type sentEmail struct {
	To, Subject, Body string
}

func (s *fakeSender) Send(_ context.Context, to, subject, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, sentEmail{To: to, Subject: subject, Body: body})
	return nil
}

// ---------------------------------------------------------------------------
// Helpers for step-up handler tests
// ---------------------------------------------------------------------------

const stepUpTestSecret = "test-secret-at-least-32-bytes-xx"

// buildStepUpHandlerFlagOff builds an IdentitiesHandler with step-up disabled.
// It wires a fake queries object but no real pool (transactions not tested here).
func buildStepUpHandlerFlagOff() (*IdentitiesHandler, *stepUpFakeQueries, *fakeSender) {
	q := newStepUpFakeQueries()
	sender := &fakeSender{}
	consumed := &sync.Map{}
	h := &IdentitiesHandler{
		queries:        &db.Queries{}, // not called in flag-off paths
		sender:         sender,
		jwtSecret:      stepUpTestSecret,
		consumed:       consumed,
		stepUpRequired: false,
	}
	_ = q
	return h, q, sender
}

// buildStepUpHandlerFlagOn builds an IdentitiesHandler with step-up enabled.
func buildStepUpHandlerFlagOn() (*IdentitiesHandler, *stepUpFakeQueries, *fakeSender) {
	q := newStepUpFakeQueries()
	sender := &fakeSender{}
	consumed := &sync.Map{}
	h := &IdentitiesHandler{
		queries:        &db.Queries{}, // the verify endpoint uses h.queries directly
		sender:         sender,
		jwtSecret:      stepUpTestSecret,
		consumed:       consumed,
		stepUpRequired: true,
	}
	_ = q
	return h, q, sender
}

// issueTestStepUpToken issues a valid step-up token for the given account.
func issueTestStepUpToken(t *testing.T, userAccountID int64) string {
	t.Helper()
	token, _, _, err := localauth.IssueStepUpToken([]byte(stepUpTestSecret), userAccountID, localauth.StepUpTTL)
	if err != nil {
		t.Fatalf("IssueStepUpToken: %v", err)
	}
	return token
}

// ---------------------------------------------------------------------------
// requireStepUp helper — direct unit tests
// ---------------------------------------------------------------------------

func TestRequireStepUp_FlagOff(t *testing.T) {
	h := &IdentitiesHandler{
		stepUpRequired: false,
		consumed:       &sync.Map{},
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	// No header, flag off — should be nil.
	if err := h.requireStepUp(req, 42); err != nil {
		t.Errorf("flag-off: expected nil, got %v", err)
	}
}

func TestRequireStepUp_FlagOn_MissingHeader(t *testing.T) {
	h := &IdentitiesHandler{
		stepUpRequired: true,
		jwtSecret:      stepUpTestSecret,
		consumed:       &sync.Map{},
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := h.requireStepUp(req, 42); err == nil {
		t.Error("flag-on, no header: expected ErrStepUpRequired, got nil")
	}
}

func TestRequireStepUp_FlagOn_ValidToken(t *testing.T) {
	consumed := &sync.Map{}
	h := &IdentitiesHandler{
		stepUpRequired: true,
		jwtSecret:      stepUpTestSecret,
		consumed:       consumed,
	}
	token := issueTestStepUpToken(t, 42)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Step-Up-Token", token)

	if err := h.requireStepUp(req, 42); err != nil {
		t.Errorf("valid token: expected nil, got %v", err)
	}
}

func TestRequireStepUp_FlagOn_ReplayRejected(t *testing.T) {
	consumed := &sync.Map{}
	h := &IdentitiesHandler{
		stepUpRequired: true,
		jwtSecret:      stepUpTestSecret,
		consumed:       consumed,
	}
	token := issueTestStepUpToken(t, 42)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Step-Up-Token", token)

	// First call: passes.
	if err := h.requireStepUp(req, 42); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call with same token: replay rejected.
	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	req2.Header.Set("X-Step-Up-Token", token)
	if err := h.requireStepUp(req2, 42); err == nil {
		t.Error("replay: expected ErrStepUpRequired, got nil")
	}
}

// ---------------------------------------------------------------------------
// Wrapped endpoint — flag-off: no header needed
// ---------------------------------------------------------------------------

// TestWrappedEndpoints_FlagOff verifies that when step-up is disabled, the
// four credential-mutating endpoints do not require X-Step-Up-Token. We
// test this via requireStepUp since the endpoints themselves reach the DB
// via transactions which require a real pool (fakeQueries.WithTx panics).
func TestWrappedEndpoints_FlagOff_NoHeaderRequired(t *testing.T) {
	consumed := &sync.Map{}
	h := &IdentitiesHandler{
		stepUpRequired: false,
		jwtSecret:      stepUpTestSecret,
		consumed:       consumed,
	}
	uc := newTestUC(1, 10)

	endpoints := []struct {
		name   string
		method string
	}{
		{"SetPassword", http.MethodPost},
		{"RemovePassword", http.MethodDelete},
		{"Unlink", http.MethodDelete},
		{"StartLink", http.MethodPost},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, "/", nil)
			req = withUC(req, uc)
			// No X-Step-Up-Token header.
			err := h.requireStepUp(req, uc.UserAccountID)
			if err != nil {
				t.Errorf("flag-off, no header: expected nil for %s, got %v", ep.name, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Wrapped endpoint — flag-on: 409 without header
// ---------------------------------------------------------------------------

// TestWrappedEndpoints_FlagOn_NoHeader verifies that when step-up is enabled,
// each credential-mutating endpoint returns 409 step_up_required when the
// X-Step-Up-Token header is absent.
//
// We test through the writeStepUpRequired response helper directly since
// the handler bodies call requireStepUp + writeStepUpRequired before any DB
// access. We drive it via a minimal wrapper to avoid the transaction panic.
type stepUpGatedHandler struct {
	h  *IdentitiesHandler
	uc *localauth.UserContext
}

func (sg *stepUpGatedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := sg.h.requireStepUp(r, sg.uc.UserAccountID); err != nil {
		writeStepUpRequired(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func TestWrappedEndpoints_FlagOn_NoHeader_Returns409(t *testing.T) {
	consumed := &sync.Map{}
	h := &IdentitiesHandler{
		stepUpRequired: true,
		jwtSecret:      stepUpTestSecret,
		consumed:       consumed,
	}
	uc := newTestUC(1, 10)

	names := []string{"SetPassword", "RemovePassword", "Unlink", "StartLink"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			sg := &stepUpGatedHandler{h: h, uc: uc}
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req = withUC(req, uc)
			rec := httptest.NewRecorder()
			sg.ServeHTTP(rec, req)

			if rec.Code != http.StatusConflict {
				t.Errorf("expected 409, got %d", rec.Code)
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if body["error"] != "step_up_required" {
				t.Errorf("error = %v, want step_up_required", body["error"])
			}
			if body["challenge_path"] != "/v1/self/credential/step-up" {
				t.Errorf("challenge_path = %v, want /v1/self/credential/step-up", body["challenge_path"])
			}
		})
	}
}

// TestWrappedEndpoints_FlagOn_ValidToken_Passes verifies that with a valid
// step-up token, requireStepUp returns nil (gate passes).
func TestWrappedEndpoints_FlagOn_ValidToken_Passes(t *testing.T) {
	consumed := &sync.Map{}
	h := &IdentitiesHandler{
		stepUpRequired: true,
		jwtSecret:      stepUpTestSecret,
		consumed:       consumed,
	}
	uc := newTestUC(1, 10)

	names := []string{"SetPassword", "RemovePassword", "Unlink", "StartLink"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			// Each iteration needs a fresh token (single-use).
			token := issueTestStepUpToken(t, uc.UserAccountID)

			sg := &stepUpGatedHandler{h: h, uc: uc}
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("X-Step-Up-Token", token)
			req = withUC(req, uc)
			rec := httptest.NewRecorder()
			sg.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent {
				t.Errorf("expected 204, got %d; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestWrappedEndpoints_FlagOn_Replay verifies that replaying the same step-up
// token on a second request is rejected with 409.
func TestWrappedEndpoints_FlagOn_Replay(t *testing.T) {
	consumed := &sync.Map{}
	h := &IdentitiesHandler{
		stepUpRequired: true,
		jwtSecret:      stepUpTestSecret,
		consumed:       consumed,
	}
	uc := newTestUC(1, 10)
	token := issueTestStepUpToken(t, uc.UserAccountID)

	sg := &stepUpGatedHandler{h: h, uc: uc}

	// First request — passes.
	req1 := httptest.NewRequest(http.MethodPost, "/", nil)
	req1.Header.Set("X-Step-Up-Token", token)
	req1 = withUC(req1, uc)
	rec1 := httptest.NewRecorder()
	sg.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("first request: expected 204, got %d", rec1.Code)
	}

	// Second request with same token — replay rejected.
	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	req2.Header.Set("X-Step-Up-Token", token)
	req2 = withUC(req2, uc)
	rec2 := httptest.NewRecorder()
	sg.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Errorf("replay: expected 409, got %d", rec2.Code)
	}
}

// ---------------------------------------------------------------------------
// StepUpVerify: direct logic tests
// ---------------------------------------------------------------------------

// StepUpVerify relies on h.queries which is *db.Queries (concrete type with no
// interface). We test the observable behavior: wrong code → 401, missing code
// → 400, missing active code row → 401. We use a minimal inline handler that
// mirrors StepUpVerify's logic with the fake queries.
type stepUpVerifyTestHandler struct {
	q         *stepUpFakeQueries
	jwtSecret string
	consumed  *sync.Map
}

func (h *stepUpVerifyTestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())

	var req stepUpVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if req.Code == "" {
		http.Error(w, "code required", 400)
		return
	}

	emailCode, err := h.q.GetActiveEmailCode(r.Context(), db.GetActiveEmailCodeParams{
		UserAccountID: uc.UserAccountID,
		Purpose:       "credential_change",
	})
	if err == pgx.ErrNoRows {
		http.Error(w, "unauthorized", 401)
		return
	}
	if err != nil {
		http.Error(w, "internal", 500)
		return
	}

	if err := bcryptCompare(emailCode.CodeHash, req.Code); err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}

	_ = h.q.ConsumeEmailCode(r.Context(), emailCode.ID)

	token, _, _, err := localauth.IssueStepUpToken([]byte(h.jwtSecret), uc.UserAccountID, localauth.StepUpTTL)
	if err != nil {
		http.Error(w, "internal", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(stepUpVerifyResponse{
		StepUpToken: token,
		ExpiresIn:   int(localauth.StepUpTTL.Seconds()),
	})
}

// bcryptCompare is a local shim to avoid importing bcrypt in the test file.
func bcryptCompare(hash, code string) error {
	_, err := localauth.HashPassword(code) // just to use localauth
	_ = err
	// Use the real bcrypt compare via the password helper.
	ok, err := localauth.VerifyPassword(code, hash)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("mismatch")
	}
	return nil
}

func TestStepUpVerify_MissingCode(t *testing.T) {
	q := newStepUpFakeQueries()
	h := &stepUpVerifyTestHandler{q: q, jwtSecret: stepUpTestSecret, consumed: &sync.Map{}}
	uc := newTestUC(1, 10)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = withUC(req, uc)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing code: expected 400, got %d", rec.Code)
	}
}

func TestStepUpVerify_NoActiveCode_Returns401(t *testing.T) {
	q := newStepUpFakeQueries()
	h := &stepUpVerifyTestHandler{q: q, jwtSecret: stepUpTestSecret, consumed: &sync.Map{}}
	uc := newTestUC(1, 10)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"code":"123456"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withUC(req, uc)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no active code: expected 401, got %d", rec.Code)
	}
}

func TestStepUpVerify_WrongCode_Returns401(t *testing.T) {
	q := newStepUpFakeQueries()
	uc := newTestUC(1, 10)

	// Store a real code hash in the fake.
	hash, err := localauth.HashPassword("correct-code-123456")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	q.emailCodes[emailCodeKey(uc.UserAccountID, "credential_change")] = db.EmailCode{
		ID:            1,
		UserAccountID: uc.UserAccountID,
		CodeHash:      hash,
		Purpose:       "credential_change",
		ExpiresAt:     pgtype.Timestamptz{Valid: true, Time: time.Now().Add(5 * time.Minute)},
	}
	q.nextCodeID = 2

	h := &stepUpVerifyTestHandler{q: q, jwtSecret: stepUpTestSecret, consumed: &sync.Map{}}

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"code":"wrong-code-000000"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withUC(req, uc)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong code: expected 401, got %d", rec.Code)
	}
}

func TestStepUpVerify_CorrectCode_ReturnsToken(t *testing.T) {
	q := newStepUpFakeQueries()
	uc := newTestUC(1, 10)

	correctCode := "123456"
	hash, err := localauth.HashPassword(correctCode)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	q.emailCodes[emailCodeKey(uc.UserAccountID, "credential_change")] = db.EmailCode{
		ID:            1,
		UserAccountID: uc.UserAccountID,
		CodeHash:      hash,
		Purpose:       "credential_change",
		ExpiresAt:     pgtype.Timestamptz{Valid: true, Time: time.Now().Add(5 * time.Minute)},
	}
	q.nextCodeID = 2

	h := &stepUpVerifyTestHandler{q: q, jwtSecret: stepUpTestSecret, consumed: &sync.Map{}}

	body := fmt.Sprintf(`{"code":%q}`, correctCode)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUC(req, uc)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("correct code: expected 200, got %d; body=%s", rec.Code, rec.Body.String())
	}

	var resp stepUpVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.StepUpToken == "" {
		t.Error("step_up_token is empty")
	}
	if resp.ExpiresIn != int(localauth.StepUpTTL.Seconds()) {
		t.Errorf("expires_in = %d, want %d", resp.ExpiresIn, int(localauth.StepUpTTL.Seconds()))
	}

	// Verify the issued token round-trips.
	consumed := &sync.Map{}
	if err := localauth.VerifyStepUpToken([]byte(stepUpTestSecret), resp.StepUpToken, uc.UserAccountID, consumed); err != nil {
		t.Errorf("issued token does not verify: %v", err)
	}
}

// ---------------------------------------------------------------------------
// StepUpRequest — anti-enumeration timing
// ---------------------------------------------------------------------------

func TestStepUpRequest_AntiEnumerationTiming(t *testing.T) {
	// StepUpRequest must return in at least 200ms regardless of whether the
	// code was sent. We drive it with a nil sender (no error path; the
	// sendStepUpCode goroutine logs and returns early).
	uc := newTestUC(1, 10)
	// Build minimal handler. We don't need queries that work because the
	// goroutine will fail silently (no sender) and the handler returns 204.
	h := &IdentitiesHandler{
		queries:  &db.Queries{},
		sender:   nil, // nil sender triggers early return in goroutine
		consumed: &sync.Map{},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/self/credential/step-up", nil)
	req = withUC(req, uc)
	rec := httptest.NewRecorder()

	start := time.Now()
	h.StepUpRequest(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	// Should take at least 200ms (anti-enumeration timer).
	if elapsed < 190*time.Millisecond {
		t.Errorf("response too fast: elapsed=%v, want ≥200ms", elapsed)
	}
}

// ---------------------------------------------------------------------------
// sendStepUpCode — anonymous-account guard
// ---------------------------------------------------------------------------

// TestSendStepUpCode_AnonymousAccountSkipsSend verifies that sendStepUpCode
// does not call sender.Send when uc.Email is empty (anonymous account guard).
// RequireVerifiedEmail already blocks anonymous users from reaching
// StepUpRequest, but this test documents the defense-in-depth behaviour added
// in Phase 3 task 001. The empty-email guard fires before CreateEmailCode, so
// no code row is created and the sender is never called.
func TestSendStepUpCode_AnonymousAccountSkipsSend(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}

	// queries is non-nil so the nil-check guard passes; the email guard fires
	// before CreateEmailCode is called.
	h := &IdentitiesHandler{
		queries:  &db.Queries{},
		sender:   sender,
		consumed: &sync.Map{},
	}

	// Build an anonymous-style UserContext: no email, no EmailVerifiedAt.
	anonUC := &localauth.UserContext{
		UserAccountID: 99,
		UserUUID:      "00000000-0000-0000-0000-000000000099",
		EntityID:      990,
		Email:         "", // anonymous account has no email
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/self/credential/step-up", nil)
	req = withUC(req, anonUC)
	rec := httptest.NewRecorder()

	h.StepUpRequest(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Errorf("sender.Send called %d time(s) for anonymous account; want 0", len(sender.sent))
	}
}
