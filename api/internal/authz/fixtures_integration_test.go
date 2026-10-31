//go:build integration

package authz_test

// fixtures_integration_test.go holds the SQL-only fixtures shared by the
// integration suites. Everything inserts through plain SQL, so the database
// invariants under test hold whatever the writer, and no suite depends on the
// service layer.
//
// Identity model recap (see the baseline migration): a user is an entity of
// type `user` with a principals row and a mod_users.users row. A bring-your-own
// user owns itself and has exactly one personal profile; a managed user is
// owned by its managing party and has none; a guest is a bring-your-own user
// with is_guest set. Grants are held by profiles. Wildcard and type-entity
// grants go only to operator actors: profiles of the party claimed with
// core_claim_operator_party, or system actors' profiles.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/moduleforge/core-api/opctx"
)

// fxQ is the subset of pgxpool.Pool and pgx.Tx the fixtures need.
type fxQ interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var fxCounter atomic.Int64

// fxTag returns a unique suffix for names and values.
func fxTag(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, fxCounter.Add(1))
}

// fxTx runs fn inside a transaction and commits it. It returns the commit
// error, so a test can assert on a deferred-constraint failure. A failure
// inside fn is fatal.
func fxTx(t *testing.T, fn func(tx pgx.Tx)) error {
	t.Helper()
	ctx := context.Background()
	tx, err := integPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			// fn failed the test (runtime.Goexit) or Commit errored; release the connection.
			_ = tx.Rollback(ctx)
		}
	}()
	fn(tx)
	committed = true
	return tx.Commit(ctx)
}

// fxMustTx is fxTx with a fatal commit failure.
func fxMustTx(t *testing.T, fn func(tx pgx.Tx)) {
	t.Helper()
	if err := fxTx(t, fn); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// expectState fails the test unless err carries the SQLSTATE want.
func expectState(t *testing.T, err error, want, what string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: got nil error, want SQLSTATE %s", what, want)
		return
	}
	if got := sqlState(err); got != want {
		t.Errorf("%s: SQLSTATE %q (%v), want %s", what, got, err, want)
	}
}

// expectOK fails the test when err is non-nil.
func expectOK(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: got %v, want success", what, err)
	}
}

// expectConstraint fails the test unless err is a unique violation (23505) on
// the named index.
func expectUnique(t *testing.T, err error, index, what string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Errorf("%s: got %v, want unique violation on %s", what, err, index)
		return
	}
	if pgErr.Code != "23505" || pgErr.ConstraintName != index {
		t.Errorf("%s: got code %s constraint %q, want 23505 on %q", what, pgErr.Code, pgErr.ConstraintName, index)
	}
}

func fxScanID(t *testing.T, q fxQ, what, sql string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := q.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return id
}

func fxExec(t *testing.T, q fxQ, what, sql string, args ...any) {
	t.Helper()
	if _, err := q.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// ---------------------------------------------------------------------------
// Entities, parties, people
// ---------------------------------------------------------------------------

// fxEntity inserts an entity of the named type with an optional owner.
func fxEntity(t *testing.T, q fxQ, typeSlug string, ownerID *int64) int64 {
	t.Helper()
	return fxScanID(t, q, "insert "+typeSlug+" entity",
		`INSERT INTO entities (fundamental_type_id, owner_id) SELECT id, $2 FROM types WHERE slug = $1 RETURNING id`,
		typeSlug, ownerID)
}

// fxOrg inserts a party: an organization entity with its legal_entities and
// organizations rows.
func fxOrg(t *testing.T, q fxQ, name string) int64 {
	t.Helper()
	id := fxEntity(t, q, "organization", nil)
	fxExec(t, q, "insert legal_entities", `INSERT INTO legal_entities (entity_id) VALUES ($1)`, id)
	fxExec(t, q, "insert organizations",
		`INSERT INTO organizations (entity_id, legal_name, legal_form) VALUES ($1, $2, 'corporation')`, id, name)
	return id
}

// seedOrg is fxOrg in its own transaction.
func seedOrg(t *testing.T, name string) int64 {
	t.Helper()
	var id int64
	fxMustTx(t, func(tx pgx.Tx) { id = fxOrg(t, tx, name) })
	return id
}

// fxNaturalPerson inserts a natural_person entity (a person record, not a
// user) with an optional owner.
func fxNaturalPerson(t *testing.T, q fxQ, ownerID *int64) int64 {
	t.Helper()
	id := fxEntity(t, q, "natural_person", ownerID)
	fxExec(t, q, "insert legal_entities", `INSERT INTO legal_entities (entity_id) VALUES ($1)`, id)
	fxExec(t, q, "insert natural_persons", `INSERT INTO natural_persons (entity_id) VALUES ($1)`, id)
	return id
}

// seedNaturalPerson is fxNaturalPerson, unowned, in its own transaction.
func seedNaturalPerson(t *testing.T) int64 {
	t.Helper()
	var id int64
	fxMustTx(t, func(tx pgx.Tx) { id = fxNaturalPerson(t, tx, nil) })
	return id
}

// ownerSubject maps a personal profile to its holder (the ownership subject the
// authorization own arm compares against); any other id is returned unchanged.
func ownerSubject(t *testing.T, id int64) int64 {
	t.Helper()
	var subject *int64
	if err := integPool.QueryRow(context.Background(), `SELECT profile_own_subject($1)`, id).Scan(&subject); err != nil {
		t.Fatalf("profile_own_subject(%d): %v", id, err)
	}
	if subject == nil {
		return id
	}
	return *subject
}

// seedOwnedOrg inserts an organization owned by ownerID (a user or a legal
// entity) from insert time (owner_id is immutable afterwards).
func seedOwnedOrg(t *testing.T, ownerID int64, name string) int64 {
	t.Helper()
	ownerID = ownerSubject(t, ownerID)
	var id int64
	fxMustTx(t, func(tx pgx.Tx) {
		id = fxEntity(t, tx, "organization", &ownerID)
		fxExec(t, tx, "insert legal_entities", `INSERT INTO legal_entities (entity_id) VALUES ($1)`, id)
		fxExec(t, tx, "insert organizations",
			`INSERT INTO organizations (entity_id, legal_name, legal_form) VALUES ($1, $2, 'corporation')`, id, name)
	})
	return id
}

// seedUnownedOrg inserts an organization whose owner_id stays NULL.
func seedUnownedOrg(t *testing.T, name string) int64 {
	t.Helper()
	return seedOrg(t, name)
}

// ---------------------------------------------------------------------------
// Users and profiles
// ---------------------------------------------------------------------------

// userOpts shapes fxUser.
type userOpts struct {
	ManagingPartyID *int64
	Guest           bool
	PersonID        *int64
	// NoPersonalProfile skips the personal profile (a bring-your-own user
	// created this way fails at commit).
	NoPersonalProfile bool
}

// fxUser inserts the user entity, principals row, users row and, for a
// bring-your-own user, the personal profile. It returns the user id and the
// personal profile id (0 when none was created).
func fxUser(t *testing.T, q fxQ, o userOpts) (userID, profileID int64) {
	t.Helper()
	userID = fxEntity(t, q, "user", o.ManagingPartyID) // NULL owner defaults to the user itself
	fxExec(t, q, "insert principals", `INSERT INTO principals (entity_id) VALUES ($1)`, userID)
	fxExec(t, q, "insert users",
		`INSERT INTO mod_users.users (entity_id, is_guest, managing_party_id, person_id) VALUES ($1, $2, $3, $4)`,
		userID, o.Guest, o.ManagingPartyID, o.PersonID)
	if o.ManagingPartyID == nil && !o.NoPersonalProfile {
		profileID = fxPersonalProfile(t, q, userID)
	}
	return userID, profileID
}

// fxPersonalProfile inserts the personal profile of a user.
func fxPersonalProfile(t *testing.T, q fxQ, userID int64) int64 {
	t.Helper()
	id := fxEntity(t, q, "profile", &userID)
	fxExec(t, q, "insert personal profile",
		`INSERT INTO profiles (entity_id, holder_id, party_id, status, started_at) VALUES ($1, $2, NULL, 'active', now())`,
		id, userID)
	return id
}

// fxPartyProfile inserts a party profile of a user. The profile entity is
// owned by the party (its ownership subject).
func fxPartyProfile(t *testing.T, q fxQ, userID, partyID int64, status string) int64 {
	t.Helper()
	id := fxEntity(t, q, "profile", &partyID)
	fxExec(t, q, "insert party profile",
		`INSERT INTO profiles (entity_id, holder_id, party_id, status, started_at) VALUES ($1, $2, $3, $4, CASE WHEN $4 = 'invited' THEN NULL ELSE now() END)`,
		id, userID, partyID, status)
	return id
}

// seedUserEntity inserts a bring-your-own user with its personal profile.
func seedUserEntity(t *testing.T) (userID, profileID int64) {
	t.Helper()
	fxMustTx(t, func(tx pgx.Tx) { userID, profileID = fxUser(t, tx, userOpts{}) })
	return userID, profileID
}

// seedUserID returns only the user entity id (for use as a target or owner).
func seedUserID(t *testing.T) int64 {
	t.Helper()
	id, _ := seedUserEntity(t)
	return id
}

// seedUser inserts a bring-your-own user and returns its personal profile id,
// the actor of an ordinary, non-operator caller. The tag documents the call
// site. When withWildcardManage is true it returns instead an operator profile
// holding a wildcard manage grant (see seedOperator).
func seedUser(t *testing.T, tag string, withWildcardManage bool) int64 {
	t.Helper()
	_ = tag
	if withWildcardManage {
		return seedOperator(t, "manage")
	}
	_, profileID := seedUserEntity(t)
	return profileID
}

// ---------------------------------------------------------------------------
// Operator actors
// ---------------------------------------------------------------------------

var (
	operatorOnce  sync.Once
	operatorParty int64
)

// operatorPartyID returns the deployment's operator party, claiming it on first
// use. The claim is set-once for the life of the database.
func operatorPartyID(t *testing.T) int64 {
	t.Helper()
	operatorOnce.Do(func() {
		var id int64
		fxMustTx(t, func(tx pgx.Tx) { id = fxOrg(t, tx, "Integration Operator Party") })
		var claimed bool
		if err := integPool.QueryRow(context.Background(), `SELECT core_claim_operator_party($1)`, id).Scan(&claimed); err != nil {
			t.Fatalf("claim operator party: %v", err)
		}
		if !claimed {
			t.Fatalf("operator party was already claimed")
		}
		operatorParty = id
	})
	if operatorParty == 0 {
		t.Fatalf("operator party unavailable")
	}
	return operatorParty
}

// seedOperator inserts a fresh user with an active profile in the operator
// party (an operator actor) and gives that profile a wildcard grant for each
// named operation. It returns the party profile id.
func seedOperator(t *testing.T, wildcardOps ...string) int64 {
	t.Helper()
	party := operatorPartyID(t)
	var profileID int64
	fxMustTx(t, func(tx pgx.Tx) {
		userID, _ := fxUser(t, tx, userOpts{})
		profileID = fxPartyProfile(t, tx, userID, party, "active")
	})
	for _, op := range wildcardOps {
		seedWildcardGrant(t, profileID, op)
	}
	return profileID
}

// seedOperatorUser is seedOperator that also returns the holder user id.
func seedOperatorUser(t *testing.T) (userID, profileID int64) {
	t.Helper()
	party := operatorPartyID(t)
	fxMustTx(t, func(tx pgx.Tx) {
		userID, _ = fxUser(t, tx, userOpts{})
		profileID = fxPartyProfile(t, tx, userID, party, "active")
	})
	return userID, profileID
}

// actorCtx returns a context acting as the profile with the given entity id,
// in a session that just authenticated (AuthTime now), so the engine's
// assurance gate passes for every seeded operation, including those with a
// max_auth_age (impersonate, credentials). The session id is a placeholder: the
// owning-party gate reads the session's factors only for an owner with a
// restrictive policy, and those scenarios use real sessions.
func actorCtx(profileID int64) context.Context {
	var principal int64
	_ = integPool.QueryRow(context.Background(), `SELECT holder_id FROM profiles WHERE entity_id = $1`, profileID).Scan(&principal)
	return opctx.WithAuthContext(context.Background(), opctx.AuthContext{
		PrincipalID: principal,
		ProfileID:   profileID,
		SessionID:   1,
		ACR:         1,
		AuthTime:    time.Now(),
	})
}

// ---------------------------------------------------------------------------
// Identifiers, credentials, sessions
// ---------------------------------------------------------------------------

func fxIdentifier(t *testing.T, q fxQ, userID int64, kind, value string) int64 {
	t.Helper()
	return fxScanID(t, q, "insert identifier",
		`INSERT INTO mod_users.user_identifiers (user_id, kind, value_canon) VALUES ($1, $2, $3) RETURNING id`,
		userID, kind, value)
}

// fxCredential inserts the common credentials row and returns its id and uuid.
func fxCredential(t *testing.T, q fxQ, principalID int64, kind string) (id int64, uuid string) {
	t.Helper()
	if err := q.QueryRow(context.Background(),
		`INSERT INTO mod_users.credentials (principal_id, kind) VALUES ($1, $2) RETURNING id, uuid::text`,
		principalID, kind).Scan(&id, &uuid); err != nil {
		t.Fatalf("insert %s credential: %v", kind, err)
	}
	return id, uuid
}

// credInsert inserts a credentials row and returns the raw error.
func credInsert(q fxQ, principalID int64, kind string) (int64, error) {
	var id int64
	err := q.QueryRow(context.Background(),
		`INSERT INTO mod_users.credentials (principal_id, kind) VALUES ($1, $2) RETURNING id`, principalID, kind).Scan(&id)
	return id, err
}

func fxPasswordSub(q fxQ, credID int64, identifierID *int64) error {
	_, err := q.Exec(context.Background(),
		`INSERT INTO mod_users.cred_password (credential_id, identifier_id, hash, principal_id) VALUES ($1, $2, 'argon2id$test', 0)`,
		credID, identifierID)
	return err
}

func fxEmailOTPSub(q fxQ, credID, identifierID int64) error {
	_, err := q.Exec(context.Background(),
		`INSERT INTO mod_users.cred_email_otp (credential_id, identifier_id) VALUES ($1, $2)`, credID, identifierID)
	return err
}

func fxOIDCSub(q fxQ, credID int64, issuer, provider, subject string) error {
	_, err := q.Exec(context.Background(),
		`INSERT INTO mod_users.cred_oidc (credential_id, issuer, provider_id, subject) VALUES ($1, $2, $3, $4)`,
		credID, issuer, provider, subject)
	return err
}

func fxSSHSub(q fxQ, credID int64, fingerprint string) error {
	_, err := q.Exec(context.Background(),
		`INSERT INTO mod_users.cred_ssh_key (credential_id, fingerprint_sha256, key_type, public_key) VALUES ($1, $2, 'ssh-ed25519', $3)`,
		credID, fingerprint, "ssh-ed25519 AAAA"+fingerprint)
	return err
}

func fxAPIKeySub(q fxQ, credID int64) error {
	_, err := q.Exec(context.Background(),
		`INSERT INTO mod_users.cred_api_key (credential_id, client_id, secret_hash) VALUES ($1, $2, 'hash')`,
		credID, fxTag("client"))
	return err
}

func fxAnonSub(q fxQ, credID int64) error {
	_, err := q.Exec(context.Background(),
		`INSERT INTO mod_users.cred_anon (credential_id, device_id, token_hash) VALUES ($1, $2, $3)`,
		credID, fxTag("device"), fxTag("token"))
	return err
}

// fxPassword inserts a complete password credential.
func fxPassword(t *testing.T, q fxQ, principalID int64, identifierID *int64) int64 {
	t.Helper()
	id, _ := fxCredential(t, q, principalID, "password")
	if err := fxPasswordSub(q, id, identifierID); err != nil {
		t.Fatalf("insert cred_password: %v", err)
	}
	return id
}

// fxOIDC inserts a complete oidc credential.
func fxOIDC(t *testing.T, q fxQ, principalID int64, issuer, provider, subject string) int64 {
	t.Helper()
	id, _ := fxCredential(t, q, principalID, "oidc")
	if err := fxOIDCSub(q, id, issuer, provider, subject); err != nil {
		t.Fatalf("insert cred_oidc: %v", err)
	}
	return id
}

// fxSSHKey inserts a complete ssh_key credential.
func fxSSHKey(t *testing.T, q fxQ, principalID int64, fingerprint string) (id int64, uuid string) {
	t.Helper()
	id, uuid = fxCredential(t, q, principalID, "ssh_key")
	if err := fxSSHSub(q, id, fingerprint); err != nil {
		t.Fatalf("insert cred_ssh_key: %v", err)
	}
	return id, uuid
}

// fxAPIKey inserts a complete api_key credential.
func fxAPIKey(t *testing.T, q fxQ, principalID int64) (id int64, uuid string) {
	t.Helper()
	id, uuid = fxCredential(t, q, principalID, "api_key")
	if err := fxAPIKeySub(q, id); err != nil {
		t.Fatalf("insert cred_api_key: %v", err)
	}
	return id, uuid
}

// fxRevoke revokes a credential (credentials are revoked, never deleted).
func fxRevoke(t *testing.T, q fxQ, credID int64) {
	t.Helper()
	fxExec(t, q, "revoke credential",
		`UPDATE mod_users.credentials SET revoked_at = now(), revoke_reason = 'user' WHERE id = $1`, credID)
}

func fxOIDCProvider(t *testing.T, q fxQ, id string, trustACR bool) {
	t.Helper()
	fxExec(t, q, "insert oidc provider",
		`INSERT INTO mod_users.oidc_providers (id, trust_acr) VALUES ($1, $2)`, id, trustACR)
}

// fxPolicy sets a party's authentication policy.
func fxPolicy(t *testing.T, q fxQ, partyID int64, rule string, provider *string, minACR int) {
	t.Helper()
	fxExec(t, q, "insert party_authn_policies",
		`INSERT INTO mod_users.party_authn_policies (party_id, credential_rule, required_oidc_provider_id, min_acr) VALUES ($1, $2, $3, $4)`,
		partyID, rule, provider, minACR)
}

// sessionOpts shapes fxSession.
type sessionOpts struct {
	ACR                int
	ExpiresIn          string // Postgres interval, default '1 hour'
	ParentID           *int64
	ImpersonateProfile *int64
	// ImpersonatorProfile is the administrator's acting profile of an
	// impersonation; nil picks a profile held by the parent session's principal.
	ImpersonatorProfile *int64
}

// impersonatorProfileSQL is the impersonator_profile_id the fixtures insert:
// NULL without a parent ($4), else $7 or, when $7 is NULL, a profile held by
// the parent session's principal.
const impersonatorProfileSQL = `CASE WHEN $4::bigint IS NULL THEN NULL ELSE COALESCE($7::bigint,
  (SELECT min(p.entity_id) FROM public.profiles p
     JOIN mod_users.sessions ps ON ps.principal_id = p.holder_id
    WHERE ps.id = $4::bigint)) END`

// fxSession inserts a session and returns its id and uuid.
func fxSession(t *testing.T, q fxQ, principalID int64, o sessionOpts) (id int64, uuid string) {
	t.Helper()
	if o.ACR == 0 {
		o.ACR = 1
	}
	if o.ExpiresIn == "" {
		o.ExpiresIn = "1 hour"
	}
	var reason *string
	if o.ParentID != nil {
		r := "support"
		reason = &r
	}
	if err := q.QueryRow(context.Background(), `
INSERT INTO mod_users.sessions (principal_id, acr, amr, auth_time, expires_at,
                                impersonator_session_id, impersonated_profile_id, impersonation_reason,
                                impersonator_profile_id)
VALUES ($1, $2, ARRAY['pwd'], now(), now() + $3::interval, $4, $5, $6, `+impersonatorProfileSQL+`)
RETURNING id, uuid::text`,
		principalID, o.ACR, o.ExpiresIn, o.ParentID, o.ImpersonateProfile, reason, o.ImpersonatorProfile).Scan(&id, &uuid); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return id, uuid
}

// sessionInsert inserts a session and returns the raw error. An
// impersonation's impersonator_profile_id is a profile held by the parent
// session's principal.
func sessionInsert(q fxQ, principalID int64, parentID, impersonatedProfile *int64) error {
	return sessionInsertAs(q, principalID, parentID, impersonatedProfile, nil)
}

// sessionInsertAs is sessionInsert with an explicit impersonator_profile_id
// (nil: a profile held by the parent session's principal).
func sessionInsertAs(q fxQ, principalID int64, parentID, impersonatedProfile, impersonatorProfile *int64) error {
	var reason *string
	if parentID != nil {
		r := "support"
		reason = &r
	}
	_, err := q.Exec(context.Background(), `
INSERT INTO mod_users.sessions (principal_id, acr, amr, auth_time, expires_at,
                                impersonator_session_id, impersonated_profile_id, impersonation_reason,
                                impersonator_profile_id)
VALUES ($1, $2, ARRAY['pwd'], now(), now() + $3::interval, $4, $5, $6, `+impersonatorProfileSQL+`)`,
		principalID, 1, "1 hour", parentID, impersonatedProfile, reason, impersonatorProfile)
	return err
}

func fxFactor(t *testing.T, q fxQ, sessionID, credID int64, assertedACR *int) {
	t.Helper()
	fxExec(t, q, "insert session factor",
		`INSERT INTO mod_users.session_factors (session_id, credential_id, asserted_acr) VALUES ($1, $2, $3)`,
		sessionID, credID, assertedACR)
}

// ---------------------------------------------------------------------------
// Grants
// ---------------------------------------------------------------------------

func opID(t *testing.T, slug string) int64 {
	t.Helper()
	return fxScanID(t, integPool, "resolve op "+slug, `SELECT id FROM authz_operations WHERE slug = $1`, slug)
}

// seedWildcardGrant inserts a wildcard grant (target_id IS NULL). The actor
// must be an operator actor (mod-authz's operator trigger).
func seedWildcardGrant(t *testing.T, actorID int64, operationSlug string) {
	t.Helper()
	fxExec(t, integPool, "insert wildcard grant",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, NULL, NULL) ON CONFLICT DO NOTHING`,
		actorID, opID(t, operationSlug))
}

// deleteWildcardGrant removes the wildcard grant for (actor, operation).
func deleteWildcardGrant(t *testing.T, actorID int64, operationSlug string) {
	t.Helper()
	fxExec(t, integPool, "delete wildcard grant",
		`DELETE FROM grants WHERE actor_id = $1 AND operation_id = $2 AND target_id IS NULL`, actorID, opID(t, operationSlug))
}

// targetedGrant inserts an entity-scope grant (actor, op, target).
func targetedGrant(t *testing.T, actorID, targetID int64, operationSlug string) {
	t.Helper()
	fxExec(t, integPool, "insert targeted grant",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, $3, NULL) ON CONFLICT DO NOTHING`,
		actorID, opID(t, operationSlug), targetID)
}

// seedActorGroup inserts an authz actor group entity and its row.
func seedActorGroup(t *testing.T, name string) int64 {
	t.Helper()
	var id int64
	fxMustTx(t, func(tx pgx.Tx) {
		// Actor groups that hold type-entity grants must be owned by the
		// operator party.
		party := operatorPartyID(t)
		id = fxEntity(t, tx, "authz_actor_group", &party)
		fxExec(t, tx, "insert authz_actor_groups", `INSERT INTO authz_actor_groups (entity_id, name) VALUES ($1, $2)`, id, name)
	})
	return id
}

// organizationTypeID resolves the 'organization' type's internal id.
func organizationTypeID(t *testing.T) int64 {
	t.Helper()
	return fxScanID(t, integPool, "resolve organization type", `SELECT id FROM types WHERE slug = 'organization'`)
}

// seedNaturalPerson2 is seedNaturalPerson, named for the call sites that need a
// natural_person instance (a person record, not a user) as a grant target.
func seedNaturalPerson2(t *testing.T) int64 { return seedNaturalPerson(t) }
