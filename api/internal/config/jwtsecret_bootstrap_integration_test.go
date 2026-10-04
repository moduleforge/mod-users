//go:build integration

package config

// jwtsecret_bootstrap_integration_test.go proves, against a real Postgres,
// the properties the unit tests in jwtsecret_bootstrap_test.go cannot: a
// genuinely fresh, unmigrated database (config.Load() runs before this
// module's own migrations apply -- see 001's and 002's header notes), a
// genuinely concurrent multi-caller first boot against it (the
// table-creation race the advisory lock protects, which ON CONFLICT DO
// NOTHING alone does not), and a real CHECK-constraint rejection of a
// corrupt secret.
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
// (the single usersmigrations.Migrate migration, which also creates auth_jwt_secrets) carries FKs
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
// checkJWTSecretIntegPrereqs treats a missing composed dir as a missing
// prerequisite (clean skip with an actionable message), mirroring
// authz_integration_test.go's checkPrereqs.
//
// This suite's shadow DB (jwt_secret_integ_users) is distinct from
// authz_integ_users, so both suites can share the same
// "users-module-postgres" container without colliding. "Genuinely empty"
// here means dropping the whole shadow database (not merely truncating
// tables), since the fresh-database and concurrent-race scenarios below
// specifically need no goose_db_version_users table present either -- see
// resetJWTSecretShadowDB. The corruption-detection scenario
// (TestInteg_AuthJWTSecrets_CheckConstraint_RejectsShortSecret) creates
// auth_jwt_secrets via bootstrapJWTSecretDDL directly (byte-identical to
// the baseline migration's own DDL -- see the drift-guard unit test) instead of via
// a full migration run, so it always exercises the real CHECK constraint
// whenever the DB is reachable at all, independent of the composed-dir
// prerequisite above.
//
// Run with:
//
//	cd mod-users && make dev.start   # or an equivalent Postgres; see authz_integration_test.go
//	cd mod-users/api && \
//	  AUTHZ_DEV_PG_HOST=localhost \
//	  go test -tags=integration -p 1 -v ./internal/config/...

import (
	"context"
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

// jwtSecretIntegDB is this suite's dedicated shadow database, distinct from
// authz_integ_users (authz_integration_test.go) and any other suite's
// shadow DB on the same shared Postgres container.
const jwtSecretIntegDB = "jwt_secret_integ_users"

// jwtSecretIntegContainer is the shared Postgres container reused by every
// mod-users integration suite in this environment (see
// authz_integration_test.go's header comment).
const jwtSecretIntegContainer = "users-module-postgres"

// jwtSecretIntegMigrationsDirEnvVar overrides migrationsDir()'s resolved
// path, for environments where the test binary does not run from its normal
// location inside a mod-users checkout. Unset by default. Mirrors
// authz_integration_test.go's integMigrationsDirEnvVar (AUTHZ_INTEG_MIGRATIONS_DIR),
// kept as a distinct, file-scoped constant rather than shared across
// packages.
const jwtSecretIntegMigrationsDirEnvVar = "JWTSECRET_INTEG_MIGRATIONS_DIR"

// integPGHost is resolved once in TestMain and reused by every test.
var integPGHost string

// migrationsDir resolves the composed schema dir (core + authz + users,
// produced by mod-users/model/Makefile's `compose` target) relative to this
// source file's own location, mirroring authz_integration_test.go's own
// migrationsDir. This file lives at
// <repo>/api/internal/config/jwtsecret_bootstrap_integration_test.go; the
// composed schema lives at <repo>/model/schema/migrations.
func migrationsDir() string {
	if d := os.Getenv(jwtSecretIntegMigrationsDirEnvVar); d != "" {
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
	pgHost := resolveJWTSecretIntegHost()
	if err := checkJWTSecretIntegPrereqs(pgHost); err != nil {
		fmt.Fprintf(os.Stderr, "integration: skipping jwtsecret bootstrap tests — %v\n", err)
		os.Exit(0) // exit 0 so `go test` reports skipped, not failed.
	}
	integPGHost = pgHost
	os.Exit(m.Run())
}

// checkJWTSecretIntegPrereqs verifies docker, the shared container, and
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
func checkJWTSecretIntegPrereqs(pgHost string) error {
	cmd := exec.Command("docker", "inspect", "--format={{.State.Running}}", jwtSecretIntegContainer)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker inspect: %w", err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("container %s is not running", jwtSecretIntegContainer)
	}
	if _, err := exec.LookPath("goose"); err != nil {
		return fmt.Errorf("goose not in PATH: %w", err)
	}
	if dir := migrationsDir(); !dirExists(dir) {
		return fmt.Errorf(
			"composed migrations dir %s not found — run `make -C model compose` (or `make dev.start`, which builds it as a dependency) first, or set %s",
			dir, jwtSecretIntegMigrationsDirEnvVar)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, jwtSecretIntegAdminDSN(pgHost))
	if err != nil {
		return fmt.Errorf("connect to %s as the expected users/users role: %w", jwtSecretIntegContainer, err)
	}
	conn.Close(context.Background())

	return nil
}

// resolveJWTSecretIntegHost mirrors authz_integration_test.go's resolveHost:
// AUTHZ_DEV_PG_HOST always wins when set (the documented
// AUTHZ_DEV_PG_HOST=localhost Docker Desktop for macOS convention -- reused
// verbatim, per this task's own instruction, rather than inventing a new
// host-resolution env var), falling back to the container's Docker-network
// IP, and finally the historical hard-coded sandbox IP.
func resolveJWTSecretIntegHost() string {
	if h := os.Getenv("AUTHZ_DEV_PG_HOST"); h != "" {
		return h
	}
	cmd := exec.Command("docker", "inspect",
		"--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", jwtSecretIntegContainer)
	out, err := cmd.Output()
	if err == nil {
		if ip := strings.TrimSpace(string(out)); ip != "" {
			return ip
		}
	}
	return "172.23.0.3"
}

func jwtSecretIntegAdminDSN(pgHost string) string {
	return fmt.Sprintf("postgres://users:users@%s:5432/postgres?sslmode=disable", pgHost)
}

func jwtSecretIntegShadowDSN(pgHost string) string {
	return fmt.Sprintf("postgres://users:users@%s:5432/%s?sslmode=disable", pgHost, jwtSecretIntegDB)
}

// resetJWTSecretShadowDB drops and recreates jwt_secret_integ_users so every
// test starts from a genuinely empty database: no auth_jwt_secrets table, no
// goose_db_version_users table, nothing -- not merely truncated tables. This
// is what the fresh-database and concurrent-race scenarios below need, and
// is a superset of what the already-migrated scenario needs.
func resetJWTSecretShadowDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, jwtSecretIntegAdminDSN(integPGHost))
	if err != nil {
		t.Fatalf("reset shadow db: connect admin: %v", err)
	}
	defer conn.Close(ctx)

	for _, stmt := range []string{
		"DROP DATABASE IF EXISTS " + jwtSecretIntegDB,
		"CREATE DATABASE " + jwtSecretIntegDB,
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
// The baseline migration 0100_baseline.sql (which also creates auth_jwt_secrets) has
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

// TestInteg_BootstrapJWTSecret_MigratedDB_RoundTrip resets the shadow DB and
// applies the composed migrations dir against it via goose (normal path --
// table pre-exists via goose, mirroring mod-core's field-key precedent
// scenario), then calls bootstrapJWTSecretFromDB twice: the first call must
// succeed and generate a well-formed secret; the second call against the
// same DB must return the same persisted secret, not generate a new one.
func TestInteg_BootstrapJWTSecret_MigratedDB_RoundTrip(t *testing.T) {
	resetJWTSecretShadowDB(t)
	dsn := jwtSecretIntegShadowDSN(integPGHost)
	runComposedMigrations(t, dsn)

	first, err := bootstrapJWTSecretFromDB(context.Background(), dsn)
	if err != nil {
		t.Fatalf("first bootstrap call: %v", err)
	}
	if len(first) != jwtSecretByteLen*2 {
		t.Errorf("first secret length = %d, want %d", len(first), jwtSecretByteLen*2)
	}

	second, err := bootstrapJWTSecretFromDB(context.Background(), dsn)
	if err != nil {
		t.Fatalf("second bootstrap call: %v", err)
	}
	if second != first {
		t.Errorf("second call returned a different secret (%q) than the first (%q); a second call against the same DB must return the persisted secret", second, first)
	}
}

// ---------------------------------------------------------------------------
// Real fresh-database round trip
// ---------------------------------------------------------------------------

// TestInteg_BootstrapJWTSecret_FreshUnmigratedDB_CreatesTableAndConvergesWithGoose
// resets the shadow DB to genuinely empty -- no auth_jwt_secrets, no
// goose_db_version_users, no migrations applied at all -- the scenario
// mod-core's field-key precedent does not need, since mod-core's
// config-equivalent bootstrap runs after its own migrations. It calls
// bootstrapJWTSecretFromDB directly against that raw, unmigrated DB and
// asserts it succeeds (creating the table itself via its own idempotent
// DDL), then applies the composed migrations dir against that same DB
// afterward via goose and asserts it succeeds cleanly and records migration
// the baseline (version 100) as applied in goose_db_version_users -- proving the goose
// migration's own CREATE TABLE IF NOT EXISTS tolerates the table Load()'s
// bootstrap already created, and the two converge.
func TestInteg_BootstrapJWTSecret_FreshUnmigratedDB_CreatesTableAndConvergesWithGoose(t *testing.T) {
	resetJWTSecretShadowDB(t)
	dsn := jwtSecretIntegShadowDSN(integPGHost)

	secret, err := bootstrapJWTSecretFromDB(context.Background(), dsn)
	if err != nil {
		t.Fatalf("bootstrap against a genuinely fresh, unmigrated DB: %v", err)
	}
	if len(secret) != jwtSecretByteLen*2 {
		t.Errorf("secret length = %d, want %d", len(secret), jwtSecretByteLen*2)
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
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM auth_jwt_secrets").Scan(&count); err != nil {
		t.Fatalf("count auth_jwt_secrets rows after convergence: %v", err)
	}
	if count != 1 {
		t.Errorf("auth_jwt_secrets row count after bootstrap+migrate = %d, want 1", count)
	}
}

// ---------------------------------------------------------------------------
// Real concurrent-race proof, including the table-creation race
// ---------------------------------------------------------------------------

// TestInteg_BootstrapJWTSecret_ConcurrentFirstBoot_SingleWinnerAcrossAllCallers
// resets the shadow DB to genuinely empty (no migrations applied) and
// launches 8 goroutines, each calling bootstrapJWTSecretFromDB concurrently
// against the same DSN with its own independent connection -- simulating N
// replica instances booting simultaneously against a brand-new database.
// This is the test that actually exercises the advisory lock under real
// concurrency against an unmigrated database: the unit tests can only prove
// the Go-level control flow handles a *simulated* row-level lost race
// correctly, not that the table-creation race -- the part ON CONFLICT DO
// NOTHING alone does not protect -- is genuinely closed. Unaffected by this
// file's KNOWN LIMITATION: it never calls usersmigrations.Migrate.
func TestInteg_BootstrapJWTSecret_ConcurrentFirstBoot_SingleWinnerAcrossAllCallers(t *testing.T) {
	resetJWTSecretShadowDB(t)
	dsn := jwtSecretIntegShadowDSN(integPGHost)
	const n = 8

	var wg sync.WaitGroup
	secrets := make([]string, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			secrets[i], errs[i] = bootstrapJWTSecretFromDB(context.Background(), dsn)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: bootstrapJWTSecretFromDB failed: %v", i, err)
		}
	}

	want := secrets[0]
	for i, s := range secrets {
		if s != want {
			t.Errorf("goroutine %d returned secret %q, want %q (every concurrent first-boot caller must converge on the same secret)", i, s, want)
		}
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to verify row count: %v", err)
	}
	defer conn.Close(ctx)

	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM auth_jwt_secrets").Scan(&count); err != nil {
		t.Fatalf("count auth_jwt_secrets rows after concurrent boot: %v", err)
	}
	if count != 1 {
		t.Errorf("auth_jwt_secrets row count after %d concurrent bootstrap callers = %d, want exactly 1 (the table-creation race is not genuinely closed)", n, count)
	}
}

// ---------------------------------------------------------------------------
// Real corruption-detection proof
// ---------------------------------------------------------------------------

// TestInteg_AuthJWTSecrets_CheckConstraint_RejectsShortSecret resets the
// shadow DB, creates auth_jwt_secrets via bootstrapJWTSecretDDL directly
// (byte-identical to the baseline migration's own DDL -- see
// TestBootstrapJWTSecretDDL_MatchesBaselineMigration) without inserting a row,
// then attempts a raw INSERT INTO auth_jwt_secrets (id, secret) VALUES (1,
// 'short') directly and asserts it fails with a CHECK constraint violation
// -- proving the char_length(secret) >= 32 CHECK from Task 001 actually
// rejects a short secret at the schema level, independent of the Go-level
// length check fetchOrGeneratePersistedJWTSecret also performs. Creating
// the table via the DDL constant directly (rather than via a full migration
// run) sidesteps this file's KNOWN LIMITATION entirely, and exercises
// exactly the same constraint a normal migration would have created.
func TestInteg_AuthJWTSecrets_CheckConstraint_RejectsShortSecret(t *testing.T) {
	resetJWTSecretShadowDB(t)
	dsn := jwtSecretIntegShadowDSN(integPGHost)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, bootstrapJWTSecretDDL); err != nil {
		t.Fatalf("create auth_jwt_secrets table: %v", err)
	}

	_, err = conn.Exec(ctx, "INSERT INTO auth_jwt_secrets (id, secret) VALUES (1, 'short')")
	if err == nil {
		t.Fatal("insert a short secret directly: got nil error, want a CHECK constraint violation")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("insert a short secret: error is not a *pgconn.PgError: %v (%T)", err, err)
	}
	const checkViolation = "23514" // Postgres SQLSTATE for check_violation
	if pgErr.Code != checkViolation {
		t.Errorf("insert a short secret: pg error code = %q, want %q (check_violation): %v", pgErr.Code, checkViolation, err)
	}

	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM auth_jwt_secrets").Scan(&count); err != nil {
		t.Fatalf("count rows after rejected insert: %v", err)
	}
	if count != 0 {
		t.Errorf("row count after a rejected insert = %d, want 0", count)
	}
}
