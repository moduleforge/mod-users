# Add Login And Register Form Components

## Purpose and scope

Add two new self-contained, reusable form components to `gui/src/components/` — `LoginForm` and `RegisterForm` — and export them from `gui/src/index.ts`. These are the first of four components (this task, plus `oidc-callback-page` and `auth-page` — the latter depends on this task) delivering the "Login/Register UI" capability described in `plan/overview.md`. No standard skill is invoked; this is a novel, self-contained implementation task within an established component-library convention (see References).

Scope is strictly `gui/src/components/login-form.tsx`, `gui/src/components/register-form.tsx`, and the corresponding additions to `gui/src/index.ts` and (if you add Ladle stories per the convention below) `gui/src/stories/`. Do not touch `model/`, `api/`, `app-mfdemo`, or any file outside `gui/`.

## Requirements

Read the reference implementation in full before writing any code:
- `/Users/zane/playground/moduleforge/app-mfdemo/src/app/auth/login/page.tsx` — source of truth for `LoginForm`'s fields, OIDC-provider button UX (including the inline Google/Microsoft brand SVG icons), and error handling.
- `/Users/zane/playground/moduleforge/app-mfdemo/src/app/auth/register/page.tsx` — source of truth for `RegisterForm`'s fields and validation.

Both components must be **self-contained**: unlike `mod-core`'s `NaturalPersonForm` (a pure controlled `value`/`onChange` component with no data-fetching), `LoginForm` and `RegisterForm` call `useAuth()` (from `../lib/auth-context`) directly to perform submission — matching the explicit shape requested for this feature. Each renders only its `<form>` internals (fields, inline error, submit button, and — for `LoginForm` — the OIDC provider section); neither renders a `Card` wrapper. The enclosing `Card` chrome is the `auth-page` task's responsibility (task `003-auth-page.md`), so these two components must be usable standalone (e.g. embedded in an existing `Card`, a modal, or a Ladle story) without asserting their own outer chrome.

### `LoginForm` (`gui/src/components/login-form.tsx`)

```ts
export interface LoginFormProps {
  /** Called after a successful login. */
  onSuccess?: () => void;
  /**
   * Initial error message to display — e.g. surfaced from an OIDC callback
   * redirect's `?error=` query param by the consuming app. This component
   * never reads `window.location` or a router itself for this value; the
   * consumer owns URL/query parsing and passes the decoded message in.
   */
  initialError?: string | null;
  /**
   * Site-relative path echoed back once the OIDC provider round trip
   * completes; passed as the `return` query param on the OIDC start URL.
   * Defaults to `'/'`.
   */
  returnPath?: string;
}
export function LoginForm(props: LoginFormProps): JSX.Element
```

Behavior (adapted from `LoginPageInner` in the reference, minus the Next.js-specific `useRouter`/`useSearchParams`/`Suspense` wrapper — those are app-level routing concerns this library does not own, per `docs/mod-users-spec.md`'s Non-goals):

- Local state: `email`, `password`, `error` (initialized from `initialError`, default `null`), `isSubmitting`, `providers: OIDCProvider[] | null` (starts `null` = "still loading"; an empty array means "loaded, none configured" — matches the reference's tri-state comment).
- `useEffect` on mount: call the already-exported `fetchProviders()` (from `../lib/api`, already re-exported via `gui/src/index.ts`) and set `providers` from the result. Guard against a state update after unmount (`cancelled` flag), matching the reference.
- `handleSubmit`: `e.preventDefault()`; clear `error`; set `isSubmitting`; `await useAuth().login(email, password)`; on success call `onSuccess?.()`; on `ApiRequestError` (imported from `../lib/api`) set `error` to `err.message`; on any other error, log via `console.error` and set a generic fallback message ("Something went wrong. Check the browser console for details."); `finally` clear `isSubmitting`.
- `handleProviderClick(providerId: string)`: `window.location.assign(`${API_BASE_URL}/v1/auth/oidc/${encodeURIComponent(providerId)}/start?return=${encodeURIComponent(returnPath)}`)` — a full page navigation, not a fetch, matching the reference exactly (`API_BASE_URL` is already exported from `../lib/api`).
- Render: `<ErrorMessage message={error} />` (from `../components/error-message`, already in this directory) at the top of the form; email input (`type="email"`, `autoComplete="email"`, `required`); password input (`type="password"`, `autoComplete="current-password"`, `required`); submit `Button` (`type="submit"`, disabled while submitting, label "Sign in" / "Signing in..."). When `providers` is non-null and non-empty, render the "or continue with" divider and one `Button` (`variant="outline"`) per provider, each showing the small inline brand icon (reuse the `GoogleIcon`/`MicrosoftIcon`/`providerIcon(id)` pattern from the reference, adapted verbatim — these are small, dependency-free inline SVGs, not an icon package) and `Sign in with {display_name}`.
- Import `Button`, `Input`, `Label` from `@moduleforge/core-gui` (the existing peer dependency; see `gui/src/components/error-message.tsx` and `gui/src/components/sidebar-nav.tsx` for the established import pattern from this package).

### `RegisterForm` (`gui/src/components/register-form.tsx`)

```ts
export interface RegisterFormProps {
  /** Called after a successful registration. */
  onSuccess?: () => void;
}
export function RegisterForm(props: RegisterFormProps): JSX.Element
```

Behavior (adapted from the reference register page):

- Local state: `email`, `password`, `givenName`, `familyName`, `error`, `isSubmitting`.
- Client-side validation before submit: reject `password.length < 12` with the message `'Password must be at least 12 characters.'` — this mirrors the server-side rule enforced in `api/internal/service/user_accounts.go` (`len(*in.Password) < 12`); do not invent a different threshold.
- `handleSubmit`: as above but calling `useAuth().register(email, password, givenName, familyName)` (the context method already maps these to the `RegisterRequest` shape — see `gui/src/lib/auth-context.tsx`'s `register` callback). Same `ApiRequestError` vs. generic-fallback error handling as `LoginForm`.
- Render: `<ErrorMessage message={error} />`; a two-column grid of "First name" / "Last name" text inputs (`autoComplete="given-name"` / `"family-name"`, both `required`); email input; password input with a "(min 12 chars)" hint in the label and `minLength={12}`; submit `Button` (label "Create account" / "Creating account...").
- No OIDC section — registration via OIDC is not a separate flow in this module (OIDC login auto-creates or links an account per `docs/mod-users-spec.md` use cases 5–6).

### Export wiring

Add both components (and their prop-type interfaces) to `gui/src/index.ts` under the existing `// ─── Components ───` section, following the exact export style already used there (named component export + named `export type` for its props interface, e.g. how `ClientLayoutProps`/`SidebarNavProps` are exported).

### Ladle stories (style convention, not a hard requirement)

Existing components each have a `gui/src/stories/<Component>.stories.tsx` file (see `RequireAuth.stories.tsx` for the pattern: wrap in `<AuthProvider>`, exercise a couple of representative states). Add `LoginForm.stories.tsx` and `RegisterForm.stories.tsx` following that pattern if feasible in Ladle without a live API (submission will fail with a `network_error` in the story environment — that is expected and fine, matching how `RequireAuth`'s story never reaches a real backend either). If you judge a story impractical for either component, note that decision explicitly in your final report rather than silently omitting it — precedent exists for skipping a story when not practical (`client-layout.tsx` has no story).

## Validation

1. `cd gui && bunx tsc --noEmit` (equivalent to `make lint.gui`). **Known pre-existing environment gap** (see `plan/followups.yaml` items `ThVz`/`HSiS`/`VqCM` from the prior `gui-lib-conversion` plan, and `.claude/CLAUDE.md`'s "Known gotchas"): this will fail with `Cannot find module '@moduleforge/core-gui'` in any environment where `.yalc/` is not populated, and the sandbox's permission classifier has previously denied `yalc publish`/`yalc add` from a task-agent session. If the **only** typecheck failures are unresolved `@moduleforge/core-gui` module errors, treat that as the known environment gap (not a task failure) and say so explicitly in your report; do not attempt `yalc publish`/`yalc add`. If there are any other typecheck errors — in the new files, in `index.ts`, or anywhere else — those must be fixed before reporting completion.
2. Re-read the two new files against the "Requirements" section above and confirm every listed behavior (state fields, validation rule, error-handling branches, OIDC provider button loop) is present.
3. `grep -n "LoginForm\|RegisterForm" gui/src/index.ts` shows both the component and its props-type export.
4. Confirm neither new file imports from `next/navigation`, `next/link`, or any other framework-specific router package — these components must remain framework-agnostic per `docs/mod-users-spec.md`'s Non-goals ("User interface routing... is not provided by `gui/`").
5. If stories were added, confirm they render without throwing when Ladle statically analyzes them (a full `make preview` run is not required, but the file must be syntactically valid TSX matching the existing story exports' shape).

## Assumptions

- `@moduleforge/core-gui`'s `Button`, `Input`, `Label` components exist with the same API surface `app-mfdemo` and this package's existing components (`error-message.tsx`, `sidebar-nav.tsx`) already assume — no need to re-verify their internal implementation.
- The 12-character password minimum is a stable, intentional server-side rule (confirmed live in `api/internal/service/user_accounts.go:157-158`), not a placeholder to reconsider during this task.
- Icon assets: the two brand SVGs are copied/adapted from the reference implementation as inline components, not pulled from `lucide-react` or a new icon dependency.

## References

- `/Users/zane/playground/moduleforge/app-mfdemo/src/app/auth/login/page.tsx` — reference login page.
- `/Users/zane/playground/moduleforge/app-mfdemo/src/app/auth/register/page.tsx` — reference register page.
- `gui/src/lib/auth-context.tsx` — `useAuth()`, `login`, `register`, `ApiRequestError` re-export path.
- `gui/src/lib/api.ts` — `fetchProviders`, `API_BASE_URL`, `OIDCProvider` type.
- `gui/src/components/error-message.tsx` — `ErrorMessage` component this task consumes.
- `gui/src/components/require-auth.tsx` and `gui/src/stories/RequireAuth.stories.tsx` — established callback-injection and story-authoring conventions for this package.
- `/Users/zane/playground/moduleforge/mod-core/gui/src/NaturalPersonForm.tsx` — sibling-module form-component precedent (contrast: that component is a pure controlled form; these two are intentionally self-contained per this feature's explicit request).
- `api/internal/service/user_accounts.go` — server-side password-length validation this task's client-side check mirrors.

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-05
- **Implementation worktree:** `/Users/zane/playground/moduleforge/mod-users/worktrees/phase-01-task-01-add-login-and-register-form-co` (branch `phase-01-task-01-add-login-and-register-form-co`, commit `58d8bef`)
- **Files added/modified (repo-relative to `mod-users`):**
  - `gui/src/components/login-form.tsx` (new)
  - `gui/src/components/register-form.tsx` (new)
  - `gui/src/stories/LoginForm.stories.tsx` (new)
  - `gui/src/stories/RegisterForm.stories.tsx` (new)
  - `gui/src/index.ts` (added `LoginForm`/`LoginFormProps` and `RegisterForm`/`RegisterFormProps` exports)
- **Validation summary:**
  1. `cd gui && bunx tsc --noEmit` — remaining errors are exactly the known `Cannot find module '@moduleforge/core-gui'` environment gap (5 occurrences, one pre-existing per file that imports the package: `error-message.tsx`, `login-form.tsx`, `register-form.tsx`, `sidebar-nav.tsx`, `ui/dialog.tsx`). No other typecheck errors. Confirmed `.yalc/` is not populated in this worktree; did not attempt `yalc publish`/`yalc add` per instruction.
  2. Re-read both new files against Requirements — all state fields, the OIDC provider tri-state fetch/render loop, the 12-char client-side password validation, and both error-handling branches (`ApiRequestError` vs. generic fallback) are present and match the reference implementation's behavior (adapted to drop Next.js-specific routing).
  3. `grep -n "LoginForm\|RegisterForm" gui/src/index.ts` — both component and props-type exports present.
  4. Confirmed via `grep` that neither new component nor its stories import `next/navigation`, `next/link`, or any router package.
  5. Added `LoginForm.stories.tsx` and `RegisterForm.stories.tsx` following the `RequireAuth.stories.tsx` pattern (wrapped in `AuthProvider`); both typecheck cleanly under `tsc --noEmit`.
- **Decisions made:**
  - Did not annotate component return types as `JSX.Element` (as the illustrative signature in Requirements showed) — `@types/react@19` no longer exposes a global `JSX` namespace, and no other component in this package annotates return types, so components were left with inferred return types for consistency with existing conventions.
  - Added explicit `React.ChangeEvent<HTMLInputElement>` parameter types on `Input` `onChange` handlers. With `@moduleforge/core-gui` unresolved, `Input`'s prop types fall back to `any`, which made the handler's `e` parameter an implicit `any` under `strict`/`noImplicitAny` — a cascading effect of the known environment gap, not a new defect. Annotating explicitly resolves it independent of whether `core-gui` is linked.
