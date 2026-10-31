package authz_test

import (
	"strings"
	"testing"

	"github.com/moduleforge/core-api/authz/setup"
	"github.com/moduleforge/mod-users/api/internal/authz"
)

// TestAccessFuncGenerator pins the generator hosts install: the grant-table
// body for a slug, wrapped in a call to mod_users.access_row_permitted with
// the six predicate-contract arguments.
func TestAccessFuncGenerator(t *testing.T) {
	t.Parallel()
	if authz.AccessRowFilter != "mod_users.access_row_permitted" {
		t.Fatalf("AccessRowFilter = %q, want mod_users.access_row_permitted (the 0100_baseline.sql function)", authz.AccessRowFilter)
	}
	gen, err := authz.AccessFuncGenerator()
	if err != nil {
		t.Fatalf("AccessFuncGenerator: %v", err)
	}
	body, err := gen.GenerateForResource("user")
	if err != nil {
		t.Fatalf("GenerateForResource(user): %v", err)
	}
	inner, err := setup.NewGrantTableGenerator().GenerateForResource("user")
	if err != nil {
		t.Fatalf("grant-table GenerateForResource(user): %v", err)
	}
	if !strings.Contains(body, inner) {
		t.Errorf("hooked body does not wrap the grant-table body:\n%s", body)
	}
	if !strings.Contains(body, authz.AccessRowFilter+"(p_actor_entity_id, rf_e.id, rf_e.owner_id,") {
		t.Errorf("hooked body does not call %s with the predicate contract's arguments:\n%s", authz.AccessRowFilter, body)
	}
	if _, err := gen.GenerateForResource("Bad-Slug"); err == nil {
		t.Error("GenerateForResource accepted an invalid slug")
	}
}
