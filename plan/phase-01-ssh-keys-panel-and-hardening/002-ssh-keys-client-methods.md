# SSH Keys Client Methods

## Purpose and scope

Add typed users-gui client methods for the already-shipped self-service SSH-key API and the step-up challenge endpoints, so the `SSHKeysPanel` (task `003`) can call them through the shared client. Scope: `gui/src/lib/api.ts`, `gui/src/lib/api.test.ts`, and `gui/src/index.ts`. No component, backend, or doc change. No standard skill covers this; implement directly.

**Coordination with an in-flight plan (soft dependency, both orders are fine).** The `home-app-switcher` wave's mod-users plan `users-gui-integration-seams` (worktree `worktrees/plan/users-gui-integration-seams`) also edits `gui/src/lib/api.ts` and `gui/src/index.ts`:

- its task `phase-01-gui-seams/001-runtime-config-base-url-and-token-key` adds `gui/src/lib/config.ts` and `configureUsersApi`, makes `createUsersClient` accept `baseUrl: string | (() => string)` with a getter, routes `getToken` and the 401 branch through `config.ts`, and rebuilds the `api` singleton on `getApiBaseUrl`;
- its task 005 adds `api.auth.verifyEmail` and a `purpose` field;
- several of its tasks append exports to `index.ts`.

At planning time (mod-users `main` `4ae3641`) none of that had merged. Before editing, check the task branch's base:

- **If `gui/src/lib/config.ts` exists** (seams 001 merged): build on it unchanged. The new methods inherit the lazy base URL automatically. Add the extra test case in requirement 5.
- **If it does not exist:** proceed against today's `api.ts`. Keep the additions in their own hunks: a new types section after the Apps types, and new client groups appended after the `apps` group in `createUsersClient`'s returned object. Do not touch `getToken`, the 401 branch, `API_BASE_URL`, or the `api` singleton construction, which are the lines seams 001 rewrites. That keeps the eventual rebase on either side to trivial, adjacent-hunk conflicts.

Record which case applied in the task report.

## Requirements

1. **Types**, in a new `// ─── SSH keys` section in `api.ts`, matching `api/openapi.yaml`'s `SSHKey`, `SSHKeyCreate`, and `PaginatedSSHKeys` schemas and the step-up handler's response (`api/internal/handlers/identities.go`, `stepUpVerifyResponse`):
   - `SSHKey { uuid: string; key_type: string; fingerprint: string; public_key: string; label: string; created_at: string }`. Keep `key_type` as `string`, not a closed union, so a server-side allow-list change cannot break typing.
   - `SSHKeyListResponse { items: SSHKey[]; total: number }`
   - `RegisterSSHKeyRequest { public_key: string; label?: string | null }`
   - `StepUpVerifyResponse { step_up_token: string; expires_in: number }`
   - `StepUpOptions { stepUpToken?: string }`
2. **Client groups** appended to the object `createUsersClient` returns, each calling the existing `request()` with no change to `request()` itself:
   - `sshKeys.list(params?: { limit?: number; offset?: number })` sends `GET /v1/self/ssh-keys`, with a query string built with `URLSearchParams` only from the provided params.
   - `sshKeys.register(data: RegisterSSHKeyRequest, options?: StepUpOptions)` sends `POST /v1/self/ssh-keys` with a JSON body, plus the header `X-Step-Up-Token: <token>` only when `options.stepUpToken` is a non-empty string.
   - `sshKeys.revoke(keyUuid: string, options?: StepUpOptions)` sends `DELETE /v1/self/ssh-keys/${encodeURIComponent(keyUuid)}`, with the same header rule. It returns `void` (the server sends `204`).
   - `stepUp.request()` sends `POST /v1/self/credential/step-up` and returns `void` (`204`).
   - `stepUp.verify(code: string)` sends `POST /v1/self/credential/step-up/verify` with body `{ code }` and **always passes `skipAuthRedirect: true`**. The backend answers a wrong or expired code with `401`, and without the flag `request()` would clear the session token and hard-redirect to the login page. Explain this in a comment and in the JSDoc.
   - Give each method a short JSDoc naming its endpoint, its step-up gating, and its action-required outcomes (`users.email_unverified` on register and revoke; `users.step_up_required` when `AUTH_REQUIRE_STEP_UP` is on).
3. Do not add operator-route methods (`/v1/user-accounts/{uuid}/ssh-keys`). This plan is self-service only.
4. **Exports** from `gui/src/index.ts`: add the five new types to the existing `export type { ... } from './lib/api'` block. Append them at the end of the block to keep merges with the seams plan append-only.
5. **Tests** in `gui/src/lib/api.test.ts`, following its existing `stubFetch` and `afterEach` restore pattern, with a fetch stub that records the URL, method, headers, and body:
   - `list()` with no params calls `.../v1/self/ssh-keys` with no query string; `list({ limit: 200 })` adds `?limit=200`.
   - `register` without options sends no `X-Step-Up-Token` header; with `{ stepUpToken: 't' }` it sends `X-Step-Up-Token: t` and the JSON body unchanged. `{ stepUpToken: '' }` sends no header.
   - `revoke` URL-encodes the key UUID and resolves `undefined` on `204`.
   - A `409` body `{action: {code: 'users.step_up_required', message, path: '/step-up'}}` on `register` rejects with `ApiActionRequiredError` (code, `path` `/step-up`, status 409). A `403` `users.email_unverified` action on `revoke` rejects the same way.
   - A `409` `{error: {code: 'conflict', details: [{field: 'public_key', code: 'users.ssh_key_in_use', ...}]}}` rejects with `ApiRequestError` carrying the details.
   - `stepUp.verify('000000')` against a stubbed `401` rejects with `ApiRequestError` code `unauthenticated` **and** leaves `localStorage.auth_token` in place, with `window.location.href` not changed to the login path. That is the regression guard for the `skipAuthRedirect` rule. If seams 001 has merged, use its token-key helpers for the assertion.
   - Only if seams 001 has merged: after `configureUsersApi({ baseUrl: '' })`, `api.sshKeys.list()` fetches a relative `/v1/self/ssh-keys` URL. Restore with `resetUsersApiConfig()`.
   - All pre-existing tests pass unchanged.

## Validation

- `cd gui && bun run typecheck && bun test` passes. It needs `mod-core/gui` built from mod-core's real checkout; see `AGENTS.md` First-time setup and its "Known gap" about building through the worktree symlink.
- `git diff --stat`, excluding `plan/`, touches only `gui/src/lib/api.ts`, `gui/src/lib/api.test.ts`, and `gui/src/index.ts`.
- `git diff gui/src/lib/api.ts` shows no change to `request()`, `getToken`, the 401 branch, `API_BASE_URL`, or the `api` singleton line (or, if seams 001 merged, to `config.ts`-related lines).
- `grep -n "localhost" gui/src/lib/api.ts` shows only the pre-existing fallback and JSDoc example lines, with no new occurrence.
- `grep -n "skipAuthRedirect: true" gui/src/lib/api.ts` includes the `stepUp.verify` call.

## Assumptions

- The SSH-key and step-up endpoints behave as recorded in [the design note's wire contract](../notes/ssh-keys-panel-design.md#backend-wire-contract-the-panel-consumes). The step-up endpoints are not in `api/openapi.yaml` (followup `biJk`), so `api/internal/handlers/identities.go` is the source for them.

## References

- [The design note](../notes/ssh-keys-panel-design.md): wire contract and the coordination section.
- `gui/src/lib/api.ts`, `gui/src/lib/api.test.ts`, `gui/src/index.ts`.
- `api/openapi.yaml` (`/v1/self/ssh-keys`, `SSHKey`, `SSHKeyCreate`, `PaginatedSSHKeys`); `api/internal/handlers/identities.go` (`StepUpRequest`, `StepUpVerify`).
- The seams plan's `plan/phase-01-gui-seams/001-runtime-config-base-url-and-token-key.md` in worktree `worktrees/plan/users-gui-integration-seams` (read-only), for what it changes in `api.ts`.
