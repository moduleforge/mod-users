# Audit Handlers

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This is a read-only audit of every mod-users route and every service method those routes call:

- the 45 routes registered by the registrars in `api/internal/handlers/*_routes.go`, `api/internal/handlers/oidcconfig_routes.go` and `api/internal/handlers/auth/routes.go`;
- the dev-only `DELETE /v1/assume` and the health routes;
- the service methods beneath them, in `api/internal/service/*.go` and in the composition-root closures in `api/cmd/server/main.go`.

Follow the per-handler procedure in [common section 2](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#2-per-handler-conformance-audit-procedure). The output is the authoritative audit table that later tasks work from. Make no source changes.

## Requirements

1. Verify every row of the [preliminary route table](../notes/handler-inventory.md#preliminary-route-table) and every planning decision D1-D15 in the same note against source. Read each handler and the service method beneath it in full; the reserved slot requires a full review of every handler file.
2. Append the five-axis table under [Audit table](../notes/handler-inventory.md#audit-table). Give one row per route and do not collapse rows. Each row records:
   - the route class (authenticated, verified, public, or credential-authenticated);
   - the authn gate;
   - the stateless checks and where they run;
   - the authorize call and its target;
   - the resolve/existence classification;
   - the post-authorization errors with the planned classification;
   - the wire-visible effect;
   - the in-process caller effect.
3. Append a second table that classifies every literal `server.Error(`/`server.ErrorWithDetails(` call site outside `server/respond.go`, under D12, as convert or keep, with the reason. Search all of `api/`, not only `handlers/`. Give the exact count per file and say whether it matches the planner's 66 + 4 + 6.
4. Confirm the D2 premise. `user_accounts.uuid` is not an `entities.uuid`. Read mod-users' `Authorizer.Authorize` (`api/internal/authz/authz.go:220`) and confirm that target `0` quietly denies a non-admin and is harmless for a wildcard admin (common rule 5).
5. Confirm the D5 premise against mod-core `main` (`/Users/zane/playground/moduleforge/mod-core/api/service/natural_person.go`, `UpdateByEntityUUID`). Then check whether a non-admin's `update` on their own entity is granted by the `Authorizer`'s ownership arm, which is how `PUT /self` works today.
6. Grep the playground (`/Users/zane/playground/moduleforge`, excluding `worktrees/` and `node_modules/`) for in-process callers of `UserAccountService.Get`/`Update`/`Delete`/`Assume`/`LoadByUUID` and of `SSHKeyService`. Grep also for HTTP clients of each mod-users route. List them in the [Consumers affected](../notes/handler-inventory.md#consumers-affected) section.
7. Confirm by grep that no non-test code in `api/internal/handlers` calls `opctx.ActorEntityID` or `RequireAuthenticated` (D1). Confirm that `GET /oidc-config/saved` is unauthenticated, and record what it returns.
8. Where source contradicts the note, correct the preliminary table in place and list each correction in your report. Do not change any decision yourself; flag it.
9. File genuinely out-of-scope findings with `followups_add` (`plan_slug: users-conformance`), per common rule 9.

## Validation

- `git diff --stat -- . ':!plan/'` is empty (no source changes).
- `plan/notes/handler-inventory.md` contains, under `## Audit table`, one row per route (45 registrar routes plus the D-rows) and the literal-site table.
- The route count matches source: `grep -hcE 'r\.(Get|Post|Put|Delete)\(' api/internal/handlers/*_routes.go api/internal/handlers/auth/routes.go | paste -sd+ - | bc` gives 45 (the glob already includes `oidcconfig_routes.go`). Explain any difference.

## Metadata

architectural_impact: false

## References

- [Common conformance instructions](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md): sections 1, 2 and 4.
- [http-error-precedence.md](/Users/zane/playground/moduleforge/docs-mf-standards/architecture/http-error-precedence.md): the normative spec.
- [Handler inventory](../notes/handler-inventory.md) and [wire-risk note](../notes/wire-risk.md).
- [Sibling audit example](/Users/zane/playground/moduleforge/mod-authz/worktrees/plan/authz-conformance/plan/notes/handler-inventory.md): the shape of the authz plan's table.

## Status

- Outcome: succeeded (2026-10-11).
- Output: `plan/notes/handler-inventory.md`. The preliminary route table was corrected in place (12 corrections listed under "Corrections to the preliminary table"), and `## Audit table` now holds the five-axis table (45 routes plus D1-D3), the D1-D15 verification, the literal-site table (76 sites, disposition per site) and findings F1-F18. The Consumers affected section and the dependent-tests list were extended.
- Validation: `git diff --stat -- . ':!plan/'` is empty; the audit table has rows 1-45 and D1-D3 plus the literal-site table; the route-count command gives 45 (the glob includes `oidcconfig_routes.go`).
- Decisions flagged, not changed: F1 (GUI `new_password` vs server `password`, the D6 halt case), F3 (D12 register literals vs displayed messages), F4 (`Assume` sudo-unaware actor lookup), F5 (masked key miss after authorization), F8 (confirm drops unknown provider ids), F10 (provider GET bad id 404 vs 400).
- Findings filed: 9qmR, 4Olf, bc1h, 1KXD, bxIA, FAVu.
