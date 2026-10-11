# Overview

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
