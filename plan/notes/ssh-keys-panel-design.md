# SSH Keys Panel Design

## Purpose and scope

Research findings and design decisions for mod-users' slice of the `mfgit-upstream-modules` plan-group: the users-gui `SSHKeysPanel`, label hardening (followup `GrC7`), and the `mod_users` schema operations note (followup `AWmf`). Every claim cites a file read at planning time (mod-users `main` at `4ae3641`, 2026-10-04). The cross-project rationale lives in `/Users/zane/playground/moduleforge/.flow/app-mfgit-managed-mode-n1-n3-q6-review.md` ("Scope of the N1 work", mod-users items 1 and 2).

## Backend wire contract the panel consumes

The backend shipped with the `ssh-public-keys` plan (merged 2026-09-28). Nothing below changes in this plan except the label rule (see [Label hardening](#label-hardening-grc7)).

| Call | Request | Success | Gates and failures |
|---|---|---|---|
| `GET /v1/self/ssh-keys?limit=&offset=` | none | `200 {items: SSHKey[], total: number}`; `items` is always an array, never `null` (`api/internal/handlers/ssh_keys.go`, `list`) | Reachable with an unverified email; never step-up-gated. Pagination: `limit` max 200, default 20. |
| `POST /v1/self/ssh-keys` | `{public_key: string, label?: string \| null}` plus optional `X-Step-Up-Token` header | `201 SSHKey` | `403 action users.email_unverified` (path `/verify-email`); `409 action users.step_up_required` (path `/step-up`); `409 error conflict` with detail `users.ssh_key_in_use` on `public_key`; `400 error invalid_input` with detail `users.ssh_key_invalid`, `users.ssh_key_type_unsupported`, `users.ssh_key_too_weak` (field `public_key`) or `users.ssh_key_label_too_long` (field `label`) |
| `DELETE /v1/self/ssh-keys/{key_uuid}` | optional `X-Step-Up-Token` header | `204` | `403` is either the action `users.email_unverified` or a masked plain error (unknown, foreign, or already-revoked key); `409 action users.step_up_required` |
| `POST /v1/self/credential/step-up` | none | always `204`, held to about 200 ms | Requires a verified email. Emails a 6-digit code (purpose `credential_change`, 5-minute expiry) (`api/internal/handlers/identities.go`, `StepUpRequest`). |
| `POST /v1/self/credential/step-up/verify` | `{code: string}` | `200 {step_up_token: string, expires_in: number}` | Wrong or expired code returns **`401`** (`identities.go`, `StepUpVerify`). |

`SSHKey` is `{uuid, key_type, fingerprint, public_key, label, created_at}` (`api/openapi.yaml`, `SSHKey` schema). `fingerprint` is OpenSSH `SHA256:<base64>` form.

The step-up endpoints are absent from `api/openapi.yaml` (pre-existing gap, followup `biJk`); the handler code above is the source.

Facts that shape the client design:

- **A wrong step-up code is a 401.** `request()` in `gui/src/lib/api.ts` turns every 401 into a token clear plus a hard redirect to `/auth/login` unless `skipAuthRedirect: true` is passed. The step-up verify call must pass it, or a typo signs the user out. The same latent behavior is tracked for `EmailCodePage` as finding `ljyL` in the `users-gui-integration-seams` plan.
- **A step-up token is single-use and consumed on verification, before the mutation runs.** `checkStepUp` (`api/internal/handlers/identities.go`) calls `VerifyStepUpToken`, which records the JTI as consumed. If the register call then fails with a `400` (for example a malformed key), the token is spent. The next attempt gets `409 users.step_up_required` again and needs a fresh code. The panel must not cache or reuse a token across attempts.
- **The email-verification gate runs before the step-up gate.** `RequireVerifiedEmail` is route middleware, so an unverified account sees `403 users.email_unverified` first and never reaches step-up.
- **`action.path` is already sanitized** by `request()` (`sanitizeActionPath`) before it reaches `ApiActionRequiredError.path`.

## Decision: step-up is completed inline in the panel

The research rounds require the panel to "handle" `users.step_up_required` but do not say how. No `/step-up` page exists anywhere: the `users-gui-integration-seams` plan explicitly declined to build one (its finding `Y7W0`), and `AuthProvider` has no step-up navigation (project followup `u353`). Navigating away to a page that does not exist would leave self-service key management unusable on any host with `AUTH_REQUIRE_STEP_UP` on. That includes MFManager, the managed-mode account home, where round 3 relies on "step-up works, because MFManager is a normal mod-users host."

The panel therefore completes step-up itself, as a small inline challenge:

1. A gated call (register or revoke) fails with `ApiActionRequiredError` code `users.step_up_required`.
2. The panel keeps the pending operation (the form values, or the key being revoked) and shows a challenge: "Confirm it's you. We'll email a code." with a "Send code" button. It calls `POST /v1/self/credential/step-up` only when the user clicks.
3. The user enters the code. The panel calls verify with `skipAuthRedirect: true`. A `401` shows "That code is wrong or has expired" inline and stays signed in.
4. On success, the panel retries the pending operation exactly once with `X-Step-Up-Token`, then discards the token whatever the outcome.
5. Cancel discards the pending operation and the challenge.

This is the first GUI call site for `users.step_up_required` in mod-users. It does not add a reusable `/step-up` page or `AuthProvider` wiring, so followups `u353` and `Y7W0` stay open. A later standard step-up page could reuse the client methods this plan adds.

## Decision: `users.email_unverified` and other actions are delegated to the host

Verification is a whole-page flow, and the seams plan is building the standard `VerifyEmailPage` for it. The panel does not duplicate that flow. On any action-required error other than step-up, the panel:

- calls an optional `onActionRequired(error: ApiActionRequiredError)` prop, which the host uses to navigate to `error.path`; and
- when the prop is absent, shows an inline notice built from `error.message`. For `users.email_unverified` the notice says that SSH keys can be added or removed only after the email address is verified.

The panel never calls `window.location` or a router itself. This matches the library's router-agnostic rule ("components report outcomes via router-agnostic callback props", `docs/architecture.md`, GUI component library).

Listing is reachable while unverified, so an unverified user still sees their keys. Only add and revoke trigger the notice.

## Decision: base URL comes from the shared client, not from the panel

`gui/src/lib/api.ts` has one runtime base-URL mechanism today: the module singleton `api = createUsersClient({ baseUrl: API_BASE_URL })`. `API_BASE_URL` resolves `NEXT_PUBLIC_API_BASE_URL`, then `window.__USERS_API_URL__`, then `http://localhost:8080`, at module load. Every existing component (`ForgotPasswordPage`, `LoginForm`, `AuthProvider`, and others) calls that singleton directly. None takes a base-URL or client prop.

The panel follows the same pattern: it calls `api.sshKeys.*` and `api.stepUp.*` on the shared singleton. It never reads an env var, never reads `window.__USERS_API_URL__`, and never contains a URL literal. Whatever base-URL mechanism the library has when the panel ships, the panel inherits it.

### Coordination with `users-gui-integration-seams`

The in-flight `home-app-switcher` wave's mod-users plan `users-gui-integration-seams` (worktree `worktrees/plan/users-gui-integration-seams`) has not started (all 10 tasks open at planning time). Its task `phase-01-gui-seams/001-runtime-config-base-url-and-token-key` adds `gui/src/lib/config.ts` (`configureUsersApi`, lazy `getApiBaseUrl`). It also rewrites parts of `api.ts`: `createUsersClient` accepts a `baseUrl` function, `getToken` uses `getStoredToken`, the 401 branch uses `clearStoredToken`, and the singleton becomes `createUsersClient({ baseUrl: getApiBaseUrl })`. Its task 005 adds `api.auth.verifyEmail` and a `purpose` field, and several of its tasks append to `gui/src/index.ts`.

- **If seams 001 has merged first:** the panel inherits `configureUsersApi` with no extra work. The panel's tests should add one case: after `configureUsersApi({ baseUrl: '' })`, the panel's list call goes to a relative `/v1/self/ssh-keys` URL.
- **If this plan merges first:** nothing in this plan blocks on seams. Seams 001 will rebase over the added `sshKeys` and `stepUp` groups. They sit in their own hunks: types go in a new section after the Apps types, and client groups are appended after the `apps` group. The conflicts should be trivial, adjacent hunks in `createUsersClient`'s returned object and append-only lines in `index.ts`. The new methods use only `request()` and do not touch `getToken`, the 401 branch, or the singleton construction, which are the lines seams 001 rewrites.

Either order works. The client-methods task records which case applied.

## Panel shape

- **Name and export:** `SSHKeysPanel` from `gui/src/components/ssh-keys-panel.tsx`, exported with `SSHKeysPanelProps` from `gui/src/index.ts`. It is a panel, not a page: no full-height centering wrapper, so a host can put it on a settings or account page (app-mfgit standalone, MFManager account page).
- **Props (all optional):** `onActionRequired?: (error: ApiActionRequiredError) => void`; `title?: string` (default "SSH keys"); `description?: string` (default text explaining that the keys authenticate git over SSH).
- **List view:** one row per key showing the label (or "Untitled key" when empty), `key_type`, `fingerprint` (monospace), and the created date, with a Revoke action. Show an empty state and a loading state. Fetch with `limit=200` in one page. If `total > items.length`, show "Showing N of M keys". Full pagination UI is not needed: 200 keys per account is far above realistic use.
- **Label rendering:** render the label inside a `<bdi>` element. This defends against bidi-override labels stored before the label-hardening fix, which does not rewrite existing rows. React already escapes text, so this covers only the display-spoofing vector.
- **Add form:** a `<textarea>` for the `authorized_keys` line (core-gui has no Textarea primitive; style it to match core-gui's `Input`), an optional label `Input` with `maxLength={100}`, and a submit button. On success, clear the form and refresh the list. Do not insert the key optimistically: the server is the source of truth for fingerprint and type.
- **Revoke:** confirm in the library's existing `Dialog` (`gui/src/components/ui/dialog.tsx`) and show the key's label and fingerprint. On `204`, refresh the list. A masked `403` (plain error, not an action) shows "This key could not be revoked. It may already have been removed." and refreshes.
- **Errors:** map `ApiRequestError.details` to per-field messages for `public_key` and `label` through core-gui's `FieldError`, matching how existing forms render details. Fall back to `ErrorMessage` or `ErrorBanner` for others. Show friendly copy for the known detail codes: in use, invalid, unsupported type, too weak, label too long, and the new label-invalid code.
- **Client-only:** the panel uses hooks and `localStorage` (indirectly, through `request()`), and its file starts with `'use client';` like every other component file.

## Label hardening (`GrC7`)

`NormalizeLabel` (`api/internal/sshkey/label.go`) bounds only length. A NUL in a caller-supplied label reaches Postgres and fails the INSERT as a `500`. Control characters and Unicode bidi overrides are stored and echoed in API responses and audit snapshots. The panel makes the display-spoofing vector real, which is why round 3 folds this fix in next to the panel.

Policy, taken from the followup text:

- **Caller-supplied label (`requested != nil`):** reject any rune for which `unicode.IsControl` is true, plus the bidi formatting characters U+202A to U+202E and U+2066 to U+2069, with a new sentinel `sshkey.ErrLabelInvalidChars`. It maps to `400 invalid_input` with detail code `users.ssh_key_label_invalid` on field `label`. Run the check after the existing raw-byte bound, so oversized input is still rejected cheaply first. Whether to check before or after `TrimSpace` does not matter for correctness: a leading or trailing tab or newline is a control character, but `TrimSpace` removes it first. The implementer should choose the order that keeps "  my key\n" valid, matching today's trim behavior, and document it in a test.
- **Comment-derived default (`requested == nil`):** strip those runes instead of rejecting, then trim and truncate as today. A key whose comment contains them still registers.
- **Detail-code name:** `users.ssh_key_label_invalid`, chosen to parallel the existing `users.ssh_key_invalid`. No central detail-code registry exists; codes are literals in `api/internal/service/ssh_keys.go` (`mapSSHKeyLabelError`) and are documented in `api/openapi.yaml` and `docs/mod-users-spec.md`.
- **Existing stored rows are not rewritten.** There is no data migration. The panel's `<bdi>` rendering covers display of any legacy row.

## `mod_users` schema operations note (`AWmf`)

The followup asks for a sentence in `docs/architecture.md` about two things. First, a composing host's runtime role needs `USAGE` and table privileges on `mod_users`. Second, backup, reset, or test tooling scoped to `public` will miss `mod_users.ssh_public_keys`. Planning-time state:

- `docs/architecture/ssh-keys.md` D1's "Operational consequence" paragraph already states both points. It was written by the `ssh-public-keys` plan's doc-updates task (`afee1a1`, the same day `AWmf` was filed).
- `docs/architecture.md`'s Data model section only points at D1 ("including the operational consequence for ... its backup/reset/test tooling"). It does not state the requirement itself. Its Key decisions D1 bullet is likewise only a pointer.
- Neither document separates the **read** privilege a host needs just to resolve SSH keys from the **write** privileges it needs only if it serves the register and revoke routes. A host that serves only the resolver needs `USAGE` on `mod_users` plus `SELECT` on `mod_users.ssh_public_keys`. It also needs `SELECT` on the two `public` tables the resolver reads. `ResolveActiveSSHPublicKey` (`model/queries/ssh_public_keys.sql`) joins `public.user_accounts`. `SSHKeyResolver.ResolveActor` (`api/internal/service/ssh_key_resolver.go`) then reads the account holder from `public.entities` with mod-core's `GetEntityByID`, to exclude an archived holder. Round 3 shows this split matters: in MFManager managed mode a composed app gets `SELECT`-only access to canonical identity data and has no write path, so a doc that says only "the usual table privileges" is ambiguous for that host.
- **mod-users' own tooling audit (planning-time grep, to be re-run by the task):** no backup, reset, truncate, or schema-enumeration tooling in `scripts/`, `Makefile`, `deploy/`, or the Go integration tests enumerates tables by scanning `public`. `api/internal/authz/ssh_keys_integration_test.go` already queries `mod_users.ssh_public_keys` schema-qualified. `make clean` removes the whole local database volume, so it is schema-agnostic. The round-4 review (`app-mfgit-managed-mode-r1-schema-review.md`, "Operational tooling blind spots") reports the same result for `app-mfmanager`, with no blanket public-only reset routine.

The task therefore states the requirement directly in `docs/architecture.md`, splits read from write privileges, adds a short checklist for composing hosts' tooling, and records mod-users' own audit result. It does not describe how MFManager grants privileges: that belongs to app-mfmanager's own plan-group in this wave. Describing it here would document behavior that has not landed.
