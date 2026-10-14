# Bound Email Code Request Latency And Pre-Check Cooldown Before Hashing

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates findings `QGTV` and `Wr6I`, both recorded in this plan's `plan/findings.yaml`.

- `QGTV`: `EmailCodeRequest` waits for the full background work (lookup, bcrypt hash, insert, send) before answering, so response latency reveals whether an email exists or is inside its cooldown.
- `Wr6I`: `sendEmailCode` and `sendStepUpCode` generate and bcrypt-hash a code before discovering that the cooldown forbids issuing one, which wastes work and makes the cooldown path expensive.

The work targets the plan branch `plan/users-security-hardening` and lands through the ordinary per-task loop (implement, validate, review, commit on the plan branch). It changes handler and query code in `mod-users` only. No status code, response body, or route changes.

## Requirements

1. `EmailCodeRequest` answers `204` at the roughly 200 ms latency floor whether or not the background work (lookup, hash, insert, send) has finished. The background work continues on a context detached from request cancellation (for example `context.WithoutCancel`) so the email is still sent. Existing and unknown email addresses must remain indistinguishable by latency.
2. `sendEmailCode` and `sendStepUpCode` perform a cheap, indexed existence check for any-state codes within the cooldown window before generating and bcrypt-hashing a code. When such a code exists, they skip silently. `CreateEmailCodeWithCooldown` remains the authoritative atomic guard.
3. The pre-check query is added to `model/queries/email_codes.sql`. `model/db` is regenerated with sqlc, and the fakes are updated to match the same semantics.
4. No status code, response body, or route changes.

## Validation

1. Unit test: with a sender that blocks well past 200 ms, `EmailCodeRequest` returns `204` within about the floor, and the send still completes.
2. Unit test: a request inside the cooldown performs no bcrypt hash and no insert. The existing cooldown, concurrency, and allow-list tests still pass.
3. From `api/`: `go build ./...`, `go vet ./...`, `go test -race ./internal/handlers/...`, and `make lint` all pass. Rerunning sqlc produces no diff.

## References

- Finding `QGTV` in this plan's `plan/findings.yaml` (email code request latency reveals account existence).
- Finding `Wr6I` in this plan's `plan/findings.yaml` (cooldown is checked only after hashing and code generation).
