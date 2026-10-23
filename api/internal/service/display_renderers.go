package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/display"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// Type slugs the display renderers are registered for (the fundamental type
// slugs of the entities, not table names).
const (
	displayUserType    = "user"
	displayProfileType = "profile"
)

// RegisterDisplayRenderers registers mod-users' renderers on reg (decision
// U19): `user` renders as the person's name, else the primary email, else
// "Guest"; `profile` renders as "<holder name> (Personal)" or
// "<holder name> @ <party name>". Both register the name field; description
// renders empty. Call it once at startup, after display.NewRegistry.
//
// db serves renders made outside a transaction (a nil tx); a render inside a
// transaction reads through that transaction.
func RegisterDisplayRenderers(reg *display.Registry, db usersdb.DBTX) {
	reg.Register(displayUserType, display.FieldName, func(ctx context.Context, tx pgx.Tx, entityID int64) (string, error) {
		sums, err := ReadPrincipalSummaries(ctx, displayDB(db, tx), []int64{entityID})
		if err != nil {
			return "", err
		}
		sum, ok := sums[entityID]
		if !ok {
			return "", fmt.Errorf("display: user %d: %w", entityID, apiresp.ErrNotFound)
		}
		return sum.DisplayName, nil
	})
	reg.Register(displayProfileType, display.FieldName, func(ctx context.Context, tx pgx.Tx, entityID int64) (string, error) {
		sums, err := readProfileSummaries(ctx, displayDB(db, tx), []int64{entityID})
		if err != nil {
			return "", err
		}
		sum, ok := sums[entityID]
		if !ok {
			return "", fmt.Errorf("display: profile %d: %w", entityID, apiresp.ErrNotFound)
		}
		return sum.DisplayName, nil
	})
	empty := func(context.Context, pgx.Tx, int64) (string, error) { return "", nil }
	reg.Register(displayUserType, display.FieldDescription, empty)
	reg.Register(displayProfileType, display.FieldDescription, empty)
}

// displayDB picks the connection a render reads through: the transaction when
// one is given, else the registered fallback.
func displayDB(fallback usersdb.DBTX, tx pgx.Tx) usersdb.DBTX {
	if tx != nil {
		return tx
	}
	return fallback
}
