package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/opctx"
)

// -----------------------------------------------------------------------
// Compile-time shape assertions for the anonymous-actor facade surface.
//
// task 002's AnonymousActor holds its entity id in an unexported field
// (api/internal/auth/anonymous_actor.go), and its only DB-backed constructor
// is NewAnonymousActor(ctx, *pgxpool.Pool), which needs a live Postgres
// connection. That leaves no dependency-free seam in this package for
// constructing a *AnonymousActor with a chosen entity id, so
// NewResolveActorOrAnonymous's anon.EntityID()-unwrap step cannot be
// exercised behaviorally from here without either a live pool or inventing a
// test seam inside internal/auth — which the task instructs against. These
// var declarations instead pin every new export's exact signature at compile
// time, so a signature drift (e.g. an accidental argument reorder) fails the
// build rather than passing silently.
//
// The behavior NewResolveActorOrAnonymous delegates to *is* exercised
// end-to-end below, via the plain ResolveActorOrAnonymous re-export, which
// takes the same bare int64 entity id inner.ResolveActorOrAnonymous does and
// requires no pool. NewResolveActorOrAnonymous's own body is a single
// pass-through line (anon.EntityID() then inner.ResolveActorOrAnonymous —
// see auth.go), so this plus task 004's exhaustive suite in
// api/internal/auth/anonymous_actor_test.go covers the delegation this
// facade adds.
var (
	_ AnonymousActor = AnonymousActor{}

	_ func(context.Context, *pgxpool.Pool) (*AnonymousActor, error) = NewAnonymousActor

	_ func(*Verifier, ClaimMapper, *UserResolver, *AnonymousActor) func(http.Handler) http.Handler = NewResolveActorOrAnonymous

	_ func(*Verifier, ClaimMapper, *UserResolver, int64) func(http.Handler) http.Handler = ResolveActorOrAnonymous

	_ func(context.Context) bool = IsAnonymousActor
)

// anonFacadeTestEntityID stands in for the entity id NewAnonymousActor
// resolves at boot. Deliberately distinct from any account/actor id a real
// request might carry, so a test asserting "landed on the shared actor"
// cannot pass by coincidence.
const anonFacadeTestEntityID int64 = 900002

// TestResolveActorOrAnonymous_NoAuthHeader_ResolvesToGivenEntityID asserts
// that the facade's ResolveActorOrAnonymous truly delegates to the internal
// middleware rather than merely compiling against it: a request with no
// Authorization header at all falls through to the given entity id and the
// anonymous marker, on the same code path NewResolveActorOrAnonymous's
// anon.EntityID()-unwrap ultimately reaches.
//
// verifier/mapper/resolver are nil here deliberately: AuthenticateRequest
// (api/internal/auth/middleware.go) returns ErrNoAuthHeader before touching
// any of them when the header is empty, so this case is exercisable with no
// pool, no verifier, and no mapper — "at the level the facade permits
// without a DB", per this task's Requirement 4.
func TestResolveActorOrAnonymous_NoAuthHeader_ResolvesToGivenEntityID(t *testing.T) {
	mw := ResolveActorOrAnonymous(nil, nil, nil, anonFacadeTestEntityID)

	var gotCtx context.Context
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCtx = r.Context()
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotCtx == nil {
		t.Fatal("next was not called")
	}
	actorID, ok := opctx.ActorEntityID(gotCtx)
	if !ok {
		t.Fatal("expected opctx actor entity id to be set on the anonymous branch")
	}
	if actorID != anonFacadeTestEntityID {
		t.Errorf("opctx.ActorEntityID = %d, want %d", actorID, anonFacadeTestEntityID)
	}
	if !IsAnonymousActor(gotCtx) {
		t.Error("IsAnonymousActor = false, want true on the anonymous branch")
	}
}

// TestIsAnonymousActor_FalseOnUntouchedContext confirms the facade's
// IsAnonymousActor re-export delegates correctly on the negative case too,
// not just the positive one asserted above.
func TestIsAnonymousActor_FalseOnUntouchedContext(t *testing.T) {
	if IsAnonymousActor(context.Background()) {
		t.Error("IsAnonymousActor on a bare context = true, want false")
	}
}
