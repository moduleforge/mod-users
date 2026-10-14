# Document Email Code Attempt Limit, Cooldown, And Exhaustion-Boundary Behavior

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates findings `y3s6` and `33Bo`, both recorded in this plan's `plan/findings.yaml`.

- `y3s6`: the durable docs do not describe the email one-time code limits (5-attempt limit, 60 s any-state request cooldown, single-use consumption, public-endpoint purpose allow-list), and some of them still describe the superseded `GetLatestEmailCodeCreatedAt` cooldown and a single migration.
- `33Bo`: the consume step in `EmailCodeVerify` and `StepUpVerify` does not state the fail-closed behavior at the exhaustion boundary, where a correct guess holding an earlier attempt is refused when a concurrent final-attempt claim consumes the code.

The work targets the plan branch `plan/users-security-hardening` and lands through the ordinary per-task loop (implement, validate, review, commit on the plan branch). The Go change is comment-only.

## Requirements

1. Durable docs describe the email one-time code behavior:
   - `docs/architecture.md` (email one-time code section) and `docs/mod-users-spec.md` (security notes) state the 5-attempt limit.
   - They state the 60 s any-state request cooldown, enforced atomically by `create_email_code_with_cooldown`.
   - They state single-use consumption.
   - They state the public-endpoint purpose allow-list.
2. `AGENTS.md` (per-module migration range note) and `docs/architecture.md` no longer say that `mod-users` has a single migration. They list `0101` through `0103`, plus any migration added by this round (`0104` if the cooldown index migration from this round has landed).
3. `plan/notes/attempt-limiting-design.md` describes `CreateEmailCodeWithCooldown` and any-state semantics instead of `GetLatestEmailCodeCreatedAt`.
4. Code comments at the consume step in `EmailCodeVerify` and `StepUpVerify` state the fail-closed behavior at the exhaustion boundary: a correct guess that holds an earlier attempt is refused when a concurrent final-attempt claim consumes the code.

## Validation

1. A grep of `docs/` and `AGENTS.md` for `GetLatestEmailCodeCreatedAt` and for `single migration` returns no matches. The cooldown and attempt limit are described in the durable docs.
2. Documentation links resolve. No non-plan document links into `plan/`. From `api/`, `go vet ./...` passes, confirming the Go change is comment-only.

## References

- Finding `y3s6` in this plan's `plan/findings.yaml` (durable docs omit email code attempt limit and cooldown behavior).
- Finding `33Bo` in this plan's `plan/findings.yaml` (exhaustion-boundary fail-closed behavior is undocumented at the consume step).

## Status

Outcome: succeeded (2026-10-14). Validation: grep for `GetLatestEmailCodeCreatedAt` / `single migration` in `docs/` and `AGENTS.md` returns nothing; `go vet ./...` from `api/` passes. Changed: `AGENTS.md`, `docs/architecture.md`, `docs/mod-users-spec.md`, `plan/notes/attempt-limiting-design.md`, and comment-only edits in `api/internal/handlers/auth/emailcode.go` and `api/internal/handlers/identities.go`.
