package handlers_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	pubauth "github.com/moduleforge/mod-users/api/auth"
	pubconfig "github.com/moduleforge/mod-users/api/config"
	"github.com/moduleforge/mod-users/api/handlers"
	inner "github.com/moduleforge/mod-users/api/internal/handlers"
	db "github.com/moduleforge/mod-users/model/db"
)

// fakeQuerier is a minimal in-memory inner.OIDCConfigQuerier. Only the
// methods EnsureSetupToken / RefreshState exercise are meaningfully
// implemented; ListOIDCProviders returns empty since these tests use an
// empty ProviderRegistry.
type fakeQuerier struct {
	mu  sync.Mutex
	row db.OidcConfig
}

func newFakeQuerier(initial db.OidcConfig) *fakeQuerier {
	return &fakeQuerier{row: initial}
}

func (f *fakeQuerier) GetOIDCConfig(ctx context.Context) (db.OidcConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.row, nil
}

func (f *fakeQuerier) UpdateOIDCConfig(ctx context.Context, optOut bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.OptOut = optOut
	return nil
}

func (f *fakeQuerier) SetSetupTokenHash(ctx context.Context, hash pgtype.Text) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.SetupTokenHash = hash
	return nil
}

func (f *fakeQuerier) ClearSetupTokenHash(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row.SetupTokenHash = pgtype.Text{}
	f.row.SetupTokenCreatedAt = nil
	return nil
}

func (f *fakeQuerier) SetOIDCProviderEnabled(ctx context.Context, arg db.SetOIDCProviderEnabledParams) error {
	return nil
}

func (f *fakeQuerier) ListOIDCProviders(ctx context.Context) ([]db.OidcProvider, error) {
	return nil, nil
}

// newTestHandler builds an *handlers.OIDCConfigHandler (via the internal
// constructor, since the public facade's NewOIDCConfigHandler takes a
// concrete *usersdb.Queries rather than the OIDCConfigQuerier interface)
// wired to a fake querier and an empty-registry OAuth orchestrator — no
// network I/O happens for an empty registry.
func newTestHandler(t *testing.T, initial db.OidcConfig, tokenDisplay pubconfig.TokenDisplay) (*handlers.OIDCConfigHandler, *fakeQuerier) {
	t.Helper()
	fq := newFakeQuerier(initial)
	oauth, err := pubauth.NewOAuth(context.Background(), &pubconfig.Config{
		LocalAuth: pubconfig.LocalAuthConfig{JWTSecret: "test-secret-at-least-32-bytes-xx"},
	})
	if err != nil {
		t.Fatalf("NewOAuth: %v", err)
	}
	h := inner.NewOIDCConfigHandler(inner.OIDCConfigDeps{
		Queries:      fq,
		OAuth:        oauth,
		EnvRegistry:  pubconfig.ProviderRegistry{},
		EnvNoOIDCEnv: false,
		TokenDisplay: tokenDisplay,
	})
	if err := h.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}
	return h, fq
}

func TestEnsureSetupTokenAndPrint_TokenDisplayNone_Unconfirmed(t *testing.T) {
	h, _ := newTestHandler(t, db.OidcConfig{ID: 1}, pubconfig.TokenDisplayNone)

	err := handlers.EnsureSetupTokenAndPrint(context.Background(), h, pubconfig.TokenDisplayNone, "http://localhost:3000")
	if err == nil {
		t.Fatal("expected an error when TokenDisplay is none and onboarding is unconfirmed, got nil")
	}
}

func TestEnsureSetupTokenAndPrint_TokenDisplayNone_Confirmed(t *testing.T) {
	// opt_out=true with an empty provider registry yields a confirmed
	// boot state (no providers configured, opted out).
	h, _ := newTestHandler(t, db.OidcConfig{ID: 1, OptOut: true}, pubconfig.TokenDisplayNone)
	if !h.CurrentState().Confirmed() {
		t.Fatalf("test setup: expected confirmed state, got %s", h.CurrentState())
	}

	if err := handlers.EnsureSetupTokenAndPrint(context.Background(), h, pubconfig.TokenDisplayNone, "http://localhost:3000"); err != nil {
		t.Fatalf("EnsureSetupTokenAndPrint: got error %v, want nil", err)
	}
}

func TestEnsureSetupTokenAndPrint_MintsTokenWhenUnconfirmed(t *testing.T) {
	for _, td := range []pubconfig.TokenDisplay{pubconfig.TokenDisplayStderr, pubconfig.TokenDisplayBoth, pubconfig.TokenDisplayLocalhost} {
		t.Run(string(td), func(t *testing.T) {
			h, fq := newTestHandler(t, db.OidcConfig{ID: 1}, td)

			if err := handlers.EnsureSetupTokenAndPrint(context.Background(), h, td, "http://localhost:3000"); err != nil {
				t.Fatalf("EnsureSetupTokenAndPrint: got error %v, want nil", err)
			}

			fq.mu.Lock()
			hashSet := fq.row.SetupTokenHash.Valid
			fq.mu.Unlock()
			if !hashSet {
				t.Error("expected a setup token hash to be persisted, got none")
			}
		})
	}
}

func TestEnsureSetupTokenAndPrint_NoOpOnRepeatCall(t *testing.T) {
	h, _ := newTestHandler(t, db.OidcConfig{ID: 1}, pubconfig.TokenDisplayBoth)

	if err := handlers.EnsureSetupTokenAndPrint(context.Background(), h, pubconfig.TokenDisplayBoth, "http://localhost:3000"); err != nil {
		t.Fatalf("first call: got error %v, want nil", err)
	}
	// Second call in the same process should be a no-op (EnsureSetupToken
	// returns "" when the in-memory plaintext is already held) and must
	// not error.
	if err := handlers.EnsureSetupTokenAndPrint(context.Background(), h, pubconfig.TokenDisplayBoth, "http://localhost:3000"); err != nil {
		t.Fatalf("second call: got error %v, want nil", err)
	}
}
