package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/config"
	db "github.com/moduleforge/mod-users/model/db"
)

// fakePlatform is an in-memory PlatformSettingsReader.
type fakePlatform struct {
	mu       sync.Mutex
	operator pgtype.Int8
}

func (f *fakePlatform) GetPlatformSettings(context.Context) (coredb.PlatformSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return coredb.PlatformSetting{ID: 1, OperatorPartyID: f.operator}, nil
}

func (f *fakePlatform) bootstrap() {
	f.mu.Lock()
	f.operator = pgtype.Int8{Int64: 42, Valid: true}
	f.mu.Unlock()
}

func newLifecycleHandler(t *testing.T, initial db.GetOIDCConfigRow) (*OIDCConfigHandler, *fakeQuerier, *fakePlatform) {
	t.Helper()
	fq := newFakeQuerier(initial)
	fp := &fakePlatform{}
	h := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries:      fq,
		OAuth:        newEmptyOAuth(t),
		EnvRegistry:  config.ProviderRegistry{},
		TokenDisplay: config.TokenDisplayBoth,
		Platform:     fp,
	})
	if err := h.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}
	return h, fq, fp
}

func confirmWithHeader(h *OIDCConfigHandler, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/confirm", strings.NewReader(`{"opt_out":true}`))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Setup-Token", token)
	}
	rr := httptest.NewRecorder()
	h.Confirm(rr, req)
	return rr
}

func loopbackSetupToken(h *OIDCConfigHandler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/oidc-config/setup-token", nil)
	req.RemoteAddr = "127.0.0.1:4444"
	rr := httptest.NewRecorder()
	h.SetupToken(rr, req)
	return rr
}

// TestSetupTokenLifecycle_ConfirmFirst: confirming OIDC with the token while
// the bootstrap is pending keeps the token (and the loopback endpoint); the
// bootstrap then clears it.
func TestSetupTokenLifecycle_ConfirmFirst(t *testing.T) {
	h, fq, fp := newLifecycleHandler(t, db.GetOIDCConfigRow{ID: 1})
	token, err := h.EnsureSetupToken(context.Background())
	if err != nil || token == "" {
		t.Fatalf("EnsureSetupToken: %q, %v", token, err)
	}
	if rr := confirmWithHeader(h, token); rr.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rr.Code, rr.Body.String())
	}
	if !h.CurrentState().Confirmed() {
		t.Fatalf("not confirmed")
	}
	if !fq.row.SetupTokenHash.Valid || !h.SetupTokenActive() {
		t.Fatalf("token cleared while the bootstrap is pending")
	}
	if rr := loopbackSetupToken(h); rr.Code != http.StatusOK {
		t.Errorf("setup-token endpoint while the bootstrap is pending: %d, want 200", rr.Code)
	}
	// The bootstrap route's fresh check still accepts the stored token.
	if ok, err := h.VerifySetupTokenFresh(context.Background(), token); err != nil || !ok {
		t.Errorf("VerifySetupTokenFresh = %v, %v; want true", ok, err)
	}

	fp.bootstrap()
	if err := h.AfterBootstrap(context.Background()); err != nil {
		t.Fatalf("AfterBootstrap: %v", err)
	}
	if fq.row.SetupTokenHash.Valid || h.SetupTokenActive() {
		t.Errorf("token not cleared after both steps")
	}
	if rr := loopbackSetupToken(h); rr.Code != http.StatusNotFound {
		t.Errorf("setup-token endpoint after both steps: %d, want 404", rr.Code)
	}
	if rr := confirmWithHeader(h, token); rr.Code != http.StatusUnauthorized {
		t.Errorf("confirm with the cleared token: %d, want 401", rr.Code)
	}
}

// TestSetupTokenLifecycle_BootstrapFirst: a bootstrap while OIDC is
// unconfirmed keeps the token for the confirm, which then clears it.
func TestSetupTokenLifecycle_BootstrapFirst(t *testing.T) {
	h, fq, fp := newLifecycleHandler(t, db.GetOIDCConfigRow{ID: 1})
	token, err := h.EnsureSetupToken(context.Background())
	if err != nil || token == "" {
		t.Fatalf("EnsureSetupToken: %q, %v", token, err)
	}
	fp.bootstrap()
	if err := h.AfterBootstrap(context.Background()); err != nil {
		t.Fatalf("AfterBootstrap: %v", err)
	}
	if !fq.row.SetupTokenHash.Valid {
		t.Fatalf("bootstrap cleared the token while OIDC is unconfirmed")
	}
	if rr := confirmWithHeader(h, token); rr.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rr.Code, rr.Body.String())
	}
	if fq.row.SetupTokenHash.Valid {
		t.Errorf("token not cleared after both steps")
	}
}

// TestSetupTokenLifecycle_MintsWhileBootstrapPending: an opt-out (confirmed)
// boot state still mints the token while the operator party is unset, and
// does not once it is set.
func TestSetupTokenLifecycle_MintsWhileBootstrapPending(t *testing.T) {
	h, fq, fp := newLifecycleHandler(t, db.GetOIDCConfigRow{ID: 1, OptOut: true})
	if !h.CurrentState().Confirmed() {
		t.Fatalf("opt-out state not confirmed")
	}
	plain, err := h.EnsureSetupToken(context.Background())
	if err != nil || plain == "" {
		t.Fatalf("EnsureSetupToken with bootstrap pending = %q, %v; want a token", plain, err)
	}
	fp.bootstrap()
	plain, err = h.EnsureSetupToken(context.Background())
	if err != nil || plain != "" {
		t.Errorf("EnsureSetupToken after the bootstrap = %q, %v; want none", plain, err)
	}
	if fq.row.SetupTokenHash.Valid {
		t.Errorf("hash not cleared once the token is inactive")
	}
}

// TestSetupTokenLifecycle_ExternallyManaged: the externally-managed state is
// confirmed without a database read, yet a pending bootstrap mints.
func TestSetupTokenLifecycle_ExternallyManaged(t *testing.T) {
	fq := newFakeQuerier(db.GetOIDCConfigRow{ID: 1})
	h := NewOIDCConfigHandler(OIDCConfigDeps{
		Queries: fq, OAuth: newEmptyOAuth(t), EnvRegistry: config.ProviderRegistry{},
		TokenDisplay: config.TokenDisplayBoth, Platform: &fakePlatform{},
	})
	h.MarkExternallyManaged()
	plain, err := h.EnsureSetupToken(context.Background())
	if err != nil || plain == "" {
		t.Fatalf("EnsureSetupToken = %q, %v; want a token", plain, err)
	}
}

// TestBootstrapHandler_TokenBeforeBody: the header token is evaluated before
// the body (401 users.setup_token_invalid even for a malformed body), and the
// token is checked against the stored hash.
func TestBootstrapHandler_TokenBeforeBody(t *testing.T) {
	h, _, _ := newLifecycleHandler(t, db.GetOIDCConfigRow{ID: 1})
	if _, err := h.EnsureSetupToken(context.Background()); err != nil {
		t.Fatalf("EnsureSetupToken: %v", err)
	}
	bh := NewBootstrapHandler(nil, h) // the service is never reached
	for _, tok := range []string{"", "wrong"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/bootstrap", strings.NewReader("{not json"))
		if tok != "" {
			req.Header.Set("X-Setup-Token", tok)
		}
		rr := httptest.NewRecorder()
		bh.Bootstrap(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: status %d, want 401", tok, rr.Code)
		}
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil || env.Error.Code != "users.setup_token_invalid" {
			t.Errorf("token %q: body %s, want users.setup_token_invalid", tok, rr.Body.String())
		}
	}
}

// TestBootstrapHandler_BodyValidation: with a valid token, a malformed or
// invalid body is 400 before the service runs.
func TestBootstrapHandler_BodyValidation(t *testing.T) {
	h, _, _ := newLifecycleHandler(t, db.GetOIDCConfigRow{ID: 1})
	token, err := h.EnsureSetupToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureSetupToken: %v", err)
	}
	bh := NewBootstrapHandler(nil, h)
	for _, body := range []string{
		"{not json",
		`{"operator":{"kind":"organization","legal_name":"","legal_form":"llc"},"admin":{"email":"a@b.test","password":"long-enough-password","given_name":"A","family_name":"B"}}`,
		`{"operator":{"kind":"natural_person"},"admin":{"email":"not-an-email","password":"short","given_name":"","family_name":""}}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/bootstrap", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Setup-Token", token)
		rr := httptest.NewRecorder()
		bh.Bootstrap(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %s: status %d, want 400 (%s)", body, rr.Code, rr.Body.String())
		}
	}
}

// TestSetupToken_RefusedOnOIDCRoutesOnceConfirmed: with OIDC confirmed and the
// operator party still unset, the token is refused on the OIDC configuration
// routes (header and body) but still accepted by POST /v1/bootstrap.
func TestSetupToken_RefusedOnOIDCRoutesOnceConfirmed(t *testing.T) {
	h, fq, _ := newLifecycleHandler(t, db.GetOIDCConfigRow{ID: 1})
	token, err := h.EnsureSetupToken(context.Background())
	if err != nil || token == "" {
		t.Fatalf("EnsureSetupToken: %q, %v", token, err)
	}
	if rr := confirmWithHeader(h, token); rr.Code != http.StatusOK {
		t.Fatalf("first confirm with the token while unconfirmed: %d %s", rr.Code, rr.Body.String())
	}
	if !fq.row.SetupTokenHash.Valid || !h.CurrentState().Confirmed() {
		t.Fatalf("precondition: want OIDC confirmed with the token still live")
	}

	if rr := confirmWithHeader(h, token); rr.Code != http.StatusUnauthorized {
		t.Errorf("confirm header token once confirmed: %d, want 401", rr.Code)
	}
	body := `{"opt_out":true,"setup_token":"` + token + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/oidc-config/confirm", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.Confirm(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("confirm body token once confirmed: %d, want 401", rr.Code)
	}
	if ok, err := h.authorizeAmbient(httptest.NewRequest(http.MethodGet, "/v1/oidc-config/providers", nil)); err != nil || ok {
		t.Errorf("tokenless ambient auth = %v, %v; want false", ok, err)
	}
	get := httptest.NewRequest(http.MethodGet, "/v1/oidc-config/providers", nil)
	get.Header.Set("X-Setup-Token", token)
	if rr := httptest.NewRecorder(); h.authorizeBodyless(rr, get, "test") || rr.Code != http.StatusUnauthorized {
		t.Errorf("providers route with the token once confirmed: %d, want 401", rr.Code)
	}

	bh := NewBootstrapHandler(nil, h)
	breq := httptest.NewRequest(http.MethodPost, "/v1/bootstrap", strings.NewReader("{not json"))
	breq.Header.Set("X-Setup-Token", token)
	brr := httptest.NewRecorder()
	bh.Bootstrap(brr, breq)
	if brr.Code != http.StatusBadRequest {
		t.Errorf("bootstrap with the token once confirmed: %d, want 400 (token accepted, body rejected)", brr.Code)
	}
}

// TestSetupToken_StaleCacheRejectsClearedHash: the hash is cleared in the
// database after the handler cached it; the old token is refused.
func TestSetupToken_StaleCacheRejectsClearedHash(t *testing.T) {
	h, fq, _ := newLifecycleHandler(t, db.GetOIDCConfigRow{ID: 1})
	token, err := h.EnsureSetupToken(context.Background())
	if err != nil || token == "" {
		t.Fatalf("EnsureSetupToken: %q, %v", token, err)
	}
	if ok, err := h.verifySetupToken(context.Background(), token); err != nil || !ok {
		t.Fatalf("verifySetupToken with the live token = %v, %v; want true", ok, err)
	}
	// Another replica clears the hash behind this handler's cache.
	if err := fq.ClearSetupTokenHash(context.Background()); err != nil {
		t.Fatalf("ClearSetupTokenHash: %v", err)
	}
	if !h.cachedDB.SetupTokenHash.Valid {
		t.Fatalf("precondition: cache should still hold the hash")
	}
	if ok, err := h.verifySetupToken(context.Background(), token); err != nil || ok {
		t.Errorf("verifySetupToken after the hash was cleared = %v, %v; want false", ok, err)
	}
}
