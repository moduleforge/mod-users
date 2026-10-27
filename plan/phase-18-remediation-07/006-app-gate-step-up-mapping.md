# Map Step-Up Refusals In The Invite-Only App Gates

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates the phase 8 gate finding QKwM, recorded as a finding in this plan's `plan/findings.yaml`. Both the app gate in the authenticator and SessionService.gateFor map only ErrForbidden and ErrUnauthenticated, so an engine ErrStepUpRequired on the login operation (possible once an operator sets min_acr or max_auth_age on login) surfaces as a 500. The two gates must behave the same after this task.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

This task is independent of the four sequenced tasks (001 to 004 of this phase). It touches api/internal/service/session_service.go, so if it runs concurrently with them it merges after task 003 (credentials-refuse-impersonation).

Scope is limited to fixing the linked finding. Anything beyond that is out of scope.

## Requirements

1. Independent of the four sequenced tasks; touches api/internal/service/session_service.go, so if run concurrently with them, merge after credentials-refuse-impersonation.
2. In both Authenticator.checkAppGate (api/internal/auth/authenticator.go) and SessionService.gateFor (api/internal/service/session_service.go), map an engine step-up refusal (errors.As to *apiresp.ActionRequiredError with code users.step_up_required, or errors.Is authz.ErrStepUpRequired) explicitly instead of wrapping it as a database fault: propagate the step-up action unchanged (409 users.step_up_required) in both; if the Authenticate contract cannot carry an action-required error, treat it in both places as not admitted (app_login_not_granted) and say so in the report. The two gates must behave the same.
3. Pin the choice with unit tests that stub the authorizer to return the step-up error for each gate.
4. Record in API surface section 6.1 (plan/notes/mod-users-api-surface.md), as an amendment line, the additive NewAuthenticatorWithAppGate constructor and SetAppGate method, and the step-up mapping chosen.
5. Never run scripts/link-siblings.sh or make preflight in a worktree; set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users and verify the sibling links afterwards. Do not edit mod-core or any sibling project.
6. The Go developer role (`plugins/flow/roles/developer-go.md`) applies to this task.

## Validation

1. make build, make test and make lint pass.
2. api/internal/auth/app_gate_test.go and api/internal/service/app_gate_test.go (or session_service_test.go) contain the new step-up cases and pass.
3. grep -n 'NewAuthenticatorWithAppGate\|SetAppGate' plan/notes/mod-users-api-surface.md shows the 6.1 amendment.

## References

- Finding QKwM in this plan's `plan/findings.yaml` (bug, medium): checkAppGate and SessionService.gateFor map only ErrForbidden and ErrUnauthenticated, so an engine step-up refusal on the login operation surfaces as a 500.
- [Decisions and resolutions](../notes/decisions-and-resolutions.md) — the Amendments (2026-10-27, phase 8 gate) section, for the phase 8 gate context in which this finding was recorded.
