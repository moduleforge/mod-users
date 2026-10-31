# Adopt The Profile Row Lock Query And Organization CreateInTx

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task removes two pieces of hand-written SQL that mod-users runs against mod-core's tables, now that mod-core provides generated or trusted replacements. It closes finding `T9ZL` and the mod-users side of `FAK8` and `0b75`. Finding `h0m8` needs no mod-users change: mod-core added `profile_policy_confirmations_party_id_idx`, and `dropPolicyConfirmations` stays as it is.

The two replacements:

- **`lockProfile`.** `api/internal/service/profile_service.go` runs its own `SELECT <profileColumns> FROM public.profiles WHERE entity_id = $1 FOR UPDATE` and scans into `coredb.Profile`. That couples mod-users to mod-core's column list at run time. mod-core now generates `GetProfileByEntityIDForUpdate` into a separate package, `core-model/dblock`, which keeps `coredb.Querier` unchanged. Use it as `dblock.New(tx).GetProfileByEntityIDForUpdate(ctx, id)`. It returns a `dblock.Profile` with the same columns.
- **Bootstrap `createOrganization`.** `api/internal/service/bootstrap_service.go` composes `coreservice.CreateEntity(WithoutOwner)` with the raw `CreateLegalEntity` and `CreateOrganization` queries. That skips mod-core's normalization, its `tax_id` encryption path and its `create organization` observation. mod-core now offers the trusted `OrganizationService.CreateInTx(ctx, tx, in, opts...)`, which returns `(coredb.Organization, uuid.UUID, int64, error)`. It runs no authorization, takes the owner from the options and calls the in-transaction observer. The caller calls `ObserveAfterCommit` after commit.

Roles: read `developer-go.md` and `developer.md` (Go developer role) before starting. No standard skill covers this task.

Scope:

- `api/internal/service/profile_service.go`
- `api/internal/service/bootstrap_service.go`
- the constructors and wiring that give the bootstrap an organization service: `api/usersservice/service.go` (`NewBootstrapService`), `api/cmd/server/main.go` and `api/internal/integration/harness_integration_test.go`
- tests

The task is independent of tasks 001 and 002 and may run in parallel with them, but it merges before task 004.

## Requirements

1. **Prerequisite check.** In the mod-core sibling, confirm that:
   - `model/dblock/profiles.sql.go` defines `GetProfileByEntityIDForUpdate`
   - `api/service/organization.go` defines `(*OrganizationService).CreateInTx` with the signature above
   - `api/service` exports `WithoutOwner`

   If anything is missing, stop and report `blocked`.
2. **`lockProfile` on `dblock`.** Replace `lockProfileSQL` and its scan with `dblock.New(tx).GetProfileByEntityIDForUpdate`. Convert the result to `coredb.Profile` (field by field, or by a conversion if the structs are identical) so callers stay unchanged. A missing row still maps to `apiresp.ErrNotFound`.
   - Keep `profileColumns` and `scanProfile` only while `lockOpenProfileSQL` or another hand-written query still uses them. Do not invent a mod-core change for those. Say in the report which hand-written `public.profiles` reads remain, and why.
   - The `core-model` module must resolve `dblock` through the existing sibling replace. Do not add a new `replace`.
3. **Bootstrap organization through `CreateInTx`.** Replace the body of `createOrganization` with a call to the mod-core `OrganizationService.CreateInTx(ctx, tx, coreservice.CreateOrganizationInput{...}, coreservice.WithoutOwner())`. The operator organization keeps owner NULL (decision U10). Use the same acting context as today: `personalCtx`, the bootstrap user's personal profile, which mod-audit needs as the actor.
   - **Wiring.** `BootstrapService` gains a dependency on mod-core's organization service. Add a parameter to the internal `NewBootstrapService` and to the `usersservice.NewBootstrapService` facade, and update `api/cmd/server/main.go` and the integration harness. No sibling calls `NewBootstrapService` today; confirm that with a read-only grep of the sibling plan worktrees and report it, because the facade signature changes. The narrowest type that offers `CreateInTx` is enough. Keep `CreateInTx` away from any handler-facing path (finding `ujE9`): only the bootstrap calls it.
   - **After-commit observation.** `CreateInTx` calls the in-transaction observer. The caller must call `ObserveAfterCommit` (`create`, `organization`, the returned entity id) after `tx.Commit` succeeds. Carry the entity id out of `bootstrapInTx` and dispatch the observation after the commit in the bootstrap's public method, through the same observer group mod-users uses elsewhere. Check how mod-users handles `NaturalPersonService.CreateInTx`'s after-commit observation today. If mod-users never dispatches it, follow the same choice for consistency, but file a finding for the missing after-commit observations, naming both call sites.
   - **Behavior kept.** The bootstrap's HTTP contract, its error order and its `create bootstrap` observation stay unchanged. `CreateInTx` validates `legal_name` and `legal_form`. The bootstrap validates its input earlier, so keep the bootstrap's own validation errors first. A `CreateInTx` validation error that can still occur must map to the same `400` the bootstrap returned before. The new in-transaction `create organization` audit row is expected (mod-audit op vocabulary U21: `create`). Update tests that count audit rows for the organization-operator bootstrap and say so in the report.
4. **Fakes.** mod-core widened the exported `OrganizationServicer` interface with `CreateInTx` (finding `U6SE`). Grep mod-users' tests for fakes that implement `OrganizationServicer` and extend them if any exist. If none exist, say so.
5. **Comments.** This task replaces implementations and should add no new authorization branch. Update the doc comments of `lockProfile` and `createOrganization` to describe the new implementation, and remove the comment that says mod-core's `Create` cannot be used. If you add a branch (for example a conversion or error mapping), comment at the branch why it exists and what it implies (user requirement).
6. **Tests.** The existing bootstrap integration tests (`api/internal/integration/bootstrap_integration_test.go`) pass for both operator kinds. Add an assertion for the organization operator: the organization row has owner NULL, `legal_name`, `legal_form` and `jurisdiction` are stored as normalized, and the in-transaction `create organization` audit row exists when mod-audit is linked. The profile lifecycle tests that go through `lockProfile` (end, suspend, reactivate, the last-admin guard) pass unchanged.
7. **Working rules.**
   - Never run `scripts/link-siblings.sh` and never run a bare root `make preflight` in a plan or task worktree.
   - Every build and `make` invocation sets `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users`. Afterwards, verify with `ls -l /Users/zane/playground/moduleforge/mod-users/worktrees/plan` that `mod-core`, `mod-authz` and `mod-audit` resolve to the siblings' `worktrees/plan/identity-model-impl`.
   - Run `make -C model compose`, then the unit suites and the `authz`, `config` and `integration` suites against a throwaway `postgres:16` container with superuser `users` that you start and remove yourself (`AGENTS.md` recipe). Remove the container even when tests fail.
   - Never edit any sibling.
8. **Findings.** File out-of-scope issues with `followups_add` (`plan_slug: identity-model-impl`, `phase_slug: adopt-core-engine-remediation`, a `type:` tag). Never edit any `followups.yaml`, never call `followups_patch`, never remove a followup.
9. **Report.** Return the structured report the Go developer role defines. Include the facade signature change, the remaining hand-written `public.profiles` reads, and the audit-count test changes.

## Validation

1. With `MODULEFORGE_SIBLINGS_DIR` set: `make build`, `make test` and `make lint` pass, and `make -C model verify` passes.
2. The `authz`, `config` and `integration` suites pass against the throwaway `postgres:16`, and no container of yours is left running.
3. `grep -n 'lockProfileSQL\|FROM public.profiles WHERE entity_id = \$1 FOR UPDATE' api/internal/service/profile_service.go` returns nothing. `grep -n 'GetProfileByEntityIDForUpdate' api/internal/service/profile_service.go` shows the call.
4. `grep -n 'CreateLegalEntity\|CreateOrganization(' api/internal/service/bootstrap_service.go` returns nothing. `grep -n 'CreateInTx' api/internal/service/bootstrap_service.go` shows the call with `WithoutOwner()`.
5. The new bootstrap assertions (requirement 6) pass and fail when the `CreateInTx` call is replaced by the old raw inserts. Check this temporarily, then restore the code.
6. `ls -l /Users/zane/playground/moduleforge/mod-users/worktrees/plan` shows the three sibling links resolving to the siblings' `worktrees/plan/identity-model-impl`. No sibling worktree has changes from you.

## Assumptions

- mod-core's remediation phase 11 task 007 is merged on mod-core's plan branch. The organization service is constructible from mod-users' composition root with the dependencies mod-core's own wiring uses: pool, authorizer, observer group, rotating cipher and type resolver. If the bootstrap cannot get a `RotatingCipher` without new configuration, stop and report it rather than adding configuration silently.

## References

- mod-core plan worktree (read only): `plan/phase-11-identity-engine-proof-remediation/007-profile-lock-org-create-in-tx-policy-index.md` (Status), `model/dblock/profiles.sql.go`, `api/service/organization.go` (`CreateInTx`, `OrganizationServicer`), `api/service/natural_person.go` (`CreateInTx`, the same after-commit rule), `AGENTS.md` (`service/` and `model/` rows).
- Finding `T9ZL` (mod-core project followups): the adoption recipe.
- Findings `FAK8`, `0b75` and `h0m8` (mod-users project followups): the original requests.
- Findings `U6SE` and `ujE9` (mod-core project followups, informational): the widened interface, and keeping `CreateInTx` off handler paths.
- [Decisions](../notes/decisions-and-resolutions.md): U10 (bootstrap contents) and U21 (audit op vocabulary).
- [Phase summary](../phases/adopt-core-engine-remediation.md).

## Checkpoint hints

- After `lockProfile` moves to `dblock` and the profile tests pass.
- After the bootstrap uses `CreateInTx`, with the wiring and the after-commit observation.

## Status

- Outcome: succeeded (2026-10-30).
- Validation: `make build`, `make test`, `make lint`, `make -C model verify` pass; `authz`, `config` and `integration` suites pass against a throwaway `postgres:16` (removed afterwards); the new bootstrap assertions fail with the raw inserts restored and pass with `CreateInTx`.
- Changed: `api/internal/service/profile_service.go` (`lockProfile` on `dblock`), `api/internal/service/bootstrap_service.go` (`createOrganization` via `CreateInTx`, new `orgs` dependency), `api/usersservice/service.go` (facade gains `orgs`, plus `OrganizationFromServices`), `api/cmd/server/main.go`, `moduleforge.module.yaml`, and the integration tests.
- Remaining hand-written `public.profiles` reads on `profileColumns`/`scanProfile`: only `lockOpenProfileSQL` (`profile_admit.go`, lookup by holder and party, no mod-core generated counterpart).
- After-commit observation: mod-users never dispatches it for `NaturalPersonService.CreateInTx`, so the bootstrap follows the same choice; filed as a finding.
