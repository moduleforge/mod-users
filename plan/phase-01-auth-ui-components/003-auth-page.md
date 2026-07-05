# Add Auth Page Component

## Purpose and scope

Add a new page component, `AuthPage`, to `gui/src/components/auth-page.tsx`, and export it from `gui/src/index.ts`. This is the third of the four "Login/Register UI" components (`plan/overview.md`) — the "full page component that composes the form component[s] into a complete login/register page" called for by the feature request. No standard skill is invoked; this is a novel, self-contained implementation task.

**Depends on task `001-login-register-forms.md`**: this component imports and composes `LoginForm` and `RegisterForm` from that task. Do not start this task until `001` has landed (its components exist at `gui/src/components/login-form.tsx` and `gui/src/components/register-form.tsx`).

Scope is strictly `gui/src/components/auth-page.tsx`, the corresponding addition to `gui/src/index.ts`, and (optionally) a Ladle story. Do not touch `model/`, `api/`, `app-mfdemo`, or any file outside `gui/`.

## Requirements

`AuthPage` supplies the `Card` chrome around `LoginForm`/`RegisterForm` and toggles between the two modes **in-page** (an internal `useState`, not app-level routing) — this module does not own routing, per `docs/mod-users-spec.md`'s Non-goals, so mode-switching must not require the consumer to change URL or route. Read `/Users/zane/playground/moduleforge/app-mfdemo/src/app/auth/login/page.tsx` and `.../register/page.tsx` again for the exact `Card`/`CardHeader`/`CardTitle`/`CardDescription`/`CardContent`/`CardFooter` composition and copy text this component reproduces per-mode.

```ts
export type AuthMode = 'login' | 'register';

export interface AuthPageProps {
  /** Which mode to render first. Defaults to `'login'`. */
  initialMode?: AuthMode;
  /** Called after a successful login or registration (either mode). */
  onAuthenticated?: () => void;
  /**
   * Initial error message forwarded into `LoginForm` — e.g. surfaced from an
   * OIDC callback's `?error=` query param by the consuming app. Only applies
   * while in login mode.
   */
  initialError?: string | null;
  /** Forwarded to `LoginForm`'s OIDC `return` path. Defaults to `'/'`. */
  returnPath?: string;
}
export function AuthPage(props: AuthPageProps): JSX.Element
```

Behavior:

- `const [mode, setMode] = useState<AuthMode>(initialMode ?? 'login');` — internal, uncontrolled state; `AuthPage` does not accept a controlled `mode` prop. (`initialMode` only seeds the first render — e.g. a consumer whose own route is `/auth/register` can pass `initialMode="register"` so a page refresh lands back on the right mode, but subsequent toggling is entirely internal.)
- Render a single `Card` (`className="w-full max-w-sm"`, centered via a wrapping `<div className="flex min-h-full items-center justify-center p-6">`, matching the reference's outer layout):
  - `CardHeader` with `CardTitle`/`CardDescription` text switched on `mode`: `'Sign in'` / `'Enter your credentials to continue'` for login; `'Create an account'` / `'Fill in your details to get started'` for register.
  - `CardContent` renders `<LoginForm onSuccess={onAuthenticated} initialError={initialError} returnPath={returnPath} />` when `mode === 'login'`, else `<RegisterForm onSuccess={onAuthenticated} />`.
  - `CardFooter` (`className="text-sm text-center"`) renders the mode-toggle prompt: in login mode, `"No account? "` followed by a `<button type="button">` styled as a link (`className="text-foreground hover:underline"`, matching the reference's `Link` styling) reading `"Create one"` that calls `setMode('register')`; in register mode, `"Already have an account? "` / `"Sign in"` toggling back via `setMode('login')`. Use a `<button type="button">`, not an anchor/`Link` — there is no URL change to make, and this component must not import a router package.
- Import `Card`, `CardContent`, `CardDescription`, `CardFooter`, `CardHeader`, `CardTitle` from `@moduleforge/core-gui`; import `LoginForm`/`RegisterForm` from `./login-form` / `./register-form` (the sibling files added by task `001`).

### Export wiring

Add `AuthPage`, `AuthPageProps`, and `AuthMode` to `gui/src/index.ts` under the `// ─── Components ───` section, matching the existing export style.

### Ladle story (style convention)

Add `gui/src/stories/AuthPage.stories.tsx` following the `RequireAuth.stories.tsx` pattern (wrap in `<AuthProvider>`; a story for the default login-mode render and one that sets `initialMode="register"` are sufficient — submission will fail with a `network_error` in the story environment, which is expected, matching `RequireAuth`'s story precedent of never reaching a real backend).

## Validation

1. `cd gui && bunx tsc --noEmit` (equivalent to `make lint.gui`). **Known pre-existing environment gap**: this will fail with `Cannot find module '@moduleforge/core-gui'` in any environment where `.yalc/` is not populated (see `plan/followups.yaml` items `ThVz`/`HSiS`/`VqCM`, and `.claude/CLAUDE.md`'s "Known gotchas"). If the **only** failures are unresolved `@moduleforge/core-gui` module errors, treat that as the known gap and say so in your report; do not attempt `yalc publish`/`yalc add`. Any other typecheck error — including one caused by `LoginForm`/`RegisterForm` not existing or exporting a different shape than task `001` produced — must be fixed or flagged before reporting completion.
2. Confirm `gui/src/components/login-form.tsx` and `gui/src/components/register-form.tsx` exist and export `LoginForm`/`RegisterForm` with the prop shapes documented in task `001-login-register-forms.md` before starting; if they are missing or materially differ, halt and flag rather than guessing at a different integration.
3. Re-read the new file against the behavior list above; confirm the internal (uncontrolled) mode toggle, the per-mode `CardTitle`/`CardDescription`/`CardFooter` text, and the `onAuthenticated`/`initialError`/`returnPath` prop pass-through to `LoginForm` are all present.
4. `grep -n "AuthPage" gui/src/index.ts` shows the component, its props-type export, and the `AuthMode` type export.
5. Confirm the file imports nothing from `next/navigation`, `next/link`, or any other router package.

## Assumptions

- Task `001-login-register-forms.md` has already landed with `LoginForm`/`RegisterForm` matching the prop signatures specified in that task document. If reality differs, adapt the integration to the actual signatures and note the discrepancy in your report rather than treating it as a blocker.
- `@moduleforge/core-gui`'s `Card`/`CardHeader`/`CardTitle`/`CardDescription`/`CardContent`/`CardFooter` components exist with the same API `app-mfdemo` and this task's sibling tasks already assume.

## References

- `/Users/zane/playground/moduleforge/app-mfdemo/src/app/auth/login/page.tsx` and `.../register/page.tsx` — reference `Card` composition and copy text.
- `plan/phase-01-auth-ui-components/001-login-register-forms.md` — the task this one depends on; defines `LoginForm`/`RegisterForm`'s prop shapes.
- `gui/src/stories/RequireAuth.stories.tsx` — story-authoring convention for `AuthProvider`-dependent components.
- `docs/mod-users-spec.md` — Non-goals ("User interface routing... is not provided by `gui/`"), the constraint motivating the internal (not router-driven) mode toggle.

## Status

- **Outcome**: succeeded
- **Date**: 2026-07-05
- **Summary**: Added `gui/src/components/auth-page.tsx` exporting `AuthPage`, `AuthPageProps`, and `AuthMode`; wired the exports into `gui/src/index.ts` under `// ─── Components ───`; added `gui/src/stories/AuthPage.stories.tsx` (`Default` and `RegisterMode` stories, `AuthProvider`-wrapped, matching `RequireAuth.stories.tsx`'s precedent of not reaching a real backend). `AuthPage` uses internal `useState<AuthMode>` seeded by `initialMode` (defaulting to `'login'`), composes `Card`/`CardHeader`/`CardTitle`/`CardDescription`/`CardContent`/`CardFooter` from `@moduleforge/core-gui` around `LoginForm`/`RegisterForm`, and toggles mode via `<button type="button">` (no router import).
- **Validation**:
  1. `cd gui && bunx tsc --noEmit` — failed only with the known pre-existing `Cannot find module '@moduleforge/core-gui'` errors (7 occurrences across `auth-page.tsx` and 6 pre-existing files); `.yalc/` is absent in this worktree, confirming the documented environment gap (`plan/followups.yaml` `ThVz`/`HSiS`/`VqCM`). No other typecheck errors.
  2. Confirmed `gui/src/components/login-form.tsx` and `register-form.tsx` exist, already merged from task `001`, exporting `LoginForm`/`RegisterForm` with the documented prop shapes (`LoginFormProps`: `onSuccess`, `initialError`, `returnPath`; `RegisterFormProps`: `onSuccess`) — used as-is, no adaptation needed.
  3. Re-read the new file: internal uncontrolled mode toggle confirmed, per-mode `CardTitle`/`CardDescription`/`CardFooter` text confirmed, `onAuthenticated`/`initialError`/`returnPath` pass-through to `LoginForm` confirmed.
  4. `grep -n "AuthPage" gui/src/index.ts` — shows `AuthPage`, `AuthPageProps`, and `AuthMode` exports.
  5. `grep -n "next/navigation\|next/link\|router" gui/src/components/auth-page.tsx` — no matches; no router import.
- **Assumptions applied**: Task `001`'s `LoginForm`/`RegisterForm` prop shapes matched what this task assumed; no adaptation was necessary. `@moduleforge/core-gui`'s `Card` family exists with the same API `app-mfdemo` uses (assumed per task doc; not directly verifiable in this environment due to the `.yalc/` gap, consistent with the known pre-existing issue).
