# Add Oidc Callback Page Component

## Purpose and scope

Add a new page component, `OidcCallbackPage`, to `gui/src/components/oidc-callback-page.tsx`, and export it from `gui/src/index.ts`. This is the second of the four "Login/Register UI" components (`plan/overview.md`); it has no code dependency on task `001-login-register-forms.md` or `003-auth-page.md` and may be implemented in parallel with `001`. No standard skill is invoked; this is a novel, self-contained implementation task.

Scope is strictly `gui/src/components/oidc-callback-page.tsx`, the corresponding addition to `gui/src/index.ts`, and (optionally) a Ladle story. Do not touch `model/`, `api/`, `app-mfdemo`, or any file outside `gui/`.

## Requirements

Read the reference implementation in full before writing any code: `/Users/zane/playground/moduleforge/app-mfdemo/src/app/auth/oidc/return/page.tsx`. This is the page the API's OIDC callback handler (`GET /v1/auth/oidc/{provider}/callback`, described in `docs/mod-users-spec.md` use case 5 and `docs/architecture.md`'s Authentication flow section) redirects the browser to after the provider round trip completes: on success, the token and a `return` path arrive in the URL **fragment** (never sent to the server in access logs); on failure, the API redirects with an `?error=` query param instead.

`OidcCallbackPage` must be framework-agnostic (no `next/navigation` or other router import) — like `AuthProvider`/`RequireAuth`/`ClientLayout`, it reports outcomes via injected callbacks rather than performing navigation itself:

```ts
export interface OidcCallbackPageProps {
  /**
   * Called once the callback completes successfully, with the site-relative
   * path the consumer should navigate to. This is either the validated
   * `return` value carried in the URL fragment, or `defaultReturnPath` when
   * that value is absent or fails the safety check below.
   */
  onComplete: (returnPath: string) => void;
  /**
   * Called when the callback fails: a provider-reported `?error=`, a
   * malformed/missing token, or a failed session hydration. `message` is
   * safe to display directly or forward as a login page's `?error=` value.
   */
  onError: (message: string) => void;
  /** Fallback return path when no safe `return` value is present. Defaults to `'/'`. */
  defaultReturnPath?: string;
}
export function OidcCallbackPage(props: OidcCallbackPageProps): JSX.Element
```

Behavior, adapted directly from the reference's `OidcReturnPage` (the `useAuth()` import path is `../lib/auth-context`; this component must be rendered under an `AuthProvider`, same precondition as `RequireAuth`):

1. Use a `useRef` guard (`hasProcessed`) so the effect body only runs once, guarding against React 19 Strict Mode's double-invoke — copy this pattern verbatim from the reference, it is a deliberate fix for a specific double-consumption bug (the token/fragment must only be consumed once).
2. On mount, inside the guarded effect:
   - Parse `window.location.search` for an `error` param. If present, call `onError(errorParam)` (decoded) and return — do not attempt to process a fragment in this branch.
   - Otherwise, parse `window.location.hash` (stripping a leading `#`) as `URLSearchParams` and read `token` and `return`.
   - Reimplement the reference's `looksLikeJwt(token)` helper (three non-empty base64url segments) as a local pure function in this file. If the token is missing or fails this check, call `onError('Sign-in failed: malformed response from authentication provider.')` and return.
   - Reimplement the reference's `isSafeReturnPath(candidate)` helper as a local pure function in this file — **copy the full validation exactly**, including all three rejection branches (must start with exactly one `/`; reject `//`-prefixed protocol-relative paths; reject any `\` anywhere in the string; reject a `:` in the first path segment). This is the same open-redirect guard documented as a security requirement in `docs/mod-users-spec.md` ("Open-redirect prevention") — do not simplify or drop any branch.
   - Strip the fragment from the URL via `window.history.replaceState(null, '', window.location.pathname)` (wrapped in `try/catch`, matching the reference — the History API may be unavailable in some test/story environments) **before** any callback fires, so the token never lingers in browser history.
   - Resolve `returnPath = isSafeReturnPath(returnCandidate) ? returnCandidate : defaultReturnPath`.
   - `await completeExternalLogin(token)` (from `useAuth()`); on success call `onComplete(returnPath)`; on failure, `console.error` the failure and call `onError('Sign-in failed: your session could not be established.')`.
3. Render: a simple `Card`/`CardHeader`/`CardTitle` ("Signing you in") / `CardDescription` (a `message` state string, defaulting to `'Signing you in...'`, updated to `'Sign-in failed. Redirecting...'` on the failure path before `onError` is called) — matching the reference's minimal "please wait" UI. Import `Card`, `CardContent`, `CardDescription`, `CardHeader`, `CardTitle` from `@moduleforge/core-gui`.

### Export wiring

Add `OidcCallbackPage` and `OidcCallbackPageProps` to `gui/src/index.ts` under the `// ─── Components ───` section, matching the existing export style.

### Ladle story (optional, style convention)

A story for this component is inherently awkward (it depends on `window.location.hash`/`.search` at mount time and a full `AuthProvider` + callback wiring) — `client-layout.tsx` is the existing precedent for a component with no story for similar reasons. You may add one that pre-seeds `window.location.hash` before mount to exercise the success path, or skip it; if you skip it, say so explicitly in your report rather than silently omitting it.

## Validation

1. `cd gui && bunx tsc --noEmit` (equivalent to `make lint.gui`). **Known pre-existing environment gap**: this will fail with `Cannot find module '@moduleforge/core-gui'` in any environment where `.yalc/` is not populated (see `plan/followups.yaml` items `ThVz`/`HSiS`/`VqCM`, and `.claude/CLAUDE.md`'s "Known gotchas"). If the **only** failures are unresolved `@moduleforge/core-gui` module errors, treat that as the known gap and say so in your report; do not attempt `yalc publish`/`yalc add`. Any other typecheck error must be fixed before reporting completion.
2. Re-read the new file against the numbered behavior list above; confirm the double-invoke guard, both parse branches (`error` query param vs. token fragment), both pure helper functions (with all validation branches), the history-strip-before-callback ordering, and both callback invocations (`onComplete`, `onError`) are present.
3. `grep -n "OidcCallbackPage" gui/src/index.ts` shows both the component and its props-type export.
4. Confirm the file imports nothing from `next/navigation` or any other router package.
5. Confirm `isSafeReturnPath` rejects all three of: an absolute-looking value without a leading `/`, a `//evil.com`-style protocol-relative value, and a value containing `\` or a `:` in its first segment — either via a quick manual trace of the logic or (if you choose to write one) a small inline check; this is a security-relevant validation and must not be weakened relative to the reference.

## Assumptions

- `@moduleforge/core-gui`'s `Card`/`CardHeader`/`CardTitle`/`CardContent`/`CardDescription` components exist with the same API this package's other consumers already assume (see `gui/src/components/client-layout.tsx` for a component with no Card usage, and the reference `app-mfdemo` pages for the Card composition pattern).
- The OIDC callback's redirect-with-fragment mechanics (`token`, `return` in the hash; `error` in the query string) are a stable, already-implemented API contract (`api/internal/handlers`), not something this task verifies server-side.

## References

- `/Users/zane/playground/moduleforge/app-mfdemo/src/app/auth/oidc/return/page.tsx` — reference implementation (source of truth for every behavior above).
- `docs/mod-users-spec.md` — use case 5 (OIDC login) and the "Open-redirect prevention" / "CSRF / state validation" security requirements.
- `docs/architecture.md` — Authentication flow section, OIDC step-by-step.
- `gui/src/lib/auth-context.tsx` — `useAuth()`, `completeExternalLogin`.
- `gui/src/components/require-auth.tsx` — established `AuthProvider`-dependent, callback-injection component precedent in this package.
