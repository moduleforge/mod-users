# Update Architecture Docs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Review and update mod-users' architecture and spec docs so they reflect the error-precedence conformance changes planned in this session:

- `RequireAuth` inside the authenticated registrars, never on public or credential routes;
- the masked user-account miss and its target-0 authorizer call;
- strict `server.Decode`/`DecodeJSON` request decoding;
- the step-up gate's position after stateless checks;
- the credential-in-body and middleware-gate exceptions;
- post-authorization 409/422 classification;
- the apps-membership resolver change (`AllowNotFound("app")` removed from the shared resolver).

## Requirements

- The planned implementation task documents that surfaced the architectural implications. They are plain paths relative to the plan worktree, and will be complete when this phase runs:
  - `plan/phase-01-authenticate-routes/002-mount-require-auth.md`
  - `plan/phase-02-existence-leaks/001-user-account-masked-miss.md`
  - `plan/phase-03-decode-and-shape/002-decode-wrapper-and-callers.md`
  - `plan/phase-03-decode-and-shape/004-apps-membership-shape.md`
  - `plan/phase-03-decode-and-shape/005-self-service-shape.md`
  - `plan/phase-03-decode-and-shape/006-oidc-config-credential-shape.md`
  - `plan/phase-04-conflicts-and-order-tests/001-state-conflict-audit.md`
- Architecture and spec files to review:
  - `docs/architecture.md`: "API layer", "Authentication flow", "Cross-cutting patterns" and "Key decisions".
  - `docs/mod-users-spec.md`, found by the `docs/*-spec.md` glob. Its security requirements, and the existence-masking text at about line 352.
  - `docs/architecture/ssh-keys.md`: existence masking and step-up ordering (D9, D10 in the plan's notes).
  - `AGENTS.md` Conventions and `docs/consumer-upgrade-notes.md`, already updated for wire risks by task 5.2. Review them for architectural accuracy and do not duplicate that work.
  - The shared `docs/mf-standards/architecture/*` lives in the `docs-mf-standards` repo, and the submodule is not initialized here. Do not edit it. If it states something this plan made false, file a followup with `target_project` set to `docs-mf-standards`.
- role_doc: `plugins/flow/roles/architect-backend.md`
- Procedure: `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.
- The architect writes durable content into the project docs listed above and never links the plan task docs listed in these Requirements. Those paths are plain code text, not Markdown links.

## Validation

- Each file named above was reviewed, and the report lists, per file, "updated" (with a summary) or "already correct".
- `grep -rn '](.*plan/' README.md AGENTS.md docs/` shows no links into `plan/` other than the durable artifacts allowed by the ephemeral linking rule.
- If any Go package doc was edited, from `api/`: `test -z "$(gofmt -l .)" && go vet ./... && go test ./...`.

## Metadata

architectural_impact: false

## Status

- Outcome: succeeded (2026-10-11). Docs-only; no Go package docs edited, so the Go validation check is not applicable.
- `docs/architecture.md`: updated (API layer error-order and RequireAuth paragraph; entityResolver service row and Data model apps sentence; routes-wiring note; new D20 and D21).
- `docs/architecture/ssh-keys.md`: updated (D9 request order, step-up position, key parse before authorization, masked miss).
- `docs/mod-users-spec.md`: updated (use cases 9 and 11, app-membership resolver text, middleware-gate exceptions and anti-enumeration 204s in Request order).
- `AGENTS.md` and `docs/consumer-upgrade-notes.md`: already correct.
- `docs/mf-standards/*`: not edited; sibling docs-mf-standards checked, nothing made false.
- Link check: no links into `plan/` from README.md, AGENTS.md or docs/.
