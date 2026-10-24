# Reserve Assurance Claims And Stamp A Distinct Typ On Audience Tokens

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `UT68` (security). `audienceReservedClaims` omits `acr`, `amr` and `auth_time`, so an audience-token caller can stamp assurance claims onto a token signed with the access-token key, and no `typ` header separates the two token classes. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-go.md`

## Requirements

1. `audienceReservedClaims` includes `acr`, `amr` and `auth_time`, so `IssueAudienceToken` refuses them in `extra`.
2. `IssueAudienceToken` sets a distinct JWS `typ` header (for example `aud+jwt`), and the access-token `Authenticator` rejects tokens bearing it.
3. `docs/jwt-eddsa.md` (or the token section consumers read) notes the `typ` distinction for offline verifiers. Do not add a link to any plan document.

## Validation

1. `carrier_test`: `extra` containing `acr`, `amr` or `auth_time` is refused; an issued audience token carries the `typ` header; `Authenticator` rejects it.
2. `make build`, `make test` and `make lint` pass in `api/`.

## References

- Finding `UT68` in this plan's `plan/findings.yaml` (security, medium): `audienceReservedClaims` omits `acr`, `amr` and `auth_time`, and audience tokens share the access-token key and issuer.
