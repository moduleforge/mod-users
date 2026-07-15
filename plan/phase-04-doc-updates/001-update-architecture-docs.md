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
