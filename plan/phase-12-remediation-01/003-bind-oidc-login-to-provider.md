# Bind OIDC Sign-In To The Credential's Provider Configuration

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `VxMT` (security). `OIDCResolver.Resolve` signs in a credential through any provider that shares its issuer and subject, without comparing the credential's `provider_id` to the policy's provider. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-go.md`

## Requirements

1. `OIDCResolver.Resolve` (or `callbackLogin`) refuses with `RedirectErrAuthFailed` when the active `cred_oidc` row matching (issuer, subject) has `provider_id` different from `policy.ProviderID`, matching the `callbackStepUp` check.
2. The refusal writes no session and no observation.

## Validation

1. A resolver unit test: a credential recorded for provider A presented through provider B with the same issuer resolves to a refusal; the same provider still resolves to `ActionLogin`.
2. `make build`, `make test` and `make lint` pass in `api/`.

## References

- Finding `VxMT` in this plan's `plan/findings.yaml` (security, medium): `OIDCResolver.Resolve` returns `ActionLogin` for any active (issuer, subject) match and never compares `provider_id`.

## Status

Outcome: succeeded (2026-10-25). `OIDCResolver.Resolve` now refuses with `RedirectErrAuthFailed` when the matched credential's `provider_id` differs from `policy.ProviderID`; the resolver is read-only so no session or observation is written. Unit test case added in `api/internal/auth/resolver_test.go` (fake querier now returns provider `p`). `make build`, `make test`, `make lint` in `api/` passed (siblings via MODULEFORGE_SIBLINGS_DIR). Source: `api/internal/auth/resolver.go`.
