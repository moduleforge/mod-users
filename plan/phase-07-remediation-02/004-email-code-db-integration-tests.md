# Add Postgres-Backed Integration Tests For Email Code Limiting

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates the findings `sE2h` and `R7WT`, each recorded as a finding in this plan's plan/findings.yaml. The work targets the plan branch plan/users-security-hardening and lands through the ordinary per-task loop.

Dependency: runs after documents 001, 002 and 003 (`001-cooldown-covers-exhausted-codes.md`, `002-single-use-code-consumption.md`, `003-allowlist-public-email-code-purpose.md`) so it covers the remediated SQL and handlers.

## Requirements

1. Add a build-tag-gated (`//go:build integration`) DB-backed test following the existing `api/internal/authz/*_integration_test.go` pattern and its throwaway-Postgres recipe, exercising the real `db.Querier` against applied migrations.
2. Cover:
   - `ClaimEmailCodeAttempt` under N concurrent goroutines never exceeds the max and consumes at the bound.
   - The cooldown query/insert (as remediated) blocks re-mint after exhaustion within the window and allows it afterwards.
   - Concurrent cooldown inserts yield one code.
   - Single-use consumption yields one success under concurrent correct submissions.
   - The exhaust-then-request flow end to end at the handler level, if feasible.
3. Run the suite once against a uniquely named throwaway `postgres:16` container started with `docker run` and removed afterwards. Never run `make dev.start`, `make dev.restart`, `make dev.stop`, or `clean.data`, and never use the shared `users-module-postgres` container.

## Validation

- Integration suite passes against the throwaway container. Report the container name, the run command, and its removal; the container is gone afterwards (`docker ps -a` shows no match).
- `cd api && go vet -tags integration ./...` and `go test ./...` (default tags, integration skipped) pass.

## Metadata

architectural_impact: false

## References

- finding `sE2h`
- finding `R7WT`
