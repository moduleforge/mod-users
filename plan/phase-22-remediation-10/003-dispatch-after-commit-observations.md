# Dispatch After-Commit Observations For CreateInTx Creations

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task makes the in-transaction creation paths hand their created entity id to the code that owns the commit, and makes each committing caller dispatch the after-commit observation only once `tx.Commit` has succeeded. It closes findings `DDfb`, `bwMq` and `06rd`, and it resolves the manifest regeneration drift tracked by finding `7sEV`.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Roles: read `developer-go.md` and `developer.md` (Go developer role) before starting.

Environment and working rules:

- Set `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users`. Do not assume `/Users/zane/playground/../..` or any other path for it.
- Never run `link-siblings.sh` and never run a bare `make preflight`.
- Integration tests run against a throwaway `postgres:16` container that you start and remove yourself. Remove it even when tests fail.
- Do not edit sibling projects. Before coding, confirm the exact `ObserveAfterCommit` API in the mod-core sibling (resolved through `MODULEFORGE_SIBLINGS_DIR`). If its contract cannot be met without a sibling edit, stop and report `blocked`.
- Decisions D through M in `plan/notes/decisions-and-resolutions.md` still apply.

## Requirements

1. The in-transaction creation paths that call mod-core's `OrganizationService.CreateInTx` or `NaturalPersonService.CreateInTx` carry the created entity id, and any payload the observer needs, out to the code that owns the commit. For example, add them to `CreatedUser` and to the bootstrap result.
2. Every caller that owns the commit calls `ObserveAfterCommit` through the module's observer group only after `tx.Commit` succeeds. It passes create, the entity kind (`organization` or `natural_person`) and the entity id, and it uses the same acting context the in-transaction observation used. Nothing is dispatched on rollback. The callers are:
   - `Bootstrap` in `api/internal/service/bootstrap_service.go`
   - `Register` in `api/internal/handlers/auth/register.go`
   - `createBYOUser` in `api/internal/handlers/auth/oidc_flows.go`
   - the upgrade path in `api/internal/service/user_self_service.go`
   - the callers of `CreateBYOUserInTx` in `api/internal/service/user_service.go`
3. Remove the NOTE comment in `api/internal/service/bootstrap_service.go` (around lines 424-429). Document on the public `*InTx` primitives that their committing caller must dispatch the after-commit observation.
4. Correct `plan/notes/mod-users-api-surface.md` around lines 741-743 only if they are still inaccurate after the change.
5. Manifest regeneration: phase 21 changed the manifest (`bootstrapOrganizationService` and `coreServices` in `requires`), and mfgen was never run. Run the project's documented mfgen or regeneration step as AGENTS.md describes, and commit the regenerated output.

## Validation

1. Tests with a recording post-commit observer:
   - bootstrap emits exactly one after-commit create for `organization`
   - registration and the self-service upgrade each emit exactly one after-commit create for `natural_person`
   - a forced rollback emits none
2. `grep -rn 'ObserveAfterCommit' api --include=*.go` (non-test files) shows a dispatch at each committing caller listed in requirement 2.
3. After regeneration, `make build` passes and no manifest drift remains (finding `7sEV`).
4. `make build`, `make test`, `make lint` and the integration suites pass with `MODULEFORGE_SIBLINGS_DIR` set. The throwaway `postgres:16` container is removed afterwards.

## References

- Finding `DDfb` (plan findings, `plan/findings.yaml`): in-transaction creations are not observed after commit.
- Finding `bwMq` (plan findings, `plan/findings.yaml`): committing callers do not dispatch the after-commit observation.
- Finding `06rd` (plan findings, `plan/findings.yaml`): the after-commit dispatch contract is undocumented on the `*InTx` primitives.
- Finding `7sEV` (plan findings, `plan/findings.yaml`): manifest regeneration drift after phase 21.
- [Decisions and resolutions](../notes/decisions-and-resolutions.md): Decisions D through M.
