package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	db "github.com/moduleforge/users-module/model/db"
)

// identityQuerier is the subset of db.Querier needed by IdentityCounts.
// Defined at the point of use so tests can inject stubs without importing
// the full db.Querier interface.
type identityQuerier interface {
	CountOIDCIdentitiesByUserAccount(ctx context.Context, userAccountID int64) (int64, error)
	GetAuthLocal(ctx context.Context, userAccountID int64) (db.AuthLocal, error)
}

// IdentityCounts returns the count of OIDC identities and whether an auth_local
// row exists for userAccountID. Both values reflect the current committed state;
// callers that need consistent read-then-delete semantics must call this within
// the same transaction (pass db.New(tx) as q).
func IdentityCounts(ctx context.Context, q identityQuerier, userAccountID int64) (oidc int64, hasLocal bool, err error) {
	oidc, err = q.CountOIDCIdentitiesByUserAccount(ctx, userAccountID)
	if err != nil {
		return 0, false, fmt.Errorf("identity_counts: count oidc: %w", err)
	}

	_, localErr := q.GetAuthLocal(ctx, userAccountID)
	if localErr == nil {
		hasLocal = true
	} else if localErr == pgx.ErrNoRows {
		hasLocal = false
	} else {
		return 0, false, fmt.Errorf("identity_counts: check auth_local: %w", localErr)
	}

	return oidc, hasLocal, nil
}
