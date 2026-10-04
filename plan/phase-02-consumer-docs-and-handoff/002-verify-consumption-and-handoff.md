# Verify Built Package Consumption and Finalize Wave-2 Hand-off

## Purpose and scope

After phase 1, prove the packaged `@moduleforge/users-gui` is consumable the way wave 2 (a Next.js 15 app-router app, no React Router) will consume it, and finalize the hand-off record. Depends on phase 1 (all tasks) and on task `001` (it type-checks the guide's snippets); not parallel-eligible. Scope: read-only verification (scratch files allowed under `gui/.scratch/`, never committed, deleted before finishing), and a final update of [`../notes/consumption-and-handoff.md`](../notes/consumption-and-handoff.md). Nothing outside `mod-users` is modified.

## Requirements

1. **Build**: with `mod-core/gui` built (from its real checkout; see AGENTS.md known gap), run `cd gui && bun run build`. Confirm `dist/index.mjs`, `dist/index.js`, `dist/index.d.ts` exist and `dist/index.d.ts` declares: `configureUsersApi`, `resetUsersApiConfig`, `getApiBaseUrl`, `getStoredToken`, `clearStoredToken`, `getTokenStorageKey`, `USERS_TOKEN_KEY`, `UsersApiConfig`, `UnauthenticatedContext`, `isSafeReturnPath`, and the `allowRegistration`/`onForgotPassword`/`loginPath` props; `AuthPage`, `LoginForm`, `AuthProvider`, `RequireAuth`, `OidcCallbackPage`, `ResetPasswordPage`, `ForgotPasswordPage`, `EmailCodePage`, `useAuth`, and the new `VerifyEmailPage`, `VerifyEmailPageProps`, `OidcConfigPage`, `OidcConfigPageProps`, `OidcSetupGate`, `OidcSetupGateProps`, `USERS_GUI_ROUTES`, with `onVerified`, `onComplete`, `redirectDelayMs` and the `returnPath` argument on `onAuthenticated`/`onSuccess` visible in the declarations. Confirm `package.json` has no `./styles.css` export.
2. **No router coupling**: `grep -E "react-router|next/" dist/index.mjs dist/index.js` finds no import of either (this covers the migrated OIDC config page and the verify-email page); list the bundle's external import specifiers and record them (expected: `react`, `react/jsx-runtime`, `@moduleforge/core-gui`, `radix-ui`, `lucide-react`, `class-variance-authority`, `clsx`, `tailwind-merge`). Also record whether `"use client"` appears in the bundle (expected: no; the guide tells consumers to import only from client modules).
3. **Server-render safety (no `window`)**: in `gui/.scratch/` write a plain script (run with `bun run`, so the `bunfig.toml` test preload and happy-dom are NOT active) that imports from the built `dist/index.mjs` and `react-dom/server` resolved from `gui/node_modules`, asserts `typeof window === 'undefined'`, calls `configureUsersApi({ baseUrl: '' })`, and `renderToString`s: `AuthProvider` wrapping `AuthPage` (default, `allowRegistration={false}`, with `onForgotPassword`), `LoginForm`, `ResetPasswordPage token="x"`, `ForgotPasswordPage`, `EmailCodePage`, `OidcCallbackPage`, `RequireAuth`, and the new `VerifyEmailPage` (inside `AuthProvider` and bare), `OidcConfigPage` (renders its loading state; no fetch during the server pass) and `OidcSetupGate` (loading state). All must render without throwing; the `allowRegistration={false}` output must not contain "Create one". Also assert `getApiBaseUrl() === ''` and `getStoredToken() === null` server-side.
4. **Consumer type-check**: a scratch `.tsx` shaped like wave 2's usage (module-top `configureUsersApi({ baseUrl: process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8090', tokenStorageKey: 'mfmanager_session_token', unauthenticatedRedirectUrl: USERS_GUI_ROUTES.login, unauthenticatedReturnParam: 'return' })`; `AuthProvider loginPath={USERS_GUI_ROUTES.login} onNavigate={...}`; a login page using `AuthPage allowRegistration={false} onForgotPassword onAuthenticated returnPath`; a reset page, OIDC return page, `RequireAuth onUnauthenticated`, a `/verify-email` page using `VerifyEmailPage onVerified`, and a `/oidc-config` page using `OidcConfigPage onComplete` wrapped by `OidcSetupGate`, with paths taken from `USERS_GUI_ROUTES`), type-checked against `dist/index.d.ts` with the repo's TypeScript (`bunx tsc --noEmit` with a scratch tsconfig extending `gui/tsconfig.json`). Also paste each code block from `gui/README.md` (task 001) into scratch files and type-check them; fix README snippets (the only file this task may edit besides the note) if they fail.
5. **Backward compatibility check**: run the unchanged pre-existing tests (`cd gui && bun test`) and confirm app-mftodo-style usage still compiles: a scratch file using `AuthProvider onNavigate`, `AuthPage onAuthenticated initialError returnPath`, `OidcCallbackPage`, `ClientLayout` with `currentPath onNavigateToConfig onNavigate LinkComponent` (app-mfdemo shape, unchanged props), and the `window.__USERS_API_URL__` pattern with no `configureUsersApi` call type-checks and `getApiBaseUrl()` returns the `window.location.origin` value set before first request.
6. **Hand-off record**: update `../notes/consumption-and-handoff.md`: replace the verification placeholder in "Exact hand-off wave 2 needs" item 3 with the verified result (export list including the new pages, scratch-check commands and pass/fail summaries, the `use client` finding, any deviation from the overview interface, which should be none), and leave the merge-SHA line as the manager-filled placeholder. Do not invent a SHA. Summaries only, no full command output.
7. Delete `gui/.scratch/` before finishing; confirm `git status` shows no untracked scratch files.

## Validation

- Every export named in requirement 1 is found by `grep` in `gui/dist/index.d.ts`.
- All scratch scripts and type-checks pass (or failures are reported with the fix applied in README/note only; a failure that needs a source change halts with a report instead).
- `cd gui && bun run typecheck && bun test` pass.
- `git status` shows changes only in `plan/notes/consumption-and-handoff.md` and (if snippets were fixed) `gui/README.md`; no scratch files remain.

## Metadata

architectural_impact: false

## Assumptions

- Phase 1 and the guide are available; `mod-core/gui` is built.

## References

- [`../notes/consumption-and-handoff.md`](../notes/consumption-and-handoff.md), [`../notes/embedding-and-routes.md`](../notes/embedding-and-routes.md), [`../overview.md`](../overview.md)
- mod-core `shared-home-switcher` plan `phase-02-consumption-handoff/001-verify-consumption-and-handoff.md` (the pattern mirrored; read-only)

## Status

- Outcome: succeeded (2026-10-04).
- Validation: all requirement-1 exports found in `gui/dist/index.d.ts`; no router imports and no `use client` in the bundle; SSR smoke script passed (no `window`); wave-2-shaped consumer and all 8 `gui/README.md` tsx snippets type-check clean (no README fix needed); backward-compat scratch passed; `cd gui && bun run typecheck && bun test` pass (185 tests).
- Deviation: bundle does not import `class-variance-authority` (expected in the external list); no interface deviation.
- Scratch lived in gitignored `gui/node_modules/.scratch-smoke/` and was removed. Logs in `.flow/validation-logs/`.
- Updated: `plan/notes/consumption-and-handoff.md` (verification result, current interface and trust notes; merge SHA left for the manager).
