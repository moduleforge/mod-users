# Phase 9, Task 2 — GUI Provider Buttons + OAuth Return Page

**Agent**: node-dev
**Working directory**: task worktree (`users-module/worktree/phase-09-task-02-gui-provider-buttons/gui/`)
**Branch**: `phase-09-task-02-gui-provider-buttons`

## Context

Read `phase.9.google-sso.md` (same directory) for the architecture diagram.

This task implements the GUI half of the Google SSO flow, against the API contract defined in Task 9.1. You do not need to wait for 9.1 to merge — mock the API shape for local dev, and the integration will come together when Task 9.3 wires the stack.

## API contract you're coding against

**`GET /v1/auth/providers`** (unauth'd) → `200 application/json`:
```json
[{"id": "google", "display_name": "Google"}, {"id": "authelia", "display_name": "Authelia"}]
```
Empty array is valid — means no providers configured.

**`GET /v1/auth/oidc/{id}/start?return=<path>`** — opaque redirect. GUI sends the browser there as a full navigation, not a fetch.

**Callback target**: `GET /auth/oidc/return#token=<jwt>&return=<path>` — this is the GUI page you're building. Token and return path arrive in the URL **fragment**, not query string.

On error: `GET /auth/oidc/return?error=<message>` — error in query string (fragment is reserved for success token).

## Acceptance

### 1. Provider fetch (`gui/src/lib/api.ts` — UPDATE)

Add a new unauth'd function:

```ts
export interface OIDCProvider {
  id: string;
  display_name: string;
}

export async function fetchProviders(): Promise<OIDCProvider[]>
```

- Does NOT send `Authorization` header.
- On network error or non-200, returns `[]` and logs to console — login page must render even if the API is down (local-auth form still works).

### 2. Login page (`gui/src/app/auth/login/page.tsx` — UPDATE)

Current file renders local form + email-code link. Add:

- On mount (client-side `useEffect`), call `fetchProviders()`.
- If providers returned, render a section below the form and above the footer:
  - A divider with "or continue with".
  - One button per provider. Button label: `Sign in with {display_name}`.
  - Provider icon: for `google`, render the Google "G" logo (can use `react-icons/fc` `FcGoogle`, or inline SVG — node-dev's call, whichever is already in the dep tree or lighter to add). For `microsoft`, use a Microsoft logo the same way. For anything else, no icon; just text.
  - Click handler: `window.location.assign(\`/v1/auth/oidc/${id}/start?return=/profile\`)`.
- Loading state: while providers are fetching, don't render the section (no flash of empty space). Fetch is fast; just render nothing until the fetch resolves.

Do NOT split provider buttons into a separate component file. Keep them inline in `page.tsx` — they're ~15 lines of JSX.

### 3. OAuth return page (`gui/src/app/auth/oidc/return/page.tsx` — NEW)

Client component. On mount:

1. Parse `window.location.hash` (strip leading `#`, `URLSearchParams`).
2. Parse `window.location.search` for `error`.
3. If `error` present:
   - Redirect to `/auth/login` with an error toast/message. (Use whatever toast/error display the login page already uses — `ErrorMessage` component, probably passed via query param or a shared client store.)
4. If `token` present:
   - Validate it looks like a JWT (three base64 segments). On malformed, treat as error.
   - Call `auth-context.setTokenAndUser(token, null)` — actually, `auth-context` probably needs a small refactor. See below.
   - Remove hash from URL (`window.history.replaceState(null, '', window.location.pathname)`) to prevent the token persisting in browser history.
   - Read `return` from the hash (same hash); fall back to `/profile`. Validate it starts with `/` and doesn't contain `//` or `:` before the first `/` — reject open-redirect attempts, fall back to `/profile`.
   - `router.replace(returnPath)`.
5. While processing, show a minimal "Signing you in..." card so the user sees something if the browser is slow.

### 4. Auth context adjustment (`gui/src/lib/auth-context.tsx` — UPDATE)

Current `login(email, password)` encapsulates credential → token → self-fetch. The OAuth return page needs to inject an already-obtained token and trigger the same rehydration logic.

Add a new exported method on the context:

```ts
async completeExternalLogin(token: string): Promise<void>
```

Behavior:
- Store `token` in localStorage (same key as local login).
- Call the existing "fetch `/v1/self` with Bearer token" routine to hydrate the user.
- Update context state (token + user) the same way `login()` does on success.
- On failure (invalid token, /v1/self 401), clear localStorage and throw — caller (return page) displays error.

Refactor the internal helper so `login()` and `completeExternalLogin()` share the "set token then hydrate self" code path — do not duplicate.

### 5. Env var (`gui/.env.example`, `gui/next.config.ts` if applicable)

- No new `NEXT_PUBLIC_*` env vars required — provider list comes from the API at runtime.
- If there's an existing `NEXT_PUBLIC_API_BASE_URL` (or similar) it's already used by `api.ts` — reuse it.

### 6. Tests (`gui/src/app/auth/oidc/return/*.test.tsx` or equivalent)

- OAuth return page: test with mock `window.location` containing token-in-fragment; assert `completeExternalLogin` called and router redirected.
- Malformed token in fragment → redirects to login with error.
- `?error=access_denied` → redirects to login with error message visible.
- Open-redirect attempt (`return=//evil.com`) → falls back to `/profile`.

Use whatever testing setup the project already has. If none, add vitest + testing-library-react minimally; don't build out a full harness.

## Non-goals

- Do not build server-side provider rendering (no SSR fetch of `/v1/auth/providers` — client-side is fine and matches the rest of the app).
- Do not change local-auth form behavior.
- Do not introduce NextAuth or a similar library — the auth flow lives on the Go API.

## How to verify (local, after Task 9.1 merges)

1. `pnpm -F gui dev` with API running.
2. Set `AUTH_PROVIDER_AUTHELIA_CLIENT_ID=...` on API, restart.
3. Browse `/auth/login` → Authelia button visible.
4. Click → Authelia → return → `/profile` loaded with user name in header.

## Stop and ask if

- The existing auth-context makes it awkward to add `completeExternalLogin` without breaking `login` — flag before refactoring aggressively.
- The current GUI has no test setup at all and adding one seems out of scope — ask whether to skip tests or add a minimal harness.
