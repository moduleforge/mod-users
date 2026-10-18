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
6. **Wiring and docs**: `partyPolicyService` in `moduleforge.module.yaml`, `api/cmd/server/main.go`,
   `api/usersservice`; routes added to `RegisterPartyRoutes`; `api/openapi.yaml`;
   `AGENTS.md`/`README.md` rows.
7. **Tests**: policy CRUD and check violations; admin path; self-authorization through another
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
  grants (U20); tests use those.

## References

- [API surface](../notes/mod-users-api-surface.md) sections 2.7, 2.8, 3, 7, 10.
- [Decisions](../notes/decisions-and-resolutions.md) resolution (a), U5, U20.
- [Work packages](../notes/work-packages.md), phase `parties-profiles-and-bootstrap`, package 2.
- mod-core ADR `docs/architecture/identity-model.md`, "Credential gating and profile availability".

## Checkpoint hints

- After the service and the "excluding one factor" availability evaluation.
- After the routes and tests.
