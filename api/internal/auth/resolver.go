package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/mod-users/api/internal/config"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Redirect error codes of the OIDC callback (`#error=<code>`).
const (
	// RedirectErrAccountExists: the identity is unknown, but its email matches
	// an existing identifier. The user must sign in and link the provider; the
	// resolver never links by email.
	RedirectErrAccountExists = "account_exists_sign_in_and_link"
	// RedirectErrNoAccount: the identity is unknown and the provider creates no
	// account for it.
	RedirectErrNoAccount = "no_account"
	// RedirectErrAuthFailed: the sign-in failed for any other reason; the cause
	// is in the server log only.
	RedirectErrAuthFailed = "authentication_failed"
	// RedirectErrMissingEmail: the identity is unknown, the provider would
	// create a personal account, and the IdP supplied no usable email.
	RedirectErrMissingEmail = "missing_email"
	// RedirectErrIdentityInUse: a link flow found the external identity already
	// linked to another principal.
	RedirectErrIdentityInUse = "identity_in_use"
)

// identifierKindEmail is the user_identifiers.kind of an email address.
const identifierKindEmail = "email"

// ProviderPolicy is what the resolver and the callback need to know about a
// provider's account-creation behavior. It is the provider's party binding,
// jit_mode and trust_acr after the env/DB merge.
type ProviderPolicy struct {
	// ProviderID is the provider's id (cred_oidc.provider_id).
	ProviderID string
	// PartyID is the party (legal entity id) the provider is bound to; 0 when
	// unbound.
	PartyID int64
	// JITMode is config.JITModeNone, config.JITModeBYO or config.JITModeManaged.
	JITMode string
	// TrustACR makes the session count the IdP's asserted acr.
	TrustACR bool
	// DisplayName is the provider's display name.
	DisplayName string
}

// NewProviderPolicy derives the policy of p. noOIDCAccounts is the
// NO_OIDC_ACCOUNTS opt-out: it turns a bring-your-own provider into one that
// creates no account. An unknown jit_mode fails closed to none.
func NewProviderPolicy(p config.Provider, noOIDCAccounts bool) ProviderPolicy {
	mode := p.JITMode
	if mode == "" {
		mode = config.JITModeBYO
	}
	if !config.ValidJITMode(mode) || (mode == config.JITModeBYO && noOIDCAccounts) {
		mode = config.JITModeNone
	}
	return ProviderPolicy{
		ProviderID:  p.ID,
		PartyID:     p.PartyID,
		JITMode:     mode,
		TrustACR:    p.TrustACR,
		DisplayName: p.DisplayName,
	}
}

// AssertedACR is the acr the session may count for identity id: the IdP's
// asserted acr when the provider has trust_acr, else 0 (nothing asserted).
func (p ProviderPolicy) AssertedACR(id ExternalIdentity) int16 {
	if !p.TrustACR {
		return 0
	}
	return id.AssertedACR
}

// Action is what the callback does for a verified external identity.
type Action int

const (
	// ActionRefuse redirects with Resolution.Refusal.
	ActionRefuse Action = iota
	// ActionLogin signs in with Resolution.Credential.
	ActionLogin
	// ActionCreateBYO creates a bring-your-own user (user, person, personal
	// profile, email identifier, oidc credential) and signs it in.
	ActionCreateBYO
	// ActionCreateManaged creates a managed user of the provider's party with
	// an active idp_jit party profile and a party-confined oidc credential, and
	// signs it in.
	ActionCreateManaged
)

// Resolution is the resolver's decision for one verified identity.
type Resolution struct {
	Action Action
	// Credential is the matched active oidc credential (ActionLogin).
	Credential usersdb.Credential
	// Refusal is the redirect error code (ActionRefuse).
	Refusal string
}

// ResolverQuerier is the read access the resolver needs.
type ResolverQuerier interface {
	GetActiveOIDCByIssuerSubject(ctx context.Context, arg usersdb.GetActiveOIDCByIssuerSubjectParams) (usersdb.GetActiveOIDCByIssuerSubjectRow, error)
	GetIdentifierByKindValue(ctx context.Context, arg usersdb.GetIdentifierByKindValueParams) (usersdb.UserIdentifier, error)
}

// OIDCResolver decides what a verified external identity maps to. It only
// reads: the callback performs the writes the decision calls for. The decision
// table:
//
//	known active oidc credential (issuer, subject)        -> ActionLogin
//	unknown, email matches an existing identifier         -> refuse account_exists_sign_in_and_link
//	unknown, jit_mode none                                -> refuse no_account
//	unknown, jit_mode byo, no email                       -> refuse missing_email
//	unknown, jit_mode byo                                 -> ActionCreateBYO
//	unknown, jit_mode managed with a party                -> ActionCreateManaged
//
// It never links an identity to an account by email: an email only ever
// produces a refusal.
type OIDCResolver struct {
	q ResolverQuerier
}

// NewOIDCResolver constructs an OIDCResolver over q.
func NewOIDCResolver(q ResolverQuerier) *OIDCResolver {
	return &OIDCResolver{q: q}
}

// Resolve decides what to do for id, verified by the provider policy describes.
func (r *OIDCResolver) Resolve(ctx context.Context, id ExternalIdentity, policy ProviderPolicy) (Resolution, error) {
	if id.Issuer == "" || id.Subject == "" {
		return Resolution{Action: ActionRefuse, Refusal: RedirectErrAuthFailed}, nil
	}

	known, err := r.q.GetActiveOIDCByIssuerSubject(ctx, usersdb.GetActiveOIDCByIssuerSubjectParams{
		Issuer: id.Issuer, Subject: id.Subject,
	})
	if err == nil {
		return Resolution{Action: ActionLogin, Credential: known.Credential}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Resolution{}, fmt.Errorf("resolver: look up oidc credential: %w", err)
	}

	// Unknown identity. An email match is a refusal, never a link.
	email := strings.ToLower(strings.TrimSpace(id.Email))
	if email != "" {
		_, err := r.q.GetIdentifierByKindValue(ctx, usersdb.GetIdentifierByKindValueParams{
			Kind: identifierKindEmail, ValueCanon: email,
		})
		if err == nil {
			return Resolution{Action: ActionRefuse, Refusal: RedirectErrAccountExists}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Resolution{}, fmt.Errorf("resolver: look up email identifier: %w", err)
		}
	}

	switch policy.JITMode {
	case config.JITModeBYO:
		if email == "" {
			return Resolution{Action: ActionRefuse, Refusal: RedirectErrMissingEmail}, nil
		}
		return Resolution{Action: ActionCreateBYO}, nil
	case config.JITModeManaged:
		if policy.PartyID <= 0 {
			return Resolution{Action: ActionRefuse, Refusal: RedirectErrNoAccount}, nil
		}
		return Resolution{Action: ActionCreateManaged}, nil
	default:
		return Resolution{Action: ActionRefuse, Refusal: RedirectErrNoAccount}, nil
	}
}
