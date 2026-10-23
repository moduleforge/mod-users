package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/config"
)

// Compile-time pins of the binding facade signatures (API surface section
// 6.1). A change here is a change to a binding name and must be flagged.
var (
	_ func(context.Context) (*Identity, bool)                                                 = IdentityFromContext
	_ func(*pgxpool.Pool, *Verifier, config.AuthConfig) *Authenticator                        = NewAuthenticator
	_ func(*Authenticator) func(http.Handler) http.Handler                                    = NewRequireAuth
	_ func(*Authenticator, *AnonymousActor) func(http.Handler) http.Handler                   = NewResolveOrAnonymous
	_ func() func(http.Handler) http.Handler                                                  = NewRequireVerifiedEmail
	_ func(func() config.BootState) func(http.Handler) http.Handler                           = RequireOIDCConfirmed
	_ func(context.Context, *pgxpool.Pool) (*AnonymousActor, error)                           = NewAnonymousActor
	_ func(context.Context) bool                                                              = IsAnonymousActor
	_ func(context.Context, config.LocalAuthConfig) (*Verifier, error)                        = NewLocalVerifier
	_ func(context.Context, ...VerifierOption) (*Verifier, error)                             = NewVerifierWithOptions
	_ func(config.LocalAuthConfig) (*Signer, error)                                           = NewSigner
	_ func(*Signer, string, uuid.UUID, string, time.Duration, map[string]any) (string, error) = IssueAudienceToken
	_ func(string) (string, error)                                                            = HashPassword
	_ interface {
		PrincipalID() int64
		ProfileID() int64
	} = (*AnonymousActor)(nil)
	_ interface {
		Authenticate(*http.Request) (*Identity, error)
	} = (*Authenticator)(nil)
)

func TestIsAnonymousActor_DelegatesToAuthContext(t *testing.T) {
	if IsAnonymousActor(context.Background()) {
		t.Error("no AuthContext: want false")
	}
	ctx := opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: 1, ProfileID: 2})
	if IsAnonymousActor(ctx) {
		t.Error("non-anonymous AuthContext: want false")
	}
	ctx = opctx.WithAuthContext(context.Background(), opctx.AuthContext{PrincipalID: 1, ProfileID: 2, Anonymous: true})
	if !IsAnonymousActor(ctx) {
		t.Error("anonymous AuthContext: want true")
	}
}
