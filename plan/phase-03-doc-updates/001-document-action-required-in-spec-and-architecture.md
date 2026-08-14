# Document Action-Required Envelope In Spec And Architecture

## Purpose and scope

Update mod-users' prose documentation — `docs/mod-users-spec.md` and `docs/architecture.md` — to
describe the action-required response kind and the two reserved-mechanism changes that Phase 1
introduces on five live endpoints. The action-required envelope is a **new response kind** these docs
do not currently describe at all; two other sites change their error representation. Scope is limited
to the two prose documents; `api/openapi.yaml` is handled by the sibling task
`002-document-action-required-in-openapi`. This task must run after Phase 1 has landed (it documents
the shipped shapes). No dedicated skill covers this change; implement directly per the requirements
below, keeping the docs consistent with the shipped Phase 1 code.

## Requirements

### The five migrated shapes to document (final, from Phase 1)

- `users.email_unverified` — 403, action-required envelope, `action.path = "/verify-email"`, no
  `action.data` (from task 003).
- `users.oidc_not_confirmed` — 503 (written verbatim, never remapped), action-required envelope,
  `action.path = "/oidc-config"`, `action.data = {"state": "<boot-state>"}` (from task 004).
- `users.step_up_required` — 409, action-required envelope, `action.path = "/step-up"`, no
  `action.data` (from task 005).
- `users.last_identity` — 409, **nested error** envelope: top-level `error.code = "conflict"` with a
  `details[]` entry `{field:"", code:"users.last_identity", message:"You can't remove your last
  sign-in method. Add another first."}` (from task 005; this is NOT an action-required response).
- `RequireVerifiedEmail` misconfiguration branch — 500, reserved-core `internal_error` (from task
  003; a programmer-error path, mention only if the doc documents middleware failure modes).

The `users.email_taken` 409 wire shape is **unchanged** (top-level `conflict` + `users.email_taken`
detail) — `docs/mod-users-spec.md`'s "structured payload" paragraph already names it; no change is
needed for it.

### `docs/mod-users-spec.md`

- In the security/response-contract section (the "structured payload" bullet at line ~178 that
  documents the top-level `error` object and its reserved vocabulary), add a parallel description of
  the **action-required** response kind: a top-level `action` object, mutually exclusive with `error`,
  carrying `code` (module-namespaced, from a closed set), `message` (a prompt to act, not an error),
  `path` (an application-relative navigation target), and an optional `data` object. List the three
  mod-users action codes with their bound statuses and paths. Frame it as "navigate, don't alarm"
  (the client navigates the user to `action.path` rather than surfacing an error banner), and point to
  `docs/mf-standards/architecture/api-response-design.md` as the authoritative contract, mirroring how
  the existing error-envelope bullet points to `api/openapi.yaml`.
- In use case 9's Outcome (line ~100), which currently reads `409 (last_identity)` and `409
  (step_up_required)` in old flat terms: update these to the shipped shapes — `step_up_required` is
  now an action-required response (`users.step_up_required`, 409, navigate to `/step-up`);
  `last_identity` is now a `conflict` with a `users.last_identity` detail. Keep the surrounding
  behavioral description (when each is returned) intact; only the response-shape wording changes.
- Wherever the spec describes the email-verified gate (the 403 for an unverified account on
  verified-email-gated endpoints) and the OIDC-not-confirmed 503, update any response-shape wording to
  the action-required envelope (`users.email_unverified` / `users.oidc_not_confirmed`). If the spec
  does not currently pin those response shapes, no edit is needed there beyond the new
  action-required paragraph above.
- Retire any remaining references to the retired flat field names (`verify_path`, `challenge_path`,
  `config_path`) or the flat top-level `error` string values for these sites, if present.

### `docs/architecture.md`

- The Identities/Credentials API-layer row/paragraph (line ~59) describes the step-up gating; if it
  pins the `409`/`step_up_required` response shape, update it to note the action-required envelope.
- If `docs/architecture.md` has a section describing the response-envelope contract or the auth
  middleware (`RequireVerifiedEmail`, `RequireOIDCConfirmed`) behavior, add a short note that these
  gates now emit the action-required envelope (`users.email_unverified` 403, `users.oidc_not_confirmed`
  503 with `data.state`) via mod-core's `apiresp.WriteActionRequired`, and that the mod-users action
  codes are owned in a small internal registry (`api/internal/useraction`, added by task 001). Keep
  this proportional — a note reflecting the new mechanism, not a full rewrite.
- If neither document currently pins these response shapes at a given site, prefer adding the new
  action-required description over inventing per-site detail the doc did not previously carry.

## Validation

- `grep -n "action\b\|users.email_unverified\|users.step_up_required\|users.oidc_not_confirmed\|users.last_identity" docs/mod-users-spec.md`
  returns the new action-required content.
- `grep -n "verify_path\|challenge_path\|config_path" docs/mod-users-spec.md docs/architecture.md`
  returns no matches for the migrated sites (retired field names gone where they were pinned).
- Cross-check every documented code/status/path against the Phase 1 task docs and the shipped code
  (`api/internal/auth/require_verified.go`, `require_confirmed.go`,
  `api/internal/handlers/identities.go`, `api/internal/useraction/`): all codes, statuses, and paths
  match exactly.
- No contradiction remains with `moduleforge.module.yaml` or the handler source (endpoint paths,
  verbs, gating, and now response shapes match).
- Markdown renders cleanly; any API-layer table stays well-formed (pipe-count parity across rows).
- `api/openapi.yaml` is handled by the sibling task — do not edit it here; if a cross-reference to it
  is warranted, add prose only.

## Metadata

architectural_impact: false

## Assumptions

- Phase 1 has landed, so the final codes/statuses/paths are the shipped ones (this task documents
  reality, not intent). If any Phase 1 shape differs from what this task assumes, document the shipped
  shape and note the divergence.
- The `action.path` values (`/verify-email`, `/step-up`, `/oidc-config`) are resolved (Answer, D2) and
  are what the docs must state; the cross-repo requirement that consuming apps mount `/verify-email`
  and `/step-up` is flagged for the manager and out of scope for this doc task.
- No markdown lint target exists in this repo's Makefile (per the prior doc-updates plan); structural
  sanity is checked manually.

## References

- `plan/phase-01-go-action-required-migration/003-migrate-require-verified-middleware.md`,
  `.../004-migrate-require-oidc-confirmed-middleware.md`,
  `.../005-migrate-identities-step-up-and-last-identity.md` — the shipped shapes this task documents.
- `docs/mf-standards/architecture/api-response-design.md` — "Action-required responses", "The
  `action` object", "Action-code vocabulary", "Action-required status set".
- `plan/notes/action-path-values-and-decisions.md` — D2, D3, D7, and the Answer (resolved paths).
- `docs/mod-users-spec.md` (use case 9 outcome ~line 100; structured-payload bullet ~line 178),
  `docs/architecture.md` (Identities/Credentials row ~line 59) — the sites this task updates.

## Status

- **Outcome**: succeeded
- **Date**: 2026-08-14
- **Validation summary**: `grep -n "action\b|users.email_unverified|users.step_up_required|users.oidc_not_confirmed|users.last_identity" docs/mod-users-spec.md` returned the new action-required paragraph, the use-case-9 outcome update, and the pre-existing audit-log bullet (unrelated "action" match). `grep -n "verify_path|challenge_path|config_path" docs/mod-users-spec.md docs/architecture.md` returned no matches (these retired field names were never pinned in either doc, so nothing needed removal). Cross-checked all five migrated codes/statuses/paths against the shipped Phase 1 code (`api/internal/auth/require_verified.go`, `require_confirmed.go`, `api/internal/handlers/identities.go`, `api/internal/useraction/action_codes.go`) — all match exactly (`users.email_unverified` 403 `/verify-email`; `users.oidc_not_confirmed` 503 `/oidc-config` with `data.state`; `users.step_up_required` 409 `/step-up`; `users.last_identity` as a `conflict` detail entry). No contradiction found against `moduleforge.module.yaml` (`/v1/oidc-config` is the API prefix, distinct from the `/oidc-config` GUI route the doc now references, matching the plan notes' D2). Markdown table pipe-count in `docs/architecture.md`'s API-layer table is unchanged (new content added as prose paragraphs, not table rows); backtick parity checked programmatically for both files.
- **Affected source files**:
  - `docs/mod-users-spec.md`
  - `docs/architecture.md`
- **Assumptions applied**: Phase 1 had landed on the branch this worktree was cut from, so the final shipped codes/statuses/paths (confirmed by reading the Phase 1 task docs' own `## Status` sections and the source files directly) were documented as reality, matching the task's assumption. `/verify-email`, `/step-up`, and `/oidc-config` used as the literal `action.path` values per the plan notes' recorded Answer (Reading A) — no divergence found between the assumed and shipped shapes.
- **Note**: `docs/mf-standards` is a git submodule that was not checked out in this worktree at task start (empty directory); it was initialized read-only (`git submodule update --init docs/mf-standards`) to read `api-response-design.md` for cross-checking. This is a read-only local checkout operation on an already-committed submodule pointer, not a content change — `git status` shows no diff for it.
