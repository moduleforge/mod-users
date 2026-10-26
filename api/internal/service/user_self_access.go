package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// This file holds the self-service identifier, credential and session methods
// behind /v1/me. See user_self_service.go for the authority and step-up rules
// every Self* method follows.

// ---------------------------------------------------------------------------
// identifiers
// ---------------------------------------------------------------------------

// IdentifierView is the self-service view of an identifier.
type IdentifierView struct {
	UUID       uuid.UUID  `json:"uuid"`
	Kind       string     `json:"kind"`
	Value      string     `json:"value"`
	IsPrimary  bool       `json:"is_primary"`
	Verified   bool       `json:"verified"`
	VerifiedAt *time.Time `json:"verified_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func identifierView(i Identifier) IdentifierView {
	return IdentifierView{
		UUID: i.UUID, Kind: i.Kind, Value: i.Value, IsPrimary: i.IsPrimary,
		Verified: i.VerifiedAt != nil, VerifiedAt: i.VerifiedAt, CreatedAt: i.CreatedAt,
	}
}

// selfIdentifier loads the caller's identifier addressed by uuid; one that does
// not exist or belongs to someone else is apiresp.ErrNotFound (masking).
func selfIdentifier(ctx context.Context, q usersdb.Querier, userID int64, id uuid.UUID) (usersdb.UserIdentifier, error) {
	row, err := q.GetIdentifierByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return usersdb.UserIdentifier{}, apiresp.ErrNotFound
	}
	if err != nil {
		return usersdb.UserIdentifier{}, fmt.Errorf("users: load identifier: %w", err)
	}
	if row.UserID != userID {
		return usersdb.UserIdentifier{}, apiresp.ErrNotFound
	}
	return row, nil
}

// SelfListIdentifiers lists the caller's identifiers, primary first
// (GET /v1/me/identifiers).
func (s *UserService) SelfListIdentifiers(ctx context.Context) ([]IdentifierView, error) {
	ac, err := selfReader(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := s.ListIdentifiers(ctx, ac.PrincipalID)
	if err != nil {
		return nil, err
	}
	out := make([]IdentifierView, len(ids))
	for i, id := range ids {
		out[i] = identifierView(id)
	}
	return out, nil
}

// AddSelfIdentifierInput is the input of SelfAddIdentifier.
type AddSelfIdentifierInput struct {
	// Kind is the identifier kind; empty means email, the only supported kind.
	Kind  string
	Value string
}

// SelfAddIdentifier adds an unverified email identifier to the caller
// (POST /v1/me/identifiers). The first email becomes primary. A guest is
// ErrGuestMustUpgrade; an address that is registered to anyone is
// ErrIdentifierTaken. Observation: `create` `user_identifier`.
func (s *UserService) SelfAddIdentifier(ctx context.Context, in AddSelfIdentifierInput) (IdentifierView, error) {
	ac, err := selfWriter(ctx)
	if err != nil {
		return IdentifierView{}, err
	}
	kind := in.Kind
	if kind == "" {
		kind = IdentifierKindEmail
	}
	if kind != IdentifierKindEmail {
		return IdentifierView{}, invalidField("kind", "users.identifier_kind_unsupported", "only email identifiers are supported")
	}
	if err := validateEmail(CanonicalizeEmail(in.Value)); err != nil {
		return IdentifierView{}, err
	}
	if err := s.requireFresh(ac); err != nil {
		return IdentifierView{}, err
	}
	var out Identifier
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.AddIdentifierInTx(ctx, tx, AddIdentifierInput{
			UserID: ac.PrincipalID, Kind: kind, Value: in.Value,
		})
		return err
	})
	if err != nil {
		return IdentifierView{}, err
	}
	return identifierView(out), nil
}

// SelfRequestVerification emails a verify_identifier code to the caller's
// identifier (POST /v1/me/identifiers/{uuid}/verification). An already
// verified identifier, and a request inside the cooldown window, send nothing
// and succeed, so the response does not reveal the identifier's state beyond
// what the list already shows.
func (s *UserService) SelfRequestVerification(ctx context.Context, identifierUUID uuid.UUID) error {
	ac, err := s.selfWrite(ctx)
	if err != nil {
		return err
	}
	if s.challenges == nil {
		return errors.New("users: no challenge service configured for self-service")
	}
	row, err := selfIdentifier(ctx, s.q, ac.PrincipalID, identifierUUID)
	if err != nil {
		return err
	}
	if row.VerifiedAt != nil {
		return nil
	}
	var issued IssuedChallenge
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		issued, err = s.challenges.IssueCodeInTx(ctx, tx, IssueCodeInput{
			Kind:    ChallengeVerifyIdentifier,
			Subject: Subject{PrincipalID: ac.PrincipalID, IdentifierID: row.ID},
		})
		return err
	})
	if errors.Is(err, ErrChallengeCooldown) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.challenges.DeliverCode(ctx, row.ValueCanon, ChallengeVerifyIdentifier, issued.Code)
}

// errCodeInvalid is the 400 for a wrong, expired or exhausted identifier
// verification code. It is deliberately not the 401 ErrInvalidCode: the caller
// is authenticated, and a 401 would read as a dead session.
func errCodeInvalid() error {
	return invalidField("code", "users.code_invalid", "the code is invalid or has expired")
}

// SelfVerifyIdentifier checks the emailed code and marks the caller's
// identifier verified, creating its email_otp credential (decision U12)
// (POST /v1/me/identifiers/{uuid}/verify). A wrong, expired or exhausted code
// is a 400 field error. Observation: `update` `user_identifier`.
func (s *UserService) SelfVerifyIdentifier(ctx context.Context, identifierUUID uuid.UUID, code string) (IdentifierView, error) {
	ac, err := selfWriter(ctx)
	if err != nil {
		return IdentifierView{}, err
	}
	if code == "" {
		return IdentifierView{}, invalidField("code", "users.code_required", "code is required")
	}
	if err := s.requireFresh(ac); err != nil {
		return IdentifierView{}, err
	}
	if s.challenges == nil {
		return IdentifierView{}, errors.New("users: no challenge service configured for self-service")
	}
	row, err := selfIdentifier(ctx, s.q, ac.PrincipalID, identifierUUID)
	if err != nil {
		return IdentifierView{}, err
	}
	if _, err := s.challenges.VerifyCode(ctx, VerifyCodeInput{
		Kind:    ChallengeVerifyIdentifier,
		Subject: Subject{PrincipalID: ac.PrincipalID, IdentifierID: row.ID},
		Code:    code,
	}); err != nil {
		if errors.Is(err, ErrInvalidCode) {
			return IdentifierView{}, errCodeInvalid()
		}
		return IdentifierView{}, err
	}
	var out Identifier
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.VerifyIdentifierInTx(ctx, tx, ac.PrincipalID, row.ID)
		return err
	})
	if err != nil {
		return IdentifierView{}, err
	}
	return identifierView(out), nil
}

// SelfSetPrimaryIdentifier makes the caller's email identifier the primary one
// (PUT /v1/me/identifiers/{uuid}/primary). Observation: `update`
// `user_identifier`.
func (s *UserService) SelfSetPrimaryIdentifier(ctx context.Context, identifierUUID uuid.UUID) (IdentifierView, error) {
	ac, err := s.selfWrite(ctx)
	if err != nil {
		return IdentifierView{}, err
	}
	var out Identifier
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		row, err := selfIdentifier(ctx, s.usersQ(tx), ac.PrincipalID, identifierUUID)
		if err != nil {
			return err
		}
		out, err = s.SetPrimaryIdentifierInTx(ctx, tx, ac.PrincipalID, row.ID)
		return err
	})
	if err != nil {
		return IdentifierView{}, err
	}
	return identifierView(out), nil
}

// SelfRemoveIdentifier removes the caller's identifier
// (DELETE /v1/me/identifiers/{uuid}), first revoking the credentials bound to
// it. Removing the last interactive credential is ErrLastCredential, and an
// identifier still referenced by a restricting foreign key is ErrIdentifierInUse.
// Observation: `delete` `user_identifier`.
func (s *UserService) SelfRemoveIdentifier(ctx context.Context, identifierUUID uuid.UUID) error {
	ac, err := s.selfWrite(ctx)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		row, err := selfIdentifier(ctx, s.usersQ(tx), ac.PrincipalID, identifierUUID)
		if err != nil {
			return err
		}
		return s.RemoveIdentifierInTx(ctx, tx, ac.PrincipalID, row.ID)
	})
}

// ---------------------------------------------------------------------------
// credentials
// ---------------------------------------------------------------------------

// CredentialView is the self-service view of an active credential: identifying
// and display attributes only, never a hash, secret or token.
type CredentialView struct {
	UUID                uuid.UUID  `json:"uuid"`
	Kind                string     `json:"kind"`
	Label               string     `json:"label"`
	CreatedAt           time.Time  `json:"created_at"`
	LastUsedAt          *time.Time `json:"last_used_at"`
	ExpiresAt           *time.Time `json:"expires_at"`
	DefaultProfileUUID  *uuid.UUID `json:"default_profile_uuid"`
	ConfinedToPartyUUID *uuid.UUID `json:"confined_to_party_uuid"`
	// IdentifierUUID is the identifier a password is bound to.
	IdentifierUUID *uuid.UUID `json:"identifier_uuid,omitempty"`
	// ProviderID and EmailAtLink describe an oidc credential.
	ProviderID  string `json:"provider_id,omitempty"`
	EmailAtLink string `json:"email_at_link,omitempty"`
	// KeyType, Fingerprint and PublicKey describe an ssh_key credential.
	KeyType     string `json:"key_type,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	// ClientID describes an api_key credential.
	ClientID string `json:"client_id,omitempty"`
}

// entityUUIDs maps entity ids to their public uuids in one query.
func entityUUIDs(ctx context.Context, db usersdb.DBTX, ids []int64) (map[int64]uuid.UUID, error) {
	out := make(map[int64]uuid.UUID, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx, `SELECT id, uuid FROM public.entities WHERE id = ANY($1::bigint[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("users: resolve entity uuids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id int64
			u  uuid.UUID
		)
		if err := rows.Scan(&id, &u); err != nil {
			return nil, fmt.Errorf("users: scan entity uuid: %w", err)
		}
		out[id] = u
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("users: resolve entity uuids: %w", err)
	}
	return out, nil
}

// credentialViews renders details for principalID.
func (s *UserService) credentialViews(ctx context.Context, principalID int64, details []CredentialDetail) ([]CredentialView, error) {
	var entityIDs []int64
	for _, d := range details {
		if d.DefaultProfileID != 0 {
			entityIDs = append(entityIDs, d.DefaultProfileID)
		}
		if d.ConfinedToPartyID != 0 {
			entityIDs = append(entityIDs, d.ConfinedToPartyID)
		}
	}
	uuids, err := entityUUIDs(ctx, s.db, entityIDs)
	if err != nil {
		return nil, err
	}
	identifiers, err := s.ListIdentifiers(ctx, principalID)
	if err != nil {
		return nil, err
	}
	identifierUUID := make(map[int64]uuid.UUID, len(identifiers))
	for _, i := range identifiers {
		identifierUUID[i.ID] = i.UUID
	}

	out := make([]CredentialView, len(details))
	for i, d := range details {
		v := CredentialView{
			UUID: d.UUID, Kind: d.Kind, Label: d.Label, CreatedAt: d.CreatedAt,
			LastUsedAt: d.LastUsedAt, ExpiresAt: d.ExpiresAt,
			ProviderID: d.ProviderID, EmailAtLink: d.EmailAtLink,
			KeyType: d.KeyType, Fingerprint: d.Fingerprint, PublicKey: d.PublicKey, ClientID: d.ClientID,
		}
		if u, ok := uuids[d.DefaultProfileID]; ok && d.DefaultProfileID != 0 {
			v.DefaultProfileUUID = &u
		}
		if u, ok := uuids[d.ConfinedToPartyID]; ok && d.ConfinedToPartyID != 0 {
			v.ConfinedToPartyUUID = &u
		}
		if u, ok := identifierUUID[d.IdentifierID]; ok && d.IdentifierID != 0 {
			v.IdentifierUUID = &u
		}
		out[i] = v
	}
	return out, nil
}

// SelfListCredentials lists the caller's active credentials, newest first
// (GET /v1/me/credentials).
func (s *UserService) SelfListCredentials(ctx context.Context) ([]CredentialView, error) {
	ac, err := selfReader(ctx)
	if err != nil {
		return nil, err
	}
	details, err := s.creds.List(ctx, ac.PrincipalID, false)
	if err != nil {
		return nil, err
	}
	return s.credentialViews(ctx, ac.PrincipalID, details)
}

// selfCredentialView re-reads the caller's credential after a write.
func (s *UserService) selfCredentialView(ctx context.Context, principalID int64, credentialUUID uuid.UUID) (CredentialView, error) {
	views, err := s.SelfListCredentials(ctx)
	if err != nil {
		return CredentialView{}, err
	}
	for _, v := range views {
		if v.UUID == credentialUUID {
			return v, nil
		}
	}
	return CredentialView{}, fmt.Errorf("users: credential %s not found after write (principal %d)", credentialUUID, principalID)
}

// AddSelfPasswordInput is the input of SelfAddPassword.
type AddSelfPasswordInput struct {
	Password string
	Label    string
	// IdentifierUUID binds the password to one of the caller's identifiers (the
	// one it signs in with). Nil binds it to the primary email when there is
	// one, else leaves it unbound.
	IdentifierUUID *uuid.UUID
}

// SelfAddPassword adds a password credential for the caller
// (POST /v1/me/credentials/password). A caller that already has an active
// password for the identifier (or an unbound one) gets ErrPasswordExists:
// replace a password by removing the old one first. A guest is
// ErrGuestMustUpgrade. Observation: `create` `credential`.
func (s *UserService) SelfAddPassword(ctx context.Context, in AddSelfPasswordInput) (CredentialView, error) {
	ac, err := selfWriter(ctx)
	if err != nil {
		return CredentialView{}, err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return CredentialView{}, err
	}
	if _, err := normalizeLabel(in.Label); err != nil {
		return CredentialView{}, err
	}
	if err := s.requireFresh(ac); err != nil {
		return CredentialView{}, err
	}
	var created Credential
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		q := s.usersQ(tx)
		if _, err := s.loadNonGuest(ctx, q, ac.PrincipalID); err != nil {
			return err
		}
		var identifierID *int64
		if in.IdentifierUUID != nil {
			row, err := selfIdentifier(ctx, q, ac.PrincipalID, *in.IdentifierUUID)
			if err != nil {
				return err
			}
			identifierID = &row.ID
		} else if primary, err := q.GetPrimaryEmailByUser(ctx, ac.PrincipalID); err == nil {
			identifierID = &primary.ID
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("users: look up primary email: %w", err)
		}
		created, err = s.creds.CreatePasswordInTx(ctx, tx, CreatePasswordInput{
			PrincipalID: ac.PrincipalID, IdentifierID: identifierID, Password: in.Password, Label: in.Label,
		})
		return err
	})
	if err != nil {
		return CredentialView{}, err
	}
	return s.selfCredentialView(ctx, ac.PrincipalID, created.UUID)
}

// AddSelfSSHKeyInput is the input of SelfAddSSHKey.
type AddSelfSSHKeyInput struct {
	// PublicKey is one authorized_keys line.
	PublicKey string
	// Label overrides the key's comment when non-nil.
	Label *string
}

// SelfAddSSHKey registers an SSH public key for the caller
// (POST /v1/me/credentials/ssh-keys). A malformed key or label is a 400 field
// error; a fingerprint that is active anywhere is ErrSSHKeyInUse; a guest is
// ErrGuestMustUpgrade. Observation: `create` `credential`.
func (s *UserService) SelfAddSSHKey(ctx context.Context, in AddSelfSSHKeyInput) (CredentialView, error) {
	ac, err := selfWriter(ctx)
	if err != nil {
		return CredentialView{}, err
	}
	if fes := ValidateSSHKeyShape(in.PublicKey, in.Label); len(fes) > 0 {
		return CredentialView{}, apiresp.InvalidInput(fes...)
	}
	if err := s.requireFresh(ac); err != nil {
		return CredentialView{}, err
	}
	var created SSHKeyCredential
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.loadNonGuest(ctx, s.usersQ(tx), ac.PrincipalID); err != nil {
			return err
		}
		var err error
		created, err = s.creds.CreateSSHKeyInTx(ctx, tx, CreateSSHKeyInput{
			PrincipalID: ac.PrincipalID, PublicKey: in.PublicKey, Label: in.Label,
		})
		return err
	})
	if err != nil {
		return CredentialView{}, err
	}
	return s.selfCredentialView(ctx, ac.PrincipalID, created.UUID)
}

// selfCredential loads the caller's active credential addressed by uuid; one
// that does not exist, belongs to someone else or is revoked is
// apiresp.ErrNotFound (masking).
func selfCredential(ctx context.Context, q usersdb.Querier, principalID int64, id uuid.UUID) (usersdb.Credential, error) {
	row, err := q.GetCredentialByUUID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return usersdb.Credential{}, apiresp.ErrNotFound
	}
	if err != nil {
		return usersdb.Credential{}, fmt.Errorf("users: load credential: %w", err)
	}
	if row.PrincipalID != principalID || row.RevokedAt != nil {
		return usersdb.Credential{}, apiresp.ErrNotFound
	}
	return row, nil
}

// UpdateSelfCredentialInput is the input of SelfUpdateCredential. Nil and
// unset fields are left unchanged.
type UpdateSelfCredentialInput struct {
	Label *string
	// DefaultProfile is the landing profile; it must be held by the caller.
	DefaultProfile OptionalUUID
}

// SelfUpdateCredential changes the label and/or landing profile of the caller's
// credential (PATCH /v1/me/credentials/{uuid}). Observation: `update`
// `credential`, written only when something changed.
func (s *UserService) SelfUpdateCredential(ctx context.Context, credentialUUID uuid.UUID, in UpdateSelfCredentialInput) (CredentialView, error) {
	ac, err := selfWriter(ctx)
	if err != nil {
		return CredentialView{}, err
	}
	if in.Label != nil {
		if _, err := normalizeLabel(*in.Label); err != nil {
			return CredentialView{}, err
		}
	}
	if err := s.requireFresh(ac); err != nil {
		return CredentialView{}, err
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		cred, err := selfCredential(ctx, s.usersQ(tx), ac.PrincipalID, credentialUUID)
		if err != nil {
			return err
		}
		update := UpdateCredentialInput{Label: in.Label}
		if in.DefaultProfile.Set {
			if in.DefaultProfile.Value == uuid.Nil {
				update.ClearDefaultProfile = true
			} else {
				id, err := heldProfileID(ctx, s.coreQuerier(tx), ac.PrincipalID, in.DefaultProfile.Value)
				if err != nil {
					return err
				}
				update.DefaultProfileID = &id
			}
		}
		_, err = s.creds.UpdateInTx(ctx, tx, ac.PrincipalID, cred.ID, update)
		return err
	})
	if err != nil {
		return CredentialView{}, err
	}
	return s.selfCredentialView(ctx, ac.PrincipalID, credentialUUID)
}

// SelfRevokeCredential revokes the caller's credential with reason `user`
// (DELETE /v1/me/credentials/{uuid}), ending the sessions that hold it as a
// factor. Revoking the last active interactive credential is ErrLastCredential.
// Observations: `revoke` `credential`, and `revoke` `session` for each session
// ended.
func (s *UserService) SelfRevokeCredential(ctx context.Context, credentialUUID uuid.UUID) error {
	ac, err := s.selfWrite(ctx)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		cred, err := selfCredential(ctx, s.usersQ(tx), ac.PrincipalID, credentialUUID)
		if err != nil {
			return err
		}
		return s.creds.RemoveInTx(ctx, tx, ac.PrincipalID, cred.ID, RevokeReasonUser)
	})
}

// ---------------------------------------------------------------------------
// sessions
// ---------------------------------------------------------------------------

// SelfSessionView is one of the caller's live sessions; Current marks the one
// that made the request.
type SelfSessionView struct {
	SessionSummary
	Current bool `json:"current"`
}

// SelfListSessions lists the caller's live sessions, most recently seen first
// (GET /v1/me/sessions).
func (s *UserService) SelfListSessions(ctx context.Context) ([]SelfSessionView, error) {
	ac, err := selfReader(ctx)
	if err != nil {
		return nil, err
	}
	if s.sessions == nil {
		return nil, errors.New("users: no session service configured for self-service")
	}
	sums, err := s.sessions.ListSessions(ctx, ac.PrincipalID)
	if err != nil {
		return nil, err
	}
	out := make([]SelfSessionView, len(sums))
	for i, sum := range sums {
		out[i] = SelfSessionView{SessionSummary: sum, Current: sum.ID == ac.SessionID}
	}
	return out, nil
}

// SelfRevokeSession revokes one of the caller's sessions, addressed by uuid,
// with reason `logout` (DELETE /v1/me/sessions/{uuid}). The caller's own
// current session may be revoked (it then behaves as a logout). A session that
// does not exist, belongs to someone else or is already revoked is
// apiresp.ErrNotFound. Observation: `revoke` `session` for each session ended.
func (s *UserService) SelfRevokeSession(ctx context.Context, sessionUUID uuid.UUID) error {
	ac, err := s.selfWrite(ctx)
	if err != nil {
		return err
	}
	if s.sessions == nil {
		return errors.New("users: no session service configured for self-service")
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		return s.sessions.RevokeSessionInTx(ctx, tx, ac.PrincipalID, sessionUUID, SessionRevokeLogout)
	})
}
