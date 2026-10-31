# Allow X-Setup-Token In CORS And Correct CORS Docs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates findings gz8M, EqWE, Yo85, and 7Yab. It covers the CORS allowed-header list for the setup-token routes, the CORS guide's description of credentialed starts and setup-token headers, and the stale Known limits entries in the consumer upgrade notes.

The work targets the plan branch plan/identity-model-impl and lands through the ordinary per-task loop.

## Requirements

1. Add "X-Setup-Token" to AllowedHeaders in corsOptions (api/internal/server/server.go). Change no other CORS option; AllowCredentials stays as is. (gz8M)
2. Extend TestCorsOptions_MethodsAndHeaders in api/internal/server/cors_test.go to assert that X-Setup-Token is an allowed header. (gz8M)
3. In docs/cors.md (around lines 11-14), replace "the sign-in and link authorize-URL starts" with "the link and step-up authorize-URL starts", and add that the sign-in start is a plain GET navigation that needs no credentials. (Yo85, EqWE)
4. In docs/cors.md, add that a credentialed start needs an explicit CORS_ORIGINS entry even in DEPLOY_MODE=local, because the local wildcard never sends Allow-Credentials; and that the setup-token routes (POST /v1/bootstrap and the /v1/oidc-config writes) send X-Setup-Token, which is in the allowed headers. (EqWE, gz8M)
5. In docs/consumer-upgrade-notes.md Known limits, remove or rewrite the line (around line 270) claiming the CORS middleware still lists X-Step-Up-Token, and the line just above it (around line 269) claiming the usersservice facade lacks IssueAPIKeyInput, IssuedAPIKey, and ProfileSummary, since those aliases now exist. (7Yab)

## Validation

1. Run `go test ./internal/server/...` in api/ and confirm it passes. Confirm the extended test fails if X-Setup-Token is removed from AllowedHeaders.
2. Run `grep -n 'sign-in and link' docs/cors.md` and confirm it returns nothing. Confirm docs/cors.md names the link and step-up starts, consistent with docs/architecture/sessions-and-tokens.md.
3. Confirm docs/consumer-upgrade-notes.md Known limits no longer claims that X-Step-Up-Token is listed, or that the facade lacks the three aliases.
4. Confirm `git diff` touches only api/internal/server/server.go, api/internal/server/cors_test.go, docs/cors.md, and docs/consumer-upgrade-notes.md.

## References

- Finding gz8M in this plan's `plan/findings.yaml`.
- Finding EqWE in this plan's `plan/findings.yaml`.
- Finding Yo85 in this plan's `plan/findings.yaml`.
- Finding 7Yab in this plan's `plan/findings.yaml`.

## Status

Outcome: succeeded (2026-10-31). Validation: `go test ./internal/server/...` passes and fails with X-Setup-Token removed; `grep 'sign-in and link' docs/cors.md` is empty; both stale Known limits lines removed. Files: api/internal/server/server.go, api/internal/server/cors_test.go, docs/cors.md, docs/consumer-upgrade-notes.md.
