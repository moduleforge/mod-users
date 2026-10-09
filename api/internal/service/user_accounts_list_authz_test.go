package service

// Tests for the type-level authorization of UserAccountService.List. List
// requires "list" on natural_person's type through authorizeType: the
// natural_person types.id must never reach Authorizer.Authorize as a target,
// and authorization must happen before SearchUserAccounts runs.

import (
	"context"
	"errors"
	"testing"

	"github.com/moduleforge/core-api/apiresp"
	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/types"

	db "github.com/moduleforge/mod-users/model/db"
)

// searchRecorderQuerier embeds db.Querier and overrides only
// SearchUserAccounts, recording each call. Any other Querier method panics on
// the nil embedded interface, which would surface an unintended query.
type searchRecorderQuerier struct {
	db.Querier
	calls int
	rows  []db.UserAccount
}

func (q *searchRecorderQuerier) SearchUserAccounts(_ context.Context, _ db.SearchUserAccountsParams) ([]db.UserAccount, error) {
	q.calls++
	return q.rows, nil
}

func newListAuthzTestService(az coreAuthz.Authorizer, q db.Querier) *UserAccountService {
	return &UserAccountService{
		q:       q,
		az:      az,
		obs:     &observer.ObserverGroup{},
		typeRes: types.NewFromMap(map[string]int64{"natural_person": testNaturalPersonTypeID}),
	}
}

func listStubRows() []db.UserAccount {
	return []db.UserAccount{
		{ID: 1, AccountHolder: 11},
		{ID: 2, AccountHolder: 12},
	}
}

func requireNoNonNilTarget(t *testing.T, calls []authzCall) {
	t.Helper()
	for _, c := range calls {
		if c.target != nil {
			t.Errorf("Authorize(%q) was called with non-nil target %d; a type id must never be passed as an entity target", c.op, *c.target)
		}
	}
}

func TestUserAccountService_List_TypeAuthorizerAllowReturnsRows(t *testing.T) {
	t.Parallel()

	az := &entityOwnerAuthorizer{}
	q := &searchRecorderQuerier{rows: listStubRows()}
	got, err := newListAuthzTestService(az, q).List(context.Background(), ListUserAccountsInput{})

	if err != nil {
		t.Fatalf("List error: got %v, want nil", err)
	}
	if len(got) != len(q.rows) {
		t.Errorf("List rows: got %d, want %d", len(got), len(q.rows))
	}
	if len(az.typeCalls) != 1 || az.typeCalls[0].op != "list" || az.typeIDs[0] != testNaturalPersonTypeID {
		t.Errorf("AuthorizeType calls: got ops=%v ids=%v, want exactly one list for type id %d", az.typeCalls, az.typeIDs, testNaturalPersonTypeID)
	}
	requireNoNonNilTarget(t, az.calls)
	if q.calls != 1 {
		t.Errorf("SearchUserAccounts calls: got %d, want 1", q.calls)
	}
}

func TestUserAccountService_List_TypeAuthorizerDenialPropagatesWithoutQuery(t *testing.T) {
	t.Parallel()

	az := &entityOwnerAuthorizer{typeErr: apiresp.ErrForbidden}
	q := &searchRecorderQuerier{rows: listStubRows()}
	got, err := newListAuthzTestService(az, q).List(context.Background(), ListUserAccountsInput{})

	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("List error: got %v, want ErrForbidden", err)
	}
	if len(got) != 0 {
		t.Errorf("List leaked %d rows on denial", len(got))
	}
	requireNoNonNilTarget(t, az.calls)
	if q.calls != 0 {
		t.Errorf("SearchUserAccounts was called %d time(s) despite the denial", q.calls)
	}
}

func TestUserAccountService_List_PlainAuthorizerFallsBackToNilTarget(t *testing.T) {
	t.Parallel()

	t.Run("allow", func(t *testing.T) {
		t.Parallel()
		az := &plainAuthorizer{}
		q := &searchRecorderQuerier{rows: listStubRows()}
		got, err := newListAuthzTestService(az, q).List(context.Background(), ListUserAccountsInput{})
		if err != nil {
			t.Fatalf("List error: got %v, want nil", err)
		}
		if len(got) != len(q.rows) {
			t.Errorf("List rows: got %d, want %d", len(got), len(q.rows))
		}
		if len(az.calls) != 1 || az.calls[0].op != "list" || az.calls[0].target != nil {
			t.Errorf("Authorize calls: got %+v, want exactly one list with a nil target", az.calls)
		}
	})

	t.Run("deny", func(t *testing.T) {
		t.Parallel()
		az := &plainAuthorizer{decision: apiresp.ErrForbidden}
		q := &searchRecorderQuerier{rows: listStubRows()}
		got, err := newListAuthzTestService(az, q).List(context.Background(), ListUserAccountsInput{})
		if !errors.Is(err, apiresp.ErrForbidden) {
			t.Errorf("List error: got %v, want ErrForbidden", err)
		}
		if len(got) != 0 {
			t.Errorf("List leaked %d rows on denial", len(got))
		}
		if len(az.calls) != 1 || az.calls[0].op != "list" || az.calls[0].target != nil {
			t.Errorf("Authorize calls: got %+v, want exactly one list with a nil target", az.calls)
		}
		if q.calls != 0 {
			t.Errorf("SearchUserAccounts was called %d time(s) despite the denial", q.calls)
		}
	})
}

// entityAuthorityOnlyAuthorizer implements only Authorize: it allows every
// non-nil target (an actor with authority over any entity) and denies a nil
// target. List must still be denied through the nil-target fallback.
type entityAuthorityOnlyAuthorizer struct{ calls []authzCall }

func (a *entityAuthorityOnlyAuthorizer) Authorize(_ context.Context, op string, target *int64) error {
	a.calls = append(a.calls, authzCall{op, target})
	if target != nil {
		return nil
	}
	return apiresp.ErrForbidden
}

func TestUserAccountService_List_EntityAuthorityDoesNotBypassFallback(t *testing.T) {
	t.Parallel()

	az := &entityAuthorityOnlyAuthorizer{}
	q := &searchRecorderQuerier{rows: listStubRows()}
	got, err := newListAuthzTestService(az, q).List(context.Background(), ListUserAccountsInput{})

	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("List error: got %v, want ErrForbidden", err)
	}
	if len(got) != 0 {
		t.Errorf("List leaked %d rows on denial", len(got))
	}
	requireNoNonNilTarget(t, az.calls)
	if len(az.calls) != 1 {
		t.Errorf("Authorize calls: got %d, want 1 (the nil-target fallback)", len(az.calls))
	}
	if q.calls != 0 {
		t.Errorf("SearchUserAccounts was called %d time(s) despite the denial", q.calls)
	}
}
