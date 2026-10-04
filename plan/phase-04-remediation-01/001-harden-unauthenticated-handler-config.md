# Unify Return-Path Validation, Guard onUnauthenticated, and Document Config Trust

## Purpose and scope

Remediates findings V6al, dYMj and X1dW in `gui/src/lib/config.ts` and its neighbours: the 401 return value written by the unauthenticated handler and the value accepted by the return-path reader use diverging predicates; a throwing consumer `onUnauthenticated` masks the 401; and the trust assumptions of the base-URL fallbacks and the cross-origin redirect target are undocumented. Work targets the plan branch `plan/users-gui-integration-seams` and lands through the ordinary per-task loop.

## Requirements

1. The 401 return value written by `handleUnauthenticated`/`currentReturnPath` and the value accepted by `readReturnPath`/`OidcCallbackPage` use one predicate, so any value the writer appends is one the reader accepts. `config.ts` must keep importing nothing from other lib modules: define the full predicate (including the colon-in-first-segment rule) in `config.ts` and keep `isSafeReturnPath` exported from `return-path.ts` and `index.ts` with an unchanged public signature, for example as a re-export or thin wrapper. Decide and document whether `loginPath`/`unauthenticatedRedirectUrl` validation (currently `isSafeSitePath`) also adopts the colon rule. Either is acceptable, but no second divergent return-path rule may remain.
2. Keep the full existing test tables for both `isSafeSitePath` and `isSafeReturnPath` behaviours, and add a round-trip test: for a page such as `/team:alpha/x`, `handleUnauthenticated` with `unauthenticatedReturnParam` set never writes a return value that `readReturnPath` rejects.
3. A custom `onUnauthenticated` that throws no longer replaces the 401. `request()` still throws `ApiRequestError` with status 401, the stored token is still cleared, and the handler's error is logged (`console.error`) rather than swallowed silently. `AuthProvider.refreshUser`'s 401 branch, `logout()`, is still reached.
4. Documentation only, no behaviour change: JSDoc on `getApiBaseUrl`/`UsersApiConfig.baseUrl` states that the `NEXT_PUBLIC_API_BASE_URL` and `window.__USERS_API_URL__` fallbacks are trusted deploy-time inputs, used without the validation `configureUsersApi` applies, and that the bearer token is sent to them. JSDoc on `unauthenticatedRedirectUrl`/`unauthenticatedReturnParam` states that an absolute URL target combined with a return param sends the current pathname+search to that origin.
5. Backward compatibility: with no configuration call, behaviour for existing consumers is unchanged.

## Validation

1. `bun test` in `gui/` passes, including the existing `config.test.ts`, `return-path.test.ts`, `unauthenticated.test.ts`, `api.test.ts` and `auth-context.test.tsx` tables.
2. A new test configures `onUnauthenticated` to throw and asserts `request()` rejects with `ApiRequestError` status 401, the token key is removed from localStorage, and `console.error` was called.
3. A new round-trip test asserts that a written return value for a colon-in-first-segment path is either accepted by `readReturnPath` or never written (falls back consistently).
4. The gui typecheck passes (`cd gui && bun run typecheck`; the gui has no `lint` script). The JSDoc for the documentation requirement is present on the named symbols, and the code diff for that requirement contains no executable change.

## References

- Finding V6al in this plan's `plan/findings.yaml` ("Duplicate site-path safety check").
- Finding dYMj in this plan's `plan/findings.yaml` ("Throwing onUnauthenticated masks the 401").
- Finding X1dW in this plan's `plan/findings.yaml` ("Base URL fallbacks and redirect hardening").
