# Exclude Disabled Principals From The Last Operator Admin Count

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `DO7p` (security, priority medium). It carries a required security review. The remaining-operator-admin count in `requireNotLastOperatorAdmin` counts administrators whose principal is disabled, so the operator party can be left with no usable administrator, and the guard's boundary is not stated. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop. This task runs after task 003, which changed the same function; the suspension and EndInTx coverage added there is preserved.

## Requirements

1. The remaining-operator-admin count in `requireNotLastOperatorAdmin` (`api/internal/service/profile_last_admin.go`) counts only active operator-party profiles whose principal is not disabled (`principals.disabled_at IS NULL`).
2. The doc comment of `requireNotLastOperatorAdmin` states the guard's boundary: it does not cover revocation of a wildcard-`manage` grant in mod-authz, and it does not cover gate-locked administrators (administrators whose profile the party gate would lock out).
3. The change builds on task 003's changes to the same function. The suspension and EndInTx coverage introduced there is not undone.

## Validation

1. An integration test: with two wildcard-`manage` operator administrators, one of whose principals is disabled, ending or leaving the other is refused with 409 `users.last_operator_admin`.
2. The task 003 integration tests still pass, and `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `DO7p` in this plan's `plan/findings.yaml` (type:security, priority:medium): the remaining-admin count in `requireNotLastOperatorAdmin` ignores whether an administrator's principal is disabled, and the guard's boundary (mod-authz grant revocation, gate-locked admins) is not documented.

## Status

Outcome: succeeded (2026-10-26). `operatorAdminProfileSQL` now joins `public.principals` on the profile holder with `disabled_at IS NULL`; the `requireNotLastOperatorAdmin` doc comment states the boundary (no mod-authz wildcard-grant revocation coverage, no gate-locked administrator coverage). Added `TestLeaveGate_DisabledPrincipalAdminDoesNotCountAsRemaining`. `make build.api`, `make test.unit` and the full integration suite passed (throwaway postgres:16 container, users superuser, tests executed, none skipped). Files: `api/internal/service/profile_last_admin.go`, `api/internal/integration/leave_gate_integration_test.go`.
