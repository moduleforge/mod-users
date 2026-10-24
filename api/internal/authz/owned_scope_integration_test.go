//go:build integration

package authz_test

// owned_scope_integration_test.go proves decision U27 consequence (1) against
// the real composed schema: the minimal Authorize port reads only
// scope = 'entity' grants, so an owned-scope grant on a party never acts as an
// entity grant on the party itself (the wildcard and entity arms both filter
// on scope).
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"errors"
	"testing"

	"github.com/moduleforge/core-api/apiresp"
)

// ownedGrant inserts an owned-scope grant: actor holds the operation over every
// entity owned by owner. It returns the SQL error.
func ownedGrant(t *testing.T, actorID, ownerID int64, operationSlug string) {
	t.Helper()
	fxExec(t, integPool, "insert owned-scope grant",
		`INSERT INTO grants (actor_id, operation_id, target_id, scope, granted_by) VALUES ($1, $2, $3, 'owned', NULL)`,
		actorID, opID(t, operationSlug), ownerID)
}

func TestInteg_OwnedScopeGrantOnAPartyDoesNotAuthorizeThePartyEntity(t *testing.T) {
	party := seedOrg(t, "Owned Scope Party")
	member := seedOrg(t, "Owned Scope Bystander")
	_, actor := seedUserEntity(t)

	ownedGrant(t, actor, party, "update")

	// The grant covers entities owned by the party; it is not an entity grant on
	// the party itself, and it is not a wildcard.
	for _, tc := range []struct {
		name   string
		op     string
		target *int64
	}{
		{"update on the party entity", "update", &party},
		{"read on the party entity", "read", &party},
		{"update on an unrelated entity", "update", &member},
		{"update with no target (wildcard arm)", "update", nil},
		{"manage with no target (wildcard arm)", "manage", nil},
	} {
		err := integAZ.Authorize(actorCtx(actor), tc.op, tc.target)
		if err == nil {
			t.Errorf("%s: owned-scope grant authorized it, want ErrForbidden", tc.name)
			continue
		}
		if !errors.Is(err, apiresp.ErrForbidden) {
			t.Errorf("%s: err = %v, want ErrForbidden", tc.name, err)
		}
	}

	// Control: an entity-scope grant on the same party does authorize it, so the
	// denials above are about scope and not about the fixtures.
	targetedGrant(t, actor, party, "update")
	if err := integAZ.Authorize(actorCtx(actor), "update", &party); err != nil {
		t.Errorf("entity-scope grant on the party: err = %v, want nil", err)
	}
	if err := integAZ.Authorize(actorCtx(actor), "update", &member); err == nil {
		t.Errorf("entity-scope grant on the party authorized an unrelated entity")
	}
}

func TestInteg_OwnedScopeGrantIsNotReadAsAWildcardGrant(t *testing.T) {
	party := seedOrg(t, "Owned Scope Wildcard Party")
	target := seedOrg(t, "Owned Scope Wildcard Target")
	_, actor := seedUserEntity(t)

	// Even every operation owned-scope granted over the party leaves the actor
	// without any entity-level authority.
	for _, op := range []string{"read", "update", "manage"} {
		ownedGrant(t, actor, party, op)
	}
	for _, op := range []string{"read", "update", "manage"} {
		if err := integAZ.Authorize(actorCtx(actor), op, &target); err == nil {
			t.Errorf("%s on an arbitrary entity authorized by owned-scope grants", op)
		}
		if err := integAZ.Authorize(actorCtx(actor), op, &party); err == nil {
			t.Errorf("%s on the owner entity authorized by owned-scope grants", op)
		}
	}
}
