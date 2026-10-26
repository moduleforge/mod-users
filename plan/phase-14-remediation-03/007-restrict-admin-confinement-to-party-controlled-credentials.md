# Restrict Administrator Confinement To Party-Controlled Credentials

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `Pxlw` (security, high) and the consistent project-backlog followup `E3ly`. Through the administrator door, `PUT /v1/parties/{party_uuid}/confinements/{credential_uuid}` lets a party administrator confine any live credential of any holder of a non-ended profile in the party, including a personal credential of an outside collaborator. The holder door of `DELETE` on the same path lets a holder clear the confinement of a credential the party issued, so party-issued credentials can escape departure revocation. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

This task records the binding user decision A:

- The administrator door of `ConfineCredential` may confine only party-controlled credentials. A credential is party-controlled when it belongs to a managed user of the party, or it is an OIDC credential from a provider bound to the party. Any other credential needs the holder door.
- The held-profile check in `gateCredentialWrite` requires profile status `active`, not `invited`.
- There is no schema change. `created_by_profile_id` is never used as an authority input.
- Consistently (`E3ly`), the holder door of `DELETE /v1/parties/{party_uuid}/confinements/{credential_uuid}` is refused for party-controlled credentials and allowed otherwise.

Dependency: this task MUST run after task 004 (`serialize-profile-writers-with-endings`), because both edit `gateCredentialWrite`. Build on 004's `SELECT ... FOR SHARE` read of the holder's open profile: tighten that same read to `status = 'active'` rather than reintroducing a bare `EXISTS`.

Files: `api/internal/service/party_policy.go` (`gateCredentialWrite`, `ConfineCredential`, `UnconfineCredential`), `api/openapi.yaml`, and tests.

## Requirements

1. **Party-controlled classification helper.** Add one helper in `party_policy.go` that reports whether a credential is party-controlled for a party. It is true when either holds:
   - the credential's principal is a managed user of the party (the party manages the user);
   - the credential is an `oidc` credential whose provider is bound to the party (`cred_oidc.provider_id` names the party's `party_authn_policies.required_oidc_provider_id` provider, or the provider the party is bound to under the model's existing binding column; confirm the binding against `model/migrations/sql/0100_baseline.sql`).

   Run it inside the transaction, after the credential lock, so the answer is stable for the write. Do not use `created_by_profile_id` or any other issuer column as an authority input.
2. **Administrator door of `ConfineCredential`.** When the caller entered through the administrator door (`admin` true in `gateCredentialWrite`) and the credential is not party-controlled, refuse. Use the door's existing refusal convention for a credential the caller may not touch (`apiresp.ErrNotFound` where the credential exists but the gate hides it, or the 403 the current door returns), and keep it indistinguishable from an unknown credential. A caller that is both administrator and the credential's own holder is served by the holder door.
3. **Held-profile check requires `active`.** In `gateCredentialWrite`, the read of the holder's profile in the party (task 004's `FOR SHARE` read) filters `status = 'active'` instead of `status <> 'ended'`. An `invited` profile does not satisfy it (`apiresp.ErrNotFound`).
4. **Holder door of `UnconfineCredential`.** When the caller entered through the holder door and the credential is party-controlled (requirement 1), refuse with the same refusal the holder door gives other callers it will not serve. The administrator door keeps its present behavior. A holder may still clear the confinement of their own personal credential.
5. **Departure cleanup unchanged.** `departureCleanupInTx` still revokes every credential confined to the party at departure. Add no code there. The change closes only the paths that let a party-controlled credential escape it.
6. **Documentation.** Update the `PUT` and `DELETE` operations of `/v1/parties/{party_uuid}/confinements/{credential_uuid}` in `api/openapi.yaml`:
   - `PUT` states that the administrator door confines only party-controlled credentials (managed-user credentials and OIDC credentials from a party-bound provider) and that any other credential needs the holder door.
   - `DELETE` states that the holder door is refused for party-controlled credentials.
   - The held profile must be active.

   Update the doc comments on `gateCredentialWrite`, `ConfineCredential` and `UnconfineCredential` to match.

## Validation

1. An integration test: a party administrator confining the personal credential of an outside collaborator who holds an active profile in the party is refused (404 or 403, whichever the current door convention returns), and the credential's `confined_to_party_id` is unchanged.
2. An integration test: the administrator confines a managed user's credential, and confines an OIDC credential from a party-bound provider; both succeed and set `confined_to_party_id`.
3. An integration test: the holder door of `DELETE` is refused for a managed-user credential and for a party-bound-provider OIDC credential, and succeeds for the holder's own personal credential confined to the party.
4. An integration test: a holder whose only profile in the party is `invited` cannot be the subject of an authorize or confine write (the check requires `active`).
5. An integration test: after the holder departs, departure cleanup still revokes party-controlled confined credentials (managed-user and party-bound-provider OIDC), and a credential the holder could not unconfine is still revoked.
6. `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `Pxlw` in this plan's `plan/findings.yaml` (security, high): `gateCredentialWrite` in `api/internal/service/party_policy.go` lets a party administrator confine any live credential of any holder of a non-ended profile in the party, including an outside collaborator's personal credential.
- Followup `E3ly` (project followups backlog; cited by id only): through the holder door, `UnconfineCredential` lets a holder clear the confinement of a party-issued credential and escape departure revocation. Consistent with decision A.
- [Task 004](./004-serialize-profile-writers-with-endings.md): serializes `gateCredentialWrite` with profile endings; this task builds on its `FOR SHARE` profile read and must run after it.
- [Decisions](../notes/decisions-and-resolutions.md#amendments-2026-10-26): the 2026-10-26 amendments.

## Status

- Outcome: succeeded (2026-10-25).
- Validation: `make build.api`, `make test.unit` and the full integration suite (authz, config, integration packages, throwaway Postgres) pass; `make -C api lint` clean. New integration tests in `api/internal/integration/party_confinement_integration_test.go` cover validation checks 1 to 5.
- Changes: `api/internal/service/party_policy.go` (`credentialWriteGate` records the admin and holder doors; `partyControlled` helper; `ConfineCredential` hides non-party-controlled credentials from the administrator door with `apiresp.ErrNotFound`; `UnconfineCredential` refuses the holder door for party-controlled credentials via `refuse`; the held-profile `FOR SHARE` read requires `status = 'active'`), `api/openapi.yaml`, `README.md`, `AGENTS.md`.
- Decisions: the provider binding is `oidc_providers.party_id` (the baseline column), not `required_oidc_provider_id`. A caller who is both administrator and holder is served by the holder door on confine; on unconfine the administrator door applies (unrestricted). Existing tests that used the administrator door on a personal credential were moved to party-controlled credentials, or to a direct SQL write where the test is about session gating (`party_flows_integration_test.go`).
