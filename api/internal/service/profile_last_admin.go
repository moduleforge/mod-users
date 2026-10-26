package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	coredb "github.com/moduleforge/core-model/db"
)

// CodeLastOperatorAdmin is the detail code of ErrLastOperatorAdmin.
const CodeLastOperatorAdmin = "users.last_operator_admin"

// ErrLastOperatorAdmin is returned when ending a profile would leave the
// operator party with no active profile holding the wildcard `manage` grant
// (users.last_operator_admin, 409).
var ErrLastOperatorAdmin = apiresp.Conflict(apiresp.FieldError{
	Field: "status", Code: CodeLastOperatorAdmin,
	Message: "the profile is the last active operator administrator and cannot be ended",
})

// operatorAdminProfileSQL selects the active operator-party profiles holding a
// direct entity-scope wildcard `manage` grant (target NULL, as Bootstrap
// creates it). A grant inherited through an actor group is not counted.
const operatorAdminProfileSQL = `
SELECT p.entity_id
  FROM public.profiles p
  JOIN public.platform_settings s ON s.id = 1 AND p.party_id = s.operator_party_id
 WHERE p.status = 'active'
   AND EXISTS (
     SELECT 1
       FROM public.grants g
       JOIN public.authz_operations o ON o.id = g.operation_id
      WHERE g.actor_id = p.entity_id AND o.slug = $1 AND g.target_id IS NULL AND g.scope = 'entity')`

// lockPlatformSettingsForEndSQL serializes endings of operator administrators:
// each ending takes this row before counting the remaining administrators, so
// of two concurrent endings the second sees the first's committed result.
const lockPlatformSettingsForEndSQL = `SELECT 1 FROM public.platform_settings WHERE id = 1 FOR UPDATE`

// requireNotLastOperatorAdmin refuses (ErrLastOperatorAdmin) to end the locked
// profile p when it is an active operator administrator and no other active
// operator administrator exists. It runs in the ending's transaction. Profiles
// that are not active administrators of the operator party pass untouched.
func requireNotLastOperatorAdmin(ctx context.Context, tx pgx.Tx, p coredb.Profile) error {
	if p.Status != ProfileStatusActive || !p.PartyID.Valid {
		return nil
	}
	var isAdmin bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (`+operatorAdminProfileSQL+` AND p.entity_id = $2)`,
		manageOperationSlug, p.EntityID).Scan(&isAdmin); err != nil {
		return fmt.Errorf("users: check operator administrator: %w", err)
	}
	if !isAdmin {
		return nil
	}
	if _, err := tx.Exec(ctx, lockPlatformSettingsForEndSQL); err != nil {
		return fmt.Errorf("users: lock platform settings: %w", err)
	}
	var others bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (`+operatorAdminProfileSQL+` AND p.entity_id <> $2)`,
		manageOperationSlug, p.EntityID).Scan(&others); err != nil {
		return fmt.Errorf("users: count operator administrators: %w", err)
	}
	if !others {
		return ErrLastOperatorAdmin
	}
	return nil
}
