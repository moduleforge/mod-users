# Update Deploy Manifests For EdDSA Signing Key Configuration

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `CLi0` (documentation). The deploy manifests still require the `jwt-secret` secret and do not show `JWT_SIGNING_KEY` or `JWT_VERIFY_PUBLIC_KEYS`, which the EdDSA-only change made the operative configuration. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer.md`

## Requirements

1. `deploy/k8s/base/deployment.yaml` and `deploy/cloudrun/service.yaml` make `JWT_SECRET` optional (`secretKeyRef` `optional: true` or equivalent) and show `JWT_SIGNING_KEY` (plus `JWT_SIGNING_KID` and `JWT_VERIFY_PUBLIC_KEYS` for rotation) supplied from a secret.
2. Comments note the persisted `mod_users.auth_signing_keys` fallback when `JWT_SIGNING_KEY` is unset, and the optional `AUTH_*` lifetime and audience variables.

## Validation

1. The manifests parse (`kubectl kustomize deploy/k8s/base` or `yq`), and every variable they set is read by `api/internal/config`.

## References

- Finding `CLi0` in this plan's `plan/findings.yaml` (documentation, medium): `deploy/k8s/base/deployment.yaml` and `deploy/cloudrun/service.yaml` require `users-api-secrets/jwt-secret` and do not show the EdDSA signing-key variables.
