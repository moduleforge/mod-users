package authz

import (
	"fmt"

	"github.com/moduleforge/core-api/authz/setup"
)

// AccessRowFilter is the schema-qualified name of mod-users' list-side row
// filter, the SQL predicate mod_users.access_row_permitted (0100_baseline.sql).
// It applies to every listed row the same owning-party gate the Authorizer's
// prelude applies to a single row: it returns true for a trusted system actor
// and otherwise mod_users.owning_party_gate, the one SQL definition both sides
// call (decision F and session_meets_party_policy's decisions D, G, J and L
// included). It follows mod-core's setup.RowFilterGenerator predicate contract.
const AccessRowFilter = "mod_users.access_row_permitted"

// AccessFuncGenerator returns the access-function generator a composition
// root composing mod-users must pass to setup.ApplyFuncs: mod-core's
// grant-table generator wrapped by setup.NewRowFilterGenerator in
// AccessRowFilter, so every accessible_<slug>_ids_for_actor function drops the
// rows the owning-party gate refuses for the acting profile.
//
// Installing the plain setup.NewGrantTableGenerator() instead breaks list and
// single-row parity: a cross-party collaborator below an owning party's
// credential_rule or min_acr would still list rows Authorize refuses. Every
// list query run against these functions must run in a transaction bound with
// setup.BindAuthInputs for the request's context; an unbound list fails closed
// (it silently drops the gated rows). A host that decorates the grant-table
// generator (for example with an extra arm) wraps its decorated generator
// with setup.NewRowFilterGenerator(decorated, AccessRowFilter) instead, so the
// filter covers the added arm too.
func AccessFuncGenerator() (setup.AccessFuncGenerator, error) {
	gen, err := setup.NewRowFilterGenerator(setup.NewGrantTableGenerator(), AccessRowFilter)
	if err != nil {
		return nil, fmt.Errorf("authz: access-function generator: %w", err)
	}
	return gen, nil
}
