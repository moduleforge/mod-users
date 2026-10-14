//go:build integration

package authz_test

// user_account_update_conflict_integration_test.go proves against a real
// Postgres that UserAccountService.Update maps a unique violation on
// user_accounts.email to the 409 users.email_taken conflict (the constraint
// name user_accounts_email_key is matched exactly), and that a non-conflicting
// update still succeeds.
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/types"
	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/mod-users/api/internal/service"
	db "github.com/moduleforge/mod-users/model/db"
)

func newIntegUpdateService(t *testing.T) *service.UserAccountService {
	t.Helper()
	typeRes := types.NewFromMap(map[string]int64{"natural_person": typeIDForSlug(t, "natural_person")})
	return service.NewUserAccountService(integPool, db.New(integPool), coredb.New(integPool), integAZ,
		&observer.ObserverGroup{}, nil, typeRes, nil)
}

func integAccountUUID(t *testing.T, entityID int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := integPool.QueryRow(context.Background(),
		`SELECT uuid FROM user_accounts WHERE account_holder = $1`, entityID).Scan(&id); err != nil {
		t.Fatalf("lookup user_account uuid: %v", err)
	}
	return id
}

func TestInteg_UserAccountService_Update_DuplicateEmailIsConflict(t *testing.T) {
	adminID := seedUser(t, "conflict-update-admin@example.com", true)
	_ = seedUser(t, "conflict-update-taken@example.com", false)
	victimID := seedUser(t, "conflict-update-victim@example.com", false)
	svc := newIntegUpdateService(t)
	ctx := actorCtx(adminID)
	victim := integAccountUUID(t, victimID)

	taken := "conflict-update-taken@example.com"
	_, err := svc.Update(ctx, victim, service.UpdateUserAccountInput{Email: &taken})
	if err != service.ErrEmailTaken {
		t.Fatalf("duplicate email: got %v, want service.ErrEmailTaken", err)
	}

	// The failed update left the account untouched, and a fresh address works.
	free := "conflict-update-free@example.com"
	out, err := svc.Update(ctx, victim, service.UpdateUserAccountInput{Email: &free})
	if err != nil {
		t.Fatalf("update to a free email: %v", err)
	}
	if out.Email == nil || *out.Email != free {
		t.Errorf("email = %v, want %q", out.Email, free)
	}
}

// The email constraint is named exactly; this guards the name the service
// matches against the schema it actually runs on.
func TestInteg_UserAccountsEmailConstraintName(t *testing.T) {
	seedUser(t, "conflict-constraint-name@example.com", false)
	holder := seedUser(t, "conflict-constraint-other@example.com", false)
	_, err := integPool.Exec(context.Background(),
		`UPDATE user_accounts SET email = $1 WHERE account_holder = $2`,
		"conflict-constraint-name@example.com", holder)
	pgErr, ok := err.(*pgconn.PgError)
	if !ok || pgErr.Code != "23505" || pgErr.ConstraintName != "user_accounts_email_key" {
		t.Fatalf("got %v, want 23505 on user_accounts_email_key", err)
	}
}
