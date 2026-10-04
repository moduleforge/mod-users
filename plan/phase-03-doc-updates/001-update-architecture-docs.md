# Update Architecture Docs

## Purpose and scope

Update architecture and spec documentation to reflect the users-gui seams this plan adds: a public runtime-configuration API, a configurable unauthenticated handler, new component props, an exported `isSafeReturnPath`, and the removed `./styles.css` export.

## Requirements

- Implementation task docs that surfaced architectural implications (all paths relative to the plan worktree root; these will be complete when this phase runs):
  - `plan/phase-01-gui-seams/001-runtime-config-base-url-and-token-key.md`
  - `plan/phase-01-gui-seams/002-unauthenticated-handler.md`
  - `plan/phase-01-gui-seams/003-auth-component-props.md`
- Files to review and update where needed:
  - `docs/architecture.md` (the `gui/` paragraph near line 142: add the runtime config module, the 401 handling seam, the new props, the removed CSS export; keep the "no CSS bundled, consumers `@source`-scan `dist/`" statement accurate)
  - `docs/mod-users-spec.md` (GUI/consumer sections near line 158 and the Non-goals about routing: reflect that the library remains router-agnostic and owns no routes, and the configuration surface)
  - `docs/project-structure.md` (the `gui/` description near lines 69-80: new `src/lib/config.ts`, `src/lib/return-path.ts`, `gui/README.md`)
  - `AGENTS.md` (the `gui/` rows of the layout table and any guidance on `gui/src/lib`; mention `gui/README.md` as the integration guide)
- `role_doc: plugins/flow/roles/architect-frontend.md`
- Procedure: follow `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.
- Keep `gui/README.md` as the single detailed reference; the architecture docs summarize and link rather than duplicate field tables.

## Validation

- Each named file was reviewed; `grep -n "configureUsersApi" docs/architecture.md` and `grep -n "styles.css" docs/ AGENTS.md` show accurate (non-stale) statements, and no doc still claims a `./styles.css` export exists.
- `git diff --stat` shows only the named doc files changed.
