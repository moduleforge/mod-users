package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// The access token is a carrier for a server-side session (claim "sid") or a
// non-interactive credential (claim "cid"); it is EdDSA-signed and never
// authoritative on its own. Every request re-checks the session or credential
// in the database (Authenticator), which supplies the authoritative acr and
// auth_time. Claim set, per API surface section 5 (binding for offline
// verifiers):
//
//	iss        LOCAL_ISSUER
//	aud        the session app's slug, else AUTH_DEFAULT_AUDIENCE (client
//	           credentials: AUTH_DEFAULT_AUDIENCE or the requested app slug)
//	sub        principal uuid (under impersonation: the target holder's)
//	sid | cid  session uuid | credential uuid (exactly one)
//	prof       acting profile uuid
//	acr        "1", "2" or "3"
//	amr        factor amr values (client credentials: ["swk"])
//	auth_time  sessions.auth_time (client credentials: issue time)
//	act        {"sub": impersonator principal uuid}, only when impersonating
//	iat, exp   exp = min(iat + AUTH_ACCESS_TOKEN_TTL, sessions.expires_at)
//	           (client credentials: iat + AUTH_CLIENT_CREDENTIALS_TTL)
//
// There are no email, roles, impersonation-subject or anonymity claims.

// Carrier claim names.
const (
	claimIssuer     = "iss"
	claimSubject    = "sub"
	claimAudience   = "aud"
	claimIssuedAt   = "iat"
	claimExpiresAt  = "exp"
	claimNotBefore  = "nbf"
	claimSessionID  = "sid"
	claimCredential = "cid"
	claimProfile    = "prof"
	claimACR        = "acr"
	claimAMR        = "amr"
	claimAuthTime   = "auth_time"
	claimActor      = "act"
)

// amrSoftwareKey is the amr value of a client-credentials token.
const amrSoftwareKey = "swk"

// SessionTokenParams describes an interactive access token for a session.
type SessionTokenParams struct {
	// Issuer is LOCAL_ISSUER.
	Issuer string
	// Audience is the session app's slug, else AUTH_DEFAULT_AUDIENCE.
	Audience string
	// PrincipalUUID is the session principal's uuid (under impersonation, the
	// target profile's holder).
	PrincipalUUID uuid.UUID
	// SessionUUID is sessions.uuid.
	SessionUUID uuid.UUID
	// ProfileUUID is the acting profile's uuid.
	ProfileUUID uuid.UUID
	// ACR is sessions.acr (1 to 3).
	ACR int16
	// AMR lists the session factors' amr values.
	AMR []string
	// AuthTime is sessions.auth_time.
	AuthTime time.Time
	// ImpersonatorPrincipalUUID is the impersonating administrator's own
	// principal uuid; uuid.Nil unless impersonating.
	ImpersonatorPrincipalUUID uuid.UUID
	// TTL is AUTH_ACCESS_TOKEN_TTL; it is capped at config.MaxAccessTokenTTL.
	TTL time.Duration
	// SessionExpiresAt is sessions.expires_at; the token never outlives it.
	SessionExpiresAt time.Time
}

// ClientCredentialsTokenParams describes a client-credentials access token.
type ClientCredentialsTokenParams struct {
	// Issuer is LOCAL_ISSUER.
	Issuer string
	// Audience is AUTH_DEFAULT_AUDIENCE or the requested app slug.
	Audience string
	// PrincipalUUID is the credential holder's uuid.
	PrincipalUUID uuid.UUID
	// CredentialUUID is credentials.uuid.
	CredentialUUID uuid.UUID
	// ProfileUUID is the credential's default profile, else the holder's
	// personal profile.
	ProfileUUID uuid.UUID
	// ACR is the credential kind's base_acr (1 to 3).
	ACR int16
	// TTL is AUTH_CLIENT_CREDENTIALS_TTL.
	TTL time.Duration
}

// IssueSessionToken signs an interactive access token for a session and
// returns it with its expiry. exp is min(now + TTL, SessionExpiresAt), with
// TTL capped at config.MaxAccessTokenTTL; a session that has already expired
// yields an error rather than a token that is dead on arrival.
func IssueSessionToken(s *Signer, p SessionTokenParams, now time.Time) (string, time.Time, error) {
	if err := validateCarrierCommon(p.Issuer, p.Audience, p.PrincipalUUID, p.ProfileUUID, p.ACR); err != nil {
		return "", time.Time{}, err
	}
	if p.SessionUUID == uuid.Nil {
		return "", time.Time{}, errors.New("carrier: session uuid is required")
	}
	if p.AuthTime.IsZero() {
		return "", time.Time{}, errors.New("carrier: auth_time is required")
	}
	if p.TTL <= 0 {
		return "", time.Time{}, errors.New("carrier: ttl must be positive")
	}
	ttl := min(p.TTL, config.MaxAccessTokenTTL)

	now = now.Truncate(time.Second)
	exp := now.Add(ttl)
	if !p.SessionExpiresAt.IsZero() && p.SessionExpiresAt.Before(exp) {
		exp = p.SessionExpiresAt.Truncate(time.Second)
	}
	if !exp.After(now) {
		return "", time.Time{}, errors.New("carrier: session has expired")
	}

	amr := p.AMR
	if amr == nil {
		amr = []string{}
	}
	claims := jwt.MapClaims{
		claimIssuer:    p.Issuer,
		claimAudience:  p.Audience,
		claimSubject:   p.PrincipalUUID.String(),
		claimSessionID: p.SessionUUID.String(),
		claimProfile:   p.ProfileUUID.String(),
		claimACR:       strconv.Itoa(int(p.ACR)),
		claimAMR:       amr,
		claimAuthTime:  p.AuthTime.Unix(),
		claimIssuedAt:  now.Unix(),
		claimExpiresAt: exp.Unix(),
	}
	if p.ImpersonatorPrincipalUUID != uuid.Nil {
		claims[claimActor] = map[string]any{claimSubject: p.ImpersonatorPrincipalUUID.String()}
	}

	signed, err := s.Sign(claims)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("carrier: sign session token: %w", err)
	}
	return signed, exp, nil
}

// IssueClientCredentialsToken signs a client-credentials access token and
// returns it with its expiry (now + TTL). auth_time is the issue time and amr
// is ["swk"].
func IssueClientCredentialsToken(s *Signer, p ClientCredentialsTokenParams, now time.Time) (string, time.Time, error) {
	if err := validateCarrierCommon(p.Issuer, p.Audience, p.PrincipalUUID, p.ProfileUUID, p.ACR); err != nil {
		return "", time.Time{}, err
	}
	if p.CredentialUUID == uuid.Nil {
		return "", time.Time{}, errors.New("carrier: credential uuid is required")
	}
	if p.TTL <= 0 {
		return "", time.Time{}, errors.New("carrier: ttl must be positive")
	}

	now = now.Truncate(time.Second)
	exp := now.Add(p.TTL)
	claims := jwt.MapClaims{
		claimIssuer:     p.Issuer,
		claimAudience:   p.Audience,
		claimSubject:    p.PrincipalUUID.String(),
		claimCredential: p.CredentialUUID.String(),
		claimProfile:    p.ProfileUUID.String(),
		claimACR:        strconv.Itoa(int(p.ACR)),
		claimAMR:        []string{amrSoftwareKey},
		claimAuthTime:   now.Unix(),
		claimIssuedAt:   now.Unix(),
		claimExpiresAt:  exp.Unix(),
	}

	signed, err := s.Sign(claims)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("carrier: sign client-credentials token: %w", err)
	}
	return signed, exp, nil
}

// audienceReservedClaims are the claims IssueAudienceToken sets itself or
// that would make an audience-bound token look like an access token; extra
// may not supply them.
var audienceReservedClaims = map[string]struct{}{
	claimIssuer: {}, claimSubject: {}, claimAudience: {}, claimIssuedAt: {},
	claimExpiresAt: {}, claimNotBefore: {}, claimSessionID: {}, claimCredential: {},
	claimProfile: {}, claimActor: {}, claimACR: {}, claimAMR: {}, claimAuthTime: {},
}

// audienceTokenTyp is the JWS "typ" header of an audience token. It separates
// audience tokens from access tokens, which share the signing key and issuer;
// the access-token Authenticator rejects it.
const audienceTokenTyp = "aud+jwt"

// IssueAudienceToken signs an audience-bound token for an offline verifier (a
// managed app acting as a relying party, a relay): iss, sub = principal uuid,
// aud, iat and exp = iat + ttl, plus the caller's extra claims. It carries no
// sid or cid, so it is never accepted as a bearer token by this module's own
// Authenticator. extra may not set any reserved claim (iss, sub, aud, iat,
// exp, nbf, sid, cid, prof, act, acr, amr, auth_time). The JWS typ header is
// "aud+jwt", which the access-token verifier rejects.
func IssueAudienceToken(s *Signer, issuer string, principalUUID uuid.UUID, audience string,
	ttl time.Duration, extra map[string]any,
) (string, error) {
	if issuer == "" {
		return "", errors.New("carrier: issuer is required")
	}
	if audience == "" {
		return "", errors.New("carrier: audience is required")
	}
	if principalUUID == uuid.Nil {
		return "", errors.New("carrier: principal uuid is required")
	}
	if ttl <= 0 {
		return "", errors.New("carrier: ttl must be positive")
	}

	claims := make(jwt.MapClaims, len(extra)+5)
	for k, v := range extra {
		if _, reserved := audienceReservedClaims[k]; reserved {
			return "", fmt.Errorf("carrier: extra claim %q is reserved", k)
		}
		claims[k] = v
	}
	now := time.Now().Truncate(time.Second)
	claims[claimIssuer] = issuer
	claims[claimSubject] = principalUUID.String()
	claims[claimAudience] = audience
	claims[claimIssuedAt] = now.Unix()
	claims[claimExpiresAt] = now.Add(ttl).Unix()

	signed, err := s.signTyped(claims, audienceTokenTyp)
	if err != nil {
		return "", fmt.Errorf("carrier: sign audience token: %w", err)
	}
	return signed, nil
}

func validateCarrierCommon(issuer, audience string, principalUUID, profileUUID uuid.UUID, acr int16) error {
	switch {
	case issuer == "":
		return errors.New("carrier: issuer is required")
	case audience == "":
		return errors.New("carrier: audience is required")
	case principalUUID == uuid.Nil:
		return errors.New("carrier: principal uuid is required")
	case profileUUID == uuid.Nil:
		return errors.New("carrier: profile uuid is required")
	case acr < 1 || acr > 3:
		return errors.New("carrier: acr must be 1, 2 or 3")
	}
	return nil
}

// carrier is a verified access token's claims, parsed. Exactly one of
// SessionUUID and CredentialUUID is non-nil.
type carrier struct {
	PrincipalUUID  uuid.UUID
	ProfileUUID    uuid.UUID
	SessionUUID    uuid.UUID
	CredentialUUID uuid.UUID
	Audience       []string
	AMR            []string
	AuthTime       time.Time
	IssuedAt       time.Time
}

// audienceClaim decodes a JWT "aud", which is either a string or an array of
// strings.
type audienceClaim []string

func (a *audienceClaim) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		*a = audienceClaim{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("invalid aud")
	}
	*a = many
	return nil
}

// carrierPayload is the wire shape verifyCarrier decodes.
type carrierPayload struct {
	Iss      string        `json:"iss"`
	Sub      string        `json:"sub"`
	Aud      audienceClaim `json:"aud"`
	Sid      string        `json:"sid"`
	Cid      string        `json:"cid"`
	Prof     string        `json:"prof"`
	AMR      []string      `json:"amr"`
	AuthTime *float64      `json:"auth_time"`
	Iat      *float64      `json:"iat"`
	Exp      *float64      `json:"exp"`
}

// verifyCarrier verifies a bearer access token: the EdDSA signature (pinned;
// see verifyLocalSignature), "iss", "exp", and the carrier shape (a uuid
// "sub" and "prof", exactly one of "sid" and "cid", a non-empty "aud"). The
// audience policy is applied by the caller (audiencePolicy.check), because
// the "registered app slug" arm needs the database. Errors are fixed strings
// that never echo token content.
func (v *Verifier) verifyCarrier(rawToken string, now time.Time) (*carrier, error) {
	if v == nil {
		return nil, errors.New("nil verifier")
	}
	payload, err := v.verifyLocalSignature(rawToken)
	if err != nil {
		return nil, err
	}
	if rawTokenTyp(rawToken) == audienceTokenTyp {
		return nil, errors.New("audience token not accepted")
	}
	var p carrierPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, errors.New("invalid jwt payload")
	}

	if p.Iss != v.localIssuer {
		return nil, errors.New("issuer mismatch")
	}
	if p.Exp == nil || !time.Unix(int64(*p.Exp), 0).After(now) {
		return nil, errors.New("token expired")
	}
	if len(p.Aud) == 0 {
		return nil, errors.New("missing aud")
	}

	c := &carrier{Audience: []string(p.Aud), AMR: p.AMR}
	if c.PrincipalUUID, err = uuid.Parse(p.Sub); err != nil {
		return nil, errors.New("invalid sub")
	}
	if c.ProfileUUID, err = uuid.Parse(p.Prof); err != nil {
		return nil, errors.New("invalid prof")
	}
	switch {
	case p.Sid != "" && p.Cid == "":
		if c.SessionUUID, err = uuid.Parse(p.Sid); err != nil {
			return nil, errors.New("invalid sid")
		}
	case p.Cid != "" && p.Sid == "":
		if c.CredentialUUID, err = uuid.Parse(p.Cid); err != nil {
			return nil, errors.New("invalid cid")
		}
	default:
		return nil, errors.New("token must carry exactly one of sid and cid")
	}
	if p.Iat != nil {
		c.IssuedAt = time.Unix(int64(*p.Iat), 0)
	}
	if p.AuthTime != nil {
		c.AuthTime = time.Unix(int64(*p.AuthTime), 0)
	} else {
		c.AuthTime = c.IssuedAt
	}
	return c, nil
}

// rawTokenTyp returns the JWS "typ" header of an already signature-verified
// token, or "" when absent or not a string.
func rawTokenTyp(rawToken string) string {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ""
	}
	var h struct {
		Typ string `json:"typ"`
	}
	if json.Unmarshal(b, &h) != nil {
		return ""
	}
	return h.Typ
}

// audiencePolicy is the bearer-token audience rule: AUTH_ACCEPTED_AUDIENCES
// when set; otherwise AUTH_DEFAULT_AUDIENCE plus the slug of any registered
// app.
type audiencePolicy struct {
	defaultAudience string
	accepted        map[string]struct{}
}

func newAudiencePolicy(cfg config.AuthConfig) audiencePolicy {
	p := audiencePolicy{defaultAudience: cfg.DefaultAudience}
	if len(cfg.AcceptedAudiences) > 0 {
		p.accepted = make(map[string]struct{}, len(cfg.AcceptedAudiences))
		for _, a := range cfg.AcceptedAudiences {
			p.accepted[a] = struct{}{}
		}
	}
	return p
}

// check applies the policy to a token's audiences. ok reports an offline
// decision; when ok is false and appSlugs is non-empty, the token is
// acceptable only if one of appSlugs is a registered app's slug, which the
// caller checks in the database. ok false with no appSlugs is a rejection.
func (p audiencePolicy) check(aud []string) (ok bool, appSlugs []string) {
	if p.accepted != nil {
		for _, a := range aud {
			if _, hit := p.accepted[a]; hit {
				return true, nil
			}
		}
		return false, nil
	}
	for _, a := range aud {
		if a != "" && a == p.defaultAudience {
			return true, nil
		}
	}
	for _, a := range aud {
		if a != "" {
			appSlugs = append(appSlugs, a)
		}
	}
	return false, appSlugs
}
