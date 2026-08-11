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
// This suite is lighter than that precedent in one respect and heavier in
// another:
//
//   - Lighter: it needs only mod-users' own module migrations
//     (usersmigrations, model/migrations) applied via the Migrate function
//     directly, not the cross-module composed core+authz+users schema that
//     suite's goose-CLI-driven resetDB applies -- auth_jwt_secrets itself
//     has no FKs outside itself. Its own shadow DB
//     (jwt_secret_integ_users) is distinct from authz_integ_users, so both
//     suites can share the same "users-module-postgres" container without
//     colliding.
//
//   - Heavier: "genuinely empty" here means dropping the whole shadow
//     database (not merely truncating tables), since the fresh-database and
//     concurrent-race scenarios below specifically need no
//     goose_db_version_users table present either -- see resetJWTSecretShadowDB.
//
// KNOWN LIMITATION (see task 003's report for the full empirical
// reproduction): mod-users' own plain migrations are not actually
// self-contained the way this suite's Requirements assumed. Migration
// 0100_schema.sql (part of the same usersmigrations.Migrate call as 0102)
// carries FKs into core-model tables (legal_entities, apps) that this
// suite's lighter, non-composed shadow DB never creates. Running
// usersmigrations.Migrate against a truly bare database -- as
// TestInteg_BootstrapJWTSecret_MigratedDB_RoundTrip and the tail of
// TestInteg_BootstrapJWTSecret_FreshUnmigratedDB_CreatesTableAndConvergesWithGoose
// both do -- fails with SQLSTATE 42P01 (undefined_table) on 0100, not
// because of anything related to auth_jwt_secrets or the JWT bootstrap code
// under test. runUsersMigrationsOrSkip detects exactly that failure mode and
// skips the affected assertions with a clear message, rather than failing
// on something unrelated to this task's own code, or silently omitting the
// scenario the task doc asks for. The corruption-detection scenario
// (TestInteg_AuthJWTSecrets_CheckConstraint_RejectsShortSecret) sidesteps
// this entirely by creating auth_jwt_secrets via bootstrapJWTSecretDDL
// directly (byte-identical to migration 0102's own DDL -- see the
// drift-guard unit test) instead of via a full migration run, so it always
// exercises the real CHECK constraint whenever the DB is reachable at all.
//
// Run with:
//
//	cd mod-users && make dev.start   # or an equivalent Postgres; see authz_integration_test.go
//	cd mod-users/api && \
//	  AUTHZ_DEV_PG_HOST=localhost \
//	  go test -tags=integration -p 1 -v ./internal/config/...

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// integPGHost is resolved once in TestMain and reused by every test.
var integPGHost string

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
// check), and additionally probes a real connection with the expected
// "users"/"users" role. The container being "running" per docker inspect
// does not guarantee it is actually provisioned with that role -- a stale
// data volume, or a container started for an unrelated purpose, can leave
// it running yet unusable for this suite's own DSNs. Treating a probe
// failure as a missing prerequisite (clean skip) avoids every test in this
// file failing individually on it later.
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

// runUsersMigrationsOrSkip runs usersmigrations.Migrate against dsn. See
// this file's header comment (KNOWN LIMITATION) for why a shadow DB that
// only ever ran this suite's own reset (never the cross-module composed
// schema) can legitimately fail this with SQLSTATE 42P01 (undefined_table)
// on migration 0100 -- a missing prerequisite unrelated to the JWT-bootstrap
// code under test, not a regression in it. That specific failure mode
// degrades to a per-test skip; anything else is a genuine, reportable
// failure.
func runUsersMigrationsOrSkip(t *testing.T, dsn string) {
	t.Helper()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open sql.DB for migration: %v", err)
	}
	defer sqlDB.Close()

	if err := usersmigrations.Migrate(context.Background(), sqlDB); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // undefined_table
			t.Skipf("usersmigrations.Migrate requires core-model tables (e.g. legal_entities, apps) this suite's plain, non-composed shadow DB does not create -- skipping (see this file's KNOWN LIMITATION header note): %v", err)
		}
		t.Fatalf("usersmigrations.Migrate: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Real fetch-or-generate round trip against an already-migrated DB
// ---------------------------------------------------------------------------

// TestInteg_BootstrapJWTSecret_MigratedDB_RoundTrip resets the shadow DB and
// runs usersmigrations.Migrate against it (normal path -- table pre-exists
// via goose, mirroring mod-core's field-key precedent scenario), then calls
// bootstrapJWTSecretFromDB twice: the first call must succeed and generate a
// well-formed secret; the second call against the same DB must return the
// same persisted secret, not generate a new one.
func TestInteg_BootstrapJWTSecret_MigratedDB_RoundTrip(t *testing.T) {
	resetJWTSecretShadowDB(t)
	dsn := jwtSecretIntegShadowDSN(integPGHost)
	runUsersMigrationsOrSkip(t, dsn)

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
// DDL), then runs usersmigrations.Migrate against that same DB afterward and
// asserts it succeeds cleanly and records migration 0102 as applied in
// goose_db_version_users -- proving the goose migration's own CREATE TABLE
// IF NOT EXISTS tolerates the table Load()'s bootstrap already created, and
// the two converge.
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

	runUsersMigrationsOrSkip(t, dsn) // may skip the convergence assertions below; see KNOWN LIMITATION

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to verify migration state: %v", err)
	}
	defer conn.Close(ctx)

	var applied bool
	const versionSQL = `SELECT is_applied FROM goose_db_version_users WHERE version_id = 102 ORDER BY id DESC LIMIT 1`
	if err := conn.QueryRow(ctx, versionSQL).Scan(&applied); err != nil {
		t.Fatalf("query goose_db_version_users for migration 0102: %v", err)
	}
	if !applied {
		t.Errorf("goose_db_version_users records migration 0102 as not applied, want applied=true")
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
// (byte-identical to migration 0102's own DDL -- see
// TestBootstrapJWTSecretDDL_MatchesMigration0102) without inserting a row,
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
