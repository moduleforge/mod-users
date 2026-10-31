# Align CORS Methods Headers And Docs With Routes

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates findings BNNN (CORS allowed methods omit PATCH), ndUS (CORS allowed headers include stale X-Step-Up-Token), and fb9H (CORS docs omit the credentials exception for OIDC start POSTs).

The work targets the plan branch plan/identity-model-impl and lands through the ordinary per-task loop.

## Requirements

1. In `corsOptions` in `api/internal/server/server.go`, add PATCH to `AllowedMethods` (BNNN).
2. In `corsOptions`, remove `X-Step-Up-Token` from `AllowedHeaders` (ndUS).
3. `api/internal/server/cors_test.go` asserts that PATCH is an allowed method and that X-Step-Up-Token is not an allowed header.
4. In `docs/cors.md`, line 11 keeps the Bearer-only statement but adds the exception: the two OIDC start POSTs (sign-in and link authorize-URL starts) send `credentials: 'include'` for the `oidc_state` cookie, so a cross-origin front end must be in the explicit `CORS_ORIGINS` list. Link to `architecture/sessions-and-tokens.md#client-contract` (fb9H).
5. Do not change `AllowCredentials`; that is a separate open decision (x3It, promoted to the project backlog).

## Validation

1. `go test ./internal/server/...` passes in `api/`.
2. `grep -rn 'X-Step-Up-Token' api/internal` returns nothing.
3. `docs/cors.md` names the `credentials: 'include'` exception, and the link resolves to an existing heading.

## References

- Finding BNNN in this plan's `plan/findings.yaml`.
- Finding ndUS in this plan's `plan/findings.yaml`.
- Finding fb9H in this plan's `plan/findings.yaml`.

## Status

succeeded (2026-10-31). PATCH added, stale step-up header removed from corsOptions in `api/internal/server/server.go`; test in `api/internal/server/cors_test.go`; `docs/cors.md` documents the credentials exception. X-Setup-Token not added (architecture docs do not require it). Validation: go test passed, grep empty, link target exists.
