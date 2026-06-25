// Package localdb is the public facade for users-module database pool construction.
// It re-exports the pool constructor from internal/db so external modules can
// build the *pgxpool.Pool used by the users module.
package localdb

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/users-module/api/config"
	inner "github.com/moduleforge/users-module/api/internal/db"
)

// New constructs a *pgxpool.Pool from the provided configuration.
// It reads DB.URL and pool-tuning fields from cfg.
func New(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	return inner.New(ctx, cfg)
}
