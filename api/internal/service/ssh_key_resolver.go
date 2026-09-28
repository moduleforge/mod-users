package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	gossh "golang.org/x/crypto/ssh"

	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/sshkey"
	db "github.com/moduleforge/mod-users/model/db"
)

// ErrUnknownSSHKey is returned by SSHKeyResolver.ResolveActor for a key
// that does not resolve to any known, currently-usable account: an
// unregistered key, a revoked key, and a key whose account holder entity is
// archived all return this exact sentinel, with no distinguishing wrapping
// between the three cases (design note D11) -- ResolveActor never reveals
// more than "resolution failed" to its caller.
var ErrUnknownSSHKey = errors.New("service: unknown ssh public key")

// SSHKeyResolver resolves a candidate SSH public key to the account_holder
// entity id of the user account that registered it. It is the Go shape
// app-mfgit's future git-over-SSH transport (mod-repos' transport.
// KeyResolver) wraps, called once per *candidate* key a client offers
// during the SSH handshake -- including unsigned RFC 4252 "query" probes
// that never authenticate (see mod-repos/api/transport/publickeyauth.go).
//
// ResolveActor makes four hard guarantees, load-bearing for that call site:
//
//   - No Authorize call, and no actor is required on ctx. This is a
//     pre-authentication credential check, in the same category as the
//     POST /v1/auth/login credential check -- there is no authenticated
//     caller yet to authorize, and requiring one would make the method
//     unusable at its only call site.
//   - No writes of any kind. The transport invokes this once per candidate
//     key, including probe keys that never complete a handshake, so any
//     write here -- for example a last-used timestamp -- would let an
//     unauthenticated party mutate state for a key it does not hold.
//   - No caching of any kind. Revocation must take effect on the very next
//     connection (design note D4); a cache, at any layer, would delay
//     that. Every call issues a fresh, indexed lookup.
//   - No logging of key material. Only derived, non-reversible values
//     (e.g. a fingerprint) are safe to log; this implementation logs
//     nothing at all.
type SSHKeyResolver struct {
	q     db.Querier
	coreQ coredb.Querier
}

// NewSSHKeyResolver constructs an SSHKeyResolver.
func NewSSHKeyResolver(q db.Querier, coreQ coredb.Querier) *SSHKeyResolver {
	return &SSHKeyResolver{q: q, coreQ: coreQ}
}

// ResolveActor resolves key to the account_holder entity id of the user
// account that registered it. See SSHKeyResolver's doc comment for the
// hard constraints this method upholds: no authorization, no writes, no
// caching, no key-material logging.
//
// Returns ErrUnknownSSHKey when key is nil, does not match any active
// registered key, or matches a key whose account holder entity is
// archived. Any other error is a transient or internal failure, wrapped
// with %w, and does not satisfy errors.Is(err, ErrUnknownSSHKey).
func (r *SSHKeyResolver) ResolveActor(ctx context.Context, key gossh.PublicKey) (int64, error) {
	if key == nil {
		return 0, ErrUnknownSSHKey
	}

	// Comparing both the fingerprint and the canonical authorized_keys text
	// (rather than either alone) means a stored-row mismatch can never
	// resolve (design note D11). Both helpers are shared with registration
	// (sshkey.Parse) so the two derive byte-identical values from the same
	// key.
	accountHolder, err := r.q.ResolveActiveSSHPublicKey(ctx, db.ResolveActiveSSHPublicKeyParams{
		FingerprintSha256: sshkey.Fingerprint(key),
		PublicKey:         sshkey.Canonical(key),
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, ErrUnknownSSHKey
		}
		return 0, fmt.Errorf("ssh_key_resolver.ResolveActor: resolve key: %w", err)
	}

	entity, err := r.coreQ.GetEntityByID(ctx, accountHolder)
	if err != nil {
		if err == pgx.ErrNoRows {
			// A missing account holder entity is unreachable in a
			// consistent database (user_accounts.account_holder is FK'd
			// to entities), but is masked identically to every other
			// resolution failure rather than surfaced as an internal
			// error, matching this method's uniform-refusal contract.
			return 0, ErrUnknownSSHKey
		}
		return 0, fmt.Errorf("ssh_key_resolver.ResolveActor: load account holder: %w", err)
	}
	if entity.ArchivedAt != nil {
		return 0, ErrUnknownSSHKey
	}

	return accountHolder, nil
}
