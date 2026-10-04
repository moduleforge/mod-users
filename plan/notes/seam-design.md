# users-gui Seam Design and Source Verification

## Purpose and scope

Records each seam's verification against `gui/src` source (paths are relative to `mod-users/gui/`), the design chosen, the alternatives rejected, and decisions the plan made on the user's behalf. Source for the seam list: `app-mfmanager/.flow/mfmanager-auth-vs-mod-users.md` (G1-G10). The authoritative interface summary lives in [`../overview.md`](../overview.md); this note holds the reasoning.

## Verification of the investigation's claims

| Claim | Verified | Evidence |
|---|---|---|
| G1: base URL computed once at module load, `""` falls back to `localhost:8080` | Yes | `src/lib/api.ts` bottom: `API_BASE_URL = typeof process !== 'undefined' && process.env.NEXT_PUBLIC_API_BASE_URL ? ... : window.__USERS_API_URL__ ? ... : 'http://localhost:8080'`; both tests are truthiness checks, so `""` is skipped. `api = createUsersClient({ baseUrl: API_BASE_URL })` captures it. |
| `API_BASE_URL` used outside `request()` | Yes (wider than the investigation said) | `login-form.tsx` (OIDC `start` URL), `fetchProviders()` in `api.ts`, `lib/oidc-config.ts` (3 sites), `lib/oidc-provider.ts` (4 sites + `testProviderURL`). All must read the configured value lazily. |
| G2: 401 hard-redirects to `/auth/login`, no return path | Yes | `createUsersClient().request()`: `localStorage.removeItem('auth_token'); window.location.href = '/auth/login'` unless `skipAuthRedirect`. |
| Same literal elsewhere | Yes | `AuthProvider.logout()` calls `navigate('/auth/login')` (`auth-context.tsx`); `SidebarNav` has an `href="/auth/login"` (admin chrome, out of scope); `OidcCallbackPage` docs show `/auth/login?error=`. |
| The 401 redirect also fires at `AuthProvider` mount | Yes | Mount effect calls `api.self.get()` without `skipAuthRedirect`, so a stale stored token hard-redirects even on a public page (the redirect clears the token first, so there is no loop; it does cost one reload). |
| G3: fixed key `auth_token`, no getter | Yes | `TOKEN_KEY` const in `auth-context.tsx`; literal in `api.ts` `getToken()` and the 401 branch. app-mftodo reads the same literal in `gui/src/lib/{actor,clients,tag-search,tag-templates}.ts`. |
| `AuthPage` cannot hide registration | Yes | `auth-page.tsx` always renders the register panel and "Create one" button. Also: neither `AuthPage` nor `LoginForm` has any "Forgot password?" entry point, so `ForgotPasswordPage` is unreachable from the stock login UI. |
| `./styles.css` export dangling | Yes | `package.json` exports `./styles.css` to `./dist/index.css`; the `build` script is `tsup` only and `docs/architecture.md` states the library ships no CSS. `.ladle/styles.css` (the only stylesheet) includes `@layer base` and a `body` rule and duplicates the whole token set, so shipping it would be harmful. |
| Router-agnostic | Yes | No `react-router`, `next/*`, or other router import anywhere in `src`. Navigation is injected (`onNavigate`, `onUnauthenticated`, `onAuthenticated`, `onSuccess`, `onComplete`, `onNavigateToLogin`). |
| Components touch `window`/`localStorage` only in effects/callbacks | Yes | `auth-context.tsx` accesses `localStorage` inside effects and callbacks; `oidc-callback-page.tsx` inside an effect; `API_BASE_URL` uses `typeof` guards. Next SSR of the client components should be safe; phase 2 proves it with a real `react-dom/server` render rather than assuming. |
| No `"use client"` in the bundle | Plausible (source files carry it; tsup bundling drops module-level directives per the investigation's count of 0) | Phase 2 re-verifies against the fresh build. |
| Existing session/login return support | Partly | `LoginForm.returnPath` only feeds the OIDC `start?return=` param. Local login return is the app's job via `onSuccess`/`onAuthenticated` (no argument is passed). `OidcCallbackPage.onComplete(returnPath)` receives a validated path; its `isSafeReturnPath` is module-private. |

## Design

### One module-level configuration, read lazily

New `src/lib/config.ts` holds all runtime configuration and the token helpers; `api.ts`, `auth-context.tsx`, `login-form.tsx`, `oidc-config.ts`, `oidc-provider.ts` import from it (no cycle: `config.ts` imports nothing from them). Every consumer reads the value **at call time**. That removes the import-order fragility app-mftodo works around with `configure-users-gui.ts` and makes the setup Next-app-router-safe: calling `configureUsersApi(...)` at the top of any client module (or in an effect before first use) is enough, because no request is issued before an effect or event handler runs.

Rejected: a provider prop (`<AuthProvider apiBaseUrl>`). The OIDC helper functions and `fetchProviders()` are plain functions, not components, so they cannot read React context; threading config through them would break their signatures.

### Base URL (G1)

Resolution precedence: `configureUsersApi({ baseUrl })` > `process.env.NEXT_PUBLIC_API_BASE_URL` when it is **defined** (including `""`) > `window.__USERS_API_URL__` when it is a string (including `""`) > `'http://localhost:8080'`. `""` means same-origin: requests go to the relative path `/v1/...` (the existing `${baseUrl}${path}` concatenation already yields that). The env expression must stay in its literal `process.env.NEXT_PUBLIC_API_BASE_URL` form so Next/webpack/Turbopack inlining keeps working (a dynamic `process.env[name]` lookup would silently stop being inlined).

Backward compatibility: only the `""`-env case changes (it used to be ignored; now it is honored as same-origin). An unset variable, a non-empty variable, and the `__USERS_API_URL__` escape hatch (app-mftodo, Vite: `process` is undefined in the browser) behave exactly as before. `API_BASE_URL` stays exported as a deprecated snapshot of the resolved default at module load; internal code no longer uses it. `api.baseUrl` becomes a getter so `client.baseUrl` reflects later configuration. `createUsersClient({ baseUrl })` with an explicit string stays static, as before (additionally it may accept a `() => string`, used by the singleton).

### Unauthenticated handling (G2)

Aligned with mod-core's wave-1 `unauthenticatedRedirectUrl` (same option name, same default `'/auth/login'`, same validation: a same-origin path starting with exactly one `/`, no backslash, no control characters, or an absolute `http:`/`https:` URL; anything else throws `TypeError` and applies none of the call's fields). Superset additions:

- `unauthenticatedReturnParam` (default `null`): when set, the default handler appends `?<param>=<encoded current pathname+search>` to the target (`&` if the target already has `?`). The hash is deliberately excluded (it can carry tokens). The target page owns validating the value (apps use their own sanitizer, or the newly exported `isSafeReturnPath`).
- Same-path guard (only when a return param is configured): if the current pathname already equals the redirect target's pathname on the same origin, the handler clears the token and does not navigate (prevents `/login?return=%2Flogin` and a pointless reload when the mount-time `/v1/self` check fails on the login page).
- `onUnauthenticated(ctx)` full override, with `ctx.returnPath` (current pathname+search; `'/'` outside a browser). **Deliberate difference from mod-core's handler:** users-gui always clears the stored token (under the configured key) *before* invoking a custom handler, because a surviving stale token would re-trigger the 401 on every subsequent request. mod-core's custom handler replaces the default entirely including the token removal. Flagged in the overview as an assumption.
- `AuthProvider` gains `loginPath?: string` (default `'/auth/login'`) used by `logout()` and the 401-in-`refreshUser` path, since `logout()` navigates through the injected `onNavigate`, not the redirect URL (an absolute cross-origin URL cannot be passed to a router `push`).
- `skipAuthRedirect: true` keeps meaning "no clear, no handler, just throw".

Why not delegate `request()` to core-gui's (mod-core's note suggests it as the long-term fix): larger refactor, needs core-gui changes users-gui cannot yet depend on (mod-core's wave 1 may not be merged when this lands), and changes the `ApiActionRequiredError` plumbing. Out of scope; this plan keeps `request()` local and aligns names so a later merge is mechanical. Followup HPJi (this repo) and ZyTU (app-mftodo) describe the same gap; this plan implements the mod-users half and the manager should close HPJi on merge.

### Token key (G3)

`configureUsersApi({ tokenStorageKey })` (default `'auth_token'`), plus exports `getStoredToken()`, `clearStoredToken()`, `getTokenStorageKey()` and `USERS_TOKEN_KEY` (the default constant). Making the key configurable, not only readable, answers the migration question without app-side work: MFManager can set `tokenStorageKey: 'mfmanager_session_token'`, so its existing sessions and its own `api-client.ts` (which reads and clears that key, including the label-cache hook) keep working untouched. The alternative (app adopts `auth_token`; one-time sign-out) remains available to the app. The key is read lazily on every access, so it must be configured before the `AuthProvider` mounts (module top of a client module is enough). Changing the key at runtime after a session exists is unsupported and documented as such.

### Components

- `AuthPage.allowRegistration` (default `true`): `false` removes the "Create one" button and the register panel and forces login mode even if `initialMode="register"`. This is a UI affordance only; the API's `/v1/auth/register` stays open (followup context from the investigation: no backend switch exists).
- `LoginForm.onForgotPassword` / `AuthPage.onForgotPassword` (optional): renders a "Forgot password?" text button when supplied; absent renders nothing, so existing consumers (app-mftodo, app-mfdemo) are unchanged.
- `isSafeReturnPath` moves to `src/lib/return-path.ts`, is imported by `OidcCallbackPage` (behavior identical) and exported for apps, so MFManager can validate `?return=` with the same rules rather than re-implementing them.

Revised (user decision: standard unless there is a design reason): `AuthPage`/`LoginForm` callbacks now receive the validated return path when `unauthenticatedReturnParam` is configured (task 007; opt-in, so default behavior is unchanged); `VerifyEmailPage` and `OidcConfigPage` (with `OidcSetupGate`) become standard pages (tasks 005, 006); `USERS_GUI_ROUTES` exports the standard paths. Design notes: the verify page is code-entry only because the backend emails a 6-digit code, not a link; every call that can legitimately return `401` for a wrong code passes `skipAuthRedirect`; the oidc-config page keeps the reference's use of `window.location`/`history.replaceState` inside effects only and delegates navigation to `onComplete`; `AuthProvider` skips navigation to the current path because `GET /v1/self` itself returns the `/oidc-config` action-required `503` when OIDC is unconfirmed.

Still not planned (listed so wave 2 does not assume them): an email-code link in `AuthPage`; `SidebarNav` changes and `ClientLayout` behavior changes (admin chrome, not used by MFManager; only the gate is extracted); a `/step-up` page.

### Styles export

Remove the dangling `./styles.css` export rather than building it: the repo's documented model (`docs/architecture.md`) is "consumers generate CSS by `@source`-scanning `dist/`", and the only existing stylesheet contains a `body`/`@layer base` rule and a full token duplicate that would clash with core-gui's `styles.css`/`tokens.css` (the investigation's G5 concern). Consumers need core-gui's CSS plus `@source` of users-gui's `dist/`. Nothing can resolve the export today, so removal cannot break a working consumer; grep of app-mftodo, app-mfdemo, app-mfmanager sources found no `users-gui/styles.css` import.

### Next.js compatibility conclusion

No router adapter or `navigate` prop is needed: the components are already router-agnostic (verified above) and app-mfdemo already runs them under Next 15. Remaining Next-specific needs are documentation plus verification: (1) import only from client components, because the built bundle carries no `"use client"` directive; (2) `useSearchParams` consumers need a `<Suspense>` boundary (app's page code); (3) call `configureUsersApi` from a client module. Adding a `"use client"` banner in `tsup.config.ts` was considered and **not** adopted by default: it would let server components import the library, but it would make non-component exports (`configureUsersApi`, `getStoredToken`, `createUsersClient`) client references that a server module could not call, and Vite/Rollup consumers (app-mftodo) emit "module level directive" warnings on pre-built ESM. Surfaced as an open question in the overview; the plan's phase-2 verification records the no-banner behavior.
