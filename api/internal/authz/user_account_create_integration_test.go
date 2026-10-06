//go:build integration

package authz_test

// user_account_create_integration_test.go wires the real UserAccountService
// to the real grants-table Authorizer and proves the type-target bypass is
// closed at the service layer: an actor with entity-level authority over
// entity T, where T is the natural_person types.id, is denied Create, while a
// wildcard manage holder is authorized. The transaction is replaced by a
// stub that fails on BeginTx, so the test observes whether Create got past
// authorization without writing any rows.
//
// Run with the throwaway-Postgres recipe in authz_integration_test.go's header.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/types"
	"github.com/moduleforge/mod-users/api/internal/authz"
	"github.com/moduleforge/mod-users/api/internal/service"
)

var errIntegBeginTx = errors.New("integ: transaction reached")

type integBeginTxDB struct{ calls int }

func (d *integBeginTxDB) BeginTx(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	d.calls++
	return nil, errIntegBeginTx
}

func newIntegCreateService(t *testing.T, d *integBeginTxDB) *service.UserAccountService {
	t.Helper()
	typeRes := types.NewFromMap(map[string]int64{"natural_person": typeIDForSlug(t, "natural_person")})
	return service.NewUserAccountService(d, nil, nil, integAZ, &observer.ObserverGroup{}, nil, typeRes, nil)
}

func integCreateInput() service.CreateUserAccountInput {
	return service.CreateUserAccountInput{Email: "typetarget-create@example.com", GivenName: "T", FamilyName: "T"}
}

func TestInteg_UserAccountService_Create_EntityAuthorityOverTypeIDDenied(t *testing.T) {
	typeID := typeIDForSlug(t, "natural_person")
	userU := seedUser(t, "typetarget-svc-u@example.com", false)
	arm := grantEntityLevelAuthorityOver(t, userU, typeID)
	t.Logf("natural_person types.id=%d; entity-level authority over entity %d via %s", typeID, typeID, arm)

	// Characterization: the pre-fix call (entity-level Authorize with the type
	// id) would have allowed this actor.
	ctxU := actorCtx(userU)
	if err := integAZ.Authorize(ctxU, "create", &typeID); err != nil {
		t.Fatalf("precondition: Authorize(create, &%d): got %v, want nil", typeID, err)
	}

	d := &integBeginTxDB{}
	_, err := newIntegCreateService(t, d).Create(ctxU, integCreateInput())

	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("Create as entity-%d authority holder: got %v, want ErrForbidden", typeID, err)
	}
	if d.calls != 0 {
		t.Error("transaction was started despite the authorization denial")
	}
}

func TestInteg_UserAccountService_Create_WildcardManageAllowed(t *testing.T) {
	manager := seedUser(t, "typetarget-svc-manager@example.com", true)

	d := &integBeginTxDB{}
	_, err := newIntegCreateService(t, d).Create(actorCtx(manager), integCreateInput())

	if !errors.Is(err, errIntegBeginTx) {
		t.Errorf("Create as wildcard manage holder: got %v, want the transaction sentinel (authorized)", err)
	}
	if d.calls != 1 {
		t.Errorf("BeginTx calls: got %d, want 1", d.calls)
	}
}
