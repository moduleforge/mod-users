# Correct Route Mounting And OpenAPI Coverage Notes

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates findings N2lP (AGENTS.md misstates route auth mounting) and kmxU (architecture docs do not note that `/v1/oidc-config` is omitted from the OpenAPI spec).

The work targets the plan branch plan/identity-model-impl and lands through the ordinary per-task loop.

## Requirements

1. Reword the AGENTS.md "Error precedence follows http-error-precedence" bullet so it matches `docs/architecture.md`: the manifest (`moduleforge.module.yaml`) mounts `requireOIDCConfirmed` then `requireAuth` on every authenticated prefix; registrars never mount `auth.NewRequireAuth` themselves; and authenticated handlers answer 500 when no `auth.Identity` is present (N2lP).
2. `docs/architecture.md` (API layer intro or the OIDC configuration row) states that the `/v1/oidc-config` routes are served by the manifest-mounted handlers but are not described in `api/openapi.yaml` (kmxU).

## Validation

1. `grep` AGENTS.md for "Registrars of authenticated routes mount" and confirm it no longer appears.
2. Read `docs/architecture.md` and confirm the OpenAPI omission of `/v1/oidc-config` is stated, and that `grep -n 'oidc-config' api/openapi.yaml` still finds nothing.

## References

- Finding N2lP in this plan's `plan/findings.yaml`.
- Finding kmxU in this plan's `plan/findings.yaml`.

## Status

- Outcome: succeeded (2026-10-31).
- Validation: AGENTS.md no longer contains "Registrars of authenticated routes mount"; `docs/architecture.md` OIDC configuration row states the OpenAPI omission; `grep -c oidc-config api/openapi.yaml` is 0.
- Files: `AGENTS.md`, `docs/architecture.md`.
