//go:build integration

package authz_test

// sessions_invariants_integration_test.go proves the MF101 session invariants
// of the baseline migration, writing directly through SQL: no impersonation
// nesting, the impersonated profile held by the session principal, the
// impersonator profile held by the parent session's principal, immutable
// principal and impersonation columns, a user as session principal, and a
// factor's credential belonging to the session principal.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestInteg_MF101_ImpersonationNesting(t *testing.T) {
	admin, _ := seedUserEntity(t)
	target, targetProfile := seedUserEntity(t)
	adminSession, _ := fxSession(t, integPool, admin, sessionOpts{})

	// A plain impersonation of the target's profile is allowed.
	impersonation, _ := fxSession(t, integPool, target, sessionOpts{ParentID: &adminSession, ImpersonateProfile: &targetProfile})

	// An impersonation cannot be the parent of another impersonation.
	expectState(t, sessionInsert(integPool, target, &impersonation, &targetProfile),
		"MF101", "impersonation session as the parent of an impersonation")
}

func TestInteg_MF101_ImpersonatedProfileHeld(t *testing.T) {
	admin, adminProfile := seedUserEntity(t)
	target, targetProfile := seedUserEntity(t)
	adminSession, _ := fxSession(t, integPool, admin, sessionOpts{})

	expectState(t, sessionInsert(integPool, target, &adminSession, &adminProfile),
		"MF101", "impersonated profile held by someone other than the session principal")
	expectOK(t, sessionInsert(integPool, target, &adminSession, &targetProfile),
		"impersonated profile held by the session principal")
}

func TestInteg_MF101_ImpersonatorProfileHeld(t *testing.T) {
	admin, adminProfile := seedUserEntity(t)
	_, otherProfile := seedUserEntity(t)
	target, targetProfile := seedUserEntity(t)
	adminSession, _ := fxSession(t, integPool, admin, sessionOpts{})

	expectState(t, sessionInsertAs(integPool, target, &adminSession, &targetProfile, &otherProfile),
		"MF101", "impersonator profile held by someone other than the parent session's principal")
	expectOK(t, sessionInsertAs(integPool, target, &adminSession, &targetProfile, &adminProfile),
		"impersonator profile held by the parent session's principal")

	// The impersonator profile is set exactly when the session is an
	// impersonation: an impersonation without one fails the invariant trigger
	// (which runs before sessions_impersonation_complete), a plain session
	// with one fails the check.
	_, err := integPool.Exec(context.Background(), `
INSERT INTO mod_users.sessions (principal_id, acr, amr, auth_time, expires_at,
                                impersonator_session_id, impersonated_profile_id, impersonation_reason)
VALUES ($1, 1, ARRAY['pwd'], now(), now() + interval '1 hour', $2, $3, 'support')`, target, adminSession, targetProfile)
	expectState(t, err, "MF101", "impersonation without an impersonator profile")
	_, err = integPool.Exec(context.Background(), `
INSERT INTO mod_users.sessions (principal_id, acr, amr, auth_time, expires_at, impersonator_profile_id)
VALUES ($1, 1, ARRAY['pwd'], now(), now() + interval '1 hour', $2)`, admin, adminProfile)
	expectState(t, err, "23514", "impersonator profile on a plain session")
}

func TestInteg_MF101_ImmutableColumns(t *testing.T) {
	admin, _ := seedUserEntity(t)
	other, otherProfile := seedUserEntity(t)
	target, targetProfile := seedUserEntity(t)
	adminSession, _ := fxSession(t, integPool, admin, sessionOpts{})
	party := seedOrg(t, "MF101 immutable impersonator org")
	var adminSecondProfile int64
	fxMustTx(t, func(tx pgx.Tx) { adminSecondProfile = fxPartyProfile(t, tx, admin, party, "active") })
	otherSession, _ := fxSession(t, integPool, admin, sessionOpts{})
	imp, _ := fxSession(t, integPool, target, sessionOpts{ParentID: &adminSession, ImpersonateProfile: &targetProfile})
	plain, _ := fxSession(t, integPool, target, sessionOpts{})

	for name, c := range map[string]struct {
		id  int64
		sql string
		arg any
	}{
		"principal_id":            {plain, `UPDATE mod_users.sessions SET principal_id = $2 WHERE id = $1`, other},
		"impersonator_session_id": {imp, `UPDATE mod_users.sessions SET impersonator_session_id = $2 WHERE id = $1`, otherSession},
		"impersonated_profile_id": {imp, `UPDATE mod_users.sessions SET impersonated_profile_id = $2 WHERE id = $1`, otherProfile},
		"impersonation_reason":    {imp, `UPDATE mod_users.sessions SET impersonation_reason = $2 WHERE id = $1`, "changed"},
		"impersonator_profile_id": {imp, `UPDATE mod_users.sessions SET impersonator_profile_id = $2 WHERE id = $1`, adminSecondProfile},
	} {
		expectState(t, rawExec(integPool, c.sql, c.id, c.arg), "MF101", "change "+name)
	}
	// Mutable columns still update.
	expectOK(t, rawExec(integPool, `UPDATE mod_users.sessions SET revoked_at = now(), revoke_reason = 'logout' WHERE id = $1`, plain),
		"revoke a session")
}

func TestInteg_MF101_SessionPrincipalIsUser(t *testing.T) {
	owner := seedOrg(t, "MF101 owner org")
	svc, _ := seedServiceAccount(t, owner)
	var systemActor int64
	if err := integPool.QueryRow(context.Background(), `SELECT core_ensure_system_actor('anonymous', 'Anonymous')`).Scan(&systemActor); err != nil {
		t.Fatalf("resolve anonymous system actor: %v", err)
	}
	user, _ := seedUserEntity(t)

	expectState(t, sessionInsert(integPool, svc, nil, nil), "MF101", "session for a service account")
	expectState(t, sessionInsert(integPool, systemActor, nil, nil), "MF101", "session for a system actor")
	expectOK(t, sessionInsert(integPool, user, nil, nil), "session for a user")
}

func TestInteg_MF101_FactorPrincipalMismatch(t *testing.T) {
	user, _ := seedUserEntity(t)
	other, _ := seedUserEntity(t)
	session, _ := fxSession(t, integPool, user, sessionOpts{})
	own := fxPassword(t, integPool, user, nil)
	foreign := fxPassword(t, integPool, other, nil)

	_, err := integPool.Exec(context.Background(),
		`INSERT INTO mod_users.session_factors (session_id, credential_id) VALUES ($1, $2)`, session, foreign)
	expectState(t, err, "MF101", "factor credential of another principal")
	_, err = integPool.Exec(context.Background(),
		`INSERT INTO mod_users.session_factors (session_id, credential_id) VALUES ($1, $2)`, session, own)
	expectOK(t, err, "factor credential of the session principal")
}
