# Review GUI Exports and Contract Docs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Close the phase: reconcile `gui/src/index.ts`, the Ladle stories and `gui/README.md` after tasks 001
to 005 merged in parallel, confirm the frontend contract is documented as implemented, sweep for
removed symbols, and bring the whole `gui/` sub-project green. Mostly a verification read with small
fixes. Suggested role: `plugins/flow/roles/developer-react.md`; `plugins/flow/roles/tech-writer.md`
for the README pass. No standard skill covers this task.

Depends on tasks 001 to 005 (hard dependency: it reviews their merged result).

## Requirements

1. **Exports**: `gui/src/index.ts` exports every public component, hook, type and helper added by
   tasks 001 to 005 exactly once, grouped by section, and nothing removed (`SSHKeysPanel`,
   `UserAccount*`, `StepUpVerifyResponse`, `StepUpOptions`, `completeExternalLogin`, `isAdmin`-based
   props). Resolve any merge duplication.
2. **README**: `gui/README.md` has one "Frontend contract" section matching
   [API surface section 12](../notes/mod-users-api-surface.md#12-frontend-contract-resolution-c) and
   the implemented behavior; its exports and components documentation matches `index.ts`; it lists
   the removed and renamed public symbols in one place for consumers (host apps such as
   app-mfmanager, app-mftodo); no text describes removed routes, `localStorage` tokens, step-up
   tokens, `isAdmin`, or the hard `/auth/login` navigation.
3. **Stories**: every exported component has a story or is consciously omitted (state which in the
   report); no story references removed symbols.
4. **Sweep**: `grep -rnE "/v1/self|/v1/user-accounts|/v1/assume|anonymous-demo|X-Step-Up-Token|step_up_token|localStorage|isAdmin|completeExternalLogin|SSHKeysPanel" gui/`
   (excluding `node_modules`, `dist`) returns only intentional hits (tests asserting absence);
   fix the rest or explain them.
5. **Followups**: confirm whether `HPJi` (hard 401 navigation) and `PRvD` (apps client body) are
   resolved by the merged code. Do not edit `plan/followups.yaml`: state the exact intended change
   (for example removal with the resolving commit) in the structured report for the manager to
   apply; the final phase's reconciliation task performs the removals.

## Validation

- `make -C gui build`, `make -C gui test` and `make -C gui lint` all pass.
- `cd gui && bun run preview:build` (Ladle) succeeds, or the report states why it could not run.
- The sweep in requirement 4 is recorded in the report with its result.

## Metadata

architectural_impact: false

## Assumptions

- Tasks 001 to 005 are merged into the phase branch.
- The sibling `../mod-core/gui` is built (`bun run build`) from mod-core's plan worktree before the
  `gui/` build and typecheck (see `.claude/CLAUDE.md`).
- Repository-wide documentation (`README.md`, `AGENTS.md`, `docs/`) is reviewed by the
  `doc-updates` phase, not here.

## References

- [API surface section 12](../notes/mod-users-api-surface.md#12-frontend-contract-resolution-c).
- [Work packages, phase `users-gui-and-frontend-contract`](../notes/work-packages.md).
- Task documents 001 to 005 in this phase directory.

## Status

succeeded, 2026-10-31. `make -C gui build`, `test` (421 pass) and `lint` pass; `bun run preview:build` passes. Sweep hits are all intentional (README migration notes, tests asserting `localStorage` is unused, comments on `/v1/self` 503). `gui/src/index.ts` needed no change (no duplicates, no removed symbols). Added an "Other exports" table to `gui/README.md`. Stories omitted by choice: `ClientLayout`, `ProfileLockNotice`, `ProfileRemedy`, `OidcSetupGate` (need provider or gate context). Followups `HPJi` and `PRvD` are resolved by the merged code.
