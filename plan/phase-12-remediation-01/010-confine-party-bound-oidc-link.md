# Confine Linked OIDC Credentials To The Provider Party

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `wZsk` (ambiguity, decided by owner: confine now) and `Nobq` (security, high). The OIDC link callback does not confine the linked credential to the party bound to the provider, while the managed-user creation path does. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-go.md`

## Requirements

1. In `callbackLink` (`api/internal/handlers/auth/oidc_flows.go`), load the provider policy via `h.oauth.PolicyFor(providerID)`. When `policy.PartyID > 0`, pass `ConfinedToPartyID = policy.PartyID` to `CredentialService.LinkOIDCInTx`, exactly as `createManagedUser` does.
2. Providers with no party keep linking unconfined.
3. Document the behavior in the relevant architecture or API doc. Do not add a link to any plan document.

## Validation

1. A route test linking through a party-bound provider asserts the credential's `confined_to_party_id` equals the provider party.
2. A route test with an unbound provider asserts the credential is unconfined.
3. `make build`, `make test` and `make lint` pass in `api/`.

## References

- Finding `wZsk` in this plan's `plan/findings.yaml` (ambiguity): owner decided to confine linked OIDC credentials to the provider party now.
- Finding `Nobq` in this plan's `plan/findings.yaml` (security, high): `callbackLink` links OIDC credentials without the provider's party confinement that `createManagedUser` applies.
