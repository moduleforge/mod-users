# Party Gating Administration

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Implement `PartyPolicyService`: administration of a party's authn policy
(`mod_users.party_authn_policies`), of credential authorizations
(`mod_users.credential_party_authorizations`) and of credential confinement
(`credentials.confined_to_party_id`), with the routes under `/v1/parties/{party_uuid}`. The tables
and the availability function that consumes them already exist from phase 6; this task adds the
write side and its authorization rules.

Roles: `plugins/flow/roles/developer-go.md` (primary), `plugins/flow/roles/developer-sql.md`.

Depends on task 001 (`PartiesHandler` and `RegisterPartyRoutes` exist). Runs in parallel with
tasks 004 and 005. Task 003 depends on this task.

## Requirements

1. **Authn policy.** `GET /v1/parties/{party_uuid}/authn-policy` returns the effective policy (no
   row means `credential_rule = 'any'`, `min_acr = 1`). `PUT` upserts `credential_rule`,
   `required_oidc_provider_id` and `min_acr` under the table's checks (`provider` iff a required
   provider is set; `min_acr` 1 to 3; the provider must exist). Authorization: `update` on the
   party entity (or `AuthorizeTypeIn(update, party_authn_policy, party)` if a type is registered;
   choose and document), plus the step-up freshness check task 001 uses for profile
   administration. Emit `party_authn_policy` observer events.
2. **Credential authorizations.** `GET /v1/parties/{party_uuid}/credential-authorizations` lists
   them (credential uuid, kind, label, holder principal uuid, authorized-by profile, time).
   `PUT /credential-authorizations/{credential_uuid}` creates one (idempotent);
   `DELETE` removes one. `authorized_by_profile_id` is the acting profile. Emit
   `credential_party_authorization` observer events.
3. **Confinement.** `PUT /confinements/{credential_uuid}` sets `confined_to_party_id` to the party;
   `DELETE` clears it only when it currently names this party. A credential confined to another
   party is a 409.
4. **Who may write an authorization or confinement** (ADR "Credential gating"), exactly one of:
   - a **party admin**: the acting profile holds `update` on the party (the same check as the
     policy route), or
   - the **credential's own holder acting through a session that already satisfies the party's
     gate through another factor**: the session principal holds the credential; the session's
     current profile is an active profile of this party; `mod_users.profile_availability(session,
     that profile)` is `available`; and the availability still holds when the credential being
     authorized is excluded from the session's factors (it must not authorize itself). Implement
     the "excluding one factor" evaluation once (a SQL function or a query variant next to
     `profile_availability`), not by duplicating the policy rules in Go.
   Every other caller gets 403 (or masked 404 when the actor cannot read the party). The credential
   must belong to a principal holding a non-ended profile in the party; otherwise 404.
5. **JIT confinement default.** Confirm that the phase-6 OIDC resolver confines credentials JIT-created
   from a party-bound provider (`jit_mode = 'managed'`, and `byo` with a `party_id`) to that party;
   fix it here if it does not, and add a test.
6. **Audit ops** (decision U21): only the ops of
   [API surface section 10.1](../notes/mod-users-api-surface.md):
   - `party_authn_policy`: `create`, `update`, `delete`;
   - `credential_party_authorization`: `create`, `delete`;
   - confinement set or cleared: `update` on `credential`.

   No other verb is allowed.
7. **Wiring and docs**: `partyPolicyService` in `moduleforge.module.yaml`, `api/cmd/server/main.go`,
   `api/usersservice`; routes added to `RegisterPartyRoutes`; `api/openapi.yaml`;
   `AGENTS.md`/`README.md` rows.
8. **Tests**: policy CRUD and check violations; admin path; self-authorization through another
   factor succeeds; self-authorization using only the credential being authorized fails; a
   personal-only session cannot authorize a credential for a party with `authorized` policy;
   confinement set/clear/conflict; availability changes take effect on the next request (a
   policy switch from `any` to `authorized` locks a profile reached only through an unauthorized
   credential).

## Validation

- `make build`, `make test`, `make lint`, `cd model && make verify` pass; integration tests pass.
- `api/openapi.yaml` lists the six route forms of requirements 1 to 3.
- `grep -rn "partyPolicyService" moduleforge.module.yaml api/cmd/server/main.go` shows the wiring.
- The "self-authorization using only the credential being authorized fails" test exists and passes.

## Metadata

architectural_impact: true

## Assumptions

- Phase 6 delivered `mod_users.party_authn_policies`, `mod_users.credential_party_authorizations`,
  `credentials.confined_to_party_id`, `profile_availability`, the `party_policies.sql` queries
  and the OIDC resolver.
- Until the engine phase, `AuthorizeTypeIn` and `Authorize` admit only wildcard, type and entity
  grants (U20); tests use those. mod-authz phase 13 has merged (decision U27), so wildcard and
  type-entity grants are held only by operator-party profiles. A non-operator party admin is
  modeled with an entity-scope `update` grant on the party entity.

## References

- [API surface](../notes/mod-users-api-surface.md) sections 2.7, 2.8, 3, 7, 10.
- [Decisions](../notes/decisions-and-resolutions.md) resolution (a), U5, U20, U21, U27.
- [Work packages](../notes/work-packages.md), phase `parties-profiles-and-bootstrap`, package 2.
- mod-core ADR `docs/architecture/identity-model.md`, "Credential gating and profile availability".

## Checkpoint hints

- After the service and the "excluding one factor" availability evaluation.
- After the routes and tests.

## Status

Outcome: succeeded (2026-10-25).

Validation: `make build`, `make test`, `make lint`, `cd model && make verify` pass; the integration suites (`api/internal/authz`, `api/internal/config`, `api/internal/integration`) pass against a throwaway Postgres 16 (database clock within 10s of the host). `api/openapi.yaml` lists the authn-policy (GET, PUT), credential-authorizations (GET), credential-authorizations/{credential_uuid} (PUT, DELETE) and confinements/{credential_uuid} (PUT, DELETE) routes. The "self-authorization using only the credential being authorized fails" test is `TestPartyPolicy_SelfAuthorizationUsingOnlyTheCredentialBeingAuthorizedFails`.

Changed: `api/internal/service/party_policy.go` (`PartyPolicyService`), `api/internal/handlers/parties/policy_routes.go`, `model/migrations/sql/0100_baseline.sql` (`session_acr_excluding`, `profile_availability_excluding`; `session_acr` now delegates), `model/queries/{sessions,credentials}.sql` and regenerated `model/db`, `api/internal/handlers/auth/oidc_flows.go`, wiring in `moduleforge.module.yaml`, `api/cmd/server/main.go`, `api/usersservice/service.go`, `api/handlers/handlers.go`, plus `api/openapi.yaml`, `README.md`, `AGENTS.md`.

Decisions:
- Authority resource for the policy and the writes is `update` on the party entity (no `party_authn_policy` entity type is registered). GET of the policy needs only the party-scoped read gate; listing authorizations needs `update`.
- The "excluding one factor" evaluation is the SQL function `mod_users.profile_availability_excluding(session, profile, credential)` over the shared `profile_availability_for`; the credential is also left out of the session acr (`session_acr_excluding`). The holder door requires both `profile_availability` and the excluding variant to be `available` (excluding a credential confined elsewhere could otherwise improve the result).
- Every write (policy, authorization, confinement, both doors) needs the freshness gate (409 after authorization); writes under impersonation are 403.
- A caller refused by both doors gets 404 when it cannot read the party (`AuthorizeTypeIn(read, profile, party)`), else 403.
- DELETE of a missing authorization or of an unconfined credential is 404; confining an already-confined-here credential and re-authorizing are idempotent and observe nothing.
- JIT confinement (requirement 5): the managed path and the link flow already confined; the `byo` JIT sign-up from a party-bound provider did not, now fixed in `createBYOUser`, with `TestOIDC_JIT_BYOFromAPartyBoundProviderConfinesTheCredentialToTheParty`.
- `NewPartiesHandler` now takes the `PartyPolicyService` as a second argument.
- No `DELETE /authn-policy` route (not in the requirements), so the `delete` op of `party_authn_policy` is not emitted by this task.
