// Package usersservice is the public facade for the users-module UserAccountService.
// It re-exports the service type and constructor from internal/service.
package usersservice

import (
	"context"

	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	coreservice "github.com/moduleforge/core-api/service"
	"github.com/moduleforge/core-api/txhelper"
	"github.com/moduleforge/core-api/types"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"
	gossh "golang.org/x/crypto/ssh"

	inner "github.com/moduleforge/mod-users/api/internal/service"
)

// UserAccountService manages user account CRUD.
type UserAccountService = inner.UserAccountService

// NewUserAccountService constructs a UserAccountService.
func NewUserAccountService(
	pool txhelper.DB,
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
	npService coreservice.NaturalPersonServicer,
	typeRes *types.Resolver,
	hashPassword func(plain string) (string, error),
) *UserAccountService {
	return inner.NewUserAccountService(pool, q, coreQ, az, obs, npService, typeRes, hashPassword)
}

// SSHKeyService manages SSH public-key registration, listing, and
// revocation (design note ../../plan/notes/ssh-key-design.md).
type SSHKeyService = inner.SSHKeyService

// SSHKey is the public view of one registered SSH public key.
type SSHKey = inner.SSHKey

// SSHKeyResolver resolves a candidate SSH public key to the entity id of
// the account that registered it. See inner.SSHKeyResolver's doc comment
// for the hard constraints it upholds: no authorization, no writes, no
// caching, no key-material logging.
type SSHKeyResolver = inner.SSHKeyResolver

// ErrUnknownSSHKey is re-exported as the same value as
// inner.ErrUnknownSSHKey (not copied into a new sentinel) so
// errors.Is(err, usersservice.ErrUnknownSSHKey) matches across the facade
// boundary for a caller that only imports this package.
var ErrUnknownSSHKey = inner.ErrUnknownSSHKey

// NewSSHKeyService constructs an SSHKeyService.
func NewSSHKeyService(
	pool txhelper.DB,
	q usersdb.Querier,
	coreQ coredb.Querier,
	az coreAuthz.Authorizer,
	obs *observer.ObserverGroup,
) *SSHKeyService {
	return inner.NewSSHKeyService(pool, q, coreQ, az, obs)
}

// NewSSHKeyResolver constructs an SSHKeyResolver.
func NewSSHKeyResolver(q usersdb.Querier, coreQ coredb.Querier) *SSHKeyResolver {
	return inner.NewSSHKeyResolver(q, coreQ)
}

// sshKeyResolverActor is a local, one-method interface used only as a
// compile-time assertion that *SSHKeyResolver exposes the exact method
// shape mod-repos' transport.KeyResolver requires (design note D11) --
// without this package importing mod-repos, which mod-users must not do.
type sshKeyResolverActor interface {
	ResolveActor(context.Context, gossh.PublicKey) (int64, error)
}

var _ sshKeyResolverActor = (*SSHKeyResolver)(nil)
