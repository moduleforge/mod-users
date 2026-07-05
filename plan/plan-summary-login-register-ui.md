# Login/Register UI Components — Plan Summary

## What was planned and why

`gui/src` already exported `AuthProvider`, `useAuth`, `useOptionalAuth`, `RequireAuth`, and the OIDC/login API client helpers (`createUsersClient`, `api`, `fetchProviders`, OIDC config/provider helpers), but shipped no ready-to-use Login/Register UI — no form component, no page component. Consuming applications were left to hand-build their own login/register UI against the raw API helpers, as `app-mfdemo` (a separate, sibling git repository) had already done at `app-mfdemo/src/app/auth/login/page.tsx`, `.../register/page.tsx`, and `.../oidc/return/page.tsx`.

This plan added four new components to `gui/src/components/`, exported from `gui/src/index.ts`, generalizing `app-mfdemo`'s hand-built login/register/OIDC-return pages into reusable library components: `LoginForm`, `RegisterForm`, `AuthPage`, and `OidcCallbackPage`. `app-mfdemo`'s existing pages served as the source of truth for expected UX, form fields, validation behavior, and the OIDC-redirect/callback round trip.

Explicitly out of scope: email one-time-code login, forgot-password/reset-password flows, and updating `app-mfdemo` itself to consume the new exports (a separate repository outside this plan's `project_root`, per established plan-documents handling protocol).

This was a single-phase plan at the outset (Phase 1 — Auth UI Components); a second phase (Documentation Updates) was appended after the phase-1 gate review's architectural-implications check found that `docs/architecture.md` needed updating to reflect the new component surface.

## What shipped

### Phase 1 — Auth UI Components (`auth-ui-components`)

- **Task 001 — `login-register-forms`** (merge `12e6757fcf5b26f5fef986414fde8e3fae14526b`): Added `LoginForm` and `RegisterForm` to `gui/src/components/`, adapted from `app-mfdemo`'s reference pages with Next.js routing stripped in favor of `onSuccess` callback + `initialError`/`returnPath` props. Both are self-contained (call `useAuth()` directly). `LoginForm` reproduces the tri-state OIDC-provider fetch with an unmount guard and inline provider icons; `RegisterForm` reproduces the 12-character server-mirrored password rule (`api/internal/service/user_accounts.go`). Both exported from `gui/src/index.ts` with Ladle stories. Typecheck clean apart from the known pre-existing `@moduleforge/core-gui` `.yalc` gap.
- **Task 002 — `oidc-callback-page`** (merge `8eaa167ade6631faa7fc9000cdc27595d3ef8763`): Added `OidcCallbackPage`, a framework-agnostic page consuming the OIDC provider round trip — reads a query-string error param (failure, calls `onError`) or a URL-fragment token/return pair (success), validates via `looksLikeJwt`/`isSafeReturnPath` (copied verbatim from the `app-mfdemo` reference including all open-redirect rejection branches), strips the fragment from history before firing callbacks, then calls `completeExternalLogin` from `useAuth()` and reports via injected `onComplete`/`onError`. Exported from `gui/src/index.ts`. One real issue (an incompatible `JSX.Element` return-type annotation) was found and fixed during implementation.
- **Task 003 — `auth-page`** (merge `fcac9bce19b34e7021a7e04f7dd14ca93a3c8763`): Added `AuthPage`, composing `LoginForm`/`RegisterForm` (from task 001) inside a `Card` from `core-gui`, with an internal `useState<AuthMode>` toggle (seeded by `initialMode`, default `'login'`) rather than router-driven navigation. Per-mode `CardTitle`/`CardDescription` copy and `CardFooter` toggle prompts match the `app-mfdemo` reference. Exported `AuthPage`/`AuthPageProps`/`AuthMode` from `gui/src/index.ts` with a Ladle story. All 5 validation checks passed.

**Interstitial fix (not a plan task):** After the phase-1 gate review, a simple-task fix was dispatched and merged as commit `deb1afe` (branch `2026-07-04-fix-authpage-footer-style-and`), addressing two gate-review findings: `AuthPage`'s `CardFooter` text-muted-foreground styling parity, and an `OidcCallbackPage` `onError` JSDoc clarification around `encodeURIComponent`. This touched `gui/src/components/auth-page.tsx` and `gui/src/components/oidc-callback-page.tsx`. Because it wasn't a registered plan task, it doesn't appear in `todo_list_all`'s output — recorded here from dispatch context.

### Phase 2 — Documentation Updates (`doc-updates`)

- **Task 001 — `update-architecture-docs`** (merge `423bb93f08ed4c47868d5d6d450bda7890cb33d1`): Updated `docs/architecture.md`'s GUI component library section to name the four Phase-1 components (`LoginForm`, `RegisterForm`, `AuthPage`, `OidcCallbackPage`) and the pattern they establish — self-contained components calling `useAuth()`/the API client directly; page-level components reporting outcomes via router-agnostic callback props — replacing prior wording that overstated scope by implying ready-made password-reset/profile-management/all-channel-login UI already existed. `docs/mod-users-spec.md` use case 13 and `docs/project-structure.md`'s `gui/` section were reviewed and found already accurate at their existing generic-capability level; no edits made there. Every claim was cross-checked against merged component source before wording it.

## Key decisions

- **Two separate form components (`LoginForm`/`RegisterForm`)** rather than one combined `AuthForm` — mirrors the two distinct concerns (and two distinct `app-mfdemo` reference pages) and keeps each form's validation/submission logic independently self-contained.
- **`AuthPage`/`OidcCallbackPage` split as two separate page components** rather than one — the OIDC callback leg is a distinct routing target (the provider's redirect-back URL) with its own lifecycle (fragment parsing, token validation, history cleanup) that doesn't belong on the same page as the login/register form chrome.
- **Mode toggle is internal component state, not app-level routing** — `AuthPage` owns a `useState<AuthMode>` (default `'login'`, overridable via `initialMode`) so consuming apps don't need to wire up separate routes/pages just to switch between login and register; this also avoids the router-agnostic pattern being broken by a routing dependency.
- **Router-agnostic callback pattern preserved** — `OidcCallbackPage` reports success/failure via injected `onComplete`/`onError` callbacks rather than importing a router directly, consistent with `AuthProvider`/`RequireAuth`/`ClientLayout`'s existing `onNavigate`/`onUnauthenticated`/`onNavigateToConfig` pattern.
- **Scope confined to login/register/OIDC only** — email one-time-code login, forgot-password, and reset-password were explicitly excluded, as was migrating `app-mfdemo` itself to consume the new exports (out-of-boundary: a separate git repository outside this plan's `project_root`).
- **Password-length validation mirrors the server** — `RegisterForm`'s client-side validation reproduces the 12-character minimum enforced server-side in `api/internal/service/user_accounts.go`, rather than inventing an independent rule.

## Follow-up items

Drawn from `plan/followups.yaml`, filtered to this plan's phases (`auth-ui-components`, `doc-updates`) plus the `security-urgent` item surfaced during the phase-1 gate review:

### Security — urgent, flagged prominently

- **`pDQQ` — Apparent live OAuth secrets in git history.** Independent security review of this plan's phase-1 diff discovered, while reading always-read context, what appear to be live committed OAuth credentials at the `mod-users` repo root: `client_secret_159014138472-2lnc34p2cgldb0g714tcoifgr3u4e71j.apps.googleusercontent.com.json` (a Google OAuth client_secret JSON — confirmed via presence of a `client_secret` key) and three `ms-secret-*.token` files (~41 bytes each, consistent with Microsoft Entra client secrets). Added in commit `9680136` ("wip(main): add UpdateTagValue query to model/queries/tags.sql" — an unrelated commit message suggesting accidental inclusion), well before this plan's `diff_base`. Unrelated to this plan's changes but too significant to withhold: **the repo owner should verify whether these credentials are live and, if so, rotate them and scrub git history.**

### Phase 1 — auth-ui-components

- **`Vthh`** (`001-login-register-forms.md`): No Task tool was available to dispatch `review-changes-security`/`review-changes-correctness` as independent sub-agents; the implementing agent applied both lenses' checklists inline instead (no findings). An independent review pass may be warranted before this ships.
- **`lbn5`** (`001-login-register-forms.md`): Task document status update was committed directly in the plan worktree (commit `884b4b3`) rather than inside the task's own worktree, since the task document is a plan artifact that only exists in the plan worktree.
- **`CAHg`** (`002-oidc-callback-page.md`): Task document for this task does not exist inside the task's own worktree (branch predates the plan branch's phase-01 task docs); the implementing agent read it from the plan worktree per dispatch instructions instead and did not create a Status section in its own worktree.
- **`gMz5`** (`002-oidc-callback-page.md`): Mid-implementation, the implementing agent mistakenly ran `finalize-task-commit.sh` once from the main checkout instead of its task worktree, creating an unwanted commit (`77dac0d`) touching `.flow/session-lock`, `.flow/sessions/`, `.flow/tasks/*.json`, and worktree dirs as gitlinks. The agent caught this and ran `git reset 9af4f26` in the main checkout to undo it. The manager independently verified afterward that the main checkout HEAD is back at `9af4f26` with no data lost.
- **`Tn8F`** (`002-oidc-callback-page.md`): No Task tool was available for independent security/correctness sub-agent review; both lenses were applied inline by the same implementing agent instead (no findings).
- **`s5fu`** (`003-auth-page.md`): No Task tool was available to sub-dispatch `review-changes-correctness`/`review-changes-security` as separate agents; the implementing agent applied both lenses inline instead (no findings). Manager may want an independent review pass if strict reviewer/implementer separation matters for this task.
- **`YL5l`** (`003-auth-page.md`): Task document status update was left uncommitted in the plan worktree (`plan/phase-01-auth-ui-components/003-auth-page.md` modified but not committed there) — manager to commit as part of plan-doc bookkeeping.
- **`wufX`** (phase-1 gate review, efficiency lens): `AuthPage`'s mode ternary swaps `LoginForm`/`RegisterForm` rather than toggling visibility of stably-mounted children, so `LoginForm` unmounts/remounts on every transition back to login mode, re-firing its `fetchProviders()` network call each time. Not a regression vs. the prior `app-mfdemo` per-route architecture, and the toggle is a low-frequency user-driven interaction (cold path) — informational, not a required fix. If provider-list churn becomes a concern, either keep both forms mounted and toggle visibility with CSS, or lift the provider fetch/cache above `LoginForm` (e.g. into `AuthPage` or a shared hook).

### Phase 2 — doc-updates

- **`slra`** (`001-update-architecture-docs.md`): Task document Status-section update was made in the plan worktree rather than the task worktree (document only exists there); left uncommitted there — manager should commit alongside applying this report.
- **`hQGq`** (`001-update-architecture-docs.md`): No other stale GUI-inventory claims found; no reviewer findings (no `review_focus` was supplied for this task).

### Consumer migration (app-mfdemo) — note on a gap

`plan/overview.md` states that migrating `app-mfdemo` to consume the new `gui/` exports (instead of its bespoke pages) is out of scope for this plan because `app-mfdemo` is a separate git repository outside `project_root`, and records this as "a flagged follow-up for the manager." However, no `plan/followups.yaml` item tagged `login-register-ui-consumer-migration` (or any equivalent tag) currently exists to carry that flag forward. **The manager should add a follow-up item for the `app-mfdemo` consumer migration** (updating `app-mfdemo/src/app/auth/login/page.tsx`, `.../register/page.tsx`, and `.../oidc/return/page.tsx` to use the new `LoginForm`/`RegisterForm`/`AuthPage`/`OidcCallbackPage` exports) if one hasn't been recorded elsewhere — this summary was drafted expecting to find such an item and did not.
