# Update Architecture Docs

## Purpose and scope

Update this repo's own architecture-relevant documentation to reflect the
new `MFAPP_DATABASE_URL`/`DB_URL` fallback (Phase 3) and the new
`auth_jwt_secrets` table and `Load()` bootstrap behavior (Phase 4), per
the architectural-implications check: this plan adds significant new
tracked state (a new table) and changes `Load()`'s observable
behavior/failure-mode contract (new env var precedence; a new
DB-dependent failure mode when `JWT_SECRET` is absent). Follow the
`update-architecture-docs` task-procedure at
`plugins/flow/task-procedures/update-architecture-docs/SKILL.md` in the
Flow plugin install (a separate repo from this project; not a path inside
this project's `project_root`, hence referenced by plugin-relative path
rather than a repo-relative Markdown link).

`role_doc: plugins/flow/roles/architect-backend.md` — the implications
here are primarily a config-loading/behavior change (env var precedence,
a new secret-bootstrap fallback path with new failure modes), with the
new table as a secondary, tightly-coupled implication of that same
behavior change — the reverse framing from the sibling `mod-core` plan's
equivalent task, which is correct there but not here, since this plan's
central change is `Load()`'s behavior, not the schema.

## Requirements

- **Which planned implementation task documents surfaced the
  architectural implications** (all flagged `architectural_impact: true`
  except Phase 3's, which is a smaller, non-architectural change included
  here for narrative completeness):
  - `plan/phase-03-db-url-fallback/001-add-database-url-env-fallback.md`
    — `MFAPP_DATABASE_URL`/`DB_URL` precedence (not itself flagged
    architectural, but the value `Load()`'s new JWT-secret bootstrap path
    consumes).
  - `plan/phase-04-jwt-secret-bootstrap/001-add-auth-jwt-secrets-schema.md`
    — new `auth_jwt_secrets` table (significant new tracked state).
  - `plan/phase-04-jwt-secret-bootstrap/002-implement-jwt-secret-bootstrap.md`
    — `Load()`'s new internal DB dependency and failure mode (a
    behavior/contract change to a widely-called public function, even
    though its exported signature is unchanged).
- **Which architecture and spec files need review:**
  - `docs/architecture.md` — specifically:
    - The "Data model" table (§Data model) — add a row for
      `auth_jwt_secrets`, matching the existing table's style (see the
      `system_actors` row for a comparable "operational, not domain data"
      entry).
    - Somewhere near "Runtime service dependencies" or "Authentication
      flow" — a short new note describing: `MFAPP_DATABASE_URL`/`DB_URL`
      precedence (permanent, not a deprecation window — see
      `plan/overview.md`'s "Deferred and flagged" for the reasoning to
      carry into the doc update); and the `JWT_SECRET`
      fetch-or-generate-persist fallback (env var always wins; DB
      consulted only when the env var is absent and a DB URL resolves;
      corrupt persisted state fails loudly; this is a new DB-dependent
      failure mode inside `Load()`, previously pure/network-free).
  - `docs/mod-users-spec.md` — reviewed for contradiction (as of
    planning, it does not mention `JWT_SECRET`/`DB_URL`/`config.Load` at
    all, so no change is expected — confirm this remains true rather than
    assuming it, since Phase 3/4 task authors may have touched adjacent
    prose).
  - `AGENTS.md`'s "Database migrations" section — mention the new
    `0102_auth_jwt_secrets.sql` migration (the section currently only
    describes the mechanism generically, not an enumerated file list, so
    check whether a mention is warranted per its current style rather than
    forcing one).
  - `.env.example` — confirm Phase 3's own task already updated its
    `DB_URL`/`MFAPP_DATABASE_URL` comments (per that task's Requirement 5)
    and that the trailing comments accurately describe the shipped
    behavior, not merely what was planned.
- Confirm no other in-repo doc (e.g. `README.md`, `next-steps.md`) makes
  claims about `Load()`'s boot-failure behavior or `DB_URL` that this
  change contradicts; update any such claim found.

## Validation

- `docs/architecture.md`'s Data model table includes an `auth_jwt_secrets`
  row, and a new note (wherever it lands) describes both the env var
  precedence and the JWT-secret bootstrap fallback as shipped.
- `grep -n "auth_jwt_secrets\|MFAPP_DATABASE_URL" docs/architecture.md`
  returns at least one match for each.
- `AGENTS.md` and `.env.example` reviewed per the Requirements above;
  updated if warranted, left alone with a note in this task's report if
  not (do not force a change to files that do not need one).
- A final read-through of `docs/architecture.md`'s updated sections
  confirms they accurately describe the shipped behavior (permanent
  dual-name support, env-var-always-wins, fetch-or-generate-and-persist
  fallback, fail-loudly corruption handling, the new DB-dependent failure
  mode) as implemented by Phases 3-4's tasks, not merely as planned.

## Status

- **Outcome:** succeeded (2026-08-10).
- **Validation summary:**
  - `docs/architecture.md`'s Data model table now includes an
    `auth_jwt_secrets` row (matching the `system_actors` row's
    "operational, not domain data" framing) and a new `### Configuration
    and secrets bootstrap` subsection (nested under `## Overview`,
    alongside the existing `### Runtime service dependencies`) describes
    both the permanent `MFAPP_DATABASE_URL`/`DB_URL` precedence and the
    `JWT_SECRET` fetch-or-generate-persist fallback, framed explicitly as
    a new pre-pool, per-boot Postgres dependency inside `Load()` (not
    merely "a new table exists") — per the phase-4 gate's architecture-
    conformance finding F1.
  - `grep -n "auth_jwt_secrets\|MFAPP_DATABASE_URL" docs/architecture.md`
    returns matches for both (lines 23 and 50 post-edit).
  - Per finding F2, the `## API layer` section now names
    `config.Load()`'s JWT-secret bootstrap as the one deliberate exception
    to this module's `requires.infra: pool`-mediated Postgres access
    pattern, and explains why (`Load()` runs before the pool exists in
    every generated `main.go`).
  - `AGENTS.md`'s "Database migrations" section reviewed: left unchanged.
    It already documents the mechanism generically (no enumerated
    migration-file list, no per-file callouts for `0100`/`0101`), so
    adding a `0102`-specific mention would break that established style
    rather than match it — no change warranted.
  - `.env.example` reviewed: left unchanged. Phase 3's task already
    updated the `DB_URL`/`MFAPP_DATABASE_URL` trailing comments
    (confirmed accurate against the shipped `resolveDBURL()` behavior);
    the `JWT_SECRET` line was not touched, since the task's own
    Requirements scope the `.env.example` review to the `DB_URL`/
    `MFAPP_DATABASE_URL` comments only, and the existing `JWT_SECRET` line
    makes no claim (e.g. "required") that the new DB-bootstrap fallback
    contradicts.
  - Confirmed no other in-repo doc contradicts the shipped behavior:
    `README.md`, `next-steps.md`, `docs/project-structure.md`, and
    `docs/oidc-troubleshooting.md` make no mention of `DB_URL` or
    `JWT_SECRET`; `docs/mod-users-spec.md` still makes no mention of
    `JWT_SECRET`/`DB_URL`/`config.Load` (re-confirmed via grep, matching
    the task doc's own prediction).
  - Final read-through of the two new/edited `docs/architecture.md`
    passages against Phase 3's and Phase 4's task-doc `## Status` sections
    confirms the description matches shipped behavior (permanent
    dual-name support, env-var-always-wins, fetch-or-generate-and-persist,
    fail-loudly corruption handling, the new DB-dependent failure mode),
    not merely what was planned.
- **Affected files:**
  - `docs/architecture.md`
  - `plan/phase-05-doc-updates/001-update-architecture-docs.md` (this
    file — `## Status` added)
