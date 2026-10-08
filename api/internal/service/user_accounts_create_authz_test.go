package service

// Regression tests for the type-level authorization of
// UserAccountService.Create. The natural_person types.id must never reach
// Authorizer.Authorize as a target (Authorize treats its target as an
// entities.id, so an actor holding authority over the entity whose id equals
// the type id would be authorized to create accounts). Create must route the
// check through AuthorizeType, or fall back to a nil target.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/types"

	"github.com/moduleforge/mod-users/api/localAuthz"
)

const testNaturalPersonTypeID int64 = 4242

// errBeginTx is returned by beginTxDB so a test can tell that Create got past
// authorization and reached the transaction, without needing a database.
var errBeginTx = errors.New("beginTxDB: transaction reached")

type beginTxDB struct{ calls int }

func (d *beginTxDB) BeginTx(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
	d.calls++
	return nil, errBeginTx
}

// authzCall is one recorded Authorize call.
type authzCall struct {
	op     string
	target *int64
}

// entityOwnerAuthorizer implements both Authorize and AuthorizeType. Authorize
// simulates an actor holding authority over entity typeID: it allows any
// non-nil target. A nil target is denied (wildcard-only). AuthorizeType
// returns typeErr.
type entityOwnerAuthorizer struct {
	typeErr   error
	calls     []authzCall
	typeCalls []authzCall
	typeIDs   []int64
}

func (a *entityOwnerAuthorizer) Authorize(_ context.Context, op string, target *int64) error {
	a.calls = append(a.calls, authzCall{op, target})
	if target != nil {
		return nil
	}
	return apiresp.ErrForbidden
}

func (a *entityOwnerAuthorizer) AuthorizeType(_ context.Context, op string, typeID int64) error {
	a.typeCalls = append(a.typeCalls, authzCall{op: op})
	a.typeIDs = append(a.typeIDs, typeID)
	return a.typeErr
}

var _ coreAuthz.Authorizer = (*entityOwnerAuthorizer)(nil)

// plainAuthorizer implements only coreAuthz.Authorizer (a decorator or test
// stub that does not expose AuthorizeType). It records every call and returns
// decision.
type plainAuthorizer struct {
	decision error
	calls    []authzCall
}

func (a *plainAuthorizer) Authorize(_ context.Context, op string, target *int64) error {
	a.calls = append(a.calls, authzCall{op, target})
	return a.decision
}

var _ coreAuthz.Authorizer = (*plainAuthorizer)(nil)

func newCreateAuthzTestService(az coreAuthz.Authorizer, d *beginTxDB) *UserAccountService {
	return &UserAccountService{
		db:      d,
		az:      az,
		obs:     &observer.ObserverGroup{},
		typeRes: types.NewFromMap(map[string]int64{"natural_person": testNaturalPersonTypeID}),
	}
}

func validCreateInput() CreateUserAccountInput {
	return CreateUserAccountInput{Email: "a@example.com", GivenName: "A", FamilyName: "B"}
}

// (a) An actor with entity-level authority over entity typeID must not be
// able to create: Create has to ask AuthorizeType, which denies.
func TestUserAccountService_Create_TypeLevelDenialNotBypassedByEntityAuthority(t *testing.T) {
	t.Parallel()

	az := &entityOwnerAuthorizer{typeErr: apiresp.ErrForbidden}
	d := &beginTxDB{}
	svc := newCreateAuthzTestService(az, d)

	_, err := svc.Create(context.Background(), validCreateInput())

	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("Create error: got %v, want ErrForbidden", err)
	}
	for _, c := range az.calls {
		if c.target != nil {
			t.Errorf("Authorize(%q) was called with non-nil target %d; a type id must never be passed as an entity target", c.op, *c.target)
		}
	}
	if len(az.typeCalls) != 1 || az.typeCalls[0].op != "create" || az.typeIDs[0] != testNaturalPersonTypeID {
		t.Errorf("AuthorizeType calls: got ops=%v ids=%v, want exactly one create for type id %d", az.typeCalls, az.typeIDs, testNaturalPersonTypeID)
	}
	if d.calls != 0 {
		t.Error("transaction was started despite the authorization denial")
	}
}

// (b) An authorizer without AuthorizeType must be asked with a nil target,
// never &typeID.
func TestUserAccountService_Create_PlainAuthorizerGetsNilTarget(t *testing.T) {
	t.Parallel()

	az := &plainAuthorizer{decision: apiresp.ErrForbidden}
	d := &beginTxDB{}
	svc := newCreateAuthzTestService(az, d)

	_, err := svc.Create(context.Background(), validCreateInput())

	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("Create error: got %v, want ErrForbidden", err)
	}
	if len(az.calls) != 1 {
		t.Fatalf("Authorize calls: got %d, want 1", len(az.calls))
	}
	if az.calls[0].op != "create" {
		t.Errorf("Authorize op: got %q, want create", az.calls[0].op)
	}
	if az.calls[0].target != nil {
		t.Errorf("Authorize target: got %d, want nil; a type id must never be passed as an entity target", *az.calls[0].target)
	}
	if d.calls != 0 {
		t.Error("transaction was started despite the authorization denial")
	}
}

// (c) AuthorizeType allowing lets Create proceed to the transaction, and the
// entity-level Authorize is never consulted.
func TestUserAccountService_Create_TypeAuthorizerAllowProceedsToTransaction(t *testing.T) {
	t.Parallel()

	az := &entityOwnerAuthorizer{}
	d := &beginTxDB{}
	svc := newCreateAuthzTestService(az, d)

	_, err := svc.Create(context.Background(), validCreateInput())

	if !errors.Is(err, errBeginTx) {
		t.Errorf("Create error: got %v, want the transaction sentinel (authorization should have passed)", err)
	}
	if d.calls != 1 {
		t.Errorf("BeginTx calls: got %d, want 1", d.calls)
	}
	if len(az.calls) != 0 {
		t.Errorf("entity-level Authorize was called %d time(s); the type-level path must not use it", len(az.calls))
	}
}

// Input validation still runs before authorization.
func TestUserAccountService_Create_ValidatesInputBeforeAuthorizing(t *testing.T) {
	t.Parallel()

	az := &entityOwnerAuthorizer{typeErr: apiresp.ErrForbidden}
	svc := newCreateAuthzTestService(az, &beginTxDB{})

	_, err := svc.Create(context.Background(), CreateUserAccountInput{})

	if errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("Create error: got ErrForbidden, want an input-validation error")
	}
	if len(az.typeCalls) != 0 || len(az.calls) != 0 {
		t.Error("authorizer consulted before input validation")
	}
}

// The production Authorizer must satisfy the structural interface authorizeType
// asserts. If its AuthorizeType signature drifts, authorizeType would silently
// fall back to the nil target and deny every non-wildcard caller; this
// compile-time check makes that drift a build failure instead.
var _ typeAuthorizer = (*localAuthz.Authorizer)(nil)

// The nil-target fallback must fail closed on a non-positive type id, like the
// TypeAuthorizer path, without consulting the authorizer at all.
func TestAuthorizeType_PlainAuthorizerNonPositiveTypeIDFailsClosed(t *testing.T) {
	t.Parallel()

	for _, typeID := range []int64{0, -1} {
		az := &plainAuthorizer{decision: nil} // would allow if consulted
		err := authorizeType(context.Background(), az, "create", typeID)
		if !errors.Is(err, apiresp.ErrForbidden) {
			t.Errorf("typeID %d: got %v, want ErrForbidden", typeID, err)
		}
		if len(az.calls) != 0 {
			t.Errorf("typeID %d: Authorize called %d times, want 0", typeID, len(az.calls))
		}
	}
}
