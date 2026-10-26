package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	gossh "golang.org/x/crypto/ssh"

	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/sshkey"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// ErrUnknownSSHKey is returned by SSHKeyResolver.ResolveCredential for a key
// that does not resolve to any known, currently usable credential: an
// unregistered key, a revoked or expired key, and a key whose principal is
// archived or disabled all return this exact sentinel, with no distinguishing
// wrapping between the cases (docs/architecture/ssh-keys.md D11) --
// ResolveCredential never reveals more than "resolution failed" to its caller.
var ErrUnknownSSHKey = errors.New("service: unknown ssh public key")

// ErrSSHKeyNotAuthorized is returned by SSHKeyResolver.AuthContextForOwner
// when the credential may not act in the requested context: it is confined to
// a party, or the profile it would act as is not available to it. The error
// satisfies errors.Is(err, ErrSSHKeyNotAuthorized); errors.As to
// *SSHKeyNotAuthorizedError recovers the party the key is not authorized for.
var ErrSSHKeyNotAuthorized = errors.New("service: ssh key not authorized")

// SSHKeyNotAuthorizedError carries the party a key is not authorized for
// ("key not authorized for X"). PartyUUID is the zero UUID when the refusal is
// not about a party.
type SSHKeyNotAuthorizedError struct {
	PartyUUID uuid.UUID
}

func (e *SSHKeyNotAuthorizedError) Error() string { return ErrSSHKeyNotAuthorized.Error() }

// Is makes errors.Is(err, ErrSSHKeyNotAuthorized) succeed.
func (e *SSHKeyNotAuthorizedError) Is(target error) bool { return target == ErrSSHKeyNotAuthorized }

// SSHCredential is a resolved, active ssh_key credential.
type SSHCredential struct {
	CredentialID   int64
	CredentialUUID uuid.UUID
	PrincipalID    int64
	PrincipalUUID  uuid.UUID
	// ConfinedToPartyID is 0 when the credential is not confined.
	ConfinedToPartyID int64
}

// SSHKeyResolver resolves a candidate SSH public key to the active `ssh_key`
// credential that registered it, and derives the AuthContext a repository
// operation runs as. It is the Go shape app-mfgit's git-over-SSH transport
// (mod-repos' transport.KeyResolver) wraps, called once per *candidate* key a
// client offers during the SSH handshake -- including unsigned RFC 4252 "query"
// probes that never authenticate (see mod-repos/api/transport/publickeyauth.go).
//
// ResolveCredential and AuthContextForOwner make four hard guarantees,
// load-bearing for that call site:
//
//   - No Authorize call, and no actor is required on ctx. This is a
//     pre-authentication credential check, in the same category as the
//     POST /v1/auth/login credential check -- there is no authenticated
//     caller yet to authorize.
//   - No writes of any kind. The transport invokes this once per candidate
//     key, including probe keys that never complete a handshake, so any
//     write here -- for example a last-used timestamp -- would let an
//     unauthenticated party mutate state for a key it does not hold.
//   - No caching of any kind. Revocation must take effect on the very next
//     connection (docs/architecture/ssh-keys.md D4); a cache, at any layer,
//     would delay that. Every call issues a fresh, indexed lookup.
//   - No logging of key material. Only derived, non-reversible values (e.g. a
//     fingerprint) are safe to log; this implementation logs nothing at all.
type SSHKeyResolver struct {
	q     usersdb.Querier
	coreQ coredb.Querier
	now   func() time.Time
}

// NewSSHKeyResolver constructs an SSHKeyResolver.
func NewSSHKeyResolver(q usersdb.Querier, coreQ coredb.Querier) *SSHKeyResolver {
	return &SSHKeyResolver{q: q, coreQ: coreQ, now: time.Now}
}

// ResolveCredential resolves key to the active ssh_key credential that holds
// it. See SSHKeyResolver's doc comment for the constraints it upholds.
//
// Returns ErrUnknownSSHKey when key is nil, does not match any active
// registered key (a revoked or expired credential never matches), or matches a
// credential whose principal entity is archived or whose principal is
// disabled. Any other error is a transient or internal failure, wrapped with
// %w, and does not satisfy errors.Is(err, ErrUnknownSSHKey).
func (r *SSHKeyResolver) ResolveCredential(ctx context.Context, key gossh.PublicKey) (SSHCredential, error) {
	if key == nil {
		return SSHCredential{}, ErrUnknownSSHKey
	}

	// Comparing both the fingerprint and the canonical authorized_keys text
	// (rather than either alone) means a stored-row mismatch can never
	// resolve (docs/architecture/ssh-keys.md D11). Both helpers are shared
	// with registration (sshkey.Parse) so the two derive byte-identical
	// values from the same key.
	row, err := r.q.GetActiveSSHKeyByFingerprint(ctx, sshkey.Fingerprint(key))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SSHCredential{}, ErrUnknownSSHKey
		}
		return SSHCredential{}, fmt.Errorf("ssh_key_resolver.ResolveCredential: resolve key: %w", err)
	}
	if row.PublicKey != sshkey.Canonical(key) {
		return SSHCredential{}, ErrUnknownSSHKey
	}
	cred := row.Credential
	if cred.RevokedAt != nil || (cred.ExpiresAt != nil && !cred.ExpiresAt.After(r.now())) {
		return SSHCredential{}, ErrUnknownSSHKey
	}

	entity, err := r.coreQ.GetEntityByID(ctx, cred.PrincipalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A missing principal entity is unreachable in a consistent
			// database (credentials.principal_id is FK'd to principals), but
			// is masked identically to every other resolution failure rather
			// than surfaced as an internal error, matching this method's
			// uniform-refusal contract.
			return SSHCredential{}, ErrUnknownSSHKey
		}
		return SSHCredential{}, fmt.Errorf("ssh_key_resolver.ResolveCredential: load principal entity: %w", err)
	}
	if entity.ArchivedAt != nil {
		return SSHCredential{}, ErrUnknownSSHKey
	}
	principal, err := r.coreQ.GetPrincipalByEntityID(ctx, cred.PrincipalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SSHCredential{}, ErrUnknownSSHKey
		}
		return SSHCredential{}, fmt.Errorf("ssh_key_resolver.ResolveCredential: load principal: %w", err)
	}
	if principal.DisabledAt != nil {
		return SSHCredential{}, ErrUnknownSSHKey
	}

	return SSHCredential{
		CredentialID:      cred.ID,
		CredentialUUID:    cred.Uuid,
		PrincipalID:       cred.PrincipalID,
		PrincipalUUID:     entity.Uuid,
		ConfinedToPartyID: cred.ConfinedToPartyID.Int64,
	}, nil
}

// AuthContextForOwner derives the AuthContext a repository operation on a
// repository owned by ownerID runs as: PrincipalID, ProfileID, CredentialID,
// ACR = the credential kind's base_acr, AuthTime = now. It performs no
// authorization and no writes.
//
// The profile is derived from the owner:
//
//  1. ownerID names a party in which the credential's principal holds an
//     active profile: that profile, when credential_profile_availability is
//     'available' for the credential; otherwise ErrSSHKeyNotAuthorized
//     carrying the party's uuid. An SSH key is never an OIDC factor, so a
//     party whose rule requires a provider always refuses it, as does a party
//     min_acr above the credential kind's base_acr.
//  2. Otherwise (no owner, an owner that is not a party, no active profile in
//     it, or the principal's own user or person): the principal's personal
//     profile, unless the credential is confined to a party, in which case
//     ErrSSHKeyNotAuthorized for the confining party. A personal profile that
//     is not available to the credential (a disabled principal) is refused
//     the same way, without a party.
//  3. A confined credential used against its own party follows rule 1.
func (r *SSHKeyResolver) AuthContextForOwner(ctx context.Context, cred SSHCredential, ownerID *int64) (opctx.AuthContext, error) {
	profileID, err := r.deriveProfile(ctx, cred, ownerID)
	if err != nil {
		return opctx.AuthContext{}, err
	}

	kind, err := r.q.GetCredentialKind(ctx, KindSSHKey)
	if err != nil {
		return opctx.AuthContext{}, fmt.Errorf("ssh_key_resolver.AuthContextForOwner: credential kind: %w", err)
	}
	return opctx.AuthContext{
		PrincipalID:  cred.PrincipalID,
		ProfileID:    profileID,
		CredentialID: cred.CredentialID,
		ACR:          kind.BaseAcr,
		AuthTime:     r.now(),
	}, nil
}

// deriveProfile applies the owner rules of AuthContextForOwner and returns the
// acting profile's entity id.
func (r *SSHKeyResolver) deriveProfile(ctx context.Context, cred SSHCredential, ownerID *int64) (int64, error) {
	if ownerID != nil && *ownerID != 0 {
		partyProfile, found, err := r.activePartyProfile(ctx, cred.PrincipalID, *ownerID)
		if err != nil {
			return 0, err
		}
		if found {
			ok, err := r.available(ctx, cred, partyProfile)
			if err != nil {
				return 0, err
			}
			if !ok {
				return 0, r.notAuthorizedFor(ctx, *ownerID)
			}
			return partyProfile, nil
		}
	}

	if cred.ConfinedToPartyID != 0 {
		return 0, r.notAuthorizedFor(ctx, cred.ConfinedToPartyID)
	}

	profile, err := r.coreQ.GetPersonalProfileByHolder(ctx, cred.PrincipalID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, &SSHKeyNotAuthorizedError{}
		}
		return 0, fmt.Errorf("ssh_key_resolver.AuthContextForOwner: load personal profile: %w", err)
	}
	ok, err := r.available(ctx, cred, profile.EntityID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, &SSHKeyNotAuthorizedError{}
	}
	return profile.EntityID, nil
}

// activePartyProfile returns the entity id of the principal's active profile in
// partyID. found is false when the principal holds none, which includes an
// owner that is not a party.
func (r *SSHKeyResolver) activePartyProfile(ctx context.Context, principalID, partyID int64) (id int64, found bool, err error) {
	profiles, err := r.coreQ.ListProfilesByHolder(ctx, principalID)
	if err != nil {
		return 0, false, fmt.Errorf("ssh_key_resolver.AuthContextForOwner: list profiles: %w", err)
	}
	for _, p := range profiles {
		if p.PartyID.Valid && p.PartyID.Int64 == partyID && p.Status == "active" {
			return p.EntityID, true, nil
		}
	}
	return 0, false, nil
}

// available reports whether credential_profile_availability is 'available' for
// the credential and profile.
func (r *SSHKeyResolver) available(ctx context.Context, cred SSHCredential, profileID int64) (bool, error) {
	availability, err := r.q.CredentialProfileAvailability(ctx, usersdb.CredentialProfileAvailabilityParams{
		CredentialID: cred.CredentialID,
		ProfileID:    profileID,
	})
	if err != nil {
		return false, fmt.Errorf("ssh_key_resolver.AuthContextForOwner: profile availability: %w", err)
	}
	return availability == "available", nil
}

// notAuthorizedFor builds the refusal for a party; a failure to resolve the
// party's uuid degrades to a refusal without it rather than an internal error.
func (r *SSHKeyResolver) notAuthorizedFor(ctx context.Context, partyID int64) error {
	party, err := r.coreQ.GetEntityByID(ctx, partyID)
	if err != nil {
		return &SSHKeyNotAuthorizedError{}
	}
	return &SSHKeyNotAuthorizedError{PartyUUID: party.Uuid}
}
