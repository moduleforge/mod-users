package auth

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/moduleforge/core-api/opctx"
)

// Principal kinds an Identity can carry.
const (
	PrincipalKindUser           = "user"
	PrincipalKindServiceAccount = "service_account"
)

// Identity is the resolved caller, stored on the request context by the
// authentication middleware alongside opctx.AuthContext (which carries the
// same ids for module-agnostic code). Every field comes from a live session
// or credential row checked on this request, except PrincipalUUID,
// ProfileUUID and SessionUUID, which the token carried and the liveness check
// matched against those rows. The internal ids never leave the process.
type Identity struct {
	PrincipalID             int64
	PrincipalUUID           uuid.UUID
	PrincipalKind           string // PrincipalKindUser | PrincipalKindServiceAccount
	ProfileID               int64
	ProfileUUID             uuid.UUID
	PartyID                 int64 // 0 for a personal profile
	SessionID               int64 // 0 for client-credentials calls
	SessionUUID             uuid.UUID
	CredentialID            int64 // 0 for session calls
	AppID                   int64 // 0 when the session has no app
	ACR                     int16
	AMR                     []string
	AuthTime                time.Time
	ImpersonatorPrincipalID int64 // 0 unless impersonating
	IsGuest                 bool
	EmailVerified           bool // user holds at least one verified email identifier
}

// AuthContext returns the opctx.AuthContext equivalent of id. Anonymous is
// always false: an Identity is an authenticated caller.
func (id *Identity) AuthContext() opctx.AuthContext {
	// WHY no system-actor marker: an Identity is an authenticated caller with
	// a session or a credential, never trusted code.
	// IMPLIES: every request context the middleware installs is gated by the
	// engine on its own session or credential.
	return opctx.AuthContext{
		PrincipalID:             id.PrincipalID,
		ProfileID:               id.ProfileID,
		ImpersonatorPrincipalID: id.ImpersonatorPrincipalID,
		SessionID:               id.SessionID,
		CredentialID:            id.CredentialID,
		ACR:                     id.ACR,
		AuthTime:                id.AuthTime,
	}
}

// contextKey is the unexported key type for every value this package stores
// on a context.Context.
type contextKey int

const (
	// identityKey addresses the *Identity stored by WithIdentity.
	identityKey contextKey = iota
)

// WithIdentity returns a context carrying id and its opctx.AuthContext. It is
// the single place both values are installed, so they cannot disagree.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	ctx = context.WithValue(ctx, identityKey, id)
	return opctx.WithAuthContext(ctx, id.AuthContext())
}

// IdentityFromContext returns the Identity the authentication middleware
// stored on ctx. It returns nil, false for an unauthenticated context and for
// the anonymous system actor's context (which carries only an
// opctx.AuthContext with Anonymous set).
func IdentityFromContext(ctx context.Context) (*Identity, bool) {
	id, ok := ctx.Value(identityKey).(*Identity)
	return id, ok && id != nil
}
