# Wire Risk Docs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Record every wire-visible change this plan made in the project docs, name the downstream clients, and document the error order and the helper usage. Follow [common rule 4](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#4-rules-all-plans) and the "Docs and wire-risk note" shape in [common section 6](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#6-standard-per-plan-task-template). The source is the [wire-risk note](../notes/wire-risk.md#recorded-by-tasks): what tasks actually recorded, not the planned list.

## Requirements

1. Reconcile the planned list with the recorded entries in the wire-risk note. Each planned item is either recorded, or explained as not done, with the task that dropped it.
2. **`api/openapi.yaml`.**
   - Add an "Error precedence" note to the top-level description: 401, 400, 403, 404, then 409/422, with the documented exceptions D8 and D11 from the [handler inventory](../notes/handler-inventory.md#planning-decisions).
   - Update the operations that exist in the file: the `/v1/auth/*` routes, `/v1/self`, the apps membership paths, and the ssh-keys paths. Cover the new 400 details, 403 masking, 409 and 422 responses, and strict decoding.
   - The file's user-account paths are stale: it lists `/v1/users...`, but the routes are `/v1/user-accounts...`. Its identities surface is also missing (followup biJk). Do not rewrite those sections. Note the drift in the report; it is tracked by project followup fs0H.
3. **`docs/consumer-upgrade-notes.md`.** Add a section for this change listing every recorded wire change, grouped as HTTP and in-process. Name the clients:
   - `@moduleforge/users-gui`;
   - the apps that compose mod-users (grep `/Users/zane/playground/moduleforge/app-*` for `mod-users` in their manifests and list them);
   - apps pairing mod-core's `ProfileEditor` with `PUT /v1/self`;
   - external `/v1/auth/*` clients;
   - in-process `UserAccountService` callers.

   Include the target-0 authorizer note (common rule 5) and the fact that composing apps do not need to change their manifests for this plan (D1).
4. **`docs/mod-users-spec.md`.** Update the security requirements: user-account existence masking (it now matches the ssh-key routes), anti-enumeration unchanged, and the request order including the step-up position (D9) and the documented exceptions.
5. **`AGENTS.md` Conventions.** "Authorization is checked first in every service method, before any data access" is now inaccurate. Replace it with the precedence rule:
   - stateless request-shape checks in the handler (`server.Decode`/`Validator`, `apiresp.ParseUUIDParam`) before authorization;
   - masked lookups for user accounts;
   - registrars mount `RequireAuth`;
   - public and credential routes never do.

   Link the spec at `docs/mf-standards/architecture/http-error-precedence.md` (the submodule path, consistent with the existing links).
6. **`README.md`.** Add a one-line pointer to the consumer upgrade notes entry if the existing link text does not already cover it.
7. Do not link any `plan/` path from these docs (ephemeral linking rule).

## Validation

```bash
git diff --stat -- . ':!plan/'   # only api/openapi.yaml, docs/*.md, AGENTS.md, README.md
grep -rn '](.*plan/' README.md AGENTS.md docs/ | grep -v 'plan/followups.yaml\|plan/plan-summary-\|plan/summary-\|](./plan/)'   # no hits
python3 -c 'import yaml,sys; yaml.safe_load(open("api/openapi.yaml"))'   # OpenAPI still parses
```

## Metadata

architectural_impact: false

## Assumptions

- Phases 1-4 have landed. Task 5.1 may run in parallel; it touches only `gui/`.

## Status

- Outcome: succeeded (2026-10-11). Validation: diff scope limited to api/openapi.yaml, docs/consumer-upgrade-notes.md, docs/mod-users-spec.md, AGENTS.md; no plan/ links; OpenAPI parses. README.md unchanged (its existing consumer-upgrade-notes link text covers this).
- Planned vs recorded: all 13 planned items were recorded; 5 and 7 are done in 4.1 (409 and 422). The 3.4 note now says a body user_uuid naming no account is 422.
- Drift noted, not rewritten: openapi.yaml still lists `/v1/users...` (routes are `/v1/user-accounts...`) and lacks the identities surface (followups fs0H, biJk).
- Folded in: anti-enumeration status corrected from 202 to 204 in docs/mod-users-spec.md and the two api/openapi.yaml responses (code returns 204).
