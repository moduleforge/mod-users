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
