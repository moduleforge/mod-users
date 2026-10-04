# Update Architecture Docs

## Purpose and scope

Update mod-users' architecture and specification docs to reflect the changes phase 1 of this plan implemented. Phase 1 added a new public users-gui component, `SSHKeysPanel`, with an inline step-up challenge and new client methods. It also changed spec-defined SSH-key label validation: a new `400` detail code, `users.ssh_key_label_invalid`, and comment-derived labels now strip control and bidi characters. Follow the `update-architecture-docs` task-procedure at `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.

## Requirements

- **Task documents that surfaced the architectural implications** (paths relative to the plan worktree; all complete by the time this phase runs):
  - `plan/phase-01-ssh-keys-panel-and-hardening/001-ssh-key-label-hardening.md` changes spec-defined label validation and adds detail code `users.ssh_key_label_invalid`.
  - `plan/phase-01-ssh-keys-panel-and-hardening/002-ssh-keys-client-methods.md` adds public client methods and types (`sshKeys`, `stepUp`, `SSHKey`, and others) to `@moduleforge/users-gui`.
  - `plan/phase-01-ssh-keys-panel-and-hardening/003-ssh-keys-panel.md` adds the new public component `SSHKeysPanel`, the first users-gui call site for `users.step_up_required`, completed inline.
- **Files to review and update where needed:**
  - `docs/architecture.md`, GUI component library section. Add `SSHKeysPanel` to the component inventory, with its router-agnostic `onActionRequired` delegation and inline step-up behavior. Note that it uses the shared client's runtime base URL and therefore works in any host. The section's sentence "Profile management is not yet part of this surface" must stay accurate; rephrase it if the SSH keys panel makes it misleading. Do **not** edit the Data model section's `mod_users` grant text: task `004` of phase 1 owns it, and it is already merged.
  - `docs/mod-users-spec.md`:
    - use case 14 (GUI component rendering) should mention the SSH-key management surface;
    - use case 16 (self-service SSH keys) should note that a standard UI exists;
    - the Security requirements "Algorithm policy" bullet should list the new `users.ssh_key_label_invalid` refusal and the strip-from-comment rule, matching `api/openapi.yaml` as changed by task `001`.
  - `docs/project-structure.md`: its `gui/src/components/` description ("auth flows, profile, admin views") should cover account-credential components if that is now inaccurate.
  - `docs/architecture/ssh-keys.md`: review only. Add a one-line pointer to the GUI component if its scope statement says that no UI exists. Do not restate decisions.
  - `AGENTS.md`: review only. No change is expected unless a build or test convention changed.
- `role_doc: plugins/flow/roles/architect-frontend.md`. The primary implication is a new public frontend component and client surface; the label rule is a small spec-level addition.
- Procedure: the `update-architecture-docs` task-procedure, `plugins/flow/task-procedures/update-architecture-docs/SKILL.md` (Flow plugin root).

## Validation

- `grep -n "SSHKeysPanel" docs/architecture.md` returns at least one hit in the GUI component library section.
- `grep -n "users.ssh_key_label_invalid" docs/mod-users-spec.md` returns a hit in the Security requirements section.
- The task report lists each file named above as reviewed, with the change made or "no change needed", and why.
- Every claim added matches the merged code: `gui/src/components/ssh-keys-panel.tsx`, `gui/src/lib/api.ts`, `api/internal/sshkey/label.go`, `api/openapi.yaml`.
- `git diff --stat`, excluding `plan/`, touches only files under `docs/`, plus `AGENTS.md` only if justified.

## Status

- Outcome: succeeded (2026-10-04).
- Files reviewed and their disposition:
  - `docs/architecture.md` (GUI component library section) — **changed**. Added a sentence inventorying `SSHKeysPanel` as a sixth, non-full-page component: its base-URL inheritance (`api.sshKeys.*`/`api.stepUp.*`, no prop/env of its own), its inline `users.step_up_required` completion, and its `onActionRequired` delegation for every other action-required code. Rephrased "Profile management is not yet part of this surface" to "Profile editing and other account self-service beyond SSH-key credential management is not yet part of this surface," since the prior wording was no longer accurate once a self-service credential-management component existed. Did not touch the Data model section's `mod_users` grant text (owned by phase-1 task `004`, already merged).
  - `docs/mod-users-spec.md` — **changed**, three spots:
    - Use case 14 outcome: added "SSH-key management" to the list of surfaces the GUI components render.
    - Use case 16 outcome: added a sentence naming `SSHKeysPanel` as the standard UI for the self-service flow, cross-linking to use case 14, and noting direct API use remains supported.
    - Security requirements "Algorithm policy" bullet: added the `users.ssh_key_label_invalid` refusal for a supplied label containing a control or Unicode bidi-formatting (`Bidi_Control`) character, and the strip-not-reject rule for a comment-derived default label. Described the rule generically (matching `api/openapi.yaml`'s own wording) rather than enumerating specific code points, since `isDisallowedLabelRune` now covers the full `Bidi_Control` set (extended past phase 1's subset by this plan's phase-5 remediation task `001-reject-all-bidi-control-label-chars.md`) and an enumerated list would go stale again on the next such extension.
  - `docs/project-structure.md` — **changed**. `gui/src/components/` description extended from "(auth flows, profile, admin views)" to "(auth flows, account credentials, profile, admin views)" to cover the new credential-management component; left "profile" and "admin" as-is (pre-existing, out of this task's scope).
  - `docs/architecture/ssh-keys.md` — **reviewed, no change**. Its `## Purpose and scope` and D1 text describe the SSH-key capability and its operational consequences but never assert that no UI exists, so the conditional pointer the task doc anticipated does not apply.
  - `AGENTS.md` — **reviewed, no change**. No build or test convention changed; its one `gui/src/components/` reference is a generic one-line table entry ("React UI components") that needed no edit.
- Validation: `grep -n "SSHKeysPanel" docs/architecture.md` hits in the GUI component library section; `grep -n "users.ssh_key_label_invalid" docs/mod-users-spec.md` hits in the Security requirements section; `git diff --stat` (excluding `plan/`) touches only `docs/architecture.md`, `docs/mod-users-spec.md`, and `docs/project-structure.md`. Every claim added was checked against `gui/src/components/ssh-keys-panel.tsx`, `gui/src/lib/api.ts`, `api/internal/sshkey/label.go` (including the phase-5-remediated `isDisallowedLabelRune`), and `api/openapi.yaml`'s label-field and endpoint-description text before writing it.
- Files: `docs/architecture.md`, `docs/mod-users-spec.md`, `docs/project-structure.md`, `plan/phase-02-doc-updates/001-update-architecture-docs.md`.
