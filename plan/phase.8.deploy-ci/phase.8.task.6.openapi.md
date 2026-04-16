# Phase 8, Task 6 — OpenAPI contract + codegen verification

## Acceptance
- `api/openapi.yaml` (v3.1) covers every endpoint defined in Phases 3–6:
  - All `/v1/auth/*`
  - `/v1/self`, `/v1/self/apps`, `/v1/self/end-assumption`
  - `/v1/users` family
  - `/v1/apps` family
  - `/v1/audit` family
  - `/healthz`, `/readyz`
- Schema components for `User`, `Entity`, `App`, `AppMembership`, `AuditEntry`, `Error`.
- Auth scheme `bearerAuth` (Bearer JWT) declared globally; per-operation `security: []` for unauthenticated endpoints.
- `make api.spec.lint` runs spectral (or redocly) against the spec.
- `make api.client.gen` runs `oapi-codegen` to produce a typed Go client at `api-client/go` AND `openapi-typescript-codegen` for the TS client at `gui/api-client/`.
- CI's `contract-check` job (Task 8.5) asserts: spec is lint-clean AND `oapi-codegen --diff` reports no changes vs committed clients.
- `api/internal/handlers/*` files annotated (or referenced) so a future move to `huma`/`go-fuego` is straightforward — for v1 we maintain the spec by hand.

## How to verify
- `make api.spec.lint` clean.
- `make api.client.gen && git diff --exit-code` clean.
- Generated TS client compiles inside `gui/`.

## Notes
- Hand-maintained spec is fine for v1; revisit code-first generation in v2 if drift becomes painful.
