# Document Persisted Signing Key Exposure

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates finding e4f6 (persisted signing key exposure documentation gap).

The work targets the plan branch plan/identity-model-impl and lands through the ordinary per-task loop.

## Requirements

1. In the `docs/jwt-eddsa.md` "Persisted signing key" section, state that the persisted Ed25519 private key is stored unencrypted in `mod_users.auth_signing_keys`.
2. In the same section, state that read access to that table or to any database backup grants the ability to forge access tokens for any session.
3. Change the production wording from "can" to a recommendation that production deployments should set `JWT_SIGNING_KEY` from a secret store.

## Validation

1. Read the section and confirm all three statements are present and consistent with `model/migrations/sql/0100_baseline.sql` (`private_key BYTEA`, no encryption).
2. Confirm no other section of `docs/jwt-eddsa.md` changes.

## References

- Finding e4f6 in this plan's `plan/findings.yaml`.
