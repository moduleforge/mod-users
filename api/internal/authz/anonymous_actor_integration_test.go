//go:build integration

package authz_test

// anonymous_actor_integration_test.go proves, against a real Postgres with
// the composed schema applied, the database-enforced invariants that make
// the anonymous system actor provably zero-authority (per the anonymous
// actor architecture proposal, plan/notes/anonymous-actor-architecture-
// proposal.md, and mod-users/model/migrations/0101_system_actors.sql):
//
//   - It can never own an entity (entities_no_system_actor_owner, fired on
//     both INSERT and UPDATE).
//   - It cannot join an actor group (authz_actor_group_members' member-type
//     check trigger, mod-authz's 0502_authz_actor_group_members.sql).
//   - It can never hold a login identity (user_accounts.account_holder's FK
//     to legal_entities(entity_id) structurally excludes system_actor).
//   - Passing Authorize's effectiveActor early-return, it is denied with
//     ErrForbidden, never ErrUnauthenticated.
//   - It holds zero grants and is invisible to the list-side access
//     functions, agreeing by construction with the single-row denial.
//
// This file reuses authz_integration_test.go's TestMain, checkPrereqs,
// resolveHost, and seeding helpers (integPool, integAZ, integOpReg,
// seedUser, seedWildcardGrant, actorCtx, corporationTypeID,
// seedOwnedCorporation, seedUnownedCorporation) -- see that file's header
// comment for run instructions and the macOS Docker Desktop host-resolution
// convention (AUTHZ_DEV_PG_HOST=localhost). Do not rediscover it.
//
// Scope: integration tests only, per plan/phase-01-anonymous-actor/
// 006-db-invariant-integration-tests.md. No production code changes.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	authzdb "github.com/moduleforge/authz-model/db"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/authz"
)

// anonymousActorEntityID resolves the seeded 'anonymous' system actor's
// entity id by slug -- never a hardcoded id, since entities.id (BIGSERIAL)
// is not deterministic across environments.
func anonymousActorEntityID(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	const sql = `SELECT entity_id FROM system_actors WHERE slug = $1`
	var id int64
	if err := integPool.QueryRow(ctx, sql, "anonymous").Scan(&id); err != nil {
		t.Fatalf("anonymousActorEntityID: resolve by slug: %v", err)
	}
	return id
}

// authzActorGroupTypeID resolves the 'authz_actor_group' type's internal ID.
func authzActorGroupTypeID(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	const typeSQL = `SELECT id FROM types WHERE slug = 'authz_actor_group'`
	var typeID int64
	if err := integPool.QueryRow(ctx, typeSQL).Scan(&typeID); err != nil {
		t.Fatalf("authzActorGroupTypeID: resolve type: %v", err)
	}
	return typeID
}

// seedActorGroup inserts entity -> authz_actor_groups and returns the
// entity's internal ID, so assertion (e) has a real actor group to attempt
// to add the anonymous actor to.
func seedActorGroup(t *testing.T, name string) (entityID int64) {
	t.Helper()
	ctx := context.Background()

	coreQ := coredb.New(integPool)
	ent, err := coreQ.CreateEntity(ctx, authzActorGroupTypeID(t))
	if err != nil {
		t.Fatalf("seedActorGroup: create entity: %v", err)
	}
	entityID = ent.ID

	authzQ := authzdb.New(integPool)
	if _, err := authzQ.CreateActorGroup(ctx, authzdb.CreateActorGroupParams{
		EntityID:    entityID,
		Name:        name,
		Description: "",
	}); err != nil {
		t.Fatalf("seedActorGroup: create actor group: %v", err)
	}
	return entityID
}

// ---------------------------------------------------------------------------
// (a) Seeded state
// ---------------------------------------------------------------------------

// TestInteg_AnonymousActor_SeededState asserts exactly one system_actors row
// exists, with slug 'anonymous'; its entities row has owner_id IS NULL (the
// self-own default did not fire for this type, since system_actor descends
// from neither natural_person nor service_account); and its types row is
// concrete with parent slug 'entity'.
func TestInteg_AnonymousActor_SeededState(t *testing.T) {
	ctx := context.Background()

	var count int
	if err := integPool.QueryRow(ctx, `SELECT count(*) FROM system_actors`).Scan(&count); err != nil {
		t.Fatalf("count system_actors: %v", err)
	}
	if count != 1 {
		t.Errorf("system_actors row count = %d, want exactly 1", count)
	}

	var slug string
	if err := integPool.QueryRow(ctx, `SELECT slug FROM system_actors LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("select system_actors.slug: %v", err)
	}
	if slug != "anonymous" {
		t.Errorf("system_actors.slug = %q, want %q", slug, "anonymous")
	}

	anonID := anonymousActorEntityID(t)

	var ownerID *int64
	const ownerSQL = `SELECT owner_id FROM entities WHERE id = $1`
	if err := integPool.QueryRow(ctx, ownerSQL, anonID).Scan(&ownerID); err != nil {
		t.Fatalf("select entities.owner_id for anonymous actor: %v", err)
	}
	if ownerID != nil {
		t.Errorf("anonymous actor entity owner_id = %d, want NULL (self-own default must not fire for system_actor)", *ownerID)
	}

	var concrete bool
	var parentSlug string
	const typeSQL = `
SELECT t.concrete, pt.slug
FROM entities e
JOIN types t ON t.id = e.fundamental_type_id
JOIN types pt ON pt.id = t.parent_id
WHERE e.id = $1`
	if err := integPool.QueryRow(ctx, typeSQL, anonID).Scan(&concrete, &parentSlug); err != nil {
		t.Fatalf("select fundamental type for anonymous actor: %v", err)
	}
	if !concrete {
		t.Errorf("anonymous actor's fundamental type is not concrete, want concrete")
	}
	if parentSlug != "entity" {
		t.Errorf("anonymous actor's fundamental type parent slug = %q, want %q", parentSlug, "entity")
	}
}

// ---------------------------------------------------------------------------
// (b) Ownership guard on INSERT
// ---------------------------------------------------------------------------

// TestInteg_AnonymousActor_OwnershipGuard_RejectsInsert asserts that
// INSERT INTO entities (fundamental_type_id, owner_id) with owner_id set to
// the anonymous actor's entity id raises entities_no_system_actor_owner.
// The inserted row's type is 'corporation' -- a type that never self-owns
// (entities_owner_default_self only defaults natural_person/service_account
// descendants) -- so the only possible reason for failure is the guard.
func TestInteg_AnonymousActor_OwnershipGuard_RejectsInsert(t *testing.T) {
	ctx := context.Background()
	anonID := anonymousActorEntityID(t)
	typeID := corporationTypeID(t)

	const insertSQL = `INSERT INTO entities (fundamental_type_id, owner_id) VALUES ($1, $2)`
	_, err := integPool.Exec(ctx, insertSQL, typeID, anonID)
	if err == nil {
		t.Fatal("insert entity with owner_id = anonymous actor: got nil error, want ownership-guard violation")
	}
	const wantSubstr = "a system actor may not own an entity"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("insert entity with owner_id = anonymous actor: error = %q, want it to contain %q", err.Error(), wantSubstr)
	}
}

// ---------------------------------------------------------------------------
// (c) Ownership guard on UPDATE
// ---------------------------------------------------------------------------

// TestInteg_AnonymousActor_OwnershipGuard_RejectsUpdate asserts that
// UPDATE entities SET owner_id = <anon entity id> WHERE id = <some other
// entity> raises entities_no_system_actor_owner's message, not
// entities_owner_immutable's. Both triggers are BEFORE-row triggers that
// would reject this UPDATE (the target's owner_id transitions from NULL to
// a value, which entities_owner_immutable also forbids), but
// 'entities_no_system_actor_owner' sorts alphabetically before
// 'entities_owner_immutable', and Postgres fires same-event row triggers in
// name order -- so the system-actor guard's message is what surfaces here.
// This is documented, expected behavior (see 0101_system_actors.sql's (d)
// comment), not a bug; the assertion pins it so a future rename that
// disturbs the alphabetical ordering is caught.
func TestInteg_AnonymousActor_OwnershipGuard_RejectsUpdate(t *testing.T) {
	ctx := context.Background()
	anonID := anonymousActorEntityID(t)
	targetID := seedUnownedCorporation(t, "Acme Corp Anon Update Target")

	const updateSQL = `UPDATE entities SET owner_id = $1 WHERE id = $2`
	_, err := integPool.Exec(ctx, updateSQL, anonID, targetID)
	if err == nil {
		t.Fatal("update entity owner_id to anonymous actor: got nil error, want ownership-guard violation")
	}
	const wantSubstr = "a system actor may not own an entity"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("update entity owner_id to anonymous actor: error = %q, want it to contain %q", err.Error(), wantSubstr)
	}
	if strings.Contains(err.Error(), "immutable") {
		t.Errorf("update entity owner_id to anonymous actor: error unexpectedly mentions immutability (%q) -- entities_no_system_actor_owner should have fired first, per alphabetical trigger-name ordering", err.Error())
	}
}

// ---------------------------------------------------------------------------
// (d) A normal entity is unaffected
// ---------------------------------------------------------------------------

// TestInteg_AnonymousActor_OwnershipGuard_NormalEntitiesUnaffected is the
// regression guard for a BEFORE INSERT OR UPDATE trigger that fires on every
// entities row, not just ones that touch owner_id: inserting an entity with
// a normal (non-system-actor) owner_id, inserting one with owner_id NULL,
// and updating an ordinary entity all still succeed.
func TestInteg_AnonymousActor_OwnershipGuard_NormalEntitiesUnaffected(t *testing.T) {
	ctx := context.Background()

	// Insert with a normal owner_id succeeds.
	owner := seedUser(t, "normal-owner-anon-d@example.com", false)
	ownedID := seedOwnedCorporation(t, owner, "Acme Corp Anon D Owned")

	// Insert with owner_id NULL succeeds.
	_ = seedUnownedCorporation(t, "Acme Corp Anon D Unowned")

	// Updating an ordinary entity (without touching owner_id, which is
	// immutable once set -- see seedOwnedCorporation's doc comment) still
	// succeeds: the new entities_no_system_actor_owner trigger must not
	// have broken entities writes in general.
	const archiveSQL = `UPDATE entities SET archived_at = now() WHERE id = $1 AND archived_at IS NULL`
	if _, err := integPool.Exec(ctx, archiveSQL, ownedID); err != nil {
		t.Errorf("archive ordinary owned entity: got %v, want nil", err)
	}
	const unarchiveSQL = `UPDATE entities SET archived_at = NULL WHERE id = $1`
	if _, err := integPool.Exec(ctx, unarchiveSQL, ownedID); err != nil {
		t.Errorf("unarchive ordinary owned entity: got %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// (e) Actor-group membership is rejected
// ---------------------------------------------------------------------------

// TestInteg_AnonymousActor_ActorGroupMembershipRejected asserts that
// INSERT INTO authz_actor_group_members (group_id, member_id) with member_id
// set to the anonymous actor's entity id raises the member-type check
// trigger's exception. This is a structural guarantee that falls out of
// parenting system_actor under 'entity' rather than 'legal_entity' (see
// mod-authz/model/migrations/0502_authz_actor_group_members.sql); pin it so
// a future re-parenting is caught.
func TestInteg_AnonymousActor_ActorGroupMembershipRejected(t *testing.T) {
	ctx := context.Background()
	anonID := anonymousActorEntityID(t)
	groupID := seedActorGroup(t, "Anon Actor Group Rejection Test")

	const memberSQL = `INSERT INTO authz_actor_group_members (group_id, member_id) VALUES ($1, $2)`
	_, err := integPool.Exec(ctx, memberSQL, groupID, anonID)
	if err == nil {
		t.Fatal("add anonymous actor as actor group member: got nil error, want member-type-check violation")
	}
	const wantSubstr = "only legal_entity subtypes (e.g. natural_person, corporation) and authz_actor_group are allowed as actor group members"
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("add anonymous actor as actor group member: error = %q, want it to contain %q", err.Error(), wantSubstr)
	}
}

// ---------------------------------------------------------------------------
// (f) No login identity is possible
// ---------------------------------------------------------------------------

// TestInteg_AnonymousActor_NoLoginIdentityPossible asserts that
// INSERT INTO user_accounts (account_holder, ...) with account_holder set to
// the anonymous actor's entity id fails the FK to legal_entities(entity_id).
// This is the structural half of "no JWT can resolve to it": the
// local-issuer fast path in api/internal/auth/resolver.go maps a JWT
// subject UUID to a user_accounts row, and none can ever exist for this
// entity, since system_actor is parented directly under 'entity', never
// under 'legal_entity'. (Task 004 owns the request-level half -- a JWT for a
// nonexistent account yields ErrUserGone -> 401.)
func TestInteg_AnonymousActor_NoLoginIdentityPossible(t *testing.T) {
	ctx := context.Background()
	anonID := anonymousActorEntityID(t)

	const uaSQL = `INSERT INTO user_accounts (account_holder, email) VALUES ($1, $2)`
	_, err := integPool.Exec(ctx, uaSQL, anonID, "anon-login-attempt@example.com")
	if err == nil {
		t.Fatal("insert user_account for anonymous actor: got nil error, want FK violation against legal_entities")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("insert user_account for anonymous actor: error is not a *pgconn.PgError: %v (%T)", err, err)
	}
	const foreignKeyViolation = "23503"
	if pgErr.Code != foreignKeyViolation {
		t.Errorf("insert user_account for anonymous actor: pg error code = %q, want %q (foreign_key_violation)", pgErr.Code, foreignKeyViolation)
	}
}

// ---------------------------------------------------------------------------
// (g) Authorize returns ErrForbidden, not ErrUnauthenticated
// ---------------------------------------------------------------------------

// TestInteg_AnonymousActor_AuthorizeForbiddenNotUnauthenticated builds the
// real *authz.Authorizer the way the rest of this suite does, puts the
// anonymous actor's entity id on the context via opctx.WithActor, and calls
// Authorize for both a nil target and a real target the anonymous actor
// neither owns nor holds a grant on. Both must return ErrForbidden and,
// explicitly, NOT ErrUnauthenticated -- passing effectiveActor's
// early-return (authz.go:110-113) is the entire point of the anonymous
// actor mechanism: it is a real, resolvable entity id, so
// effectiveActor(ctx) succeeds and Authorize proceeds to the ordinary
// grant/ownership checks, which it always fails.
func TestInteg_AnonymousActor_AuthorizeForbiddenNotUnauthenticated(t *testing.T) {
	anonID := anonymousActorEntityID(t)
	ctx := actorCtx(anonID)

	if err := integAZ.Authorize(ctx, "manage", nil); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("anonymous actor Authorize(%q, nil): got %v, want ErrForbidden", "manage", err)
	} else if errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("anonymous actor Authorize(%q, nil): got ErrUnauthenticated, want NOT ErrUnauthenticated", "manage")
	}

	targetID := seedUnownedCorporation(t, "Acme Corp Anon G Target")
	if err := integAZ.Authorize(ctx, "read", &targetID); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("anonymous actor Authorize(%q, target): got %v, want ErrForbidden", "read", err)
	} else if errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("anonymous actor Authorize(%q, target): got ErrUnauthenticated, want NOT ErrUnauthenticated", "read")
	}
}

// ---------------------------------------------------------------------------
// (h) Zero platform authority, both paths
// ---------------------------------------------------------------------------

// TestInteg_AnonymousActor_ZeroPlatformAuthority asserts the anonymous actor
// holds no grants and is a member of no actor group, and that the list-side
// accessible_corporation_ids_for_actor access function (installed for real
// by wireServices, not the empty-set stub 0099_access_function_stubs.sql
// ships) returns the empty set for it -- confirming the proposal's claim
// that both paths agree by construction. system_actor is never added to any
// access-function slug list; this reuses the existing corporation access
// function the rest of the suite already installs.
func TestInteg_AnonymousActor_ZeroPlatformAuthority(t *testing.T) {
	ctx := context.Background()
	anonID := anonymousActorEntityID(t)

	var grantCount int
	if err := integPool.QueryRow(ctx, `SELECT count(*) FROM grants WHERE actor_id = $1`, anonID).Scan(&grantCount); err != nil {
		t.Fatalf("count grants for anonymous actor: %v", err)
	}
	if grantCount != 0 {
		t.Errorf("anonymous actor holds %d grants, want 0", grantCount)
	}

	var groupMemberCount int
	const memberCountSQL = `SELECT count(*) FROM authz_actor_group_members WHERE member_id = $1`
	if err := integPool.QueryRow(ctx, memberCountSQL, anonID).Scan(&groupMemberCount); err != nil {
		t.Fatalf("count actor group memberships for anonymous actor: %v", err)
	}
	if groupMemberCount != 0 {
		t.Errorf("anonymous actor is a member of %d actor group(s), want 0", groupMemberCount)
	}

	readOpIDs, err := integOpReg.SatisfiedBy("read")
	if err != nil {
		t.Fatalf("SatisfiedBy(read): %v", err)
	}

	const accessSQL = `SELECT entity_id FROM accessible_corporation_ids_for_actor($1, $2)`
	rows, err := integPool.Query(ctx, accessSQL, anonID, readOpIDs)
	if err != nil {
		t.Fatalf("query accessible_corporation_ids_for_actor for anonymous actor: %v", err)
	}
	defer rows.Close()

	var found []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan accessible id: %v", err)
		}
		found = append(found, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("accessible_corporation_ids_for_actor(anonymous actor, ...) returned %d row(s), want 0: %v", len(found), found)
	}
}
