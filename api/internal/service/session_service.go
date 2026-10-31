package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
	"github.com/moduleforge/mod-users/api/internal/useraction"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Session revoke reasons (sessions.revoke_reason CHECK).
const (
	SessionRevokeLogout             = "logout"
	SessionRevokeAdmin              = "admin"
	SessionRevokeCredentialRevoked  = "credential_revoked"
	SessionRevokePrincipalDisabled  = "principal_disabled"
	SessionRevokeRefreshReuse       = "refresh_reuse"
	SessionRevokeImpersonationEnded = "impersonation_ended"
	SessionRevokeParentRevoked      = "parent_revoked"
	SessionRevokeProfileEnded       = "profile_ended"
)

var validSessionRevokeReasons = map[string]bool{
	SessionRevokeLogout: true, SessionRevokeAdmin: true, SessionRevokeCredentialRevoked: true,
	SessionRevokePrincipalDisabled: true, SessionRevokeRefreshReuse: true,
	SessionRevokeImpersonationEnded: true, SessionRevokeParentRevoked: true, SessionRevokeProfileEnded: true,
}

// opLoginApp is the authz operation the invite-only app gate checks.
const opLoginApp = "login"

// refreshTokenPrefix marks an opaque refresh token; the database keeps only
// HashToken of the whole token.
const refreshTokenPrefix = "mfr_"

// tokenTypeBearer is the token_type of every token response.
const tokenTypeBearer = "Bearer"

// SessionConfig holds the token and session lifetimes and the carrier's
// issuer and default audience.
type SessionConfig struct {
	// Issuer is LOCAL_ISSUER.
	Issuer string
	// DefaultAudience is the aud of a token whose session has no app.
	DefaultAudience string
	// AccessTokenTTL is AUTH_ACCESS_TOKEN_TTL (capped at one hour by the
	// carrier).
	AccessTokenTTL time.Duration
	// SessionMaxAge is AUTH_SESSION_MAX_AGE: sessions.expires_at = now + it.
	SessionMaxAge time.Duration
	// IdleTimeout is AUTH_SESSION_IDLE_TIMEOUT: a refresh token expires this
	// long after it is minted (never after the session).
	IdleTimeout time.Duration
	// ClientCredentialsTTL is AUTH_CLIENT_CREDENTIALS_TTL.
	ClientCredentialsTTL time.Duration
	// ImpersonationTTL is AUTH_IMPERSONATION_TTL: the lifetime of an
	// impersonation session at its start and of each renewal.
	ImpersonationTTL time.Duration
	// ImpersonationMax is AUTH_IMPERSONATION_MAX: the longest an impersonation
	// session lives from its creation, renewals included.
	ImpersonationMax time.Duration
}

// SessionConfigFrom builds a SessionConfig from the loaded configuration.
func SessionConfigFrom(cfg *config.Config) SessionConfig {
	return SessionConfig{
		Issuer:               cfg.LocalAuth.LocalIssuer,
		DefaultAudience:      cfg.Auth.DefaultAudience,
		AccessTokenTTL:       cfg.Auth.AccessTokenTTL,
		SessionMaxAge:        cfg.Auth.SessionMaxAge,
		IdleTimeout:          cfg.Auth.SessionIdleTimeout,
		ClientCredentialsTTL: cfg.Auth.ClientCredentialsTTL,
		ImpersonationTTL:     cfg.Auth.ImpersonationTTL,
		ImpersonationMax:     cfg.Auth.ImpersonationMax,
	}
}

// SessionView is the session part of a token response.
type SessionView struct {
	UUID          uuid.UUID `json:"uuid"`
	ACR           int16     `json:"acr"`
	AMR           []string  `json:"amr"`
	AuthTime      time.Time `json:"auth_time"`
	Impersonating bool      `json:"impersonating"`
}

// TokenResponse is the token response of every sign-in, refresh, handoff,
// switch, fork and step-up (API surface section 7, binding for GUIs). Every
// member is always present: AccessToken is null when the landing chain found
// no profile (SelectionRequired is then true and the client refreshes with a
// chosen profile_uuid), RefreshToken is null when no refresh token was minted
// (switch, step-up, client credentials), and Session is null for client
// credentials.
type TokenResponse struct {
	AccessToken       *string       `json:"access_token"`
	TokenType         string        `json:"token_type"`
	ExpiresIn         int64         `json:"expires_in"`
	RefreshToken      *string       `json:"refresh_token"`
	Session           *SessionView  `json:"session"`
	Profile           *ProfileView  `json:"profile"`
	SelectionRequired bool          `json:"selection_required"`
	Profiles          []ProfileView `json:"profiles"`
}

// SessionService owns interactive sessions: creation with factors, the
// landing chain, access-token issue, refresh lineages with reuse detection,
// profile switch, fork, step-up, logout and revocation, plus the
// client-credentials grant (no session). Liveness per request is the
// authentication middleware's (auth.Authenticator).
//
// Observations (API surface section 10.1, decision U21) use resource
// "session" with target the principal: `login` when a sign-in creates a
// session, `update` on step-up, `revoke` on every revocation. Refresh
// rotation, fork and switch write none. The sign-in flows run before any
// AuthContext exists, so they install the unauthenticated-flow AuthContext of
// section 10.1 (acting profile: the landed profile, else the personal
// profile, else the oldest non-ended profile; SessionID the new session).
type SessionService struct {
	pool       *pgxpool.Pool
	q          usersdb.Querier
	coreQ      coredb.Querier
	az         coreAuthz.Authorizer
	obs        *observer.ObserverGroup
	signer     *localauth.Signer
	challenges *ChallengeService
	cfg        SessionConfig
	newQ       func(pgx.Tx) usersdb.Querier
	newCoreQ   func(pgx.Tx) coredb.Querier
	now        func() time.Time
}

// NewSessionService constructs a SessionService. q and coreQ must be backed
// by pool; flows that write open their own transaction on pool, and the
// ...InTx methods take the caller's. az runs the invite-only app gate (an
// absent authorizer refuses every invite-only app). challenges verifies the
// email codes of the code sign-in and the step-up.
func NewSessionService(
	pool *pgxpool.Pool,
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	signer *localauth.Signer,
	challenges *ChallengeService,
	cfg SessionConfig,
) *SessionService {
	return &SessionService{
		pool: pool, q: q, coreQ: coreQ, az: az, obs: obs, signer: signer,
		challenges: challenges, cfg: cfg, now: time.Now,
	}
}

func (s *SessionService) usersQ(tx pgx.Tx) usersdb.Querier {
	if s.newQ != nil {
		return s.newQ(tx)
	}
	return usersdb.New(tx)
}

func (s *SessionService) coreQuerier(tx pgx.Tx) coredb.Querier {
	if s.newCoreQ != nil {
		return s.newCoreQ(tx)
	}
	return coredb.New(tx)
}

// inTx runs fn in a transaction on the pool and commits when fn succeeds.
func (s *SessionService) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if s.pool == nil {
		return errors.New("sessions: no database pool")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("sessions: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("sessions: commit: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// session creation
// ---------------------------------------------------------------------------

// Factor is a credential verified by a sign-in. AssertedACR is the acr an
// external IdP asserted for an oidc factor (0 when none); it counts only when
// the provider has trust_acr.
type Factor struct {
	CredentialID int64
	AssertedACR  int16
}

// StartSessionInput is the input of StartSessionInTx and CreateSessionInTx.
type StartSessionInput struct {
	PrincipalID int64
	// AppID is the app the session signs in to (apps.id); 0 for none.
	AppID int64
	// Factors are the verified credentials; at least one.
	Factors []Factor
	// CredentialDefaultProfileID is the signing-in credential's
	// default_profile_id (0 when unset), the first landing candidate.
	CredentialDefaultProfileID int64
	// RequestedProfileUUID, when set, is the profile the client asked to land
	// on; it replaces the landing chain.
	RequestedProfileUUID uuid.UUID
}

// SessionRef identifies a created session.
type SessionRef struct {
	ID          int64
	UUID        uuid.UUID
	PrincipalID int64
}

// StartSessionInTx is the common tail of every interactive sign-in: it
// creates the session (expires_at = now + AUTH_SESSION_MAX_AGE, auth_time =
// now) with its factors, sets acr from mod_users.session_acr and amr from
// the factor kinds, runs the landing chain, writes the `login` observation
// as the section 10.1 acting profile, mints the tab's refresh lineage and
// returns the token response. It does not commit.
//
// Errors: ProfileUnavailable("principal_disabled") for a disabled principal
// and ProfileUnavailable("not_held") for a principal with no non-ended
// profile (no session is usable); ErrAppLoginNotGranted when the app is
// invite-only and admits none of the principal's available profiles. On
// error the caller rolls back, so a failed sign-in writes no observation.
func (s *SessionService) StartSessionInTx(ctx context.Context, tx pgx.Tx, in StartSessionInput) (TokenResponse, error) {
	q := s.usersQ(tx)
	sess, err := s.createSessionRow(ctx, tx, q, in)
	if err != nil {
		return TokenResponse{}, err
	}
	sc, err := s.loadSessionContext(ctx, tx, sess)
	if err != nil {
		return TokenResponse{}, err
	}
	user, err := q.GetUserByEntityID(ctx, sess.PrincipalID)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sessions: load user: %w", err)
	}

	landed, views, err := s.land(ctx, tx, sc, landingInput{
		credentialDefaultProfileID: in.CredentialDefaultProfileID,
		userDefaultProfileID:       user.DefaultProfileID.Int64,
		requestedProfileUUID:       in.RequestedProfileUUID,
	})
	if err != nil {
		return TokenResponse{}, err
	}
	if len(views) == 0 {
		return TokenResponse{}, ProfileUnavailable("not_held")
	}
	if landed == nil && sc.inviteOnly && !anyAvailable(views) && anyGateLocked(views) {
		return TokenResponse{}, ErrAppLoginNotGranted
	}

	acting := views[0]
	if landed != nil {
		acting = *landed
	}
	if err := s.observeLogin(ctx, tx, sess, acting.id); err != nil {
		return TokenResponse{}, err
	}

	refresh, err := s.mintRefreshInTx(ctx, q, sess, uuid.New())
	if err != nil {
		return TokenResponse{}, err
	}
	return s.tokenResponse(sc, landed, views, &refresh.token)
}

// CreateSessionInTx creates a session with its factors and writes its
// `login` observation, without landing or minting tokens: the OIDC callback
// uses it, then hands the session to the tab through a login_handoff
// challenge that ExchangeHandoff redeems. The observation's acting profile is
// the principal's personal profile, else its oldest non-ended profile
// (section 10.1). It does not commit. Errors are those of StartSessionInTx
// except the app gate, which runs at the exchange.
func (s *SessionService) CreateSessionInTx(ctx context.Context, tx pgx.Tx, in StartSessionInput) (SessionRef, error) {
	q := s.usersQ(tx)
	sess, err := s.createSessionRow(ctx, tx, q, in)
	if err != nil {
		return SessionRef{}, err
	}
	acting, err := fallbackActingProfile(ctx, tx, sess.PrincipalID)
	if err != nil {
		return SessionRef{}, err
	}
	if acting == 0 {
		return SessionRef{}, ProfileUnavailable("not_held")
	}
	if err := s.observeLogin(ctx, tx, sess, acting); err != nil {
		return SessionRef{}, err
	}
	return SessionRef{ID: sess.ID, UUID: sess.Uuid, PrincipalID: sess.PrincipalID}, nil
}

// createSessionRow inserts the session and its factors and sets its acr and
// amr. A disabled principal is refused before any write.
func (s *SessionService) createSessionRow(ctx context.Context, tx pgx.Tx, q usersdb.Querier, in StartSessionInput) (usersdb.Session, error) {
	if len(in.Factors) == 0 {
		return usersdb.Session{}, errors.New("sessions: a session needs at least one factor")
	}
	principal, err := s.coreQuerier(tx).GetPrincipalByEntityID(ctx, in.PrincipalID)
	if err != nil {
		return usersdb.Session{}, fmt.Errorf("sessions: load principal: %w", err)
	}
	if principal.DisabledAt != nil {
		return usersdb.Session{}, ProfileUnavailable("principal_disabled")
	}

	now := s.now().UTC()
	appID := pgtype.Int8{}
	if in.AppID > 0 {
		appID = pgtype.Int8{Int64: in.AppID, Valid: true}
	}
	// acr and amr are placeholders until the factors exist: session_acr
	// derives them from the factor rows.
	sess, err := q.CreateSession(ctx, usersdb.CreateSessionParams{
		PrincipalID: in.PrincipalID,
		AppID:       appID,
		Acr:         1,
		Amr:         []string{},
		AuthTime:    timestamptz(now),
		ExpiresAt:   timestamptz(now.Add(s.cfg.SessionMaxAge)),
	})
	if err != nil {
		return usersdb.Session{}, fmt.Errorf("sessions: create session: %w", err)
	}
	for _, f := range in.Factors {
		if _, err := q.UpsertSessionFactor(ctx, usersdb.UpsertSessionFactorParams{
			SessionID:    sess.ID,
			CredentialID: f.CredentialID,
			AssertedAcr:  assertedACR(f.AssertedACR),
		}); err != nil {
			return usersdb.Session{}, fmt.Errorf("sessions: add factor: %w", err)
		}
	}
	return s.refreshAssurance(ctx, q, sess.ID, now)
}

func assertedACR(acr int16) pgtype.Int2 {
	if acr < 1 {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: acr, Valid: true}
}

// refreshAssurance recomputes the session's acr (mod_users.session_acr) and
// amr (its unrevoked factors' kinds) and sets auth_time.
func (s *SessionService) refreshAssurance(ctx context.Context, q usersdb.Querier, sessionID int64, authTime time.Time) (usersdb.Session, error) {
	acr, err := q.SessionACR(ctx, sessionID)
	if err != nil {
		return usersdb.Session{}, fmt.Errorf("sessions: derive acr: %w", err)
	}
	amr, err := q.ListSessionAMR(ctx, sessionID)
	if err != nil {
		return usersdb.Session{}, fmt.Errorf("sessions: derive amr: %w", err)
	}
	if amr == nil {
		amr = []string{}
	}
	sess, err := q.UpdateSessionAssurance(ctx, usersdb.UpdateSessionAssuranceParams{
		ID: sessionID, Acr: acr, Amr: amr, AuthTime: timestamptz(authTime),
	})
	if err != nil {
		return usersdb.Session{}, fmt.Errorf("sessions: set assurance: %w", err)
	}
	return sess, nil
}

// sessionAudit is the observer payload for a session: identifiers and
// assurance only.
func sessionAudit(sess usersdb.Session) map[string]any {
	m := map[string]any{
		"uuid":      sess.Uuid.String(),
		"acr":       sess.Acr,
		"amr":       sess.Amr,
		"auth_time": sess.AuthTime.Time.UTC().Format(time.RFC3339),
	}
	if sess.RevokeReason.Valid {
		m["revoke_reason"] = sess.RevokeReason.String
	}
	return m
}

// observeLogin writes the `login` `session` observation with the section
// 10.1 AuthContext for an unauthenticated flow.
func (s *SessionService) observeLogin(ctx context.Context, tx pgx.Tx, sess usersdb.Session, actingProfileID int64) error {
	// WHY no system-actor marker: the login is the real principal's own new
	// session. IMPLIES: the context is gated like that session; it is used for
	// the login observation only.
	actCtx := opctx.WithAuthContext(ctx, opctx.AuthContext{
		PrincipalID: sess.PrincipalID,
		ProfileID:   actingProfileID,
		SessionID:   sess.ID,
		ACR:         sess.Acr,
		AuthTime:    sess.AuthTime.Time,
	})
	return observeInTx(actCtx, s.obs, tx, opLogin, resourceSession, sess.PrincipalID, nil, sessionAudit(sess))
}

// fallbackActingProfileSQL picks the section 10.1 acting profile when no
// profile was landed: the personal profile, else the oldest non-ended one.
const fallbackActingProfileSQL = `
SELECT p.entity_id
  FROM public.profiles p
 WHERE p.holder_id = $1 AND p.status <> 'ended'
 ORDER BY (p.party_id IS NOT NULL), p.created_at, p.entity_id
 LIMIT 1`

// fallbackActingProfile returns the principal's personal profile, else its
// oldest non-ended profile, else 0.
func fallbackActingProfile(ctx context.Context, db usersdb.DBTX, principalID int64) (int64, error) {
	var id int64
	err := db.QueryRow(ctx, fallbackActingProfileSQL, principalID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("sessions: resolve acting profile: %w", err)
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// session context, landing and token issue
// ---------------------------------------------------------------------------

// sessionContext is a session row with what token issue needs beyond it.
type sessionContext struct {
	sess                      usersdb.Session
	principalUUID             uuid.UUID
	appSlug                   string
	inviteOnly                bool
	impersonatorPrincipalUUID uuid.UUID
}

const sessionContextSQL = `
SELECT pe.uuid,
       a.slug,
       COALESCE(a.invite_only, false),
       ipe.uuid
  FROM public.entities pe
  LEFT JOIN public.apps a ON a.id = $2
  LEFT JOIN mod_users.sessions parent ON parent.id = $3
  LEFT JOIN public.entities ipe ON ipe.id = parent.principal_id
 WHERE pe.id = $1`

func (s *SessionService) loadSessionContext(ctx context.Context, db usersdb.DBTX, sess usersdb.Session) (sessionContext, error) {
	sc := sessionContext{sess: sess}
	var (
		slug         pgtype.Text
		impersonator pgtype.UUID
	)
	if err := db.QueryRow(ctx, sessionContextSQL, sess.PrincipalID, sess.AppID, sess.ImpersonatorSessionID).
		Scan(&sc.principalUUID, &slug, &sc.inviteOnly, &impersonator); err != nil {
		return sessionContext{}, fmt.Errorf("sessions: load session context: %w", err)
	}
	sc.appSlug = slug.String
	if impersonator.Valid {
		sc.impersonatorPrincipalUUID = uuid.UUID(impersonator.Bytes)
	}
	return sc, nil
}

// live reports whether the session is unrevoked and unexpired.
func (s *SessionService) live(sess usersdb.Session) bool {
	return sess.RevokedAt == nil && sess.ExpiresAt.Time.After(s.now())
}

// gateFor returns the invite-only app gate for the session, or nil when the
// session's app is not invite-only.
//
// The gate is Authorize(ctx-with-candidate-profile, "login", &app.entity_id)
// (decision U15): the engine's wildcard, entity, owned-scope and own arms, and
// the actor groups the candidate profile belongs to, all apply. An app has no
// row of its own beyond its entity (apps.id is the entity id), so the target
// is the session's app id. Only an invite-only app pays the extra query; any
// other app returns a nil gate. An absent authorizer admits nobody.
func (s *SessionService) gateFor(sc sessionContext) appGate {
	if !sc.inviteOnly || !sc.sess.AppID.Valid {
		return nil
	}
	appID := sc.sess.AppID.Int64
	return func(ctx context.Context, profileID int64) (bool, error) {
		if s.az == nil {
			return false, nil
		}
		// WHY no system-actor marker: the candidate is the session's real
		// principal acting as one of its profiles. IMPLIES: the invite-only
		// app gate is evaluated with the engine's assurance and owning-party
		// gates applied to that session.
		candidate := opctx.WithAuthContext(ctx, opctx.AuthContext{
			PrincipalID: sc.sess.PrincipalID,
			ProfileID:   profileID,
			SessionID:   sc.sess.ID,
			ACR:         sc.sess.Acr,
			AuthTime:    sc.sess.AuthTime.Time,
		})
		err := s.az.Authorize(candidate, opLoginApp, &appID)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, apiresp.ErrForbidden), errors.Is(err, apiresp.ErrUnauthenticated):
			return false, nil
		case isStepUpRequired(err):
			return false, err
		default:
			return false, fmt.Errorf("sessions: app gate: %w", err)
		}
	}
}

// sessionViews lists the principal's profiles with their availability to
// the session. An impersonation session's only profile is the impersonated
// one: the holder's other profiles are neither listed nor reachable.
func (s *SessionService) sessionViews(ctx context.Context, db usersdb.DBTX, sc sessionContext) ([]ProfileView, error) {
	return loadProfileViews(ctx, db, viewScope{
		principalID: sc.sess.PrincipalID,
		sessionID:   sc.sess.ID,
		onlyProfile: sc.sess.ImpersonatedProfileID.Int64,
	}, s.gateFor(sc))
}

// landingInput holds the landing chain's candidates.
type landingInput struct {
	credentialDefaultProfileID int64
	userDefaultProfileID       int64
	requestedProfileUUID       uuid.UUID
}

// land runs the landing chain (API surface section 7): the requested profile
// when the client named one; otherwise the credential's default profile, the
// user's default profile, the personal profile, then the only available
// profile. Each candidate must be available to the session (and admitted by
// the app gate). landed is nil when the chain yields nothing.
func (s *SessionService) land(ctx context.Context, db usersdb.DBTX, sc sessionContext, in landingInput) (*ProfileView, []ProfileView, error) {
	views, err := s.sessionViews(ctx, db, sc)
	if err != nil {
		return nil, nil, err
	}
	if in.requestedProfileUUID != uuid.Nil {
		if v, ok := findView(views, in.requestedProfileUUID); ok && v.Available {
			return &v, views, nil
		}
		return nil, views, nil
	}
	for _, id := range []int64{in.credentialDefaultProfileID, in.userDefaultProfileID} {
		if v, ok := findViewByID(views, id); ok && v.Available {
			return &v, views, nil
		}
	}
	for _, v := range views {
		if v.Kind == ProfileKindPersonal && v.Available {
			return &v, views, nil
		}
	}
	var only *ProfileView
	for i := range views {
		if views[i].Available {
			if only != nil {
				return nil, views, nil
			}
			only = &views[i]
		}
	}
	if only != nil {
		v := *only
		return &v, views, nil
	}
	return nil, views, nil
}

func anyAvailable(views []ProfileView) bool {
	for _, v := range views {
		if v.Available {
			return true
		}
	}
	return false
}

func anyGateLocked(views []ProfileView) bool {
	for _, v := range views {
		if v.Lock != nil && v.Lock.Reason == LockAppLoginNotGranted {
			return true
		}
	}
	return false
}

// mintedRefresh is a minted refresh token: the opaque token, returned only
// now, and its stored row.
type mintedRefresh struct {
	token string
	row   usersdb.SessionRefreshToken
}

// mintRefreshInTx mints a refresh token in lineage for the session. It
// expires AUTH_SESSION_IDLE_TIMEOUT from now, and never after the session.
func (s *SessionService) mintRefreshInTx(ctx context.Context, q usersdb.Querier, sess usersdb.Session, lineage uuid.UUID) (mintedRefresh, error) {
	secret, err := randomHex(32)
	if err != nil {
		return mintedRefresh{}, fmt.Errorf("sessions: refresh token: %w", err)
	}
	token := refreshTokenPrefix + secret
	expires := s.now().Add(s.cfg.IdleTimeout)
	if sess.ExpiresAt.Valid && sess.ExpiresAt.Time.Before(expires) {
		expires = sess.ExpiresAt.Time
	}
	row, err := q.CreateRefreshToken(ctx, usersdb.CreateRefreshTokenParams{
		SessionID:   sess.ID,
		LineageUuid: lineage,
		TokenHash:   HashToken(token),
		ExpiresAt:   timestamptz(expires),
	})
	if err != nil {
		return mintedRefresh{}, fmt.Errorf("sessions: store refresh token: %w", err)
	}
	return mintedRefresh{token: token, row: row}, nil
}

func sessionView(sess usersdb.Session) *SessionView {
	amr := sess.Amr
	if amr == nil {
		amr = []string{}
	}
	return &SessionView{
		UUID:          sess.Uuid,
		ACR:           sess.Acr,
		AMR:           amr,
		AuthTime:      sess.AuthTime.Time.UTC(),
		Impersonating: sess.ImpersonatorSessionID.Valid,
	}
}

// tokenResponse builds the token response for the session: an access token
// for profile when it is set (selection is required otherwise), the session
// view, the profile list and the refresh token when one was minted.
func (s *SessionService) tokenResponse(sc sessionContext, profile *ProfileView, views []ProfileView, refresh *string) (TokenResponse, error) {
	resp := TokenResponse{
		TokenType:    tokenTypeBearer,
		RefreshToken: refresh,
		Session:      sessionView(sc.sess),
		Profiles:     views,
	}
	if resp.Profiles == nil {
		resp.Profiles = []ProfileView{}
	}
	if profile == nil {
		resp.SelectionRequired = true
		return resp, nil
	}
	token, exp, err := s.issueAccessToken(sc, profile.UUID)
	if err != nil {
		return TokenResponse{}, err
	}
	p := *profile
	resp.AccessToken = &token
	resp.ExpiresIn = int64(exp.Sub(s.now()).Round(time.Second) / time.Second)
	resp.Profile = &p
	return resp, nil
}

// issueAccessToken signs the session's access token acting as profileUUID.
func (s *SessionService) issueAccessToken(sc sessionContext, profileUUID uuid.UUID) (string, time.Time, error) {
	if s.signer == nil {
		return "", time.Time{}, errors.New("sessions: no token signer configured")
	}
	audience := sc.appSlug
	if audience == "" {
		audience = s.cfg.DefaultAudience
	}
	token, exp, err := localauth.IssueSessionToken(s.signer, localauth.SessionTokenParams{
		Issuer:                    s.cfg.Issuer,
		Audience:                  audience,
		PrincipalUUID:             sc.principalUUID,
		SessionUUID:               sc.sess.Uuid,
		ProfileUUID:               profileUUID,
		ACR:                       sc.sess.Acr,
		AMR:                       sc.sess.Amr,
		AuthTime:                  sc.sess.AuthTime.Time,
		ImpersonatorPrincipalUUID: sc.impersonatorPrincipalUUID,
		TTL:                       s.cfg.AccessTokenTTL,
		SessionExpiresAt:          sc.sess.ExpiresAt.Time,
	}, s.now())
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sessions: issue access token: %w", err)
	}
	return token, exp, nil
}

// ---------------------------------------------------------------------------
// revocation
// ---------------------------------------------------------------------------

// revokeSessionInTx revokes one session with reason, stops its refresh
// lineages, ends its impersonation children (parent_revoked), and writes a
// `revoke` `session` observation for each session it ended. ctx must carry an
// AuthContext with an acting profile. An already revoked session is a no-op.
func (s *SessionService) revokeSessionInTx(ctx context.Context, tx pgx.Tx, q usersdb.Querier, sessionID int64, reason string) error {
	revoked, err := q.RevokeSession(ctx, usersdb.RevokeSessionParams{
		ID: sessionID, RevokeReason: pgtype.Text{String: reason, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sessions: revoke session: %w", err)
	}
	return s.afterRevoke(ctx, tx, q, []usersdb.Session{revoked})
}

// afterRevoke finishes revoking sessions already marked revoked: their
// refresh tokens stop rotating, their impersonation children end, and each
// ended session is observed. When ctx carries no acting profile (only the
// refresh-reuse path of a principal without any non-ended profile), the
// revocation still happens and is logged instead of observed, because an
// observation without an actor would fail and roll the revocation back.
func (s *SessionService) afterRevoke(ctx context.Context, tx pgx.Tx, q usersdb.Querier, revoked []usersdb.Session) error {
	actor, _ := opctx.ProfileID(ctx)
	hasActor := actor > 0
	for len(revoked) > 0 {
		sess := revoked[0]
		revoked = revoked[1:]
		if _, err := q.ExpireRefreshTokensBySession(ctx, sess.ID); err != nil {
			return fmt.Errorf("sessions: expire refresh tokens: %w", err)
		}
		if hasActor {
			if err := observeInTx(ctx, s.obs, tx, opRevoke, resourceSession, sess.PrincipalID, nil, sessionAudit(sess)); err != nil {
				return err
			}
		} else {
			logNoActor(ctx, sess.ID, sess.RevokeReason.String)
		}
		if sess.ImpersonatorSessionID.Valid {
			continue // impersonations do not nest (MF101)
		}
		children, err := q.RevokeImpersonationChildren(ctx, usersdb.RevokeImpersonationChildrenParams{
			ImpersonatorSessionID: pgtype.Int8{Int64: sess.ID, Valid: true},
			RevokeReason:          pgtype.Text{String: SessionRevokeParentRevoked, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("sessions: end impersonations: %w", err)
		}
		revoked = append(revoked, children...)
	}
	return nil
}

// UnauthenticatedActingContext returns ctx carrying the AuthContext an
// unauthenticated flow (password reset, OIDC callback) uses for its
// observations about principalID when no session is involved (API surface
// section 10.1): the principal acting as its personal profile, else its
// oldest non-ended profile. ok is false when the principal holds no
// non-ended profile, so no actor exists to observe as.
func UnauthenticatedActingContext(ctx context.Context, db usersdb.DBTX, principalID int64) (context.Context, bool, error) {
	acting, err := fallbackActingProfile(ctx, db, principalID)
	if err != nil || acting == 0 {
		return ctx, false, err
	}
	// WHY no system-actor marker: password reset and the OIDC link and step-up
	// callbacks act for a real user who has not authenticated in this
	// request, not for trusted platform code (withActingProfile).
	// IMPLIES: the context is for observations only; the engine would gate it
	// and refuse it if it reached an Authorize entry point.
	return withActingProfile(ctx, principalID, acting), true, nil
}

// withFallbackActor returns ctx carrying the section 10.1 AuthContext for an
// observation about principalID's session sessionID: the personal profile,
// else the oldest non-ended profile. When the principal holds no non-ended
// profile the AuthContext has no acting profile, and afterRevoke logs instead
// of observing.
func withFallbackActor(ctx context.Context, db usersdb.DBTX, principalID, sessionID int64) (context.Context, error) {
	acting, err := fallbackActingProfile(ctx, db, principalID)
	if err != nil {
		return ctx, err
	}
	// WHY no system-actor marker: refresh-reuse revocation acts for the
	// session's own (real) principal, so the context carries that session
	// and a real user's profile, never the trusted-code marker.
	// IMPLIES: the engine gates it like any session context; with no ACR or
	// AuthTime (and a session being revoked) it would be refused if it reached
	// an Authorize entry point, so it is used for the revocation's
	// observations only.
	return opctx.WithAuthContext(ctx, opctx.AuthContext{
		PrincipalID: principalID, ProfileID: acting, SessionID: sessionID,
	}), nil
}

// logNoActor records a revocation that could not be observed because the
// principal holds no non-ended profile. The revocation itself still happens.
func logNoActor(ctx context.Context, sessionID int64, reason string) {
	slog.WarnContext(ctx, "sessions: revocation not observed: principal has no acting profile",
		"session_id", sessionID, "reason", reason)
}

// isStepUpRequired reports whether err is, or wraps, the engine's assurance
// refusal: an action-required error with code users.step_up_required.
func isStepUpRequired(err error) bool {
	var action *apiresp.ActionRequiredError
	return errors.As(err, &action) && action.Action().Code == useraction.StepUpRequired.Code
}
