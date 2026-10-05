# Fix Actor Group Target Confusion

## Purpose and scope

This plan fixes an authorization bypass in mod-users' grants-table `Authorizer` (`api/internal/authz/authz.go`, exported as `api/localAuthz`). This is the **mod-users slice of a federated four-project plan**: mod-users (phases 1-2, this project), mod-authz (phases 3-4), mod-core (phases 5-6), and mod-workflows (phases 7-8). The other slices have only soft dependencies on this one (see Federation hand-off). This slice edits nothing outside mod-users.

**Problem.** `Authorize(ctx, op, target *int64)` treats every non-nil target as an `entities.id` (in `checkGrantOrOwn`: grants through `TargetChain`, plus an `entities.owner_id` ownership arm). The ecosystem convention (`docs-mf-standards/architecture/authorization-design.md` call-shape table) has type-level `create` and `list` pass a **`types.id`** in that same parameter. So an actor who owns the entity whose id equals the type id, or holds any grant over it, passes the type-level check. This was reproduced in app-mfmanager (finding `ilu6`, app-mfmanager plan `managed-app-home-nav`): an ordinary user gets 403 then 201 on actor-group creation once they own entity 7. The same pattern exists inside mod-users itself: `UserAccountService.Create` authorizes `create` with the `natural_person` type id. Root cause and design: [type target design](./notes/type-target-design.md).

**What must change (mod-users only):**

- A failing (red) test in mod-users' own `internal/authz` tests that reproduces the confusion, both as a stubbed unit test and as a real-database integration test.
- A distinct, explicit type-level API, `(*Authorizer).AuthorizeType(ctx, operation, typeID int64)`, plus an exported `localAuthz.TypeAuthorizer` interface. It authorizes against type-scoped authority only: wildcard grants. It never consults entity ownership or entity-targeted grants. `Authorize` stays entity-only and source-compatible.
- Every mod-users call site that passes a type id migrates to the type-level API. The only one is `UserAccountService.Create`. It uses an assert-or-nil-fallback helper, so a decorated or foreign Authorizer still fails closed. The full audit is in [call site audit](./notes/call-site-audit.md).
- Regression tests at the authorizer level and the service level.
- Documentation of the new consumer contract, the fallback rule, and compat/pinning guidance: [consumer compat and pinning](./notes/consumer-compat-and-pinning.md).

**What must not change:**

- The `mod-core` `Authorizer` interface, `Authorize`'s signature, and `Authorize`'s behavior for entity targets and nil targets.
- `localAuthz.New`'s signature.
- Schema and migrations.
- Anything outside the mod-users repository. Other repos are handled by their own slices (mod-authz, mod-core, mod-workflows) or by followups (docs-mf-standards `rsiV`, mod-authz `qWTH`, the app pin bumps).

**Success criteria:**

- The repro tests fail against the unmodified code and pass after the fix.
- `AuthorizeType` denies a non-wildcard actor who owns the entity whose id equals the type id, and allows wildcard-`manage`/`create` holders.
- `UserAccountService.Create` denies that actor.
- All existing mod-users unit and integration tests pass.
- `go vet`/lint are clean.
- The exported `localAuthz` surface carries `TypeAuthorizer` with a compile-time assertion.

**Hard constraints:**

- The Docker host is **shared**. No task may run `make dev.start` (or `dev.restart`, `dev.stop`, or `clean.data`), or touch the shared `users-module-postgres` container. Integration tests run only against a throwaway Postgres 16+ container with a unique name and a random host port, removed afterwards.
- The authz tasks are security-sensitive (tier `sonnet-high` or above). The phase-01 gate carries a security review.

## Current status

Planning complete. Execution begins with phase 01, task 001. No pre-conditions beyond sibling checkouts of mod-core, mod-authz, and mod-audit next to mod-users (`make preflight` plants worktree symlinks via `scripts/link-siblings.sh`), Go 1.26, `goose` on `PATH`, and Docker for throwaway containers.

## Overview

### Phase 01 — Type-target authorization (security-reviewed)

Tasks are sequential. Each depends on the previous one.

1. `001-throwaway-postgres-integration-harness` (`sonnet-med`). Makes the `internal/authz` integration `TestMain` runnable against a throwaway Postgres: add a host-port override, skip the `users-module-postgres` container check when overrides are set, and document a throwaway-container recipe. Not security logic. It is test infrastructure that every later integration run depends on.
2. `002-authorize-type-api` (`sonnet-high`, security). Writes the red repro tests first (unit plus integration), confirms they fail on unmodified code, then implements `AuthorizeType` with a shared prelude, `localAuthz.TypeAuthorizer`, and corrected package and method docs.
3. `003-migrate-type-level-call-sites` (`sonnet-high`, security). Re-audits every `Authorize` call site and the exported surface, migrates `UserAccountService.Create` through an assert-or-nil-fallback helper, and adds service-level and integration regression tests.

### Phase 02 — Documentation updates

1. `001-update-architecture-docs` (`sonnet-high`). Updates `docs/architecture.md` (a new key decision covering the type-level vs entity-level authorization contract and the fallback rule) and `docs/mod-users-spec.md` (security requirements), as needed. It runs after phase 01 lands.

### Federation hand-off

The plan was widened from two projects to four:

| Project | Phases | Covers |
|---|---|---|
| mod-users | 1-2 | `AuthorizeType` and `localAuthz.TypeAuthorizer` (this slice) |
| mod-authz | 3-4 | `ActorGroupService` and `TargetGroupService` `create`/`list` (four call sites), structural assert-or-nil-fallback, no mod-users import |
| mod-core | 5-6 | Followup `ls1q`: four create call sites, plus `authz.TypeAuthorizer` and `authz.AuthorizeType` in core-api |
| mod-workflows | 7-8 | Followup `eiFh`: workflow definition and instance `create`/`list` call sites |

Followups `ls1q` (mod-core) and `eiFh` (mod-workflows) are **now in scope** of this plan, no longer out of scope. Still followups: docs-mf-standards `rsiV` (call-shape table), mod-authz `qWTH`, and the app pin bumps.

**Landing order.** All dependencies are soft: each slice closes the hole through the nil fallback (`Authorize(ctx, op, nil)`) even against today's mod-users. The preferred order is mod-users, then mod-core, then mod-authz, then mod-workflows, so production takes the explicit `AuthorizeType` path.

**mod-core's `TypeAuthorizer`.** mod-core adds `authz.TypeAuthorizer` and an `authz.AuthorizeType` helper in core-api. Its method signature is identical to mod-users' `localAuthz.TypeAuthorizer`/`AuthorizeType`, so `*Authorizer` satisfies it automatically. mod-users does **not** depend on it; `localAuthz.TypeAuthorizer` stays as designed. Optional follow-on once both land: make `localAuthz.TypeAuthorizer` an alias of core's. This is a followup-style note only, with no task in this plan.

**Pin bumps.** After everything lands, bump pins in the consumers: app-mfmanager (`zdj9`) and app-mftodo (`mJ3M`), plus mod-core, which is pinned by app-mfgit and mod-users. These are followups, outside this plan.
