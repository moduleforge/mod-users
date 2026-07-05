# Login/Register UI Components

## Purpose and scope

`gui/src` currently exports `AuthProvider`, `useAuth`, `useOptionalAuth`, `RequireAuth`, and the OIDC/login API client helpers (`createUsersClient`, `api`, `fetchProviders`, OIDC config/provider helpers), but ships no ready-to-use Login/Register UI — no form component, no page component. Consuming applications must hand-build their own login/register UI against the raw API helpers, as `app-mfdemo` currently does at `app-mfdemo/src/app/auth/login/page.tsx` and `app-mfdemo/src/app/auth/register/page.tsx`.

This plan adds four new components to `gui/src/components/` and exports them from `gui/src/index.ts`, generalizing `app-mfdemo`'s hand-built login/register/OIDC-return pages into reusable library components:

- **`LoginForm`** — email/password login fields, client-side validation, error display, and (when the API reports configured OIDC providers) "sign in with `<provider>`" buttons that redirect to the OIDC start endpoint. Self-contained: calls `useAuth().login` and the exported `fetchProviders` helper internally.
- **`RegisterForm`** — given name / family name / email / password registration fields, client-side password-length validation (mirrors the server's 12-character minimum in `api/internal/service/user_accounts.go`), and error display. Self-contained: calls `useAuth().register` internally.
- **`AuthPage`** — a full page component that supplies the `Card` chrome, composes `LoginForm`/`RegisterForm`, and toggles between login and register mode with an in-page link (no app-level routing required to switch modes).
- **`OidcCallbackPage`** — a full page component for the OIDC provider's redirect-back leg: parses the token/return-path fragment (or `?error=`) the API's OIDC callback redirects with, validates it, calls `useAuth().completeExternalLogin`, and reports success/failure via injected callbacks (`onComplete`/`onError`) rather than importing a router directly — consistent with the router-agnostic pattern `AuthProvider`/`RequireAuth`/`ClientLayout` already use (`onNavigate`, `onUnauthenticated`, `onNavigateToConfig`).

`app-mfdemo` (a separate project/git repository at `/Users/zane/playground/moduleforge/app-mfdemo`, sibling to `mod-users`) is the source of truth for expected UX, form fields, validation behavior, and the OIDC-redirect/callback round trip; its pages at `src/app/auth/login/page.tsx`, `src/app/auth/register/page.tsx`, and `src/app/auth/oidc/return/page.tsx` were read in full and are the basis for the new components' behavior.

**Explicitly out of scope for this plan** (not requested, and not part of the "Login/Register UI" ask):
- Email one-time-code login (`app-mfdemo/src/app/auth/email-code/page.tsx`).
- Forgot-password / reset-password (`app-mfdemo/src/app/auth/forgot-password/page.tsx`, `.../reset/page.tsx`).
- Updating `app-mfdemo` itself to consume the new `gui/` exports instead of its bespoke pages — `app-mfdemo` is a separate git repository outside `project_root` (`/Users/zane/playground/moduleforge/mod-users`); per this project's established plan-documents handling protocol and prior-plan precedent (follow-up `0PHW`: a cross-repo README correction was left untouched as out-of-boundary for the same reason), a task dispatched from this plan cannot reach into `app-mfdemo`'s working tree. **This is recorded as a flagged follow-up for the manager** (see the structured report), not as a plan phase/task.

## Current status

No plan/TODO.yaml existed before this session — this is the first planning invocation for `login-register-ui`. The plan begins at Phase 1 (single phase; see below) with no pre-conditions beyond the current `main`-branch state of `gui/src` (as aligned by the immediately-prior `gui-lib-conversion` plan: single `"."` export map entry in `gui/package.json`, all public symbols re-exported from `gui/src/index.ts`).

## Overview

Single-phase plan. The change is confined to one coherent area (`gui/src/components/` + `gui/src/index.ts`); no research gaps block full task decomposition; app-mfdemo's reference pages have already been read in full, so field names, validation rules, and OIDC round-trip mechanics are established inputs, not open questions.

### Phase 1 — Auth UI Components

Adds the four components described above and wires them into the package's public export surface.

1. **`login-register-forms`** — add `LoginForm` (`gui/src/components/login-form.tsx`) and `RegisterForm` (`gui/src/components/register-form.tsx`), export both from `gui/src/index.ts`. No dependency on other Phase 1 tasks.
2. **`oidc-callback-page`** — add `OidcCallbackPage` (`gui/src/components/oidc-callback-page.tsx`), export from `gui/src/index.ts`. No dependency on other Phase 1 tasks; **parallel-eligible with task 1** (both only add new files + append to `index.ts`; any merge-order contention on `index.ts` is a normal sequential-merge concern, not a task-design issue).
3. **`auth-page`** — add `AuthPage` (`gui/src/components/auth-page.tsx`), which imports and composes `LoginForm`/`RegisterForm` from task 1. **Depends on task 1** (`login-register-forms`) landing first. Also exports from `gui/src/index.ts`.

If the architectural-implications check (run after this phase's task documents are authored) finds implications, a `doc-updates` phase is appended after Phase 1 — see the structured report for whether it was added.

### Related notes

- No `plan/notes/` research topics were needed — all required investigation (existing `gui/src` structure, `AuthProvider`/`useAuth`/`RequireAuth`, the API client, the package export-map convention shared with `mod-core`/`mod-contacts`/`mod-tags`/`mod-tasks`, and `app-mfdemo`'s reference pages) was completed during this same planning session and is summarized inline in each task document rather than in a separate notes file.
