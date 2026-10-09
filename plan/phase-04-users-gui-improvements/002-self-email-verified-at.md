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
