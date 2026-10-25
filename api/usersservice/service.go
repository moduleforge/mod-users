// Package usersservice is the public facade for the users-module services.
// It re-exports the service types and constructors from internal/service.
package usersservice

import (
	"context"
	"time"

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

// DirectoryService answers batch display lookups for other modules. It does no
// authorization: callers authorize first.
type DirectoryService = inner.DirectoryService

// PrincipalSummary is the display summary of a principal.
type PrincipalSummary = inner.PrincipalSummary

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
