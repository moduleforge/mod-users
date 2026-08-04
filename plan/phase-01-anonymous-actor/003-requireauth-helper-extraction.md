# Requireauth Characterization Tests And Helper Extraction

## Purpose and scope

Prepare `RequireAuth` for a second consumer without changing any of its behavior. Two steps, in
this order:

1. Add **characterization tests** pinning every one of `RequireAuth`'s existing branches. There
   are currently **no** direct behavioral tests for `RequireAuth` anywhere in the repo
   (`grep -rn "RequireAuth" --include=*_test.go api/` finds only comments), so "keep the
   behavior bit-identical" is presently unverifiable. Fix that first.
2. Extract the shared success-population and error-mapping blocks out of `RequireAuth`
   (`api/internal/auth/middleware.go`) into one unexported helper that task 004's
   `ResolveActorOrAnonymous` will also call, so the two middlewares cannot drift.

Invoke the standard `implement-task` skill with the Go developer role and the Go design
standards as the technology layer.

Scope: `api/internal/auth/middleware.go` and a new `api/internal/auth/middleware_test.go`. No
new middleware is added here — that is task 004.

## Requirements

### 1. Characterization tests first

Write `api/internal/auth/middleware_test.go` **against the current, unmodified `RequireAuth`**
and confirm it passes before touching `middleware.go`. Follow the in-package style already used
by `require_verified_test.go` (a `captureHandler` recording whether `next` ran, `httptest`
request/recorder, JSON body assertions) and `jwt_test.go` (minting HS256 tokens directly with
`jwt.NewWithClaims`).

Construct the dependencies without a database:
- `verifier`: `NewVerifier(ctx, "", "", testSecret, testIssuer)` — local-only mode, exactly as
  `api/cmd/server/main.go` wires it.
- `mapper`: `NewClaimMapper("generic", MapperOptions{...})` or the in-package equivalent.
- `resolver`: the injectable-stub pattern from `resolver_test.go`
  (`&UserResolver{pool: nil, queries: nil, localIssuer: ..., uuidLookup: stub}`), so the
  resolver's outcome is fully controlled by the test.

Pin every branch of `RequireAuth`, asserting **both** the status code and the exact response
body (`error` code and `message` strings), plus whether `next` ran:

| Case | Expected |
|---|---|
| No `Authorization` header at all | 401, `unauthorized`, `missing Authorization header`; `next` not called |
| `Authorization` present but not `Bearer ` form | 401, `unauthorized`, `invalid Authorization header format`; `next` not called |
| Bearer token the verifier rejects (bad signature) | 401, `unauthorized`, `invalid or expired token` |
| Bearer token with an `exp` in the past | 401, `unauthorized`, `invalid or expired token` |
| Resolver returns `ErrUserGone` | 401, `unauthorized`, `user no longer exists` |
| Claim mapper fails (error prefixed `claim map:`) | 500, `internal_error`, `failed to process authentication claims` |
| Resolver returns some other internal error | 500, `internal_error`, `failed to resolve user` |
| Success, no assumed user | `next` called; `FromContext(ctx)` yields the `*UserContext`; `opctx.ActorEntityID(ctx)` equals `uc.EntityID`; `opctx.SudoActorEntityID(ctx)` is **not** set |
| Success, `uc.AssumedUser != nil` | `next` called; `opctx.SudoActorEntityID(ctx)` equals `uc.AssumedUser.EntityID`; `opctx.ActorEntityID(ctx)` still equals `uc.EntityID` |

The missing-header / malformed-header message distinction is deliberate and pre-existing (see
the comment at `middleware.go:76-78` about the pre-9.10a behavior); it must survive the
refactor. Pin it explicitly.

Prefer table-driven tests; keep them deterministic and `t.Parallel()`-safe where the shared
`captureHandler` allows.

### 2. Extract the shared helper

Only after the characterization tests pass, refactor `RequireAuth`'s body
(`middleware.go:71-113`). Extract:

- **The error-mapping block** (`middleware.go:73-101`) into an unexported helper — something
  like `writeAuthError(w http.ResponseWriter, r *http.Request, err error)` — that performs the
  identical `errors.Is` switch and writes the identical `server.Error` responses, including the
  missing-vs-malformed header distinction and the `claim map:` prefix classification with its
  `slog.ErrorContext` calls.
- **The success-population block** (`middleware.go:102-111`) into an unexported helper —
  something like `contextWithAuthenticatedActor(ctx context.Context, uc *UserContext)
  context.Context` — that applies `WithUserContext`, `opctx.WithActor`, and the conditional
  `opctx.WithSudoActor`.

`RequireAuth` then becomes a thin composition of `AuthenticateRequest` + these two helpers.
Task 004 calls the same two helpers for its own success and non-`ErrNoAuthHeader` error paths.

Preserve the existing comments' content — the rationale about `opctx` population and about the
`WithUserContext` richer struct is load-bearing documentation; move it with the code rather than
dropping it.

Keep the helpers unexported. Do not change `AuthenticateRequest`, its signature, its sentinel
errors, or its doc comment.

### Must not change

- `RequireAuth`'s exported signature, and every observable behavior it has today: status codes,
  response bodies, log lines, and the exact context values it sets. This is the single hardest
  constraint in the task — the whole point of the characterization tests is to prove it.
- `api/auth/auth.go`'s `RequireAuth` wrapper.
- `AuthenticateRequest`, `RequireVerifiedEmail`, `RequireOIDCConfirmed`.
- `api/internal/authz/authz.go`, mod-core's `opctx`, `anon_tokens`, `user_accounts`.
- Any existing route's `scope:` in `moduleforge.module.yaml`, and any wiring in
  `api/cmd/server/main.go`.

## Validation

- `api/internal/auth/middleware_test.go` exists and covers all nine cases in the table above.
- The characterization tests pass against the **pre-refactor** `middleware.go` (verify this
  before the refactor commit — a checkpoint commit at that point is the cleanest evidence) and
  continue to pass unchanged after it. The test file must not be edited as part of making the
  refactor pass.
- `git diff` on `middleware.go` shows only motion of existing logic into helpers plus the new
  helper signatures — no changed status codes, message strings, or log calls. Review the diff
  explicitly for this and state the conclusion in the task report.
- `cd api && go build ./...`, `go vet ./...` clean.
- `cd api && go test ./internal/auth/...` passes; all pre-existing tests in the package pass
  unchanged.
- `make lint` and `make test.unit` pass.
- `grep -n "func RequireAuth" api/internal/auth/middleware.go` shows the signature is byte-identical
  to `func RequireAuth(verifier *Verifier, mapper ClaimMapper, resolver *UserResolver) func(http.Handler) http.Handler`.

## Metadata

architectural_impact: true

## Assumptions

- This task is independent of tasks 001 and 002 and may run concurrently with them; it touches
  no file they touch.
- `server.Error` (`api/internal/server`) keeps its current response shape; the tests assert on
  the JSON body it produces.

## Checkpoint hints

- After the characterization tests pass against unmodified `middleware.go` (before any refactor).
- After the error-mapping helper is extracted and the tests still pass.
- After the success-population helper is extracted and the tests still pass.

## References

- [Anonymous-actor architecture proposal](../notes/anonymous-actor-architecture-proposal.md) —
  §3, in particular: "The implementation should extract the shared success-population and
  error-mapping blocks (`middleware.go:73-111`) into one unexported helper that both middlewares
  call, so the two cannot drift." **Authoritative.**
- [Current-state audit](../notes/current-state-audit.md) — §1b and §6 "Hard/unfavorable facts":
  `RequireAuth`'s error branches are reject-and-stop by construction.
- `api/internal/auth/require_verified_test.go` — the `captureHandler` + `httptest` style.
- `api/internal/auth/jwt_test.go` — minting HS256 tokens with `jwt.NewWithClaims`.
- `api/internal/auth/resolver_test.go` — the nil-pool injectable-stub `UserResolver` pattern.

## Status

- **Outcome:** succeeded. 2026-08-04.
- Added `api/internal/auth/middleware_test.go` (`TestRequireAuth`, table-driven, `t.Parallel()`
  subtests) covering all nine characterization cases from the requirements table. Confirmed the
  tests pass against the unmodified `middleware.go` first (checkpoint commit `88d8692`), then
  extracted `writeAuthError` (error-mapping) and `contextWithAuthenticatedActor`
  (success-population) as unexported helpers in `middleware.go`, both unchanged in logic — the
  diff is pure code motion plus the two new helper signatures/doc comments (verified via
  `git diff 88d8692 429a5db -- api/internal/auth/middleware.go`; no status code, message string,
  or log call changed). All nine tests still pass unchanged after the refactor
  (checkpoint commit `429a5db`); the test file was not edited to make the refactor pass.
- **AssumedUser case note:** `resolver.buildUserContext` populates `uc.AssumedUser` only via
  `UserResolver.queries.GetUserAccountByUUID` (a direct call, not the injectable `uuidLookup`
  stub `resolver_test.go` uses for the primary-user lookup). To exercise this branch without a
  real database, the test file adds a minimal test-only `db.DBTX`/`pgx.Row` fake
  (`sudoAccountDBTX`/`sudoAccountRow`) that answers `GetUserAccountByUUID` with a fixed row. This
  wasn't spelled out in the task doc's "resolver: the injectable-stub pattern from
  resolver_test.go" guidance (that pattern alone only covers the `uuidLookup`-driven cases); flagged
  for awareness in case task 004 hits the same gap.
- **Validation:** `go build ./...`, `go vet ./...`, `go test -race ./internal/auth/...`, and
  `make lint.api` all pass cleanly. `grep -n "func RequireAuth" api/internal/auth/middleware.go`
  confirms the exported signature is byte-identical to the required signature.
  `make lint` (full) and `make test.unit` (full) each fail, but only on pre-existing,
  out-of-scope conditions unrelated to this diff — see `flagged_for_manager` in the task
  report for detail (a `model/` shadow-db-lint schema issue reproduced on the unmodified main
  checkout, and the already-tracked flaky `TestNewStepUpConsumedCache_JanitorStopsOnCancel` in
  the unrelated `api/auth` package, followup id `5RbD`).
- **Security review:** self-performed `review-changes-security` lens pass (no `Task` tool
  available in this environment) against `git diff a1c438c..429a5db` — no findings; the change is
  a pure behavior-preserving refactor of an unexported code path plus additive test coverage.
  See the task report's `review_reports` for the full structured review.
