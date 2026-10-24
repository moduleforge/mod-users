//go:build integration

package config

// signing_key_bootstrap_integration_test.go proves, against a real Postgres,
// the properties the unit tests in signing_key_bootstrap_test.go cannot: a
// genuinely fresh, unmigrated database (config.Load() runs before this
// module's own migrations apply -- see 001's and 002's header notes), a
// genuinely concurrent multi-caller first boot against it (the
// table-creation race the advisory lock protects, which ON CONFLICT DO
// NOTHING alone does not), and a real CHECK-constraint rejection of a
// corrupt signing key.
//
// Structural precedent: api/internal/authz/authz_integration_test.go
// establishes the TestMain / prerequisite-check / host-resolution /
// shadow-DB-reset pattern this file follows (see that file's header comment
// for the shared-container convention and the AUTHZ_DEV_PG_HOST=localhost
// Docker Desktop for macOS caveat -- reused verbatim here rather than
// inventing a new host-resolution env var, per this task's own instruction).
//
// Migrations: mod-users' own plain migrations (usersmigrations,
// model/migrations) are not self-contained -- the baseline migration 0100_baseline.sql
// (the single usersmigrations.Migrate migration, which also creates mod_users.auth_signing_keys) carries FKs
// into core-model tables (legal_entities, apps) that a mod-users-only
// database never creates. Running usersmigrations.Migrate in-process
// against a bare shadow DB therefore fails with SQLSTATE 42P01
// (undefined_table) on the baseline. This suite avoids that entirely by following
// authz_integration_test.go's own precedent: applying the pre-built
// composed migrations directory (core + authz + users, produced by `make -C
// model compose`) via the goose CLI directly, exactly as that suite's
// resetDB does -- see migrationsDir/runComposedMigrations below. This means
// the composed dir must exist before running this suite; run `make -C model
// compose` (or `make dev.start`, which builds it as a dependency) first.
// checkSigningKeyIntegPrereqs treats a missing composed dir as a missing
// prerequisite (clean skip with an actionable message), mirroring
// authz_integration_test.go's checkPrereqs.
//
// This suite's shadow DB (signing_key_integ_users) is distinct from
// authz_integ_users, so both suites can share the same
// "users-module-postgres" container without colliding. "Genuinely empty"
// here means dropping the whole shadow database (not merely truncating
// tables), since the fresh-database and concurrent-race scenarios below
// specifically need no goose_db_version_users table present either -- see
// resetSigningKeyShadowDB. The corruption-detection scenario
// (TestInteg_AuthSigningKeys_CheckConstraint_RejectsShortKey) creates
// mod_users.auth_signing_keys via the bootstrap DDL directly (byte-identical to
// the baseline migration's own DDL -- see the drift-guard unit test) instead of via
// a full migration run, so it always exercises the real CHECK constraint
// whenever the DB is reachable at all, independent of the composed-dir
// prerequisite above.
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header,
// replacing the package path with ./internal/config/...:
//
//	(cd api && AUTHZ_DEV_PG_HOST=127.0.0.1 AUTHZ_DEV_PG_PORT="$port" \
//	   go test -tags=integration -p 1 -count=1 ./internal/config/...)

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	usersmigrations "github.com/moduleforge/mod-users/model/migrations"
)

// ---------------------------------------------------------------------------
// Package-level state
// ---------------------------------------------------------------------------

// signingKeyIntegDB is this suite's dedicated shadow database, distinct from
// authz_integ_users (authz_integration_test.go) and any other suite's
// shadow DB on the same shared Postgres container.
const signingKeyIntegDB = "signing_key_integ_users"

// signingKeyIntegContainer is the shared Postgres container reused by every
// mod-users integration suite in this environment (see
// authz_integration_test.go's header comment).
const signingKeyIntegContainer = "users-module-postgres"

// signingKeyIntegMigrationsDirEnvVar overrides migrationsDir()'s resolved
// path, for environments where the test binary does not run from its normal
// location inside a mod-users checkout. Unset by default. Mirrors
// authz_integration_test.go's integMigrationsDirEnvVar (AUTHZ_INTEG_MIGRATIONS_DIR),
// kept as a distinct, file-scoped constant rather than shared across
// packages.
const signingKeyIntegMigrationsDirEnvVar = "SIGNING_KEY_INTEG_MIGRATIONS_DIR"

// integPGHost is resolved once in TestMain and reused by every test.
var integPGHost string

// migrationsDir resolves the composed schema dir (core + authz + users,
// produced by mod-users/model/Makefile's `compose` target) relative to this
// source file's own location, mirroring authz_integration_test.go's own
// migrationsDir. This file lives at
// <repo>/api/internal/config/signing_key_bootstrap_integration_test.go; the
// composed schema lives at <repo>/model/schema/migrations.
func migrationsDir() string {
	if d := os.Getenv(signingKeyIntegMigrationsDirEnvVar); d != "" {
		return d
	}
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "model", "schema", "migrations")
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func TestMain(m *testing.M) {
	pgHost := resolveSigningKeyIntegHost()
	if err := checkSigningKeyIntegPrereqs(pgHost); err != nil {
		fmt.Fprintf(os.Stderr, "integration: skipping signing-key bootstrap tests — %v\n", err)
		os.Exit(0) // exit 0 so `go test` reports skipped, not failed.
	}
	integPGHost = pgHost
	os.Exit(m.Run())
}

// checkSigningKeyIntegPrereqs verifies docker, the shared container, and
// goose are available (matching authz_integration_test.go's own prerequisite
// check), that the composed migrations dir this suite's two
// migration-dependent scenarios apply via goose exists (mirroring that same
// suite's own composed-dir check), and additionally probes a real
// connection with the expected "users"/"users" role. The container being
// "running" per docker inspect does not guarantee it is actually
// provisioned with that role -- a stale data volume, or a container started
// for an unrelated purpose, can leave it running yet unusable for this
// suite's own DSNs. Treating a probe failure as a missing prerequisite
// (clean skip) avoids every test in this file failing individually on it
// later.
func checkSigningKeyIntegPrereqs(pgHost string) error {
	// An explicit AUTHZ_DEV_PG_HOST means the caller supplied their own
	// Postgres (e.g. a throwaway container); skip the shared-container check,
	// but refuse the default port unless confirmed (this suite drops and
	// recreates a database).
	if os.Getenv("AUTHZ_DEV_PG_HOST") == "" {
		cmd := exec.Command("docker", "inspect", "--format={{.State.Running}}", signingKeyIntegContainer)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("docker inspect: %w", err)
		}
		if strings.TrimSpace(string(out)) != "true" {
			return fmt.Errorf("container %s is not running", signingKeyIntegContainer)
		}
	} else if resolveSigningKeyIntegPort() == "5432" && os.Getenv("AUTHZ_DEV_PG_CONFIRM_SHARED") != "1" {
		return fmt.Errorf("refusing to run: AUTHZ_DEV_PG_HOST is set but AUTHZ_DEV_PG_PORT is unset or 5432 (likely a shared Postgres); " +
			"set AUTHZ_DEV_PG_PORT to a throwaway container's port, or AUTHZ_DEV_PG_CONFIRM_SHARED=1 to confirm using the shared instance")
	}
	if _, err := exec.LookPath("goose"); err != nil {
		return fmt.Errorf("goose not in PATH: %w", err)
	}
	if dir := migrationsDir(); !dirExists(dir) {
		return fmt.Errorf(
			"composed migrations dir %s not found — run `make -C model compose` (or `make dev.start`, which builds it as a dependency) first, or set %s",
			dir, signingKeyIntegMigrationsDirEnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, signingKeyIntegAdminDSN(pgHost))
	if err != nil {
		return fmt.Errorf("connect to %s as the expected users/users role: %w", signingKeyIntegContainer, err)
	}
	conn.Close(context.Background())

	return nil
}

// resolveSigningKeyIntegHost mirrors authz_integration_test.go's resolveHost:
// AUTHZ_DEV_PG_HOST always wins when set (the documented
// AUTHZ_DEV_PG_HOST=localhost Docker Desktop for macOS convention -- reused
// verbatim, per this task's own instruction, rather than inventing a new
// host-resolution env var), falling back to the container's Docker-network
// IP, and finally the historical hard-coded sandbox IP.
func resolveSigningKeyIntegHost() string {
	if h := os.Getenv("AUTHZ_DEV_PG_HOST"); h != "" {
		return h
	}
	cmd := exec.Command("docker", "inspect",
		"--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", signingKeyIntegContainer)
	out, err := cmd.Output()
	if err == nil {
		if ip := strings.TrimSpace(string(out)); ip != "" {
			return ip
		}
	}
	return "172.23.0.3"
}

// resolveSigningKeyIntegPort returns the Postgres host port: AUTHZ_DEV_PG_PORT
// (same variable as authz_integration_test.go), default 5432.
func resolveSigningKeyIntegPort() string {
	if p := os.Getenv("AUTHZ_DEV_PG_PORT"); p != "" {
		return p
	}
	return "5432"
}

func signingKeyIntegAdminDSN(pgHost string) string {
	return fmt.Sprintf("postgres://users:users@%s:%s/postgres?sslmode=disable", pgHost, resolveSigningKeyIntegPort())
}

func signingKeyIntegShadowDSN(pgHost string) string {
	return fmt.Sprintf("postgres://users:users@%s:%s/%s?sslmode=disable", pgHost, resolveSigningKeyIntegPort(), signingKeyIntegDB)
}

// resetSigningKeyShadowDB drops and recreates signing_key_integ_users so every
// test starts from a genuinely empty database: no auth_signing_keys table, no
// goose_db_version_users table, nothing -- not merely truncated tables. This
// is what the fresh-database and concurrent-race scenarios below need, and
// is a superset of what the already-migrated scenario needs.
func resetSigningKeyShadowDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, signingKeyIntegAdminDSN(integPGHost))
	if err != nil {
		t.Fatalf("reset shadow db: connect admin: %v", err)
	}
	defer conn.Close(ctx)

	for _, stmt := range []string{
		"DROP DATABASE IF EXISTS " + signingKeyIntegDB,
		"CREATE DATABASE " + signingKeyIntegDB,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("reset shadow db: %s: %v", stmt, err)
		}
	}
}

// runComposedMigrations applies the pre-built composed migrations dir (core
// + authz + users, produced by `make -C model compose`) against dsn via the
// goose CLI directly -- the same approach authz_integration_test.go's
// resetDB uses -- rather than calling usersmigrations.Migrate in-process.
// The baseline migration 0100_baseline.sql (which also creates mod_users.auth_signing_keys) has
// FKs into core-model tables (legal_entities, apps) that a mod-users-only
// database never creates; the composed dir already includes mod-core's own
// migrations, so those FKs resolve and this never hits SQLSTATE 42P01
// (undefined_table) the way applying usersmigrations.Migrate alone against
// a bare shadow DB would. -table pins the goose version-tracking table to
// usersmigrations.TableName ("goose_db_version_users") so this run's
// bookkeeping lands in the same table the callers below (and
// usersmigrations.Migrate itself, were it ever run against this same DB)
// read and write, keeping the two converge-able.
func runComposedMigrations(t *testing.T, dsn string) {
	t.Helper()
	cmd := exec.Command("goose", //nolint:gosec // fixed args/resolved paths, not user input
		"-dir", migrationsDir(),
		"-table", usersmigrations.TableName,
		"postgres", dsn, "up")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("goose up (composed migrations): %v\n%s", err, out)
	}
}

// ---------------------------------------------------------------------------
// Real fetch-or-generate round trip against an already-migrated DB
// ---------------------------------------------------------------------------

// TestInteg_BootstrapSigningKey_MigratedDB_RoundTrip applies the composed
// migrations, then calls bootstrapSigningKeyFromDB twice: the first call
// generates and persists a key; the second must return the same key.
func TestInteg_BootstrapSigningKey_MigratedDB_RoundTrip(t *testing.T) {
	resetSigningKeyShadowDB(t)
	dsn := signingKeyIntegShadowDSN(integPGHost)
	runComposedMigrations(t, dsn)

	first, err := bootstrapSigningKeyFromDB(context.Background(), dsn)
	if err != nil {
		t.Fatalf("first bootstrap call: %v", err)
	}
	second, err := bootstrapSigningKeyFromDB(context.Background(), dsn)
	if err != nil {
		t.Fatalf("second bootstrap call: %v", err)
	}
	if !second.Equal(first) {
		t.Error("second call returned a different key than the first; it must return the persisted key")
	}
}

// ---------------------------------------------------------------------------
// Real fresh-database round trip
// ---------------------------------------------------------------------------

// TestInteg_BootstrapSigningKey_FreshUnmigratedDB_CreatesTableAndConvergesWithGoose
// bootstraps against a genuinely empty database (no mod_users schema, no
// goose table), then applies the composed migrations and asserts the
// baseline's CREATE ... IF NOT EXISTS converges with the bootstrap's own DDL.
func TestInteg_BootstrapSigningKey_FreshUnmigratedDB_CreatesTableAndConvergesWithGoose(t *testing.T) {
	resetSigningKeyShadowDB(t)
	dsn := signingKeyIntegShadowDSN(integPGHost)

	if _, err := bootstrapSigningKeyFromDB(context.Background(), dsn); err != nil {
		t.Fatalf("bootstrap against a genuinely fresh, unmigrated DB: %v", err)
	}

	runComposedMigrations(t, dsn)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to verify migration state: %v", err)
	}
	defer conn.Close(ctx)

	var applied bool
	const versionSQL = `SELECT is_applied FROM goose_db_version_users WHERE version_id = 100 ORDER BY id DESC LIMIT 1`
	if err := conn.QueryRow(ctx, versionSQL).Scan(&applied); err != nil {
		t.Fatalf("query goose_db_version_users for baseline migration 100: %v", err)
	}
	if !applied {
		t.Errorf("goose_db_version_users records baseline migration 100 as not applied, want applied=true")
	}

	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM mod_users.auth_signing_keys").Scan(&count); err != nil {
		t.Fatalf("count auth_signing_keys rows after convergence: %v", err)
	}
	if count != 1 {
		t.Errorf("auth_signing_keys row count after bootstrap+migrate = %d, want 1", count)
	}
}

// ---------------------------------------------------------------------------
// Real concurrent-race proof, including the table-creation race
// ---------------------------------------------------------------------------

// TestInteg_BootstrapSigningKey_ConcurrentFirstBoot_SingleWinnerAcrossAllCallers
// launches 8 concurrent bootstrap callers against a genuinely empty database
// and asserts they all converge on one key and one row: the advisory lock
// closes the schema/table-creation race that ON CONFLICT DO NOTHING alone
// does not.
func TestInteg_BootstrapSigningKey_ConcurrentFirstBoot_SingleWinnerAcrossAllCallers(t *testing.T) {
	resetSigningKeyShadowDB(t)
	dsn := signingKeyIntegShadowDSN(integPGHost)
	const n = 8

	var wg sync.WaitGroup
	keys := make([]ed25519.PrivateKey, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			keys[i], errs[i] = bootstrapSigningKeyFromDB(context.Background(), dsn)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: bootstrapSigningKeyFromDB failed: %v", i, err)
		}
	}
	for i, k := range keys {
		if k == nil || !k.Equal(keys[0]) {
			t.Errorf("goroutine %d returned a different key; every concurrent first-boot caller must converge", i)
		}
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to verify row count: %v", err)
	}
	defer conn.Close(ctx)

	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM mod_users.auth_signing_keys").Scan(&count); err != nil {
		t.Fatalf("count auth_signing_keys rows after concurrent boot: %v", err)
	}
	if count != 1 {
		t.Errorf("auth_signing_keys row count after %d concurrent bootstrap callers = %d, want exactly 1", n, count)
	}
}

// ---------------------------------------------------------------------------
// Real corruption-detection proof
// ---------------------------------------------------------------------------

// TestInteg_AuthSigningKeys_CheckConstraint_RejectsShortKey creates the table
// via the bootstrap DDL (byte-identical to the baseline; see the drift-guard
// unit test) and asserts the octet_length(private_key) >= 48 CHECK rejects a
// truncated key at the schema level.
func TestInteg_AuthSigningKeys_CheckConstraint_RejectsShortKey(t *testing.T) {
	resetSigningKeyShadowDB(t)
	dsn := signingKeyIntegShadowDSN(integPGHost)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	for _, ddl := range []string{bootstrapSigningKeySchemaDDL, bootstrapSigningKeyTableDDL} {
		if _, err := conn.Exec(ctx, ddl); err != nil {
			t.Fatalf("create auth_signing_keys table: %v", err)
		}
	}

	_, err = conn.Exec(ctx, "INSERT INTO mod_users.auth_signing_keys (id, private_key) VALUES (1, $1)", []byte("short"))
	if err == nil {
		t.Fatal("insert a short key directly: got nil error, want a CHECK constraint violation")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("insert a short key: error is not a *pgconn.PgError: %v (%T)", err, err)
	}
	const checkViolation = "23514" // Postgres SQLSTATE for check_violation
	if pgErr.Code != checkViolation {
		t.Errorf("insert a short key: pg error code = %q, want %q (check_violation): %v", pgErr.Code, checkViolation, err)
	}
}
