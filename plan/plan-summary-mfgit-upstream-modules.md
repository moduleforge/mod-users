# Plan Summary: mfgit-upstream-modules

## What was planned and why

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

## What shipped

### Phase 01 — SSH Keys Panel and Label Hardening

1. **SSH Key Label Hardening** (`001-ssh-key-label-hardening.md`, tier `sonnet-med`) — Added control and bidi character rejection for supplied SSH key labels (stripping for comment-derived defaults), with the new users.ssh_key_label_invalid detail code, OpenAPI docs and tests. No surprises.
   Commit `0bf56dd`, merged at `8fd89b11581e4eadb7b67860acc2b163f471f86c`.

2. **SSH Keys Client Methods** (`002-ssh-keys-client-methods.md`, tier `sonnet-med`) — Added typed sshKeys and stepUp client groups, the SSH-key types, and the five type exports. All changes are additive hunks in api.ts that avoid the lines the seams plan rewrites. stepUp.verify uses skipAuthRedirect: true, with a regression test. Tests pass (38/38) only when core-gui resolves, which it does not in the worktree itself.
   Commit `0e8fe5c`, merged at `8c6aa6bf87ed9cd5b946398a145ad282fab86164`.

3. **SSH Keys Panel** (`003-ssh-keys-panel.md`, tier `sonnet-high`) — Built SSHKeysPanel (list, add with field-error mapping, revoke dialog, inline email-code step-up, host delegation of action-required errors) on the shared api singleton, plus 29 behavior tests, five Ladle stories, and an append-only export. Tests pass only with the environment workaround below.
   Commit `f01bde4`, merged at `db0c32838bc9c9881ed4d68288e187c2b3682e72`.

4. **Mod Users Schema Ops Note** (`004-mod-users-schema-ops-note.md`, tier `sonnet-low`) — Documented the mod_users privilege sets (read-only vs write) and the public-scoped tooling caveat in the architecture doc's Data model and D1. Aligned ssh-keys.md D1 with them. The audit found nothing to fix.
   Commit `1ebb059`, merged at `f63edaebda9e9302abf24e81b5052339479622f4`.

### Phase 02 — Documentation Updates

1. **Update Architecture Docs** (`001-update-architecture-docs.md`, tier `sonnet-high`) — Updated docs/architecture.md, docs/mod-users-spec.md, and docs/project-structure.md to describe the new public SSHKeysPanel component and its router-agnostic action delegation/inline step-up behavior, and to document the new users.ssh_key_label_invalid refusal and strip-from-comment rule (described in its current, phase-5-extended full-Bidi_Control-set form). docs/architecture/ssh-keys.md and AGENTS.md were reviewed and needed no change.
   Commit `464edd6`, merged at `ab0c248d8311b21d1b163889711ea3899a1a20be`.

### Phase 05 — Remediation Round 1

1. **Reject All Unicode Bidi Control Characters in SSH Key Labels** (`001-reject-all-bidi-control-label-chars.md`, tier `sonnet-low`) — Extended the SSH key label's bidi-rejection filter to cover Unicode's full Bidi_Control set (added U+061C, U+200E, U+200F) per the plan's stated scope, closing finding sQRd. Zero-width characters stayed allowed per the task's explicit carve-out. Table-driven tests cover all three new code points plus a ZWJ regression guard; all tests pass; diff scope matches exactly the two files the task specified.
   Commit `9da9374`, merged at `3f9fe6e53681499e0d59274cae611f40428f74fa`.

### Phase 06 — Remediation Round 2

1. **Correct SSHKeysPanel Action-Required And Ordinal Wording In Architecture Doc** (`001-fix-ssh-keys-panel-doc-wording.md`, tier `sonnet-low`) — Corrected two factual inaccuracies in docs/architecture.md's SSHKeysPanel description (a miscounted ordinal and a mischaracterized fallback-only alert) to match the merged ssh-keys-panel.tsx implementation, closing findings 3oGr and uNF5.
   Commit `f68883f`, merged at `9a195841f8959005f0f01a299b4d1d3f7ef87e7a`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Findings

- **`WAAG`** — **The gui half of make test.unit cannot pass in** — dismissed — ref: `YPQ2` — 2026-10-04 — reason: Duplicate of YPQ2, which carries the more specific cause/remedy and is being escalated.

- **`xcHA`** — **Environment gap (pre-existing, not caused by** — dismissed — ref: `YPQ2` — 2026-10-04 — reason: Duplicate of YPQ2, which carries the more specific cause/remedy and is being escalated.

- **`5ffL`** — **architecture.md omits SSHKeysPanel** — dismissed — ref: `phase-02-doc-updates/001-update-architecture-docs.md` — 2026-10-04 — reason: Already planned: covered by this plan's own pending task phase-02-doc-updates/001-update-architecture-docs.md, whose requirements add SSHKeysPanel to docs/architecture.md's GUI component library section and whose validation greps for it.

- **`Rvii`** — **plan/ docs orphaned, no policy exclusion** — promoted — ref: `Rvii` — 2026-10-04

- **`hlTS`** — **mf-standards submodule links dangle** — promoted — ref: `hlTS` — 2026-10-04

- **`124J`** — **Deploy/model docs unreachable from README** — promoted — ref: `124J` — 2026-10-04

- **`YPQ2`** — **Environment: mod-core/gui/node_modules/react** — promoted — ref: `YPQ2` — 2026-10-04

- **`KOQ4`** — **The recommended live-stack manual check (make** — promoted — ref: `KOQ4` — 2026-10-04

- **`sQRd`** — **Label spoof: weak bidi/ZW marks allowed** — dismissed — ref: `3f9fe6e53681499e0d59274cae611f40428f74fa` — 2026-10-04 — reason: Fixed by remediation task phase-05-remediation-01/001-reject-all-bidi-control-label-chars.md: isDisallowedLabelRune now covers Unicode's full Bidi_Control set (added U+061C, U+200E, U+200F), closing the gap this finding named. Verified independently by both the correctness lens and the full security lens at the remediation gate (security-001 noted only a bookkeeping reconciliation gap, not a code defect; the zero-width-character portion is a deliberate, documented out-of-scope carve-out, not part of this finding's Bidi_Control claim).

- **`dWOG`** — **sQRd closure not reconciled in store** — dismissed — 2026-10-04 — reason: Addressed: the manager has now explicitly closed sQRd (dismissed with a fixed-disposition reason citing the merge commit), resolving the bookkeeping gap this finding flagged.

- **`3oGr`** — **architecture.md misdescribes fallback alert** — dismissed — ref: `9a195841f8959005f0f01a299b4d1d3f7ef87e7a` — 2026-10-04 — reason: Fixed by remediation task phase-06-remediation-02/001-fix-ssh-keys-panel-doc-wording.md: docs/architecture.md now states the inline alert always shows and the onActionRequired callback is also invoked when supplied, matching handleActionRequired exactly. Independently verified by the round-2 gate's correctness lens.

- **`uNF5`** — **"sixth component" ordinal unclear** — dismissed — ref: `9a195841f8959005f0f01a299b4d1d3f7ef87e7a` — 2026-10-04 — reason: Fixed by remediation task phase-06-remediation-02/001-fix-ssh-keys-panel-doc-wording.md: docs/architecture.md now calls SSHKeysPanel "an additional component" rather than the miscounted "a sixth component." Independently verified by the round-2 gate's correctness lens.

## Remediation

- Rounds used: 2 (resolved max_rounds: 3).
- Remediation tasks added: 2 (resolved max_added_tasks: 10).
- Remediation phases:
  - `remediation-01` — 1 task(s)
  - `remediation-02` — 1 task(s)
- Security review required: yes (at least one remediation phase carries `security_review: required`).

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — SSH Keys Panel and Label Hardening

- [x] [001-ssh-key-label-hardening.md](./phase-01-ssh-keys-panel-and-hardening/001-ssh-key-label-hardening.md) — tier `sonnet-med` · branch `plan/mfgit-upstream-modules-01-001` · commit `0bf56dd` · merge `8fd89b11581e4eadb7b67860acc2b163f471f86c`
- [x] [002-ssh-keys-client-methods.md](./phase-01-ssh-keys-panel-and-hardening/002-ssh-keys-client-methods.md) — tier `sonnet-med` · branch `plan/mfgit-upstream-modules-01-002` · commit `0e8fe5c` · merge `8c6aa6bf87ed9cd5b946398a145ad282fab86164`
- [x] [003-ssh-keys-panel.md](./phase-01-ssh-keys-panel-and-hardening/003-ssh-keys-panel.md) — tier `sonnet-high` · branch `plan/mfgit-upstream-modules-01-003` · commit `f01bde4` · merge `db0c32838bc9c9881ed4d68288e187c2b3682e72`
- [x] [004-mod-users-schema-ops-note.md](./phase-01-ssh-keys-panel-and-hardening/004-mod-users-schema-ops-note.md) — tier `sonnet-low` · branch `plan/mfgit-upstream-modules-01-004` · commit `1ebb059` · merge `f63edaebda9e9302abf24e81b5052339479622f4`

### Phase 02 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-02-doc-updates/001-update-architecture-docs.md) — tier `sonnet-high` · branch `plan/mfgit-upstream-modules-02-001` · commit `464edd6` · merge `ab0c248d8311b21d1b163889711ea3899a1a20be`

### Phase 05 — Remediation Round 1

- [x] [001-reject-all-bidi-control-label-chars.md](./phase-05-remediation-01/001-reject-all-bidi-control-label-chars.md) — tier `sonnet-low` · branch `plan/mfgit-upstream-modules-05-001` · commit `9da9374` · merge `3f9fe6e53681499e0d59274cae611f40428f74fa`

### Phase 06 — Remediation Round 2

- [x] [001-fix-ssh-keys-panel-doc-wording.md](./phase-06-remediation-02/001-fix-ssh-keys-panel-doc-wording.md) — tier `sonnet-low` · branch `plan/mfgit-upstream-modules-06-001` · commit `f68883f` · merge `9a195841f8959005f0f01a299b4d1d3f7ef87e7a`
