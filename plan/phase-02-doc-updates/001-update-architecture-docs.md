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

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-05
- **Implementation worktree:** `/Users/zane/playground/moduleforge/mod-users/worktrees/phase-02-task-01-update-architecture-docs` (branch `phase-02-task-01-update-architecture-docs`)
- **Files modified (repo-relative to `mod-users`):**
  - `docs/architecture.md` — "GUI component library" section rewritten to name `LoginForm`, `RegisterForm`, `AuthPage`, and `OidcCallbackPage` at the capability level (self-contained form components calling `useAuth()`/the API client directly; router-agnostic callback-prop pages), and to explicitly state that password reset, profile management, and email one-time-code login are not yet part of the `gui/` surface. The prior text ("ready-made components for registration, login (all channels), password reset, and profile management") overstated the actual inventory once read against what Phase 1 delivered.
- **Files reviewed, no change made:**
  - `docs/mod-users-spec.md` use case 13 ("GUI component rendering and demo app") — reads at a generic "auth, profile, admin, and OIDC-config surfaces" level that does not specifically claim login/register capabilities beyond what now exists; still accurate, no edit needed.
  - `docs/project-structure.md`'s `gui/` section — the `components/` directory-purpose annotation ("auth flows, profile, admin views") is a generic directory-contents description, not a specific component-inventory claim; still accurate, no edit needed.
  - `docs/architecture.md`'s "Sub-project layout" table row for `gui/` ("UI components for auth flows and user management") — same generic-capability framing as above, not a specific inventory claim; left untouched as out of this task's scope (it predates Phase 1 and Phase 1 did not touch it).
- **Validation summary:**
  1. `docs/architecture.md`'s "GUI component library" section was read and updated; it now accurately reflects that Login/Register UI (form + page level) exists, without overstating scope into email-code/forgot-password/reset flows or profile management, all of which remain unimplemented.
  2. `docs/mod-users-spec.md`'s use case 13 was read; it remains accurate — no wording change needed (recorded explicitly per the Validation section's instruction rather than making a cosmetic edit).
  3. `grep -rln "gui/" docs/*.md` → `docs/architecture.md`, `docs/project-structure.md`, `docs/mod-users-spec.md`. Each was individually reviewed above; no stale component-inventory claim remains in any of the three.
- **Assumptions applied:** None beyond what the task doc itself stated (Phase 1's three task documents, read in full, were treated as the authoritative source of what changed; the merged source in the sibling implementation worktree was read directly to cross-check every claim before wording it into the doc).
