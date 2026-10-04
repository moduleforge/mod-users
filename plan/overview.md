# mfgit-upstream-modules (mod-users)

## Purpose and scope

This plan is mod-users' slice of the `mfgit-upstream-modules` plan-group. The group is the first, critical-path plan-group of the `app-mfgit-managed-launch` wave program (lead project: app-mfmanager). The group is federated across mod-users (this plan) and mod-repos (a sibling plan with the same slug). It lands the upstream module changes that later plan-groups build on: app-mfgit's own managed-mode port and MFManager's platform and catalog work.

mod-users' backend for SSH public keys is already shipped (migration `0103`, `SSHKeyService`/`SSHKeyResolver`, `/v1/self/ssh-keys`, merged 2026-09-28). The slice adds the missing UI and closes two followups the shipped work left behind:

1. **A users-gui `SSHKeysPanel`** that lists, registers, and revokes the caller's own SSH public keys against `/v1/self/ssh-keys`. It must handle the `users.email_unverified` and `users.step_up_required` action-required responses. It must use the library's existing runtime base-URL mechanism, so the same component works when app-mfgit serves it (standalone mode) and when MFManager serves it (managed mode). Neither host integration is part of this plan; both belong to later plan-groups.
2. **Label hardening (followup `GrC7`)**: reject control and bidi-formatting characters in caller-supplied SSH-key labels, and strip them from comment-derived default labels. It is in scope because the panel renders labels, which turns the stored-bidi display-spoofing vector into a real one. The cross-project review (round 3) folds the fix in next to the panel.
3. **`mod_users` schema operations note (followup `AWmf`)**: state directly in `docs/architecture.md` what a composing host's runtime database role needs on `mod_users`, separating read from write, and that `public`-scoped backup, reset, or test tooling misses `mod_users.ssh_public_keys`. Record mod-users' own tooling audit result.

What must not change:

- The SSH-key HTTP API shapes and status codes, except the one new `400` detail code (`users.ssh_key_label_invalid`).
- The behavior of every existing users-gui component and export.
- `request()`'s base-URL, token, and 401 behavior: the panel only adds methods that call `request()`.
- Operator routes (`/v1/user-accounts/{uuid}/ssh-keys`): the panel is self-service only.
- Any project outside mod-users.

Success criteria:

- `SSHKeysPanel` is exported from `@moduleforge/users-gui`, with tests and a Ladle story.
- The panel lists, registers, and revokes keys, showing label, key type, fingerprint, and created date.
- It completes a step-up challenge inline when the server requires one.
- It delegates other action-required responses (notably email verification) to the host through a callback, with an inline fallback notice.
- It contains no base-URL literal or env read of its own.
- Label validation rejects NUL, other control characters, and bidi overrides with a `400` instead of a `500`.
- `docs/architecture.md` states the `mod_users` grant and tooling requirement.
- `gui` typecheck and tests and the `api` unit tests pass.

Constraints:

- The resolved scope from the four cross-project research rounds is settled. Round 3 is `/Users/zane/playground/moduleforge/.flow/app-mfgit-managed-mode-n1-n3-q6-review.md`, "Scope of the N1 work"; round 4 is `app-mfgit-managed-mode-r1-schema-review.md`, which keeps `mod_users` in its own per-schema grant model.
- `gui/src/lib/api.ts` is also edited by the in-flight `home-app-switcher` wave's `users-gui-integration-seams` plan (task `001-runtime-config-base-url-and-token-key` and others). This is a soft coordination risk, not a dependency. See [the design note's coordination section](./notes/ssh-keys-panel-design.md#coordination-with-users-gui-integration-seams).

## Current status

Planned, not started. Phase 1 tasks `001`, `002`, and `004` have no preconditions beyond the toolchain in `AGENTS.md`: Go plus `make preflight` for the worktree sibling links, and `bun` plus a built `mod-core/gui`, including that file's documented worktree-symlink gap. Task `003` starts after `002`. At planning time (mod-users `main` at `4ae3641`), `users-gui-integration-seams` had not started any task, so `gui/src/lib/api.ts` still carries the original load-time `API_BASE_URL` singleton.

## Overview

Wire shapes, design decisions, and the planning-time audit are in [the SSH keys panel design note](./notes/ssh-keys-panel-design.md). Decisions the manager may want to review before execution:

- **Step-up is completed inline in the panel.** It requests a code, verifies it with `skipAuthRedirect`, and retries the pending operation once with `X-Step-Up-Token`. No `/step-up` page exists, and `users-gui-integration-seams` explicitly declined to build one, so delegating would leave key management unusable wherever `AUTH_REQUIRE_STEP_UP` is on. That includes MFManager, the managed-mode account home.
- **`users.email_unverified` and other actions are delegated** to an optional `onActionRequired` callback, with an inline fallback notice. The seams plan is building the standard `VerifyEmailPage`, so the panel does not duplicate it.
- **The base URL comes from the shared `api` singleton,** following every existing component, with no new base-URL or client prop.
- **The new label detail code is `users.ssh_key_label_invalid`.** Existing stored labels are not rewritten; the panel isolates label text with `<bdi>`.

### Phase 1: `ssh-keys-panel-and-hardening` (4 tasks)

- `001-ssh-key-label-hardening` (sonnet-med): `GrC7`. Adds the label content rule in `api/internal/sshkey/label.go`, the new sentinel and its detail-code mapping in `api/internal/service/ssh_keys.go`, the `api/openapi.yaml` text on both register endpoints, and table-driven tests. Go only; independent.
- `002-ssh-keys-client-methods` (sonnet-med): adds `SSHKey` and related types plus the `sshKeys` (list, register, revoke) and `stepUp` (request, verify) groups to `createUsersClient` in `gui/src/lib/api.ts`, exports them from `gui/src/index.ts`, and adds `api.test.ts` cases. It carries the `api.ts` coordination check and rebase note. Independent.
- `003-ssh-keys-panel` (sonnet-high): the `SSHKeysPanel` component with the inline step-up challenge, action delegation, field-error mapping, revoke confirmation, and `<bdi>` label rendering, plus component tests, a Ladle story, and the export. Depends on `002`.
- `004-mod-users-schema-ops-note` (sonnet-low): `AWmf`. Re-runs the tooling audit and states the `mod_users` read and write privilege requirement and the `public`-only tooling blind spot in `docs/architecture.md` (Data model and Key decisions D1), with a matching touch to `docs/architecture/ssh-keys.md` D1 if needed. Docs only; independent.

Parallel-eligible: `001`, `002`, and `004` can run concurrently, and `003` can run alongside `001` and `004` once `002` lands. `001` changes server copy that `003` displays, but `003` renders field-error details generically and only adds friendly copy for the new code, so there is no hard dependency. `004` and the phase-2 doc task both edit `docs/architecture.md`, which is why the doc task runs in the next phase.

### Phase 2: `doc-updates` (1 task, after phase 1)

- `001-update-architecture-docs` (sonnet-high, architect-frontend): reflects the new public `SSHKeysPanel` component and the new label rule and detail code in `docs/architecture.md` (GUI component library), `docs/mod-users-spec.md` (use case 14 and 16 and the Security requirements algorithm-policy bullet), and `docs/project-structure.md` if it lists components. It is registered because the plan adds a public component and changes spec-defined label validation.

### Followups

- `GrC7` and `AWmf` are resolved by tasks `001` and `004` (manager action at merge).
- `u353` (no GUI step-up navigation wiring) stays open. The panel becomes the first GUI call site for `users.step_up_required` but handles it inline, not through `AuthProvider` navigation.
- No new followups were filed during planning.
