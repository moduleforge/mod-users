# Rewrite Authorization Engine

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Rewrite the production `Authorizer` in `api/internal/authz/authz.go` to the ADR's authorization engine, as fixed by [API surface section 6.2](../notes/mod-users-api-surface.md#62-localauthz-binding). Phase 6 left a minimal port: the actor is `opctx.ProfileID` and the own arm goes through `profile_own_subject` without `ownership_confers`. This task adds the full engine:

- the operation-level assurance gate
- scope-aware wildcard and entity arms
- the owned-scope arm with `include_subtypes`
- the per-operation own arm through `authz_operations.ownership_confers`
- `AuthorizeTypeIn`

Suggested roles: `plugins/flow/roles/developer-go.md` (primary) and `plugins/flow/roles/developer-sql.md` (the arm predicates). No standard skill covers this task.

Scope: `api/internal/authz/authz.go`, `api/localAuthz/authz.go` and `api/internal/authz/export_test.go`, plus the minimum test edits needed to keep existing suites compiling. The full engine test matrix belongs to task 002 (`authorization-engine-tests`). Impersonation, the `credentials` routes, the app gate and the personal party are tasks 003 to 005.

This task runs first in the phase. Tasks 002 to 005 depend on it and can run in parallel with each other once it lands.

## Requirements

1. **Prerequisite check.** Before you change any code, confirm that mod-authz's schema phase has merged and that `scripts/link-siblings.sh` points the mod-authz and mod-core links at their plan worktrees. That phase is [API surface section 9](../notes/mod-users-api-surface.md#9-required-from-mod-authz-to-be-planned-by-mod-authz-names-fixed-here-or-by-mod-core), items 1 to 4. Confirm these are present:
   - the `authz_operations.ownership_confers`, `min_acr` and `max_auth_age` columns
   - the `impersonate` and `credentials` operations
   - the `grants.scope`, `target_type_id` and `include_subtypes` columns
   - `OperationRegistry.Policy(slug) (OperationPolicy, error)`
   - the extended `authzdb.CreateGrantParams`

   If any are missing, stop and report `blocked` with the missing names.
2. **Actor.** The actor is `opctx.Auth(ctx).ProfileID`. With no profile, return `ErrUnauthenticated`.
3. **Assurance gate (U14).** When `SessionID != 0 || CredentialID != 0`, look up the operation's `OperationPolicy` through `OperationRegistry.Policy` and require both of these:
   - `ACR >= MinACR`
   - `now - AuthTime <= MaxAuthAge`, when `MaxAuthAge` is non-zero

   A failure returns the action `users.step_up_required` as the shared `apiresp` action-required error added in mod-core phase 1 (followup `Uure`) (renders 409 through `apiresp.WriteError`), constructed or wrapped in the engine; the engine defines no mod-users-local rendering. Because `WriteError` renders it uniformly, the refusal also reaches clients correctly when raised inside another module's service. System-actor contexts and the anonymous context skip the gate. The gate runs before any grant arm. Keep the existing prelude behavior for unknown slugs.
4. **Wildcard arm.** The actor chain (the profile and its actor groups) holds a grant with `scope = 'entity' AND target_id IS NULL`.
5. **Entity and type-entity arm.** Instance semantics stay as they are: a grant on the type entity confers authority over its instances. This arm only considers grants with `scope = 'entity'`.
6. **Owned-scope arm.** Matches a grant held by the actor chain with:
   - `scope = 'owned'`
   - `target_id = target.owner_id`
   - a type match: `target_type_id IS NULL`, `target_type_id = target type`, or `include_subtypes` and the target type descends from `target_type_id`

   It never reaches the owner's own record: the target is the owned entity, never the owner (mod-core D7).
7. **Own arm (followup `rHzV`, mod-users' half).** The arm passes when both of these hold:
   - `target.owner_id = public.profile_own_subject(actor)`
   - `EXISTS (SELECT 1 FROM public.authz_operations o WHERE o.id = ANY(op_ids) AND o.ownership_confers)`

   This is the same predicate as mod-core's generated `accessible_*` functions, so the single-row and list paths agree by construction. `profile_own_subject` yields a subject only for a user's personal profile. `impersonate`, `manage` and `login` never confer through this arm, because `ownership_confers` is false for them.
8. **`AuthorizeTypeIn(ctx, op, typeID, ownerID)`.** Order:
   1. the prelude, including the assurance gate
   2. the wildcard arm
   3. a type-entity grant on the exact type
   4. an owned-scope grant with `target_id = ownerID` and the type match
   5. the own arm when `ownerID = profile_own_subject(actor)` and the operation confers

   Keep `Authorize` and `AuthorizeType` signatures unchanged. `AuthorizeType` keeps its existing type-level semantics, and an instance grant never satisfies it.
9. **Facade.** `api/localAuthz/authz.go` keeps `New(authzQ, opReg, pool)` unchanged. Add a compile-time assertion that `*Authorizer` implements `coreAuthz.OwnerScopedTypeAuthorizer`, alongside the existing `Authorizer` and `TypeAuthorizer` assertions. Rewrite the package documentation in both files:
   - Remove all `assume`, sudo and "account holder" wording.
   - Describe the arms, the assurance gate and the operator-actor trust warning for type-entity grants in the new vocabulary (profiles, `impersonate`, `credentials`).
10. **Test stubs.** Add an owned-scope stub to `api/internal/authz/export_test.go`, alongside the existing wildcard, grant-or-own and type-grant stubs ([API surface section 6.6](../notes/mod-users-api-surface.md#66-test-helpers)). Remove stubs whose arms no longer exist.
11. **Keep green.** Existing tests that encode removed semantics must not be left failing. This includes the own arm without `ownership_confers` and the unscoped grant reads. Either adjust the assertion to the new semantics, or mark the case with a `t.Skip` naming task 002. Do not leave `api/` uncompilable.
12. **Binding names.** Do not rename anything marked binding in API surface section 6.2. If a change is unavoidable, record it in the report and flag it.

## Validation

- `make build` succeeds.
- `make test` passes, including `go test ./internal/authz/...` unit tests.
- `make lint` passes.
- `cd model && make verify` passes.
- The authz integration suite passes against a throwaway Postgres per `AGENTS.md`, "Authz integration tests against a throwaway Postgres" (`go test -tags=integration -p 1 -count=1 ./internal/authz/...`), with mod-core and mod-authz migrations composed from the plan worktrees.
- `grep -rniE 'assume|sudo|account holder' api/internal/authz/authz.go api/localAuthz/authz.go` returns no package-documentation hits.
- `grep -n 'OwnerScopedTypeAuthorizer' api/localAuthz/authz.go` shows the compile-time assertion.
- In a manual read of the own-arm SQL, the predicate matches API surface 6.2 item 6 exactly and references `ownership_confers`.

## Metadata

architectural_impact: true

## Assumptions

- Phases 6 and 7 are complete. Phase 6 added the minimal engine port, `opctx.AuthContext` population (`SessionID`, `CredentialID`, `ACR`, `AuthTime`) and the `users.step_up_required` action, built on the shared mod-core action-required error.
- mod-core's `coreAuthz.OwnerScopedTypeAuthorizer` interface and the `authz.AuthorizeTypeIn` helper exist. Until this task lands, the helper falls back to `AuthorizeType` (U20).
- Integration tests compose mod-core, mod-authz and mod-users migrations (`make -C model compose`).

## References

- [Phase summary](../phases/users-authorization-engine.md): goals and outputs of this phase.
- [API surface section 6.2](../notes/mod-users-api-surface.md#62-localauthz-binding): the engine semantics, which are binding.
- [API surface section 9](../notes/mod-users-api-surface.md#9-required-from-mod-authz-to-be-planned-by-mod-authz-names-fixed-here-or-by-mod-core): the mod-authz prerequisites.
- [Decisions](../notes/decisions-and-resolutions.md#planning-decisions):
  - U5: liveness lives in the middleware; the `Authorizer` enforces only the operation gate.
  - U14: assurance gate scope.
  - U20: `AuthorizeTypeIn` fallback.
- [Work packages](../notes/work-packages.md#phase-users-authorization-engine): package 1.
- mod-core ADR `docs/architecture/identity-model.md`, section "Authorization engine".

## Checkpoint hints

- After the engine and its arms compile in `api/internal/authz/authz.go`.
- After the `api/localAuthz/authz.go` assertion and documentation rewrite.
- After `export_test.go` stubs and existing-test adjustments are green.
