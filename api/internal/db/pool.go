// Package db provides a production-ready pgx connection pool with
// mode-aware sizing and health-check on creation.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/users-module/api/internal/config"
)

// New creates a pgx v5 connection pool configured from cfg.DB,
// pings the database to verify connectivity, and returns the pool.
func New(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DB.URL)
	if err != nil {
		return nil, fmt.Errorf("db: parse connection string: %w", err)
	}

	poolCfg.MaxConns = int32(cfg.DB.MaxConns)
	poolCfg.MaxConnLifetime = cfg.DB.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.DB.MaxConnIdleTime
	poolCfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping failed: %w", err)
	}

	return pool, nil
}
