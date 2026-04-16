# Phase 6, Task 4 — Authorization scoping

## Context
Per CLAUDE.md: "users in the 'admin' group can do anything, all other users can only access their own data". Apps add the dimension that "their data" is per-app.

## Acceptance

`api/internal/auth/authz.go`:
- `Authz` helper exposing:
  - `IsAdmin(ctx) bool`
  - `IsSelf(ctx, targetUserUUID string) bool`
  - `HasAppRole(ctx, role string) bool`
  - `RequireAdminOrSelf(ctx, targetUserUUID string) error` — returns `ErrForbidden` (rendered as 403 by the response writer) on miss.
- All handlers that touch user data switch from inline `if !uc.IsAdmin && uc.User.UUID != target {…}` checks to using `Authz.RequireAdminOrSelf`.

Scoping audit:
- Walk every handler in Phases 3–5 and confirm the right guard. Document the matrix in `api/internal/auth/AUTHZ.md`:
  ```
  Endpoint                         | Public | Self | Admin | App-scoped
  GET /healthz                     |   ✓    |      |       |
  GET /v1/self                     |        |  ✓   |       |
  POST /v1/auth/*                  |   ✓    |      |       |
  GET /v1/users                    |        |      |   ✓   |  no (admin global)
  GET /v1/users/:uuid              |        |  ✓   |   ✓   |
  POST /v1/users/:uuid/assume      |        |      |   ✓   |
  GET /v1/users/:uuid/audit        |        |      |   ✓   |
  POST /v1/apps                    |        |      |   ✓   |
  ...                              |        |      |       |
  ```

## How to verify
- Add e2e tests covering:
  - Anonymous request to admin endpoint → 401.
  - User A request for User B's profile → 404 (NOT 403, to avoid existence enumeration).
  - User A request for User A's profile → 200.
  - Admin request for User B's profile → 200.
  - User without app membership requesting an app-scoped endpoint → 403.

## Notes
- Choose 404 over 403 for cross-user reads to prevent existence enumeration. For mutations, 403 is fine (user is admitting they tried).
- The `AUTHZ.md` matrix is the single source of truth; any new endpoint must add a row before merge.
