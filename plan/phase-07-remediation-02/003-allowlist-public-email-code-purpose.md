# Allow-List Purposes On Public Email Code Endpoints

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates the finding `8iUq`, recorded as a finding in this plan's plan/findings.yaml. The work targets the plan branch plan/users-security-hardening and lands through the ordinary per-task loop.

Dependency: independent of documents 001 and 002, but touches `emailcode.go`, so it runs after them.

## Requirements

1. `POST /v1/auth/email-code/request` and `/v1/auth/email-code/verify` accept only purpose `login` or `verify_email` (empty still defaults to `login`); `credential_change` and any other value are refused.
2. Refusal is enumeration-safe and uses existing response shapes: request keeps its uniform 204 behavior or returns the existing 400 `invalid_input` for a malformed body, independent of whether the email exists; verify returns the existing 400 `invalid_input` (or the existing 401) without touching the DB. No new status codes or bodies.
3. Step-up (`credential_change`) issuance and verification via the authenticated identities endpoints are unchanged.

## Validation

- Unit tests: request with `purpose=credential_change` creates no code and sends no email; verify with `purpose=credential_change` never calls `GetActiveEmailCode` or `ClaimEmailCodeAttempt`, and so cannot burn a step-up code's attempts; login and verify_email paths are unchanged.
- `cd api && go test -race ./internal/handlers/... && go vet ./... && make lint` pass.

## Metadata

architectural_impact: false

## References

- finding `8iUq`
