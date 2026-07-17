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
