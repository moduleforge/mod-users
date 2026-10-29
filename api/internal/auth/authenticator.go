package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/mod-users/api/internal/config"
	"github.com/moduleforge/mod-users/api/internal/useraction"
)

// Sentinel errors from Authenticator.Authenticate. Callers use errors.Is.
var (
	// ErrNoAuthHeader is returned when the request has no Authorization
	// header or the header isn't in "Bearer <token>" form.
	ErrNoAuthHeader = errors.New("auth: no bearer token")
	// ErrInvalidToken is returned when the token fails verification
	// (signature, algorithm, issuer, expiry, audience, or claim shape).
	ErrInvalidToken = errors.New("auth: invalid or expired token")
	// ErrNotLive is returned (wrapped in a *NotLiveError) when the token is
	// valid but its session or credential, or the profile it acts as, is no
	// longer usable.
	ErrNotLive = errors.New("auth: session or profile not live")
)

// Error codes the authentication middleware answers a not-live token with
// (HTTP 401).
const (
	CodeSessionRevoked     = "users.session_revoked"
	CodeProfileUnavailable = "users.profile_unavailable"
)

// livenessStatusOK is the liveness functions' success status.
const livenessStatusOK = "ok"

// revokedStatuses are the liveness statuses that mean the session or
// credential itself is gone (or the token does not belong to it), as opposed
// to a profile-availability lock reason.
var revokedStatuses = map[string]struct{}{
	"session_revoked":      {},
	"session_expired":      {},
	"parent_session_ended": {},
	"principal_mismatch":   {},
	"credential_revoked":   {},
	"credential_expired":   {},
}

// NotLiveError reports a liveness failure. errors.Is(err, ErrNotLive) holds.
type NotLiveError struct {
	// Status is the liveness function's status: a revocation status
	// (session_revoked, session_expired, parent_session_ended,
	// principal_mismatch, credential_revoked, credential_expired) or a
	// profile-availability lock reason (not_held, inactive,
	// principal_disabled, confined, credential_not_authorized,
	// provider_required, acr_too_low).
	Status string
}

func (e *NotLiveError) Error() string { return "auth: not live: " + e.Status }

// Is makes errors.Is(err, ErrNotLive) true.
func (e *NotLiveError) Is(target error) bool { return target == ErrNotLive }

// Code returns the 401 error code: CodeSessionRevoked for a revocation
// status, CodeProfileUnavailable for a lock reason.
func (e *NotLiveError) Code() string {
	if _, revoked := revokedStatuses[e.Status]; revoked {
		return CodeSessionRevoked
	}
	return CodeProfileUnavailable
}

// LockReason returns the profile-availability lock reason, or "" when the
// failure is a revocation.
func (e *NotLiveError) LockReason() string {
	if e.Code() == CodeProfileUnavailable {
		return e.Status
	}
	return ""
}

// livenessResult is one row of the per-request liveness query.
type livenessResult struct {
	Status                  string
	SessionID               int64
	CredentialID            int64
	PrincipalID             int64
	ProfileID               int64
	PartyID                 int64
	ImpersonatorPrincipalID int64
	AppID                   int64
	ACR                     int16
	AMR                     []string
	AuthTime                time.Time
	IsUser                  bool
	IsGuest                 bool
	EmailVerified           bool
	// AudienceRegistered is true when no app-slug check was requested or one
	// of the requested slugs is a registered app's.
	AudienceRegistered bool
	// InviteOnly is true when the session's app is invite-only; the
	// authenticator then checks the `login` gate for the acting profile.
	InviteOnly bool
}

// livenessFn runs the liveness query for a verified carrier. appSlugs, when
// non-empty, are audience values to check against registered app slugs in
// the same round trip.
type livenessFn func(ctx context.Context, c *carrier, appSlugs []string) (livenessResult, error)

// Authenticator authenticates bearer access tokens: it verifies the EdDSA
// carrier offline, then checks the session (sid) or credential (cid) and the
// acting profile in one database round trip (decision U5), so a revocation
// or a profile lock takes effect on the next request. It is safe for
// concurrent use.
type Authenticator struct {
	verifier *Verifier
	audience audiencePolicy
	liveness livenessFn
	now      func() time.Time

	// appGate authorizes `login` on the session's app for invite-only apps
	// (decision U15). nil refuses every invite-only app.
	appGate coreAuthz.Authorizer
}

// NewAuthenticator builds an Authenticator over pool (liveness), verifier
// (normally NewLocalVerifier's) and the auth configuration (audiences).
func NewAuthenticator(pool *pgxpool.Pool, verifier *Verifier, cfg config.AuthConfig) *Authenticator {
	return &Authenticator{
		verifier: verifier,
		audience: newAudiencePolicy(cfg),
		liveness: poolLiveness(pool),
		now:      time.Now,
	}
}

// NewAuthenticatorWithAppGate is NewAuthenticator plus the authorizer that
// evaluates the invite-only app gate (see SetAppGate). The module manifest
// constructs the authenticator through it.
func NewAuthenticatorWithAppGate(pool *pgxpool.Pool, verifier *Verifier, cfg config.AuthConfig, az coreAuthz.Authorizer) *Authenticator {
	a := NewAuthenticator(pool, verifier, cfg)
	a.SetAppGate(az)
	return a
}

// bearerToken extracts the token of an "Authorization: Bearer <token>"
// header, or returns ErrNoAuthHeader. No route reads an authentication
// cookie.
func bearerToken(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", ErrNoAuthHeader
	}
	tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	if tok == "" {
		return "", ErrNoAuthHeader
	}
	return tok, nil
}

// Authenticate resolves the request's bearer token to a live Identity. It
// does not touch the response writer.
//
// Errors:
//   - ErrNoAuthHeader: missing or non-Bearer Authorization header.
//   - ErrInvalidToken: the token failed verification or its audience is not
//     accepted.
//   - *NotLiveError (errors.Is ErrNotLive): the session or credential is
//     revoked, expired or not the token's, or the profile is unavailable.
//   - any other error: a database fault (treat as 500).
func (a *Authenticator) Authenticate(r *http.Request) (*Identity, error) {
	raw, err := bearerToken(r)
	if err != nil {
		return nil, err
	}

	c, err := a.verifier.verifyCarrier(raw, a.now())
	if err != nil {
		return nil, ErrInvalidToken
	}
	audOK, appSlugs := a.audience.check(c.Audience)
	if !audOK && len(appSlugs) == 0 {
		return nil, ErrInvalidToken
	}
	if audOK {
		appSlugs = nil
	}

	res, err := a.liveness(r.Context(), c, appSlugs)
	if err != nil {
		return nil, fmt.Errorf("auth: liveness: %w", err)
	}
	if !res.AudienceRegistered {
		return nil, ErrInvalidToken
	}
	if res.Status != livenessStatusOK {
		return nil, &NotLiveError{Status: res.Status}
	}

	id := &Identity{
		PrincipalID:             res.PrincipalID,
		PrincipalUUID:           c.PrincipalUUID,
		PrincipalKind:           PrincipalKindServiceAccount,
		ProfileID:               res.ProfileID,
		ProfileUUID:             c.ProfileUUID,
		PartyID:                 res.PartyID,
		SessionID:               res.SessionID,
		SessionUUID:             c.SessionUUID,
		CredentialID:            res.CredentialID,
		AppID:                   res.AppID,
		ACR:                     res.ACR,
		AMR:                     res.AMR,
		AuthTime:                res.AuthTime,
		ImpersonatorPrincipalID: res.ImpersonatorPrincipalID,
		IsGuest:                 res.IsGuest,
		EmailVerified:           res.EmailVerified,
	}
	if res.IsUser {
		id.PrincipalKind = PrincipalKindUser
	}
	if res.InviteOnly {
		if err := a.checkAppGate(r.Context(), id); err != nil {
			return nil, err
		}
	}
	if c.CredentialUUID != uuid.Nil {
		// Client credentials: the credential row has no amr or auth_time;
		// both come from the token (amr ["swk"], auth_time = issue time).
		id.AMR = c.AMR
		id.AuthTime = c.AuthTime
	}
	return id, nil
}

// sessionLivenessSQL runs mod_users.session_liveness and, in the same
// statement, reads the user flags the Identity needs and (when $4 is
// non-empty) whether an audience value is a registered, non-archived app
// slug (an app's archived_at lives on its entities row). One round
// trip per request (decision U5).
const sessionLivenessSQL = `
SELECT l.status,
       l.session_id,
       l.principal_id,
       l.profile_id,
       l.party_id,
       l.impersonator_principal_id,
       l.app_id,
       l.acr,
       l.amr,
       l.auth_time,
       u.entity_id IS NOT NULL,
       COALESCE(u.is_guest, false),
       EXISTS (SELECT 1 FROM mod_users.user_identifiers i
                WHERE i.user_id = l.principal_id
                  AND i.kind = 'email'
                  AND i.verified_at IS NOT NULL),
       (cardinality($4::text[]) = 0
        OR EXISTS (SELECT 1 FROM public.apps a
                   JOIN public.entities ae ON ae.id = a.id
                   WHERE a.slug = ANY($4::text[])
                     AND ae.archived_at IS NULL)),
       COALESCE(app.invite_only, false)
FROM mod_users.session_liveness($1::uuid, $2::uuid, $3::uuid) l
LEFT JOIN mod_users.users u ON u.entity_id = l.principal_id
LEFT JOIN public.apps app ON app.id = l.app_id`

// credentialLivenessSQL is sessionLivenessSQL's counterpart for
// client-credentials tokens, over mod_users.credential_liveness.
const credentialLivenessSQL = `
SELECT l.status,
       l.credential_id,
       l.principal_id,
       l.profile_id,
       l.party_id,
       l.acr,
       u.entity_id IS NOT NULL,
       COALESCE(u.is_guest, false),
       EXISTS (SELECT 1 FROM mod_users.user_identifiers i
                WHERE i.user_id = l.principal_id
                  AND i.kind = 'email'
                  AND i.verified_at IS NOT NULL),
       (cardinality($4::text[]) = 0
        OR EXISTS (SELECT 1 FROM public.apps a
                   JOIN public.entities ae ON ae.id = a.id
                   WHERE a.slug = ANY($4::text[])
                     AND ae.archived_at IS NULL))
FROM mod_users.credential_liveness($1::uuid, $2::uuid, $3::uuid) l
LEFT JOIN mod_users.users u ON u.entity_id = l.principal_id`

// poolLiveness returns the production livenessFn over pool. Every value is
// bound as a parameter, never interpolated.
func poolLiveness(pool *pgxpool.Pool) livenessFn {
	return func(ctx context.Context, c *carrier, appSlugs []string) (livenessResult, error) {
		if pool == nil {
			return livenessResult{}, errors.New("no database pool")
		}
		if appSlugs == nil {
			appSlugs = []string{}
		}
		var (
			res                                       livenessResult
			sessionID, credentialID, principalID      *int64
			profileID, partyID, impersonatorID, appID *int64
			acr                                       *int16
			authTime                                  *time.Time
		)
		var err error
		if c.SessionUUID != uuid.Nil {
			err = pool.QueryRow(ctx, sessionLivenessSQL,
				c.SessionUUID, c.PrincipalUUID, c.ProfileUUID, appSlugs,
			).Scan(&res.Status, &sessionID, &principalID, &profileID, &partyID,
				&impersonatorID, &appID, &acr, &res.AMR, &authTime,
				&res.IsUser, &res.IsGuest, &res.EmailVerified, &res.AudienceRegistered, &res.InviteOnly)
		} else {
			err = pool.QueryRow(ctx, credentialLivenessSQL,
				c.CredentialUUID, c.PrincipalUUID, c.ProfileUUID, appSlugs,
			).Scan(&res.Status, &credentialID, &principalID, &profileID, &partyID, &acr,
				&res.IsUser, &res.IsGuest, &res.EmailVerified, &res.AudienceRegistered)
		}
		if err != nil {
			return livenessResult{}, err
		}
		res.SessionID = deref(sessionID)
		res.CredentialID = deref(credentialID)
		res.PrincipalID = deref(principalID)
		res.ProfileID = deref(profileID)
		res.PartyID = deref(partyID)
		res.ImpersonatorPrincipalID = deref(impersonatorID)
		res.AppID = deref(appID)
		res.ACR = deref(acr)
		if authTime != nil {
			res.AuthTime = *authTime
		}
		return res, nil
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// lockAppLoginNotGranted is the profile-unavailable lock reason of an
// invite-only app that does not admit the acting profile
// (service.LockAppLoginNotGranted).
const lockAppLoginNotGranted = "app_login_not_granted"

// SetAppGate installs the authorizer that evaluates the invite-only app gate:
// whenever the session's app has apps.invite_only, a request is live only if
// Authorize(ctx-with-acting-profile, "login", &app.entity_id) allows it
// (decision U15), so a revoked or removed `login` grant takes effect on the
// next request. Without a gate, every invite-only app is refused. An app that
// is not invite-only costs no extra query. Call it once at startup, before
// the authenticator serves requests.
func (a *Authenticator) SetAppGate(az coreAuthz.Authorizer) {
	a.appGate = az
}

// isStepUpRequired reports whether err is, or wraps, the engine's assurance
// refusal: an action-required error with code users.step_up_required.
func isStepUpRequired(err error) bool {
	var action *apiresp.ActionRequiredError
	return errors.As(err, &action) && action.Action().Code == useraction.StepUpRequired.Code
}

// checkAppGate applies the invite-only app gate to the authenticated identity.
// A refusal is the profile-unavailable failure with lock reason
// app_login_not_granted. An engine step-up refusal (the operator set min_acr
// or max_auth_age on `login`) propagates unchanged as the action-required error
// (409 users.step_up_required, rendered by writeAuthError), exactly as
// SessionService.gateFor does; any other error (a database fault) is wrapped.
func (a *Authenticator) checkAppGate(ctx context.Context, id *Identity) error {
	denied := &NotLiveError{Status: lockAppLoginNotGranted}
	if a.appGate == nil || id.AppID <= 0 {
		return denied
	}
	appID := id.AppID
	candidate := opctx.WithAuthContext(ctx, opctx.AuthContext{
		PrincipalID:             id.PrincipalID,
		ProfileID:               id.ProfileID,
		SessionID:               id.SessionID,
		ImpersonatorPrincipalID: id.ImpersonatorPrincipalID,
		ACR:                     id.ACR,
		AuthTime:                id.AuthTime,
	})
	err := a.appGate.Authorize(candidate, "login", &appID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, apiresp.ErrForbidden), errors.Is(err, apiresp.ErrUnauthenticated):
		return denied
	case isStepUpRequired(err):
		return err
	default:
		return fmt.Errorf("auth: app gate: %w", err)
	}
}
