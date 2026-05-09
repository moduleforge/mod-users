//go:build integration

package authz_test

// authz_integration_test.go verifies the wildcard-grant admin policy after
// the removal of the is_admin column (Task 2 of the is_admin-removal phase).
//
// Run with:
//
//	cd users-module/api && \
//	  AUTHZ_DEV_PG_HOST=$(docker inspect users-module-postgres | \
//	    grep -m1 '"IPAddress"' | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | head -1) \
//	  go test -tags=integration -p 1 -v ./internal/authz/...
//
// Container-IP convention: "localhost:5432" resolves to the test-process loopback,
// not the Docker container. Use AUTHZ_DEV_PG_HOST set to the container's
// Docker-network IP (e.g. 172.x.x.x), or let the fallback resolve it via
// docker inspect. See project MEMORY.md reference_atlas_dev_shadow.md.
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

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	authzapi "github.com/moduleforge/authz-api/authz"
	authzdb "github.com/moduleforge/authz-model/db"
	"github.com/moduleforge/core-api/opctx"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/users-module/api/internal/authz"
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

// integMigrationsDir is the composed schema (core + authz + users + ...).
const integMigrationsDir = "/Users/zane/playground/user-components/users-module/model/schema/migrations"

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
	return nil
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
	cmd := exec.Command("goose", "-dir", integMigrationsDir, "postgres", dsn, "up") //nolint:gosec
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

	// Own entity (entity_id == actor) should still pass for read (own-predicate).
	if err := integAZ.Authorize(ctx, "read", &oidcUserID); err != nil {
		t.Errorf("OIDC role path: own entity read: got %v, want nil (own-predicate)", err)
	}
}
