// Package usersservice is the public facade for the users-module UserAccountService.
// It re-exports the service type and constructor from internal/service.
package usersservice

import (
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	coreservice "github.com/moduleforge/core-api/service"
	"github.com/moduleforge/core-api/txhelper"
	"github.com/moduleforge/core-api/types"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/users-module/model/db"

	inner "github.com/moduleforge/users-module/api/internal/service"
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
