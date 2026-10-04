# Write the users-gui Consumer Integration Guide

## Purpose and scope

Create `gui/README.md` (none exists today; `package.json`'s `files` is `dist`, so it is repo documentation) as the integration guide for apps embedding `@moduleforge/users-gui`, covering the seams added in phase 1, the route expectations, and Next.js usage, per [`../notes/embedding-and-routes.md`](../notes/embedding-and-routes.md) and [`../notes/seam-design.md`](../notes/seam-design.md). Depends on phase 1 (all tasks, including 005-007: the new pages must be documented from the landed code); task `002` depends on this task. Scope: `gui/README.md` only, plus one pointer line in the repo-root `README.md`'s users-gui paragraph (around lines 24-29) linking to it. Do not edit `AGENTS.md` or `docs/architecture.md` (phase 3 owns those).

## Requirements

1. Sections: what the library is (router-agnostic client components plus an API client; no routes, no CSS file); consumption (bun workspace member, never published, build order: `mod-core/gui` then `gui/`, pointer to AGENTS.md First-time setup, `@source` of both packages' `dist/` plus core-gui's `styles.css`/`tokens.css`; mention there is no `./styles.css` export); **Runtime configuration** (`configureUsersApi` field table with defaults and validation, `resetUsersApiConfig`, `getApiBaseUrl`, token helpers and key semantics including "configure before the `AuthProvider` mounts; do not change the key mid-session", base-URL precedence, `""` as same-origin, the deprecated `API_BASE_URL`); **Unauthenticated handling** (default behavior unchanged; `unauthenticatedRedirectUrl`, `unauthenticatedReturnParam`, `onUnauthenticated` including the token-always-cleared note and the same-path guard; a one-line note that credential-failure 401s (wrong password, wrong email code, bad reset token) show inline errors and never trigger the redirect or `onUnauthenticated`, task 008; `AuthProvider loginPath`; why both the fetch-layer and provider settings exist); **Components and props** (a table for `AuthPage`, `LoginForm`, `OidcCallbackPage`, `ResetPasswordPage`, `ForgotPasswordPage`, `EmailCodePage`, `RequireAuth`, `AuthProvider`, `ClientLayout`, plus the new `VerifyEmailPage`, `OidcConfigPage`, `OidcSetupGate`, the new `allowRegistration`/`onForgotPassword`/`isSafeReturnPath` and the widened `onAuthenticated(returnPath)`/`onSuccess(returnPath)` callbacks, and the `USERS_GUI_ROUTES` constant); **Routes** (the table from `embedding-and-routes.md`: the standard path set from `USERS_GUI_ROUTES`, the backend-fixed `/reset-password`, `/verify-email` and `/oidc-config` with the action-required `path` contract that makes `AuthProvider` navigate there, the `AUTH_FRONTEND_RETURN_URL` return route and the fragment-based token contract); **Standard pages** (a short section per new page: `VerifyEmailPage` (code-entry flow, props, how it gets the email, `onVerified`) and `OidcConfigPage` (setup-token and admin modes, `onComplete` with the full-page-navigation guidance, `OidcSetupGate` vs `ClientLayout`, mounting outside or inside `AuthProvider`)); **Next.js (app router)** (client-component-only imports, `<Suspense>` around `useSearchParams`, single React, module-top `configureUsersApi`, a full worked example of `AppShell` + login page + reset page + OIDC return page + `/verify-email` page + `/oidc-config` page using `next/navigation`); **React Router** (short example mirroring app-mftodo, noting `configureUsersApi({ baseUrl: window.location.origin })` replaces the import-order trick and `unauthenticatedRedirectUrl: '/login'` replaces the `/auth/login` alias route); **Migration notes** (everything is additive; the behavior changes are `NEXT_PUBLIC_API_BASE_URL=""` now meaning same-origin and wrong credentials now showing an inline error instead of redirecting).
2. Every code snippet must be real, type-correct TypeScript against the built `dist/index.d.ts` (paste-checked in task 002); use only exports that exist after phase 1.
3. State precisely what is **not** provided so integrators do not assume it: local-login return handling is opt-in via `unauthenticatedReturnParam` (the callback receives the validated return path or `null`; without that setting the app reads its own `?return=` as before), no email-code link on `AuthPage`, no `/step-up` page (backend path `/step-up` is not mounted by this plan; followup filed), no automatic detection of an already-verified email in `VerifyEmailPage`, `ClientLayout`/`SidebarNav` are mod-users admin chrome and keep the `/auth/login` literal, registration hiding is UI-only (the API register endpoint stays open).
4. Follow the repo's markdown conventions (check `docs/` and AGENTS.md for style); no emojis.

## Validation

- `gui/README.md` exists and contains each section above; `grep -c "configureUsersApi" gui/README.md` is greater than zero and every config field name appears in the field table.
- Every identifier used in the snippets appears in `gui/src/index.ts` (spot-check with a grep loop over the snippet's imports).
- Root `README.md` has the pointer line; `git diff --stat` shows only `gui/README.md` and `README.md`.

## Metadata

architectural_impact: false

## References

- [`../notes/embedding-and-routes.md`](../notes/embedding-and-routes.md), [`../notes/seam-design.md`](../notes/seam-design.md), [`../overview.md`](../overview.md)
- app-mfdemo `src/app/auth/*` and app-mftodo `gui/src/App.tsx`, `routes/LoginPage.tsx` (real consumer usage; read-only)
- `gui/src/index.ts`, `gui/src/components/{verify-email-page,oidc-config-page,oidc-setup-gate}.tsx`, `gui/src/lib/routes.ts`

## Status

- Outcome: succeeded (2026-10-04).
- Created `gui/README.md` against the landed code (config.ts, return-path.ts, api.ts, auth-context.tsx, auth components, VerifyEmailPage, USERS_GUI_ROUTES, OidcConfigPage/OidcSetupGate, index.ts) including the trust and known-limitation notes from review; added one pointer line to `README.md`.
- Validation: config field names each present in the field table; `configureUsersApi` appears 9 times; every snippet import (AuthProvider, OidcSetupGate, USERS_GUI_ROUTES, configureUsersApi, AuthPage, ResetPasswordPage, OidcCallbackPage, VerifyEmailPage, OidcConfigPage) is exported from `gui/src/index.ts`; diff touches only `gui/README.md` and `README.md` (plus this doc). Snippets are not yet type-checked against `dist/index.d.ts` (task 002).
