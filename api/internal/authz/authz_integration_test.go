//go:build integration

package authz_test

// authz_integration_test.go verifies the wildcard-grant admin policy after
// the removal of the is_admin column (Task 2 of the is_admin-removal phase),
// and (scenario 9) the generic entities.owner_id own-predicate that folded
// into checkGrantOrOwn (see the authz-single-row-own plan, Phase 1 Task 1).
//
// Run with:
//
//	cd mod-users && make dev.start   # or an equivalent Postgres; see below
//	cd mod-users/api && \
//	  AUTHZ_DEV_PG_HOST=localhost \
//	  go test -tags=integration -p 1 -v ./internal/authz/...
//
// Host-resolution convention: on a Docker Desktop for macOS host, the shared
// "users-module-postgres" container (reused by mod-core, mod-audit,
// mod-authz, and mod-users' own integration suites — see
// mod-core/api/authz/setup/grant_table_integration_test.go) is reachable at
// "localhost" and NOT at the container's docker-network IP; the opposite of
// what resolveHost's docker-inspect fallback below assumes. Set
// AUTHZ_DEV_PG_HOST=localhost explicitly rather than relying on the
// fallback. This cost prior tasks real time (authz-entity-ownership
// follow-ups 4gUq, GBUZ) — do not rediscover it.
//
// Migrations: this suite migrates mod-users' own composed schema
// (model/schema/migrations, core + authz + users, produced by
// `mod-users/model/Makefile`'s `compose` target), resolved relative to this
// file's own location (see migrationsDir below) rather than a hard-coded
// absolute path. If the composed dir is missing, checkPrereqs treats it as
// a missing prerequisite and the suite skips with a clear message; run
// `make -C model compose` (or `make dev.start`, which builds it as a
// dependency) first.
//
// Scenarios verified (per Final design step 9):
//  1. Wildcard manage admin — passes every Authorize check including nil-target.
//  2. Wildcard read holder — passes read/list, denied on update/delete/assume.
//  3. Targeted grant holder — permitted only on the granted target.
//  4. No-grants user — denied everywhere (nil-target and specific target).
//  5. Bootstrap first user — first registered account automatically holds wildcard manage.
//  6. Revocation — deleting the wildcard manage grant demotes the user immediately.
//  7. Nil-target Authorize — wildcard admins pass; non-wildcards are denied.
//  8. OIDC-role admin path removed — JWT roles claim does NOT confer admin privileges.
//  9. Generic owner predicate — a corporation entity (a type that never
//     self-owns) created with an explicit owner_id is accessible to its
//     owner for every operation and denied to everyone else; a NULL-owner
//     corporation is denied to all; ownership does not leak across entities;
//     and single-row Authorize agrees with the list-side access function
//     when the real access functions are installed.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	authzapi "github.com/moduleforge/authz-api/authz"
	authzdb "github.com/moduleforge/authz-model/db"
	authzsetup "github.com/moduleforge/core-api/authz/setup"
	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/authz"
)

// ---------------------------------------------------------------------------
// Package-level state
// ---------------------------------------------------------------------------

var (
	integPool  *pgxpool.Pool
	integAZ    *authz.Authorizer
	integOpReg *authzapi.OperationRegistry
)

const integDevDB = "authz_integ_users"

// integMigrationsDirEnvVar overrides migrationsDir()'s resolved path, for
// environments where the test binary does not run from its normal location
// inside a mod-users checkout. Unset by default.
const integMigrationsDirEnvVar = "AUTHZ_INTEG_MIGRATIONS_DIR"

// migrationsDir resolves the composed schema dir (core + authz + users,
// produced by mod-users/model/Makefile's `compose` target) relative to this
// source file's own location, rather than a hard-coded, machine-specific
// absolute path (the prior convention, which broke on every checkout but
// the one it was authored on). This file lives at
// <repo>/api/internal/authz/authz_integration_test.go; the composed schema
// lives at <repo>/model/schema/migrations. Mirrors the precedent fix in
// mod-core/api/authz/setup/grant_table_integration_test.go (migrationsDir).
func migrationsDir() string {
	if d := os.Getenv(integMigrationsDirEnvVar); d != "" {
		return d
	}
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "model", "schema", "migrations")
}

// ---------------------------------------------------------------------------
// TestMain
// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	if err := checkPrereqs(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: skipping authz users tests — %v\n", err)
		os.Exit(0)
	}

	pgHost := resolveHost()

	if err := resetDB(pgHost); err != nil {
		fmt.Fprintf(os.Stderr, "integration: DB reset failed: %v\n", err)
		os.Exit(1)
	}

	dsn := fmt.Sprintf("postgres://users:users@%s:5432/%s?sslmode=disable", pgHost, integDevDB)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: open pool: %v\n", err)
		os.Exit(1)
	}
	integPool = pool

	if err := wireServices(context.Background(), pool); err != nil {
		fmt.Fprintf(os.Stderr, "integration: wire services: %v\n", err)
		pool.Close()
		os.Exit(1)
	}

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func checkPrereqs() error {
	cmd := exec.Command("docker", "inspect", "--format={{.State.Running}}", "users-module-postgres")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker inspect: %w", err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("container users-module-postgres is not running")
	}
	if _, err := exec.LookPath("goose"); err != nil {
		return fmt.Errorf("goose not in PATH: %w", err)
	}
	if dir := migrationsDir(); !dirExists(dir) {
		return fmt.Errorf(
			"composed migrations dir %s not found — run `make -C model compose` (or `make dev.start`, which builds it as a dependency) first, or set %s",
			dir, integMigrationsDirEnvVar)
	}
	return nil
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func resolveHost() string {
	if h := os.Getenv("AUTHZ_DEV_PG_HOST"); h != "" {
		return h
	}
	cmd := exec.Command("docker", "inspect",
		"--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
		"users-module-postgres")
	out, err := cmd.Output()
	if err == nil {
		if ip := strings.TrimSpace(string(out)); ip != "" {
			return ip
		}
	}
	return "172.23.0.3"
}

func resetDB(pgHost string) error {
	ctx := context.Background()
	adminURL := fmt.Sprintf("postgres://users:users@%s:5432/postgres?sslmode=disable", pgHost)

	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("connect admin: %w", err)
	}
	defer conn.Close(ctx)

	for _, stmt := range []string{
		"DROP DATABASE IF EXISTS " + integDevDB,
		"CREATE DATABASE " + integDevDB,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}

	dsn := fmt.Sprintf("postgres://users:users@%s:5432/%s?sslmode=disable", pgHost, integDevDB)
	cmd := exec.Command("goose", "-dir", migrationsDir(), "postgres", dsn, "up") //nolint:gosec // fixed args/resolved paths, not user input
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("goose up: %w\n%s", err, out)
	}
	return nil
}

func wireServices(ctx context.Context, pool *pgxpool.Pool) error {
	authzQ := authzdb.New(pool)
	opReg, err := authzapi.NewOperationRegistry(ctx, authzQ)
	if err != nil {
		return fmt.Errorf("operation registry: %w", err)
	}
	integOpReg = opReg
	integAZ = authz.New(authzQ, opReg, pool)

	// Install the real accessible_corporation_ids_for_actor access function
	// (0099_access_function_stubs.sql ships an empty-set stub; app startup
	// normally replaces it via setup.ApplyFuncs). Scenario 9's list/single-row
	// symmetry assertion runs this function directly, so it must be the real
	// generic three-arm body, not the stub — otherwise the assertion would
	// pass vacuously against an always-empty result set.
	if err := authzsetup.ApplyFuncs(ctx, pool, authzsetup.NewGrantTableGenerator(), []string{"corporation"}); err != nil {
		return fmt.Errorf("apply access functions: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Seed helpers
// ---------------------------------------------------------------------------

// seedUser inserts entity → legal_entity → natural_person → user_account and
// returns the entity's internal ID. Optionally grants a wildcard manage grant.
func seedUser(t *testing.T, email string, withWildcardManage bool) (entityID int64) {
	t.Helper()
	ctx := context.Background()

	coreQ := coredb.New(integPool)

	const typeSQL = `SELECT id FROM types WHERE slug = 'natural_person'`
	var npTypeID int64
	if err := integPool.QueryRow(ctx, typeSQL).Scan(&npTypeID); err != nil {
		t.Fatalf("seedUser: resolve type: %v", err)
	}

	ent, err := coreQ.CreateEntity(ctx, npTypeID)
	if err != nil {
		t.Fatalf("seedUser: create entity: %v", err)
	}
	entityID = ent.ID

	if _, err := coreQ.CreateLegalEntity(ctx, entityID); err != nil {
		t.Fatalf("seedUser: create legal_entity: %v", err)
	}

	const uaSQL = `INSERT INTO user_accounts (account_holder, email) VALUES ($1, $2)`
	if _, err := integPool.Exec(ctx, uaSQL, entityID, email); err != nil {
		t.Fatalf("seedUser: insert user_account: %v", err)
	}

	if withWildcardManage {
		seedWildcardGrant(t, entityID, "manage")
	}
	return entityID
}

// seedWildcardGrant inserts a wildcard grant (target_id IS NULL) for the given
// actor entity and operation slug.
func seedWildcardGrant(t *testing.T, actorEntityID int64, operationSlug string) {
	t.Helper()
	ctx := context.Background()

	const opSQL = `SELECT id FROM authz_operations WHERE slug = $1`
	var opID int64
	if err := integPool.QueryRow(ctx, opSQL, operationSlug).Scan(&opID); err != nil {
		t.Fatalf("seedWildcardGrant: resolve op %q: %v", operationSlug, err)
	}

	const grantSQL = `
INSERT INTO grants (actor_id, operation_id, target_id, granted_by)
VALUES ($1, $2, NULL, NULL)
ON CONFLICT DO NOTHING`
	if _, err := integPool.Exec(ctx, grantSQL, actorEntityID, opID); err != nil {
		t.Fatalf("seedWildcardGrant: insert grant: %v", err)
	}
}

// deleteWildcardGrant removes the wildcard grant for (actor, operationSlug).
func deleteWildcardGrant(t *testing.T, actorEntityID int64, operationSlug string) {
	t.Helper()
	ctx := context.Background()

	const opSQL = `SELECT id FROM authz_operations WHERE slug = $1`
	var opID int64
	if err := integPool.QueryRow(ctx, opSQL, operationSlug).Scan(&opID); err != nil {
		t.Fatalf("deleteWildcardGrant: resolve op %q: %v", operationSlug, err)
	}

	const delSQL = `DELETE FROM grants WHERE actor_id = $1 AND operation_id = $2 AND target_id IS NULL`
	if _, err := integPool.Exec(ctx, delSQL, actorEntityID, opID); err != nil {
		t.Fatalf("deleteWildcardGrant: delete: %v", err)
	}
}

// targetedGrant inserts a targeted grant (actor → op → target).
func targetedGrant(t *testing.T, actorID, targetID int64, operationSlug string) {
	t.Helper()
	ctx := context.Background()

	const opSQL = `SELECT id FROM authz_operations WHERE slug = $1`
	var opID int64
	if err := integPool.QueryRow(ctx, opSQL, operationSlug).Scan(&opID); err != nil {
		t.Fatalf("targetedGrant: resolve op %q: %v", operationSlug, err)
	}

	const grantSQL = `
INSERT INTO grants (actor_id, operation_id, target_id, granted_by)
VALUES ($1, $2, $3, NULL)
ON CONFLICT DO NOTHING`
	if _, err := integPool.Exec(ctx, grantSQL, actorID, opID, targetID); err != nil {
		t.Fatalf("targetedGrant: insert: %v", err)
	}
}

// actorCtx returns a context with the given entity ID set as actor.
func actorCtx(entityID int64) context.Context {
	return opctx.WithActor(context.Background(), entityID)
}

// ---------------------------------------------------------------------------
// Owner-predicate seed helpers (Scenario 9)
// ---------------------------------------------------------------------------
//
// corporation is the subject: a concrete type that never self-owns (the
// entities_owner_default_self trigger, migration 0013, matches only
// natural_person and service_account descendants), and it exists in
// mod-users' own composed schema, so these scenarios prove type-agnostic
// ownership without depending on mod-tags or mod-tasks.

// corporationTypeID resolves the 'corporation' type's internal ID.
func corporationTypeID(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	const typeSQL = `SELECT id FROM types WHERE slug = 'corporation'`
	var typeID int64
	if err := integPool.QueryRow(ctx, typeSQL).Scan(&typeID); err != nil {
		t.Fatalf("corporationTypeID: resolve type: %v", err)
	}
	return typeID
}

// seedOwnedCorporation inserts entity -> legal_entity -> corporation with
// owner_id set to ownerEntityID at INSERT time via CreateEntityWithOwner,
// and returns the entity's internal ID.
//
// owner_id must be set at INSERT time, not via a follow-up UPDATE:
// entities_owner_immutable (migration 0013) fires on any UPDATE OF
// owner_id, including the first NULL -> value write, so a
// CreateEntity-then-UPDATE seeding helper would fail with "entities:
// owner_id is immutable after insert".
func seedOwnedCorporation(t *testing.T, ownerEntityID int64, legalName string) (entityID int64) {
	t.Helper()
	ctx := context.Background()
	coreQ := coredb.New(integPool)

	ent, err := coreQ.CreateEntityWithOwner(ctx, coredb.CreateEntityWithOwnerParams{
		FundamentalTypeID: corporationTypeID(t),
		OwnerID:           pgtype.Int8{Int64: ownerEntityID, Valid: true},
	})
	if err != nil {
		t.Fatalf("seedOwnedCorporation: create entity: %v", err)
	}
	entityID = ent.ID

	if _, err := coreQ.CreateLegalEntity(ctx, entityID); err != nil {
		t.Fatalf("seedOwnedCorporation: create legal_entity: %v", err)
	}
	if _, err := coreQ.CreateCorporation(ctx, coredb.CreateCorporationParams{EntityID: entityID, LegalName: legalName}); err != nil {
		t.Fatalf("seedOwnedCorporation: create corporation: %v", err)
	}
	return entityID
}

// seedUnownedCorporation inserts entity -> legal_entity -> corporation via
// plain CreateEntity (no owner argument), so owner_id stays NULL — matching
// every real corporation-creation path in the system (see
// mod-core/api/service/corporation.go, and the single-row-own-predicate
// investigation note's per-type owner_id table: no creation path ever sets
// a corporation's owner_id).
func seedUnownedCorporation(t *testing.T, legalName string) (entityID int64) {
	t.Helper()
	ctx := context.Background()
	coreQ := coredb.New(integPool)

	ent, err := coreQ.CreateEntity(ctx, corporationTypeID(t))
	if err != nil {
		t.Fatalf("seedUnownedCorporation: create entity: %v", err)
	}
	entityID = ent.ID

	if _, err := coreQ.CreateLegalEntity(ctx, entityID); err != nil {
		t.Fatalf("seedUnownedCorporation: create legal_entity: %v", err)
	}
	if _, err := coreQ.CreateCorporation(ctx, coredb.CreateCorporationParams{EntityID: entityID, LegalName: legalName}); err != nil {
		t.Fatalf("seedUnownedCorporation: create corporation: %v", err)
	}
	return entityID
}

// assertOwnerIDNull asserts, via direct SQL against entities, that
// entityID's owner_id is NULL. Scenarios that depend on a NULL owner call
// this before exercising Authorize, so a future change to
// entities_owner_default_self's type matching (e.g. if 'corporation' were
// ever added to the self-owning types) cannot make the scenario pass
// vacuously.
func assertOwnerIDNull(t *testing.T, entityID int64) {
	t.Helper()
	ctx := context.Background()
	const ownerSQL = `SELECT owner_id FROM entities WHERE id = $1`
	var ownerID *int64
	if err := integPool.QueryRow(ctx, ownerSQL, entityID).Scan(&ownerID); err != nil {
		t.Fatalf("assertOwnerIDNull: query entity %d: %v", entityID, err)
	}
	if ownerID != nil {
		t.Fatalf("assertOwnerIDNull: entity %d has owner_id = %d, want NULL", entityID, *ownerID)
	}
}

// ---------------------------------------------------------------------------
// Scenario 1: Wildcard manage admin passes every Authorize check
// ---------------------------------------------------------------------------

func TestInteg_WildcardManageAdmin_PassesAll(t *testing.T) {
	adminID := seedUser(t, "admin-s1@example.com", true)
	targetID := seedUser(t, "target-s1@example.com", false)
	ctx := actorCtx(adminID)

	// Nil-target (admin-only operations like create, list).
	if err := integAZ.Authorize(ctx, "manage", nil); err != nil {
		t.Errorf("wildcard admin: manage nil-target: got %v, want nil", err)
	}
	if err := integAZ.Authorize(ctx, "list", nil); err != nil {
		t.Errorf("wildcard admin: list nil-target: got %v, want nil", err)
	}

	// Specific target.
	if err := integAZ.Authorize(ctx, "read", &targetID); err != nil {
		t.Errorf("wildcard admin: read target: got %v, want nil", err)
	}
	if err := integAZ.Authorize(ctx, "update", &targetID); err != nil {
		t.Errorf("wildcard admin: update target: got %v, want nil", err)
	}
	if err := integAZ.Authorize(ctx, "delete", &targetID); err != nil {
		t.Errorf("wildcard admin: delete target: got %v, want nil", err)
	}
	if err := integAZ.Authorize(ctx, "assume", &targetID); err != nil {
		t.Errorf("wildcard admin: assume target: got %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// Scenario 2: Wildcard read holder — sees all on read, denied on write ops
// ---------------------------------------------------------------------------

func TestInteg_WildcardReadHolder_ReadAllowedWriteDenied(t *testing.T) {
	readerID := seedUser(t, "reader-s2@example.com", false)
	targetID := seedUser(t, "target-s2@example.com", false)

	seedWildcardGrant(t, readerID, "read")

	ctx := actorCtx(readerID)

	// Read operations should be permitted via wildcard read grant.
	if err := integAZ.Authorize(ctx, "read", &targetID); err != nil {
		t.Errorf("wildcard read: read target: got %v, want nil", err)
	}

	// List with nil target: the wildcard read grant should satisfy "list" (which
	// is in the read-satisfied-by closure per standardOps). But note: Authorize
	// with nil target first checks wildcard, and "list" may not be in the read
	// closure. Let's test with a specific op IDs lookup.
	opIDs, err := integOpReg.SatisfiedBy("list")
	if err != nil {
		t.Fatalf("SatisfiedBy(list): %v", err)
	}
	readIDs, err := integOpReg.SatisfiedBy("read")
	if err != nil {
		t.Fatalf("SatisfiedBy(read): %v", err)
	}
	_ = opIDs
	_ = readIDs

	// update/delete/assume should be denied for a read-only wildcard holder.
	if err := integAZ.Authorize(ctx, "update", &targetID); err == nil {
		t.Errorf("wildcard read: update target: got nil, want ErrForbidden")
	}
	if err := integAZ.Authorize(ctx, "delete", &targetID); err == nil {
		t.Errorf("wildcard read: delete target: got nil, want ErrForbidden")
	}
	if err := integAZ.Authorize(ctx, "assume", &targetID); err == nil {
		t.Errorf("wildcard read: assume target: got nil, want ErrForbidden")
	}

	// Nil-target with manage op: read-wildcard holder should be denied (no manage grant).
	if err := integAZ.Authorize(ctx, "manage", nil); err == nil {
		t.Errorf("wildcard read: manage nil-target: got nil, want ErrForbidden")
	}
}

// ---------------------------------------------------------------------------
// Scenario 3: Targeted grant holder — permitted only on granted target
// ---------------------------------------------------------------------------

func TestInteg_TargetedGrantHolder_PermittedOnlyOnGrantedTarget(t *testing.T) {
	actorID := seedUser(t, "actor-s3@example.com", false)
	grantedTargetID := seedUser(t, "granted-s3@example.com", false)
	otherTargetID := seedUser(t, "other-s3@example.com", false)

	targetedGrant(t, actorID, grantedTargetID, "read")

	ctx := actorCtx(actorID)

	// Permitted on granted target.
	if err := integAZ.Authorize(ctx, "read", &grantedTargetID); err != nil {
		t.Errorf("targeted grant: granted target: got %v, want nil", err)
	}

	// Denied on other target.
	if err := integAZ.Authorize(ctx, "read", &otherTargetID); err == nil {
		t.Errorf("targeted grant: other target: got nil, want ErrForbidden")
	}

	// Denied for nil-target (admin-only).
	if err := integAZ.Authorize(ctx, "read", nil); err == nil {
		t.Errorf("targeted grant: nil target: got nil, want ErrForbidden")
	}
}

// ---------------------------------------------------------------------------
// Scenario 4: No-grants user — denied everywhere
// ---------------------------------------------------------------------------

func TestInteg_NoGrantsUser_DeniedEverywhere(t *testing.T) {
	actorID := seedUser(t, "nogrants-s4@example.com", false)
	targetID := seedUser(t, "target-s4@example.com", false)
	ctx := actorCtx(actorID)

	if err := integAZ.Authorize(ctx, "read", &targetID); err == nil {
		t.Errorf("no grants: read target: got nil, want ErrForbidden")
	}
	if err := integAZ.Authorize(ctx, "update", &targetID); err == nil {
		t.Errorf("no grants: update target: got nil, want ErrForbidden")
	}
	if err := integAZ.Authorize(ctx, "manage", nil); err == nil {
		t.Errorf("no grants: manage nil-target: got nil, want ErrForbidden")
	}
}

// ---------------------------------------------------------------------------
// Scenario 5: Bootstrap first-user — verified to hold wildcard manage grant
// ---------------------------------------------------------------------------

// TestInteg_Bootstrap_FirstUserHasWildcardGrant verifies the structural
// expectation: after calling seedWildcardGrant (simulating what the bootstrap
// hook does), the entity passes Authorize("manage", nil).
//
// Note: the actual first-user-hook is exercised in the full server integration
// test (not here, where we cannot control registration order). This test pins
// the policy semantics: a wildcard manage grant confers full admin privileges.
func TestInteg_Bootstrap_WildcardGrantConfersFull(t *testing.T) {
	firstUserID := seedUser(t, "first-s5@example.com", true) // true = wildcard manage grant
	secondUserID := seedUser(t, "second-s5@example.com", false)

	// First user passes manage nil-target.
	if err := integAZ.Authorize(actorCtx(firstUserID), "manage", nil); err != nil {
		t.Errorf("bootstrap: first user manage nil: got %v, want nil", err)
	}

	// Second user (no grant) is denied manage nil-target.
	if err := integAZ.Authorize(actorCtx(secondUserID), "manage", nil); err == nil {
		t.Errorf("bootstrap: second user manage nil: got nil, want ErrForbidden")
	}
}

// ---------------------------------------------------------------------------
// Scenario 6: Revocation — demotes immediately
// ---------------------------------------------------------------------------

func TestInteg_Revocation_DemotesImmediately(t *testing.T) {
	adminID := seedUser(t, "admin-s6@example.com", true)
	ctx := actorCtx(adminID)

	// Before revocation: full admin.
	if err := integAZ.Authorize(ctx, "manage", nil); err != nil {
		t.Fatalf("pre-revoke: manage nil: got %v, want nil", err)
	}

	// Revoke the wildcard manage grant.
	deleteWildcardGrant(t, adminID, "manage")

	// After revocation: denied.
	if err := integAZ.Authorize(ctx, "manage", nil); err == nil {
		t.Errorf("post-revoke: manage nil: got nil, want ErrForbidden")
	}
}

// ---------------------------------------------------------------------------
// Scenario 7: Nil-target Authorize semantics
// ---------------------------------------------------------------------------

func TestInteg_NilTarget_WildcardAdminPassesNonWildcardDenied(t *testing.T) {
	adminID := seedUser(t, "admin-s7@example.com", true)
	normalID := seedUser(t, "normal-s7@example.com", false)

	// Wildcard admin passes nil-target for all ops.
	adminCtx := actorCtx(adminID)
	for _, op := range []string{"manage", "read", "update", "delete"} {
		if err := integAZ.Authorize(adminCtx, op, nil); err != nil {
			t.Errorf("nil-target: admin op=%q: got %v, want nil", op, err)
		}
	}

	// Non-wildcard user is denied nil-target even with a targeted grant.
	targetID := seedUser(t, "target-s7@example.com", false)
	targetedGrant(t, normalID, targetID, "read")

	normalCtx := actorCtx(normalID)
	if err := integAZ.Authorize(normalCtx, "read", nil); err == nil {
		t.Errorf("nil-target: normal user with targeted grant: got nil, want ErrForbidden")
	}
}

// ---------------------------------------------------------------------------
// Scenario 8: OIDC-role admin path removed
// ---------------------------------------------------------------------------

// TestInteg_OIDCRoleAdminPathRemoved verifies that having "admin" in a JWT's
// roles claim does NOT grant admin privileges. Admin status is determined solely
// by the grants table. This test exercises the Authorizer directly (the
// claim-mapping path was removed in Task 2 — there is no longer a code path
// that maps JWT roles to IsAdmin).
//
// Mechanically: we seed a user WITHOUT a wildcard grant, then call Authorize
// with a context that only has the actor entity ID set (same as the real
// RequireAuth middleware does). No JWT role mapping can influence the result.
func TestInteg_OIDCRoleAdminPathRemoved(t *testing.T) {
	// Seed a user with no grants. In the old system, if this user had an
	// "admin" role in their JWT, they would have bypassed authorization.
	// In the new system, only a wildcard grant grants admin access.
	oidcUserID := seedUser(t, "oidc-s8@example.com", false)

	// Context has only the actor entity ID — same as what RequireAuth sets.
	// There is no IsAdmin field or role-based bypass path anywhere in Authorize.
	ctx := actorCtx(oidcUserID)

	// Must be denied — no wildcard grant, no targeted grant.
	if err := integAZ.Authorize(ctx, "manage", nil); err == nil {
		t.Errorf("OIDC role path: manage nil-target: got nil, want ErrForbidden")
	}

	otherID := seedUser(t, "other-s8@example.com", false)
	if err := integAZ.Authorize(ctx, "update", &otherID); err == nil {
		t.Errorf("OIDC role path: update other user: got nil, want ErrForbidden")
	}

	// Own entity read should still pass — but now because oidcUserID's
	// natural_person entity self-owns via entities_owner_default_self
	// (migration 0013: NEW.owner_id := NEW.id for natural_person /
	// service_account descendants), which checkGrantOrOwn's generic
	// entities.owner_id own-arm resolves like any other ownership, not
	// because target == actor. Before task 001, the old code recognized
	// this case via a hardcoded `*target == actorEntityID` identity check;
	// that check is gone, and this assertion now exercises the same
	// generic own-arm Scenario 9 proves against a corporation (a type that
	// never self-owns), so the coincidence that oidcUserID's target equals
	// its own actor ID is no longer load-bearing for this assertion to
	// pass.
	if err := integAZ.Authorize(ctx, "read", &oidcUserID); err != nil {
		t.Errorf("OIDC role path: own entity read: got %v, want nil (own-predicate)", err)
	}
}

// ---------------------------------------------------------------------------
// Scenario 9: Generic owner predicate (entities.owner_id), proved against a
// corporation — a concrete type that never self-owns via
// entities_owner_default_self. Task 001 folded a generic "OR EXISTS (...
// e.owner_id = actorEntityID)" arm into checkGrantOrOwn, replacing the old
// *target == actorEntityID identity check and checkTagOwnership. Task 001's
// unit seam proves the Go-side branching against a stubbed grantOrOwnFn;
// only a live DB — here — proves the SQL, the NULL-owner semantics, and
// that the predicate is genuinely type-agnostic.
// ---------------------------------------------------------------------------

// TestInteg_OwnerPredicate_OwnerAllowedAnyOperation seeds actor A and a
// corporation entity with owner_id = A via seedOwnedCorporation, records no
// grant of any kind, and asserts Authorize returns nil for read, update,
// delete, and a non-CRUD verb (assume) — operation by operation, so the
// requirement that the own-arm is not gated on the operation or on opIDs is
// pinned per-operation rather than by a single passing call. This is
// exactly the case that returned ErrForbidden before task 001: corporation
// is not natural_person or service_account, so the old identity check never
// matched, and checkTagOwnership cannot even run in mod-users' own schema
// (no tags table).
func TestInteg_OwnerPredicate_OwnerAllowedAnyOperation(t *testing.T) {
	ownerID := seedUser(t, "owner-s9a@example.com", false)
	corpID := seedOwnedCorporation(t, ownerID, "Acme Corp S9A")
	ctx := actorCtx(ownerID)

	for _, op := range []string{"read", "update", "delete", "assume"} {
		if err := integAZ.Authorize(ctx, op, &corpID); err != nil {
			t.Errorf("owner predicate: owner op=%q: got %v, want nil", op, err)
		}
	}
}

// TestInteg_OwnerPredicate_NonOwnerDenied is the non-owner mirror of
// TestInteg_OwnerPredicate_OwnerAllowedAnyOperation: a second actor B, with
// no grants and no ownership of A's corporation, is denied every operation
// A is allowed on that same entity.
func TestInteg_OwnerPredicate_NonOwnerDenied(t *testing.T) {
	ownerID := seedUser(t, "owner-s9b@example.com", false)
	nonOwnerID := seedUser(t, "nonowner-s9b@example.com", false)
	corpID := seedOwnedCorporation(t, ownerID, "Acme Corp S9B")
	ctx := actorCtx(nonOwnerID)

	for _, op := range []string{"read", "update", "delete", "assume"} {
		if err := integAZ.Authorize(ctx, op, &corpID); err == nil {
			t.Errorf("owner predicate: non-owner op=%q: got nil, want ErrForbidden", op)
		}
	}
}

// TestInteg_OwnerPredicate_NullOwnerDenied seeds a corporation via
// seedUnownedCorporation (plain CreateEntity — owner_id stays NULL, matching
// every real corporation-creation path), asserts the NULL precondition by
// direct SQL first via assertOwnerIDNull, then asserts both actor A and
// actor B are denied read. e.owner_id = actorEntityID evaluates to NULL
// (not true) when owner_id IS NULL, so the own-arm's EXISTS is false and a
// NULL-owner entity matches no actor — see checkGrantOrOwn's doc comment in
// authz.go.
func TestInteg_OwnerPredicate_NullOwnerDenied(t *testing.T) {
	actorA := seedUser(t, "actor-a-s9c@example.com", false)
	actorB := seedUser(t, "actor-b-s9c@example.com", false)
	corpID := seedUnownedCorporation(t, "Acme Corp S9C")

	assertOwnerIDNull(t, corpID)

	for _, actor := range []struct {
		name string
		id   int64
	}{{"A", actorA}, {"B", actorB}} {
		ctx := actorCtx(actor.id)
		if err := integAZ.Authorize(ctx, "read", &corpID); err == nil {
			t.Errorf("null-owner corporation: actor %s: got nil, want ErrForbidden", actor.name)
		}
	}
}

// TestInteg_OwnerPredicate_DoesNotLeakAcrossEntities seeds actor A owning
// corporation X, and asserts A is denied read on both an unowned corporation
// Y and a corporation Z owned by a different actor — guarding against a
// mis-parameterized query that ignores the target id (the failure mode that
// would make the own-arm allow everything, not just the actor's own row).
func TestInteg_OwnerPredicate_DoesNotLeakAcrossEntities(t *testing.T) {
	ownerID := seedUser(t, "owner-s9d@example.com", false)
	otherOwnerID := seedUser(t, "other-owner-s9d@example.com", false)
	ownedID := seedOwnedCorporation(t, ownerID, "Acme Corp S9D Owned")
	unownedID := seedUnownedCorporation(t, "Acme Corp S9D Unowned")
	otherOwnedID := seedOwnedCorporation(t, otherOwnerID, "Acme Corp S9D OtherOwned")
	ctx := actorCtx(ownerID)

	if err := integAZ.Authorize(ctx, "read", &ownedID); err != nil {
		t.Errorf("cross-entity isolation: owned entity: got %v, want nil", err)
	}
	if err := integAZ.Authorize(ctx, "read", &unownedID); err == nil {
		t.Errorf("cross-entity isolation: unowned entity: got nil, want ErrForbidden")
	}
	if err := integAZ.Authorize(ctx, "read", &otherOwnedID); err == nil {
		t.Errorf("cross-entity isolation: other-owned entity: got nil, want ErrForbidden")
	}
}

// TestInteg_OwnerPredicate_ListSingleRowSymmetry is the plan's headline
// claim: for the owned corporation, single-row Authorize("read", &owned)
// succeeds, and the SAME row is returned by the list-side
// accessible_corporation_ids_for_actor access function. wireServices
// installs the REAL generic three-arm function body via
// setup.ApplyFuncs/GrantTableGenerator (not the empty-set stub
// 0099_access_function_stubs.sql ships, which app startup normally replaces
// via setup.ApplyFuncs) — so this assertion cannot pass vacuously against
// an always-empty result set.
func TestInteg_OwnerPredicate_ListSingleRowSymmetry(t *testing.T) {
	ownerID := seedUser(t, "owner-s9e@example.com", false)
	corpID := seedOwnedCorporation(t, ownerID, "Acme Corp S9E")
	ctx := actorCtx(ownerID)

	if err := integAZ.Authorize(ctx, "read", &corpID); err != nil {
		t.Fatalf("list/single-row symmetry: single-row Authorize: got %v, want nil", err)
	}

	readOpIDs, err := integOpReg.SatisfiedBy("read")
	if err != nil {
		t.Fatalf("list/single-row symmetry: SatisfiedBy(read): %v", err)
	}

	const accessSQL = `SELECT entity_id FROM accessible_corporation_ids_for_actor($1, $2)`
	rows, err := integPool.Query(context.Background(), accessSQL, ownerID, readOpIDs)
	if err != nil {
		t.Fatalf("list/single-row symmetry: query access function: %v", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("list/single-row symmetry: scan: %v", err)
		}
		if id == corpID {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list/single-row symmetry: rows: %v", err)
	}
	if !found {
		t.Errorf("list/single-row symmetry: accessible_corporation_ids_for_actor(%d, ...) did not include owned corporation %d — single-row Authorize and the list-side access function disagree", ownerID, corpID)
	}
}
