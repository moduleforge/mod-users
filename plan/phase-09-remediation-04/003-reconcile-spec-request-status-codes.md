# Reconcile Spec Anti-Enumeration Status Codes With The 204 Handlers

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates finding `BM3K` in this plan's `plan/findings.yaml`: `docs/mod-users-spec.md` states a 202 status for the email-code request endpoint and for the password-reset request, while the handlers answer 204 for both.

The work targets the plan branch `plan/users-security-hardening` and lands through the ordinary per-task loop (implement, validate, review, and merge as for any other plan task). It is limited to correcting the status-code claims in the spec. No handler or test change is in scope.

## Requirements

1. `docs/mod-users-spec.md` states 204 (not 202) for the email-code request endpoint everywhere it states a status, at approximately lines 50, 216, 260, and 343.
2. The same sentences' password-reset request status is also corrected to 204, matching `api/internal/handlers/auth/reset.go`.
3. No handler or test changes are made, and no link into `plan/` is added to the spec.

## Validation

1. Grep for `202` in `docs/mod-users-spec.md`. No remaining claim about the email-code request or password-reset request endpoints may remain.
2. The handlers still write `http.StatusNoContent` (in `emailcode.go` and `reset.go`), and their tests still assert 204.

## References

1. Finding `BM3K` in this plan's `plan/findings.yaml`: the spec states 202 for the email-code and password-reset request endpoints, while the handlers answer 204.

## Status

- Outcome: succeeded (2026-10-14).
- Changed `202` to `204` at lines 50, 60, 216, 260, 262, and 343 of `docs/mod-users-spec.md`.
- Validation: no `202` remains in the spec; handlers `emailcode.go` and `reset.go` still write `http.StatusNoContent`; no handler or test changes.
