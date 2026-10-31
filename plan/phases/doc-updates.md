# Documentation Updates

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Goals

Carry the durable content of this plan into mod-users' project documentation: the architecture and
specification documents (the planning procedure's `update-architecture-docs` task) and a review of
every repository document the earlier tasks touched. It is registered among mod-users' phases,
before the final phase, so the drift scan runs against current documents; a re-invocation of the
planning procedure must add its `update-architecture-docs` task here rather than registering a
second, trailing `doc-updates` phase.

## Inputs

- The four implementation phases of this plan, merged.
- [Decisions](../notes/decisions-and-resolutions.md) (the durable ones become architecture
  decisions) and [the API surface](../notes/mod-users-api-surface.md).
- mod-core's rewritten ADR (`docs/architecture/identity-model.md`), referenced from mod-users docs.
- [Work packages](../notes/work-packages.md) for this phase.

## Outputs

- `docs/architecture.md`, `docs/mod-users-spec.md`, `docs/architecture/ssh-keys.md`,
  `docs/jwt-eddsa.md` rewritten for the new model; a new `docs/architecture/sessions-and-tokens.md`
  linked from `docs/architecture.md` and `README.md`.
- `README.md`, `AGENTS.md`, `model/README.md`, `gui/README.md`, `docs/consumer-upgrade-notes.md`,
  `docs/cookie-path.md`, `docs/oidc-troubleshooting.md`, `docs/project-structure.md`,
  `docs/cors.md`, `next-steps.md`, `.env.example`, `deploy/` docs and `api/openapi.yaml`
  descriptions current. No project document links into `plan/`.
