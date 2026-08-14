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

## Status

**Outcome:** succeeded. **Date:** 2026-08-14.

Implemented entirely in `api/openapi.yaml`:

- Added `components.schemas.Action` (required `code`/`message`/`path`, optional `data`), mirroring the
  `Error`/`FieldError` convention. `code` enumerates all three registered `mod-users` action codes
  (`users.email_unverified`, `users.step_up_required`, `users.oidc_not_confirmed`); `data` documents the
  `oidc_not_confirmed`-only `{state: <boot-state>}` shape, cross-referencing
  `api/internal/config/oidc_state.go`'s `BootState` for the two states reachable via this response
  (`init_failed`, `no_env_no_flag` — the two non-`Confirmed()` states).
- The existing `api/openapi.yaml` convention references `Error` directly in `responses` (no `{error:
  ...}` wrapper), so `Action` is referenced the same way (no `{action: ...}` wrapper) per the task's
  "mirror whatever convention is already in use" instruction.
- Added three reusable `components.responses` entries: `EmailUnverified` (403, `Action` only —
  used where no prior 403 was documented), `ForbiddenOrEmailUnverified` (403, `oneOf: [Error, Action]`
  — replaces the 14 existing `Forbidden` refs on endpoints that sit behind both the authz check and
  `RequireVerifiedEmail`), and `OIDCNotConfirmed` (503, `Action` only).
- Wired responses by tracing the actual Phase 1 middleware chain (`api/cmd/server/main.go` +
  `moduleforge.module.yaml` route-entry `middleware:` lists), not just the task doc's one named example,
  since both action codes verifiably "apply broadly across `/v1/*` routes" per the task doc's own
  framing:
  - `503` (`OIDCNotConfirmed`) added to all 26 already-documented `/v1/auth/*` and `/v1/*` operations
    (every operation sits behind `RequireOIDCConfirmed`; `/healthz`, `/readyz` are outside `/v1` and
    untouched; `/v1/oidc-config/*` is outside the gate by design and not documented in this file).
  - `403` added only where `RequireVerifiedEmail` actually gates the route today: `PUT /v1/self`
    (`EmailUnverified`) and the 14 admin/self endpoints already carrying `403 Forbidden`
    (`ForbiddenOrEmailUnverified`) — `/v1/users*`, `/v1/apps/{app_uuid}/user-accounts*`, `/v1/audit*`.
  - `GET /v1/self` was **not** given a 403: per `main.go` (`r.Get("/self", ...)` mounted outside the
    nested `r.Group` that applies `auth.RequireVerifiedEmail`) and the manifest's
    `handlers.RegisterSelfGetRoute` entry (`middleware: [requireOIDCConfirmed, requireAuth]`, no
    `requireVerifiedEmail`), `GET /v1/self` can only ever return the 503 shape, not 403
    `email_unverified`. This corrects the task doc's `## Requirements` parenthetical ("e.g. GET
    /v1/self, which can surface both") — see `decisions_made` / `flagged_for_manager` in the structured
    report for the discrepancy. Only `PUT /v1/self` can actually surface both.
- No `verify_path`/`config_path`/flat bespoke bodies were found anywhere in `api/openapi.yaml` (the file
  never documented the phase-1-superseded shapes) — the "retire" requirement was already satisfied;
  confirmed via grep, no edit needed.
- **Not done, by design:** no identities/credential endpoints were added to `api/openapi.yaml` (out of
  scope, per followup `biJk`). The `Action.code` enum already includes `users.step_up_required` so the
  schema is ready when those endpoints land. **The `biJk`-note extension itself was not made** — this
  task agent has no access to edit `plan/followups.yaml` (master-plan-doc, manager-owned); see the
  structured report's `flagged_for_manager` for the exact text the manager should add.

**Validation:** `make openapi.validate` passes (YAML-syntax check; no `spectral` installed in this
environment). Supplemented with a manual full-document `$ref`-resolution walk (every `$ref` in the file
resolves to an existing schema/response/parameter) since no OpenAPI-specific validator was available.
Both required greps pass (`action|Action|users.*` matches the new schema/wiring; `verify_path|config_path`
has zero matches). Manually cross-checked the `Action` schema's fields, the wired 403/503 bodies, and
`data.state` against `api/internal/auth/require_verified.go`, `require_confirmed.go`, and
`api/internal/useraction/action_codes.go` — messages, `path` values (`/verify-email`, `/oidc-config`),
statuses, and codes match exactly.

**Files touched:** `api/openapi.yaml` only.
