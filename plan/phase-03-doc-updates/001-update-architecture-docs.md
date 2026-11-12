# Update Architecture Docs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Update architecture and spec docs to reflect the OIDC verification changes: explicit algorithm pinning (discovery intersected with an allowlist and the operator restriction), the typ policy, the new verifier option and env var.

## Requirements

- Planned implementation task documents that surfaced the architectural implications (read as inputs only): `phase-01-oidc-alg-pinning/002-pin-oidc-signing-algs.md`, `phase-01-oidc-alg-pinning/003-alg-confusion-negative-tests.md`, `phase-02-oidc-eddsa-login-e2e/001-eddsa-login-flow-tests.md`.
- Files to review: `docs/architecture.md` (Authentication flow OIDC steps and the OIDC issuer TLS paragraph, Access-token algorithm paragraph), `docs/mod-users-spec.md` (OIDC/token requirements), `docs/jwt-eddsa.md` (add a short section that external OIDC tokens are verified separately from local EdDSA carriers), `docs/architecture/sessions-and-tokens.md` (audience-token `typ` note if the OIDC verification is described there).
- `role_doc: plugins/flow/roles/architect-backend.md`.
- Follow the task-procedure at `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.
- Write durable content into the project docs only; never link to or name the plan documents listed above.

## Validation

- Each named file was reviewed and updated where the change applies; docs state decisions as current behaviour (defaults, RS256 still allowed, `none`/`HS*` never accepted, typ policy), not as plan history.
- Links in edited docs resolve; no links into `plan/`.

## Metadata

architectural_impact: true
