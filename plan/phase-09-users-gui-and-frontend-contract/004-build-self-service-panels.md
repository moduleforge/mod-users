# Build Self Service Panels

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Build the account self-service panels of `@moduleforge/users-gui` on `/v1/me` and `/v1/session`:
credentials, identifiers and sessions panels, the step-up prompt that satisfies
`409 users.step_up_required`, and guest upgrade. The credentials panel replaces
`ssh-keys-panel.tsx` (SSH keys become a credential kind). Suggested role:
`plugins/flow/roles/developer-react.md`. No standard skill covers this task.

Note: the GUI has no identities panel today (only `ssh-keys-panel.tsx`); the identifiers panel is
new, and the "identities panel" named in the phase summary corresponds to the removed
`/v1/self/identities*` routes.

Depends on task 001 (`rebuild-api-client-and-auth-context`). Parallel-eligible with tasks 002, 003
and 005.

## Requirements

1. **Step-up prompt** (new component, for example `gui/src/components/step-up-dialog.tsx`):
   implements the step-up handler interface task 001 exports and registers it with the auth context.
   Offers the factors the API supports — password (`POST /v1/session/step-up/password`), email code
   (`/step-up/email-code/request` and `/verify`) and OIDC (`/step-up/oidc/{provider}/start`, redirect
   to `authorize_url`) — and resolves with the fresh access token so task 001's client retries the
   original request. Carry over the existing code-length and resend-cooldown behavior from
   `ssh-keys-panel.tsx`. No step-up token is held anywhere (U8).
2. **Credentials panel** (new component, for example `credentials-panel.tsx`): lists
   `GET /v1/me/credentials` by kind (password, email OTP, OIDC, SSH key, others the API returns);
   add password, add SSH key (reuse the validation and fingerprint display from
   `ssh-keys-panel.tsx`), link an OIDC provider (`POST /v1/me/credentials/oidc/{provider}/start`),
   edit `label` and `default_profile_uuid` (`PATCH`), and revoke (`DELETE`) with a clear message for
   `409 users.last_credential`. Revoking a credential may end sessions using it; reflect that.
3. **Identifiers panel** (new component, for example `identifiers-panel.tsx`): list, add, send
   verification, verify, set primary and delete identifiers through `/v1/me/identifiers*`; map the
   index-mapped `409` codes (duplicate identifier) to readable messages.
4. **Sessions panel** (new component, for example `sessions-panel.tsx`): list
   `GET /v1/me/sessions` with the current session marked and revoke others
   (`DELETE /v1/me/sessions/{uuid}`); revoking the current session signs the tab out.
5. **Guest upgrade** (new component or a mode of the credentials panel): when the session's
   principal is a guest, offer `POST /v1/me/upgrade` and handle `409 users.guest_upgrade_required`
   wherever the API returns it.
6. **Removal**: delete `gui/src/components/ssh-keys-panel.tsx`, `ssh-keys-panel.test.tsx`,
   `gui/src/stories/SSHKeysPanel.stories.tsx` and the `SSHKeysPanel` exports.
7. **Exports and stories**: export the new panels, the step-up dialog and their props from
   `gui/src/index.ts` (own section only); add stories for each panel (including the step-up prompt
   and the last-credential error).
8. **Tests**: step-up retry end to end with a mocked client (409, prompt, verify, retry once);
   each panel's list, add, edit and delete paths; `users.last_credential`; current-session revoke
   signing out; guest upgrade.
9. **`gui/README.md`**: replace the SSH keys panel section with the new panels and the step-up
   prompt, including how a host app mounts them.

## Validation

- `make -C gui test` passes for the new tests.
- `make -C gui lint` reports no errors in the files this task owns.
- `make -C gui build` succeeds, or fails only in files owned by tasks 002, 003 or 005 (list them).
- `test ! -e gui/src/components/ssh-keys-panel.tsx` and
  `grep -rnE "SSHKeysPanel|api\.sshKeys|api\.stepUp|stepUpToken" gui/src` returns nothing.

## Metadata

architectural_impact: false

## Assumptions

- The sibling `../mod-core/gui` (bun workspace member, pointed at mod-core's plan worktree by
  `scripts/link-siblings.sh`) is built with `bun run build` before `make -C gui build`/`lint` (see
  `.claude/CLAUDE.md`).
- Task 001 is merged; use its client, step-up handler interface and route constants. If a needed
  value is missing, add it in a minimal, additive way and state it in the report.
- Self-service writes are gated server-side by `AUTH_SELF_SERVICE_MAX_AUTH_AGE`; the GUI never
  pre-checks `auth_time`, it reacts to `409 users.step_up_required`.
- Request and response shapes follow the merged `/v1/me` handlers and `api/openapi.yaml`.

## References

- [API surface section 7](../notes/mod-users-api-surface.md#7-http-api) — `/v1/me` and
  `/v1/session` routes and error codes.
- [Decisions U8 and U12](../notes/decisions-and-resolutions.md) — step-up as factor composition;
  email OTP credentials created on verification.
- [Work packages, phase `users-gui-and-frontend-contract` item 3](../notes/work-packages.md).
- `gui/src/components/ssh-keys-panel.tsx` — existing key validation, step-up UX and cooldown to
  reuse.

## Checkpoint hints

- After the step-up dialog and its retry test.
- After the credentials panel and the `ssh-keys-panel` removal.
- After the identifiers and sessions panels.
- After guest upgrade, stories, exports and `gui/README.md`.
