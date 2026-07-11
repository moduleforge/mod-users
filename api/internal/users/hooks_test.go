package users

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	authzservice "github.com/moduleforge/authz-api/service"
	"github.com/moduleforge/core-api/opctx"
)

// fakeGrantServicer is a test double for authzservice.GrantServicer that
// captures the context it receives on CreateWildcardGrant, so tests can
// assert on the actor the caller attached before invoking it.
type fakeGrantServicer struct {
	capturedCtx  context.Context
	capturedUUID uuid.UUID
	capturedOp   string
	returnErr    error
}

var _ authzservice.GrantServicer = (*fakeGrantServicer)(nil)

func (f *fakeGrantServicer) Create(ctx context.Context, in authzservice.CreateGrantInput) (authzservice.Grant, error) {
	return authzservice.Grant{}, errors.New("not implemented")
}

func (f *fakeGrantServicer) List(ctx context.Context, in authzservice.ListGrantsInput) ([]authzservice.Grant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeGrantServicer) Get(ctx context.Context, id int64) (authzservice.Grant, error) {
	return authzservice.Grant{}, errors.New("not implemented")
}

func (f *fakeGrantServicer) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

func (f *fakeGrantServicer) CreateWildcardGrant(ctx context.Context, actorUUID uuid.UUID, operationSlug string) (authzservice.Grant, error) {
	f.capturedCtx = ctx
	f.capturedUUID = actorUUID
	f.capturedOp = operationSlug
	if f.returnErr != nil {
		return authzservice.Grant{}, f.returnErr
	}
	return authzservice.Grant{ActorUUID: actorUUID, OperationSlug: operationSlug}, nil
}

func (f *fakeGrantServicer) DeleteWildcardGrant(ctx context.Context, actorUUID uuid.UUID, operationSlug string) error {
	return errors.New("not implemented")
}

// TestGrantFirstUserWildcard_SetsActorBeforeCreatingGrant confirms that the
// context passed to CreateWildcardGrant carries the newly created entity's
// own ID as the actor — reproducing the exact requirement CreateWildcardGrant
// enforces via its audit-observer write (opctx.ActorEntityID must be set,
// otherwise it hard-fails with "audit: no actor on context"). Before the fix,
// hookCtx passed through unmodified — an unauthenticated signup request
// context with no actor — and this assertion would fail.
func TestGrantFirstUserWildcard_SetsActorBeforeCreatingGrant(t *testing.T) {
	fake := &fakeGrantServicer{}
	entityID := int64(42)
	actorUUID := uuid.New()

	// hookCtx simulates the raw, unauthenticated POST /v1/auth/register
	// request context: no actor set, mirroring production.
	hookCtx := context.Background()
	if _, ok := opctx.ActorEntityID(hookCtx); ok {
		t.Fatalf("test setup invariant broken: hookCtx already carries an actor")
	}

	if err := grantFirstUserWildcard(hookCtx, fake, actorUUID, entityID); err != nil {
		t.Fatalf("grantFirstUserWildcard returned unexpected error: %v", err)
	}

	if fake.capturedCtx == nil {
		t.Fatalf("CreateWildcardGrant was never called")
	}
	gotID, ok := opctx.ActorEntityID(fake.capturedCtx)
	if !ok {
		t.Fatalf("opctx.ActorEntityID(capturedCtx) = (_, false), want (%d, true)", entityID)
	}
	if gotID != entityID {
		t.Fatalf("opctx.ActorEntityID(capturedCtx) = (%d, true), want (%d, true)", gotID, entityID)
	}
	if fake.capturedUUID != actorUUID {
		t.Fatalf("CreateWildcardGrant actorUUID = %s, want %s", fake.capturedUUID, actorUUID)
	}
	if fake.capturedOp != "manage" {
		t.Fatalf("CreateWildcardGrant operationSlug = %q, want %q", fake.capturedOp, "manage")
	}
}

// TestGrantFirstUserWildcard_PropagatesGrantServiceError confirms an error
// from CreateWildcardGrant (e.g. an already-existing wildcard grant) is
// wrapped and returned, not swallowed.
func TestGrantFirstUserWildcard_PropagatesGrantServiceError(t *testing.T) {
	wantErr := errors.New("boom")
	fake := &fakeGrantServicer{returnErr: wantErr}

	err := grantFirstUserWildcard(context.Background(), fake, uuid.New(), 7)
	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("grantFirstUserWildcard error = %v, want it to wrap %v", err, wantErr)
	}
}
