package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
)

// This file holds the credential-administration methods behind
// /v1/principals/{uuid}/credentials*.
//
// Authority. Every method authorizes the `credentials` operation on the
// principal entity through the Authorizer (CredentialService.AuthorizeCredentials):
// a wildcard or entity grant, an owned-scope grant on the owning party, or the
// own arm for a personal profile whose holder owns the principal (an owner
// rotating its self-owned service account's keys). The engine's assurance gate
// enforces the operation's max_auth_age, answering a stale authentication with
// the shared action-required error users.step_up_required. Neither `manage` nor
// `impersonate` is consulted by these methods, and no method authorizes
// impersonation for credential or SSH-key administration.
//
// Two refusals precede the principal lookup. An impersonating session is
// refused with ErrImpersonationNotAllowed (users.impersonation_not_allowed,
// 403): credential administration under another identity would let an
// administrator install a persistent credential for the impersonated user or
// for anyone the impersonated profile administers. A caller addressing its own
// principal is refused with apiresp.ErrForbidden: the /v1/principals routes are
// the administration surface, and self-service credential changes stay on
// /v1/me, which enforces the last-credential check and
// AUTH_SELF_SERVICE_MAX_AUTH_AGE. An owner rotating a self-owned service
// account addresses the service account's principal, not its own, and is
// unaffected.
//
// Error order: 401 (no actor), 400 (stateless input), 403 (impersonating),
// 404 (unknown principal or one the caller may not read), 403 (own principal
// or `credentials` refused) or 409 (step-up, fingerprint in use).

// IssuePrincipalAPIKeyInput is the input of IssuePrincipalAPIKey.
type IssuePrincipalAPIKeyInput struct {
	Label     string
	ExpiresAt *time.Time
	// DefaultProfile is the landing profile of client-credentials tokens; it
	// must be held by the service account. Nil leaves it unset.
	DefaultProfile *uuid.UUID
}

// IssuedAPIKeyView is a newly issued API key. ClientSecret is shown once; the
// database keeps only its hash.
type IssuedAPIKeyView struct {
	CredentialView
	ClientSecret string `json:"client_secret"`
}

// InstallPrincipalSSHKeyInput is the input of InstallPrincipalSSHKey.
type InstallPrincipalSSHKeyInput struct {
	// PublicKey is one authorized_keys line.
	PublicKey string
	// Label overrides the key's comment when non-nil.
	Label     *string
	ExpiresAt *time.Time
}

// credentialsTarget runs the shared front of every credential-administration
// method. Refusals, in the order they are made: the caller must be an
// authenticated actor (401); an impersonating session is refused with
// ErrImpersonationNotAllowed (403 users.impersonation_not_allowed), before the
// principal lookup; the principal must exist (404); a principal that is the
// caller's own is refused with apiresp.ErrForbidden (403), for every caller
// including a service account, before the read check; the principal must be
// readable by the caller (404, masked); and the caller must hold `credentials`
// on it (403, or 409 users.step_up_required).
func (s *UserService) credentialsTarget(ctx context.Context, id uuid.UUID) (PrincipalSummary, error) {
	ac, err := adminCaller(ctx)
	if err != nil {
		return PrincipalSummary{}, err
	}
	if ac.ImpersonatorPrincipalID != 0 {
		return PrincipalSummary{}, ErrImpersonationNotAllowed
	}
	sum, err := s.lookupPrincipal(ctx, id)
	if err != nil {
		return PrincipalSummary{}, err
	}
	// Refused before the read check: the caller knows its own uuid, so nothing
	// leaks, and the refusal must not depend on what the caller may read.
	if sum.PrincipalID == ac.PrincipalID {
		return PrincipalSummary{}, apiresp.ErrForbidden
	}
	if err := s.authorizeMasked(ctx, opSlugRead, sum.PrincipalID); err != nil {
		return PrincipalSummary{}, err
	}
	if s.creds == nil {
		return PrincipalSummary{}, errors.New("users: no credential service configured for principal administration")
	}
	if err := s.creds.AuthorizeCredentials(ctx, sum.PrincipalID); err != nil {
		return PrincipalSummary{}, err
	}
	return sum, nil
}

// ListPrincipalCredentials lists the principal's active credentials, newest
// first (GET /v1/principals/{uuid}/credentials).
func (s *UserService) ListPrincipalCredentials(ctx context.Context, id uuid.UUID) ([]CredentialView, error) {
	sum, err := s.credentialsTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	details, err := s.creds.List(ctx, sum.PrincipalID, false)
	if err != nil {
		return nil, err
	}
	return s.credentialViews(ctx, sum.PrincipalID, details)
}

// principalCredentialView re-reads one active credential of the principal.
func (s *UserService) principalCredentialView(ctx context.Context, principalID int64, credentialUUID uuid.UUID) (CredentialView, error) {
	details, err := s.creds.List(ctx, principalID, false)
	if err != nil {
		return CredentialView{}, err
	}
	views, err := s.credentialViews(ctx, principalID, details)
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

// IssuePrincipalAPIKey issues an API key to a service account
// (POST /v1/principals/{uuid}/credentials/api-keys). A principal that is not a
// service account is a 400 field error. The secret is returned once.
// Observation: `create` `credential`.
func (s *UserService) IssuePrincipalAPIKey(ctx context.Context, id uuid.UUID, in IssuePrincipalAPIKeyInput) (IssuedAPIKeyView, error) {
	if _, err := adminCaller(ctx); err != nil {
		return IssuedAPIKeyView{}, err
	}
	if s.creds == nil {
		return IssuedAPIKeyView{}, errors.New("users: no credential service configured for principal administration")
	}
	if _, err := normalizeLabel(in.Label); err != nil {
		return IssuedAPIKeyView{}, err
	}
	if err := s.creds.validateExpiry(in.ExpiresAt); err != nil {
		return IssuedAPIKeyView{}, err
	}
	sum, err := s.credentialsTarget(ctx, id)
	if err != nil {
		return IssuedAPIKeyView{}, err
	}

	var issued IssuedAPIKey
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		input := IssueAPIKeyInput{PrincipalID: sum.PrincipalID, Label: in.Label, ExpiresAt: in.ExpiresAt}
		if in.DefaultProfile != nil {
			profileID, err := heldProfileID(ctx, s.coreQuerier(tx), sum.PrincipalID, *in.DefaultProfile)
			if err != nil {
				return err
			}
			input.DefaultProfileID = &profileID
		}
		var err error
		issued, err = s.creds.IssueAPIKeyInTx(ctx, tx, input)
		return err
	})
	if err != nil {
		return IssuedAPIKeyView{}, err
	}
	view, err := s.principalCredentialView(ctx, sum.PrincipalID, issued.CredentialUUID)
	if err != nil {
		return IssuedAPIKeyView{}, err
	}
	return IssuedAPIKeyView{CredentialView: view, ClientSecret: issued.ClientSecret}, nil
}

// InstallPrincipalSSHKey registers an SSH public key for the principal
// (POST /v1/principals/{uuid}/credentials/ssh-keys): the operator path, under
// `credentials`. A malformed key or label is a 400 field error; a fingerprint
// that is active anywhere is ErrSSHKeyInUse (409 users.ssh_key_in_use); a guest
// is ErrGuestMustUpgrade. Observation: `create` `credential`.
func (s *UserService) InstallPrincipalSSHKey(ctx context.Context, id uuid.UUID, in InstallPrincipalSSHKeyInput) (CredentialView, error) {
	if _, err := adminCaller(ctx); err != nil {
		return CredentialView{}, err
	}
	if s.creds == nil {
		return CredentialView{}, errors.New("users: no credential service configured for principal administration")
	}
	if fes := ValidateSSHKeyShape(in.PublicKey, in.Label); len(fes) > 0 {
		return CredentialView{}, apiresp.InvalidInput(fes...)
	}
	if err := s.creds.validateExpiry(in.ExpiresAt); err != nil {
		return CredentialView{}, err
	}
	sum, err := s.credentialsTarget(ctx, id)
	if err != nil {
		return CredentialView{}, err
	}

	var created SSHKeyCredential
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if sum.Kind == PrincipalKindUser {
			if _, err := s.loadNonGuest(ctx, s.usersQ(tx), sum.PrincipalID); err != nil {
				return err
			}
		}
		var err error
		created, err = s.creds.CreateSSHKeyInTx(ctx, tx, CreateSSHKeyInput{
			PrincipalID: sum.PrincipalID, PublicKey: in.PublicKey, Label: in.Label, ExpiresAt: in.ExpiresAt,
		})
		return err
	})
	if err != nil {
		return CredentialView{}, err
	}
	return s.principalCredentialView(ctx, sum.PrincipalID, created.UUID)
}

// RevokePrincipalCredential revokes one of the principal's credentials with
// reason `admin` (DELETE /v1/principals/{uuid}/credentials/{credential_uuid}),
// ending every session that holds it as a factor. A credential that does not
// exist, belongs to another principal or is already revoked is
// apiresp.ErrNotFound. Unlike the self-service removal it has no
// last-credential check: an administrator may remove a principal's last
// credential. Observations: `revoke` `credential`, and `revoke` `session` for
// each session ended.
func (s *UserService) RevokePrincipalCredential(ctx context.Context, id, credentialUUID uuid.UUID) error {
	sum, err := s.credentialsTarget(ctx, id)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		cred, err := selfCredential(ctx, s.usersQ(tx), sum.PrincipalID, credentialUUID)
		if err != nil {
			return err
		}
		return s.creds.RevokeInTx(ctx, tx, cred.ID, RevokeReasonAdmin)
	})
}
