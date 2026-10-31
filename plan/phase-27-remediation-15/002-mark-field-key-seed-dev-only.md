# Mark Committed Field-Encryption Seed As Development Only

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates finding CubF. It makes the committed field-encryption seed in `.env.example` explicitly development-only, so that shared and production environments generate their own value.

The work targets the plan branch plan/identity-model-impl and lands through the ordinary per-task loop.

## Requirements

1. In the `.env.example` "Field-encryption seed (owned by mod-core)" block (around lines 207-216), state that the committed `CORE_FIELD_KEY_HEX` value is for local development only, and that any shared or production environment must generate a fresh value (for example `openssl rand -hex 32`) before first boot, because the value seeds the SSN/EIN field-encryption key. (CubF)
2. Leave the `CORE_FIELD_KEY_HEX` value unchanged.

## Validation

1. Read the "Field-encryption seed (owned by mod-core)" block and confirm it contains the development-only statement and the generate-fresh instruction, and that the value matches HEAD.
2. Confirm `git diff` touches only `.env.example`.

## References

- Finding CubF in this plan's `plan/findings.yaml`.

## Status

- Outcome: succeeded (2026-10-31).
- Added a development-only / generate-fresh (`openssl rand -hex 32`) note to the field-encryption seed block in `.env.example`; `CORE_FIELD_KEY_HEX` value unchanged.
- Validation: block read and confirmed; `git diff` touches only `.env.example` (plus this task doc).
