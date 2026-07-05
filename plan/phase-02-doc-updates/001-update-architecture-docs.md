# Update Architecture Docs

## Purpose and scope

Update the project's architecture and specification documentation to reflect the Login/Register UI components added by Phase 1 (`auth-ui-components`) of this plan. Invoke the `update-architecture-docs` task-procedure (`plugins/flow/task-procedures/update-architecture-docs/SKILL.md`) to perform this review and update.

role_doc: references/roles/architect-frontend.md

## Requirements

Phase 1's task documents surfaced the architectural implication for this phase — each adds new components to `gui/`'s public export surface (a documented component boundary in `docs/architecture.md`'s "GUI component library" section):

- `plan/phase-01-auth-ui-components/001-login-register-forms.md` — adds `LoginForm` and `RegisterForm`.
- `plan/phase-01-auth-ui-components/002-oidc-callback-page.md` — adds `OidcCallbackPage`.
- `plan/phase-01-auth-ui-components/003-auth-page.md` — adds `AuthPage`.

By the time this phase runs, all three will be implemented, exported from `gui/src/index.ts`, and merged.

Review and update where warranted:

- **`docs/architecture.md`** — the "GUI component library" section currently describes `gui/` as providing "ready-made components for registration, login (all channels), password reset, and profile management" at a summary level, without naming individual components. Confirm this summary still reads accurately now that `LoginForm`, `RegisterForm`, `AuthPage`, and `OidcCallbackPage` concretely exist, and consider whether naming them (or the pattern they establish — self-contained components that call `useAuth()`/API helpers directly, plus router-agnostic callback-injection props for page-level components) adds clarity for a reader trying to understand what the library exposes. Do not overstate: this plan does **not** add email-code, forgot-password, or reset-password UI — if the summary text could be read as claiming those exist, correct it.
- **`docs/mod-users-spec.md`** (the single file matching `docs/*-spec.md`) — use case 13 ("GUI component rendering and demo app") describes the GUI package at a similarly high level. Confirm it still reads accurately; update if it implies more (or less) than what Phase 1 delivered.
- Confirm no other `docs/*.md` file makes claims about the GUI package's component inventory that are now stale.

Do not describe `AuthPage`/`OidcCallbackPage`/`LoginForm`/`RegisterForm`'s internal prop APIs in `docs/architecture.md` — that level of detail belongs in the components' own doc comments (already present in the source, per Phase 1's task documents), not in the architecture doc, which stays at the subsystem/capability level.

## Validation

- `docs/architecture.md`'s "GUI component library" section was read and, if updated, accurately reflects that Login/Register UI components (form + page level) now exist, without overstating scope into email-code/forgot-password/reset flows that remain unimplemented.
- `docs/mod-users-spec.md`'s use case 13 was read and, if updated, remains accurate.
- A repo-wide check (e.g. `grep -rln "gui/" docs/*.md`) confirms no other doc file was left with a stale claim about the GUI package's component inventory as a result of this change.
- If no wording changes were needed (the existing summary-level text already reads accurately), say so explicitly in the report rather than making a cosmetic edit for its own sake.

## Metadata

architectural_impact: true
