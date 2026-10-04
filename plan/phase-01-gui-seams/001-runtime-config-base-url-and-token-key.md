# Add configureUsersApi With Runtime Base URL and Token Key

## Purpose and scope

Seams G1 and G3 from [`../notes/seam-design.md`](../notes/seam-design.md). Introduce a single module-level runtime configuration (`gui/src/lib/config.ts`), read lazily at call time, covering the API base URL and the token storage key, and route every existing read of those through it. Scope: `gui/src/lib/` (`config.ts` new, `api.ts`, `auth-context.tsx`, `oidc-config.ts`, `oidc-provider.ts`), `gui/src/components/login-form.tsx`, `gui/src/index.ts`, and tests. Task 002 builds on the `config.ts` created here, so it runs after this task. No behavior change for existing consumers except the documented `""`-env case.

## Requirements

1. Create `gui/src/lib/config.ts` (imports nothing from the other lib modules, to avoid cycles) exporting:
   - `interface UsersApiConfig { baseUrl?: string; tokenStorageKey?: string }` (task 002 widens it; write it so adding fields is trivial).
   - `configureUsersApi(config: UsersApiConfig): void`. An omitted/`undefined` field leaves the current value unchanged. Validate before applying anything: `baseUrl` must be `''` (same-origin), an absolute `http:`/`https:` URL, or a path starting with exactly one `/` (no `//`, no backslash, no control characters), else throw `TypeError` and change nothing from that call; trailing slashes are stripped (`'https://a.test/'` becomes `'https://a.test'`; `'/'` becomes `''`). `tokenStorageKey` must be a non-empty string else `TypeError`.
   - `resetUsersApiConfig(): void` (restores defaults; documented as primarily a test helper).
   - `getApiBaseUrl(): string`: precedence is configured value, then `process.env.NEXT_PUBLIC_API_BASE_URL` when it is **defined, including `""`**, then `window.__USERS_API_URL__` when it is a string (including `""`), then `'http://localhost:8080'`. Keep the env read in the literal expression form `typeof process !== 'undefined' && process.env.NEXT_PUBLIC_API_BASE_URL !== undefined` so Next's build-time inlining keeps working; do not use `process.env[...]` dynamic access. Evaluate on every call (not cached).
   - `USERS_TOKEN_KEY = 'auth_token'` (default key constant), `getTokenStorageKey(): string`, `getStoredToken(): string | null` (returns `null` when `window`/`localStorage` is unavailable, i.e. SSR), `clearStoredToken(): void` (no-op without a window).
2. Refactor consumers to read lazily: in `api.ts`, `createUsersClient` accepts `baseUrl: string | (() => string)` (existing string callers unchanged) and resolves it per request; the returned `baseUrl` becomes a getter (type still `string`); `getToken()` uses `getStoredToken()`; the 401 branch uses `clearStoredToken()` (the redirect itself is task 002's; leave the `/auth/login` assignment exactly as is here); the module singleton `api` is created with `baseUrl: getApiBaseUrl` (the function reference); `fetchProviders()` uses `getApiBaseUrl()`. In `auth-context.tsx` replace the `TOKEN_KEY` constant and every `localStorage.getItem/setItem/removeItem(TOKEN_KEY)` with `getTokenStorageKey()`-based access (use a small local helper; setItem stays inside effects/callbacks). In `login-form.tsx`, `oidc-config.ts`, `oidc-provider.ts` replace each `API_BASE_URL` template use with `getApiBaseUrl()` evaluated at the call site (including `testProviderURL`).
3. Keep `export const API_BASE_URL` in `api.ts` (computed once via `getApiBaseUrl()` at module load, JSDoc `@deprecated` pointing at `getApiBaseUrl`/`configureUsersApi`) and keep `api`, `createUsersClient`, `ApiRequestError`, `ApiActionRequiredError`, `fetchProviders` exports unchanged.
4. Export from `index.ts`: `configureUsersApi`, `resetUsersApiConfig`, `getApiBaseUrl`, `getStoredToken`, `clearStoredToken`, `getTokenStorageKey`, `USERS_TOKEN_KEY`, and types `UsersApiConfig`.
5. Tests (bun test, happy-dom, existing style in `api.test.ts` / `auth-context.test.tsx`; add `config.test.ts` and extend those files), each restoring state via `resetUsersApiConfig()`, `localStorage.clear()` and the original `fetch` in `afterEach`:
   - Precedence: default is `http://localhost:8080` with nothing set; `window.__USERS_API_URL__ = ''` yields `''`; the env case by setting and deleting `process.env.NEXT_PUBLIC_API_BASE_URL` (`''` yields `''`, a URL yields the URL, unset falls through); `configureUsersApi({ baseUrl })` beats both.
   - Lazy evaluation: after `configureUsersApi({ baseUrl: '' })`, `api.auth.login(...)` and `fetchProviders()` call `fetch` with a URL beginning `/v1/` (not `http://localhost:8080`); reconfiguring to `https://x.test` changes the next call without re-importing anything.
   - Validation: `'//evil.test'`, `'javascript:alert(1)'`, `'ftp://x'`, `'a\\b'`, `'/a\\b'`, a value with a newline, non-string throw `TypeError` and leave prior config (including other fields of the same call) intact; `'/api/'` normalizes to `'/api'`.
   - Token key: with `configureUsersApi({ tokenStorageKey: 'custom_key' })`, a request sends the `Bearer` from `custom_key` and not from `auth_token`; `AuthProvider` mount, `login`, `logout`, `completeExternalLogin`, and the 401 branch read/write/clear only `custom_key` (extend `auth-context.test.tsx` cases rather than duplicating them); defaults still use `auth_token`; `getStoredToken()`/`clearStoredToken()` honor the key.
   - All pre-existing tests pass unchanged.

## Validation

- `cd gui && bun run typecheck && bun test` pass (needs `mod-core/gui` built; see `AGENTS.md` First-time setup and its known worktree-symlink gap).
- `grep -rn "API_BASE_URL" gui/src --include=*.ts --include=*.tsx` shows only the deprecated declaration in `api.ts`, its re-export in `index.ts`, tests, and no template-literal uses; `grep -rn "'auth_token'" gui/src` shows only `config.ts` and tests.
- `grep -n "process.env.NEXT_PUBLIC_API_BASE_URL" gui/src/lib/config.ts` shows the literal (non-computed) form.
- `git diff --stat` touches only files under `gui/src/` (plus nothing outside `gui/`).
- A scratch check (not committed) of `configureUsersApi({ baseUrl: '' })` followed by a stubbed `fetch` confirms same-origin relative URLs; the result is noted in the task report.

## Metadata

architectural_impact: true

## References

- [`../notes/seam-design.md`](../notes/seam-design.md) (G1, G3 sections), [`../overview.md`](../overview.md) (interface)
- `gui/src/lib/api.ts`, `gui/src/lib/auth-context.tsx`, `gui/src/lib/oidc-config.ts`, `gui/src/lib/oidc-provider.ts`, `gui/src/components/login-form.tsx`
- app-mftodo `gui/src/lib/configure-users-gui.ts` (the import-order workaround this makes unnecessary; read-only)

## Checkpoint hints

- After creating `config.ts` and its tests
- After refactoring `api.ts` consumers
- After refactoring `auth-context.tsx` and the OIDC helpers

## Status

- Outcome: succeeded (2026-10-04).
- Validation: `cd gui && bun run typecheck && bun test` passed (56 tests, 0 failures); greps clean (only deprecated `API_BASE_URL` in `api.ts`, its `index.ts` re-export; `'auth_token'` only in `config.ts` and tests); literal `process.env.NEXT_PUBLIC_API_BASE_URL` form confirmed; scratch same-origin check yielded `/v1/auth/providers`.
- Source: `gui/src/lib/config.ts` (new), `gui/src/lib/config.test.ts` (new), `gui/src/lib/api.ts`, `gui/src/lib/auth-context.tsx`, `gui/src/lib/oidc-config.ts`, `gui/src/lib/oidc-provider.ts`, `gui/src/components/login-form.tsx`, `gui/src/index.ts`, `gui/src/lib/auth-context.test.tsx`.
