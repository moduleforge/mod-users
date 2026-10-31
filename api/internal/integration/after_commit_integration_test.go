//go:build integration

package integration

// The after-commit observations of the in-transaction creations. mod-core's
// OrganizationService.CreateInTx and NaturalPersonService.CreateInTx observe
// only inside the transaction; the code that owns the commit dispatches the
// post-commit `create`. These tests drive the real handlers and services over
// a real database with a recording observer in the group.

import (
	"context"
	"net/http"
	"testing"
)

// personOf returns the person record id linked to a user.
func (s *stack) personOf(t *testing.T, userID int64) int64 {
	t.Helper()
	var id int64
	if err := s.pool.QueryRow(context.Background(),
		`SELECT person_id FROM mod_users.users WHERE entity_id = $1`, userID).Scan(&id); err != nil {
		t.Fatalf("person of user %d: %v", userID, err)
	}
	return id
}

// requireOneAfterCommit asserts the recorder holds exactly one after-commit
// observation of op and resource, targeting target as the profile actor.
func requireOneAfterCommit(t *testing.T, rec *recorder, op, resource string, target, actor int64) {
	t.Helper()
	if n := rec.afterCommitCount(op, resource); n != 1 {
		t.Fatalf("after-commit %s %s count = %d, want exactly 1 (all: %+v)", op, resource, n, rec.afterCommit())
	}
	for _, o := range rec.afterCommit() {
		if o.Op != op || o.Resource != resource {
			continue
		}
		if o.Target != target {
			t.Errorf("after-commit %s %s target = %d, want %d", op, resource, o.Target, target)
		}
		if !o.HasAuth || o.Auth.ProfileID != actor {
			t.Errorf("after-commit %s %s acting profile = %d (auth %v), want %d (the in-transaction acting profile)", op, resource, o.Auth.ProfileID, o.HasAuth, actor)
		}
	}
}

func TestAfterCommit_RegisterEmitsOnePersonCreate(t *testing.T) {
	env.obs.reset()
	a := env.register(t)
	uid := env.principalID(t, a.Email)
	requireOneAfterCommit(t, env.obs, "create", "natural_person", env.personOf(t, uid), env.personalProfileID(t, uid))
}

func TestAfterCommit_RegisterRollbackEmitsNothing(t *testing.T) {
	a := env.register(t)
	env.obs.reset()
	// The duplicate email fails at the identifier insert, after the person was
	// created in the transaction: the whole registration rolls back.
	env.do(t, http.MethodPost, "/v1/auth/register", "", map[string]string{
		"email": a.Email, "password": testPassword, "given_name": "D", "family_name": "U",
	}).wantStatus(t, http.StatusConflict)
	if got := env.obs.afterCommit(); len(got) != 0 {
		t.Fatalf("rolled-back registration dispatched after-commit observations: %+v", got)
	}
}

func TestAfterCommit_GuestUpgradeEmitsOnePersonCreate(t *testing.T) {
	var guest tokens
	env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": uniq("device")}).
		wantStatus(t, http.StatusCreated).into(t, &guest)
	// Creating the guest creates no person, so nothing is dispatched yet.
	env.obs.reset()

	email := uniq("upgraded") + "@example.test"
	env.do(t, http.MethodPost, "/v1/me/upgrade", guest.access(), map[string]string{
		"email": email, "password": testPassword, "given_name": "Grace", "family_name": "Guest",
	}).wantStatus(t, http.StatusOK)
	uid := env.principalID(t, email)
	requireOneAfterCommit(t, env.obs, "create", "natural_person", env.personOf(t, uid), env.personalProfileID(t, uid))
}

func TestAfterCommit_GuestUpgradeRollbackEmitsNothing(t *testing.T) {
	taken := env.register(t)
	var guest tokens
	env.do(t, http.MethodPost, "/v1/auth/anonymous", "", map[string]string{"device_id": uniq("device")}).
		wantStatus(t, http.StatusCreated).into(t, &guest)
	env.obs.reset()
	// The email is taken: the upgrade fails after the person was created in the
	// transaction, so everything rolls back.
	env.do(t, http.MethodPost, "/v1/me/upgrade", guest.access(), map[string]string{
		"email": taken.Email, "password": testPassword, "given_name": "Grace", "family_name": "Guest",
	}).wantStatus(t, http.StatusConflict)
	if got := env.obs.afterCommit(); len(got) != 0 {
		t.Fatalf("rolled-back upgrade dispatched after-commit observations: %+v", got)
	}
}

func TestAfterCommit_BootstrapOrganization(t *testing.T) {
	s := freshStack(t)
	token := s.mintSetupToken(t)
	s.obs.reset()
	email := "admin@aftercommit.test"
	tk := s.postBootstrap(t, token, bootstrapBody(email, orgOperator())).wantStatus(t, http.StatusCreated).tok(t)
	rows := s.assertBootstrapRows(t, email, "organization", tk)

	// Exactly one organization create (the operator party) and one
	// natural_person create (the first user's own person), both as the first
	// user's personal profile, the actor of the in-transaction observations.
	requireOneAfterCommit(t, s.obs, "create", "organization", rows.partyID, rows.personalProfileID)
	requireOneAfterCommit(t, s.obs, "create", "natural_person", rows.personID, rows.personalProfileID)
}

func TestAfterCommit_BootstrapNaturalPersonOperator(t *testing.T) {
	s := freshStack(t)
	token := s.mintSetupToken(t)
	s.obs.reset()
	email := "solo@aftercommit.test"
	tk := s.postBootstrap(t, token, bootstrapBody(email, map[string]string{"kind": "natural_person"})).
		wantStatus(t, http.StatusCreated).tok(t)
	rows := s.assertBootstrapRows(t, email, "natural_person", tk)

	// The operator party is the user's own person: one natural_person create,
	// no organization create.
	requireOneAfterCommit(t, s.obs, "create", "natural_person", rows.personID, rows.personalProfileID)
	if n := s.obs.afterCommitCount("create", "organization"); n != 0 {
		t.Errorf("organization after-commit creates = %d, want 0", n)
	}
}

func TestAfterCommit_BootstrapRollbackEmitsNothing(t *testing.T) {
	s := freshStack(t)
	token := s.mintSetupToken(t)
	// A weak password is refused before any write; use a bootstrap that fails in
	// the transaction instead: claim the operator party first so the second
	// bootstrap (the loser) rolls back after creating its user and organization.
	s.postBootstrap(t, token, bootstrapBody("first@aftercommit.test", orgOperator())).wantStatus(t, http.StatusCreated)
	s.obs.reset()
	s.postBootstrap(t, token, bootstrapBody("second@aftercommit.test", orgOperator())).wantStatus(t, http.StatusConflict)
	if got := s.obs.afterCommit(); len(got) != 0 {
		t.Fatalf("rolled-back bootstrap dispatched after-commit observations: %+v", got)
	}
}
