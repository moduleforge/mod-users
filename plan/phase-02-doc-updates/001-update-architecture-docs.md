# Update Architecture Docs

## Purpose and scope

Updates `docs/architecture.md` (and reviews `docs/mod-users-spec.md`) to reflect the three new GUI components shipped by Phase 1 of this plan: `ForgotPasswordPage`, `ResetPasswordPage`, and `EmailCodePage`. Follows the `update-architecture-docs` task-procedure at `plugins/flow/task-procedures/update-architecture-docs/SKILL.md` (path relative to the Flow plugin root, e.g. `/Users/zane/playground/sdlcforge/flow/plugins/flow/task-procedures/update-architecture-docs/SKILL.md`). This phase mirrors the `login-register-ui` plan's own `doc-updates` phase precedent exactly (see `plan/plan-summary-login-register-ui.md`'s Phase 2 for the prior instance of this same task).

role_doc: references/roles/architect-frontend.md

## Requirements

- **Implementation task documents that surfaced this need** (by path, relative to this plan's `plan/` directory — these are implementation task docs that will be complete by the time this task runs, not already-executed docs):
  - `phase-01-forgot-reset-email-code-ui/001-forgot-password-page.md`
  - `phase-01-forgot-reset-email-code-ui/002-reset-password-page.md`
  - `phase-01-forgot-reset-email-code-ui/003-email-code-page.md`
- **Architecture and spec files needing review:**
  - `docs/architecture.md` — specifically the "GUI component library" section (currently states: "Password reset, profile management, and email one-time-code login are not yet part of this surface."). Update this section to name `ForgotPasswordPage`, `ResetPasswordPage`, and `EmailCodePage` alongside the four existing components, and to remove or correct the now-stale "not yet part of this surface" sentence to reflect that password reset and email one-time-code login are now covered (profile management remains out of scope and should stay flagged as such, unless a separate plan has since addressed it — verify against the current file content, do not assume).
  - `docs/mod-users-spec.md` — use case 13 ("GUI component rendering and demo app") describes the GUI surface only in generic terms; per the `login-register-ui` plan's Phase 2 precedent, this was reviewed and found accurate at its existing generic-capability level with no changes needed. Re-verify this holds after the three new components ship; only edit if a specific inaccuracy is found (do not add unnecessary detail).
  - `docs/project-structure.md`'s `gui/` section — reviewed by the `login-register-ui` plan's Phase 2 precedent and found already accurate; re-check briefly for the same reason as above.
- Cross-check every claim against the actual merged component source (`gui/src/components/forgot-password-page.tsx`, `gui/src/components/reset-password-page.tsx`, `gui/src/components/email-code-page.tsx`, and the updated `gui/src/index.ts`) before wording it — do not describe planned behavior from task documents as if already-verified fact without confirming it against the landed code, per the `login-register-ui` precedent's own stated practice.
- Do not touch unrelated pre-existing documentation gaps noted in `plan/followups.yaml` (e.g. the Storybook-vs-Ladle terminology inconsistency tagged `doc-updates` from the prior plan) unless they are directly implicated by this task's own edits.

## Validation

- `docs/architecture.md`'s GUI component library paragraph names all seven components now shipped (`LoginForm`, `RegisterForm`, `AuthPage`, `OidcCallbackPage`, `ForgotPasswordPage`, `ResetPasswordPage`, `EmailCodePage`) or is otherwise updated so no reader would conclude password-reset/email-code UI is still absent.
- `grep -n "not yet part of this surface" docs/architecture.md` — confirm the sentence has been corrected or removed to no longer overstate what's missing (email one-time-code login and password reset are no longer "not yet part of this surface"; adjust wording precisely rather than deleting the whole caveat if profile management is still genuinely absent).
- Every new claim added to `docs/architecture.md` is verifiable against the actual merged source of the three new components (spot-check prop names, callback patterns, and `useAuth()` usage against the real files, not against this task document's Requirements section, in case implementation deviated from plan).
- `docs/mod-users-spec.md` and `docs/project-structure.md` are read and either left unchanged (if still accurate) or updated with a clear rationale in the task document's own notes for what changed and why.

## Metadata

architectural_impact: true

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-05
- **Summary:** Updated `docs/architecture.md`'s "GUI component library" section (in the task worktree at `/Users/zane/playground/moduleforge/mod-users/worktrees/phase-02-task-01-update-architecture-docs`, commit `624ac5b`) to name all seven now-shipped components — `LoginForm`, `RegisterForm`, `AuthPage`, `OidcCallbackPage`, `ForgotPasswordPage`, `ResetPasswordPage`, `EmailCodePage` — and to describe the three new page-level compositions' behavior (`ForgotPasswordPage`'s request/confirmation views, `ResetPasswordPage`'s required `token` prop with no internal URL/router reads, `EmailCodePage`'s request/verify step toggle and its `useAuth()` call to establish the session on success, unlike its two unauthenticated siblings which call the API client directly). The stale sentence "Password reset, profile management, and email one-time-code login are not yet part of this surface" was narrowed to "Profile management is not yet part of this surface" — profile management remains genuinely unaddressed (no dedicated `gui/src/components/*profile*` component exists), confirmed by listing `gui/src/components/` before editing.
- **Files touched:** `docs/architecture.md` (repo-relative, inside `phase-02-task-01-update-architecture-docs` worktree).
- **Validation:**
  - `docs/architecture.md`'s GUI component library paragraph names all seven components — confirmed via per-name grep, all present.
  - `grep -n "not yet part of this surface" docs/architecture.md` — one match remains, now scoped only to "Profile management is not yet part of this surface"; password reset and email one-time-code login are no longer described as absent.
  - Every new claim (prop names, callback patterns, `useAuth()` usage) cross-checked against the actual merged source: `gui/src/components/forgot-password-page.tsx`, `gui/src/components/reset-password-page.tsx`, `gui/src/components/email-code-page.tsx`, and `gui/src/index.ts` (all three components and their prop types confirmed exported).
  - `docs/mod-users-spec.md` use case 13 ("GUI component rendering and demo app") re-read: still generic ("Components render the auth, profile, admin, and OIDC-config surfaces") and accurate at its existing capability level — left unchanged, matching the `login-register-ui` precedent's finding.
  - `docs/project-structure.md`'s `gui/` section re-read: still generic ("React UI components (auth flows, profile, admin views)") and accurate — left unchanged, matching the `login-register-ui` precedent's finding. (Its `stories/` comment still says "Storybook story files"; that pre-existing Storybook-vs-Ladle inconsistency is the one already tracked in `plan/followups.yaml` and was not touched, per this task's own Requirements instruction.)
- **Decisions made:** Kept the GUI component library paragraph as a single dense paragraph (matching the existing document's style) rather than splitting it into a list, since the section is not yet large enough to warrant restructuring and the `update-architecture-docs` procedure calls for in-place edits without restructuring accurate adjacent content. Added one clause distinguishing which new components call `useAuth()` (only `EmailCodePage`) versus which call the API client directly (`ForgotPasswordPage`, `ResetPasswordPage`), since this is a real architectural distinction worth surfacing (mirrors `docs/architecture.md`'s existing practice of noting `useAuth()`/API-client usage for `LoginForm`/`RegisterForm`).
- **Task document location note:** This Status section was written in the plan worktree (`/Users/zane/playground/moduleforge/mod-users/worktrees/plan/forgot-reset-email-code-ui`) since the task document only exists there, per dispatch instructions and the `login-register-ui` plan's own Phase 2 precedent (see `plan/plan-summary-login-register-ui.md`'s follow-up item `slra`). It is left uncommitted in the plan worktree; the manager should commit it there alongside applying this report.
