# Update Architecture Docs

## Purpose and scope

Update architecture and spec documentation to reflect the users-gui seams this plan adds: a public runtime-configuration API, a configurable unauthenticated handler, new component props, an exported `isSafeReturnPath`, the removed `./styles.css` export, and the new standard screens: `VerifyEmailPage` (the `/verify-email` action-required target), `OidcConfigPage` and `OidcSetupGate` (migrated from app-mfdemo; the `/oidc-config` target), the `USERS_GUI_ROUTES` constant, and opt-in login return-path handling.

## Requirements

- Implementation task docs that surfaced architectural implications (all paths relative to the plan worktree root; these will be complete when this phase runs):
  - `plan/phase-01-gui-seams/001-runtime-config-base-url-and-token-key.md`
  - `plan/phase-01-gui-seams/002-unauthenticated-handler.md`
  - `plan/phase-01-gui-seams/003-auth-component-props.md`
  - `plan/phase-01-gui-seams/005-verify-email-page-and-routes.md`
  - `plan/phase-01-gui-seams/006-oidc-config-page-migration.md`
  - `plan/phase-01-gui-seams/007-login-return-path-standard.md`
- Files to review and update where needed:
  - `docs/architecture.md` (the `gui/` paragraph near line 142: add the runtime config module, the 401 handling seam, the new props, the removed CSS export; keep the "no CSS bundled, consumers `@source`-scan `dist/`" statement accurate; document the standard-screens set and the backend-to-path contract: `users.email_unverified` -> `/verify-email` -> `VerifyEmailPage`, `users.oidc_not_confirmed` -> `/oidc-config` -> `OidcConfigPage`, with `AuthProvider` navigating via `onNavigate(err.path)`, and that `/step-up` has no page yet)
  - `docs/mod-users-spec.md` (GUI/consumer sections near line 158 and the Non-goals about routing: reflect that the library remains router-agnostic and owns no route files, but now ships page components for the backend-fixed paths (`/verify-email`, `/oidc-config`, `/reset-password`) and exports `USERS_GUI_ROUTES`; update any text that says mounting `/verify-email` is left to consuming apps, and the configuration surface)
  - `docs/project-structure.md` (the `gui/` description near lines 69-80: new `src/lib/config.ts`, `src/lib/return-path.ts`, `src/lib/routes.ts`, `src/components/{verify-email-page,oidc-config-page,oidc-provider-add-modal,oidc-provider-edit-modal,oidc-setup-gate}.tsx`, `gui/README.md`)
  - `AGENTS.md` (the `gui/` rows of the layout table and any guidance on `gui/src/lib`; mention `gui/README.md` as the integration guide)
- `role_doc: plugins/flow/roles/architect-frontend.md`
- Procedure: follow `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.
- Keep `gui/README.md` as the single detailed reference; the architecture docs summarize and link rather than duplicate field tables.

## Validation

- Each named file was reviewed; `grep -n "configureUsersApi" docs/architecture.md` and `grep -n "styles.css" docs/ AGENTS.md` show accurate (non-stale) statements, and no doc still claims a `./styles.css` export exists.
- `git diff --stat` shows only the named doc files changed.

## Status

- Outcome: succeeded (2026-10-04).
- Updated [docs/architecture.md](../../docs/architecture.md) (runtime config and 401 seam, standard screens and backend-to-path contract, no `./styles.css` export), [docs/mod-users-spec.md](../../docs/mod-users-spec.md) (use case 14 and the routing non-goal), [docs/project-structure.md](../../docs/project-structure.md) (new `gui/` files), and [AGENTS.md](../../AGENTS.md) (layout table); all link to [gui/README.md](../../gui/README.md) rather than duplicating it.
- Validation: `configureUsersApi` appears in `docs/architecture.md`; remaining `styles.css` mentions are the Ladle workbench entry or state that no export exists; diff touches only the four named doc files.
