# Document Action-Required Envelope In OpenAPI

## Purpose and scope

Add the action-required response kind to `api/openapi.yaml` — AGENTS.md's designated authoritative
REST API specification — so the machine-readable contract matches the shapes Phase 1 ships. Today the
OpenAPI document defines only the `Error`/`FieldError` schemas (lines ~35-82) and has no notion of an
`action` envelope. Scope is limited to `api/openapi.yaml`; the prose docs are handled by the sibling
task `001-document-action-required-in-spec-and-architecture`. This task must run after Phase 1 has
landed. No dedicated skill covers this change; implement directly per the requirements below.

## Requirements

### Add the action-required envelope schema

- Under `components.schemas`, add an action-required envelope schema mirroring the existing `Error`/
  `FieldError` convention and the design doc's field table
  (`docs/mf-standards/architecture/api-response-design.md`, "The `action` object"). Define an
  `Action` object schema with:
  - `code` (string, required) — module-namespaced action code from a closed set; enumerate the three
    mod-users codes (`users.email_unverified`, `users.step_up_required`, `users.oidc_not_confirmed`)
    or describe them as the closed set, consistent with how `Error.code` documents its reserved enum.
  - `message` (string, required) — human-readable prompt to act (not an error).
  - `path` (string, required) — application-relative navigation target (no scheme, no `//` authority).
  - `data` (object, optional) — kind-specific auxiliary state; omitted when absent (document that it
    is present only for `users.oidc_not_confirmed`, carrying `{state: <boot-state>}`).
- If the OpenAPI document wraps its error schema in a top-level envelope object (e.g. an
  `{error: Error}` response wrapper), add the parallel `{action: Action}` wrapper; if it references
  `Error` directly in responses, mirror whatever convention is already in use. Match the file's
  existing style rather than importing a new one.

### Wire the responses this document can express today

- `users.email_unverified` (403) and `users.oidc_not_confirmed` (503) are emitted by the
  `RequireVerifiedEmail` / `RequireOIDCConfirmed` middleware and apply broadly across `/v1/*` routes.
  Document these action-required responses on the endpoints/paths where the OpenAPI document already
  expresses them (e.g. `GET /v1/self`, which can surface both), referencing the new `Action` schema.
  Include the `503` `oidc_not_confirmed` shape with its `data.state`, and note the `503` is written
  verbatim and never remapped (per the design doc's action-required status rule).
- Retire any bespoke flat response bodies (`verify_path`, `config_path`, flat top-level `error` string
  values) currently documented for these two sites, if present.

### Handle the identities/credential surface gap (followup `biJk`)

- `users.step_up_required` (409) and `users.last_identity` (409) live on the `/v1/self/identities` and
  `/v1/self/credential/*` endpoints, which — per followup `biJk` (already on `main`) — are **entirely
  absent** from `api/openapi.yaml` today (the OpenAPI document has zero coverage of this surface).
  Documenting their responses would require first adding those endpoints, which is a larger, separate
  effort already tracked by `biJk` and out of this action-required plan's scope.
- Therefore: do **not** add the full identities/credential endpoint surface here. Instead, ensure the
  new `Action` schema's `code` set/enum includes `users.step_up_required` (so the schema is complete
  and ready for those endpoints when they are added), and record/confirm a follow-up (or extend
  `biJk`'s note) that when the identities/credential endpoints are added to `api/openapi.yaml`, their
  `409` `step_up_required` (action-required) and `409` `last_identity` (`conflict` + detail) responses
  must be documented using the schemas this task adds. Do not silently drop this — carry it forward
  as an explicit deferred item.

## Validation

- `api/openapi.yaml` parses/validates as a well-formed OpenAPI document (use whatever validation the
  repo already runs for it; if none, a YAML-parse + schema-ref-resolution sanity check).
- `grep -n "action\|Action\|users.email_unverified\|users.oidc_not_confirmed\|users.step_up_required"
  api/openapi.yaml` returns the new schema and wired responses.
- `grep -n "verify_path\|config_path" api/openapi.yaml` returns no matches (retired flat field names
  gone where they were pinned).
- The new `Action` schema's fields and the wired `403`/`503` responses match the shipped Phase 1 code
  (`api/internal/auth/require_verified.go`, `require_confirmed.go`, `api/internal/useraction/`) and the
  design doc's field table exactly (codes, statuses, `data.state` shape).
- The `biJk` deferral for the identities/credential `409` responses is recorded exactly once (confirm
  via `plan/followups.yaml` or the followups tool), with no plan/phase/task-name references in its
  text.

## Metadata

architectural_impact: false

## Assumptions

- Phase 1 has landed; this task documents the shipped shapes. The prose task
  (`001-document-action-required-in-spec-and-architecture`) may run before, after, or in parallel —
  the two edit disjoint files.
- `api/openapi.yaml` currently has no action-required schema and no identities/credential surface
  coverage (verified 2026-07-20; followup `biJk`). If either has changed by the time this task runs,
  adapt to the real state rather than the state assumed here.

## References

- `docs/mf-standards/architecture/api-response-design.md` — "The `action` object" field table and the
  action-required status rule (verbatim status, never remapped).
- `api/openapi.yaml` — the existing `Error`/`FieldError` schemas (lines ~35-82) whose convention the
  new `Action` schema mirrors; the `responses` blocks (from line ~436) where the migrated responses
  are wired.
- `plan/phase-01-go-action-required-migration/003-migrate-require-verified-middleware.md`,
  `.../004-migrate-require-oidc-confirmed-middleware.md`,
  `.../005-migrate-identities-step-up-and-last-identity.md` — the shipped shapes.
- Followup `biJk` (in `plan/followups.yaml`) — the pre-existing `api/openapi.yaml` identities/
  credential/step-up coverage gap this task defers to.
