package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/moduleforge/core-api/opctx"
)

// WithAnonymousActor marks ctx as carrying the shared anonymous system actor.
//
// It records the marker only; placing the actor's entity id on the context is
// a separate step (opctx.WithActor). ResolveActorOrAnonymous is the only
// production caller and always does both together.
func WithAnonymousActor(ctx context.Context) context.Context {
	return context.WithValue(ctx, anonymousActorKey, true)
}

// IsAnonymousActor reports whether ctx was populated by ResolveActorOrAnonymous'
// anonymous branch — i.e. the actor on ctx is the shared, zero-authority
// anonymous system actor rather than an authenticated principal.
//
// Handlers and observers use it to tell "no credential was presented" apart
// from "a credential was presented and resolved to a real account". It is
// false for every authenticated request, including one whose account happens
// to be a guest account, and false for any context this package's middleware
// never touched.
//
// This marker is deliberately distinct from the two pre-existing and unrelated
// "anonymous" notions in this codebase. Neither may be reused or repurposed as
// its carrier:
//
//   - The derived guest-account flag on the service layer's UserAccount
//     (toUserAccount in api/internal/service/user_accounts.go, computed from
//     !ua.Email.Valid). That describes a *guest account*: a real, per-device
//     user_accounts row minted by POST /v1/auth/anonymous, which holds a JWT,
//     can own data, and can later be upgraded to a named account. The
//     anonymous system actor has no user_accounts row, no credential, and owns
//     nothing, ever.
//   - The inert anonymity claim written by IssueAnonymousJWT
//     (api/internal/auth/local_jwt.go), which is written but never read —
//     nothing in jwt.go, the claim mappers, or resolver.go extracts it, and
//     neither Principal nor UserContext has a field for it. More
//     fundamentally, a request that resolves to the anonymous system actor
//     carries no JWT at all, so a JWT claim is the wrong carrier for it by
//     definition.
func IsAnonymousActor(ctx context.Context) bool {
	anon, _ := ctx.Value(anonymousActorKey).(bool)
	return anon
}

// ResolveActorOrAnonymous returns opt-in middleware that behaves exactly like
// RequireAuth except on one branch: a request that presents no Authorization
// header at all falls through to the shared anonymous system actor instead of
// being rejected with 401.
//
// anonActorEntityID is the entity id of the single persisted, zero-authority
// anonymous system actor (the system_actors row with slug 'anonymous'). It is
// resolved at startup rather than hardcoded, because entities.id is a
// BIGSERIAL whose value is not deterministic across environments.
//
// Branching on AuthenticateRequest's already-classified sentinel errors:
//
//	AuthenticateRequest result            behavior
//	------------------------------------  --------------------------------------
//	success                               identical to RequireAuth: WithUserContext
//	                                      + opctx.WithActor (+ opctx.WithSudoActor
//	                                      when assuming an identity), then next
//	ErrNoAuthHeader, header truly absent  the anonymous actor's id + the
//	                                      anonymous marker, then next
//	ErrNoAuthHeader, header present       401 — malformed or non-Bearer header
//	ErrInvalidToken                       401
//	ErrUserGone                           401
//	claim-map or resolver fault           500
//
// Only the genuinely-absent-header row diverges from RequireAuth. Every other
// row is produced by the same unexported helpers RequireAuth itself uses, so
// the two middlewares cannot drift apart.
//
// A presented-but-bad credential never silently downgrades to anonymous. An
// expired token, a bad signature, a deleted account, or a malformed header is
// a caller error and stays a 401. AuthenticateRequest reports ErrNoAuthHeader
// both for an absent header and for a present-but-non-Bearer one, so this
// middleware re-reads the header to tell the two apart — the same distinction
// RequireAuth's error mapping already draws. Downgrading instead would be the
// classic confused-deputy pattern, in which a caller believes it is
// authenticated while quietly being served anonymous results.
//
// Invariants on the anonymous branch, each pinned by a test in
// anonymous_actor_test.go:
//
//   - No sudo actor is ever set. Identity assumption requires an authenticated
//     admin, full stop.
//   - No UserContext is ever placed on the request context. That makes an
//     accidental composition with RequireVerifiedEmail fail closed:
//     RequireVerifiedEmail responds 500 (reserved-core internal_error, via
//     apiresp.WriteError's nested {"error":{"code","message"}} envelope —
//     not a literal "server misconfiguration" message) when no UserContext
//     is present, so a route group that wrongly composes both breaks
//     visibly in its first integration test rather than silently.
//   - The anonymous marker is set here and only here — never on the
//     authenticated branch.
//
// Hard preconditions for every route group that opts into this middleware:
//
//   - Per-IP rate limiting is required. Every anonymous request shares one
//     actor id, so actor-based throttling is meaningless. Any route opting in
//     must sit behind a per-IP/CIDR limiter at the edge or in the composition
//     root. mod-users documents this precondition and deliberately does not
//     ship the limiter.
//   - Never compose with requireAuth or requireVerifiedEmail on the same route
//     group. The former is contradictory; the latter fails closed with a 500.
//   - Keep requireOIDCConfirmed outermost, matching every existing route entry
//     in mod-users' manifest. It can respond 503 before actor injection ever
//     runs; that is acceptable, since an unconfirmed instance should serve no
//     traffic.
//   - HTTP caching: responses from an anonymous-enabled route group differ
//     between anonymous and authenticated callers. Any cache or CDN in front
//     of such a route must key on the Authorization header — consuming modules
//     emit "Vary: Authorization" from their handlers; this middleware does not
//     set it.
//   - Audit attribution collapses for the shared actor:
//     audit_log.actor_entity_id would be identical for every anonymous caller.
//     Attribution for anonymous traffic is opctx.RequestID plus HTTP access
//     logs, not audit_log. No module may authorize a mutating operation for
//     the anonymous actor.
func ResolveActorOrAnonymous(
	verifier *Verifier, mapper ClaimMapper, resolver *UserResolver,
	anonActorEntityID int64,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uc, err := AuthenticateRequest(r, verifier, mapper, resolver)
			if err != nil {
				// Only a genuinely absent credential falls through. A
				// header that is present but unusable (non-Bearer, or
				// "Bearer" with nothing after it) also yields
				// ErrNoAuthHeader, and must still be rejected exactly as
				// RequireAuth rejects it.
				if errors.Is(err, ErrNoAuthHeader) && r.Header.Get("Authorization") == "" {
					ctx := opctx.WithActor(r.Context(), anonActorEntityID)
					ctx = WithAnonymousActor(ctx)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				writeAuthError(w, r, err)
				return
			}
			ctx := contextWithAuthenticatedActor(r.Context(), uc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
