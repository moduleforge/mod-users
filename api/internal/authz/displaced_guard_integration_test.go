//go:build integration

package authz_test

// displaced_guard_integration_test.go reproduces the displaced-schema failure
// of the entities_no_system_actor_owner guard (followup Y9n6) and proves the
// fix. mod-core's types_create_entity_trigger / types_create_entity_for are
// SECURITY DEFINER with `search_path = public, pg_temp` and INSERT INTO
// entities; the guard trigger they fire inherited that path, so an unqualified
// `system_actors` lived in no searched schema when mod-users' tables were
// migrated into a non-public schema, failing with 42P01.
//
// The test builds that layout for real: a scratch database gets mod-core's
// migrations in public, then mod-users' 0100_baseline.sql applied by goose over
// a connection whose search_path is `mod_displaced, public` (so every
// unqualified mod-users table lands in mod_displaced). It then inserts a
// type -- which fires the core DEFINER chain into the guard -- and checks the
// guard still rejects a system-actor owner.
//
// Reuses authz_integration_test.go's TestMain/checkPrereqs/resolveHost/
// resolvePort/migrationsDir (run recipe: AGENTS.md).

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const displacedDB = "authz_integ_users_displaced"

func TestGuardResolvesOwnTablesInDisplacedSchema(t *testing.T) {
	ctx := context.Background()
	host, port := resolveHost(), resolvePort()

	admin, err := pgx.Connect(ctx, fmt.Sprintf("postgres://users:users@%s:%s/postgres?sslmode=disable", host, port))
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer admin.Close(ctx)
	for _, stmt := range []string{"DROP DATABASE IF EXISTS " + displacedDB, "CREATE DATABASE " + displacedDB} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), fmt.Sprintf("postgres://users:users@%s:%s/postgres?sslmode=disable", host, port))
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+displacedDB)
			c.Close(context.Background())
		}
	})

	baseDSN := fmt.Sprintf("postgres://users:users@%s:%s/%s?sslmode=disable", host, port, displacedDB)

	// Stage 1: mod-core's migrations (numbered below 0100) into public.
	coreDir, usersDir := t.TempDir(), t.TempDir()
	composed := migrationsDir()
	entries, err := os.ReadDir(composed)
	if err != nil {
		t.Fatalf("read composed dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		if !strings.HasSuffix(n, ".sql") {
			continue
		}
		num, err := strconv.Atoi(strings.SplitN(n, "_", 2)[0])
		if err != nil {
			continue
		}
		var dst string
		switch {
		case num < 100:
			dst = coreDir
		case num == 100:
			dst = usersDir
		default:
			continue
		}
		b, err := os.ReadFile(filepath.Join(composed, n))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, n), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	goose := func(dir, dsn string) {
		t.Helper()
		out, err := exec.Command("goose", "-dir", dir, "postgres", dsn, "up").CombinedOutput() //nolint:gosec // fixed args/temp paths
		if err != nil {
			t.Fatalf("goose up (%s): %v\n%s", dir, err, out)
		}
	}
	goose(coreDir, baseDSN)

	// Stage 2: mod-users' baseline with its tables displaced into mod_displaced.
	if _, err := mustConn(t, baseDSN).Exec(ctx, "CREATE SCHEMA mod_displaced"); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	q := url.Values{"search_path": {"mod_displaced,public"}}
	goose(usersDir, baseDSN+"&"+q.Encode())

	conn := mustConn(t, baseDSN)
	var schema string
	if err := conn.QueryRow(ctx, `SELECT schemaname FROM pg_tables WHERE tablename = 'system_actors'`).Scan(&schema); err != nil || schema != "mod_displaced" {
		t.Fatalf("system_actors schema = %q (%v), want mod_displaced (layout not displaced)", schema, err)
	}

	// The DEFINER chain: inserting a type fires types_create_entity_trigger
	// (search_path = public, pg_temp), which INSERTs an entity and so fires
	// the guard. Pre-fix this raised 42P01.
	if _, err := conn.Exec(ctx, `
		INSERT INTO public.types (slug, parent_id, concrete, name)
		SELECT 'displaced_probe', id, true, 'Displaced Probe' FROM public.types WHERE slug = 'entity'`); err != nil {
		t.Fatalf("type insert through the SECURITY DEFINER chain failed in the displaced layout: %v", err)
	}

	// The guard still bites, from a session whose search_path cannot see
	// mod_displaced at all.
	if _, err := conn.Exec(ctx, `SET search_path = public, pg_temp`); err != nil {
		t.Fatal(err)
	}
	var anon int64
	if err := conn.QueryRow(ctx, `SELECT entity_id FROM mod_displaced.system_actors WHERE slug = 'anonymous'`).Scan(&anon); err != nil {
		t.Fatalf("resolve anonymous actor: %v", err)
	}
	var typeID int64
	if err := conn.QueryRow(ctx, `SELECT id FROM public.types WHERE slug = 'corporation'`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(ctx, `INSERT INTO public.entities (fundamental_type_id, owner_id) VALUES ($1, $2)`, typeID, anon)
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "P0001" || !strings.Contains(pgErr.Message, "a system actor may not own an entity") {
		t.Fatalf("system-actor owner insert: err = %v, want P0001 'a system actor may not own an entity'", err)
	}
}

func mustConn(t *testing.T, dsn string) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}
