# Users Authorization Engine

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Goals

Rewrite the production `Authorizer` (`api/internal/authz/authz.go`) to the ADR's engine: the profile
as the only subject, the operation-level assurance gate (`min_acr`, `max_auth_age`), scope-aware
wildcard and entity arms, the owned-scope arm with `include_subtypes`, the per-operation own arm
through `authz_operations.ownership_confers` that never confers `impersonate` and applies only to
users' personal profiles (mod-users' half of followup `rHzV`), and `AuthorizeTypeIn`. Add the flows
that need mod-authz's new schema: impersonation as a separate session (reason, TTL, renewal cap,
step-up, no nesting, real administrator always recorded), the `credentials` operation for owner and
operator credential management (replacing `assume` for SSH keys), the `apps.invite_only` login gate,
and `EnsurePersonalParty` (resolution (b), U1, U2). It comes after the party phase and after
mod-authz's schema phase, which it depends on.

## Inputs

- mod-authz's schema phase (API surface section 9 items 1 to 4): `ownership_confers`, `min_acr`,
  `max_auth_age`, `impersonate`, `credentials`, grant `scope`/`target_type_id`/`include_subtypes`,
  `is_profile` triggers, the operator-actor trigger, `OperationRegistry.Policy`, the extended
  `CreateGrantParams`.
- mod-core's access-function generator (phase 3) for the parity tests.
- The two earlier mod-users phases.
- The ADR sections "Authorization engine", "Impersonation", "Assurance", "Billing" (personal team
  plans) and "Machine principals, system actors, guests and apps".
- [The API surface](../notes/mod-users-api-surface.md) sections 6.2, 6.3, 7, 9;
  [decisions](../notes/decisions-and-resolutions.md) U1, U2, U5, U14, U15;
  [work packages](../notes/work-packages.md) for this phase.

## Outputs

- `authz.go` implementing API surface 6.2, with package documentation free of `assume`/sudo wording;
  `localAuthz` asserting `OwnerScopedTypeAuthorizer`.
- Engine tests: owned scope (exact and subtypes, never the owner's own record), per-operation own
  arm, assurance gate, `AuthorizeTypeIn`, and parity with the generated list functions.
- `ImpersonationService` and `/v1/impersonations` with tests (no nesting via API and SQL, audit
  fields, scope of an organization admin, operator authority for personal profiles).
- `/v1/principals/{uuid}/credentials*` under the `credentials` operation; owner key rotation for
  self-owned service accounts; the invite-only app gate.
- `PersonalPartyService.EnsurePersonalParty` with idempotency and refusal tests; the party phase's
  admin flows re-tested with owned-scope grants.
