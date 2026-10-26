package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Impersonation is a separate child session (never the administrator's own
// session switched to another profile):
//
//   - it records why (impersonation_reason, non-empty);
//   - it lives AUTH_IMPERSONATION_TTL from its start and from each renewal, and
//     never longer than AUTH_IMPERSONATION_MAX from its creation;
//   - it needs a fresh authentication: the `impersonate` operation carries a
//     max_auth_age that the authorization engine's assurance gate enforces
//     (409 users.step_up_required when stale);
//   - it never nests (checked here before the MF101 trigger, which backs it);
//   - it always records the real administrator: the session's
//     impersonator_session_id names the administrator's own session, and
//     session liveness reports that session's principal as the impersonator
//     (AuthContext.ImpersonatorPrincipalID, the token's act.sub).

// MaxImpersonationReasonLen bounds the reason an administrator gives, in
// characters.
const MaxImpersonationReasonLen = 500

// ErrImpersonationLimitReached is returned by Renew when the session has
// already lived AUTH_IMPERSONATION_MAX from its creation, so it cannot be
// extended (users.impersonation_limit_reached, 409).
var ErrImpersonationLimitReached = apiresp.Conflict(apiresp.FieldError{
	Field: "session", Code: "users.impersonation_limit_reached",
	Message: "the impersonation has reached its maximum duration",
})

// errImpersonationTargetUnavailable is returned by Start when the target
// profile cannot be acted as (not active, its holder is disabled, or its
// party's policy cannot be met by the impersonation session): reason is the
// profile_availability lock reason (users.impersonation_target_unavailable,
// 409).
func errImpersonationTargetUnavailable(reason string) error {
	return apiresp.Conflict(apiresp.FieldError{
		Field: "profile_uuid", Code: "users.impersonation_target_unavailable",
		Message: "the profile cannot be impersonated: " + reason,
	})
}

// ImpersonationService starts, renews and ends impersonation sessions. It
// builds on the SessionService's token issue and profile views.
//
// Observations (API surface section 10.1, decision U21) use resource
// "impersonation" with target the impersonated profile: `impersonate` on
// start (the actor is the administrator's own profile, in the administrator's
// own session), `update` on renew and `revoke` on end (inside the child
// session, so the row carries the impersonator principal and the child
// session id). No other op is used.
type ImpersonationService struct {
	pool     *pgxpool.Pool
	q        usersdb.Querier
	az       coreAuthz.Authorizer
	obs      *observer.ObserverGroup
	sessions *SessionService
}

// NewImpersonationService constructs an ImpersonationService. q must be backed
// by pool; sessions supplies token issue, profile views and the lifetimes
// (SessionConfig.ImpersonationTTL and ImpersonationMax). An absent authorizer
// refuses every start.
func NewImpersonationService(
	pool *pgxpool.Pool,
	q usersdb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	sessions *SessionService,
) *ImpersonationService {
	return &ImpersonationService{pool: pool, q: q, az: az, obs: obs, sessions: sessions}
}

// StartImpersonationInput is the input of ImpersonationService.Start.
type StartImpersonationInput struct {
	// CallerSessionID is the administrator's own session (sessions.id).
	CallerSessionID int64
	// ProfileUUID is the profile to impersonate.
	ProfileUUID uuid.UUID
	// Reason is why the administrator impersonates; required.
	Reason string
}

// ValidateImpersonationReason reports the field error Start would return for a
// missing, blank or over-long reason, so an HTTP handler can answer 400 before
// any authorization or state check. It returns the trimmed reason.
func ValidateImpersonationReason(reason string) (string, error) {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return "", invalidField("reason", "users.reason_required", "reason is required")
	}
	if utf8.RuneCountInString(trimmed) > MaxImpersonationReasonLen {
		return "", invalidField("reason", "users.reason_too_long", "reason is too long")
	}
	return trimmed, nil
}

// impersonationTarget is the profile an impersonation names.
type impersonationTarget struct {
	profileID int64
	holderID  int64
}

const impersonationTargetSQL = `
SELECT p.entity_id, p.holder_id
  FROM public.entities e
  JOIN public.profiles p ON p.entity_id = e.id
 WHERE e.uuid = $1`

// lookupTarget finds the profile by uuid; found is false when none exists.
func (s *ImpersonationService) lookupTarget(ctx context.Context, profileUUID uuid.UUID) (t impersonationTarget, found bool, err error) {
	err = s.pool.QueryRow(ctx, impersonationTargetSQL, profileUUID).Scan(&t.profileID, &t.holderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return impersonationTarget{}, false, nil
	}
	if err != nil {
		return impersonationTarget{}, false, fmt.Errorf("impersonation: look up profile: %w", err)
	}
	return t, true, nil
}

// Start begins an impersonation of the target profile as a child of the
// caller's session and returns the token response of the child session: an
// access token acting as the target profile (with the administrator as the
// actor, `act.sub`), without a refresh token, expiring within
// AUTH_IMPERSONATION_TTL. ctx carries the administrator's AuthContext.
//
// Order of refusals: ErrImpersonationNotAllowed (403) when the caller's
// session is itself an impersonation or the profile is held by the caller;
// then the authorization of `impersonate` on the profile's entity, whose
// assurance gate answers 409 users.step_up_required for a stale
// authentication and whose grant arms answer 403; apiresp.ErrNotFound only
// for a caller who is allowed to impersonate every profile (a wildcard
// holder) and names none that exists, so existence never leaks to a caller
// without the authority; then a 409 when the profile cannot be acted as.
func (s *ImpersonationService) Start(ctx context.Context, in StartImpersonationInput) (TokenResponse, error) {
	reason, err := ValidateImpersonationReason(in.Reason)
	if err != nil {
		return TokenResponse{}, err
	}
	// The AuthContext the middleware installed knows the caller is already
	// impersonating; the session row is the authority behind it.
	if ac, ok := opctx.Auth(ctx); ok && ac.ImpersonatorPrincipalID != 0 {
		return TokenResponse{}, ErrImpersonationNotAllowed
	}
	parent, err := s.sessions.liveSession(ctx, s.q, in.CallerSessionID)
	if err != nil {
		return TokenResponse{}, err
	}
	if parent.ImpersonatorSessionID.Valid {
		return TokenResponse{}, ErrImpersonationNotAllowed
	}
	if s.az == nil {
		return TokenResponse{}, apiresp.ErrForbidden
	}

	target, found, err := s.lookupTarget(ctx, in.ProfileUUID)
	if err != nil {
		return TokenResponse{}, err
	}
	if !found {
		// A nil target is allowed only by a wildcard grant, so a caller
		// without the authority gets the same refusal for a profile that
		// does not exist as for one it may not impersonate.
		if err := s.az.Authorize(ctx, opImpersonate, nil); err != nil {
			return TokenResponse{}, err
		}
		return TokenResponse{}, apiresp.ErrNotFound
	}
	if target.holderID == parent.PrincipalID {
		return TokenResponse{}, ErrImpersonationNotAllowed
	}
	// opImpersonate is both the authorization operation, checked on the target
	// profile's entity, and the audit op of the start.
	if err := s.az.Authorize(ctx, opImpersonate, &target.profileID); err != nil {
		return TokenResponse{}, err
	}

	var resp TokenResponse
	err = s.sessions.inTx(ctx, func(tx pgx.Tx) error {
		q := s.sessions.usersQ(tx)
		now := s.sessions.now().UTC()
		amr := parent.Amr
		if amr == nil {
			amr = []string{}
		}
		child, err := q.CreateSession(ctx, usersdb.CreateSessionParams{
			PrincipalID:           target.holderID,
			Acr:                   parent.Acr,
			Amr:                   amr,
			AuthTime:              timestamptz(now),
			ExpiresAt:             timestamptz(now.Add(s.sessions.cfg.ImpersonationTTL)),
			ImpersonatorSessionID: pgtype.Int8{Int64: parent.ID, Valid: true},
			ImpersonatedProfileID: pgtype.Int8{Int64: target.profileID, Valid: true},
			ImpersonationReason:   pgtype.Text{String: reason, Valid: true},
		})
		if IsSessionInvariantError(err) {
			return ErrImpersonationNotAllowed
		}
		if err != nil {
			return fmt.Errorf("impersonation: create session: %w", err)
		}

		view, sc, views, err := s.childView(ctx, tx, child)
		if err != nil {
			return err
		}
		if !view.Available {
			return errImpersonationTargetUnavailable(lockReasonOf(view))
		}
		resp, err = s.sessions.tokenResponse(sc, &view, views, nil)
		if err != nil {
			return err
		}
		return observeInTx(ctx, s.obs, tx, opImpersonate, resourceImpersonation, target.profileID, nil, map[string]any{
			"session_uuid": child.Uuid.String(),
			"reason":       reason,
			"expires_at":   child.ExpiresAt.Time.UTC().Format(time.RFC3339),
		})
	})
	if err != nil {
		return TokenResponse{}, err
	}
	return resp, nil
}

// Renew extends the caller's impersonation session to now plus
// AUTH_IMPERSONATION_TTL, never beyond AUTH_IMPERSONATION_MAX from the
// session's creation, and returns a token response (no refresh token) with a
// fresh access token. It is ErrImpersonationNotAllowed outside an
// impersonation session and ErrImpersonationLimitReached when the cap leaves
// no room to extend. ctx carries the impersonation AuthContext.
func (s *ImpersonationService) Renew(ctx context.Context, sessionID int64) (TokenResponse, error) {
	var resp TokenResponse
	err := s.sessions.inTx(ctx, func(tx pgx.Tx) error {
		q := s.sessions.usersQ(tx)
		sess, err := s.sessions.liveSession(ctx, q, sessionID)
		if err != nil {
			return err
		}
		if !sess.ImpersonatorSessionID.Valid {
			return ErrImpersonationNotAllowed
		}
		cfg := s.sessions.cfg
		next, ok := renewedExpiry(s.sessions.now().UTC(), sess.CreatedAt.Time, sess.ExpiresAt.Time,
			cfg.ImpersonationTTL, cfg.ImpersonationMax)
		if !ok {
			return ErrImpersonationLimitReached
		}
		renewed, err := q.SetSessionExpiry(ctx, usersdb.SetSessionExpiryParams{ID: sess.ID, ExpiresAt: timestamptz(next)})
		if err != nil {
			return fmt.Errorf("impersonation: renew: %w", err)
		}

		view, sc, views, err := s.childView(ctx, tx, renewed)
		if err != nil {
			return err
		}
		if !view.Available {
			return ProfileUnavailable(lockReasonOf(view))
		}
		resp, err = s.sessions.tokenResponse(sc, &view, views, nil)
		if err != nil {
			return err
		}
		return observeInTx(ctx, s.obs, tx, opUpdate, resourceImpersonation, renewed.ImpersonatedProfileID.Int64,
			map[string]any{"session_uuid": sess.Uuid.String(), "expires_at": sess.ExpiresAt.Time.UTC().Format(time.RFC3339)},
			map[string]any{"session_uuid": renewed.Uuid.String(), "expires_at": renewed.ExpiresAt.Time.UTC().Format(time.RFC3339)})
	})
	if err != nil {
		return TokenResponse{}, err
	}
	return resp, nil
}

// renewedExpiry returns the expiry a renewal at now gives a session created at
// createdAt that currently expires at expiresAt: now plus ttl, capped at
// maxLife from createdAt. ok is false when that is not later than the current
// expiry (the cap is reached), so a renewal never shortens a session.
func renewedExpiry(now, createdAt, expiresAt time.Time, ttl, maxLife time.Duration) (next time.Time, ok bool) {
	next = now.Add(ttl)
	if limit := createdAt.Add(maxLife); next.After(limit) {
		next = limit
	}
	return next, next.After(expiresAt)
}

// End revokes the caller's impersonation session (revoke_reason
// impersonation_ended). It is ErrImpersonationNotAllowed outside an
// impersonation session. ctx carries the impersonation AuthContext.
func (s *ImpersonationService) End(ctx context.Context, sessionID int64) error {
	return s.sessions.inTx(ctx, func(tx pgx.Tx) error {
		q := s.sessions.usersQ(tx)
		sess, err := s.sessions.liveSession(ctx, q, sessionID)
		if err != nil {
			return err
		}
		if !sess.ImpersonatorSessionID.Valid {
			return ErrImpersonationNotAllowed
		}
		ended, err := q.RevokeSession(ctx, usersdb.RevokeSessionParams{
			ID: sess.ID, RevokeReason: pgtype.Text{String: SessionRevokeImpersonationEnded, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionRevoked
		}
		if err != nil {
			return fmt.Errorf("impersonation: end: %w", err)
		}
		return observeInTx(ctx, s.obs, tx, opRevoke, resourceImpersonation, ended.ImpersonatedProfileID.Int64,
			map[string]any{"session_uuid": sess.Uuid.String()},
			map[string]any{"session_uuid": ended.Uuid.String(), "revoke_reason": SessionRevokeImpersonationEnded})
	})
}

// childView loads the child session's context and its one profile view (the
// impersonated profile), which is unavailable when the profile cannot be acted
// as. views is the child's profile list: exactly that profile.
func (s *ImpersonationService) childView(ctx context.Context, tx pgx.Tx, child usersdb.Session) (ProfileView, sessionContext, []ProfileView, error) {
	sc, err := s.sessions.loadSessionContext(ctx, tx, child)
	if err != nil {
		return ProfileView{}, sessionContext{}, nil, err
	}
	views, err := s.sessions.sessionViews(ctx, tx, sc)
	if err != nil {
		return ProfileView{}, sessionContext{}, nil, err
	}
	view, ok := findViewByID(views, child.ImpersonatedProfileID.Int64)
	if !ok {
		// The profile ended since: it is no longer listed.
		return ProfileView{Lock: &ProfileLock{Reason: "inactive"}}, sc, views, nil
	}
	return view, sc, views, nil
}

// lockReasonOf returns the lock reason of an unavailable view.
func lockReasonOf(v ProfileView) string {
	if v.Lock != nil && v.Lock.Reason != "" {
		return v.Lock.Reason
	}
	return "not_held"
}
