package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// dummyPasswordHash is a valid argon2id PHC string verified when no password
// credential matches, so a miss costs the same time as a wrong password.
const dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// dummySecretHash is compared when no API key matches a client id, so a miss
// costs the same as a wrong secret.
var dummySecretHash = HashToken("dummy-client-secret")

// ResolveApp maps an app slug to its id. "" is no app (0). An unknown or
// archived app is an invalid-input error on field "app".
func (s *SessionService) ResolveApp(ctx context.Context, slug string) (int64, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return 0, nil
	}
	app, err := s.coreQ.GetAppBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && app.ArchivedAt != nil) {
		return 0, invalidField("app", "users.app_unknown", "unknown app")
	}
	if err != nil {
		return 0, fmt.Errorf("sessions: resolve app: %w", err)
	}
	return app.ID, nil
}

// ---------------------------------------------------------------------------
// password and email-code sign-in
// ---------------------------------------------------------------------------

// LoginInput is the input of LoginWithPassword and LoginWithEmailCode.
type LoginInput struct {
	Email string
	// Password (LoginWithPassword) or Code (LoginWithEmailCode).
	Password string
	Code     string
	// AppSlug is the optional app to sign in to.
	AppSlug string
	// ProfileUUID optionally names the profile to land on.
	ProfileUUID uuid.UUID
}

// LoginWithPassword signs in with an email identifier and the password bound
// to it, and starts a session (StartSessionInTx). Every credential failure
// is ErrInvalidCredentials and costs one password verification. The
// credential's last_used_at is touched.
func (s *SessionService) LoginWithPassword(ctx context.Context, in LoginInput) (TokenResponse, error) {
	canon := CanonicalizeEmail(in.Email)
	row, err := s.q.GetActivePasswordLogin(ctx, usersdb.GetActivePasswordLoginParams{
		Kind: IdentifierKindEmail, ValueCanon: canon,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = localauth.VerifyPassword(in.Password, dummyPasswordHash)
		return TokenResponse{}, ErrInvalidCredentials
	}
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sessions: look up password: %w", err)
	}
	ok, err := localauth.VerifyPassword(in.Password, row.Hash)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sessions: verify password: %w", err)
	}
	if !ok {
		return TokenResponse{}, ErrInvalidCredentials
	}
	return s.signIn(ctx, row.Credential, in)
}

// LoginWithEmailCode signs in with an email identifier and a code previously
// sent by RequestLoginCode, through the identifier's email_otp credential.
// Every failure (unknown email, no email_otp credential, wrong or exhausted
// code) is ErrInvalidCredentials; the code's attempt counter is the
// ChallengeService's.
func (s *SessionService) LoginWithEmailCode(ctx context.Context, in LoginInput) (TokenResponse, error) {
	identifier, otp, err := s.emailOTPFor(ctx, CanonicalizeEmail(in.Email))
	if err != nil {
		return TokenResponse{}, err
	}
	if identifier.ID == 0 {
		burnCompare(in.Code)
		return TokenResponse{}, ErrInvalidCredentials
	}
	if _, err := s.challenges.VerifyCode(ctx, VerifyCodeInput{
		Kind:    ChallengeEmailCode,
		Subject: Subject{PrincipalID: identifier.UserID, IdentifierID: identifier.ID},
		Code:    in.Code,
	}); err != nil {
		if errors.Is(err, ErrInvalidCode) {
			return TokenResponse{}, ErrInvalidCredentials
		}
		return TokenResponse{}, err
	}
	return s.signIn(ctx, otp, in)
}

// LoginWithDeviceToken signs a guest back in with the device token its anon
// credential was issued with (POST /v1/auth/anonymous with device_token). The
// token must belong to an active anon credential registered for deviceID;
// anything else is ErrInvalidCredentials.
func (s *SessionService) LoginWithDeviceToken(ctx context.Context, deviceID, token, appSlug string) (TokenResponse, error) {
	row, err := s.q.GetActiveAnonByTokenHash(ctx, HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenResponse{}, ErrInvalidCredentials
	}
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sessions: look up device token: %w", err)
	}
	if row.DeviceID != strings.TrimSpace(deviceID) {
		return TokenResponse{}, ErrInvalidCredentials
	}
	return s.signIn(ctx, row.Credential, LoginInput{AppSlug: appSlug})
}

// emailOTPFor returns the email identifier with value canon and its active
// email_otp credential. Both are zero when either is missing.
func (s *SessionService) emailOTPFor(ctx context.Context, canon string) (usersdb.UserIdentifier, usersdb.Credential, error) {
	if canon == "" {
		return usersdb.UserIdentifier{}, usersdb.Credential{}, nil
	}
	identifier, err := s.q.GetIdentifierByKindValue(ctx, usersdb.GetIdentifierByKindValueParams{
		Kind: IdentifierKindEmail, ValueCanon: canon,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return usersdb.UserIdentifier{}, usersdb.Credential{}, nil
	}
	if err != nil {
		return usersdb.UserIdentifier{}, usersdb.Credential{}, fmt.Errorf("sessions: look up identifier: %w", err)
	}
	otp, err := s.q.GetActiveEmailOTPByIdentifier(ctx, identifier.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return usersdb.UserIdentifier{}, usersdb.Credential{}, nil
	}
	if err != nil {
		return usersdb.UserIdentifier{}, usersdb.Credential{}, fmt.Errorf("sessions: look up email_otp: %w", err)
	}
	return identifier, otp.Credential, nil
}

// signIn starts a session for a verified credential in its own transaction.
func (s *SessionService) signIn(ctx context.Context, cred usersdb.Credential, in LoginInput) (TokenResponse, error) {
	appID, err := s.ResolveApp(ctx, in.AppSlug)
	if err != nil {
		return TokenResponse{}, err
	}
	var resp TokenResponse
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		resp, err = s.StartSessionInTx(ctx, tx, StartSessionInput{
			PrincipalID:                cred.PrincipalID,
			AppID:                      appID,
			Factors:                    []Factor{{CredentialID: cred.ID}},
			CredentialDefaultProfileID: cred.DefaultProfileID.Int64,
			RequestedProfileUUID:       in.ProfileUUID,
		})
		if err != nil {
			return err
		}
		if err := s.usersQ(tx).TouchCredentialLastUsed(ctx, cred.ID); err != nil {
			return fmt.Errorf("sessions: touch credential: %w", err)
		}
		return nil
	})
	if err != nil {
		return TokenResponse{}, err
	}
	return resp, nil
}

// RequestLoginCode mints an email_code challenge for the email identifier and
// emails it, when the identifier exists and has an active email_otp
// credential (decision U12: only a verified email has one). An unknown email,
// a missing credential and a request inside the cooldown are silently
// skipped (nil), so callers can answer uniformly; only a fault is an error.
func (s *SessionService) RequestLoginCode(ctx context.Context, email string) error {
	canon := CanonicalizeEmail(email)
	identifier, _, err := s.emailOTPFor(ctx, canon)
	if err != nil || identifier.ID == 0 {
		return err
	}
	var issued IssuedChallenge
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		issued, err = s.challenges.IssueCodeInTx(ctx, tx, IssueCodeInput{
			Kind:    ChallengeEmailCode,
			Subject: Subject{PrincipalID: identifier.UserID, IdentifierID: identifier.ID},
		})
		return err
	})
	if errors.Is(err, ErrChallengeCooldown) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.challenges.DeliverCode(ctx, identifier.ValueCanon, ChallengeEmailCode, issued.Code)
}

// ---------------------------------------------------------------------------
// refresh
// ---------------------------------------------------------------------------

// Refresh rotates a refresh token (grant_type=refresh_token) and issues an
// access token acting as profileUUID, re-running the profile's availability
// (decision U6). The new refresh token stays in the presented token's
// lineage and expires AUTH_SESSION_IDLE_TIMEOUT from now.
//
// Errors: ErrRefreshReuse (after revoking the whole session with reason
// refresh_reuse, committed) when the token was already used; ErrSessionRevoked
// for an unknown or expired token or a session that is no longer live; the
// profile's unavailability (ProfileUnavailable, ErrAppLoginNotGranted), in
// which case the rotation is rolled back and the token stays usable.
func (s *SessionService) Refresh(ctx context.Context, refreshToken string, profileUUID uuid.UUID) (TokenResponse, error) {
	if refreshToken == "" {
		return TokenResponse{}, ErrSessionRevoked
	}
	hash := HashToken(refreshToken)
	var (
		resp  TokenResponse
		reuse bool
	)
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		q := s.usersQ(tx)
		used, err := q.UseRefreshToken(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			prior, err := q.GetRefreshTokenByHash(ctx, hash)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSessionRevoked
			}
			if err != nil {
				return fmt.Errorf("sessions: look up refresh token: %w", err)
			}
			if prior.UsedAt == nil {
				return ErrSessionRevoked // expired
			}
			reuse = true
			return s.revokeForReuse(ctx, tx, q, prior.SessionID)
		}
		if err != nil {
			return fmt.Errorf("sessions: use refresh token: %w", err)
		}

		sess, err := q.GetSessionByID(ctx, used.SessionID)
		if err != nil {
			return fmt.Errorf("sessions: load session: %w", err)
		}
		if !s.live(sess) {
			return ErrSessionRevoked
		}
		sc, err := s.loadSessionContext(ctx, tx, sess)
		if err != nil {
			return err
		}
		views, err := s.sessionViews(ctx, tx, sc)
		if err != nil {
			return err
		}
		profile, ok := findView(views, profileUUID)
		if !ok {
			return ProfileUnavailable("not_held")
		}
		if !profile.Available {
			return unavailableError(profile)
		}

		next, err := s.mintRefreshInTx(ctx, q, sess, used.LineageUuid)
		if err != nil {
			return err
		}
		if err := q.SetRefreshTokenReplacedBy(ctx, usersdb.SetRefreshTokenReplacedByParams{
			ID: used.ID, ReplacedByID: pgtype.Int8{Int64: next.row.ID, Valid: true},
		}); err != nil {
			return fmt.Errorf("sessions: link refresh token: %w", err)
		}
		if err := q.TouchSession(ctx, sess.ID); err != nil {
			return fmt.Errorf("sessions: touch session: %w", err)
		}
		resp, err = s.tokenResponse(sc, &profile, views, &next.token)
		return err
	})
	if reuse && err == nil {
		return TokenResponse{}, ErrRefreshReuse
	}
	if err != nil {
		return TokenResponse{}, err
	}
	return resp, nil
}

// revokeForReuse revokes the session of a reused refresh token with reason
// refresh_reuse. The flow has no AuthContext, so the observation runs as the
// section 10.1 acting profile of the session's principal.
func (s *SessionService) revokeForReuse(ctx context.Context, tx pgx.Tx, q usersdb.Querier, sessionID int64) error {
	sess, err := q.GetSessionByID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("sessions: load reused session: %w", err)
	}
	actCtx, err := withFallbackActor(ctx, tx, sess.PrincipalID, sess.ID)
	if err != nil {
		return err
	}
	slog.WarnContext(ctx, "sessions: refresh token reuse; revoking session", "session_uuid", sess.Uuid.String())
	return s.revokeSessionInTx(actCtx, tx, q, sess.ID, SessionRevokeRefreshReuse)
}

// ---------------------------------------------------------------------------
// handoff
// ---------------------------------------------------------------------------

// ExchangeHandoff redeems a login_handoff code (decision U7) once: it
// consumes the challenge, checks its session is live, mints a refresh lineage
// for the tab and runs the landing chain (the session's factor credential's
// default profile, the user's default, the personal profile, the only
// available profile). The `login` observation was written when the session
// was created (CreateSessionInTx). Errors: ErrInvalidCredentials for an
// unknown, expired or spent code; ErrSessionRevoked when the session is no
// longer live.
func (s *SessionService) ExchangeHandoff(ctx context.Context, code string) (TokenResponse, error) {
	var resp TokenResponse
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		q := s.usersQ(tx)
		ch, err := s.challenges.ConsumeTokenInTx(ctx, tx, ChallengeLoginHandoff, code)
		if err != nil {
			if errors.Is(err, ErrInvalidCode) {
				return ErrInvalidCredentials
			}
			return err
		}
		if ch.SessionID == 0 {
			return ErrInvalidCredentials
		}
		sess, err := q.GetSessionByID(ctx, ch.SessionID)
		if err != nil {
			return fmt.Errorf("sessions: load session: %w", err)
		}
		if !s.live(sess) {
			return ErrSessionRevoked
		}
		sc, err := s.loadSessionContext(ctx, tx, sess)
		if err != nil {
			return err
		}
		user, err := q.GetUserByEntityID(ctx, sess.PrincipalID)
		if err != nil {
			return fmt.Errorf("sessions: load user: %w", err)
		}
		credDefault, err := factorDefaultProfile(ctx, q, sess.ID)
		if err != nil {
			return err
		}
		landed, views, err := s.land(ctx, tx, sc, landingInput{
			credentialDefaultProfileID: credDefault,
			userDefaultProfileID:       user.DefaultProfileID.Int64,
		})
		if err != nil {
			return err
		}
		if landed == nil && sc.inviteOnly && !anyAvailable(views) && anyGateLocked(views) {
			return ErrAppLoginNotGranted
		}
		refresh, err := s.mintRefreshInTx(ctx, q, sess, uuid.New())
		if err != nil {
			return err
		}
		resp, err = s.tokenResponse(sc, landed, views, &refresh.token)
		return err
	})
	if err != nil {
		return TokenResponse{}, err
	}
	return resp, nil
}

// factorDefaultProfile returns the default_profile_id of the session's first
// factor credential that has one, else 0.
func factorDefaultProfile(ctx context.Context, q usersdb.Querier, sessionID int64) (int64, error) {
	factors, err := q.ListSessionFactors(ctx, sessionID)
	if err != nil {
		return 0, fmt.Errorf("sessions: list factors: %w", err)
	}
	for _, f := range factors {
		cred, err := q.GetCredentialByID(ctx, f.SessionFactor.CredentialID)
		if err != nil {
			return 0, fmt.Errorf("sessions: load factor credential: %w", err)
		}
		if cred.DefaultProfileID.Valid {
			return cred.DefaultProfileID.Int64, nil
		}
	}
	return 0, nil
}

// ---------------------------------------------------------------------------
// client credentials
// ---------------------------------------------------------------------------

// ClientCredentialsInput is the input of ClientCredentials.
type ClientCredentialsInput struct {
	ClientID     string
	ClientSecret string
	// ProfileUUID optionally names the profile; otherwise the credential's
	// default profile, else the principal's personal profile.
	ProfileUUID uuid.UUID
	// AppSlug optionally names the token's audience app.
	AppSlug string
}

// ClientCredentials runs grant_type=client_credentials: it verifies an
// api_key credential, checks credential_profile_availability for the
// profile, and issues a `cid` token (AUTH_CLIENT_CREDENTIALS_TTL, amr
// ["swk"], acr the kind's base_acr). It creates no session and writes no
// observation. Errors: ErrInvalidCredentials for an unknown, revoked or
// expired client id or a wrong secret; the profile's unavailability
// (ProfileUnavailable).
func (s *SessionService) ClientCredentials(ctx context.Context, in ClientCredentialsInput) (TokenResponse, error) {
	if s.signer == nil {
		return TokenResponse{}, errors.New("sessions: no token signer configured")
	}
	row, err := s.q.GetActiveAPIKeyByClientID(ctx, in.ClientID)
	if errors.Is(err, pgx.ErrNoRows) {
		TokenMatchesHash(in.ClientSecret, dummySecretHash)
		return TokenResponse{}, ErrInvalidCredentials
	}
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sessions: look up client: %w", err)
	}
	if !TokenMatchesHash(in.ClientSecret, row.SecretHash) {
		return TokenResponse{}, ErrInvalidCredentials
	}
	cred := row.Credential

	audience := s.cfg.DefaultAudience
	if strings.TrimSpace(in.AppSlug) != "" {
		if _, err := s.ResolveApp(ctx, in.AppSlug); err != nil {
			return TokenResponse{}, err
		}
		audience = strings.TrimSpace(in.AppSlug)
	}

	views, err := loadProfileViews(ctx, s.pool, viewScope{principalID: cred.PrincipalID, credentialID: cred.ID}, nil)
	if err != nil {
		return TokenResponse{}, err
	}
	var (
		profile ProfileView
		ok      bool
	)
	switch {
	case in.ProfileUUID != uuid.Nil:
		profile, ok = findView(views, in.ProfileUUID)
	case cred.DefaultProfileID.Valid:
		profile, ok = findViewByID(views, cred.DefaultProfileID.Int64)
	default:
		for _, v := range views {
			if v.Kind == ProfileKindPersonal {
				profile, ok = v, true
				break
			}
		}
	}
	if !ok {
		return TokenResponse{}, ProfileUnavailable("not_held")
	}
	if !profile.Available {
		return TokenResponse{}, unavailableError(profile)
	}

	kind, err := s.q.GetCredentialKind(ctx, cred.Kind)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sessions: load credential kind: %w", err)
	}
	principal, err := s.coreQ.GetEntityByID(ctx, cred.PrincipalID)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sessions: load principal: %w", err)
	}
	token, exp, err := localauth.IssueClientCredentialsToken(s.signer, localauth.ClientCredentialsTokenParams{
		Issuer:         s.cfg.Issuer,
		Audience:       audience,
		PrincipalUUID:  principal.Uuid,
		CredentialUUID: cred.Uuid,
		ProfileUUID:    profile.UUID,
		ACR:            kind.BaseAcr,
		TTL:            s.cfg.ClientCredentialsTTL,
	}, s.now())
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sessions: issue client-credentials token: %w", err)
	}
	if err := s.q.TouchCredentialLastUsed(ctx, cred.ID); err != nil {
		return TokenResponse{}, fmt.Errorf("sessions: touch credential: %w", err)
	}
	return TokenResponse{
		AccessToken: &token,
		TokenType:   tokenTypeBearer,
		ExpiresIn:   int64(exp.Sub(s.now()).Round(time.Second) / time.Second),
		Profile:     &profile,
		Profiles:    []ProfileView{profile},
	}, nil
}

// ---------------------------------------------------------------------------
// authenticated session routes
// ---------------------------------------------------------------------------

// liveSession loads a live session by id (ErrSessionRevoked otherwise).
func (s *SessionService) liveSession(ctx context.Context, q usersdb.Querier, sessionID int64) (usersdb.Session, error) {
	if sessionID <= 0 {
		return usersdb.Session{}, ErrSessionRevoked
	}
	sess, err := q.GetSessionByID(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return usersdb.Session{}, ErrSessionRevoked
	}
	if err != nil {
		return usersdb.Session{}, fmt.Errorf("sessions: load session: %w", err)
	}
	if !s.live(sess) {
		return usersdb.Session{}, ErrSessionRevoked
	}
	return sess, nil
}

// SessionInfo is the current session, the profile its token acts as, and the
// principal's profiles with their availability.
type SessionInfo struct {
	Session  *SessionView  `json:"session"`
	Profile  *ProfileView  `json:"profile"`
	Profiles []ProfileView `json:"profiles"`
}

// Current returns the session and profile views for the caller's session
// (GET /v1/session). profileUUID is the profile the access token acts as.
func (s *SessionService) Current(ctx context.Context, sessionID int64, profileUUID uuid.UUID) (SessionInfo, error) {
	sess, err := s.liveSession(ctx, s.q, sessionID)
	if err != nil {
		return SessionInfo{}, err
	}
	sc, err := s.loadSessionContext(ctx, s.pool, sess)
	if err != nil {
		return SessionInfo{}, err
	}
	views, err := s.sessionViews(ctx, s.pool, sc)
	if err != nil {
		return SessionInfo{}, err
	}
	info := SessionInfo{Session: sessionView(sess), Profiles: views}
	if v, ok := findView(views, profileUUID); ok {
		info.Profile = &v
	}
	return info, nil
}

// Switch issues an access token for the same session acting as profileUUID
// (POST /v1/session/switch). It writes nothing: the active profile rides on
// the token. The profile must be available to the session (and admitted by
// an invite-only app); otherwise ProfileUnavailable or ErrAppLoginNotGranted.
func (s *SessionService) Switch(ctx context.Context, sessionID int64, profileUUID uuid.UUID) (TokenResponse, error) {
	sess, err := s.liveSession(ctx, s.q, sessionID)
	if err != nil {
		return TokenResponse{}, err
	}
	sc, err := s.loadSessionContext(ctx, s.pool, sess)
	if err != nil {
		return TokenResponse{}, err
	}
	views, err := s.sessionViews(ctx, s.pool, sc)
	if err != nil {
		return TokenResponse{}, err
	}
	profile, ok := findView(views, profileUUID)
	if !ok {
		return TokenResponse{}, ProfileUnavailable("not_held")
	}
	if !profile.Available {
		return TokenResponse{}, unavailableError(profile)
	}
	return s.tokenResponse(sc, &profile, views, nil)
}

// Fork mints a new refresh lineage for the caller's session (POST
// /v1/session/fork, a new tab) and returns a token response acting as the
// caller's current profile, which the authentication middleware found
// available on this request. No observation.
func (s *SessionService) Fork(ctx context.Context, sessionID int64, profileUUID uuid.UUID) (TokenResponse, error) {
	var resp TokenResponse
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		q := s.usersQ(tx)
		sess, err := s.liveSession(ctx, q, sessionID)
		if err != nil {
			return err
		}
		if sess.ImpersonatorSessionID.Valid {
			// An impersonation token carries no refresh token and is
			// renewed through /v1/impersonations, not refreshed.
			return ErrSessionRevoked
		}
		sc, err := s.loadSessionContext(ctx, tx, sess)
		if err != nil {
			return err
		}
		views, err := s.sessionViews(ctx, tx, sc)
		if err != nil {
			return err
		}
		profile, ok := findView(views, profileUUID)
		if !ok {
			return ProfileUnavailable("not_held")
		}
		if !profile.Available {
			return unavailableError(profile)
		}
		refresh, err := s.mintRefreshInTx(ctx, q, sess, uuid.New())
		if err != nil {
			return err
		}
		resp, err = s.tokenResponse(sc, &profile, views, &refresh.token)
		return err
	})
	if err != nil {
		return TokenResponse{}, err
	}
	return resp, nil
}

// Logout revokes the caller's session with reason logout (and its
// impersonation children with parent_revoked), writing a `revoke` `session`
// observation for each. ctx carries the caller's AuthContext.
func (s *SessionService) Logout(ctx context.Context, sessionID int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		return s.revokeSessionInTx(ctx, tx, s.usersQ(tx), sessionID, SessionRevokeLogout)
	})
}

// ---------------------------------------------------------------------------
// step-up
// ---------------------------------------------------------------------------

// stepUpSession loads the caller's session for a step-up; an impersonation
// session cannot step up (its factors are the impersonated holder's).
func (s *SessionService) stepUpSession(ctx context.Context, sessionID int64) (usersdb.Session, error) {
	sess, err := s.liveSession(ctx, s.q, sessionID)
	if err != nil {
		return usersdb.Session{}, err
	}
	if sess.ImpersonatorSessionID.Valid {
		return usersdb.Session{}, ErrStepUpRequired
	}
	return sess, nil
}

// StepUpPassword re-verifies a password of the session's principal (U8): the
// password credential is added (or re-verified) as a factor, acr is
// recomputed, auth_time is set to now, an `update` `session` observation is
// written, and a fresh access token acting as profileUUID is returned (no
// refresh token). A wrong password, or a principal without a password, is
// ErrStepUpRequired. ctx carries the caller's AuthContext.
func (s *SessionService) StepUpPassword(ctx context.Context, sessionID int64, profileUUID uuid.UUID, password string) (TokenResponse, error) {
	sess, err := s.stepUpSession(ctx, sessionID)
	if err != nil {
		return TokenResponse{}, err
	}
	passwords, err := s.q.ListActivePasswordsByPrincipal(ctx, sess.PrincipalID)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("sessions: list passwords: %w", err)
	}
	if len(passwords) == 0 {
		_, _ = localauth.VerifyPassword(password, dummyPasswordHash)
		return TokenResponse{}, ErrStepUpRequired
	}
	var matched int64
	for _, p := range passwords {
		ok, err := localauth.VerifyPassword(password, p.Hash)
		if err != nil {
			return TokenResponse{}, fmt.Errorf("sessions: verify password: %w", err)
		}
		if ok {
			matched = p.Credential.ID
			break
		}
	}
	if matched == 0 {
		return TokenResponse{}, ErrStepUpRequired
	}
	return s.addFactor(ctx, sess.ID, matched, profileUUID)
}

// stepUpIdentifier resolves the email identifier a step-up code goes to: the
// given email when it is one of the principal's identifiers, else the
// principal's primary email. It returns the identifier and its active
// email_otp credential, or ErrStepUpRequired when there is none.
func (s *SessionService) stepUpIdentifier(ctx context.Context, principalID int64, email string) (usersdb.UserIdentifier, usersdb.Credential, error) {
	var (
		identifier usersdb.UserIdentifier
		err        error
	)
	if canon := CanonicalizeEmail(email); canon != "" {
		identifier, err = s.q.GetIdentifierByKindValue(ctx, usersdb.GetIdentifierByKindValueParams{
			Kind: IdentifierKindEmail, ValueCanon: canon,
		})
	} else {
		identifier, err = s.q.GetPrimaryEmailByUser(ctx, principalID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return usersdb.UserIdentifier{}, usersdb.Credential{}, ErrStepUpRequired
	}
	if err != nil {
		return usersdb.UserIdentifier{}, usersdb.Credential{}, fmt.Errorf("sessions: look up identifier: %w", err)
	}
	if identifier.UserID != principalID {
		return usersdb.UserIdentifier{}, usersdb.Credential{}, ErrStepUpRequired
	}
	otp, err := s.q.GetActiveEmailOTPByIdentifier(ctx, identifier.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return usersdb.UserIdentifier{}, usersdb.Credential{}, ErrStepUpRequired
	}
	if err != nil {
		return usersdb.UserIdentifier{}, usersdb.Credential{}, fmt.Errorf("sessions: look up email_otp: %w", err)
	}
	return identifier, otp.Credential, nil
}

// StepUpEmailCodeRequest mints a step_up code bound to the session and the
// email identifier (email, else the primary email) and emails it. A request
// inside the cooldown is silently skipped. No email_otp credential for the
// identifier is ErrStepUpRequired.
func (s *SessionService) StepUpEmailCodeRequest(ctx context.Context, sessionID int64, email string) error {
	sess, err := s.stepUpSession(ctx, sessionID)
	if err != nil {
		return err
	}
	identifier, _, err := s.stepUpIdentifier(ctx, sess.PrincipalID, email)
	if err != nil {
		return err
	}
	var issued IssuedChallenge
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		issued, err = s.challenges.IssueCodeInTx(ctx, tx, IssueCodeInput{
			Kind:    ChallengeStepUp,
			Subject: Subject{PrincipalID: sess.PrincipalID, IdentifierID: identifier.ID, SessionID: sess.ID},
		})
		return err
	})
	if errors.Is(err, ErrChallengeCooldown) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.challenges.DeliverCode(ctx, identifier.ValueCanon, ChallengeStepUp, issued.Code)
}

// StepUpEmailCodeVerify verifies a step_up code sent by
// StepUpEmailCodeRequest and adds the identifier's email_otp credential as a
// factor, as StepUpPassword does. A wrong, expired or exhausted code is
// ErrStepUpRequired.
func (s *SessionService) StepUpEmailCodeVerify(ctx context.Context, sessionID int64, profileUUID uuid.UUID, email, code string) (TokenResponse, error) {
	sess, err := s.stepUpSession(ctx, sessionID)
	if err != nil {
		return TokenResponse{}, err
	}
	identifier, otp, err := s.stepUpIdentifier(ctx, sess.PrincipalID, email)
	if err != nil {
		if errors.Is(err, ErrStepUpRequired) {
			burnCompare(code)
		}
		return TokenResponse{}, err
	}
	if _, err := s.challenges.VerifyCode(ctx, VerifyCodeInput{
		Kind:    ChallengeStepUp,
		Subject: Subject{PrincipalID: sess.PrincipalID, IdentifierID: identifier.ID, SessionID: sess.ID},
		Code:    code,
	}); err != nil {
		if errors.Is(err, ErrInvalidCode) {
			return TokenResponse{}, ErrStepUpRequired
		}
		return TokenResponse{}, err
	}
	return s.addFactor(ctx, sess.ID, otp.ID, profileUUID)
}

// AddFactorInTx adds (or re-verifies) a verified credential as a factor of
// the session (decision U8), recomputes acr and amr, sets auth_time to now
// and writes the `update` `session` observation. It does not commit. Task
// 006's OIDC step-up calls it with the oidc credential and the IdP's asserted
// acr. ctx must carry an AuthContext with an acting profile.
func (s *SessionService) AddFactorInTx(ctx context.Context, tx pgx.Tx, sessionID int64, f Factor) (usersdb.Session, error) {
	q := s.usersQ(tx)
	before, err := s.liveSession(ctx, q, sessionID)
	if err != nil {
		return usersdb.Session{}, err
	}
	if _, err := q.UpsertSessionFactor(ctx, usersdb.UpsertSessionFactorParams{
		SessionID: sessionID, CredentialID: f.CredentialID, AssertedAcr: assertedACR(f.AssertedACR),
	}); err != nil {
		return usersdb.Session{}, fmt.Errorf("sessions: add factor: %w", err)
	}
	after, err := s.refreshAssurance(ctx, q, sessionID, s.now().UTC())
	if err != nil {
		return usersdb.Session{}, err
	}
	if err := observeInTx(ctx, s.obs, tx, opUpdate, resourceSession, after.PrincipalID, sessionAudit(before), sessionAudit(after)); err != nil {
		return usersdb.Session{}, err
	}
	return after, nil
}

// addFactor runs AddFactorInTx in its own transaction and returns a fresh
// access token acting as profileUUID (no refresh token).
func (s *SessionService) addFactor(ctx context.Context, sessionID, credentialID int64, profileUUID uuid.UUID) (TokenResponse, error) {
	var resp TokenResponse
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		sess, err := s.AddFactorInTx(ctx, tx, sessionID, Factor{CredentialID: credentialID})
		if err != nil {
			return err
		}
		resp, err = s.TokenForSessionInTx(ctx, tx, sess, profileUUID)
		return err
	})
	if err != nil {
		return TokenResponse{}, err
	}
	return resp, nil
}

// TokenForSessionInTx returns a token response (no refresh token) for the
// session acting as profileUUID, which must be available to it. Task 006's
// OIDC step-up uses it after AddFactorInTx.
func (s *SessionService) TokenForSessionInTx(ctx context.Context, tx pgx.Tx, sess usersdb.Session, profileUUID uuid.UUID) (TokenResponse, error) {
	sc, err := s.loadSessionContext(ctx, tx, sess)
	if err != nil {
		return TokenResponse{}, err
	}
	views, err := s.sessionViews(ctx, tx, sc)
	if err != nil {
		return TokenResponse{}, err
	}
	profile, ok := findView(views, profileUUID)
	if !ok {
		return TokenResponse{}, ProfileUnavailable("not_held")
	}
	if !profile.Available {
		return TokenResponse{}, unavailableError(profile)
	}
	return s.tokenResponse(sc, &profile, views, nil)
}

// ---------------------------------------------------------------------------
// session list and revocation (self-service and principal administration)
// ---------------------------------------------------------------------------

// SessionSummary is one live session of a principal.
type SessionSummary struct {
	ID            int64     `json:"-"`
	UUID          uuid.UUID `json:"uuid"`
	AppID         int64     `json:"-"`
	ACR           int16     `json:"acr"`
	AMR           []string  `json:"amr"`
	AuthTime      time.Time `json:"auth_time"`
	CreatedAt     time.Time `json:"created_at"`
	LastSeenAt    time.Time `json:"last_seen_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Impersonating bool      `json:"impersonating"`
}

// ListSessions returns the principal's live (unrevoked, unexpired) sessions,
// most recently seen first. It does not authorize.
func (s *SessionService) ListSessions(ctx context.Context, principalID int64) ([]SessionSummary, error) {
	rows, err := s.q.ListLiveSessionsByPrincipal(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("sessions: list sessions: %w", err)
	}
	out := make([]SessionSummary, len(rows))
	for i, r := range rows {
		amr := r.Amr
		if amr == nil {
			amr = []string{}
		}
		out[i] = SessionSummary{
			ID: r.ID, UUID: r.Uuid, AppID: r.AppID.Int64, ACR: r.Acr, AMR: amr,
			AuthTime: r.AuthTime.Time.UTC(), CreatedAt: r.CreatedAt.Time.UTC(),
			LastSeenAt: r.LastSeenAt.Time.UTC(), ExpiresAt: r.ExpiresAt.Time.UTC(),
			Impersonating: r.ImpersonatorSessionID.Valid,
		}
	}
	return out, nil
}

// RevokeSessionInTx revokes one session of principalID, addressed by uuid,
// with reason (a sessions.revoke_reason value, typically logout for
// self-service or admin). A session that does not exist, belongs to another
// principal or is already revoked is apiresp.ErrNotFound (masking). Its
// impersonation children end with parent_revoked. It does not authorize and
// does not commit; ctx must carry an AuthContext with an acting profile.
func (s *SessionService) RevokeSessionInTx(ctx context.Context, tx pgx.Tx, principalID int64, sessionUUID uuid.UUID, reason string) error {
	if !validSessionRevokeReasons[reason] {
		return invalidField("reason", "users.revoke_reason_invalid", "unsupported revoke reason")
	}
	q := s.usersQ(tx)
	sess, err := q.GetSessionByUUID(ctx, sessionUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return apiresp.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("sessions: load session: %w", err)
	}
	if sess.PrincipalID != principalID || sess.RevokedAt != nil {
		return apiresp.ErrNotFound
	}
	return s.revokeSessionInTx(ctx, tx, q, sess.ID, reason)
}

// RevokePrincipalSessionsInTx revokes every unrevoked session of the
// principal with reason (principal_disabled when task 007 disables a
// principal) and ends their impersonation children, writing a `revoke`
// `session` observation for each. It returns how many sessions it revoked.
// It does not authorize and does not commit; ctx must carry an AuthContext
// with an acting profile.
func (s *SessionService) RevokePrincipalSessionsInTx(ctx context.Context, tx pgx.Tx, principalID int64, reason string) (int, error) {
	if !validSessionRevokeReasons[reason] {
		return 0, invalidField("reason", "users.revoke_reason_invalid", "unsupported revoke reason")
	}
	q := s.usersQ(tx)
	revoked, err := q.RevokeSessionsByPrincipal(ctx, usersdb.RevokeSessionsByPrincipalParams{
		PrincipalID: principalID, RevokeReason: pgtype.Text{String: reason, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("sessions: revoke principal sessions: %w", err)
	}
	if err := s.afterRevoke(ctx, tx, q, revoked); err != nil {
		return 0, err
	}
	return len(revoked), nil
}
