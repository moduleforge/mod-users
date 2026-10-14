# Enforce Single-Use Consumption On Email Code Success

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates the findings `1Ki1` and `CR1R`, each recorded as a finding in this plan's plan/findings.yaml. The work targets the plan branch plan/users-security-hardening and lands through the ordinary per-task loop.

Dependency: runs after document 001 (`001-cooldown-covers-exhausted-codes.md`) because both edit the same files, so they run sequentially.

## Requirements

1. On a matching code, `EmailCodeVerify` and `StepUpVerify` grant success only to the request that actually consumes the code. The consume step reports whether it affected a row (for example `ConsumeEmailCode` as `:execrows` or a `RETURNING :one`), and a request that did not consume answers with the existing 401 ("invalid or expired code" / `apiresp.ErrUnauthenticated`).
2. A correct guess on the final (5th) attempt still succeeds exactly once: when the claim itself consumed the code at the bound, the single claimer of that attempt is treated as the consumer (the claim's returned `consumed_at` / `failed_attempts` identifies it), so the consume no-op does not turn it into a 401.
3. No status code, body, or route changes. `model/db` is regenerated via sqlc, and fakes are updated with matching semantics.

## Validation

- Unit test (both endpoints): two concurrent correct submissions yield exactly one success and one 401 (run with `-race`).
- Unit test (both endpoints): 4 wrong guesses then the correct code on the 5th attempt succeeds; a 6th submission returns 401.
- `cd api && go test -race ./internal/handlers/... && go vet ./... && make lint` pass; sqlc rerun yields no diff.

## Metadata

architectural_impact: false

## References

- finding `1Ki1`
- finding `CR1R`

## Status

- Outcome: succeeded (2026-10-14).
- `ConsumeEmailCode` is now `:execrows` (`model/queries/email_codes.sql`, regenerated `model/db`). `EmailCodeVerify` (`api/internal/handlers/auth/emailcode.go`) and `StepUpVerify` (`api/internal/handlers/identities.go`) grant success only if the claim itself consumed the code (`claimed.ConsumedAt != nil`) or the consume affected one row; zero rows answers the existing 401. A consume error now fails closed with a 500 instead of being logged and ignored.
- Fakes updated with matching semantics; new tests cover concurrent single-success, final-attempt success then 401, and consume-no-row 401 for both endpoints.
- Validation: `go test -race ./...`, `go vet`, `make lint` pass; sqlc rerun yields no further diff.
