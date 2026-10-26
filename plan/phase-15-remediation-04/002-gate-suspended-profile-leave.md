# Evaluate The Party Gate When A Holder Leaves A Suspended Profile

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `twos` (security, priority medium) and `nA1G` (security, priority medium). `mod_users.profile_availability_for` returns `inactive` for any non-active profile before it evaluates the party gate, so the leave guard in `LeaveProfile` accepts a suspended profile without checking the gate. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. `LeaveProfile` in `api/internal/service/profile_holder.go` evaluates the party credential and acr gate for a suspended profile as well as for an active one. An availability of `inactive` caused by suspension does not skip the gate.
2. The leave guard does not accept `avail == 'inactive'` as satisfying the gate. The gate is evaluated independently of profile status, for example through a status-agnostic variant of `profile_availability_for`, and that result alone decides whether the holder may leave.
3. The departure cleanup (authorizations deleted, confined credentials revoked) runs only when the gate is met.
4. The OpenAPI description of leaving a suspended profile, which states that a suspended profile has no gate to evaluate and can be left, is corrected to match the new behavior.

## Validation

1. An integration test: a suspended holder whose party gate is unmet (for example an unauthorized password under an `authorized` party, or a missing required provider) is refused on leave, and the profile, its authorizations and its confined credentials are unchanged.
2. An integration test: a suspended holder whose gate is met can still leave the suspended profile.
3. `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `twos` in this plan's `plan/findings.yaml` (type:security, priority:medium): `profile_availability_for` returns `inactive` before the party gate is evaluated, and the leave path accepts `inactive` for a suspended profile, so a session the gate would lock out can still leave and run departure cleanup.
- Finding `nA1G` in this plan's `plan/findings.yaml` (type:security, priority:medium): the leave guard in `api/internal/service/profile_holder.go` accepts `avail == 'inactive'` for a suspended profile; the recommendation is to evaluate the party gate separately for suspended profiles.

## Status

Outcome: succeeded (2026-10-26).

- Added `mod_users.profile_gate_for` (the status-agnostic core of the availability rule) and `mod_users.profile_gate(session, profile)` in `model/migrations/sql/0100_baseline.sql`; `profile_availability_for` now wraps `profile_gate_for` and applies the `inactive` status check after the held check, so its results are unchanged. New sqlc query `ProfileGate` (`model/queries/sessions.sql`, regenerated `model/db`).
- `LeaveProfile` (`api/internal/service/profile_holder.go`) decides on `ProfileGate` alone for active and suspended profiles; the departure cleanup runs only when the gate is met. The unused `availabilityInactive` constant was removed (`api/internal/service/session_views.go`).
- `api/openapi.yaml` leave description corrected.
- Tests: `api/internal/integration/leave_gate_integration_test.go` (suspended holder refused for `authorized` and `provider` rules with profile, authorization and confined credential unchanged; suspended holder with the gate met can leave).
- Validation: `make build.api`, `make test.unit`, `make -C api lint`, and the integration suites (authz, config, integration) against a throwaway Postgres passed.
