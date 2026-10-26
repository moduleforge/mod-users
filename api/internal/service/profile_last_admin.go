package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	coredb "github.com/moduleforge/core-model/db"
)

// CodeLastOperatorAdmin is the detail code of ErrLastOperatorAdmin.
const CodeLastOperatorAdmin = "users.last_operator_admin"

// ErrLastOperatorAdmin is returned when ending or suspending a profile would leave the
// operator party with no active profile holding the wildcard `manage` grant
// (users.last_operator_admin, 409).
var ErrLastOperatorAdmin = apiresp.Conflict(apiresp.FieldError{
	Field: "status", Code: CodeLastOperatorAdmin,
	Message: "the profile is the last active operator administrator and cannot be ended or suspended",
})

// operatorAdminProfileSQL selects the active operator-party profiles holding a
// direct entity-scope wildcard `manage` grant (target NULL, as Bootstrap
// creates it), whose holder principal is not disabled. A grant inherited
// through an actor group is not counted.
const operatorAdminProfileSQL = `
SELECT p.entity_id
  FROM public.profiles p
  JOIN public.platform_settings s ON s.id = 1 AND p.party_id = s.operator_party_id
  JOIN public.principals pr ON pr.entity_id = p.holder_id AND pr.disabled_at IS NULL
 WHERE p.status = 'active'
   AND EXISTS (
     SELECT 1
       FROM public.grants g
       JOIN public.authz_operations o ON o.id = g.operation_id
      WHERE g.actor_id = p.entity_id AND o.slug = $1 AND g.target_id IS NULL AND g.scope = 'entity')`

// lockPlatformSettingsForEndSQL serializes the transitions that take an
// operator administrator out of `active`: each takes this row, only when the
// profile's party is the operator party, before counting the remaining
// administrators, so of two concurrent transitions (an ending and a suspension
// alike) the second sees the first's committed result. No row (a party other
// than the operator party, or no platform_settings row) means nothing to guard.
const lockPlatformSettingsForEndSQL = `SELECT 1 FROM public.platform_settings WHERE id = 1 AND operator_party_id = $1 FOR UPDATE`

// requireNotLastOperatorAdmin refuses (ErrLastOperatorAdmin) to take the locked
// profile p out of `active` (ending or suspending it) when it is an active
// operator administrator and no other active operator administrator exists. It
// runs in the transition's transaction, after the platform_settings row lock
// and before the transition. Profiles that are not active administrators of the
// operator party pass untouched. transitionLocked calls it, so every path that
// ends or suspends a profile, EndInTx included, inherits it.
//
// Counted administrators are active operator-party profiles whose holder
// principal is not disabled (principals.disabled_at IS NULL).
//
// Boundary: the guard does not cover revocation of a wildcard-`manage` grant in
// mod-authz (removing the grant leaves the profile active but no longer an
// administrator), and it does not cover gate-locked administrators (an
// administrator whose profile the party gate would lock out is still counted).
//
// Recorded approximation (an explicit decision, not an oversight): the guard
// reads the mod-authz-owned tables public.grants and public.authz_operations,
// and public.platform_settings, with raw SQL rather than through the
// authorizer. It hardcodes the shape of the grant Bootstrap creates (a direct
// entity-scope wildcard `manage` grant, target NULL). It deliberately ignores a
// `manage` grant inherited through an actor group, so a group-derived
// administrator is neither counted nor protected: the guard can then refuse a
// transition that would not remove the last effective administrator, and a
// differently shaped grant is not seen. Resolving administrators through the
// authorizer interface is the longer-term alternative.
func requireNotLastOperatorAdmin(ctx context.Context, tx pgx.Tx, p coredb.Profile) error {
	if p.Status != ProfileStatusActive || !p.PartyID.Valid {
		return nil
	}
	var locked int
	if err := tx.QueryRow(ctx, lockPlatformSettingsForEndSQL, p.PartyID.Int64).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("users: lock platform settings: %w", err)
	}
	var self, others int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE a.entity_id = $2), count(*) FILTER (WHERE a.entity_id <> $2) FROM (`+
			operatorAdminProfileSQL+`) a`,
		manageOperationSlug, p.EntityID).Scan(&self, &others); err != nil {
		return fmt.Errorf("users: count operator administrators: %w", err)
	}
	if self > 0 && others == 0 {
		return ErrLastOperatorAdmin
	}
	return nil
}
