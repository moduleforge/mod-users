// Package config — signing_key_bootstrap implements the fetch-or-generate-
// and-durably-persist fallback for the access-token signing key: when
// JWT_SIGNING_KEY is absent, Load() consults Postgres instead of failing boot.
// See Load's own doc comment for the full precedence description.
package config

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	db "github.com/moduleforge/mod-users/model/db"
)

// SigningKeyQuerier is the minimal persistence contract
// fetchOrGeneratePersistedSigningKey needs. Satisfied structurally by
// *db.Queries (github.com/moduleforge/mod-users/model/db).
type SigningKeyQuerier interface {
	GetSigningKey(ctx context.Context) (db.AuthSigningKey, error)
	InsertSigningKeyIfAbsent(ctx context.Context, privateKey []byte) (int64, error)
}

// fetchOrGeneratePersistedSigningKey fetches the persisted Ed25519 signing
// key if one exists, or generates and durably persists a new one. Concurrent
// first-boot callers are safe at the row level: the single row is capped by
// its id = 1 primary key, the insert is ON CONFLICT DO NOTHING, and every
// caller reads the winner's key back. The caller (bootstrapSigningKeyFromDB)
// additionally holds a session-level advisory lock around the whole
// sequence, because the table may not exist yet the first time this runs.
// A persisted key that does not parse as a PKCS#8 Ed25519 key is a
// fail-loudly error, never silently regenerated (regenerating would
// invalidate every outstanding access token and the derived OAuth-state key).
func fetchOrGeneratePersistedSigningKey(ctx context.Context, q SigningKeyQuerier) (ed25519.PrivateKey, error) {
	row, err := q.GetSigningKey(ctx)
	switch {
	case err == nil:
		return parsePersistedSigningKey(row.PrivateKey)
	case errors.Is(err, pgx.ErrNoRows):
		// fall through to generate-and-persist
	default:
		return nil, fmt.Errorf("signingkey: read persisted key: %w", err)
	}

	_, candidate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("signingkey: generate key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(candidate)
	if err != nil {
		return nil, fmt.Errorf("signingkey: encode key: %w", err)
	}

	if _, err := q.InsertSigningKeyIfAbsent(ctx, der); err != nil {
		return nil, fmt.Errorf("signingkey: persist generated key: %w", err)
	}

	// Whether this caller inserted the row or lost the race, the persisted
	// row is the single source of truth: read it back.
	winner, err := q.GetSigningKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("signingkey: re-read persisted key: %w", err)
	}
	return parsePersistedSigningKey(winner.PrivateKey)
}

// parsePersistedSigningKey decodes a persisted PKCS#8 DER Ed25519 private
// key. Errors are fixed strings that never echo key material.
func parsePersistedSigningKey(der []byte) (ed25519.PrivateKey, error) {
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("signingkey: persisted key is not a valid PKCS#8 private key (corrupt or truncated)")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("signingkey: persisted key is not an Ed25519 private key")
	}
	return key, nil
}

// signingKeyBootstrapLockKey is a Postgres session-level advisory-lock key,
// namespaced via an FNV-1a hash of a distinctive string so it does not
// collide with any other advisory lock use on the same database.
var signingKeyBootstrapLockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("mod-users:auth_signing_keys:bootstrap"))
	return int64(h.Sum64())
}()

// bootstrapSigningKeySchemaDDL and bootstrapSigningKeyTableDDL must each stay
// byte-for-byte identical to the corresponding statement of
// model/migrations/sql/0100_baseline.sql (see the auth_signing_keys section's
// own comment for why IF NOT EXISTS is required there). A drift-guard test in
// signing_key_bootstrap_test.go asserts the baseline contains both verbatim.
const bootstrapSigningKeySchemaDDL = `CREATE SCHEMA IF NOT EXISTS mod_users;`

const bootstrapSigningKeyTableDDL = `CREATE TABLE IF NOT EXISTS mod_users.auth_signing_keys (
  id          SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  private_key BYTEA NOT NULL CHECK (octet_length(private_key) >= 48),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);`

// bootstrapSigningKeyFromDB opens its own short-lived, dedicated connection
// (config.Load() is the first call in every generated main.go, so the
// application's pool does not exist yet) using dbURL and fetches-or-
// generates-and-persists the signing key. Bounded by a fixed timeout so a
// boot with an unreachable database fails fast rather than hanging.
func bootstrapSigningKeyFromDB(ctx context.Context, dbURL string) (ed25519.PrivateKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("signingkey: connect: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", signingKeyBootstrapLockKey); err != nil {
		return nil, fmt.Errorf("signingkey: acquire advisory lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Closing the connection also releases a session-level lock, so a
		// failed unlock is not fatal.
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", signingKeyBootstrapLockKey)
	}()

	// Ensure the schema and table exist: config.Load() runs before this
	// module's own migrations apply, so on a fresh database neither exists
	// yet. Idempotent and serialized by the advisory lock above.
	for _, ddl := range []string{bootstrapSigningKeySchemaDDL, bootstrapSigningKeyTableDDL} {
		if _, err := conn.Exec(ctx, ddl); err != nil {
			return nil, fmt.Errorf("signingkey: ensure table: %w", err)
		}
	}

	q := db.New(conn) // *pgx.Conn satisfies db.DBTX
	return fetchOrGeneratePersistedSigningKey(ctx, q)
}
