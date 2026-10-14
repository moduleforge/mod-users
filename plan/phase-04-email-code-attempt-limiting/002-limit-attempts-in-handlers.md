# Limit Attempts In Handlers

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Backlog item GDol, part 2: use the new queries to bound guessing on both verify endpoints, add a request cooldown, and remove the timing oracle. Design in [attempt limiting design](../notes/attempt-limiting-design.md). Depends on task 001. Scope: `api/internal/handlers/auth/emailcode.go`, `api/internal/handlers/identities.go`, and their tests.

## Requirements

1. Define `maxEmailCodeAttempts = 5` once (or twice with an equality test, see the design note) and the 60 second request cooldown constant.
2. `EmailCodeVerify` and `StepUpVerify`: after fetching the active code, call the claim-attempt query before the bcrypt comparison. No row returned (exhausted or consumed concurrently) responds with the existing 401 (`unauthenticated`, "invalid or expired code" for email code; `apiresp.ErrUnauthenticated` for step-up) and never reveals lockout. A matching code is consumed as today. Do not change any status code, body, or route.
3. `EmailCodeVerify`: for the unknown-email and no-active-code paths, run `bcrypt.CompareHashAndPassword` against a package-level dummy hash generated at init with `bcrypt.DefaultCost`, then return the same 401, so response time does not reveal whether the email exists. Mirror whatever dummy-hash helper `Login` already uses if one exists.
4. `sendEmailCode` and `sendStepUpCode`: before minting, skip silently (log at debug) when an unconsumed, unexpired code for the same (user, purpose) was created within the cooldown. The requests keep their constant ~200 ms 204 behavior.
5. Tests (use fakes or an integration setup consistent with existing tests):
   - 5 wrong guesses exhaust the code; the 6th attempt with the correct code still returns 401, for both endpoints.
   - A correct code before exhaustion succeeds and a failed attempt count does not block a fresh code after the cooldown.
   - A request within the cooldown does not create a second code.
   - The unknown-email path performs a bcrypt comparison (assert via a test hook or by timing bound only if stable; prefer a structural test that the dummy comparison function is invoked).
   - Concurrency: N parallel wrong guesses never yield more than the max attempts (run with `-race`).
6. Verify end to end against a throwaway Postgres 16 once (unique container, removed afterward; never `make dev.*` or `clean.data`) if an integration harness is available.

## Validation

- `cd api && go test -race ./internal/handlers/...` passes.
- `cd api && go vet ./...` and `make lint` pass.
- Response status codes and bodies of both verify endpoints unchanged for the non-locked paths (existing tests green).
- Security reviewer can trace: attempts are claimed atomically before comparison; lockout is indistinguishable from a wrong code.

## Metadata

architectural_impact: true

## Checkpoint hints

- After EmailCodeVerify changes
- After StepUpVerify changes
- After cooldown on both request paths
- After tests

## Status

- Outcome: succeeded (2026-10-14).
- Validation: `go test -race ./internal/handlers/...`, `go vet ./...`, `make lint`, and `go test ./...` in `api` all pass.
- Changed: `api/internal/handlers/auth/emailcode.go` (claim before compare, cooldown, bcrypt dummy compare), `api/internal/handlers/auth/register.go` (querier interface), `api/internal/handlers/identities.go` (claim, cooldown, `emailCodeStore` seam), plus tests `api/internal/handlers/auth/emailcode_attempts_test.go`, `api/internal/handlers/identities_stepup_attempts_test.go`, `api/internal/handlers/auth/session_user_test.go`.
- Decisions: two constants (exported `MaxEmailCodeAttempts` and `EmailCodeRequestCooldown` in the auth package, unexported twins in handlers) with an equality test. The exhausted-claim path also runs a dummy bcrypt compare so lockout costs the same as a wrong guess. `Login`'s dummy hash is argon2, so a separate bcrypt dummy hash was added. No handler-level DB harness exists, so the end-to-end Postgres run (requirement 6) was not performed; the SQL itself is covered by task 001.
