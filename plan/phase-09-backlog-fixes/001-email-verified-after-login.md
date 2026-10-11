# Email Verified State Correct After Login

## Purpose and scope
Implements backlog item jKvl (project followup). `emailVerifiedAt` exposed by `useAuth()` in `gui/src/lib/auth-context.tsx` is derived from `user?.email_verified_at ?? null`, but `user` is also populated from login, register and email-code responses (`setTokenAndUser(response.user)`), whose payloads carry no `email_verified_at`. A verified account therefore reads as unverified right after sign-in. Work targets the plan branch `plan/flow-accounts-upstream`.

## Requirements
1. Make `emailVerifiedAt` correct immediately after `login()`, `register()` (which chains a login) and email-code sign-in. Preferred: add `email_verified_at` to the login and email-code `user` payloads on the server (`api/internal/handlers/auth/login.go`, `emailcode.go`, and the anonymous path where it makes sense: anonymous users are never verified, emit explicit null), reusing the field name and null semantics from `/v1/self` (`email_verified_at`, snake_case, always present, explicit null when unverified). If a server change turns out to be disproportionate, fall back to calling `refreshUser()` / `api.self.get()` after `setTokenAndUser` in those three paths. Pick one approach, state why in the task Status, and do not leave the field untruthful on any sign-in path.
2. Keep `LoginResponse.user` typing consistent with what the server now sends; update `api/openapi.yaml` for the login and email-code response schemas, and fix the schema drift flagged in the same item: `email_verified_at` was added to the shared `UserDetail` schema that also serves admin endpoints which emit the boolean `email_verified` instead; introduce a self-specific schema (or a `required` list for the `/v1/self` responses only) so the admin responses are not described as carrying the field.
3. Add tests: server tests for the login and email-code payloads (verified and unverified), and a GUI test that calls `login()` then reads `emailVerifiedAt` for a verified and an unverified account.
4. Docs: one sentence in the existing docs that describe `emailVerifiedAt`/the self response (`docs/mod-users-spec.md`, `gui/README.md`).

## Validation
1. `cd api && go build ./... && go vet ./... && go test ./...` passes.
2. `cd gui && bun test && bun run typecheck` passes (`@moduleforge/core-gui` must resolve: build the sibling `/Users/zane/playground/moduleforge/mod-core/gui` with `bun run build` first if needed; report any step skipped and why).

## References
- Backlog item jKvl in the project's `plan/followups.yaml`.
