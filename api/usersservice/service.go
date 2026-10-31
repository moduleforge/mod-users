// Package usersservice is the public facade for the users-module services.
// It re-exports the service types and constructors from internal/service.
//
// SSH: SSHKeyResolver.AuthContextForOwner derives the acting profile of an SSH
// push or pull from the repository owner (a party profile when the key
// satisfies the party's gate, otherwise the personal profile). A refusal
// satisfies errors.Is(err, ErrSSHKeyNotAuthorized); SSHKeyNotAuthorizedParty
// reads the uuid of the party the key is not authorized for.
package usersservice

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	gossh "golang.org/x/crypto/ssh"

	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/display"
	"github.com/moduleforge/core-api/observer"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"

	"github.com/moduleforge/mod-users/api/auth"
	"github.com/moduleforge/mod-users/api/config"
	"github.com/moduleforge/mod-users/api/email"
	inner "github.com/moduleforge/mod-users/api/internal/service"
)

// UserService creates users and serves the /v1/me and /v1/principals
// operations. Wire the self-service dependencies with WithSelfService.
type UserService = inner.UserService

// NewUserService constructs a UserService. np creates the user's own
// natural_person; creds creates the guest anon and email_otp credentials. az
// guards only CreateManagedUserInTx. Call WithSelfService on the result before
// serving /v1/me or /v1/principals.
func NewUserService(
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	np coreservice.NaturalPersonServicer,
	creds *CredentialService,
) *UserService {
	return inner.NewUserService(q, coreQ, az, obs, np, creds)
}

// NewUserServiceWithSelfService constructs a UserService wired for the /v1/me
// and /v1/principals operations: NewUserService followed by
// UserService.WithSelfService. pool serves the self-service transactions;
// sessions and challenges serve session listing/revocation and the identifier
// verification codes; ops resolves operation names to the op ids the principal
// list passes to the accessible_*_ids_for_actor functions; maxAuthAge is
// AUTH_SELF_SERVICE_MAX_AUTH_AGE (zero or negative selects the default).
//
// displayRegistry is the shared display registry: the constructor registers
// the user and profile display renderers (RegisterDisplayRenderers) on it, so
// a manifest-generated app and the dev server register them identically. A nil
// displayRegistry skips registration.
func NewUserServiceWithSelfService(
	pool *pgxpool.Pool,
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	np coreservice.NaturalPersonServicer,
	creds *CredentialService,
	sessions *SessionService,
	challenges *ChallengeService,
	ops coreAuthz.OpResolver,
	maxAuthAge time.Duration,
	displayRegistry *display.Registry,
) *UserService {
	if displayRegistry != nil {
		inner.RegisterDisplayRenderers(displayRegistry, pool)
	}
	return inner.NewUserService(q, coreQ, az, obs, np, creds).
		WithSelfService(pool, sessions, challenges, ops, maxAuthAge)
}

// CredentialService manages credentials: password, SSH key, OIDC, API key,
// email code and anonymous.
type CredentialService = inner.CredentialService

// LinkOIDCInput is the input of CredentialService.LinkOIDCInTx.
type LinkOIDCInput = inner.LinkOIDCInput

// LinkedCredential is the result of CredentialService.LinkOIDCInTx.
type LinkedCredential = inner.LinkedCredential

// IssueAPIKeyInput is the input of CredentialService's API key issuance.
type IssueAPIKeyInput = inner.IssueAPIKeyInput

// IssuedAPIKey is the result of issuing an API key: the key record plus the
// one-time plaintext secret.
type IssuedAPIKey = inner.IssuedAPIKey

// NewCredentialService constructs a CredentialService. hashPassword is
// typically auth.HashPassword.
func NewCredentialService(
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	hashPassword func(plain string) (string, error),
) *CredentialService {
	return inner.NewCredentialService(q, coreQ, az, obs, hashPassword)
}

// ChallengeService issues and verifies the email one-time codes and the
// one-time login handoffs.
type ChallengeService = inner.ChallengeService

// NewChallengeService constructs a ChallengeService; sender delivers the
// emailed codes.
func NewChallengeService(q usersdb.Querier, sender email.Sender) *ChallengeService {
	return inner.NewChallengeService(q, sender)
}

// SessionService creates, refreshes, switches and revokes sessions and issues
// their tokens.
type SessionService = inner.SessionService

// SessionConfig holds the token and session lifetimes.
type SessionConfig = inner.SessionConfig

// SessionConfigFrom builds a SessionConfig from the loaded configuration.
func SessionConfigFrom(cfg *config.Config) SessionConfig {
	return inner.SessionConfigFrom(cfg)
}

// NewSessionService constructs a SessionService. q and coreQ must be backed by
// pool. az runs the invite-only app gate.
func NewSessionService(
	pool *pgxpool.Pool,
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	signer *auth.Signer,
	challenges *ChallengeService,
	cfg SessionConfig,
) *SessionService {
	return inner.NewSessionService(pool, q, coreQ, az, obs, signer, challenges, cfg)
}

// ImpersonationService starts, renews and ends impersonation sessions: a child
// session of the administrator's own, with a recorded reason, a lifetime capped
// by AUTH_IMPERSONATION_TTL and AUTH_IMPERSONATION_MAX, a fresh authentication
// requirement, no nesting and the real administrator always recorded.
type ImpersonationService = inner.ImpersonationService

// StartImpersonationInput is the input of ImpersonationService.Start.
type StartImpersonationInput = inner.StartImpersonationInput

// ErrImpersonationNotAllowed: users.impersonation_not_allowed (403), the same
// value as the inner package's.
var ErrImpersonationNotAllowed = inner.ErrImpersonationNotAllowed

// NewImpersonationService constructs an ImpersonationService. q must be backed
// by pool; sessions supplies token issue and the impersonation lifetimes
// (carried by its SessionConfig).
func NewImpersonationService(
	pool *pgxpool.Pool,
	q usersdb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	sessions *SessionService,
) *ImpersonationService {
	return inner.NewImpersonationService(pool, q, az, obs, sessions)
}

// ProfileService owns the lifecycle of party profiles: invitation, acceptance,
// suspension, resumption and ending (with departure cleanup), managed users and
// the lifecycle observers.
type ProfileService = inner.ProfileService

// InviteInput is the input of ProfileService.InviteInTx. Exactly one of
// HolderUserID and HolderEmail names the holder.
type InviteInput = inner.InviteInput

// AdmitInput is the input of ProfileService.AdmitInTx, the trusted admission of
// a user into a party. InvitedByProfileID is 0 when none.
type AdmitInput = inner.AdmitInput

// CollaboratorGate decides whether a profile outside a party may join the
// party's actor group or be named as a grant actor over its content. mod-authz
// consumes it through its own authzservice.CollaboratorGate interface
// (structural match; mod-authz does not import this module).
type CollaboratorGate = inner.CollaboratorGate

// NewCollaboratorGate constructs the CollaboratorGate that is passed as the
// trailing argument of authzservice.NewFromArgs.
func NewCollaboratorGate() *CollaboratorGate {
	return inner.NewCollaboratorGate()
}

// Profile is the service-layer view of a party profile.
type Profile = inner.Profile

// ProfileStatusChange describes one profile creation or status change. From is
// empty when the profile was just created.
type ProfileStatusChange = inner.ProfileStatusChange

// ProfileLifecycleObserver is told of every profile creation and status change,
// inside the transaction that makes it; an error aborts the transaction.
type ProfileLifecycleObserver = inner.ProfileLifecycleObserver

// Errors of the profile lifecycle, re-exported as the same values as the inner
// package's so errors.Is matches across the facade boundary. The HTTP layer
// renders each as 409 with the named code.
var (
	// ErrGuestMustUpgrade: users.guest_upgrade_required.
	ErrGuestMustUpgrade = inner.ErrGuestMustUpgrade
	// ErrHolderNotAdmissible: users.holder_not_admissible.
	ErrHolderNotAdmissible = inner.ErrHolderNotAdmissible
	// ErrProfileSuspended: users.profile_suspended (AdmitInTx).
	ErrProfileSuspended = inner.ErrProfileSuspended
	// ErrProfileExists: users.profile_exists.
	ErrProfileExists = inner.ErrProfileExists
	// ErrProfileTransition: users.profile_transition_invalid.
	ErrProfileTransition = inner.ErrProfileTransition
)

// NewProfileService constructs a ProfileService. users must have been wired
// with UserService.WithSelfService; creds revokes the credentials confined to a
// party the holder leaves; sessions renders the holder's profile view after an
// accept. Register mod-billing style observers with AddLifecycleObserver.
func NewProfileService(
	pool *pgxpool.Pool,
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	users *UserService,
	creds *CredentialService,
	sessions *SessionService,
) *ProfileService {
	return inner.NewProfileService(pool, q, coreQ, az, obs, users, creds, sessions)
}

// PartyPolicyService administers how a party gates its profiles: its
// authentication policy, the credentials authorized for it and the credentials
// confined to it.
type PartyPolicyService = inner.PartyPolicyService

// ErrCredentialConfinedElsewhere: users.credential_confined (409), a credential
// confined to another party.
var ErrCredentialConfinedElsewhere = inner.ErrCredentialConfinedElsewhere

// NewPartyPolicyService constructs a PartyPolicyService. users must have been
// wired with UserService.WithSelfService (it supplies the freshness gate).
func NewPartyPolicyService(
	pool *pgxpool.Pool,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	users *UserService,
) *PartyPolicyService {
	return inner.NewPartyPolicyService(pool, coreQ, az, obs, users)
}

// PersonalPartyService gives a user a party of their own for personal team
// plans: EnsurePersonalParty idempotently creates the user's natural_person,
// links it and creates the personal profile's owned-scope manage grant over it,
// all in the caller's transaction. The grant is invisible to the production
// Authorizer until that transaction commits, so a caller that must authorize
// against it commits first (mod-billing's B6).
type PersonalPartyService = inner.PersonalPartyService

// PersonalParty is the outcome of PersonalPartyService.EnsurePersonalParty.
type PersonalParty = inner.PersonalParty

// Errors of EnsurePersonalParty, the same values as the inner package's
// (ErrGuestMustUpgrade is declared with the profile lifecycle errors).
var (
	// ErrPersonalProfileRequired: users.personal_profile_required (403), the
	// acting profile is not a user's personal profile.
	ErrPersonalProfileRequired = inner.ErrPersonalProfileRequired
	// ErrPersonalPartyNotOwned: users.person_not_owned (409), users.person_id
	// links a person the user does not own.
	ErrPersonalPartyNotOwned = inner.ErrPersonalPartyNotOwned
)

// ensurePersonalPartyShape is a compile-time assertion of the binding
// signature of PersonalPartyService.EnsurePersonalParty (mod-billing calls it).
var _ func(*PersonalPartyService, context.Context, pgx.Tx) (PersonalParty, error) = (*PersonalPartyService).EnsurePersonalParty

// NewPersonalPartyService constructs a PersonalPartyService; obs receives the
// in-transaction observations.
func NewPersonalPartyService(obs *observer.ObserverGroup) *PersonalPartyService {
	return inner.NewPersonalPartyService(obs)
}

// BootstrapService runs the first-run bootstrap of a deployment (the first
// user, the operator party, the admin profile and its wildcard manage grant,
// in one transaction with core_claim_operator_party as the single winner) and
// answers the operator-administrator check.
type BootstrapService = inner.BootstrapService

// BootstrapInput, BootstrapOperator, BootstrapAdmin and BootstrapResult are
// the input and outcome of BootstrapService.Bootstrap.
type (
	BootstrapInput    = inner.BootstrapInput
	BootstrapOperator = inner.BootstrapOperator
	BootstrapAdmin    = inner.BootstrapAdmin
	BootstrapResult   = inner.BootstrapResult
)

// Operator party kinds of BootstrapOperator.Kind.
const (
	BootstrapOperatorOrganization  = inner.BootstrapOperatorOrganization
	BootstrapOperatorNaturalPerson = inner.BootstrapOperatorNaturalPerson
)

// Errors of the bootstrap flow, the same values as the inner package's.
var (
	// ErrAlreadyBootstrapped: users.already_bootstrapped (409).
	ErrAlreadyBootstrapped = inner.ErrAlreadyBootstrapped
	// ErrSetupTokenInvalid: users.setup_token_invalid (401).
	ErrSetupTokenInvalid = inner.ErrSetupTokenInvalid
)

// NewBootstrapService constructs a BootstrapService. users must have been
// wired with UserService.WithSelfService; profiles supplies the internal
// profile creation (lifecycle observers fire for the admin profile); orgs is
// mod-core's organization service, used only for its trusted CreateInTx to
// create the operator organization (it is never exposed through a handler).
// Bootstrap dispatches the after-commit `create` observations (organization and
// the first user's natural_person) once its transaction commits, because the
// CreateInTx primitives observe only inside the transaction.
func NewBootstrapService(
	pool *pgxpool.Pool,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	users *UserService,
	creds *CredentialService,
	sessions *SessionService,
	profiles *ProfileService,
	orgs coreservice.OrganizationServicer,
) *BootstrapService {
	return inner.NewBootstrapService(pool, az, obs, users, creds, sessions, profiles, orgs)
}

// OrganizationFromServices extracts the OrganizationServicer from mod-core's
// aggregate. The manifest uses it to feed bootstrapService, because mod-core's
// manifest provides the aggregate (coreServices) but no organization service
// of its own.
func OrganizationFromServices(svcs *coreservice.Services) coreservice.OrganizationServicer {
	return svcs.Organization
}

// DirectoryService answers batch display lookups for other modules. It does no
// authorization: callers authorize first.
type DirectoryService = inner.DirectoryService

// PrincipalSummary is the display summary of a principal.
type PrincipalSummary = inner.PrincipalSummary

// ProfileSummary is the display summary of a profile.
type ProfileSummary = inner.ProfileSummary

// NewDirectoryService constructs a DirectoryService over the pool.
func NewDirectoryService(pool *pgxpool.Pool) *DirectoryService {
	return inner.NewDirectoryService(pool)
}

// ReadPrincipalSummaries returns the summaries of principalIDs keyed by id,
// reading through db (a pool or a transaction). Unknown ids are absent from
// the map. It does no authorization.
func ReadPrincipalSummaries(ctx context.Context, db usersdb.DBTX, principalIDs []int64) (map[int64]PrincipalSummary, error) {
	return inner.ReadPrincipalSummaries(ctx, db, principalIDs)
}

// RegisterDisplayRenderers registers the user and profile display renderers
// on reg. Call it once at startup, after display.NewRegistry.
func RegisterDisplayRenderers(reg *display.Registry, db usersdb.DBTX) {
	inner.RegisterDisplayRenderers(reg, db)
}

// SSHKeyResolver resolves a candidate SSH public key to the credential that
// registered it. See inner.SSHKeyResolver's doc comment for the hard
// constraints it upholds: no authorization, no writes, no caching, no
// key-material logging.
type SSHKeyResolver = inner.SSHKeyResolver

// SSHCredential is the result of SSHKeyResolver.ResolveCredential.
type SSHCredential = inner.SSHCredential

// ErrUnknownSSHKey is re-exported as the same value as
// inner.ErrUnknownSSHKey (not copied into a new sentinel) so
// errors.Is(err, usersservice.ErrUnknownSSHKey) matches across the facade
// boundary for a caller that only imports this package.
var ErrUnknownSSHKey = inner.ErrUnknownSSHKey

// ErrSSHKeyNotAuthorized is re-exported as the same value as
// inner.ErrSSHKeyNotAuthorized. SSHKeyResolver.AuthContextForOwner returns an
// error satisfying errors.Is(err, ErrSSHKeyNotAuthorized) when the key may not
// act as the profile the repository owner implies ("key not authorized for
// X"): a party whose gate the key does not satisfy, a confined key used
// outside its party, or a personal profile unavailable to the key. Use
// SSHKeyNotAuthorizedParty to read the party's uuid.
var ErrSSHKeyNotAuthorized = inner.ErrSSHKeyNotAuthorized

// SSHKeyNotAuthorizedError is the concrete error behind ErrSSHKeyNotAuthorized.
type SSHKeyNotAuthorizedError = inner.SSHKeyNotAuthorizedError

// SSHKeyNotAuthorizedParty returns the uuid of the party an
// ErrSSHKeyNotAuthorized refusal names, and true. It returns false when err is
// not such a refusal or the refusal is not about a party.
func SSHKeyNotAuthorizedParty(err error) (uuid.UUID, bool) {
	var e *SSHKeyNotAuthorizedError
	if errors.As(err, &e) && e.PartyUUID != uuid.Nil {
		return e.PartyUUID, true
	}
	return uuid.Nil, false
}

// NewSSHKeyResolver constructs an SSHKeyResolver.
func NewSSHKeyResolver(q usersdb.Querier, coreQ coredb.Querier) *SSHKeyResolver {
	return inner.NewSSHKeyResolver(q, coreQ)
}

// sshKeyResolverCredential is a compile-time assertion that *SSHKeyResolver
// exposes the method shape mod-repos' transport needs, without this package
// importing mod-repos, which mod-users must not do.
type sshKeyResolverCredential interface {
	ResolveCredential(context.Context, gossh.PublicKey) (SSHCredential, error)
}

var _ sshKeyResolverCredential = (*SSHKeyResolver)(nil)
