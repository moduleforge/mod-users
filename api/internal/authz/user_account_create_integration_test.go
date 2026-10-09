//go:build integration

package authz_test

// user_account_create_integration_test.go wires the real UserAccountService
// to the real grants-table Authorizer and proves, at the service layer, that
// Create is authorized by a wildcard grant or by a grant on natural_person's
// type entity (types.entity_id), and by nothing else: a create grant on an
// instance entity does not authorize it. The transaction is replaced by a stub
// that fails on BeginTx, so the test observes whether Create got past
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
	return newIntegCreateServiceFor(d, typeIDForSlug(t, "natural_person"))
}

// newIntegCreateServiceFor builds the service with its type resolver mapping
// natural_person to typeID, whatever that id is. The service passes the
// resolved id straight to AuthorizeType.
func newIntegCreateServiceFor(d *integBeginTxDB, typeID int64) *service.UserAccountService {
	typeRes := types.NewFromMap(map[string]int64{"natural_person": typeID})
	return service.NewUserAccountService(d, nil, nil, integAZ, &observer.ObserverGroup{}, nil, typeRes, nil)
}

func integCreateInput() service.CreateUserAccountInput {
	return service.CreateUserAccountInput{Email: "typetarget-create@example.com", GivenName: "T", FamilyName: "T"}
}

// TestInteg_UserAccountService_Create_TypeEntityGrantAllowed: a create grant on
// natural_person's type entity (resolved through types.entity_id, never assumed
// equal to the type id) lets Create reach the transaction.
func TestInteg_UserAccountService_Create_TypeEntityGrantAllowed(t *testing.T) {
	typeEntityID := typeEntityIDForSlug(t, "natural_person")
	userU := seedUser(t, "typetarget-svc-typegrant@example.com", false)
	targetedGrant(t, userU, typeEntityID, "create")

	d := &integBeginTxDB{}
	_, err := newIntegCreateService(t, d).Create(actorCtx(userU), integCreateInput())

	if !errors.Is(err, errIntegBeginTx) {
		t.Errorf("Create as natural_person type-entity (%d) create-grant holder: got %v, want the transaction sentinel (authorized)", typeEntityID, err)
	}
	if d.calls != 1 {
		t.Errorf("BeginTx calls: got %d, want 1", d.calls)
	}
}

// TestInteg_UserAccountService_Create_InstanceEntityGrantDenied: a create grant
// on an instance entity does not authorize Create. The instance is a freshly
// seeded user's entity, whose id is not natural_person's type entity.
func TestInteg_UserAccountService_Create_InstanceEntityGrantDenied(t *testing.T) {
	typeEntityID := typeEntityIDForSlug(t, "natural_person")
	userU := seedUser(t, "typetarget-svc-instgrant-u@example.com", false)
	instance := seedUser(t, "typetarget-svc-instgrant-i@example.com", false)
	if instance == typeEntityID {
		t.Fatalf("precondition: instance entity %d must differ from natural_person's type entity", instance)
	}
	targetedGrant(t, userU, instance, "create")

	// Characterization: the grant is real entity-level authority over the
	// instance, which is exactly why it must not answer the type-level check.
	ctxU := actorCtx(userU)
	if err := integAZ.Authorize(ctxU, "create", &instance); err != nil {
		t.Fatalf("precondition: Authorize(create, &%d): got %v, want nil", instance, err)
	}

	d := &integBeginTxDB{}
	_, err := newIntegCreateService(t, d).Create(ctxU, integCreateInput())

	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("Create as instance-entity (%d) create-grant holder: got %v, want ErrForbidden", instance, err)
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

// TestInteg_UserAccountService_Create_Ilu6_TestOnlyType is the end-to-end form
// of the ilu6 regression. The resolver maps natural_person to a test-only
// type's id, and an instance entity sits at exactly that id. A create grant on
// the instance is entity-level authority and is denied before BeginTx; a create
// grant on the test type's own type entity reaches the transaction stub.
func TestInteg_UserAccountService_Create_Ilu6_TestOnlyType(t *testing.T) {
	testTypeID, testTypeEntity := registerTestType(t, "svc-ilu6")
	seedEntityWithExplicitID(t, testTypeID, nil)

	instanceHolder := seedUser(t, "typegrant-svc-ilu6-instance@example.com", false)
	targetedGrant(t, instanceHolder, testTypeID, "create")
	// Characterization: the grant is real entity-level authority.
	if err := integAZ.Authorize(actorCtx(instanceHolder), "create", &testTypeID); err != nil {
		t.Fatalf("precondition: Authorize(create, &%d): got %v, want nil", testTypeID, err)
	}

	d := &integBeginTxDB{}
	_, err := newIntegCreateServiceFor(d, testTypeID).Create(actorCtx(instanceHolder), integCreateInput())
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("Create with a create grant on the instance at id %d: got %v, want ErrForbidden", testTypeID, err)
	}
	if d.calls != 0 {
		t.Error("transaction was started despite the authorization denial")
	}

	typeHolder := seedUser(t, "typegrant-svc-ilu6-type@example.com", false)
	targetedGrant(t, typeHolder, testTypeEntity, "create")
	d = &integBeginTxDB{}
	_, err = newIntegCreateServiceFor(d, testTypeID).Create(actorCtx(typeHolder), integCreateInput())
	if !errors.Is(err, errIntegBeginTx) {
		t.Errorf("Create with a create grant on the test type's entity %d: got %v, want the transaction sentinel (authorized)", testTypeEntity, err)
	}
	if d.calls != 1 {
		t.Errorf("BeginTx calls: got %d, want 1", d.calls)
	}
}
