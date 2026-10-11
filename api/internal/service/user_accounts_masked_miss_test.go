package service

// Tests for the masked user-account miss (authorizeUserAccount): a missing
// UUID and an existing-but-forbidden UUID are indistinguishable, and a miss
// makes exactly one target-0 Authorize call.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	db "github.com/moduleforge/mod-users/model/db"
)

// assumeQuerier overrides GetUserAccountByAccountHolder on stubUAQuerier and
// counts calls, so tests can prove the admin lookup runs after authorization.
type assumeQuerier struct {
	*stubUAQuerier
	admin       db.UserAccount
	holderCalls int
}

func (q *assumeQuerier) GetUserAccountByAccountHolder(_ context.Context, _ int64) (db.UserAccount, error) {
	q.holderCalls++
	return q.admin, nil
}

type maskedMethod struct {
	name string
	op   string
	call func(svc *UserAccountService, ctx context.Context, id uuid.UUID) error
}

func maskedMethods() []maskedMethod {
	return []maskedMethod{
		{"Get", "read", func(s *UserAccountService, ctx context.Context, id uuid.UUID) error {
			_, err := s.Get(ctx, id)
			return err
		}},
		{"Update", "update", func(s *UserAccountService, ctx context.Context, id uuid.UUID) error {
			_, err := s.Update(ctx, id, UpdateUserAccountInput{})
			return err
		}},
		{"Delete", "delete", func(s *UserAccountService, ctx context.Context, id uuid.UUID) error {
			return s.Delete(ctx, id)
		}},
		{"Assume", "assume", func(s *UserAccountService, ctx context.Context, id uuid.UUID) error {
			_, _, err := s.Assume(ctx, id)
			return err
		}},
	}
}

func newMaskedService(q db.Querier, az *recordingAuthorizer) *UserAccountService {
	return &UserAccountService{db: &fakeDB{tx: &fakeTx{}}, q: q, az: az, obs: &observer.ObserverGroup{}}
}

func TestUserAccountService_MissAndForbiddenAreIndistinguishable(t *testing.T) {
	t.Parallel()

	existing := db.UserAccount{ID: 7, Uuid: uuid.New(), AccountHolder: 700}

	for _, m := range maskedMethods() {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			q := &assumeQuerier{stubUAQuerier: newStubUAQuerier(existing)}
			az := &recordingAuthorizer{decision: apiresp.ErrForbidden}
			svc := newMaskedService(q, az)

			missErr := m.call(svc, authedCtx(), uuid.New())
			if missErr != apiresp.ErrForbidden {
				t.Fatalf("miss: got %v, want apiresp.ErrForbidden", missErr)
			}
			if az.calls != 1 || az.targets[0] != 0 || az.lastOp != m.op {
				t.Errorf("miss: Authorize calls=%d targets=%v op=%q, want one target-0 call for %q", az.calls, az.targets, az.lastOp, m.op)
			}

			*az = recordingAuthorizer{decision: apiresp.ErrForbidden}
			forbiddenErr := m.call(svc, authedCtx(), existing.Uuid)
			if forbiddenErr != apiresp.ErrForbidden {
				t.Fatalf("forbidden: got %v, want apiresp.ErrForbidden", forbiddenErr)
			}
			if az.calls != 1 || az.targets[0] != existing.AccountHolder {
				t.Errorf("forbidden: Authorize calls=%d targets=%v, want one call on the account holder", az.calls, az.targets)
			}
			if q.holderCalls != 0 {
				t.Errorf("admin lookup ran %d times before/without authorization", q.holderCalls)
			}
		})
	}
}

func TestUserAccountService_NoActorReturnsUnauthenticated(t *testing.T) {
	t.Parallel()

	for _, m := range maskedMethods() {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			az := &recordingAuthorizer{}
			svc := newMaskedService(newStubUAQuerier(), az)

			err := m.call(svc, context.Background(), uuid.New())
			if !errors.Is(err, apiresp.ErrUnauthenticated) {
				t.Errorf("got %v, want apiresp.ErrUnauthenticated", err)
			}
			if az.called {
				t.Error("Authorize must not run without an actor")
			}
		})
	}
}

func TestUserAccountService_NilAuthorizerFailsClosed(t *testing.T) {
	t.Parallel()

	svc := &UserAccountService{q: newStubUAQuerier(), obs: &observer.ObserverGroup{}}
	_, err := svc.Get(authedCtx(), uuid.New())
	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("got %v, want apiresp.ErrForbidden", err)
	}
}

func TestUserAccountService_OtherLookupErrorsAreWrappedNotMasked(t *testing.T) {
	t.Parallel()

	dbErr := errors.New("connection reset")
	az := &recordingAuthorizer{}
	svc := newMaskedService(&failingLookupQuerier{stubUAQuerier: newStubUAQuerier(), err: dbErr}, az)

	_, err := svc.Get(authedCtx(), uuid.New())
	if !errors.Is(err, dbErr) || errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("got %v, want a wrapped DB error that is not ErrForbidden", err)
	}
	if az.called {
		t.Error("Authorize must not run on a non-miss lookup error")
	}
}

type failingLookupQuerier struct {
	*stubUAQuerier
	err error
}

func (q *failingLookupQuerier) GetUserAccountByUUID(_ context.Context, _ uuid.UUID) (db.UserAccount, error) {
	return db.UserAccount{}, q.err
}

// TestUserAccountService_Assume_SudoOnlyIdentityIsAuthenticated proves Assume
// uses the effective actor: a sudo-only identity is not rejected as 401.
func TestUserAccountService_Assume_SudoOnlyIdentityIsAuthenticated(t *testing.T) {
	t.Parallel()

	target := db.UserAccount{ID: 7, Uuid: uuid.New(), AccountHolder: 700}
	admin := db.UserAccount{ID: 1, Uuid: uuid.New(), AccountHolder: 100}
	q := &assumeQuerier{stubUAQuerier: newStubUAQuerier(target), admin: admin}
	az := &recordingAuthorizer{}
	svc := newMaskedService(q, az)

	ctx := opctx.WithSudoActor(context.Background(), 100)
	sudoUA, actorUA, err := svc.Assume(ctx, target.Uuid)
	if err != nil {
		t.Fatalf("Assume: %v", err)
	}
	if sudoUA.ID != admin.ID || actorUA.ID != target.ID {
		t.Errorf("got sudo=%d actor=%d, want %d/%d", sudoUA.ID, actorUA.ID, admin.ID, target.ID)
	}
	if q.holderCalls != 1 {
		t.Errorf("admin lookup calls: got %d, want 1", q.holderCalls)
	}
}
