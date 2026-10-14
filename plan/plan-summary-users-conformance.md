# Plan Summary: users-conformance

## What was planned and why

Bring every mod-users HTTP handler (`api/internal/handlers/*`, `api/internal/handlers/auth/*`) and the service methods beneath them (`api/internal/service/*`) into conformance with the HTTP error-precedence rule: 401 (unauthenticated), then 400 (stateless request shape: path UUID parse, body decode and shape), then 403, then 404. State-dependent 409/422 come only after authorization. This is the mod-users share of plan-group `users-conformance` in wave program `http-error-precedence-conformance` (wave 2). It follows the shape of the completed sibling plan `core-handler-conformance` (mod-core and mod-audit) and the planned sibling `authz-conformance` (mod-authz).

The shared, plan-independent instructions live in the wave's [common conformance instructions](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md) on mod-core `main`. They cover the rule, the audit procedure, helper signatures, the order-test recipe, standing rules 1-9, and pins and releasing. Task documents link to its sections ("common section N", "common rule N") instead of restating them. The normative spec is [http-error-precedence.md](/Users/zane/playground/moduleforge/docs-mf-standards/architecture/http-error-precedence.md). This repo's `docs/mf-standards` submodule is not initialized, so read the sibling `docs-mf-standards` checkout. Lessons from the sibling plan are in its [plan summary](/Users/zane/playground/moduleforge/mod-core/plan/plan-summary-core-handler-conformance.md).

In scope (the authoritative reserved-slot description in the wave manifest, plus manager additions):

- Delete the 403-before-400 special case in `user_accounts.go:63-76`. Rule: stateless validation runs before authorization; followup hunL was dismissed.
- Membership handlers (`apps.go` ~75, 142, 172, 218) parse `{uuid}` and decode the body before authorizing.
- `server.Decode` (`respond.go:50`, 16 callers) becomes a thin wrapper over `apiresp.DecodeJSON`. This closes the size-cap and unknown-field gaps. Also convert `oidc_providers.go:421`, and keep the ssh-keys cap.
- Delete the `writeAuthzError`/`writeServiceError` wrappers, plus `writeCoreServiceErr` (project followup 6ZEz).
- Convert the literal `server.Error` sites to sentinels where equivalent. Preserve action-required envelopes and credential-failure 401s.
- Review every handler file in full. Public auth routes must not get `RequireAuth`.
- Audit 409/422 and add order tests.
- Make a small GUI fix: align `gui/src/lib/api.ts` apps create/update payloads with the core apps contract (project followup PRvD).
- Check the impact of mod-core's new update-route behavior on mod-users callers. Since mod-core's `core-handler-conformance` work, `NaturalPersonService.UpdateByEntityUUID` (called at `self.go:79`) does two things: it returns `ErrForbidden` on a miss and needs an authenticated actor (`AuthorizeResolve`), and it rejects blank `given_name`/`family_name` with 400.
- Re-bump the mod-core pin, as a final consent-gated task that never pushes.

Out of scope:

- The composing apps' own code, including their `grantAdmin`/`revokeAdmin` composition-root closures, regenerated `cmd/server` files, and GUIs that embed mod-core's `ProfileEditor`. These get wire-risk notes only.
- mod-core and mod-authz source, which this plan reads but never edits.
- The `scope:` field of the module manifest (decision D1).
- Pin bumps other than the final mod-core re-bump, any other lockfile edit, and any push.

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Bring every mod-users HTTP handler (`api/internal/handlers/*`, `api/internal/handlers/auth/*`) and the service methods beneath them (`api/internal/service/*`) into conformance with the HTTP error-precedence rule: 401 (unauthenticated), then 400 (stateless request shape: path UUID parse, body decode and shape), then 403, then 404. State-dependent 409/422 come only after authorization. This is the mod-users share of plan-group `users-conformance` in wave program `http-error-precedence-conformance` (wave 2). It follows the shape of the completed sibling plan `core-handler-conformance` (mod-core and mod-audit) and the planned sibling `authz-conformance` (mod-authz).

The shared, plan-independent instructions live in the wave's [common conformance instructions](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md) on mod-core `main`. They cover the rule, the audit procedure, helper signatures, the order-test recipe, standing rules 1-9, and pins and releasing. Task documents link to its sections ("common section N", "common rule N") instead of restating them. The normative spec is [http-error-precedence.md](/Users/zane/playground/moduleforge/docs-mf-standards/architecture/http-error-precedence.md). This repo's `docs/mf-standards` submodule is not initialized, so read the sibling `docs-mf-standards` checkout. Lessons from the sibling plan are in its [plan summary](/Users/zane/playground/moduleforge/mod-core/plan/plan-summary-core-handler-conformance.md).

In scope (the authoritative reserved-slot description in the wave manifest, plus manager additions):

- Delete the 403-before-400 special case in `user_accounts.go:63-76`. Rule: stateless validation runs before authorization; followup hunL was dismissed.
- Membership handlers (`apps.go` ~75, 142, 172, 218) parse `{uuid}` and decode the body before authorizing.
- `server.Decode` (`respond.go:50`, 16 callers) becomes a thin wrapper over `apiresp.DecodeJSON`. This closes the size-cap and unknown-field gaps. Also convert `oidc_providers.go:421`, and keep the ssh-keys cap.
- Delete the `writeAuthzError`/`writeServiceError` wrappers, plus `writeCoreServiceErr` (project followup 6ZEz).
- Convert the literal `server.Error` sites to sentinels where equivalent. Preserve action-required envelopes and credential-failure 401s.
- Review every handler file in full. Public auth routes must not get `RequireAuth`.
- Audit 409/422 and add order tests.
- Make a small GUI fix: align `gui/src/lib/api.ts` apps create/update payloads with the core apps contract (project followup PRvD).
- Check the impact of mod-core's new update-route behavior on mod-users callers. Since mod-core's `core-handler-conformance` work, `NaturalPersonService.UpdateByEntityUUID` (called at `self.go:79`) does two things: it returns `ErrForbidden` on a miss and needs an authenticated actor (`AuthorizeResolve`), and it rejects blank `given_name`/`family_name` with 400.
- Re-bump the mod-core pin, as a final consent-gated task that never pushes.

Out of scope:

- The composing apps' own code, including their `grantAdmin`/`revokeAdmin` composition-root closures, regenerated `cmd/server` files, and GUIs that embed mod-core's `ProfileEditor`. These get wire-risk notes only.
- mod-core and mod-authz source, which this plan reads but never edits.
- The `scope:` field of the module manifest (decision D1).
- Pin bumps other than the final mod-core re-bump, any other lockfile edit, and any push.

## Current status

Plan created; no task has run. Phase 1 starts with task 1.1, the read-only audit.

Pre-conditions found during planning (2026-10-11):

- The baseline is green at plan base `a2d704c`: `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `go test ./...` and `gofmt -l` (all from `api/`), and `api/scripts/check-server-error-literals.sh` (4/4 carve-outs).
- The wave-1 helpers are present both on the local mod-core `main` checkout (what worktree builds use) and at the current CI pin `2ea072d`. The `core-handler-conformance` behavior changes (the `self.go:79` impact) are only on the local mod-core `main`. They are merged but not pushed: local `main` is 243 commits ahead of `origin/main`. That is why the pin re-bump (phase 7) needs the user's consent to push mod-core first.
- How task worktrees build against mod-core: [building from a task worktree](./notes/handler-inventory.md#building-and-validating-from-a-task-worktree).

## Overview

Success criteria:

- Every mod-users route returns the rule's order. The documented exceptions are recorded in the [handler inventory](./notes/handler-inventory.md#planning-decisions): credential-in-body routes (D8) and the middleware caller-state gates (D11).
- No caller without authority over a user account can tell a missing account UUID from an existing one.
- Success shapes, action-required envelopes, credential-failure 401s, and existing error `code` values are unchanged, except for the recorded wire risks.
- The following pass: `gofmt`, `go vet` (with and without `-tags=integration`), `go test ./...`, `make -C api lint`, the Postgres integration suite, and the GUI typecheck and tests for the touched GUI file.
- `ordertest` or dedicated order tests cover every route.

Hard constraints:

- Common rule 1: never delete a check before its replacement is in place.
- Every `Register*` function and `New*Handler` constructor, and the public facade `api/handlers/handlers.go`, keep their signatures, because `moduleforge.module.yaml` and the composing apps call them.
- No pushes. No pin, lockfile, `go.mod` or `go.sum` edits, except in the final phase-7 task (common rule 8).

Planner findings that shape the tasks are in the [handler inventory](./notes/handler-inventory.md). It holds decisions D1-D15, consumers, the preliminary route table, and the literal-site and followup dispositions. The planned wire-visible changes are in the [wire-risk note](./notes/wire-risk.md).

Phases run in order. Within a phase, tasks touching the same Go package run sequentially. This is a sibling-plan lesson: two parallel tasks once declared the same test helper in one package and broke the build. Every task that adds test helpers to `api/internal/handlers` uses a task-specific identifier prefix and greps for collisions first.

1. **`authenticate-routes`** (phase 01; sequential)
   1. `001-audit-handlers` (sonnet-med): a read-only five-axis audit of all 45 registrar routes, plus dev-only and health routes, and the service methods beneath them. It also classifies every literal `server.Error` site. The result completes the table in the handler inventory note.
   2. `002-mount-require-auth` (sonnet-med): mount mod-core `authz.RequireAuth` inside an `r.Group` in the 8 authenticated registrars (D1), not in `authhandlers.RegisterRoutes` or `RegisterOIDCConfigRoutes`. Add 401 tests. There are no inline 401 checks to remove; the service-level `Assume` check is handled in 2.1.
2. **`existence-leaks`** (phase 02)
   1. `001-user-account-masked-miss` (sonnet-med): add a masked user-account lookup helper (D2) and use it in `Get`/`Update`/`Delete`/`Assume` and in `SSHKeyService.loadAuthorizedAccount`. A missing actor on `Assume` becomes 401.
3. **`decode-and-shape`** (phase 03; all in `api/internal/handlers` or `service`, so sequential)
   1. `001-remove-pass-through-wrappers` (haiku-med): delete `writeAuthzError`, `writeServiceError` and `writeCoreServiceErr` (D13).
   2. `002-decode-wrapper-and-callers` (sonnet-med): change `server.Decode` to a `DecodeJSON` wrapper (D6) and convert all 16 callers. This removes the `Create` 403-before-400 special case. The ssh-keys cap moves to `WithMaxBytes`. Every client body is cross-checked against the server structs.
   3. `003-user-accounts-and-self-shape` (sonnet-med): `Validator`s for user-account create and update, `ParseUUIDParam`, and the `PUT /self` blank-name checks (D4, D5). Followup N9AG is covered by real-handler tests.
   4. `004-apps-membership-shape` (sonnet-med): parse and decode before the nil-target authorize; post-authorization app lookup via `GetAppByUUID`; drop `AllowNotFound("app")` (D3).
   5. `005-self-service-shape` (sonnet-med): identities and ssh-keys run their stateless checks before step-up (D9), and SSH key parsing runs before authorization (D10).
   6. `006-oidc-config-credential-shape` (sonnet-high): the credential-in-body ordering (D8) and `parseBody` on the empty-body path with a cap and a key allowlist (D7). The 409 carve-out becomes `apiresp.Conflict`.
   7. `007-public-auth-error-literals` (sonnet-med): convert the literals in `handlers/auth` to sentinels where they are equivalent (D12).
4. **`conflicts-and-order-tests`** (phase 04; sequential)
   1. `001-state-conflict-audit` (sonnet-med): classify every post-authorization 400/404/500 (D15).
   2. `002-order-tests-account-routes` (sonnet-med): the shared order harness (D14) and the 12 account and apps routes.
   3. `003-order-tests-self-service-routes` (sonnet-med): the 15 self, identities and ssh-keys routes, plus the D11 gate tests.
   4. `004-order-tests-credential-and-public-routes` (sonnet-med): dedicated tests for the 8 oidc-config and 10 auth routes.
5. **`gui-wire-docs-and-validation`** (phase 05)
   1. `001-gui-apps-client-alignment` (haiku-med): PRvD, plus the `addMember` `roles` payload. Parallel-eligible with 5.2.
   2. `002-wire-risk-docs` (sonnet-low): OpenAPI, README, AGENTS.md, the spec and `docs/consumer-upgrade-notes.md`, with downstream clients named.
   3. `003-full-validation` (sonnet-low): the full suite including Postgres integration and the GUI, checklist greps, and the closed audit table. Runs after 5.1 and 5.2.
6. **`doc-updates`** (phase 06): `001-update-architecture-docs`, registered by the architectural-implications check because the plan changes the public HTTP API's error behavior and spec-documented behavior (existence masking).
7. **`pin-rebump`** (phase 07): `001-rebump-mod-core-pin` (haiku-med). This is the final task. It runs only after the user has consented to push mod-core and the push has happened. It runs `make pins.update REPOS="mod-core"` and commits the lockfile on its task branch, and it never pushes. Without that consent it halts as `blocked`.

Parallel-eligible: only 5.1 with 5.2. Everything else is sequential, because each later task depends on the previous one's code or shares its Go package.

## What shipped

### Phase 01 — Audit and Authenticate Routes

1. **Audit Handlers Against the Precedence Rule** (`001-audit-handlers.md`, tier `sonnet-med`) — Read-only audit: 45-route five-axis table, D1-D15 verification, 76-literal-site disposition table (convert 56, keep 20), findings F1-F18, 12 preliminary-table corrections. Open decisions F1 (GUI reset sends new_password), F3, F4 (Assume must use effective actor), F5 resolved by manager.
   Commit `3f44b9d`, merged at `77ffe8ad94323b007d76ae9e07445af2b80fc93d`.

2. **Mount RequireAuth in Authenticated Registrars** (`002-mount-require-auth.md`, tier `sonnet-med`) — Mounted coreAuthz.RequireAuth inside r.Group in the 8 authenticated registrars; 27-route anonymous 401 / malformed-input 401 / sudo-only not-401 tests (red before mount: 51 subtests); existing tests get requireAuthStandIn; make lint passes; public auth routes untouched. F4 (Assume sudo-unaware admin lookup) is left to the service task.
   Commit `e21c59c`, merged at `92fe351ff555741c6201ebb030671c9179d08ed4`.

### Phase 02 — Close Existence Leaks

1. **Mask User Account Misses in the Service Layer** (`001-user-account-masked-miss.md`, tier `sonnet-med`) — authorizeUserAccount masked-miss helper used by Get/Update/Delete/Assume and ssh-key account check; miss and forbidden both ErrForbidden with one target-0 Authorize; Assume uses effective actor after authorization (F4); handler 404 branches removed; unit and handler tests; integration suite 132 pass, 0 skip; wire-risk entries.
   Commit `5804983`, merged at `e47b9047ceef64a5e144a1aca570544e9d7c0d41`.

### Phase 03 — Decode and Shape Before Authorization

1. **Remove Pass-Through Error Wrappers** (`001-remove-pass-through-wrappers.md`, tier `haiku-med`) — Removed writeAuthzError, writeServiceError and writeCoreServiceErr wrappers; all call sites use apiresp.WriteError; two wrapper-named tests renamed; ZVum comments reworded; no behavior change; make lint passes.
   Commit `48b8723`, merged at `fd0d0dd5618f4838035bacfc138ce205dffac31e`.

2. **Make server.Decode a DecodeJSON Wrapper and Convert Callers** (`002-decode-wrapper-and-callers.md`, tier `sonnet-med`) — server.Decode is a thin wrapper over apiresp.DecodeJSON; all 16 callers converted; ssh-key register uses WithMaxBytes(32 KiB); UserAccountsHandler.Create 403-before-400 special case deleted; real-handler decode tests; client audit found only the known GUI mismatches (resetPassword new_password, apps.addMember role) left to task 5.1; integration 48 pass 0 skip.
   Commit `071c510`, merged at `324a392dd3745076c014650569c6cf3e9aa3e90d`.

3. **Stateless Checks Before Authorization for User Accounts and Self** (`003-user-accounts-and-self-shape.md`, tier `sonnet-med`) — Create/update/PUT /self Validators and ParseUUIDParam put 400s ahead of authorization; local parseUUIDParam deleted; service Update blank checks after masked authz; real-handler shape tests replace the shim; integration tests for non-admin self-update and assume-session; D5 assume-session reading in inventory was reversed (corrected in wire-risk); PUT /self names in an assume session now 403 (filed as followup).
   Commit `698b7ff`, merged at `6597d6c4f1811dbcd49576968e1f7329ff7c66b6`.

4. **Apps Membership Parse and Decode Before Authorization** (`004-apps-membership-shape.md`, tier `sonnet-med`) — Apps membership handlers parse path UUIDs and decode/validate body before the nil-target Authorize; assignUserRequest.Validate; loadApp replaces loadAppByUUIDParam; AllowNotFound('app') removed; handler authz tests rewritten; user-account 404s remain post-authorization (task 4.1 will reclassify); integration 142 pass 0 skip.
   Commit `53d3a85`, merged at `7a9c26d8d8bc1cb7e8e30e422d1aea9eba9deae4`.

5. **Identities and SSH Keys Stateless Checks Before Step-Up and Authorization** (`005-self-service-shape.md`, tier `sonnet-med`) — Stateless checks (path UUIDs, body decode, required fields, ssh key/label parse) moved ahead of the step-up gate on identities and ssh-key routes; users.new_password_required and users.public_key_required codes; parseUUIDPathParam deleted; selfsvc tests; identities.go has no user-account ErrNoRows 404s; integration 134 pass 0 skip.
   Commit `5e5fe90`, merged at `17255c76a9e6cc11604d521997f6d0795441829e`.

6. **OIDC Config Credential Routes Decode and Ordering** (`006-oidc-config-credential-shape.md`, tier `sonnet-high`) — OIDC config credential routes: ambient credentials then decode (400) then body setup_token (401) with documented credential-in-body exception; parseBody 1 MiB allowlist with unknown-key 400; GET provider bad id now 400; 409 uses apiresp.Conflict detail users.oidc_provider_exists (carve-outs 3); oidccfg tests (red then green); credential-failure 401s and action-required envelopes preserved; header now wins over a wrong body token; integration 261 pass 0 skip.
   Commit `f56122a`, merged at `ec2f72ca4bc9d6c0be5f3e8ad00823457f51cfd0`.

7. **Convert Public Auth Error Literals to Sentinels** (`007-public-auth-error-literals.md`, tier `sonnet-med`) — Converted 48 literal sites in handlers/auth (36 500s to apiresp.WriteError wraps, 400s with details and 7 new detail codes, 3 unknown-provider 404s); kept 11 literals (6 credential-failure 401s, 5 register literals per F3) with literal-keep markers; lint script now checks handlers/ (3) and handlers/auth (11); public_error_shape tests; docs/cookie-path.md updated; integration 250 pass 0 skip.
   Commit `a244ae4`, merged at `32972e752c0a40d940b6a570dea959c6591b2943`.

### Phase 04 — State Conflicts and Order Tests

1. **State-Conflict Audit for 409 vs 422** (`001-state-conflict-audit.md`, tier `sonnet-med`) — Duplicate email on PUT/POST user-accounts now 409 users.email_taken (isEmailTakenViolation, closes 4Olf); apps add-member unknown user_uuid now 422; rest classified and left; findings v2BM, 8VhV filed.
   Commit `cb5e2b2`, merged at `cbad77ba7d2e`.

2. **Order Tests for Account and Apps Membership Routes** (`002-order-tests-account-routes.md`, tier `sonnet-med`) — Shared order harness (order_helpers_test.go) and order tests for the 12 account/apps routes; red checks verified; no production code changed.
   Commit `631b627`, merged at `a1b638ef3f13`.

3. **Order Tests for Self, Identities and SSH Key Routes** (`003-order-tests-self-service-routes.md`, tier `sonnet-med`) — Order tests for 15 self/identities/ssh-keys routes plus D11 gate tests; red check via moving requireStepUp above Decode; no production change. Limit: IdentitiesHandler concrete pool, DELETE identity/password past step-up not driven.
   Commit `73f3b09`, merged at `046389bdd2ab`.

4. **Order Tests for Credential and Public Routes** (`004-order-tests-credential-and-public-routes.md`, tier `sonnet-med`) — Order tests for 8 oidc-config and 10 public auth routes plus composed-router check; red check via swapping credential/decode order; anti-enumeration pinned at 204 (task text said 202); no production change.
   Commit `cce6aed`, merged at `ccd84409f4d5`.

### Phase 05 — GUI Alignment, Wire Risk Docs and Validation

1. **Align GUI Apps Client Payloads** (`001-gui-apps-client-alignment.md`, tier `haiku-med`) — GUI aligned: CreateApp slug/name, addMember roles, reset-password field renamed to password (9qmR), add-provider modal reads structured details (jzT7); typecheck and 230 gui tests pass; scope expansion to two components was manager-directed.
   Commit `692ea96`, merged at `de54236577bd`.

2. **Document Wire Risks and Helper Usage** (`002-wire-risk-docs.md`, tier `sonnet-low`) — OpenAPI precedence section, consumer upgrade notes naming clients, spec bullet, AGENTS.md convention, ErrEmailTaken comment, wire-risk reconciled; anti-enumeration corrected to 204.
   Commit `53dcacd`, merged at `8eabc850f360`.

3. **Full Validation Pass** (`003-full-validation.md`, tier `sonnet-low`) — Full validation green: gofmt, vet (both tags), go test, make lint, model tests, Postgres integration, gui typecheck and 230 tests, checklist greps; audit closure recorded by evidence; no code changes.
   Commit `a3428e0`, merged at `7480fbb16fe7`.

### Phase 06 — Documentation Updates

1. **Update Architecture Docs** (`001-update-architecture-docs.md`, tier `sonnet-high`) — architecture.md, ssh-keys.md and spec updated for error-precedence (D20/D21, masked miss, RequireAuth groups, exceptions); stale entityResolver claims fixed.
   Commit `a074520`, merged at `c0bca9f7eb02`.

### Phase 07 — Re-bump mod-core Pin

_No tasks are recorded in this phase._

### Phase 08 — Remediation Round 1

1. **Close Step-Up Ordering Gaps for SSH Keys and StartLink** (`001-step-up-ordering-gaps.md`, tier `sonnet-med`) — SSH key/label shape validated in registerSSHKeyRequest.Validate before step-up (ValidateSSHKeyShape); StartLink validates return query before step-up (ValidateReturnPath, ErrInvalidReturnPath), internal link failures now 500; AuthorizeCreate comment fixed; stepord tests; integration 250 pass 0 skip. Fixes jAXe, P2k6, gKQO.
   Commit `9a366ad`, merged at `13e4dc4cc4bacbc622e53616bd1f697e91b8425b`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Findings

- **`jAXe`** — **ssh key parse runs after step-up** — fixed — ref: `phase-08-remediation-01/001-step-up-ordering-gaps.md` — 2026-10-11

- **`P2k6`** — **StartLink return check runs after step-up** — fixed — ref: `phase-08-remediation-01/001-step-up-ordering-gaps.md` — 2026-10-11

- **`gKQO`** — **AuthorizeCreate comment is stale** — fixed — ref: `phase-08-remediation-01/001-step-up-ordering-gaps.md` — 2026-10-11

- **`9qmR`** — **GUI reset-password sends new_password** — fixed — ref: `phase-05-gui-wire-docs-and-validation/001-gui-apps-client-alignment.md` — 2026-10-12

- **`jzT7`** — **GUI loses 409 provider-exists message** — fixed — ref: `phase-05-gui-wire-docs-and-validation/001-gui-apps-client-alignment.md` — 2026-10-12

- **`4Olf`** — **Create never returns 409 email_taken** — fixed — ref: `phase-04-conflicts-and-order-tests/001-state-conflict-audit.md` — 2026-10-12

- **`FAVu`** — **/readyz returns raw DB error unauthenticated** — promoted — ref: `FAVu` — 2026-10-12

- **`bc1h`** — **credential_change code violates CHECK** — promoted — ref: `bc1h` — 2026-10-12

- **`1KXD`** — **Fire-and-forget email sends use dead ctx** — promoted — ref: `1KXD` — 2026-10-12

- **`bxIA`** — **GUI admin/audit paths do not match server** — promoted — ref: `bxIA` — 2026-10-12

- **`jQbw`** — **oidc confirm drops unknown provider ids** — promoted — ref: `jQbw` — 2026-10-12

- **`v2BM`** — **Unlink: 409 precedes identity 404** — promoted — ref: `v2BM` — 2026-10-12

- **`8VhV`** — **OIDC link insert race gives 500** — promoted — ref: `8VhV` — 2026-10-12

## Remediation

- Rounds used: 1 (resolved max_rounds: 3).
- Remediation tasks added: 1 (resolved max_added_tasks: 10).
- Remediation phases:
  - `remediation-01` — 1 task(s)
- Security review required: no.

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — Audit and Authenticate Routes

- [x] [001-audit-handlers.md](./phase-01-authenticate-routes/001-audit-handlers.md) — tier `sonnet-med` · branch `plan/users-conformance-01-001` · commit `3f44b9d` · merge `77ffe8ad94323b007d76ae9e07445af2b80fc93d`
- [x] [002-mount-require-auth.md](./phase-01-authenticate-routes/002-mount-require-auth.md) — tier `sonnet-med` · branch `plan/users-conformance-01-002` · commit `e21c59c` · merge `92fe351ff555741c6201ebb030671c9179d08ed4`

### Phase 02 — Close Existence Leaks

- [x] [001-user-account-masked-miss.md](./phase-02-existence-leaks/001-user-account-masked-miss.md) — tier `sonnet-med` · branch `plan/users-conformance-02-001` · commit `5804983` · merge `e47b9047ceef64a5e144a1aca570544e9d7c0d41`

### Phase 03 — Decode and Shape Before Authorization

- [x] [001-remove-pass-through-wrappers.md](./phase-03-decode-and-shape/001-remove-pass-through-wrappers.md) — tier `haiku-med` · branch `plan/users-conformance-03-001` · commit `48b8723` · merge `fd0d0dd5618f4838035bacfc138ce205dffac31e`
- [x] [002-decode-wrapper-and-callers.md](./phase-03-decode-and-shape/002-decode-wrapper-and-callers.md) — tier `sonnet-med` · branch `plan/users-conformance-03-002` · commit `071c510` · merge `324a392dd3745076c014650569c6cf3e9aa3e90d`
- [x] [003-user-accounts-and-self-shape.md](./phase-03-decode-and-shape/003-user-accounts-and-self-shape.md) — tier `sonnet-med` · branch `plan/users-conformance-03-003` · commit `698b7ff` · merge `6597d6c4f1811dbcd49576968e1f7329ff7c66b6`
- [x] [004-apps-membership-shape.md](./phase-03-decode-and-shape/004-apps-membership-shape.md) — tier `sonnet-med` · branch `plan/users-conformance-03-004` · commit `53d3a85` · merge `7a9c26d8d8bc1cb7e8e30e422d1aea9eba9deae4`
- [x] [005-self-service-shape.md](./phase-03-decode-and-shape/005-self-service-shape.md) — tier `sonnet-med` · branch `plan/users-conformance-03-005` · commit `5e5fe90` · merge `17255c76a9e6cc11604d521997f6d0795441829e`
- [x] [006-oidc-config-credential-shape.md](./phase-03-decode-and-shape/006-oidc-config-credential-shape.md) — tier `sonnet-high` · branch `plan/users-conformance-03-006` · commit `f56122a` · merge `ec2f72ca4bc9d6c0be5f3e8ad00823457f51cfd0`
- [x] [007-public-auth-error-literals.md](./phase-03-decode-and-shape/007-public-auth-error-literals.md) — tier `sonnet-med` · branch `plan/users-conformance-03-007` · commit `a244ae4` · merge `32972e752c0a40d940b6a570dea959c6591b2943`

### Phase 04 — State Conflicts and Order Tests

- [x] [001-state-conflict-audit.md](./phase-04-conflicts-and-order-tests/001-state-conflict-audit.md) — tier `sonnet-med` · branch `plan/users-conformance-04-001` · commit `cb5e2b2` · merge `cbad77ba7d2e`
- [x] [002-order-tests-account-routes.md](./phase-04-conflicts-and-order-tests/002-order-tests-account-routes.md) — tier `sonnet-med` · branch `plan/users-conformance-04-002` · commit `631b627` · merge `a1b638ef3f13`
- [x] [003-order-tests-self-service-routes.md](./phase-04-conflicts-and-order-tests/003-order-tests-self-service-routes.md) — tier `sonnet-med` · branch `plan/users-conformance-04-003` · commit `73f3b09` · merge `046389bdd2ab`
- [x] [004-order-tests-credential-and-public-routes.md](./phase-04-conflicts-and-order-tests/004-order-tests-credential-and-public-routes.md) — tier `sonnet-med` · branch `plan/users-conformance-04-004` · commit `cce6aed` · merge `ccd84409f4d5`

### Phase 05 — GUI Alignment, Wire Risk Docs and Validation

- [x] [001-gui-apps-client-alignment.md](./phase-05-gui-wire-docs-and-validation/001-gui-apps-client-alignment.md) — tier `haiku-med` · branch `plan/users-conformance-05-001` · commit `692ea96` · merge `de54236577bd`
- [x] [002-wire-risk-docs.md](./phase-05-gui-wire-docs-and-validation/002-wire-risk-docs.md) — tier `sonnet-low` · branch `plan/users-conformance-05-002` · commit `53dcacd` · merge `8eabc850f360`
- [x] [003-full-validation.md](./phase-05-gui-wire-docs-and-validation/003-full-validation.md) — tier `sonnet-low` · branch `plan/users-conformance-05-003` · commit `a3428e0` · merge `7480fbb16fe7`

### Phase 06 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-06-doc-updates/001-update-architecture-docs.md) — tier `sonnet-high` · branch `plan/users-conformance-06-001` · commit `a074520` · merge `c0bca9f7eb02`

### Phase 07 — Re-bump mod-core Pin


### Phase 08 — Remediation Round 1

- [x] [001-step-up-ordering-gaps.md](./phase-08-remediation-01/001-step-up-ordering-gaps.md) — tier `sonnet-med` · branch `plan/users-conformance-08-001` · commit `9a366ad` · merge `13e4dc4cc4bacbc622e53616bd1f697e91b8425b`
