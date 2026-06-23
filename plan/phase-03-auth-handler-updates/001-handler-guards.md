# Handler Guards

## Purpose and scope

Add `is_anonymous` / nil-email guards to existing auth handlers that only make sense for named (non-anonymous) user accounts. Specifically: `login`, `email-code` (request and verify), and `password-reset` (request). These handlers must reject anonymous accounts with a clear error response rather than silently failing or sending to a nil email.

Depends on Phase 2 (service layer changes and `ErrAnonymousAccount` sentinel error). Can run in parallel with Phase 3 task 002 (new anonymous endpoint).

## Requirements

### `api/internal/handlers/auth/login.go` — `Login` handler

The login flow looks up by email string, so anonymous accounts are unreachable through the normal path (they have no email, and the handler already rejects empty email at line 34). No guard is strictly needed for the lookup path. However, if an anonymous JWT is used and the client somehow reaches this endpoint, it should fail gracefully — the existing empty-email check already handles this.

**No code change required** for `Login` beyond what Phase 2 task 001 required (the handler does not hold a `db.UserAccount` with an email field that needs guarding post-migration). Confirm this after Phase 2 completes.

### `api/internal/handlers/auth/emailcode.go`

**`sendEmailCode`** (called by `EmailCodeRequest`):
- The function already silently returns if `GetUserAccountByEmail` returns no rows (anonymous users have no email, so the lookup returns nothing — they are naturally excluded from this flow).
- After Phase 2 task 001, the `h.sender.Send(ctx, ua.Email.String, ...)` call is already guarded.
- **Add an additional guard**: if the caller provides an authenticated context (e.g., a JWT identifies an anonymous user making a request to `/v1/auth/email-code/request`), the handler should check the JWT claim and reject with `400 anonymous_account`. However, `EmailCodeRequest` is an **unauthenticated** endpoint (no Bearer token required), so there is no JWT to inspect here. No further guard is needed.

**`EmailCodeVerify`**:
- This endpoint also looks up by email string. Anonymous users have no email, so they are excluded from this lookup naturally.
- The response body at line 176–181 references `ua.Email`. After Phase 2 task 001 this is updated to `ua.Email.String`. No additional guard is needed.
- **No code change required** beyond Phase 2 task 001 updates.

### `api/internal/handlers/auth/reset.go`

**`PasswordResetRequest`**:
- Looks up by email string. Anonymous users have no email, so the lookup naturally returns nothing and the goroutine returns silently. This is already correct behavior.
- After Phase 2 task 001, the `ua.Email.Valid` guard is in place.
- **No additional code change required**.

**`PasswordResetConfirm`**: Not email-dependent; no change needed.

### `api/internal/handlers/identities.go` — Step-up challenge and credential mutation

The `IdentitiesHandler` serves `/v1/self/...` endpoints, all guarded by `RequireAuth + RequireVerifiedEmail` in the router (per the architecture doc). Anonymous users will not have a verified email, so `RequireVerifiedEmail` already blocks them from these endpoints.

**Confirm** that the `RequireVerifiedEmail` middleware properly rejects tokens issued to anonymous accounts (where `email_verified` will be false). If the JWT claim does not carry `email_verified`, the middleware may need a small update to also check `is_anonymous`. Inspect `api/internal/auth/` JWT issuance and the `RequireVerifiedEmail` middleware before deciding.

If `RequireVerifiedEmail` is email-based (checks the JWT `email` claim is non-empty), anonymous JWTs will be rejected by it naturally. Document the finding in the task commit message.

### `api/internal/handlers/auth/oidc.go`

OIDC flows always receive an email from the identity provider. No guard needed. Confirm that the `CreateUserAccount` call in the OIDC callback passes a valid `pgtype.Text{String: email, Valid: true}` after Phase 2 task 001 updates are applied.

### Summary of actual code changes in this task

After thorough review of the above, the concrete changes in this task are expected to be:

1. Inspect `api/internal/auth/` JWT issuance code — confirm whether `IssueLocalJWT` (and the equivalent for anonymous JWTs added in task 002) encodes `email_verified` or `is_anonymous` in the JWT claims.
2. If `RequireVerifiedEmail` needs adjustment to handle anonymous JWT subjects, add the check there.
3. Add a `RequireNamedAccount` middleware or inline guard to any endpoint that must be unreachable by anonymous users but is not already covered by `RequireVerifiedEmail` — specifically the self-update email path if it is not gated. Document which endpoints this applies to.

This task is intentionally scoped to guard additions only. The new anonymous endpoint is in task 002.

## Validation

- `make build.api` exits 0.
- `make test.unit` exits 0.
- An anonymous JWT (from task 002) cannot reach `/v1/self` endpoints (blocked by `RequireVerifiedEmail` or a new guard).
- `POST /v1/auth/email-code/request` with a valid anonymous JWT (if auth is required) returns an appropriate error or is naturally excluded.
- `POST /v1/auth/password-reset/request` with an anonymous-account email (empty) silently returns 204 (existing behavior preserved).

## References

- `api/internal/handlers/auth/login.go` — login handler.
- `api/internal/handlers/auth/emailcode.go` — email code handlers.
- `api/internal/handlers/auth/reset.go` — password reset handlers.
- `api/internal/handlers/identities.go` — step-up and credential mutation endpoints.
- `api/internal/auth/` — JWT issuance and middleware (inspect for `RequireVerifiedEmail` implementation).
- `api/internal/service/user_accounts.go` — `ErrAnonymousAccount` (Phase 2 task 002).

## Status

**Outcome**: succeeded  
**Date**: 2026-06-23

### Validation summary

- `make build.api`: passed (exit 0)
- `make test.unit`: passed (6 packages: auth, authz, config, handlers, handlers/auth, service — all ok)
- Anonymous JWT cannot reach `/v1/self` endpoints: confirmed — `RequireVerifiedEmail` checks `uc.EmailVerifiedAt == nil` (populated from `ua.EmailVerifiedAt` in the DB, which is nil for all anonymous accounts). No JWT claim inspection needed; the check is DB-driven.
- `POST /v1/auth/email-code/request` with anonymous JWT: endpoint is unauthenticated; anonymous accounts have no email so `GetUserAccountByEmail` returns no rows and the handler exits silently (existing behavior preserved, no change needed).
- `POST /v1/auth/password-reset/request` with empty email: silently returns 204 — existing empty-email early return at goroutine entry handles this.

### Inspection findings

1. **`IssueLocalJWT`** (`api/internal/auth/local_jwt.go`): does NOT encode `email_verified` or `is_anonymous` in JWT claims — only `Roles` and `SudoUserUUID`. The `RequireVerifiedEmail` middleware does not read JWT claims; it reads `uc.EmailVerifiedAt` from the resolved `UserContext`, which is sourced from the DB row. Anonymous accounts have `EmailVerifiedAt == nil` in the DB, so they are rejected correctly.

2. **`RequireVerifiedEmail`** (`api/internal/auth/require_verified.go`): no adjustment needed. The nil `EmailVerifiedAt` check is sufficient to block anonymous accounts without needing an `is_anonymous` flag in the JWT or middleware.

3. **No `RequireNamedAccount` middleware needed**: all `/v1/self` credential and identity endpoints (including `PUT /v1/self`, step-up, password, and OIDC link/unlink) are under `RequireAuth + RequireVerifiedEmail` in `main.go`. Anonymous accounts are excluded by the email-verification gate.

4. **OIDC handler** (`api/internal/handlers/auth/oidc.go`): confirmed — `CreateUserAccount` uses `pgtype.Text{String: principal.Email, Valid: principal.Email != ""}`. OIDC flows are additionally guarded at line 237 (reject if `principal.Email == ""`).

### Code changes made

- `api/internal/handlers/identities.go`: added defense-in-depth guard in `sendStepUpCode` — early return with a warning log when `uc.Email == ""` (anonymous account), before `CreateEmailCode` is called. Prevents creating a dangling code row for an account with no email address.
- `api/internal/auth/require_verified_test.go`: added `TestRequireVerifiedEmail_AnonymousAccount` — documents that the existing middleware blocks a `UserContext` with empty `Email` and nil `EmailVerifiedAt`.
- `api/internal/handlers/identities_stepup_test.go`: added `TestSendStepUpCode_AnonymousAccountSkipsSend` — verifies the new email guard in `sendStepUpCode` prevents `sender.Send` from being called for anonymous accounts.

### Assumptions applied

None from `## Assumptions` (section absent). Task confirmed all major handlers required no new code changes beyond the one defense-in-depth guard described above.
