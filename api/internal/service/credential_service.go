package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/sshkey"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Credential kinds (mod_users.credential_kinds).
const (
	KindPassword = "password"
	KindEmailOTP = "email_otp"
	KindOIDC     = "oidc"
	KindSSHKey   = "ssh_key"
	KindAPIKey   = "api_key"
	KindAnon     = "anon"
)

// OpCredentials is the operation slug that guards credential administration
// of a principal (issuing API keys, operator-managed SSH keys). Until
// mod-authz seeds it, the production Authorizer's unknown-slug fallback admits
// only wildcard manage holders.
const OpCredentials = "credentials"

// Revoke reasons accepted for credentials (credentials.revoke_reason CHECK).
const (
	RevokeReasonUser          = "user"
	RevokeReasonAdmin         = "admin"
	RevokeReasonDeparture     = "departure"
	RevokeReasonSuperseded    = "superseded"
	RevokeReasonGuestUpgraded = "guest_upgraded"
	RevokeReasonExpired       = "expired"
)

var validRevokeReasons = map[string]bool{
	RevokeReasonUser: true, RevokeReasonAdmin: true, RevokeReasonDeparture: true,
	RevokeReasonSuperseded: true, RevokeReasonGuestUpgraded: true, RevokeReasonExpired: true,
}

// Bounds on caller-supplied credential material.
const (
	maxPasswordBytes = 1024
	maxDeviceIDBytes = 256
	// DefaultAnonTTL is the lifetime of an anon (guest device) credential
	// when the caller does not choose one.
	DefaultAnonTTL = 30 * 24 * time.Hour
)

// Credential is the service-layer view of a mod_users.credentials row. Ids of
// related rows are 0 when the column is NULL.
type Credential struct {
	ID                int64
	UUID              uuid.UUID
	PrincipalID       int64
	Kind              string
	Label             string
	ConfinedToPartyID int64
	DefaultProfileID  int64
	CreatedAt         time.Time
	LastUsedAt        *time.Time
	ExpiresAt         *time.Time
	RevokedAt         *time.Time
	RevokeReason      string
}

// Active reports whether the credential is unrevoked and unexpired at now.
func (c Credential) Active(now time.Time) bool {
	if c.RevokedAt != nil {
		return false
	}
	return c.ExpiresAt == nil || c.ExpiresAt.After(now)
}

func toCredential(row usersdb.Credential) Credential {
	return Credential{
		ID:                row.ID,
		UUID:              row.Uuid,
		PrincipalID:       row.PrincipalID,
		Kind:              row.Kind,
		Label:             row.Label,
		ConfinedToPartyID: row.ConfinedToPartyID.Int64,
		DefaultProfileID:  row.DefaultProfileID.Int64,
		CreatedAt:         row.CreatedAt.Time,
		LastUsedAt:        row.LastUsedAt,
		ExpiresAt:         row.ExpiresAt,
		RevokedAt:         row.RevokedAt,
		RevokeReason:      row.RevokeReason.String,
	}
}

// CredentialDetail is a Credential with the per-kind columns that are safe to
// show: never a password hash, an API secret hash or an anon token hash.
type CredentialDetail struct {
	Credential
	IdentifierID int64  // password, email_otp: the bound identifier (0 for an unbound password)
	ProviderID   string // oidc
	Issuer       string // oidc
	Subject      string // oidc
	EmailAtLink  string // oidc
	KeyType      string // ssh_key
	PublicKey    string // ssh_key
	Fingerprint  string // ssh_key
	ClientID     string // api_key
}

// CredentialService manages credentials: creation per kind, revocation, label
// and landing-profile updates and listing. Credentials are revoked, never
// deleted. Every write method takes the caller's transaction and does not
// commit; a write observation (op create, update or revoke on resource
// "credential", target the principal) runs in that transaction, so ctx must
// carry an AuthContext with an acting profile.
//
// Except IssueAPIKeyInTx (it authorizes the "credentials" operation on the
// principal), these methods do not authorize: the caller does. Self-service
// routes authorize on "session principal = target"; sign-up, sign-in and JIT
// flows are trusted code paths.
type CredentialService struct {
	q        usersdb.Querier
	coreQ    coredb.Querier
	az       coreAuthz.Authorizer
	obs      *observer.ObserverGroup
	hashPw   func(plain string) (string, error)
	newQ     func(pgx.Tx) usersdb.Querier
	newCoreQ func(pgx.Tx) coredb.Querier
	now      func() time.Time
}

// NewCredentialService constructs a CredentialService. hashPassword is
// typically auth.HashPassword (argon2id); it is injectable for tests. q and
// coreQ serve the read methods; writes use queriers bound to the caller's
// transaction.
func NewCredentialService(
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	hashPassword func(plain string) (string, error),
) *CredentialService {
	return &CredentialService{
		q:      q,
		coreQ:  coreQ,
		az:     az,
		obs:    obs,
		hashPw: hashPassword,
		now:    time.Now,
	}
}

func (s *CredentialService) usersQ(tx pgx.Tx) usersdb.Querier {
	if s.newQ != nil {
		return s.newQ(tx)
	}
	return usersdb.New(tx)
}

func (s *CredentialService) coreQuerier(tx pgx.Tx) coredb.Querier {
	if s.newCoreQ != nil {
		return s.newCoreQ(tx)
	}
	return coredb.New(tx)
}

// AuthorizeCredentials authorizes the "credentials" operation on the principal
// entity, the gate for administering another principal's credentials. It
// fails closed (apiresp.ErrForbidden) when the service has no authorizer.
func (s *CredentialService) AuthorizeCredentials(ctx context.Context, principalID int64) error {
	if s.az == nil {
		return apiresp.ErrForbidden
	}
	return s.az.Authorize(ctx, OpCredentials, &principalID)
}

// commonInput holds the columns shared by every credential kind.
type commonInput struct {
	principalID       int64
	kind              string
	label             string
	confinedToPartyID *int64
	defaultProfileID  *int64
	expiresAt         *time.Time
}

func (s *CredentialService) insertCommon(ctx context.Context, q usersdb.Querier, in commonInput) (usersdb.Credential, error) {
	row, err := q.CreateCredential(ctx, usersdb.CreateCredentialParams{
		PrincipalID:        in.principalID,
		Kind:               in.kind,
		Label:              in.label,
		ConfinedToPartyID:  nullableInt8(in.confinedToPartyID),
		DefaultProfileID:   nullableInt8(in.defaultProfileID),
		CreatedByProfileID: actingProfileParam(ctx),
		ExpiresAt:          in.expiresAt,
	})
	if err != nil {
		return usersdb.Credential{}, fmt.Errorf("credentials: insert %s credential: %w", in.kind, err)
	}
	return row, nil
}

// credentialAudit is the observer payload for a credential: identifiers and
// non-secret attributes only.
func credentialAudit(c usersdb.Credential, extra map[string]any) map[string]any {
	m := map[string]any{
		"uuid":  c.Uuid.String(),
		"kind":  c.Kind,
		"label": c.Label,
	}
	if c.ConfinedToPartyID.Valid {
		m["confined"] = true
	}
	if c.RevokeReason.Valid {
		m["revoke_reason"] = c.RevokeReason.String
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// normalizeLabel validates a credential label with the shared label rules
// (sshkey.NormalizeLabel): trimmed, at most 100 runes, no control or bidi
// formatting characters.
func normalizeLabel(label string) (string, error) {
	out, err := sshkey.NormalizeLabel(&label, "")
	if err != nil {
		switch {
		case errors.Is(err, sshkey.ErrLabelTooLong):
			return "", invalidField("label", "users.credential_label_too_long", "label is too long")
		case errors.Is(err, sshkey.ErrLabelInvalidChars):
			return "", invalidField("label", "users.credential_label_invalid", "label contains invalid characters")
		}
		return "", fmt.Errorf("credentials: normalize label: %w", err)
	}
	return out, nil
}

func (s *CredentialService) validateExpiry(expiresAt *time.Time) error {
	if expiresAt != nil && !expiresAt.After(s.now()) {
		return invalidField("expires_at", "users.expires_at_past", "expires_at must be in the future")
	}
	return nil
}

// ---------------------------------------------------------------------------
// password
// ---------------------------------------------------------------------------

// CreatePasswordInput is the input of CreatePasswordInTx.
type CreatePasswordInput struct {
	PrincipalID int64
	// IdentifierID binds the password to one of the principal's identifiers
	// (the one it logs in with); nil creates the principal's unbound password.
	IdentifierID      *int64
	Password          string
	Label             string
	DefaultProfileID  *int64
	ConfinedToPartyID *int64
}

// CreatePasswordInTx creates an active password credential (argon2id). At most
// one active password exists per identifier and one unbound password per
// principal (decision U12): a second one fails with ErrPasswordExists, and a
// caller replacing a password revokes the old credential first. The password
// must be 12 to 1024 bytes.
func (s *CredentialService) CreatePasswordInTx(ctx context.Context, tx pgx.Tx, in CreatePasswordInput) (Credential, error) {
	if len(in.Password) < passwordMinLength {
		return Credential{}, invalidField("password", "users.password_too_short", "password must be at least 12 characters")
	}
	if len(in.Password) > maxPasswordBytes {
		return Credential{}, invalidField("password", "users.password_too_long", "password is too long")
	}
	label, err := normalizeLabel(in.Label)
	if err != nil {
		return Credential{}, err
	}
	hash, err := s.hashPw(in.Password)
	if err != nil {
		return Credential{}, fmt.Errorf("credentials: hash password: %w", err)
	}

	q := s.usersQ(tx)
	row, err := s.insertCommon(ctx, q, commonInput{
		principalID:       in.PrincipalID,
		kind:              KindPassword,
		label:             label,
		confinedToPartyID: in.ConfinedToPartyID,
		defaultProfileID:  in.DefaultProfileID,
	})
	if err != nil {
		return Credential{}, err
	}
	if _, err := q.CreatePasswordCredential(ctx, usersdb.CreatePasswordCredentialParams{
		CredentialID: row.ID,
		IdentifierID: nullableInt8(in.IdentifierID),
		Hash:         hash,
	}); err != nil {
		if isPasswordExistsViolation(err) {
			return Credential{}, ErrPasswordExists
		}
		return Credential{}, fmt.Errorf("credentials: insert password: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opCreate, resourceCredential, in.PrincipalID, nil,
		credentialAudit(row, map[string]any{"bound": in.IdentifierID != nil})); err != nil {
		return Credential{}, err
	}
	return toCredential(row), nil
}

// SupersedePasswordInTx replaces the active password credential oldID with a
// new one built from in. In the caller's transaction it revokes the old
// credential with reason superseded, creates the replacement, and copies the
// old credential's party authorizations to it, so party profiles with
// credential rule 'authorized' stay available without re-authorization. Every
// password supersede path uses it; any failure leaves the caller to roll the
// transaction back, so the revoke, create and copy are atomic.
func (s *CredentialService) SupersedePasswordInTx(ctx context.Context, tx pgx.Tx, oldID int64, in CreatePasswordInput) (Credential, error) {
	if err := s.RevokeInTx(ctx, tx, oldID, RevokeReasonSuperseded); err != nil {
		return Credential{}, err
	}
	created, err := s.CreatePasswordInTx(ctx, tx, in)
	if err != nil {
		return Credential{}, err
	}
	if _, err := s.usersQ(tx).CopyCredentialPartyAuthorizations(ctx, usersdb.CopyCredentialPartyAuthorizationsParams{
		NewCredentialID: created.ID,
		OldCredentialID: oldID,
	}); err != nil {
		return Credential{}, fmt.Errorf("credentials: copy party authorizations: %w", err)
	}
	return created, nil
}

// ---------------------------------------------------------------------------
// email_otp
// ---------------------------------------------------------------------------

// EnsureEmailOTPInTx creates the active email_otp credential bound to the
// identifier unless one exists (decision U12: verifying an email identifier
// creates it). It reports whether it created one. The principal must not be a
// guest (the database refuses with MF102, surfaced as an internal error).
func (s *CredentialService) EnsureEmailOTPInTx(ctx context.Context, tx pgx.Tx, principalID, identifierID int64) (created bool, err error) {
	q := s.usersQ(tx)
	_, err = q.GetActiveEmailOTPByIdentifier(ctx, identifierID)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("credentials: look up email_otp: %w", err)
	}

	row, err := s.insertCommon(ctx, q, commonInput{principalID: principalID, kind: KindEmailOTP})
	if err != nil {
		return false, err
	}
	if _, err := q.CreateEmailOTPCredential(ctx, usersdb.CreateEmailOTPCredentialParams{
		CredentialID: row.ID,
		IdentifierID: identifierID,
	}); err != nil {
		return false, fmt.Errorf("credentials: insert email_otp: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opCreate, resourceCredential, principalID, nil, credentialAudit(row, nil)); err != nil {
		return false, err
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// ssh_key
// ---------------------------------------------------------------------------

// CreateSSHKeyInput is the input of CreateSSHKeyInTx.
type CreateSSHKeyInput struct {
	PrincipalID int64
	// PublicKey is one authorized_keys line, validated by sshkey.Parse.
	PublicKey string
	// Label overrides the key's comment when non-nil (sshkey.NormalizeLabel).
	Label             *string
	DefaultProfileID  *int64
	ConfinedToPartyID *int64
	ExpiresAt         *time.Time
}

// SSHKeyCredential is a created ssh_key credential.
type SSHKeyCredential struct {
	Credential
	KeyType     string
	PublicKey   string
	Fingerprint string
}

// CreateSSHKeyInTx registers an SSH public key for the principal. The key and
// label are validated first (sshkey.Parse, sshkey.NormalizeLabel; malformed
// input is an invalid-input error). A fingerprint already active on any
// credential fails with ErrSSHKeyInUse, enforced by the
// cred_ssh_key_active_fingerprint_uq index rather than a pre-check, so a
// concurrent registration cannot slip through.
func (s *CredentialService) CreateSSHKeyInTx(ctx context.Context, tx pgx.Tx, in CreateSSHKeyInput) (SSHKeyCredential, error) {
	parsed, err := sshkey.Parse(in.PublicKey)
	if err != nil {
		return SSHKeyCredential{}, mapSSHKeyParseError(err)
	}
	label, err := sshkey.NormalizeLabel(in.Label, parsed.Comment)
	if err != nil {
		return SSHKeyCredential{}, mapSSHKeyLabelError(err)
	}
	if err := s.validateExpiry(in.ExpiresAt); err != nil {
		return SSHKeyCredential{}, err
	}

	q := s.usersQ(tx)
	row, err := s.insertCommon(ctx, q, commonInput{
		principalID:       in.PrincipalID,
		kind:              KindSSHKey,
		label:             label,
		confinedToPartyID: in.ConfinedToPartyID,
		defaultProfileID:  in.DefaultProfileID,
		expiresAt:         in.ExpiresAt,
	})
	if err != nil {
		return SSHKeyCredential{}, err
	}
	if _, err := q.CreateSSHKeyCredential(ctx, usersdb.CreateSSHKeyCredentialParams{
		CredentialID:      row.ID,
		KeyType:           parsed.Type,
		PublicKey:         parsed.Canonical,
		FingerprintSha256: parsed.Fingerprint,
	}); err != nil {
		if mapped := mapUniqueViolation(err); errors.Is(mapped, ErrSSHKeyInUse) {
			return SSHKeyCredential{}, ErrSSHKeyInUse
		}
		return SSHKeyCredential{}, fmt.Errorf("credentials: insert ssh_key: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opCreate, resourceCredential, in.PrincipalID, nil,
		credentialAudit(row, map[string]any{"fingerprint": parsed.Fingerprint, "key_type": parsed.Type})); err != nil {
		return SSHKeyCredential{}, err
	}
	return SSHKeyCredential{
		Credential:  toCredential(row),
		KeyType:     parsed.Type,
		PublicKey:   parsed.Canonical,
		Fingerprint: parsed.Fingerprint,
	}, nil
}

// ---------------------------------------------------------------------------
// oidc
// ---------------------------------------------------------------------------

// LinkOIDCInput is the input of LinkOIDCInTx.
type LinkOIDCInput struct {
	PrincipalID       int64
	ProviderID        string // cred_oidc.provider_id (a configured or env provider id)
	Issuer            string
	Subject           string
	EmailAtLink       string // "" stores NULL
	Label             string
	ConfinedToPartyID *int64
}

// LinkedCredential is the result of LinkOIDCInTx.
type LinkedCredential struct {
	CredentialID   int64
	CredentialUUID uuid.UUID
}

// LinkOIDCInTx is the trusted creation primitive for `oidc` credentials: it is
// the only code path that inserts a cred_oidc row. Task 006's OIDC JIT and
// session-link paths and app-mfmanager's managed-app owner provisioning all
// call it.
//
// It performs NO authorization; the caller authorizes. It creates an active
// `oidc` credential, so the (issuer, subject) pair logs the principal in on the
// known-identity path. It never matches or links by email.
//
// Errors: ErrOIDCIdentityInUse (409 users.oidc_identity_in_use) when an active
// credential already holds (issuer, subject), enforced by the
// cred_oidc_active_issuer_subject_uq index. The principal must be a non-guest
// user; a credential invariant refusal (MF102: a guest, a service account or a
// system actor) is a caller bug and surfaces as an internal error, as for every
// unmapped invariant. It writes a `credential` `create` observation, so ctx
// must carry an AuthContext with an acting profile.
func (s *CredentialService) LinkOIDCInTx(ctx context.Context, tx pgx.Tx, in LinkOIDCInput) (LinkedCredential, error) {
	if strings.TrimSpace(in.ProviderID) == "" {
		return LinkedCredential{}, invalidField("provider_id", "users.provider_required", "provider_id is required")
	}
	if in.Issuer == "" || in.Subject == "" {
		return LinkedCredential{}, invalidField("subject", "users.oidc_identity_required", "issuer and subject are required")
	}
	label, err := normalizeLabel(in.Label)
	if err != nil {
		return LinkedCredential{}, err
	}

	q := s.usersQ(tx)
	row, err := s.insertCommon(ctx, q, commonInput{
		principalID:       in.PrincipalID,
		kind:              KindOIDC,
		label:             label,
		confinedToPartyID: in.ConfinedToPartyID,
	})
	if err != nil {
		return LinkedCredential{}, err
	}
	if _, err := q.CreateOIDCCredential(ctx, usersdb.CreateOIDCCredentialParams{
		CredentialID: row.ID,
		ProviderID:   in.ProviderID,
		Issuer:       in.Issuer,
		Subject:      in.Subject,
		EmailAtLink:  pgtype.Text{String: in.EmailAtLink, Valid: in.EmailAtLink != ""},
	}); err != nil {
		if mapped := mapUniqueViolation(err); errors.Is(mapped, ErrOIDCIdentityInUse) {
			return LinkedCredential{}, ErrOIDCIdentityInUse
		}
		return LinkedCredential{}, fmt.Errorf("credentials: insert oidc: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opCreate, resourceCredential, in.PrincipalID, nil,
		credentialAudit(row, map[string]any{"provider_id": in.ProviderID})); err != nil {
		return LinkedCredential{}, err
	}
	return LinkedCredential{CredentialID: row.ID, CredentialUUID: row.Uuid}, nil
}

// ---------------------------------------------------------------------------
// anon
// ---------------------------------------------------------------------------

// CreateAnonInput is the input of CreateAnonInTx.
type CreateAnonInput struct {
	PrincipalID int64
	// DeviceID is a stable device fingerprint supplied by the client.
	DeviceID string
	// TTL is the credential lifetime; zero means DefaultAnonTTL.
	TTL time.Duration
}

// CreatedAnon is a created anon credential with its one-time device token.
type CreatedAnon struct {
	Credential Credential
	// Token is the raw device token, returned only at creation; the database
	// keeps HashToken(Token).
	Token string
}

// CreateAnonInTx creates the guest `anon` credential and its device token. The
// principal must be a guest user (the database refuses anything else with
// MF102).
func (s *CredentialService) CreateAnonInTx(ctx context.Context, tx pgx.Tx, in CreateAnonInput) (CreatedAnon, error) {
	device := strings.TrimSpace(in.DeviceID)
	if device == "" {
		return CreatedAnon{}, invalidField("device_id", "users.device_id_required", "device_id is required")
	}
	if len(device) > maxDeviceIDBytes {
		return CreatedAnon{}, invalidField("device_id", "users.device_id_too_long", "device_id is too long")
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = DefaultAnonTTL
	}
	raw, err := randomHex(32)
	if err != nil {
		return CreatedAnon{}, fmt.Errorf("credentials: anon token: %w", err)
	}
	expires := s.now().Add(ttl).UTC()

	q := s.usersQ(tx)
	row, err := s.insertCommon(ctx, q, commonInput{principalID: in.PrincipalID, kind: KindAnon, expiresAt: &expires})
	if err != nil {
		return CreatedAnon{}, err
	}
	if _, err := q.CreateAnonCredential(ctx, usersdb.CreateAnonCredentialParams{
		CredentialID: row.ID,
		DeviceID:     device,
		TokenHash:    HashToken(raw),
	}); err != nil {
		return CreatedAnon{}, fmt.Errorf("credentials: insert anon: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opCreate, resourceCredential, in.PrincipalID, nil, credentialAudit(row, nil)); err != nil {
		return CreatedAnon{}, err
	}
	return CreatedAnon{Credential: toCredential(row), Token: raw}, nil
}

// ---------------------------------------------------------------------------
// api_key
// ---------------------------------------------------------------------------

// IssueAPIKeyInput is the input of IssueAPIKeyInTx.
type IssueAPIKeyInput struct {
	PrincipalID      int64
	Label            string
	DefaultProfileID *int64
	ExpiresAt        *time.Time
}

// IssuedAPIKey is an issued API key. ClientSecret is shown once; the database
// keeps only HashToken(ClientSecret) (verify with TokenMatchesHash).
type IssuedAPIKey struct {
	CredentialID   int64
	CredentialUUID uuid.UUID
	ClientID       string
	ClientSecret   string
}

const (
	apiKeyClientIDPrefix = "mfk_"
	apiKeySecretPrefix   = "mfs_"
)

// IssueAPIKeyInTx issues client credentials (client id plus secret) for a
// service account. It authorizes the "credentials" operation on the principal
// entity first (AuthorizeCredentials), then requires the principal to be a
// service account; any other principal is an invalid-input error. The HTTP
// routes and the owner path come in the engine phase.
func (s *CredentialService) IssueAPIKeyInTx(ctx context.Context, tx pgx.Tx, in IssueAPIKeyInput) (IssuedAPIKey, error) {
	if err := s.AuthorizeCredentials(ctx, in.PrincipalID); err != nil {
		return IssuedAPIKey{}, err
	}
	if _, err := s.coreQuerier(tx).GetServiceAccountByEntityID(ctx, in.PrincipalID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IssuedAPIKey{}, invalidField("principal", "users.principal_not_service_account", "API keys are issued to service accounts only")
		}
		return IssuedAPIKey{}, fmt.Errorf("credentials: look up service account: %w", err)
	}
	label, err := normalizeLabel(in.Label)
	if err != nil {
		return IssuedAPIKey{}, err
	}
	if err := s.validateExpiry(in.ExpiresAt); err != nil {
		return IssuedAPIKey{}, err
	}

	idPart, err := randomHex(16)
	if err != nil {
		return IssuedAPIKey{}, fmt.Errorf("credentials: client id: %w", err)
	}
	secretPart, err := randomHex(32)
	if err != nil {
		return IssuedAPIKey{}, fmt.Errorf("credentials: client secret: %w", err)
	}
	clientID, secret := apiKeyClientIDPrefix+idPart, apiKeySecretPrefix+secretPart

	q := s.usersQ(tx)
	row, err := s.insertCommon(ctx, q, commonInput{
		principalID:      in.PrincipalID,
		kind:             KindAPIKey,
		label:            label,
		defaultProfileID: in.DefaultProfileID,
		expiresAt:        in.ExpiresAt,
	})
	if err != nil {
		return IssuedAPIKey{}, err
	}
	if _, err := q.CreateAPIKeyCredential(ctx, usersdb.CreateAPIKeyCredentialParams{
		CredentialID: row.ID,
		ClientID:     clientID,
		SecretHash:   HashToken(secret),
	}); err != nil {
		return IssuedAPIKey{}, fmt.Errorf("credentials: insert api_key: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opCreate, resourceCredential, in.PrincipalID, nil,
		credentialAudit(row, map[string]any{"client_id": clientID})); err != nil {
		return IssuedAPIKey{}, err
	}
	return IssuedAPIKey{
		CredentialID:   row.ID,
		CredentialUUID: row.Uuid,
		ClientID:       clientID,
		ClientSecret:   secret,
	}, nil
}

// ---------------------------------------------------------------------------
// revoke / remove
// ---------------------------------------------------------------------------

// RevokeInTx revokes the credential with reason (one of user, admin,
// departure, superseded, guest_upgraded, expired) and revokes every unrevoked
// session that holds it as a factor, with revoke_reason 'credential_revoked'.
// Impersonation children of a revoked session end through session liveness.
// Credentials are never deleted. It performs no authorization and no
// last-credential check (see RemoveInTx); the caller owns both.
//
// Revoking an already revoked credential is a no-op. An unknown credential id
// is apiresp.ErrNotFound. Observations: `revoke` `credential`, and `revoke`
// `session` for each session ended, both targeting the principal.
func (s *CredentialService) RevokeInTx(ctx context.Context, tx pgx.Tx, credentialID int64, reason string) error {
	if !validRevokeReasons[reason] {
		return invalidField("reason", "users.revoke_reason_invalid", "unsupported revoke reason")
	}
	q := s.usersQ(tx)
	cred, err := q.GetCredentialByID(ctx, credentialID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apiresp.ErrNotFound
		}
		return fmt.Errorf("credentials: load credential: %w", err)
	}
	return s.revokeLoaded(ctx, tx, q, cred, reason)
}

func (s *CredentialService) revokeLoaded(ctx context.Context, tx pgx.Tx, q usersdb.Querier, cred usersdb.Credential, reason string) error {
	if cred.RevokedAt != nil {
		return nil
	}
	revoked, err := q.RevokeCredential(ctx, usersdb.RevokeCredentialParams{
		ID:           cred.ID,
		RevokeReason: pgtype.Text{String: reason, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // revoked concurrently
		}
		return fmt.Errorf("credentials: revoke credential: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opRevoke, resourceCredential, cred.PrincipalID,
		credentialAudit(cred, nil), credentialAudit(revoked, nil)); err != nil {
		return err
	}

	sessions, err := q.RevokeSessionsHoldingCredential(ctx, cred.ID)
	if err != nil {
		return fmt.Errorf("credentials: revoke sessions holding credential: %w", err)
	}
	for _, sess := range sessions {
		if err := observeInTx(ctx, s.obs, tx, opRevoke, resourceSession, sess.PrincipalID, nil, map[string]any{
			"uuid":          sess.Uuid.String(),
			"revoke_reason": "credential_revoked",
			"credential":    cred.Uuid.String(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// RemoveInTx is the self-service form of RevokeInTx: it revokes a credential
// of principalID and refuses (ErrLastCredential, 409 users.last_credential) to
// revoke the principal's last active interactive credential. A credential that
// does not exist, belongs to another principal or is already revoked is
// apiresp.ErrNotFound (masking). The user row is locked first, so two
// concurrent removals cannot each see the other credential as remaining.
func (s *CredentialService) RemoveInTx(ctx context.Context, tx pgx.Tx, principalID, credentialID int64, reason string) error {
	if !validRevokeReasons[reason] {
		return invalidField("reason", "users.revoke_reason_invalid", "unsupported revoke reason")
	}
	q := s.usersQ(tx)
	if _, err := q.GetUserByEntityIDForUpdate(ctx, principalID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("credentials: lock user: %w", err)
	}
	cred, err := q.GetCredentialByID(ctx, credentialID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apiresp.ErrNotFound
		}
		return fmt.Errorf("credentials: load credential: %w", err)
	}
	if cred.PrincipalID != principalID || cred.RevokedAt != nil {
		return apiresp.ErrNotFound
	}
	kind, err := q.GetCredentialKind(ctx, cred.Kind)
	if err != nil {
		return fmt.Errorf("credentials: load credential kind: %w", err)
	}
	if kind.Class == "interactive" {
		n, err := q.CountActiveInteractiveCredentials(ctx, principalID)
		if err != nil {
			return fmt.Errorf("credentials: count interactive credentials: %w", err)
		}
		if n <= 1 {
			return ErrLastCredential
		}
	}
	return s.revokeLoaded(ctx, tx, q, cred, reason)
}

// ---------------------------------------------------------------------------
// update
// ---------------------------------------------------------------------------

// UpdateCredentialInput is the input of UpdateInTx. Nil fields are left
// unchanged.
type UpdateCredentialInput struct {
	Label *string
	// DefaultProfileID sets the landing profile; ClearDefaultProfile clears it
	// (and wins over DefaultProfileID).
	DefaultProfileID    *int64
	ClearDefaultProfile bool
}

// UpdateInTx changes a credential's label and/or landing profile. The profile
// must be held by the credential's principal (the database refuses otherwise
// with MF102, an internal error: handlers resolve the profile from the
// principal's own list). A credential of another principal, or a revoked one,
// is apiresp.ErrNotFound. Observation: `update` `credential`.
func (s *CredentialService) UpdateInTx(ctx context.Context, tx pgx.Tx, principalID, credentialID int64, in UpdateCredentialInput) (Credential, error) {
	q := s.usersQ(tx)
	cred, err := q.GetCredentialByID(ctx, credentialID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Credential{}, apiresp.ErrNotFound
		}
		return Credential{}, fmt.Errorf("credentials: load credential: %w", err)
	}
	if cred.PrincipalID != principalID || cred.RevokedAt != nil {
		return Credential{}, apiresp.ErrNotFound
	}

	label := cred.Label
	if in.Label != nil {
		if label, err = normalizeLabel(*in.Label); err != nil {
			return Credential{}, err
		}
	}
	def := cred.DefaultProfileID
	switch {
	case in.ClearDefaultProfile:
		def = pgtype.Int8{}
	case in.DefaultProfileID != nil:
		def = pgtype.Int8{Int64: *in.DefaultProfileID, Valid: true}
	}
	if label == cred.Label && def == cred.DefaultProfileID {
		return toCredential(cred), nil
	}

	updated, err := q.UpdateCredentialLabelAndDefault(ctx, usersdb.UpdateCredentialLabelAndDefaultParams{
		ID:               credentialID,
		Label:            label,
		DefaultProfileID: def,
	})
	if err != nil {
		return Credential{}, fmt.Errorf("credentials: update credential: %w", err)
	}
	if err := observeInTx(ctx, s.obs, tx, opUpdate, resourceCredential, principalID,
		credentialAudit(cred, nil), credentialAudit(updated, nil)); err != nil {
		return Credential{}, err
	}
	return toCredential(updated), nil
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

// List returns the principal's credentials, newest first, with their per-kind
// details. Revoked credentials are included only when includeRevoked is set.
// It does not authorize.
func (s *CredentialService) List(ctx context.Context, principalID int64, includeRevoked bool) ([]CredentialDetail, error) {
	var (
		rows []usersdb.Credential
		err  error
	)
	if includeRevoked {
		rows, err = s.q.ListCredentialsByPrincipal(ctx, principalID)
	} else {
		rows, err = s.q.ListActiveCredentialsByPrincipal(ctx, principalID)
	}
	if err != nil {
		return nil, fmt.Errorf("credentials: list credentials: %w", err)
	}
	out := make([]CredentialDetail, len(rows))
	index := make(map[int64]int, len(rows))
	for i, r := range rows {
		out[i] = CredentialDetail{Credential: toCredential(r)}
		index[r.ID] = i
	}
	if len(rows) == 0 {
		return out, nil
	}

	passwords, err := s.q.ListActivePasswordsByPrincipal(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("credentials: list passwords: %w", err)
	}
	for _, p := range passwords {
		if i, ok := index[p.Credential.ID]; ok {
			out[i].IdentifierID = p.IdentifierID.Int64
		}
	}
	oidc, err := s.q.ListActiveOIDCByPrincipal(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("credentials: list oidc: %w", err)
	}
	for _, o := range oidc {
		if i, ok := index[o.Credential.ID]; ok {
			out[i].ProviderID, out[i].Issuer, out[i].Subject = o.ProviderID, o.Issuer, o.Subject
			out[i].EmailAtLink = o.EmailAtLink.String
		}
	}
	keys, err := s.q.ListSSHKeysByPrincipal(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("credentials: list ssh keys: %w", err)
	}
	for _, k := range keys {
		if i, ok := index[k.Credential.ID]; ok {
			out[i].KeyType, out[i].PublicKey, out[i].Fingerprint = k.KeyType, k.PublicKey, k.FingerprintSha256
		}
	}
	apiKeys, err := s.q.ListAPIKeysByPrincipal(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("credentials: list api keys: %w", err)
	}
	for _, k := range apiKeys {
		if i, ok := index[k.Credential.ID]; ok {
			out[i].ClientID = k.ClientID
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// ssh key input validation (shared with handlers)
// ---------------------------------------------------------------------------

// sshKeyParseFieldError maps a sshkey.Parse error to its detail on field
// "public_key". ok is false for a sentinel it does not recognize.
func sshKeyParseFieldError(err error) (fe apiresp.FieldError, ok bool) {
	switch {
	case errors.Is(err, sshkey.ErrTooWeak):
		return apiresp.FieldError{Field: "public_key", Code: "users.ssh_key_too_weak", Message: "key is too weak"}, true
	case errors.Is(err, sshkey.ErrUnsupportedType):
		return apiresp.FieldError{Field: "public_key", Code: "users.ssh_key_type_unsupported", Message: "key type is not supported"}, true
	case errors.Is(err, sshkey.ErrInvalid):
		return apiresp.FieldError{Field: "public_key", Code: "users.ssh_key_invalid", Message: "key is invalid"}, true
	default:
		return apiresp.FieldError{}, false
	}
}

// sshKeyLabelFieldError maps a sshkey.NormalizeLabel error to its detail on
// field "label". ok is false for a sentinel it does not recognize.
func sshKeyLabelFieldError(err error) (fe apiresp.FieldError, ok bool) {
	switch {
	case errors.Is(err, sshkey.ErrLabelTooLong):
		return apiresp.FieldError{Field: "label", Code: "users.ssh_key_label_too_long", Message: "label is too long"}, true
	case errors.Is(err, sshkey.ErrLabelInvalidChars):
		return apiresp.FieldError{Field: "label", Code: "users.ssh_key_label_invalid", Message: "label contains invalid characters"}, true
	default:
		return apiresp.FieldError{}, false
	}
}

// mapSSHKeyParseError maps a sshkey.Parse error to its invalid-input error. A
// sentinel it does not recognize is wrapped as an internal error rather than
// silently defaulting to a known code.
func mapSSHKeyParseError(err error) error {
	if fe, ok := sshKeyParseFieldError(err); ok {
		return apiresp.InvalidInput(fe)
	}
	return fmt.Errorf("credentials: parse public key: %w", err)
}

// mapSSHKeyLabelError maps a sshkey.NormalizeLabel error to its invalid-input
// error.
func mapSSHKeyLabelError(err error) error {
	if fe, ok := sshKeyLabelFieldError(err); ok {
		return apiresp.InvalidInput(fe)
	}
	return fmt.Errorf("credentials: normalize label: %w", err)
}

// ValidateSSHKeyShape runs the stateless key and label checks of
// CreateSSHKeyInTx (sshkey.Parse, sshkey.NormalizeLabel) and returns them as
// field errors with the same detail codes. It touches no state, so an HTTP
// handler can call it before any step-up or authorization gate and report a
// malformed request as 400. publicKeyLine must be non-empty (the caller's
// required-field check owns that).
func ValidateSSHKeyShape(publicKeyLine string, label *string) []apiresp.FieldError {
	parsed, err := sshkey.Parse(publicKeyLine)
	if err != nil {
		if fe, ok := sshKeyParseFieldError(err); ok {
			return []apiresp.FieldError{fe}
		}
		return []apiresp.FieldError{{Field: "public_key", Code: "users.ssh_key_invalid", Message: "key is invalid"}}
	}
	if _, err := sshkey.NormalizeLabel(label, parsed.Comment); err != nil {
		if fe, ok := sshKeyLabelFieldError(err); ok {
			return []apiresp.FieldError{fe}
		}
		return []apiresp.FieldError{{Field: "label", Code: "users.ssh_key_label_invalid", Message: "label contains invalid characters"}}
	}
	return nil
}
