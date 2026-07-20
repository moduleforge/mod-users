# Phase — Documentation Updates

## Goals

Bring mod-users' human-facing and machine-facing API documentation into line with the wire-shape
changes Phase 1 makes to five live endpoints. The action-required envelope is a **new response kind**
not currently described anywhere in mod-users' own docs, and two sites additionally change their error
representation (`last_identity` → nested `conflict` + detail; the `require_verified` internal-error
branch → reserved-core `internal_error`). This phase reviews and updates the three documents the
migration renders stale — `docs/mod-users-spec.md`, `docs/architecture.md`, and `api/openapi.yaml` —
so the documented contract matches the shipped one. This is the architectural-implications follow-on
the plan's overview anticipated; it must run **after** Phase 1 has defined the concrete final shapes
(it references the Phase 1 task docs), and it depends only on those shapes, not on the GUI phase.

Scope of work:

- Document the action-required envelope (`{"action": {code, message, path, data?}}`) and the three
  mod-users action codes and their bound statuses/paths — `users.email_unverified` (403,
  `/verify-email`), `users.step_up_required` (409, `/step-up`), `users.oidc_not_confirmed` (503,
  `/oidc-config`, `data.state`) — in the prose spec and (as a schema) in the OpenAPI document.
- Update the two reserved-mechanism sites' documented behavior: `writeLastIdentityError`'s 409 now
  renders as a top-level `conflict` with a `users.last_identity` detail (not a bespoke flat
  `last_identity` body), and the `RequireVerifiedEmail` misconfiguration branch renders as the
  reserved-core `internal_error` 500.
- Retire references to the old flat field names (`verify_path`, `challenge_path`, `config_path`, and
  the flat top-level `error` string values `email_unverified`/`step_up_required`/`last_identity`/
  `oidc_not_confirmed`) wherever the docs pin them.
- Respect the pre-existing `api/openapi.yaml` identities/credential/step-up coverage gap (followup
  `biJk`): the OpenAPI document does not currently describe the `/v1/self/identities` and
  `/v1/self/credential/*` surface at all, so `step_up_required`/`last_identity` OpenAPI response
  documentation is entangled with that separate gap and is scoped/deferred accordingly in task 002.

## Inputs

- The five Phase 1 task documents that define the final shapes:
  `phase-01-go-action-required-migration/003-migrate-require-verified-middleware.md`,
  `.../004-migrate-require-oidc-confirmed-middleware.md`,
  `.../005-migrate-identities-step-up-and-last-identity.md`, and the `ZVum` fold-in
  `.../002-fold-in-zvum-email-taken-conflict.md` (the `email_taken` 409 wire shape is unchanged, so it
  needs no doc change beyond what already exists).
- The finalized contract: `docs/mf-standards/architecture/api-response-design.md` — "Action-required
  responses", "The `action` object", "Action-code vocabulary", "Action-required status set" (the
  authoritative envelope shape, field semantics, and the verbatim-status rule).
- Resolved decisions and the `action.path` values in
  `plan/notes/action-path-values-and-decisions.md` (D2, D3, D7, and the Answer).
- The three target docs' current state: `docs/mod-users-spec.md` (use case 9 outcome, line ~100,
  already names the `409 (step_up_required)` / `409 (last_identity)` responses in old flat terms; the
  "structured payload" error-envelope paragraph, line ~178, describes the error envelope but not the
  action envelope); `docs/architecture.md` (Identities/Credentials API-layer row/paragraph, line ~59);
  `api/openapi.yaml` (`Error`/`FieldError` schemas at lines ~35-82; no action envelope; no
  identities/credential surface — followup `biJk`).

## Outputs

- `docs/mod-users-spec.md` and `docs/architecture.md` describe the action-required response kind and
  the two reserved-mechanism changes accurately, with no remaining references to the retired flat
  field names for the migrated sites (task 001).
- `api/openapi.yaml` gains an action-required envelope schema mirroring the design doc, wired onto the
  middleware-level responses it can express today (`users.email_unverified` 403 and
  `users.oidc_not_confirmed` 503), with the `step_up_required`/`last_identity` OpenAPI coverage
  handled per the `biJk` gap (task 002).
- No contradiction remains between the docs and the shipped Phase 1 code (codes, statuses, paths, and
  envelope members match).
