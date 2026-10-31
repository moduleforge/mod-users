# Review Repository Docs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Review every repository document outside the architecture and spec set that the identity-model
rewrite touched, and correct what the merged code made stale. The earlier implementation tasks
updated the rows describing the code they changed; this task is the survey that catches what they
missed. No standard skill covers it. Architecture and spec files (task
`001-update-architecture-docs`) and `docs/consumer-upgrade-notes.md` (task
`002-write-consumer-upgrade-notes`) are out of scope except for links into them. The broader
terminology drift scan is the final phase's job; this task fixes the documents listed below only.

Suggested role doc: `plugins/flow/roles/tech-writer.md`.

## Requirements

1. Review and update, against the merged code:
   - `README.md` — feature overview, routes, configuration, documentation list (keep the
     `docs/architecture/sessions-and-tokens.md` link task 001 added; add a link to the new
     consumer-upgrade section if the README lists upgrade notes).
   - `AGENTS.md` — package map, test recipes (throwaway-Postgres recipe now composing the
     `mod_users` schema), conventions that mention `user_accounts`, `assume`, sudo or the
     first-user hook.
   - `.claude/CLAUDE.md` — the gotchas list (file names in the `@moduleforge/core-gui` import list
     that were renamed or removed by the GUI phase).
   - `model/README.md` — schema description (`mod_users` schema, baseline contents, SQLSTATE codes
     MF101 to MF103).
   - `gui/README.md` — frontend contract (Bearer only, per-tab storage, refresh lineage, fork,
     handoff); confirm the GUI phase's update is complete.
   - `docs/cookie-path.md` — only the `oidc_state` cookie remains; no authentication cookie.
   - `docs/oidc-troubleshooting.md` — handoff code instead of a token in the redirect, no link by
     email (`account_exists_sign_in_and_link`), JIT modes, provider `party_uuid`/`jit_mode`/
     `trust_acr`, setup-token lifecycle (U9).
   - `docs/project-structure.md` — deleted and added packages and files.
   - `docs/cors.md` — Bearer-only contract, no credentialed requests.
   - `next-steps.md` — remove items this plan completed; keep open ones.
   - `.env.example` — removed `JWT_ALG`, `AUTH_REQUIRE_STEP_UP` and the admin-role claim setting;
     added `AUTH_*` variables with defaults and comments; `JWT_SECRET` and `JWT_SIGNING_KEY`
     comments current.
   - `deploy/local/README.md`, `deploy/k8s/README.md`, `deploy/serverless/README.md` and any
     configuration under `deploy/` (including `deploy/cloudrun/`) that sets removed variables or
     describes first-user or HS256 setup.
   - `api/openapi.yaml` — operation and schema `description` text only (paths and schemas were set
     by the implementation tasks); fix wording that still describes accounts, assume, sudo,
     cookies or HS256.
2. Edit stale statements in place; do not restructure accurate content.
3. No project document links into `plan/`; plan paths here are plain code text.
4. If a stale statement cannot be fixed without a design decision, leave it, and state the
   location and the intended followup (title, text, `type:` tag) in the report for the manager to
   file.

## Validation

- Each listed file was read and either updated or reported as already correct, with the changes
  summarized per file in the report.
- `grep -rnE "user_accounts|user_account|account_holder|HS256|JWT_ALG|AUTH_REQUIRE_STEP_UP|X-Step-Up-Token|/v1/self|/v1/assume|SudoUserUUID|AssumedUser|firstUserGrant|first-user hook"`
  over the listed files returns no stale hit (each remaining hit justified in the report).
- `grep -rn "](.*plan/" README.md AGENTS.md model/README.md gui/README.md docs deploy next-steps.md`
  finds no link into the plan directory.
- Every variable in `.env.example` is read by `api/internal/config/` (spot-check with `grep`), and
  every variable `config.go` reads appears in `.env.example`.
- `api/openapi.yaml` still parses (`make lint` or the project's OpenAPI check passes).
- Relative links in the edited files resolve.

## Assumptions

- Phases 6 to 9 are merged; `001-update-architecture-docs` and `002-write-consumer-upgrade-notes`
  are complete (hard dependency: this task edits `README.md` after task 001's link and may link
  task 002's new section).

## References

- [Work packages](../notes/work-packages.md) — phase `doc-updates`, item 2, and the convention
  that implementation tasks update the rows for the code they change.
- [Mod-users API surface](../notes/mod-users-api-surface.md) — sections 7 (HTTP API), 8
  (configuration) and 12 (frontend contract).
- [Decisions and resolutions](../notes/decisions-and-resolutions.md) — resolution (c), U4, U7, U9.

## Checkpoint hints

- After `README.md`, `AGENTS.md`, `.claude/CLAUDE.md`.
- After `model/README.md`, `gui/README.md` and the `docs/` files.
- After `.env.example`, `next-steps.md` and `deploy/`.
- After `api/openapi.yaml` descriptions.

## Status

- Outcome: succeeded (2026-10-31). Validation: stale-term grep clean except two justified hits; no plan links; env/config parity holds; openapi parses; relative links resolve except docs/mf-standards (uninitialized submodule in worktrees).
- Changed: `.env.example` (added commented vars read by config.go), `AGENTS.md`, `.claude/CLAUDE.md`, `model/README.md`, `gui/README.md` (checked, no change), `docs/cors.md`, `docs/oidc-troubleshooting.md`, `docs/project-structure.md`, `next-steps.md`, `README.md`, `deploy/local/README.md`, `deploy/k8s/README.md`, `deploy/serverless/README.md`, `api/openapi.yaml`.
- Open for the manager (not edited here): `api/internal/server/server.go` CORS allow-list still names `X-Step-Up-Token`, and still sets `Allow-Credentials` for explicit origins; `.env.example` `CORE_FIELD_KEY_HEX` is read by no code in this repo.
