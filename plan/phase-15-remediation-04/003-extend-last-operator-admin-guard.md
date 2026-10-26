# Extend The Last Operator Admin Guard To Suspension And EndInTx

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `Kfjo` (security, priority medium), `kPpy` (bug, priority low), `Ts9r` (optimization, priority low) and `Xqat` (documentation, priority low). The last-operator-admin guard currently covers only ending through the holder leave and the admin ending paths; suspension and the exported `EndInTx` primitive are not guarded, and the guard's approximations are undocumented. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop. Task 007 builds on the changes made here, so this task runs before task 007.

## Requirements

1. `requireNotLastOperatorAdmin` in `api/internal/service/profile_last_admin.go` also guards every transition that takes an active operator administrator out of `active`, including suspension (`UpdateProfileStatus` to `suspended`), not only ending.
2. The exported `EndInTx` primitive in `api/internal/service/profile_service.go` applies the same guard, either by calling it or by moving the guard into the shared ending path (`endLocked`), so in-process callers inherit it.
3. Every guarded transition takes the `platform_settings` row lock (`lockPlatformSettingsForEndSQL`) before the remaining-admin count is read, so a concurrent ending and suspension of the two last administrators serialize and cannot both commit.
4. The admin check and the other-admin count run as a single query (a count with `FILTER`) rather than an admin-check query followed by a second query repeating the same join. The lock still precedes the count.
5. The doc comment of `requireNotLastOperatorAdmin` and the matching `AGENTS.md` row (or `docs/architecture.md`) state that the guard reads the mod-authz-owned `public.grants` and `public.authz_operations` tables and `platform_settings` with raw SQL; that it hardcodes the shape of the Bootstrap grant (direct entity-scope wildcard `manage`, target NULL); that it deliberately ignores a manage grant inherited through an actor group; and that this approximation is an explicit, recorded decision.

## Validation

1. An integration test: suspending the last active operator administrator is refused with 409 `users.last_operator_admin`.
2. An integration test: ending the last active operator administrator through `EndInTx` is refused with the same `users.last_operator_admin` error.
3. An integration test: suspending one of two operator administrators succeeds.
4. `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `Kfjo` in this plan's `plan/findings.yaml` (type:security, priority:medium): the guard is not called on suspension, and suspension does not take the `platform_settings` lock, so an ending and a suspension of the last two admins can both commit.
- Finding `kPpy` in this plan's `plan/findings.yaml` (type:bug, priority:low): the exported `EndInTx` in `api/internal/service/profile_service.go` does not call the guard, so it can end the last operator administrator.
- Finding `Ts9r` in this plan's `plan/findings.yaml` (type:optimization, priority:low): `requireNotLastOperatorAdmin` runs two queries with the same large SQL text on an admin ending; the recommendation is one query with `FILTER`.
- Finding `Xqat` in this plan's `plan/findings.yaml` (type:documentation, priority:low): the guard reads mod-authz-owned tables with raw SQL and hardcodes the Bootstrap grant shape, and the approximation is not recorded as a decision.
