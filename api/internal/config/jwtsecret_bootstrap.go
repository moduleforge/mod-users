// Package config — jwtsecret_bootstrap implements the fetch-or-generate-
// and-durably-persist fallback for JWT_SECRET: when the env var is
// absent, Load() consults Postgres instead of failing boot outright. See
// Load's own doc comment for the full precedence description.
package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	db "github.com/moduleforge/mod-users/model/db"
)

// JWTSecretQuerier is the minimal persistence contract
// fetchOrGeneratePersistedJWTSecret needs. Satisfied structurally by
// *db.Queries (github.com/moduleforge/mod-users/model/db, imported here
// under the same "db" alias provider_merge.go already uses) — this
// package already imports that package, so no new intra-module
// dependency is introduced.
type JWTSecretQuerier interface {
	GetJWTSecret(ctx context.Context) (string, error)
	InsertJWTSecretIfAbsent(ctx context.Context, secret string) (string, error)
}

const jwtSecretByteLen = 32 // 256 bits; hex-encoded to a 64-char string.

// fetchOrGeneratePersistedJWTSecret fetches the persisted secret if one
// exists, or generates and durably persists a new one. Concurrent
// first-boot callers are safe at the row level (ON CONFLICT DO
// NOTHING); the caller (bootstrapJWTSecretFromDB) additionally holds a
// session-level advisory lock around the whole sequence, needed
// because — unlike mod-core's analogous case — the target table may
// not exist yet the first time this runs (see 001's header note),
// which ON CONFLICT alone does not protect against. A persisted
// secret that fails the length check is a fail-loudly error, never
// silently regenerated.
func fetchOrGeneratePersistedJWTSecret(ctx context.Context, q JWTSecretQuerier) (string, error) {
	secret, err := q.GetJWTSecret(ctx)
	switch {
	case err == nil:
		if len(secret) < jwtSecretByteLen*2 { // hex-encoded length
			return "", fmt.Errorf("jwtsecret: persisted secret is %d chars, want >= %d (corrupt or truncated)", len(secret), jwtSecretByteLen*2)
		}
		return secret, nil
	case errors.Is(err, pgx.ErrNoRows):
		// fall through to generate-and-persist
	default:
		return "", fmt.Errorf("jwtsecret: read persisted secret: %w", err)
	}

	candidate := make([]byte, jwtSecretByteLen)
	if _, rerr := rand.Read(candidate); rerr != nil {
		return "", fmt.Errorf("jwtsecret: generate secret: %w", rerr)
	}
	encoded := hex.EncodeToString(candidate)

	inserted, err := q.InsertJWTSecretIfAbsent(ctx, encoded)
	switch {
	case err == nil:
		return inserted, nil // we won the race; DB echoes back what we inserted
	case errors.Is(err, pgx.ErrNoRows):
		// ON CONFLICT DO NOTHING skipped our row: another caller won.
		winner, rerr := q.GetJWTSecret(ctx)
		if rerr != nil {
			return "", fmt.Errorf("jwtsecret: re-fetch secret after lost race: %w", rerr)
		}
		return winner, nil
	default:
		return "", fmt.Errorf("jwtsecret: persist generated secret: %w", err)
	}
}

// jwtSecretBootstrapLockKey is a Postgres session-level advisory-lock
// key, namespaced via FNV-1a hash of a distinctive string so it does
// not collide with any other advisory lock use on the same database.
// No other advisory lock use was found in mod-core, mod-audit,
// mod-authz, mod-tags, mod-tasks, or mod-users at the time this was
// written (see plan/overview.md's research) — still, hash rather than
// hardcode a small int to keep the collision probability negligible if
// that ever changes.
var jwtSecretBootstrapLockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("mod-users:auth_jwt_secrets:bootstrap"))
	return int64(h.Sum64())
}()

// bootstrapJWTSecretDDL must stay byte-for-byte identical to the Up
// block of model/migrations/sql/0102_auth_jwt_secrets.sql (see that
// file's own header comment for why IF NOT EXISTS is required here).
// A drift-guard test in generate_test.go asserts this file's content
// contains this exact string.
const bootstrapJWTSecretDDL = `CREATE TABLE IF NOT EXISTS auth_jwt_secrets (
  id         SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  secret     TEXT NOT NULL CHECK (char_length(secret) >= 32),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`

// bootstrapJWTSecretFromDB opens its own short-lived, dedicated
// connection (not the pool the composing app's generated main.go
// builds later — that pool does not exist yet; config.Load() is the
// first call in every generated main.go) using dbURL — the value
// resolveDBURL() already produced — and fetches-or-generates-and-
// persists the JWT secret. Bounded by a fixed timeout so a boot with
// an unreachable database fails fast rather than hanging.
func bootstrapJWTSecretFromDB(ctx context.Context, dbURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return "", fmt.Errorf("jwtsecret: connect: %w", err)
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", jwtSecretBootstrapLockKey); err != nil {
		return "", fmt.Errorf("jwtsecret: acquire advisory lock: %w", err)
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", jwtSecretBootstrapLockKey)

	// Ensure the table exists: config.Load() runs before this
	// module's own migrations apply (see 001's header note), so on a
	// truly fresh database this table does not exist yet. Idempotent;
	// safe to run every boot, and safe under the advisory lock above
	// even though CREATE TABLE IF NOT EXISTS alone is not safe against
	// genuinely concurrent, unserialized callers.
	if _, err := conn.Exec(ctx, bootstrapJWTSecretDDL); err != nil {
		return "", fmt.Errorf("jwtsecret: ensure table: %w", err)
	}

	q := db.New(conn) // *pgx.Conn satisfies db.DBTX
	return fetchOrGeneratePersistedJWTSecret(ctx, q)
}
