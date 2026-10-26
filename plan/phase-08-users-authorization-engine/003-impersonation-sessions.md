# Impersonation Sessions

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Implement impersonation as a separate session: `ImpersonationService` and the `/v1/impersonations` routes. The ADR's sudo/`assume` model is replaced by a child session with these properties:

- it records the reason
- it is capped by a TTL and a renewal limit
- it needs a fresh `auth_time`
- it never nests
- it always records the real administrator

Suggested role: `plugins/flow/roles/developer-go.md`. No standard skill covers this task.

Phase 6 already shipped the schema side:

- the `sessions` impersonation columns and the MF101 no-nesting trigger
- `session_liveness` returning `parent_session_ended` and `impersonator_principal_id`
- the `act` claim

This task adds the service, handler, routes, manifest wiring and tests.

Dependency: requires task 001, because the `impersonate` operation is authorized through the new engine. It can run in parallel with tasks 002, 004 and 005.

## Requirements

1. **Service (`api/internal/service/impersonation.go`, facade `usersservice.ImpersonationService`).**
   - **Start** takes the target `profile_uuid` and a non-empty `reason`. It authorizes `impersonate` on the target profile entity. The engine's assurance gate enforces `max_auth_age` of 10 minutes and returns `users.step_up_required` when stale.
   - Start refuses with `users.impersonation_not_allowed` (403) when either holds:
     - the caller's session is itself an impersonation session; check in Go before the MF101 trigger fires, and map MF101 to the same error
     - the target profile is the caller's own
   - Start creates a child `sessions` row with these values:
     - `principal_id` = the target holder
     - `impersonated_profile_id` = the target
     - `impersonator_session_id` = the caller's session
     - `impersonation_reason`
     - `acr` and `amr` copied from the parent
     - `auth_time = now()`
     - `expires_at = now() + AUTH_IMPERSONATION_TTL`
   - Start returns the token response without a refresh token, with `expires_in <= AUTH_IMPERSONATION_TTL`. It emits an observer event with op **`impersonate`**, resource `impersonation`, target the impersonated profile, and `after` carrying the child session uuid and the reason (decision U21; mod-audit API surface section 2.2). The op is `impersonate`, never `assume`: mod-audit removed `assume` with no alias, and its CHECK rejects unknown ops. Under the `propagate` policy that rejection would roll back the start. The start request runs in the administrator's own session, so the row's actor is the administrator's profile, with no impersonator.
   - **Renew** (`POST /v1/impersonations/current/renew`) extends `expires_at` by `AUTH_IMPERSONATION_TTL`, capped at `AUTH_IMPERSONATION_MAX` from the session's `created_at`. It refuses outside an impersonation session. It emits op `update`, resource `impersonation`, target the impersonated profile.
   - **End** (`DELETE /v1/impersonations/current`) revokes the child session with `revoke_reason = 'impersonation_ended'` and returns 204. It emits op `revoke`, resource `impersonation`, target the impersonated profile. Renew and end run inside the child session, so their rows carry the impersonator principal and the child session id.
   - No other op is used for impersonation events ([API surface section 10.1](../notes/mod-users-api-surface.md)).
2. **No switching inside.** `POST /v1/session/switch` and `/v1/session/fork` refuse inside an impersonation session with `users.impersonation_not_allowed`. The impersonation session's only profile is `impersonated_profile_id`. Parent liveness is enforced by `session_liveness`: revoking or expiring the parent ends the child on the next request.
3. **Audit fields.** On every request inside an impersonation session, `AuthContext.ImpersonatorPrincipalID` is the parent session's own principal, and the token's `act.sub` is that principal's uuid. Verify this end to end rather than re-implementing it.
4. **Handler and wiring.**
   - Add `ImpersonationHandler` and `RegisterImpersonationRoutes` in `api/internal/handlers/`, re-exported from `api/handlers/handlers.go`.
   - Add the `impersonationService` and `impersonationHandler` entries to `moduleforge.module.yaml` per [API surface section 11](../notes/mod-users-api-surface.md#11-module-manifest-moduleforgemoduleyaml-binding).
   - Wire `api/cmd/server/main.go`.
   - Add the routes to `api/openapi.yaml`.
   - Config `AUTH_IMPERSONATION_TTL` (default 30m) and `AUTH_IMPERSONATION_MAX` (default 60m) belong in `api/internal/config/`. Add them there if phase 6 did not.
   - Keep the error order: 401, 400, 403, 404/409.
5. **Tests (unit and integration).**
   - No nesting through the API, and through direct SQL: inserting a child of an impersonation session raises MF101.
   - The administrator is recorded from the parent session's own principal.
   - An Acme party admin with an owned-scope `manage` grant on Acme can impersonate an Acme party profile. The same admin cannot impersonate a personal profile or a profile of another party (403).
   - Impersonating a personal profile requires operator authority: an operator-party profile with a wildcard `impersonate` or `manage` grant.
   - The own arm never confers `impersonate`.
   - Stale `auth_time` yields 409 `users.step_up_required`, the shared mod-core `apiresp` action-required error rendered by `WriteError`.
   - The TTL and renewal cap are enforced.
   - Ending the parent session ends the child (`parent_session_ended`).
   - Switch and fork inside an impersonation session are refused.
   - No refresh token is returned.
   - With a recording observer, start, renew and end emit exactly `impersonate`, `update` and
     `revoke` on resource `impersonation`. With mod-audit phase 20 merged, the three rows are
     accepted by mod-audit's real observer and `audit_log` schema. The renew and end rows carry
     `impersonator_principal_id` = the administrator's principal and a non-null `session_id`.
   - Route order tests use mod-core's impersonating `AuthContext` identity.
6. Update the `AGENTS.md` and `README.md` rows that describe impersonation or the removed `/v1/assume`, where those rows describe code this task changes.

## Validation

- `make build`, `make test` and `make lint` pass.
- `cd model && make verify` passes.
- The integration suites pass against a throwaway Postgres per `AGENTS.md`.
- `grep -n 'impersonations' api/openapi.yaml moduleforge.module.yaml` shows the routes and the manifest entries.
- The report maps each requirement 5 case to its test function.

## Metadata

architectural_impact: true

## Assumptions

- Task 001 has landed: `impersonate` is authorized through the engine with its `max_auth_age` gate.
- Phase 6 delivered the impersonation columns, the MF101 trigger, `session_liveness` impersonation handling and the `act` claim. If any is missing, report it rather than re-implementing schema in this task.
- Phase 7 delivered party profiles and the bootstrap operator party used in the fixtures.

## References

- [API surface section 2.9](../notes/mod-users-api-surface.md#29-mod_userssessions-binding-for-iduuid): the sessions schema.
- [API surface section 5](../notes/mod-users-api-surface.md#5-token-carrier-binding-for-offline-verifiers): the `act` claim.
- [API surface section 7](../notes/mod-users-api-surface.md#7-http-api): `/v1/impersonations` and the error codes.
- [API surface section 8](../notes/mod-users-api-surface.md#8-configuration): configuration.
- [API surface section 10.1](../notes/mod-users-api-surface.md): the audit op table (decision U21).
- mod-audit plan worktree, `plan/notes/mod-audit-api-surface.md` sections 2 (schema, CHECKs, op
  vocabulary), 5.1 (observer mapping) and 7 (notes for mod-users).
- [Work packages](../notes/work-packages.md#phase-users-authorization-engine): package 3.
- mod-core ADR `docs/architecture/identity-model.md`, section "Impersonation".

## Checkpoint hints

- After `ImpersonationService` and its unit tests.
- After the handler, routes, manifest, `main.go` and `openapi.yaml` wiring.
- After the integration tests.

## Status

Outcome: succeeded (2026-10-26).

Validation: `make build`, `make test`, `make lint` and `cd model && make verify` pass; the `internal/authz`, `internal/config` and `internal/integration` suites pass against a throwaway `postgres:16` (removed afterwards); `grep -n 'impersonations' api/openapi.yaml moduleforge.module.yaml` shows the routes and manifest entries.

Source files: `api/internal/service/impersonation.go` (service), `api/internal/handlers/impersonation.go` (handler and routes), `api/handlers/handlers.go` and `api/usersservice/service.go` (facade), `api/cmd/server/main.go`, `moduleforge.module.yaml`, `api/openapi.yaml`, `api/internal/service/session_service.go` (`SessionConfig` lifetimes, impersonation sessions list only the impersonated profile), `api/internal/service/session_flows.go` (switch and fork refuse), `api/internal/service/session_errors.go`, `api/internal/service/helpers.go`, `AGENTS.md`, `README.md`.

Requirement 5 to test map (`api/internal/integration/impersonation_integration_test.go`): no nesting through the API and SQL `TestImpersonation_NoNesting`; administrator from the parent's principal, no refresh token `TestImpersonation_StartCreatesAChildSession`; Acme admin reach `TestImpersonation_PartyAdminReach`; personal profile needs operator authority `TestImpersonation_PersonalProfileNeedsOperatorAuthority` (and restrictive-policy members `TestImpersonation_OperatorReachesMembersOfRestrictiveParties`); own arm `TestImpersonation_OwnArmNeverConfersImpersonate`; stale `auth_time` `TestImpersonation_StaleAuthTimeNeedsStepUp`; TTL and cap `TestImpersonation_TTLAndRenewalCap`; parent ends child `TestImpersonation_ParentLiveness`; switch and fork `TestImpersonation_SwitchAndForkRefused`; observer ops and real audit rows `TestImpersonation_Observations`; route order with `ordertest.Impersonating` `TestOrder_ImpersonationRoutes`. Unit tests: `api/internal/service/impersonation_test.go`, `api/internal/handlers/impersonation_test.go`.

Decisions: start answers 201; the child session carries no app (token audience is the default audience); renew sets the expiry to now + TTL capped at created_at + MAX and answers 409 `users.impersonation_limit_reached` when that is not later than the current expiry; an unknown target is 404 only for a caller allowed to impersonate every profile, else the same 403 as an unauthorized target; the lifetimes ride on `SessionConfig`, so `moduleforge.module.yaml` needs no extra `impersonationService` argument.
