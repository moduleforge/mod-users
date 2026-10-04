package config

// jwtsecret_bootstrap_test.go covers fetchOrGeneratePersistedJWTSecret and
// bootstrapJWTSecretFromDB with fast, no-Docker-required unit tests:
//
//   - fetchOrGeneratePersistedJWTSecret's branches, via a small hand-written
//     fake implementing the (exported) JWTSecretQuerier interface -- full
//     coverage of the absent/present/corrupt/race/error paths without a
//     real database.
//   - bootstrapJWTSecretFromDB's connection-error path, against a
//     syntactically valid but unreachable DSN (no Docker needed).
//   - A drift guard comparing the bootstrapJWTSecretDDL Go constant against
//     model/migrations/sql/0100_baseline.sql's actual content.
//
// This file is package config (not config_test): every symbol it exercises
// directly (fetchOrGeneratePersistedJWTSecret, bootstrapJWTSecretFromDB,
// bootstrapJWTSecretDDL) is unexported, so a black-box config_test package
// could not call them. provider_merge_test.go and oidc_state_test.go
// already establish the same package-config precedent alongside this
// directory's config_test.go/providers_test.go black-box files -- both
// package styles coexist in one directory and compile into the same test
// binary.
//
// The real-Postgres properties these unit tests cannot prove (the
// table-creation race under genuine concurrency, and a real CHECK-constraint
// rejection) are covered by jwtsecret_bootstrap_integration_test.go.

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// fakeJWTSecretQuerier -- a small hand-written fake implementing the
// 2-method JWTSecretQuerier interface, for full branch coverage of
// fetchOrGeneratePersistedJWTSecret without a real database.
// ---------------------------------------------------------------------------

// fakeGetResult is one configured response for a single GetJWTSecret call.
type fakeGetResult struct {
	secret string
	err    error
}

// fakeInsertResult configures InsertJWTSecretIfAbsent's single response.
// echo true means "return whatever secret was passed in, nil error" --
// simulating a successful INSERT ... RETURNING secret. A non-nil err means
// "return ("", err)" -- simulating either the ON CONFLICT DO NOTHING
// no-rows-returned case (err = pgx.ErrNoRows) or a genuine insert failure.
type fakeInsertResult struct {
	echo bool
	err  error
}

// fakeJWTSecretQuerier implements JWTSecretQuerier. getResults is consumed
// in call order; a call beyond the configured results is a test-authoring
// bug, not a production code path, so it fails loudly via t.Fatalf rather
// than panicking or silently returning a zero value.
type fakeJWTSecretQuerier struct {
	t *testing.T

	getResults []fakeGetResult
	getCalls   int

	insertResult fakeInsertResult
	insertCalls  int
	insertArgs   []string
}

func (f *fakeJWTSecretQuerier) GetJWTSecret(ctx context.Context) (string, error) {
	idx := f.getCalls
	f.getCalls++
	if idx >= len(f.getResults) {
		f.t.Fatalf("fakeJWTSecretQuerier.GetJWTSecret: called %d time(s), only %d result(s) configured", f.getCalls, len(f.getResults))
	}
	r := f.getResults[idx]
	return r.secret, r.err
}

func (f *fakeJWTSecretQuerier) InsertJWTSecretIfAbsent(ctx context.Context, secret string) (string, error) {
	f.insertCalls++
	f.insertArgs = append(f.insertArgs, secret)
	if f.insertResult.err != nil {
		return "", f.insertResult.err
	}
	if f.insertResult.echo {
		return secret, nil
	}
	return "", nil
}

// ---------------------------------------------------------------------------
// fetchOrGeneratePersistedJWTSecret
// ---------------------------------------------------------------------------

// TestFetchOrGeneratePersistedJWTSecret_AbsentGeneratesAndPersists covers the
// first-boot happy path: no persisted secret exists, generation succeeds,
// and the DB echoes back what was inserted. Asserts the returned secret is
// actually well-formed (64 hex chars = 32 random bytes), not just non-empty.
func TestFetchOrGeneratePersistedJWTSecret_AbsentGeneratesAndPersists(t *testing.T) {
	fake := &fakeJWTSecretQuerier{
		t:            t,
		getResults:   []fakeGetResult{{err: pgx.ErrNoRows}},
		insertResult: fakeInsertResult{echo: true},
	}

	secret, err := fetchOrGeneratePersistedJWTSecret(context.Background(), fake)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(secret) != jwtSecretByteLen*2 {
		t.Errorf("secret length = %d, want %d (hex-encoded %d random bytes)", len(secret), jwtSecretByteLen*2, jwtSecretByteLen)
	}
	if _, err := hex.DecodeString(secret); err != nil {
		t.Errorf("secret %q is not valid hex: %v", secret, err)
	}
	if fake.getCalls != 1 {
		t.Errorf("GetJWTSecret call count = %d, want 1", fake.getCalls)
	}
	if fake.insertCalls != 1 {
		t.Errorf("InsertJWTSecretIfAbsent call count = %d, want 1", fake.insertCalls)
	}
	if len(fake.insertArgs) != 1 || fake.insertArgs[0] != secret {
		t.Errorf("InsertJWTSecretIfAbsent args = %v, want a single call with the returned secret %q", fake.insertArgs, secret)
	}
}

// TestFetchOrGeneratePersistedJWTSecret_InsertLosesRace_ReturnsWinner covers
// the simulated lost-race path: the first absent-check finds nothing, the
// insert is skipped by ON CONFLICT DO NOTHING (simulated via
// pgx.ErrNoRows), and a second GetJWTSecret call returns a distinct,
// fixed "winner" secret. The function must return the winner's secret, not
// whatever candidate it generated internally.
func TestFetchOrGeneratePersistedJWTSecret_InsertLosesRace_ReturnsWinner(t *testing.T) {
	winner := strings.Repeat("b", jwtSecretByteLen*2)
	fake := &fakeJWTSecretQuerier{
		t: t,
		getResults: []fakeGetResult{
			{err: pgx.ErrNoRows}, // initial absent-check
			{secret: winner},     // re-fetch after lost race
		},
		insertResult: fakeInsertResult{err: pgx.ErrNoRows},
	}

	secret, err := fetchOrGeneratePersistedJWTSecret(context.Background(), fake)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if secret != winner {
		t.Errorf("secret = %q, want the winner's secret %q (not the candidate this call generated internally)", secret, winner)
	}
	if fake.getCalls != 2 {
		t.Errorf("GetJWTSecret call count = %d, want 2 (initial check + re-fetch after lost race)", fake.getCalls)
	}
	if fake.insertCalls != 1 {
		t.Errorf("InsertJWTSecretIfAbsent call count = %d, want 1", fake.insertCalls)
	}
}

// TestFetchOrGeneratePersistedJWTSecret_PersistedSecretTooShort_FailsLoudly
// covers the corrupt-persisted-secret path: GetJWTSecret returns a string
// shorter than the required length with a nil error. The function must fail
// loudly rather than falling through to "absent" and regenerating over it --
// InsertJWTSecretIfAbsent must never be called.
func TestFetchOrGeneratePersistedJWTSecret_PersistedSecretTooShort_FailsLoudly(t *testing.T) {
	fake := &fakeJWTSecretQuerier{
		t:          t,
		getResults: []fakeGetResult{{secret: "short"}},
	}

	_, err := fetchOrGeneratePersistedJWTSecret(context.Background(), fake)
	if err == nil {
		t.Fatal("expected an error for a corrupt (too-short) persisted secret, got nil")
	}
	if fake.insertCalls != 0 {
		t.Errorf("InsertJWTSecretIfAbsent call count = %d, want 0 (a corrupt read must fail loudly, never fall through to generate-and-persist)", fake.insertCalls)
	}
}

// TestFetchOrGeneratePersistedJWTSecret_GetErrors_NeverGenerates covers the
// absent-check-itself-errors path (e.g. a connection failure): GetJWTSecret
// returns a non-pgx.ErrNoRows error. The function must return a wrapped
// error and must never call InsertJWTSecretIfAbsent -- only a
// confirmed-absent read (pgx.ErrNoRows, never any other error) may trigger
// generation.
func TestFetchOrGeneratePersistedJWTSecret_GetErrors_NeverGenerates(t *testing.T) {
	connErr := errors.New("connection reset by peer")
	fake := &fakeJWTSecretQuerier{
		t:          t,
		getResults: []fakeGetResult{{err: connErr}},
	}

	_, err := fetchOrGeneratePersistedJWTSecret(context.Background(), fake)
	if err == nil {
		t.Fatal("expected an error when the absent-check itself fails, got nil")
	}
	if !errors.Is(err, connErr) {
		t.Errorf("error = %v, want it to wrap %v", err, connErr)
	}
	if fake.insertCalls != 0 {
		t.Errorf("InsertJWTSecretIfAbsent call count = %d, want 0 (only a confirmed-absent pgx.ErrNoRows read may trigger generation)", fake.insertCalls)
	}
}

// TestFetchOrGeneratePersistedJWTSecret_InsertErrors_DoesNotRetryGet covers
// the insert-fails-for-another-reason path: GetJWTSecret confirms absent,
// but InsertJWTSecretIfAbsent fails with something other than the
// ON-CONFLICT-race pgx.ErrNoRows. The function must return a wrapped error
// and must not re-fetch (GetJWTSecret called exactly once).
func TestFetchOrGeneratePersistedJWTSecret_InsertErrors_DoesNotRetryGet(t *testing.T) {
	insertErr := errors.New("insert failed: disk full")
	fake := &fakeJWTSecretQuerier{
		t:            t,
		getResults:   []fakeGetResult{{err: pgx.ErrNoRows}},
		insertResult: fakeInsertResult{err: insertErr},
	}

	_, err := fetchOrGeneratePersistedJWTSecret(context.Background(), fake)
	if err == nil {
		t.Fatal("expected an error when insert fails for a reason other than the ON CONFLICT race, got nil")
	}
	if !errors.Is(err, insertErr) {
		t.Errorf("error = %v, want it to wrap %v", err, insertErr)
	}
	if fake.getCalls != 1 {
		t.Errorf("GetJWTSecret call count = %d, want 1 (must not re-fetch after a non-conflict insert error)", fake.getCalls)
	}
}

// ---------------------------------------------------------------------------
// bootstrapJWTSecretFromDB -- connection-error path
// ---------------------------------------------------------------------------

// TestBootstrapJWTSecretFromDB_UnreachableDB_ReturnsWrappedErrorFast is the
// one place this file exercises the real pgx.Connect call: a syntactically
// valid but unreachable DSN (port 1 is privileged/unassigned on every
// platform this runs on) must fail fast with a wrapped, non-nil error,
// rather than hanging until bootstrapJWTSecretFromDB's own 10s timeout.
func TestBootstrapJWTSecretFromDB_UnreachableDB_ReturnsWrappedErrorFast(t *testing.T) {
	const unreachableDSN = "postgres://u:p@127.0.0.1:1/nonexistent?connect_timeout=1"

	start := time.Now()
	_, err := bootstrapJWTSecretFromDB(context.Background(), unreachableDSN)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error connecting to an unreachable database, got nil")
	}
	if !strings.Contains(err.Error(), "jwtsecret") {
		t.Errorf("error = %v, want it wrapped with a %q-prefixed message", err, "jwtsecret")
	}
	const bound = 5 * time.Second // well under bootstrapJWTSecretFromDB's own 10s timeout
	if elapsed > bound {
		t.Errorf("bootstrapJWTSecretFromDB took %v to fail against an unreachable DB, want well under its own 10s timeout (bound %v)", elapsed, bound)
	}
}

// ---------------------------------------------------------------------------
// Drift guard: bootstrapJWTSecretDDL vs. the baseline migration
// ---------------------------------------------------------------------------

// TestBootstrapJWTSecretDDL_MatchesBaselineMigration reads
// model/migrations/sql/0100_baseline.sql from disk (path resolved
// relative to this test file's own location via runtime.Caller, matching
// this repo's established integration-test convention for locating files
// relative to source, e.g. authz_integration_test.go's migrationsDir) and
// asserts its content contains bootstrapJWTSecretDDL verbatim -- catching
// any future edit to one without the other.
func TestBootstrapJWTSecretDDL_MatchesBaselineMigration(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this test file's own path")
	}
	// This file lives at <repo>/api/internal/config/jwtsecret_bootstrap_test.go;
	// the migration lives at <repo>/model/migrations/sql/0100_baseline.sql.
	migrationPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "model", "migrations", "sql", "0100_baseline.sql")

	content, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration file %s: %v", migrationPath, err)
	}

	if !strings.Contains(string(content), bootstrapJWTSecretDDL) {
		t.Errorf("migration file %s does not contain bootstrapJWTSecretDDL verbatim -- these must stay byte-for-byte identical (see jwtsecret_bootstrap.go's doc comment on bootstrapJWTSecretDDL)\n\nmigration content:\n%s\n\nwant substring:\n%s", migrationPath, content, bootstrapJWTSecretDDL)
	}
}
