# Update Architecture Docs

## Purpose and scope

Update `mod-users`' architecture and API-reference documentation to reflect the public HTTP
error-contract changes made by this session's implementation phases: the reserved top-level
`error.code` vocabulary (replacing the ad-hoc codes) and the new optional `details` array on the error
envelope. Runs after the implementation phases have landed.

Invoke the `update-architecture-docs` task-procedure at
`plugins/flow/task-procedures/update-architecture-docs/SKILL.md` (resolve via the provided
`plugin_root`).

## Requirements

The following implementation task documents surfaced the architectural implications (all are error-
contract / public-API changes):

- `plan/phase-01-go-apiresp-foundation/001-adopt-apiresp-sentinels-and-writer.md`
- `plan/phase-02-go-vocab-migration/001-migrate-apps-oidc-handlers.md`
- `plan/phase-02-go-vocab-migration/002-migrate-identity-account-self-handlers.md`

Review and update these files:

1. **`api/openapi.yaml`** (the authoritative REST API spec). The `Error` schema (~lines 34–46)
   currently has `example: bad_request` and only `code`/`message`. Update it:
   - Change the `example` from `bad_request` to `invalid_input`.
   - Add the optional `details` array (a `FieldError` object array: `field`, `code`, `message`) to the
     error object schema.
   - Document the reserved top-level code vocabulary (`unauthenticated`, `forbidden`, `not_found`,
     `invalid_input`, `conflict`, `internal_error`) — e.g. as an `enum` or description on `code`.
   - Sweep per-endpoint response examples for stale codes (`bad_request`, `unauthorized`,
     `email_taken`, `bad_credentials`, `validation_error`, `identity_not_found`) and update them to the
     migrated shape. Note: the flat-envelope codes deferred by this plan (`step_up_required`,
     `last_identity`, `email_unverified`, `oidc_not_confirmed`) are **out of scope** — leave any spec
     entries for them unchanged.

2. **`docs/architecture.md`** — the "API layer" and "Authentication flow" sections. Currently they
   carry no reserved-code references except a `400 anonymous_account` mention (~line 54, a flat-
   envelope code not migrated by this plan — leave it, but note it in the deferred-set follow-up).
   Add/adjust any description of the error envelope/vocabulary to match the canonical nested
   `{error:{code,message[,details]}}` shape and reserved vocabulary if the doc describes it.

3. **`docs/mod-users-spec.md`** — review for any behavioral contract that names error codes or the
   error shape; reconcile to the migrated vocabulary. (Research found no direct error-code references,
   but confirm during the review.)

Also confirm whether any deferred flat-envelope sites (plan overview "Open scope question") warrant a
note in the docs as known non-conformances; if so, add a brief pointer rather than documenting them as
canonical.

role_doc: plugins/flow/references/roles/architect-backend.md

## Validation

- `api/openapi.yaml` validates (if a validator is available) and its `Error` schema documents the
  reserved vocabulary + optional `details`; no stale ad-hoc code remains in non-deferred examples
  (`grep -n 'bad_request\|"unauthorized"\|email_taken\|bad_credentials\|validation_error\|identity_not_found' api/openapi.yaml`
  returns only intentionally-retained/deferred entries, if any).
- `docs/architecture.md` and `docs/mod-users-spec.md` describe the error contract consistently with
  the design doc and the implemented code; any deferred flat-envelope sites are noted as known
  non-conformances rather than canonical.
- The named files were each reviewed (record which required no change).

## Metadata

architectural_impact: true

## Assumptions

- The implementation phases (1–2) have landed, so the actual migrated codes/shapes are observable in
  the handler code and can be mirrored into the docs.

## References

- `docs/mf-standards/architecture/api-response-design.md` — canonical envelope, reserved vocabulary,
  and `details`/`FieldError` shape.
- `plugins/flow/task-procedures/update-architecture-docs/SKILL.md` (via `plugin_root`) — the procedure.
- Plan `overview.md` "Open scope question" — the deferred flat-envelope codes to leave untouched.

## Status

**Implementation outcome:** succeeded

**Date:** 2026-07-16

**Implementation summary:**

1. **`api/openapi.yaml`** — updated the `Error` schema: `code`'s `example` changed from
   `bad_request` to `invalid_input`; added a `code` `enum` (`unauthenticated`, `forbidden`,
   `not_found`, `invalid_input`, `conflict`, `internal_error`) plus a description explaining the
   closed top-level vocabulary and the `details[].code` module-namespacing convention; added the
   optional `details` array property (`items: $ref FieldError`) and a new `FieldError` schema
   (`field`, `code`, `message`, all required, mirroring the design doc's GUI-facing wire type).
   Swept the whole file for the other stale ad-hoc codes
   (`bad_request`/`unauthorized`/`email_taken`/`bad_credentials`/`validation_error`/
   `identity_not_found`) — the only prior occurrence was the one `Error.code.example` line already
   fixed; no per-endpoint response example embedded a stale code. `make openapi.validate` (the
   `python3 -c "import yaml..."` fallback, since `spectral` is not installed in this environment)
   confirms the YAML still parses.
2. **`docs/architecture.md`** — the "API layer" section had no error-envelope/vocabulary
   description to begin with (confirmed by re-reading the whole file), so nothing there needed
   updating. The "Authentication flow" section's `400 anonymous_account` mention (the file's only
   reserved-code-adjacent reference) is now annotated in place as a flat, non-nested envelope and a
   known, intentionally-deferred non-conformance with the module's canonical nested shape — left
   unmigrated, per the task doc's explicit instruction, but no longer reads as if it conforms.
3. **`docs/mod-users-spec.md`** — reviewed in full (no other error-code or error-shape references
   found beyond the ones already known). Three edits: (a) the "General features" bullet "All errors
   return a structured payload…" was rewritten to describe the actual nested
   `{error:{code,message[,details]}}` envelope, the closed reserved top-level vocabulary, and the
   optional namespaced `details[].code` convention, with a pointer to `api/openapi.yaml`'s
   `Error`/`FieldError` schemas as the authoritative shape; (b) the use-case-14 `anonymous_account`
   mention and (c) the Security-requirements `anonymous_account` mention are both now annotated as
   the same known, intentionally-deferred flat-envelope non-conformance (cross-referencing the
   General features bullet), left unmigrated per the task doc.

**Files touched:**
- `api/openapi.yaml`
- `docs/architecture.md`
- `docs/mod-users-spec.md`

**Validation:**
- `make openapi.validate` — passes (YAML syntax OK via the python3+pyyaml fallback; `spectral` is
  not installed in this sandbox).
- `grep -n 'bad_request\|"unauthorized"\|email_taken\|bad_credentials\|validation_error\|identity_not_found' api/openapi.yaml`
  — the only matches are the two new, intentional `users.email_taken` example strings documenting
  the namespaced `details[].code` shape (in the `code` property's description and the `FieldError`
  schema's own `code` example) — not a leftover top-level ad-hoc code.
- `docs/architecture.md` and `docs/mod-users-spec.md` reviewed in full; each was required to change
  (see summary above); the `anonymous_account` deferred sites are now explicitly annotated as known
  non-conformances rather than reading as canonical.
- Re-read all three edited files after editing: no dangling cross-references introduced (the new
  `[General features](#general-features)` / `[use case 14](#14-create-an-anonymous-account-and-optionally-upgrade-it)`
  / `[Security requirements](#security-requirements)` anchors match GitHub's heading-slug algorithm
  applied to the existing headings, and `[api/openapi.yaml](../api/openapi.yaml)` reuses the same
  relative path the doc already used elsewhere); no contradicted claims introduced within or across
  the three files.

**Assumptions applied:**
- Per `## Assumptions`, treated Phases 1–2 as landed and mirrored the actually-migrated
  codes/shapes from the handler code into the docs — see the important caveat below, however.

**Flagged for manager:**
- **Significant, newly-discovered scope gap (not caused by this task, but affects how completely
  the docs now match runtime behavior):** `api/internal/handlers/auth/*.go`
  (`register.go`, `login.go`, `emailcode.go`, `anonymous.go`, `oidc.go`, `reset.go` — the
  register/login/anonymous/email-code/OIDC-start-callback/password-reset endpoints) was **never in
  scope for either Phase 2 task's file list** (task 001 covered `apps.go`/`oidc_providers.go`/
  `oidc_config.go`; task 002 covered `identities.go`/`user_accounts.go`/`self.go`/`assume.go`) and
  still contains roughly 24 unmigrated literal `server.Error(...)` sites using the old ad-hoc codes
  (`bad_request`, `unauthorized`, `validation_error`, `email_taken`) — confirmed by grep; distinct
  from, and not covered by, the already-tracked followup `8iRl` ("Literal server.Error sites not
  centralized", which only lists the 28 sites across the seven files the two Phase 2 tasks *did*
  touch). Net effect: the new `openapi.yaml` `Error.code` `enum` I added (the reserved 6-code closed
  set, per this task's explicit Requirement 1) now describes a target contract that a meaningful
  slice of the actual API surface — including the two highest-traffic unauthenticated entry points,
  register and login — does not yet meet. I did not touch `api/internal/handlers/auth/*.go` (Go
  source, outside a docs-only task's scope and this role/tier), and did not soften the new enum,
  since the task doc explicitly directed documenting the target vocabulary regardless. Recommend the
  manager register a follow-up (a "Phase 2b"-style task) to migrate this package the same way Phase
  2's two tasks did, and/or add an explicit followups.yaml entry alongside `8iRl` so the gap doesn't
  fall out of view.
- **Separate, likely-pre-existing behavioral question (not fixed, flagged only):** both
  `docs/architecture.md` and `docs/mod-users-spec.md` state that `login`/`email-code`/
  `password-reset` "guard against anonymous accounts and return `400 anonymous_account` if called
  with an anonymous JWT." I could not find `anonymous_account` anywhere in `api/internal` (grepped
  the whole tree), nor any `IsAnonymous`/`is_anonymous` check in `login.go`, `emailcode.go`, or
  `reset.go` — and those three endpoints are unauthenticated (`security: []`, submit email/password
  in the body), so an "anonymous JWT" guard doesn't obviously fit their actual shape.
  `PasswordResetRequest` (`reset.go`) does special-case a NULL/empty `ua.Email` (anonymous account),
  but only to skip sending the email while still always returning `204` — no `400` is ever emitted.
  This looks like a pre-existing documentation/implementation mismatch predating this plan (not
  something Phases 1–2 touched or introduced), out of scope for this error-vocabulary-focused task
  to resolve, but worth a dedicated accuracy pass — the annotations I added treat the code (not
  necessarily the underlying guard's existence) as the known-deferred item, per the task doc's
  explicit instruction to treat `anonymous_account` as one of the deferred flat-envelope sites.
- `docs/mf-standards` is a git submodule that was not initialized in this worktree at task start
  (`git submodule status` showed it as `-<sha>`, i.e. uninitialized); ran
  `git submodule update --init docs/mf-standards` to read the referenced design doc. This is a
  read-only local checkout action (the submodule pointer in the index was already correct and is
  unchanged), not a committed change — noting in case other task worktrees in this plan hit the same
  uninitialized-submodule state and need the same step before they can read
  `docs/mf-standards/architecture/api-response-design.md`.
