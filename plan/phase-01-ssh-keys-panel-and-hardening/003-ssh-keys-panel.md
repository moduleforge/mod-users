# SSH Keys Panel

## Purpose and scope

Build `SSHKeysPanel`, the users-gui component that lists, registers, and revokes the signed-in user's own SSH public keys against the already-shipped `/v1/self/ssh-keys` API. It is the shared UI for account credentials in both deployment modes. Standalone app-mfgit mounts it on its own settings page; MFManager, the managed-mode account home, mounts it on an account page. Both host integrations are later plan-groups, not this task. Scope: a new `gui/src/components/ssh-keys-panel.tsx`, its test, a Ladle story in `gui/src/stories/`, and its export from `gui/src/index.ts`. It uses the client methods from task `002`. No backend or doc change. No standard skill covers this; implement directly.

## Requirements

1. **Base URL: inherit, never configure.** Call only the shared singleton `api` from `../lib/api` (`api.sshKeys.*`, `api.stepUp.*`), exactly as `ForgotPasswordPage`, `LoginForm`, and `AuthProvider` do. Do not add a base-URL or client prop, do not read `process.env` or `window.__USERS_API_URL__`, and do not write any URL literal. The panel then inherits whatever runtime base-URL mechanism the library has: today the `NEXT_PUBLIC_API_BASE_URL` / `window.__USERS_API_URL__` singleton, and after `users-gui-integration-seams` task 001 merges, `configureUsersApi`. See [the design note's base URL section](../notes/ssh-keys-panel-design.md#decision-base-url-comes-from-the-shared-client-not-from-the-panel).
2. **Props:** export `interface SSHKeysPanelProps`, all fields optional:
   - `onActionRequired?: (error: ApiActionRequiredError) => void` receives every action-required error except `users.step_up_required` (requirement 6). The host navigates to `error.path`, for example `/verify-email`.
   - `title?: string`, default "SSH keys".
   - `description?: string`, with a default line explaining that these keys authenticate git operations over SSH.
3. **Shell and primitives:** a `'use client';` file, rendered as a core-gui `Card` panel without the full-page centering wrappers the auth pages use, so a host can embed it in a settings page. Use core-gui `Button`, `Input`, `Label`, `Badge`, `Alert`, and `FieldError`; the library's `Dialog` (`gui/src/components/ui/dialog.tsx`) and `Table` (`gui/src/components/ui/table.tsx`); and the existing `ErrorMessage`. Core-gui has no Textarea, so use a native `<textarea>` styled to match core-gui's `Input` (same border, radius, focus-ring, and disabled classes), with an associated `<Label htmlFor>`.
4. **List:** on mount, call `api.sshKeys.list({ limit: 200 })`.
   - Show a loading state, an empty state ("No SSH keys yet."), and a load-error state with a Retry button.
   - Show one row per key with:
     - the label rendered inside `<bdi>`, or "Untitled key" when the label is empty;
     - `key_type` as a `Badge`;
     - `fingerprint` in monospace (`font-mono`, wrapping or truncating gracefully with the full value in `title`);
     - `created_at` formatted with `toLocaleDateString()` and the raw ISO string in a `title` attribute;
     - a Revoke button labeled for assistive technology, for example `aria-label="Revoke <label or fingerprint>"`.
   - When `total > items.length`, show "Showing N of M keys".
   - Listing is allowed for unverified accounts. Route a list-time `ApiActionRequiredError` through the same delegation as requirement 7.
5. **Add key:** a form with the public-key textarea (required, `autoComplete="off"`, `spellCheck={false}`, a placeholder like `ssh-ed25519 AAAA... you@example.com`), an optional label `Input` (`maxLength={100}`), and a submit button. Disable the button while the request is pending or the key field is blank after trimming.
   - Send the key line trimmed. Send `label` only when the trimmed input is non-empty; otherwise omit it so the server defaults it from the key comment.
   - On `201`, clear the form, show a short success message, and reload the list. Do not insert the key optimistically.
   - On `ApiRequestError`, map `details` entries to fields: `public_key` details to the textarea's `FieldError` and `label` details to the label's. Otherwise show the error message. Use friendly copy for the known detail codes and fall back to the server message for unknown codes:
     - `users.ssh_key_in_use`: the key is already registered (do not suggest which account has it);
     - `users.ssh_key_invalid`: paste exactly one public key line, without options;
     - `users.ssh_key_type_unsupported`: name the accepted types (Ed25519, FIDO `sk-` keys, ECDSA, RSA of at least 2048 bits);
     - `users.ssh_key_too_weak`;
     - `users.ssh_key_label_too_long`;
     - `users.ssh_key_label_invalid`: the label contains control or bidi characters. This code is added by task `001`; display it even if `001` has not merged yet.
6. **Step-up, completed inline.** When register or revoke rejects with `ApiActionRequiredError` and `code === 'users.step_up_required'`, follow [the design note's step-up decision](../notes/ssh-keys-panel-design.md#decision-step-up-is-completed-inline-in-the-panel):
   - Keep the pending operation: the submitted key and label values, or the key being revoked. Show an inline challenge explaining that a verification code will be emailed, with a "Send code" button. Call `api.stepUp.request()` only on that click, never automatically. After it is sent, show a 6-character code `Input` (`inputMode="numeric"`, `autoComplete="one-time-code"`), a "Verify and continue" button, a "Resend code" button with a 30-second cooldown, and Cancel.
   - On verify, call `api.stepUp.verify(code)`, which passes `skipAuthRedirect` (task `002`). A rejection with `ApiRequestError` status `401` shows "That code is wrong or has expired." inline and keeps the challenge open. The user must stay signed in, with no navigation.
   - On success, retry the pending operation exactly once with `{ stepUpToken }`, then drop the token from state whatever the outcome. The server consumes the token at verification, before the mutation runs. A failed retry, such as a `400` on a malformed key, shows its error normally, and the next attempt goes through step-up again. Never cache, reuse, log, or render the token.
   - Cancel discards the pending operation, the code, and the token.
   - If the retry itself returns `users.step_up_required` again, show the challenge again rather than looping automatically.
7. **Other action-required responses:** for any other `ApiActionRequiredError` code (notably `users.email_unverified`, `403`, path `/verify-email`), call `onActionRequired(error)` when provided. Always also show an inline `Alert` built from `error.message`. For `users.email_unverified`, the alert says that SSH keys can be added or removed only after the email address is verified. Never navigate, and never call `window.location` or a router, inside the panel.
8. **Revoke:** the Revoke button opens a confirmation `Dialog` showing the key's label (in `<bdi>`) and fingerprint, and saying that the key stops working for new SSH connections. On confirm, call `api.sshKeys.revoke(uuid)`.
   - On `204`, close the dialog and reload the list.
   - A plain `403` `ApiRequestError` (the server's masked not-found-or-not-permitted outcome) shows "This key could not be revoked. It may already have been removed." and reloads the list.
   - Step-up and action handling follow requirements 6 and 7.
9. **Export:** from `gui/src/index.ts`, add `export { SSHKeysPanel } from './components/ssh-keys-panel';` and `export type { SSHKeysPanelProps } from './components/ssh-keys-panel';`, appended after the existing component exports so the line stays append-only against the seams plan's own index additions.
10. **Story:** `gui/src/stories/SSHKeysPanel.stories.tsx`. Follow the existing stories' convention of documenting that live calls fail with `network_error` without a running API. Also add stories that stub `globalThis.fetch` inside the story to show four states: populated list, empty list, a step-up challenge (the stub answers the first `POST` with the `users.step_up_required` action), and email-unverified. Restore `fetch` on story unmount. If stubbing inside Ladle proves impractical, keep only the default story and say so in the task report.
11. **Tests** in `gui/src/components/ssh-keys-panel.test.tsx`, using bun test with happy-dom and Testing Library (`gui/bunfig.toml` preloads), stubbing `globalThis.fetch` per test and restoring it, along with `localStorage` and `window.location`, in `afterEach`:
    - List renders label, type, fingerprint, and date; the empty state; "Showing N of M" when `total` exceeds the items; a label containing U+202E renders inside a `bdi` element.
    - Register success: the request body carries the trimmed key, and `label` is omitted when blank; the form clears and the list reloads.
    - Register `409 users.ssh_key_in_use` and `400 users.ssh_key_type_unsupported` render field errors on the key field; `400 users.ssh_key_label_invalid` renders on the label field.
    - Step-up: the first `POST` gets the `409` step-up action, then "Send code" calls `/v1/self/credential/step-up`, then verify gets `{step_up_token: 't1', expires_in: 300}`, then the retried `POST` carries `X-Step-Up-Token: t1` and succeeds. A later register sends no step-up header.
    - Step-up wrong code: verify returns `401`, the inline error shows, `localStorage.auth_token` is still present, and no navigation happens.
    - Step-up cancel: no retry request is sent.
    - Email unverified on register: `onActionRequired` is called once with an `ApiActionRequiredError` whose `path` is `/verify-email`, and the inline alert renders. With no callback, the alert still renders and nothing throws.
    - Revoke: the confirm dialog, then `DELETE` to the right URL, then the list reloads; a masked `403` shows the "could not be revoked" message.
    - Base URL: every request URL the panel issues starts with the shared client's configured base (import `API_BASE_URL`, or `getApiBaseUrl()` if seams 001 merged) and ends with the expected `/v1/...` path. If seams 001 has merged, add one case where `configureUsersApi({ baseUrl: '' })` makes the panel's list call go to a relative `/v1/self/ssh-keys`.

## Validation

- `cd gui && bun run typecheck && bun test` passes, including all pre-existing tests (needs `mod-core/gui` built; see `AGENTS.md`).
- `cd gui && bun run build` succeeds, and `grep -c "SSHKeysPanel" gui/dist/index.d.ts` is at least 1.
- `grep -nE "localhost|process\.env|__USERS_API_URL__|https?://" gui/src/components/ssh-keys-panel.tsx` returns nothing.
- `grep -n "stepUpToken\|step_up_token" gui/src/components/ssh-keys-panel.tsx` shows the token held only in component state or a local variable: no `localStorage`, `sessionStorage`, or `console` use.
- `grep -rn "window.location\|useRouter\|next/" gui/src/components/ssh-keys-panel.tsx` returns nothing (router-agnostic).
- `git diff --stat`, excluding `plan/`, touches only the new component, its test, the new story, and `gui/src/index.ts`.
- Manual check (recommended when a local stack is available): with `make dev.start` and a verified account, run `make preview` and use the panel's default story with `window.__USERS_API_URL__` or `NEXT_PUBLIC_API_BASE_URL` pointed at the API. Register an Ed25519 key, see its fingerprint, and revoke it. If `AUTH_REQUIRE_STEP_UP=true`, complete the emailed step-up through Mailpit. Note in the task report whether this was done.

## Metadata

architectural_impact: true

## Assumptions

- Task `002`'s `api.sshKeys` and `api.stepUp` methods and types exist on the branch.
- `users.ssh_key_label_invalid` is the detail code task `001` introduces. The panel's copy for it is harmless if `001` has not merged.
- Whether `users-gui-integration-seams` task 001 has merged changes only the extra base-URL test case. Check for `gui/src/lib/config.ts` at task start and record the result.

## References

- [The SSH keys panel design note](../notes/ssh-keys-panel-design.md): wire contract, step-up and delegation decisions, panel shape.
- `gui/src/components/forgot-password-page.tsx` and `gui/src/components/email-code-page.tsx`: existing patterns for components that call the API client directly.
- `gui/src/lib/auth-context.tsx`: existing `ApiActionRequiredError` handling.
- `gui/src/components/ui/dialog.tsx`, `gui/src/components/ui/table.tsx`, `gui/src/components/error-message.tsx`; core-gui `ui/` (`button`, `input`, `label`, `card`, `badge`, `alert`) and `FieldError`.
- `/Users/zane/playground/moduleforge/.flow/app-mfgit-managed-mode-n1-n3-q6-review.md`, "Scope of the N1 work", mod-users item 1: the cross-project requirement (read-only).

## Checkpoint hints

- After the list view and its tests
- After the add form with field-error mapping
- After the inline step-up flow and its tests
- After revoke, the story, and the export

## Status

- Outcome: succeeded (2026-10-04). Seams 001 case: `gui/src/lib/config.ts` did NOT exist on the base, so the panel inherits today's `API_BASE_URL` singleton and the `configureUsersApi({ baseUrl: '' })` test case was skipped. Task `001`'s `users.ssh_key_label_invalid` copy is included regardless.
- Validation: `tsc --noEmit` passes; `bun run build` succeeds and `dist/index.d.ts` mentions `SSHKeysPanel` (3 matches); the URL/env, token-storage, and router greps behave as required (the token appears only as a function parameter and a local variable; the only `console` use is `console.error` of caught unexpected errors). `bun test` fails identically on the unmodified base here (dangling `mod-core/gui/node_modules/*` symlinks, the known gap); run against a scratch copy of core-gui with its dependencies resolved, 67 pass / 0 fail (29 new). `ladle build` also succeeds with the new stories.
- Manual check against a live stack: not done (no local stack available to this agent).
- Decisions: the step-up challenge is a single inline section below the list; a revoke that hits step-up closes the confirmation dialog and holds the key. The form's submit button is disabled while a challenge is open. The inline `Alert` for `users.email_unverified` uses the fixed "verified" sentence; other action codes show `error.message`. A list-time action-required error also shows a generic load-error banner with Retry.
- Files: `gui/src/components/ssh-keys-panel.tsx`, `gui/src/components/ssh-keys-panel.test.tsx`, `gui/src/stories/SSHKeysPanel.stories.tsx`, `gui/src/index.ts`.
