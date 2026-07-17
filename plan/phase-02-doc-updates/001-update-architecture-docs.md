# Update Architecture Docs

## Purpose and scope

Update `api/openapi.yaml` and review `docs/architecture.md`/`docs/mod-users-spec.md` to reflect that
`api/internal/handlers/auth/*.go`'s six endpoint groups now conform to the reserved `error.code`
vocabulary, once both Phase 01 tasks have landed. This task exists because the architectural-
implications checklist run at the end of this plan's planning session found a genuine public-API
contract change (the wire-visible `error.code` values these endpoints emit) and a spec-conformance
change (the `openapi.yaml` `Error` schema explicitly documents these endpoints as non-conformant
today).

Invoke the `update-architecture-docs` task-procedure (`plugins/flow/task-procedures/update-architecture-docs/SKILL.md`)
for the general review mechanics; the specific, concrete edit this task must make is described in
Requirement 1 below.

role_doc: `plugins/flow/references/roles/architect-backend.md`

## Requirements

1. **`api/openapi.yaml`'s `Error` schema description** (`components.schemas.Error.description`,
   currently lines ~37-48) contains a paragraph — added by the prior `users-apiresp-migration` plan's
   Phase 4, citing follow-up `biPE` — disclosing that six endpoint groups don't yet conform to the
   `code` enum:

   > `/v1/auth/register`, `/v1/auth/anonymous`, `/v1/auth/login`, `/v1/auth/email-code/*`,
   > `/v1/auth/password-reset/*`, and `/v1/auth/oidc/*`. Tracked as follow-up `biPE` in
   > `plan/followups.yaml`; migrating these handlers to the enum is out of scope for the current task.

   Now that this plan's Phase 01 tasks
   (`phase-01-auth-vocab-migration/001-migrate-local-auth-handlers.md` and
   `phase-01-auth-vocab-migration/002-migrate-oidc-auth-handler.md`) have migrated all six of these
   endpoint groups onto the reserved vocabulary, remove this disclosure (or rewrite it to state the
   endpoints now conform, whichever reads more naturally as a permanent schema description — prefer
   removal if nothing else in the description depends on the caveat existing). Confirm via grep
   (`grep -n "biPE\|does not conform\|legacy, ad-hoc" api/openapi.yaml`) that no other schema or
   endpoint description in the file still references this now-resolved gap.

2. **Review `docs/architecture.md` and `docs/mod-users-spec.md`** (the `docs/*-spec.md` glob) for any
   other reference to these six endpoint groups being non-conformant with the canonical nested error
   envelope. As of this task's authoring, the only related mentions in these two files concern a
   *different*, unrelated, and already-tracked gap — the documented-but-unconfirmed `400
   anonymous_account` flat-envelope behavior (follow-up `nnfn`), which does not appear anywhere in the
   current `api/internal` Go source and was not touched by this plan's Phase 01 tasks. Do not conflate
   the two: this task's Phase 01 sibling tasks migrated real, existing ad-hoc top-level codes
   (`bad_request`, `unauthorized`, `validation_error`, `email_taken`) that *did* exist in the six
   files; they did not add or remove any `anonymous_account` guard logic. If your review confirms
   `docs/architecture.md`/`docs/mod-users-spec.md` need no edit beyond what Requirement 1 covers in
   `openapi.yaml`, say so explicitly in your task report rather than silently skipping the files.

3. **Do not attempt to resolve follow-up `nnfn`** (the `anonymous_account` doc-accuracy question) as
   part of this task — it is a separate, pre-existing, unrelated documentation question the prior
   plan already flagged and explicitly deferred to a dedicated pass.

## Validation

- `grep -n "biPE\|does not conform\|legacy, ad-hoc" api/openapi.yaml` returns no matches (or, if the
  description was rewritten rather than removed, no matches for language claiming the six endpoint
  groups are still non-conformant).
- Read the full, current `Error` schema description in `api/openapi.yaml` after your edit and confirm
  it accurately describes the module's current state (fully conformant, no disclosure of exceptions)
  unless a genuine remaining exception exists (there should not be one — the 5 flat-envelope sites
  from follow-up `eiF8` are not part of this schema's `Error` object at all; they use a different,
  bespoke response shape and are not referenced by this schema's description in the first place, so
  no residual caveat about them belongs here).
- Confirm `docs/architecture.md` and `docs/mod-users-spec.md` were read in full and either updated or
  explicitly confirmed to need no change, with the reasoning recorded in your task report.
- `make lint` (or whatever spec-lint tooling this repo has for `openapi.yaml`, if any) still passes
  after the edit.

## Status

**Outcome:** succeeded. **Date:** 2026-07-16.

**Requirement 1 (`api/openapi.yaml`).** Removed the entire stale `description:` block from
`components.schemas.Error` (12 lines: the `biPE`-citing paragraph disclosing the six endpoint groups
as non-conformant). Git history confirms this `description` field did not exist prior to the prior
`users-apiresp-migration` plan's Phase 4 commit `ebc05c4` that introduced it — so removal (rather than
a rewrite asserting conformance) is the correct choice per the task doc's stated preference: nothing
else in the schema depended on the description existing, and every sibling top-level schema in this
file (`FieldError`, `HealthStatus`, `UserDetail`, etc.) likewise has no top-level `description`, so
removal restores consistency with the file's established convention rather than introducing a new
permanent-but-empty caveat. The `code` property's own description ("drawn from the reserved top-level
vocabulary... a closed set") already makes the intended positive claim without any exception language.

**Requirement 2 (doc review).** Read `docs/architecture.md` (137 lines) and `docs/mod-users-spec.md`
(296 lines) in full. Grepped both for `biPE`, `non-conform`, `does not conform`, `legacy, ad-hoc`,
`ad-hoc`, `bad_request`, `"unauthorized"`, `validation_error`, and `email_taken`. Every match found (4
total, at `architecture.md:56` and `mod-users-spec.md:162,168,261`) concerns the separate, already-
tracked `400 anonymous_account` flat-envelope documentation question (follow-up `nnfn`), not the six
endpoint groups' `error.code` vocabulary conformance. No other stale reference to the six endpoint
groups being non-conformant exists in either file — **no edit was needed or made** to
`docs/architecture.md` or `docs/mod-users-spec.md`.

**Requirement 3.** Follow-up `nnfn` was left untouched, as instructed — the `anonymous_account`
mentions identified above were read only to confirm they are out of scope, not edited.

**Validation summary:**
- `grep -n "biPE\|does not conform\|legacy, ad-hoc" api/openapi.yaml` — no matches (confirmed post-edit).
- Re-read the full `Error` schema after the edit: it now carries no top-level `description`, no
  disclosure of exceptions, and the `code` property description makes an unqualified positive claim —
  accurately reflecting that all endpoints in this file, including the six migrated auth groups,
  conform to the enum. The 5 deferred flat-envelope sites (follow-up `eiF8`) are correctly not
  referenced anywhere in this schema, as they use a different, bespoke response shape outside this
  `Error` object.
- `docs/architecture.md` and `docs/mod-users-spec.md` were read in full and confirmed to need no
  change (see Requirement 2 above).
- `make openapi.validate` (this repo's dedicated spec-lint target for `openapi.yaml`, falling back to
  `python3 -c "import yaml..."` since `spectral` is not installed) — passed: `api/openapi.yaml: YAML
  syntax OK`. Full `make lint` was attempted but timed out after 2 minutes attempting to resolve
  toolchains across all sub-projects (`dependencies_installed: not installed` for this task worktree,
  consistent with the plan overview's noted "not installed" state); since this is a docs-only change
  with no Go or TypeScript source touched, `make openapi.validate` is the applicable, self-sufficient
  check and was used instead per the task doc's own "(or whatever spec-lint tooling this repo has...)"
  allowance.

**Assumptions applied:** none beyond the task doc's own text — no `## Assumptions` section was present
on this task doc.
