# 002 Expose emailVerifiedAt On Self

## Purpose and scope
Flow-accounts dnL0 (existing followup QCRD): `UserAccountSelf` from `/v1/self` lacks email-verified state, so pages cannot prompt before a verified-scope call is refused with `users.email_unverified`.

## Requirements
1. Backend: add nullable `emailVerifiedAt` to the `/v1/self` response (see handler wired near `api/cmd/server/main.go:566`; keep JSON naming consistent with the neighbouring fields and the OpenAPI/`api/openapi.yaml`).
2. GUI: add `emailVerifiedAt: string | null` to the `UserAccountSelf` type and `useAuth` exposure.
3. Docs; tests for verified and unverified accounts.

## Validation
- Go handler test for both states; GUI type/test updates.
- `cd api && go test ./... && cd ../gui && bun test && bun run typecheck`.

## Status
- Outcome: succeeded (2026-10-10).
- Backend: `api/internal/handlers/self.go` adds `email_verified_at` (timestamp or null) to the self response; `api/openapi.yaml` UserDetail documents it. JSON key is snake_case to match neighbouring fields (the task text wrote `emailVerifiedAt`).
- GUI: `UserAccountSelf.email_verified_at: string | null` in `gui/src/lib/api.ts`; `useAuth()` exposes `emailVerifiedAt` in `gui/src/lib/auth-context.tsx`.
- Tests: `api/internal/handlers/self_routes_test.go`, `gui/src/lib/auth-context.test.tsx`. Docs: `docs/mod-users-spec.md`.
- Validation: go test ./... pass; bun test 225 pass; typecheck pass.
