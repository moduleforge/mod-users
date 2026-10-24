//go:build integration

package integration

// fixtures_integration_test.go holds the SQL fixtures of the suite: parties,
// managed users, party profiles and operator actors. Everything that the
// module's HTTP surface cannot create yet (party administration is a later
// phase) is inserted through plain SQL so the database invariants hold
// whatever the writer.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/mod-users/api/internal/auth"
)

// entityOf inserts an entity of the named type with an optional owner.
func (s *stack) entityOf(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, typeSlug string, ownerID *int64) int64 {
	t.Helper()
	var id int64
	if err := q.QueryRow(context.Background(),
		`INSERT INTO entities (fundamental_type_id, owner_id) SELECT id, $2 FROM types WHERE slug = $1 RETURNING id`,
		typeSlug, ownerID).Scan(&id); err != nil {
		t.Fatalf("insert %s entity: %v", typeSlug, err)
	}
	return id
}

// seedParty inserts a party: an organization entity with its legal_entities
// and organizations rows.
func (s *stack) seedParty(t *testing.T, name string) int64 {
	t.Helper()
	var id int64
	err := pgx.BeginFunc(context.Background(), s.pool, func(tx pgx.Tx) error {
		id = s.entityOf(t, tx, "organization", nil)
		for _, q := range []struct{ sql string }{
			{fmt.Sprintf(`INSERT INTO legal_entities (entity_id) VALUES (%d)`, id)},
			{fmt.Sprintf(`INSERT INTO organizations (entity_id, legal_name, legal_form) VALUES (%d, '%s', 'corporation')`, id, name)},
		} {
			if _, err := tx.Exec(context.Background(), q.sql); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed party: %v", err)
	}
	return id
}

// seedPartyProfile inserts a party profile of a user in the given status and
// returns the profile entity id and uuid.
func (s *stack) seedPartyProfile(t *testing.T, userID, partyID int64, status string) (int64, string) {
	t.Helper()
	var id int64
	var uid string
	err := pgx.BeginFunc(context.Background(), s.pool, func(tx pgx.Tx) error {
		id = s.entityOf(t, tx, "profile", &partyID)
		if _, err := tx.Exec(context.Background(),
			`INSERT INTO profiles (entity_id, holder_id, party_id, status, started_at)
			 VALUES ($1, $2, $3, $4, CASE WHEN $4 = 'invited' THEN NULL ELSE now() END)`,
			id, userID, partyID, status); err != nil {
			return err
		}
		return tx.QueryRow(context.Background(), `SELECT uuid::text FROM entities WHERE id = $1`, id).Scan(&uid)
	})
	if err != nil {
		t.Fatalf("seed party profile: %v", err)
	}
	return id, uid
}

// profileUUID returns the uuid of a profile entity.
func (s *stack) profileUUID(t *testing.T, profileID int64) string {
	t.Helper()
	var uid string
	if err := s.pool.QueryRow(context.Background(), `SELECT uuid::text FROM entities WHERE id = $1`, profileID).Scan(&uid); err != nil {
		t.Fatalf("profile uuid: %v", err)
	}
	return uid
}

// entityUUID returns the uuid of any entity.
func (s *stack) entityUUID(t *testing.T, id int64) string { return s.profileUUID(t, id) }

// seedManagedUser inserts a managed user of the party (no personal profile)
// with a verified email identifier and a password credential, and returns the
// user id. The user holds no profile until seedPartyProfile gives it one.
func (s *stack) seedManagedUser(t *testing.T, partyID int64, email, password string) int64 {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var userID int64
	err = pgx.BeginFunc(context.Background(), s.pool, func(tx pgx.Tx) error {
		userID = s.entityOf(t, tx, "user", &partyID)
		ctx := context.Background()
		if _, err := tx.Exec(ctx, `INSERT INTO principals (entity_id) VALUES ($1)`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO mod_users.users (entity_id, is_guest, managing_party_id) VALUES ($1, false, $2)`, userID, partyID); err != nil {
			return err
		}
		var identID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO mod_users.user_identifiers (user_id, kind, value_canon, verified_at, is_primary)
			 VALUES ($1, 'email', $2, now(), true) RETURNING id`, userID, email).Scan(&identID); err != nil {
			return err
		}
		var credID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO mod_users.credentials (principal_id, kind) VALUES ($1, 'password') RETURNING id`, userID).Scan(&credID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO mod_users.cred_password (credential_id, identifier_id, hash, principal_id) VALUES ($1, $2, $3, 0)`,
			credID, identID, hash)
		return err
	})
	if err != nil {
		t.Fatalf("seed managed user: %v", err)
	}
	return userID
}

var (
	operatorOnce  sync.Once
	operatorParty int64
)

// operatorPartyID returns the deployment's operator party, claiming it on
// first use (the claim is set-once for the life of the database).
func (s *stack) operatorPartyID(t *testing.T) int64 {
	t.Helper()
	operatorOnce.Do(func() {
		id := s.seedParty(t, "Integration Operator Party")
		var claimed bool
		if err := s.pool.QueryRow(context.Background(), `SELECT core_claim_operator_party($1)`, id).Scan(&claimed); err != nil {
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

// makeOperator gives the registered account an active profile in the operator
// party with a wildcard manage grant, and returns an access token acting as
// that operator profile (a switch from the account's session).
func (s *stack) makeOperator(t *testing.T, a account) (tok tokens, profileID int64) {
	t.Helper()
	party := s.operatorPartyID(t)
	userID := s.principalID(t, a.Email)
	profileID, profUUID := s.seedPartyProfile(t, userID, party, "active")
	var opID int64
	if err := s.pool.QueryRow(context.Background(), `SELECT id FROM authz_operations WHERE slug = 'manage'`).Scan(&opID); err != nil {
		t.Fatalf("resolve manage: %v", err)
	}
	s.exec(t, "wildcard grant",
		`INSERT INTO grants (actor_id, operation_id, target_id, granted_by) VALUES ($1, $2, NULL, NULL) ON CONFLICT DO NOTHING`,
		profileID, opID)
	r := s.do(t, "POST", "/v1/session/switch", a.tk.access(), map[string]string{"profile_uuid": profUUID}).wantStatus(t, 200)
	return r.tok(t), profileID
}
