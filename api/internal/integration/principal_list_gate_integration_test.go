//go:build integration

package integration

// principal_list_gate_integration_test.go proves the owning-party gate on the
// principal list over HTTP: GET /v1/principals runs its queries in a
// transaction bound with setup.BindAuthInputs, and the access functions are
// the row-filtered ones the server installs (localAuthz.AccessFuncGenerator),
// so an outside collaborator below a party's authentication policy lists none
// of the party's principals it reaches through a grant, exactly as the
// single-row route refuses each of them, and lists them once its session
// meets the policy.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	authzdb "github.com/moduleforge/authz-model/db"
)

// listedPrincipals returns the uuids of GET /v1/principals?kind=user for the
// token, and asserts the page is complete (total = page length).
func (s *stack) listedPrincipals(t *testing.T, token string) map[string]bool {
	t.Helper()
	var page struct {
		Principals []struct {
			UUID string `json:"uuid"`
		} `json:"principals"`
		Total int64 `json:"total"`
	}
	s.do(t, http.MethodGet, "/v1/principals?kind=user&limit=100", token, nil).wantStatus(t, http.StatusOK).into(t, &page)
	if int64(len(page.Principals)) != page.Total {
		t.Fatalf("principal page holds %d of %d; the fixture expects one page", len(page.Principals), page.Total)
	}
	out := make(map[string]bool, len(page.Principals))
	for _, p := range page.Principals {
		out[p.UUID] = true
	}
	return out
}

func TestPrincipalList_OwningPartyGate(t *testing.T) {
	s := env
	ctx := context.Background()

	// A party whose credential_rule is 'authorized', and two managed users it
	// owns.
	party := s.seedParty(t, "List Gate Party "+uniq("p"))
	s.exec(t, "party policy",
		`INSERT INTO mod_users.party_authn_policies (party_id, credential_rule, min_acr) VALUES ($1, 'authorized', 1)`, party)
	managed := []string{
		s.entityUUID(t, s.seedManagedUser(t, party, uniq("managed")+"@example.test", testPassword)),
		s.entityUUID(t, s.seedManagedUser(t, party, uniq("managed")+"@example.test", testPassword)),
	}

	// An outside collaborator (its personal profile) holding read and list over
	// everything the party owns, through owned-scope grants.
	collab := s.register(t)
	collabUser := s.principalID(t, collab.Email)
	collabProfile := s.personalProfileID(t, collabUser)
	for _, op := range []string{"read", "list"} {
		s.exec(t, "owned-scope grant "+op,
			`INSERT INTO grants (actor_id, operation_id, target_id, scope, granted_by)
			 SELECT $1, o.id, $2, 'owned', NULL FROM authz_operations o WHERE o.slug = $3`,
			collabProfile, party, op)
	}
	tk := s.login(t, collab)
	self := s.entityUUID(t, collabUser)

	// Below the policy: the password factor is not authorized for the party.
	listed := s.listedPrincipals(t, tk.access())
	if !listed[self] {
		t.Errorf("below the policy: the collaborator's own record (ungated) is missing from its list")
	}
	for _, id := range managed {
		if listed[id] {
			t.Errorf("below the policy: GET /v1/principals listed the party's managed user %s", id)
		}
		// The single-row route refuses the same row (masked as not found).
		s.do(t, http.MethodGet, "/v1/principals/"+id, tk.access(), nil).wantStatus(t, http.StatusNotFound)
	}

	// Meeting the policy: authorize the session's password factor for the
	// party. The same session now lists and reads the managed users.
	var credID int64
	if err := s.pool.QueryRow(ctx,
		`SELECT id FROM mod_users.credentials WHERE principal_id = $1 AND kind = 'password' AND revoked_at IS NULL`,
		collabUser).Scan(&credID); err != nil {
		t.Fatalf("collaborator password credential: %v", err)
	}
	s.exec(t, "authorize credential",
		`INSERT INTO mod_users.credential_party_authorizations (credential_id, party_id, authorized_by_profile_id) VALUES ($1, $2, $3)`,
		credID, party, collabProfile)

	listed = s.listedPrincipals(t, tk.access())
	for _, id := range managed {
		if !listed[id] {
			t.Errorf("meeting the policy: GET /v1/principals did not list the party's managed user %s", id)
		}
		s.do(t, http.MethodGet, "/v1/principals/"+id, tk.access(), nil).wantStatus(t, http.StatusOK)
	}
	if !listed[self] {
		t.Errorf("meeting the policy: the collaborator's own record is missing from its list")
	}
}

// setListPolicy rewrites the list operation's assurance policy and reloads the
// engine's operation registry, restoring the original policy when the test
// ends. The registry caches policies at startup, so a table update alone would
// not reach the running engine.
func (s *stack) setListPolicy(t *testing.T, minACR int, maxAuthAge string) {
	t.Helper()
	ctx := context.Background()
	var origACR int
	var origAge *string
	if err := s.pool.QueryRow(ctx,
		`SELECT min_acr, max_auth_age::text FROM authz_operations WHERE slug = 'list'`).Scan(&origACR, &origAge); err != nil {
		t.Fatalf("read list policy: %v", err)
	}
	apply := func(acr int, age *string) {
		if _, err := s.pool.Exec(ctx,
			`UPDATE authz_operations SET min_acr = $1, max_auth_age = $2::interval WHERE slug = 'list'`, acr, age); err != nil {
			t.Fatalf("set list policy: %v", err)
		}
		if err := s.opReg.Reload(ctx, authzdb.New(s.pool)); err != nil {
			t.Fatalf("reload operation registry: %v", err)
		}
	}
	// The restore runs even when the test fails, so a failure here cannot leak a
	// tightened list policy into the suites that follow.
	t.Cleanup(func() { apply(origACR, origAge) })
	var age *string
	if maxAuthAge != "" {
		age = &maxAuthAge
	}
	apply(minACR, age)
}

// TestPrincipalList_AssuranceGate proves the list operation's min_acr and
// max_auth_age refuse GET /v1/principals as a single-row Authorize is refused
// (409 users.step_up_required): the row filter never applies them, so the list
// service must. A session that meets the policy lists as before, and a caller
// with no list grants still gets an empty page rather than a new 403.
func TestPrincipalList_AssuranceGate(t *testing.T) {
	s := env
	a := s.register(t)
	tk := s.login(t, a)
	principal := s.principalID(t, a.Email)

	// A session below the operation's min_acr is refused. Every password
	// session sits below acr 3, so the policy cannot be met.
	s.setListPolicy(t, 3, "")
	s.do(t, http.MethodGet, "/v1/principals?kind=user", tk.access(), nil).wantAction(t, http.StatusConflict, "users.step_up_required")

	// A session past the operation's max_auth_age is refused.
	s.setListPolicy(t, 1, "10 minutes")
	s.exec(t, "age auth_time",
		`UPDATE mod_users.sessions SET auth_time = now() - interval '11 minutes' WHERE uuid = $1::uuid`, tk.Session.UUID)
	s.do(t, http.MethodGet, "/v1/principals?kind=user", tk.access(), nil).wantAction(t, http.StatusConflict, "users.step_up_required")

	// A session that meets the policy lists as before: its own record.
	s.exec(t, "fresh auth_time",
		`UPDATE mod_users.sessions SET auth_time = now() WHERE uuid = $1::uuid`, tk.Session.UUID)
	if listed := s.listedPrincipals(t, tk.access()); !listed[s.entityUUID(t, principal)] {
		t.Errorf("a session meeting the list policy did not list its own record")
	}

	// A caller with no list grants over the kind still gets an empty page, not a
	// 403: the gate adds no refusal beyond step-up.
	var page struct {
		Principals []json.RawMessage `json:"principals"`
		Total      int64             `json:"total"`
	}
	s.do(t, http.MethodGet, "/v1/principals?kind=service_account", tk.access(), nil).wantStatus(t, http.StatusOK).into(t, &page)
	if len(page.Principals) != 0 || page.Total != 0 {
		t.Errorf("a caller with no service-account grants listed %d (total %d), want an empty page", len(page.Principals), page.Total)
	}
}
