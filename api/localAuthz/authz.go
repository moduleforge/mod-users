// Package localAuthz is the public facade for the users-module grants-table Authorizer.
// It re-exports the Authorizer type and constructor from internal/authz.
package localAuthz

import (
	"github.com/jackc/pgx/v5/pgxpool"

	authzapi "github.com/moduleforge/authz-api/authz"
	authzdb "github.com/moduleforge/authz-model/db"
	inner "github.com/moduleforge/mod-users/api/internal/authz"
)

// Authorizer is the grants-table implementation of coreAuthz.Authorizer.
type Authorizer = inner.Authorizer

// New constructs an Authorizer backed by the authz grants table.
func New(authzQ authzdb.Querier, opReg *authzapi.OperationRegistry, pool *pgxpool.Pool) *Authorizer {
	return inner.New(authzQ, opReg, pool)
}
