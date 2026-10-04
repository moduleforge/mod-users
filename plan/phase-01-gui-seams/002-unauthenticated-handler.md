# Configurable Unauthenticated Handler With Return Path and Logout Target

## Purpose and scope

Seam G2 (mod-users followup `HPJi`, app-mftodo `ZyTU`) from [`../notes/seam-design.md`](../notes/seam-design.md). Make the 401 behavior of `request()` configurable (redirect target, optional return-path preservation, or a full custom handler) and make `AuthProvider.logout()`'s navigation target configurable, with the default byte-for-byte identical to today. Depends on task `001` (extends `config.ts`). Scope: `gui/src/lib/config.ts`, `gui/src/lib/api.ts`, `gui/src/lib/auth-context.tsx`, `gui/src/index.ts`, tests. Parallel-eligible with `003` and `004`; `index.ts` edits are adjacent-line additions (resolve trivially if merging).

## Requirements

1. Extend `UsersApiConfig` / `configureUsersApi` (same omitted-means-unchanged and validate-before-apply semantics as task 001; a `TypeError` rejects the whole call):
   - `unauthenticatedRedirectUrl?: string`, default `'/auth/login'`. Valid: a path starting with exactly one `/` (no `//`, no backslash, no control characters) or an absolute `http:`/`https:` URL. Same name and rule as mod-core's `configureApiClient({ unauthenticatedRedirectUrl })`.
   - `unauthenticatedReturnParam?: string | null`, default `null` (no return path appended). A non-null value must match `/^[A-Za-z0-9_-]+$/` else `TypeError`; `null` explicitly disables.
   - `onUnauthenticated?: ((ctx: UnauthenticatedContext) => void) | null`; `null` restores the default handler. Export `interface UnauthenticatedContext { returnPath: string }` where `returnPath` is `location.pathname + location.search` (never the hash), or `'/'` outside a browser or when that value fails `isSafeReturnPath`-style safety (leading single `/`, no `//` prefix, no backslash, no control chars; implement a small internal check here: task 003 later moves the exported `isSafeReturnPath` into `lib/return-path.ts`, and the implementer of whichever lands second swaps this internal check for it).
   - `resetUsersApiConfig()` (task 001) also resets these fields.
2. Replace the inline 401 handling in `request()` with a call to a shared `handleUnauthenticated()` in `config.ts` or `api.ts`: `skipAuthRedirect: true` still means no token clear and no handler (still throws `ApiRequestError('unauthenticated', ...)`); otherwise, in a browser: always `clearStoredToken()` first (under the configured key; deliberate difference from mod-core, documented in `seam-design.md`), then if a custom `onUnauthenticated` is set call it with the context, else run the default handler: build the target (`unauthenticatedRedirectUrl`; when `unauthenticatedReturnParam` is set, append `?<param>=<encodeURIComponent(returnPath)>`, or `&` if the target already contains `?`), apply the same-path guard (only when a return param is configured: if the target is a path, or an absolute URL on `window.location.origin`, whose pathname equals the current pathname, do not navigate), then `window.location.href = target`. With no new option configured the default must still do exactly: remove the token, `window.location.href = '/auth/login'`. SSR (no `window`): no-op, throw as before. The throw after the handler is unconditional, as today.
3. `AuthProvider` gains `loginPath?: string` (default `'/auth/login'`, a site-relative path validated lazily like other paths; an invalid value falls back to the default with a `console.error`) used by `logout()` and, through it, the 401 branch of `refreshUser`. `ClientLayout` is not changed. Document that the provider navigates through `onNavigate` while the fetch-layer 401 handler navigates through `window.location`, so apps with a router should set both `loginPath` and `unauthenticatedRedirectUrl` to their login route.
4. Export from `index.ts`: `UnauthenticatedContext` type (and ensure `UsersApiConfig` includes the new fields).
5. Tests (existing bun/happy-dom style; stub `window.location` via a replaceable stub with restoration in `afterEach`, following any pattern already in the gui tests, else define a minimal one):
   - Default 401 clears `auth_token` and sets `location.href` to `/auth/login`; no return param appended; identical to the pre-change behavior.
   - `unauthenticatedRedirectUrl: '/login'` navigates to `/login`; with `unauthenticatedReturnParam: 'return'` and current location `/deployments/3?tab=a#frag` the href is `/login?return=%2Fdeployments%2F3%3Ftab%3Da` (hash dropped); a target containing `?` uses `&`; an absolute `https://manager.example.test/login` target works.
   - Same-path guard: at location `/login` with the return param configured, the token is cleared and `location.href` is not assigned; without a return param the default still navigates (unchanged behavior).
   - A custom `onUnauthenticated` receives `{ returnPath }`, is invoked after the token is cleared, and the redirect URL is not used; `null` restores the default.
   - `skipAuthRedirect: true`: no clear, no handler, still throws `ApiRequestError` with status 401 (extend the existing coverage rather than duplicate).
   - Config key honored: with a custom `tokenStorageKey` the clear targets that key.
   - Validation: `'//evil.test'`, `'javascript:alert(1)'`, `'login'`, `'/a\\b'`, `'ftp://x'`, newline value, bad return-param (`'a b'`, `''`), throw `TypeError` and leave prior config intact (including the other fields of the same call).
   - `AuthProvider`: `logout()` navigates to `/auth/login` by default and to the configured `loginPath`; the 401 path through `refreshUser` does the same; existing `auth-context.test.tsx` assertions on `/auth/login` stay as the default-case checks.

## Validation

- `cd gui && bun run typecheck && bun test` pass, including all pre-existing tests unchanged.
- `grep -n "auth/login" gui/src/lib gui/src/components/oidc-callback-page.tsx` shows the literal only as defaults (config/auth-context) and doc comments; `sidebar-nav.tsx` is intentionally untouched.
- `git diff --stat` touches only `gui/src/` files.
- Calling no configure function leaves behavior exactly as before (covered by the default tests).

## Metadata

architectural_impact: true

## Assumptions

- Task 001 has landed: `config.ts` exists with `configureUsersApi`, `resetUsersApiConfig`, `getTokenStorageKey`, `clearStoredToken`.

## References

- [`../notes/seam-design.md`](../notes/seam-design.md) (G2 section), [`../overview.md`](../overview.md)
- mod-core `shared-home-switcher` plan, `phase-01-switcher-component/002-unauthenticated-redirect-config.md` (read-only; the option name and validation to match)
- `gui/src/lib/api.ts` (`request()` 401 branch), `gui/src/lib/auth-context.tsx` (`logout`)
- Followups `HPJi` (this repo), `ZyTU` (app-mftodo)

## Checkpoint hints

- After extending the config module and its validation tests
- After rewiring the `request()` 401 branch and its tests
- After the `AuthProvider loginPath` change and tests

## Status

- Outcome: succeeded (2026-10-04).
- Validation: `cd gui && bun run typecheck && bun test` pass (87 tests, 0 fail); `auth/login` literal appears only as defaults and doc comments; diff touches only `gui/src/` (plus this doc).
- Files: `gui/src/lib/config.ts` (new options, validation, `handleUnauthenticated`, `isSafeSitePath`), `gui/src/lib/api.ts`, `gui/src/lib/auth-context.tsx` (`loginPath`), `gui/src/index.ts`, `gui/src/lib/unauthenticated.test.ts` (new), `gui/src/lib/auth-context.test.tsx`.
