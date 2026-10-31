# Document CORE_FIELD_KEY_HEX Ownership In Env Example

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates finding RVUM (`CORE_FIELD_KEY_HEX` in `.env.example` sits under the Authelia timezone heading and does not state its ownership or first-boot-only semantics).

The work targets the plan branch plan/identity-model-impl and lands through the ordinary per-task loop.

## Requirements

1. Move `CORE_FIELD_KEY_HEX` in `.env.example` out from under the Authelia timezone heading into its own commented block.
2. The comment states that the variable is read by mod-core's `fieldcrypto.NewFromEnvOrGenerate`, which mod-users' dev server calls for SSN/EIN field encryption; that it is a first-boot-only seed, so later boots must match the active key; and that rotation goes through mod-core's field-crypto-keys rotation route, not this variable.

## Validation

1. Read `.env.example` and confirm the block exists, names mod-core as owner, and the value is unchanged.
2. Confirm the description against `api/cmd/server/main.go` lines 232-237.

## References

- Finding RVUM in this plan's `plan/findings.yaml`.

## Status

- Outcome: succeeded
- Date: 2026-10-31
- Validation: check 1 (block exists under its own heading, names mod-core as owner, value unchanged versus HEAD) passed; check 2 (description consistent with `api/cmd/server/main.go` lines 232-237 and mod-core's `moduleforge.module.yaml` cipher block) passed. The main.go comment does not name the env var itself; the description is consistent with it, not contradicted by it. Log: `.flow/validation-logs/01-env-block-and-description.log`.
- Affected file: `.env.example` (repo-relative). Added a `Field-encryption seed (owned by mod-core)` comment block after the `TZ=UTC` line; `CORE_FIELD_KEY_HEX` moved beneath it, value unchanged.
