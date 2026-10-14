# Update Architecture Docs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Update the durable architecture and spec docs to reflect the security changes made in phases 1 to 4: verified-TLS default and opt-in for OIDC verifiers, the attempt limit and request cooldown on email-code and step-up verification, the widened `email_codes` purpose set and new `failed_attempts` column, and the generic `/readyz` failure body. Follow the `update-architecture-docs` task procedure at `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`. This plan does not include any pin or consumer-bump work.

## Requirements

- `role_doc: plugins/flow/roles/architect-backend.md`
- Implementation task docs that surfaced architectural implications (plain code text, not links; the architect writes durable content into project docs and never links these):
  - `plan/phase-01-oidc-tls-verify/001-verified-tls-default.md`
  - `plan/phase-04-email-code-attempt-limiting/002-limit-attempts-in-handlers.md`
- Files to review and update where needed: `docs/architecture.md` (auth flow, Health row for `/readyz`, email_codes data model if described), `docs/mod-users-spec.md` (security requirements, step-up and email-code behavior, `/readyz` bullet), and `docs/architecture/` pages if they describe email codes.
- Do not duplicate content already added to `docs/consumer-upgrade-notes.md` or `docs/oidc-troubleshooting.md` in phase 1; cross-link instead.
- Do not link into `plan/` from any project doc.

## Validation

- Each named file was reviewed; edits exist where behavior or data model changed, and unchanged files are noted in the task report.
- grep confirms no project doc contains a link into `plan/`.
- The docs match the implemented option name, constants (max attempts, cooldown), and migration numbers (`0101`, `0102`).

## Metadata

architectural_impact: false
