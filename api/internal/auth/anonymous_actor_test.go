package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/opctx"
	db "github.com/moduleforge/mod-users/model/db"
)

// -----------------------------------------------------------------------
// Tests for ResolveActorOrAnonymous and the IsAnonymousActor accessor.
//
// The harness is deliberately the one middleware_test.go already builds for
// RequireAuth's characterization tests (same package): the local-only
// Verifier, the "generic" ClaimMapper, the nil-pool UserResolver with an
// injectable uuidLookup stub, the capture handler, and the error-body decoder.
// Sharing it — rather than cloning it — is what makes "identical to
// RequireAuth on every branch but one" an assertion about the same inputs
// rather than an assertion about two similar-looking test fixtures.
//
// The security invariant these tests exist to protect: a presented-but-bad
// credential must never silently downgrade to the anonymous actor. Only a
// genuinely absent Authorization header falls through.
// -----------------------------------------------------------------------

// anonActorTestEntityID stands in for the entity id NewAnonymousActor resolves
// at boot. It is deliberately far away from every account_holder id used in
// these tests, so an assertion that a caller did *not* land on the shared
// actor cannot pass by coincidence.
const anonActorTestEntityID int64 = 900001

// serveResolveActorOrAnonymous runs one request through
// ResolveActorOrAnonymous wrapped around a capture handler. An empty header
// omits the Authorization header entirely (the anonymous fall-through case);
// any other value is set verbatim.
func serveResolveActorOrAnonymous(t *testing.T, resolver *UserResolver, header string) (*httptest.ResponseRecorder, *requireAuthCaptureHandler) {
	t.Helper()
	next := &requireAuthCaptureHandler{}
	mw := ResolveActorOrAnonymous(
		newRequireAuthVerifier(t),
		newRequireAuthMapper(t),
		resolver,
		anonActorTestEntityID,
	)(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	return rec, next
}

// unreachableResolver builds a UserResolver whose uuidLookup fails the test if
// it is ever called — for cases that must be decided before Resolve runs.
func unreachableResolver(t *testing.T) *UserResolver {
	t.Helper()
	return newResolverWithStub(t, requireAuthIssuer, func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		t.Errorf("uuidLookup must not be called for this case (uuid %s)", u)
		return db.UserAccount{}, nil
	})
}

// -----------------------------------------------------------------------
// Case 1: no Authorization header → anonymous actor on opctx.
// -----------------------------------------------------------------------

func TestResolveActorOrAnonymous_NoAuthHeader_SetsAnonymousActor(t *testing.T) {
	t.Parallel()

	rec, next := serveResolveActorOrAnonymous(t, unreachableResolver(t), "")

	if !next.called {
		t.Fatalf("next was not called; status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	actorID, ok := opctx.ActorEntityID(next.ctx)
	if !ok {
		t.Fatal("expected opctx actor entity id to be set on the anonymous branch")
	}
	if actorID != anonActorTestEntityID {
		t.Errorf("opctx.ActorEntityID = %d, want %d (the shared anonymous actor)", actorID, anonActorTestEntityID)
	}
	if !IsAnonymousActor(next.ctx) {
		t.Error("IsAnonymousActor = false, want true on the anonymous branch")
	}
}

// -----------------------------------------------------------------------
// Cases 2, 3, 7, 8: every presented-credential failure still rejects.
//
// This is the confused-deputy table: not one of these rows may fall through
// to the anonymous actor, no matter how "absent" the credential looks.
// -----------------------------------------------------------------------

func TestResolveActorOrAnonymous_PresentedCredentialFailuresAreRejected(t *testing.T) {
	t.Parallel()

	knownUUID := uuid.New()
	deletedUUID := uuid.New()
	lookupFailUUID := uuid.New()

	cases := []struct {
		name        string
		header      func(t *testing.T) string
		resolver    func(t *testing.T) *UserResolver
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			// Case 2: invalid token (bad signature) → 401.
			name: "invalid token: bad signature",
			header: func(t *testing.T) string {
				return "Bearer " + signRequireAuthToken(t, "wrong-secret-entirely", requireAuthBaseClaims(knownUUID.String()))
			},
			resolver:    unreachableResolver,
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: "invalid or expired token",
		},
		{
			// Case 3: expired token → 401. Asserted separately from the
			// bad-signature row because an expired session is the realistic
			// silent-downgrade scenario.
			name: "expired token",
			header: func(t *testing.T) string {
				claims := requireAuthBaseClaims(knownUUID.String())
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
				return "Bearer " + signRequireAuthToken(t, requireAuthSecret, claims)
			},
			resolver:    unreachableResolver,
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: "invalid or expired token",
		},
		{
			// Case 7a: malformed header (non-Bearer scheme) → 401, not the
			// anonymous fall-through, even though AuthenticateRequest
			// classifies it as ErrNoAuthHeader.
			name:        "malformed header: Basic scheme",
			header:      func(t *testing.T) string { return "Basic abc" },
			resolver:    unreachableResolver,
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: "invalid Authorization header format",
		},
		{
			// Case 7b: "Bearer" with no token at all → 401, same reasoning.
			name:        "malformed header: bare Bearer with no token",
			header:      func(t *testing.T) string { return "Bearer" },
			resolver:    unreachableResolver,
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: "invalid Authorization header format",
		},
		{
			// Case 8a: ErrUserGone → 401 via the shared error helper.
			name: "resolver reports ErrUserGone",
			header: func(t *testing.T) string {
				return "Bearer " + signRequireAuthToken(t, requireAuthSecret, requireAuthBaseClaims(deletedUUID.String()))
			},
			resolver: func(t *testing.T) *UserResolver {
				return newResolverWithStub(t, requireAuthIssuer, func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
					return db.UserAccount{}, pgx.ErrNoRows
				})
			},
			wantStatus:  http.StatusUnauthorized,
			wantCode:    "unauthorized",
			wantMessage: "user no longer exists",
		},
		{
			// Case 8b: claim-mapper fault → 500 via the shared error helper.
			// A validly-signed, unexpired token missing the mandatory "sub"
			// claim verifies but fails to map.
			name: "claim mapper fault: missing sub claim",
			header: func(t *testing.T) string {
				claims := jwt.MapClaims{
					"iss": requireAuthIssuer,
					"iat": time.Now().Unix(),
					"exp": time.Now().Add(time.Hour).Unix(),
				}
				return "Bearer " + signRequireAuthToken(t, requireAuthSecret, claims)
			},
			resolver:    unreachableResolver,
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "internal_error",
			wantMessage: "failed to process authentication claims",
		},
		{
			// Case 8c: resolver internal fault → 500 via the shared helper.
			name: "resolver internal fault",
			header: func(t *testing.T) string {
				return "Bearer " + signRequireAuthToken(t, requireAuthSecret, requireAuthBaseClaims(lookupFailUUID.String()))
			},
			resolver: func(t *testing.T) *UserResolver {
				return newResolverWithStub(t, requireAuthIssuer, func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
					return db.UserAccount{}, errors.New("db: connection reset")
				})
			},
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "internal_error",
			wantMessage: "failed to resolve user",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, next := serveResolveActorOrAnonymous(t, tc.resolver(t), tc.header(t))

			if next.called {
				t.Fatalf("next was called: a presented-but-bad credential downgraded to the anonymous actor")
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			body := decodeRequireAuthErrorBody(t, rec)
			if body.Error.Code != tc.wantCode {
				t.Errorf("error.code = %q, want %q", body.Error.Code, tc.wantCode)
			}
			if body.Error.Message != tc.wantMessage {
				t.Errorf("error.message = %q, want %q", body.Error.Message, tc.wantMessage)
			}
		})
	}
}

// -----------------------------------------------------------------------
// Case 4: a valid token yields the caller's own actor, never the shared one.
// -----------------------------------------------------------------------

func TestResolveActorOrAnonymous_ValidToken_UsesResolvedActor(t *testing.T) {
	t.Parallel()

	namedUUID := uuid.New()
	namedUA := db.UserAccount{
		ID:            42,
		Uuid:          namedUUID,
		AccountHolder: 501,
		Email:         pgtype.Text{String: "user@example.com", Valid: true},
	}

	// A guest account: a real, per-device user_accounts row minted by
	// POST /v1/auth/anonymous. Its email column is NULL, which is what the
	// service layer's derived guest-account flag reads.
	guestUUID := uuid.New()
	guestUA := db.UserAccount{
		ID:            77,
		Uuid:          guestUUID,
		AccountHolder: 7700,
	}

	cases := []struct {
		name    string
		header  func(t *testing.T) string
		account db.UserAccount
	}{
		{
			name: "ordinary local JWT",
			header: func(t *testing.T) string {
				return "Bearer " + signRequireAuthToken(t, requireAuthSecret, requireAuthBaseClaims(namedUUID.String()))
			},
			account: namedUA,
		},
		{
			// A caller holding a *guest* token takes the normal authenticated
			// branch and gets their own guest actor, not the shared one. The
			// token is minted by the real IssueAnonymousJWT, so it carries the
			// inert anonymity claim verbatim — and that claim changes nothing.
			name: "guest-account JWT carrying the inert anonymity claim",
			header: func(t *testing.T) string {
				raw, err := IssueAnonymousJWT(guestUA, requireAuthSecret, requireAuthIssuer)
				if err != nil {
					t.Fatalf("IssueAnonymousJWT: %v", err)
				}
				return "Bearer " + raw
			},
			account: guestUA,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolver := newResolverWithStub(t, requireAuthIssuer, func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
				if u != tc.account.Uuid {
					t.Errorf("uuidLookup got %s, want %s", u, tc.account.Uuid)
				}
				return tc.account, nil
			})

			rec, next := serveResolveActorOrAnonymous(t, resolver, tc.header(t))

			if !next.called {
				t.Fatalf("next was not called; status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
			}

			uc, ok := FromContext(next.ctx)
			if !ok {
				t.Fatal("expected *UserContext on the authenticated branch")
			}
			if uc.EntityID != tc.account.AccountHolder {
				t.Errorf("uc.EntityID = %d, want %d", uc.EntityID, tc.account.AccountHolder)
			}
			actorID, ok := opctx.ActorEntityID(next.ctx)
			if !ok {
				t.Fatal("expected opctx actor entity id to be set")
			}
			if actorID != uc.EntityID {
				t.Errorf("opctx.ActorEntityID = %d, want %d (uc.EntityID)", actorID, uc.EntityID)
			}
			if actorID == anonActorTestEntityID {
				t.Errorf("opctx.ActorEntityID = %d, the shared anonymous actor: an authenticated caller must never land on it", actorID)
			}
			if IsAnonymousActor(next.ctx) {
				t.Error("IsAnonymousActor = true on the authenticated branch, want false")
			}
		})
	}
}

// -----------------------------------------------------------------------
// Case 5: sudo is never set on the anonymous branch — but is still set on the
// authenticated branch, so the invariant is "never on the anonymous branch",
// not "never".
// -----------------------------------------------------------------------

func TestResolveActorOrAnonymous_SudoActorNeverSetOnAnonymousBranch(t *testing.T) {
	t.Parallel()

	_, next := serveResolveActorOrAnonymous(t, unreachableResolver(t), "")

	if !next.called {
		t.Fatal("next was not called on the anonymous branch")
	}
	if sudoID, ok := opctx.SudoActorEntityID(next.ctx); ok {
		t.Errorf("opctx.SudoActorEntityID = %d, want unset on the anonymous branch", sudoID)
	}
}

func TestResolveActorOrAnonymous_SudoActorStillSetForAssumedUser(t *testing.T) {
	t.Parallel()

	primaryUUID := uuid.New()
	primaryUA := db.UserAccount{
		ID:            42,
		Uuid:          primaryUUID,
		AccountHolder: 501,
		Email:         pgtype.Text{String: "user@example.com", Valid: true},
	}
	sudoUUID := uuid.New()
	sudoUA := db.UserAccount{
		ID:            99,
		Uuid:          sudoUUID,
		AccountHolder: 777,
		Email:         pgtype.Text{String: "admin@example.com", Valid: true},
	}

	resolver := newResolverWithStub(t, requireAuthIssuer, func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		if u != primaryUUID {
			t.Errorf("uuidLookup got %s, want %s", u, primaryUUID)
		}
		return primaryUA, nil
	})
	// buildUserContext resolves the sudo user through r.queries rather than the
	// injectable uuidLookup stub, so the sudo branch needs the same fake DBTX
	// middleware_test.go uses for RequireAuth's equivalent case.
	resolver.queries = db.New(sudoAccountDBTX{account: sudoUA})

	claims := requireAuthBaseClaims(primaryUUID.String())
	claims["sudo_user_uuid"] = sudoUUID.String()
	header := "Bearer " + signRequireAuthToken(t, requireAuthSecret, claims)

	rec, next := serveResolveActorOrAnonymous(t, resolver, header)

	if !next.called {
		t.Fatalf("next was not called; status = %d, body = %s", rec.Code, rec.Body.String())
	}
	sudoID, ok := opctx.SudoActorEntityID(next.ctx)
	if !ok {
		t.Fatal("expected opctx sudo actor entity id to be set on the authenticated assume-identity branch")
	}
	if sudoID != sudoUA.AccountHolder {
		t.Errorf("opctx.SudoActorEntityID = %d, want %d", sudoID, sudoUA.AccountHolder)
	}
	actorID, ok := opctx.ActorEntityID(next.ctx)
	if !ok {
		t.Fatal("expected opctx actor entity id to be set")
	}
	if actorID != primaryUA.AccountHolder {
		t.Errorf("opctx.ActorEntityID = %d, want %d (the primary actor, not the sudo actor)", actorID, primaryUA.AccountHolder)
	}
	if IsAnonymousActor(next.ctx) {
		t.Error("IsAnonymousActor = true on the authenticated branch, want false")
	}
}

// -----------------------------------------------------------------------
// Case 6: no UserContext on the anonymous branch, and the fail-closed
// composition that invariant buys.
// -----------------------------------------------------------------------

func TestResolveActorOrAnonymous_NoUserContextOnAnonymousBranch(t *testing.T) {
	t.Parallel()

	_, next := serveResolveActorOrAnonymous(t, unreachableResolver(t), "")

	if !next.called {
		t.Fatal("next was not called on the anonymous branch")
	}
	uc, ok := FromContext(next.ctx)
	if ok || uc != nil {
		t.Errorf("FromContext = (%+v, %v), want (nil, false) on the anonymous branch", uc, ok)
	}
}

// TestResolveActorOrAnonymous_ComposedWithRequireVerifiedEmailFailsClosed pins
// the documented fail-closed behavior: composing this middleware with
// RequireVerifiedEmail is a misconfiguration, and it breaks loudly with a 500
// rather than silently admitting an anonymous caller.
func TestResolveActorOrAnonymous_ComposedWithRequireVerifiedEmailFailsClosed(t *testing.T) {
	t.Parallel()

	next := &requireAuthCaptureHandler{}
	mw := ResolveActorOrAnonymous(
		newRequireAuthVerifier(t),
		newRequireAuthMapper(t),
		unreachableResolver(t),
		anonActorTestEntityID,
	)(RequireVerifiedEmail(next))

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if next.called {
		t.Fatal("the wrapped handler ran: RequireVerifiedEmail must fail closed behind the anonymous branch")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	// RequireVerifiedEmail's missing-context branch goes through
	// apiresp.WriteError, which always writes the nested
	// {"error": {"code", "message"}} envelope (see apiresp.Envelope /
	// apiresp.ErrorBody) — never the flat shape this test used to assert.
	// WriteError also never echoes the raw wrapped error text on a 5xx
	// (apiresp.publicMessage's internal_error branch is a fixed generic
	// string), so this only pins the reserved internal_error code and a
	// non-empty message, not any specific wording.
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v (raw: %s)", err, rec.Body.String())
	}
	if body.Error.Code != "internal_error" {
		t.Errorf("error.code = %q, want %q", body.Error.Code, "internal_error")
	}
	if body.Error.Message == "" {
		t.Error("error.message: expected non-empty message")
	}
}

// -----------------------------------------------------------------------
// Case 9: no JWT can resolve to the anonymous actor.
// -----------------------------------------------------------------------

// TestResolveActorOrAnonymous_NoJWTResolvesToAnonymousActor covers the
// in-process half of the "never loginable" invariant: a locally-issued JWT
// whose subject UUID has no user_accounts row is ErrUserGone → 401. It never
// reaches the handler, and never lands on the shared actor. The structural
// half — that a user_accounts row for the system actor is impossible — is
// task 006's database-level assertion.
func TestResolveActorOrAnonymous_NoJWTResolvesToAnonymousActor(t *testing.T) {
	t.Parallel()

	orphanUUID := uuid.New()
	resolver := newResolverWithStub(t, requireAuthIssuer, func(ctx context.Context, u uuid.UUID) (db.UserAccount, error) {
		if u != orphanUUID {
			t.Errorf("uuidLookup got %s, want %s", u, orphanUUID)
		}
		return db.UserAccount{}, pgx.ErrNoRows
	})
	header := "Bearer " + signRequireAuthToken(t, requireAuthSecret, requireAuthBaseClaims(orphanUUID.String()))

	rec, next := serveResolveActorOrAnonymous(t, resolver, header)

	if next.called {
		t.Fatalf("next was called: a JWT naming a nonexistent account resolved to the anonymous actor")
	}
	if next.ctx != nil {
		t.Error("the capture handler recorded a context, meaning it ran")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	body := decodeRequireAuthErrorBody(t, rec)
	if body.Error.Message != "user no longer exists" {
		t.Errorf("error.message = %q, want %q", body.Error.Message, "user no longer exists")
	}
}

// -----------------------------------------------------------------------
// The context-key hazard: userContextKey and anonymousActorKey must not alias.
// -----------------------------------------------------------------------

func TestAnonymousActorKeyDoesNotAliasUserContextKey(t *testing.T) {
	t.Parallel()

	if int(userContextKey) == int(anonymousActorKey) {
		t.Fatalf("userContextKey and anonymousActorKey are both %d: the two context keys alias", int(userContextKey))
	}

	// A context carrying only the anonymous marker exposes no UserContext.
	anonOnly := WithAnonymousActor(context.Background())
	if uc, ok := FromContext(anonOnly); ok || uc != nil {
		t.Errorf("FromContext on an anonymous-marked context = (%+v, %v), want (nil, false)", uc, ok)
	}

	// A context carrying only a UserContext is not marked anonymous.
	want := &UserContext{EntityID: 501}
	ucOnly := WithUserContext(context.Background(), want)
	if IsAnonymousActor(ucOnly) {
		t.Error("IsAnonymousActor on a UserContext-only context = true, want false")
	}

	// Neither value clobbers the other, in either write order — the assertion
	// that actually catches an aliasing key, since a mismatched type assertion
	// would otherwise mask it as a benign zero value.
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "user context then anonymous marker", ctx: WithAnonymousActor(WithUserContext(context.Background(), want))},
		{name: "anonymous marker then user context", ctx: WithUserContext(WithAnonymousActor(context.Background()), want)},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := FromContext(tc.ctx)
			if !ok || got != want {
				t.Errorf("FromContext = (%+v, %v), want (%+v, true)", got, ok, want)
			}
			if !IsAnonymousActor(tc.ctx) {
				t.Error("IsAnonymousActor = false, want true")
			}
		})
	}
}

func TestIsAnonymousActor_FalseOnUntouchedContext(t *testing.T) {
	t.Parallel()

	if IsAnonymousActor(context.Background()) {
		t.Error("IsAnonymousActor on a bare context = true, want false")
	}
}
