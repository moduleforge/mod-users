# Reconcile Superseded Mod-Users Followups

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Verify, against the merged code on the plan branch, which mod-users followups this plan superseded,
and hand the manager the exact `followups_remove` calls for the confirmed ones. This is a
read-and-report task: it changes no code, no docs and no followups. No standard skill covers it.
Follow the [Procedure](#procedure).

The manager applies the calls under the Manager Operating Protocol's Follow-up resolution rules
(merge-confirmed removal; a `pending-merge` response is expected until the plan branch merges).

This task runs after task 001 (`mod-users-drift-scan`).

## Requirements

1. Read every item listed in [the followup table](#followup-table) from mod-users' project store
   (`mcp__flow-mcp__followups_list` with `project_root: /Users/zane/playground/moduleforge/mod-users`
   and `ids` set to the table's ids). An id that is no longer present is reported as `already
   removed` and skipped.
2. For each present item, run the table's check against this plan worktree's merged code
   (`/Users/zane/playground/moduleforge/mod-users/worktrees/plan/identity-model-impl`), reading the
   item's own text to confirm the check covers what it describes. Classify it:
   - **confirmed** — the check passes and the item's concern no longer exists in the code;
   - **not confirmed** — the check fails, or the concern survives in a new form (for example the
     same weakness on the renamed component). Default to retain: an uncertain item is not
     confirmed.
3. For each confirmed item, find the mod-users ref whose merged commits resolve it: the task
   document of this plan that made the change, as its `plan/`-prefixed path (for example
   `plan/phase-06-users-identity-core/003-<slug>.md`). Find it in `plan/TODO.yaml` (a `done` task
   with a non-empty `git.commit` list) and confirm with `git log --oneline -- <file the check
   reads>` that one of that task's commits touched the relevant file. When no single task document
   can be identified, use the plan branch `plan/identity-model-impl` and say why.
4. Do not call `followups_remove`, `followups_patch`, `followups_add` or any other followups
   mutation. For each confirmed item, state the exact intended call in the structured report under
   `requests_for_manager`, for the manager to run, in this form:

   ```text
   mcp__flow-mcp__followups_remove({
     project_root: "/Users/zane/playground/moduleforge/mod-users",
     id: "<id>",
     resolved_by: "<plan/-prefixed task document path, or plan/identity-model-impl>",
     reason: "<one line, at most 500 characters: what superseded it, with file evidence>"
   })
   ```

   Pass no `scope` (these are project-store items).
5. `UbXY` and `OlHx` are removed only if mod-authz's work closed them (table rows below). If the
   closing change lives only in mod-authz, no mod-users ref can satisfy `resolved_by`; in that case
   state the call without `resolved_by` (a reasoned removal naming the mod-authz task document and
   commit in `reason`) and mark it **manager decision: cross-project resolution**. If mod-authz did
   not close it, report it as not confirmed.
6. For each not-confirmed item, report in `requests_for_manager` what survives (file:line) and,
   where the item's text no longer matches the code, the exact proposed `followups_patch` text
   update for the manager to apply.
7. Report a table with one row per id: id, title, classification (`confirmed`, `not confirmed`,
   `already removed`, `manager decision`), evidence (file:line or command output), and
   `resolved_by`. Add a one-paragraph summary for mod-core's coordinating scan.

### Followup table

Paths are relative to the mod-users repository root. "Absent" means the `rg` command returns no
match outside `plan/`, `docs/consumer-upgrade-notes.md` and tests asserting removal.

| Id | Superseded by | Expected phase | Check |
|---|---|---|---|
| `VJg2` | `AssumedUser`/sudo branch removed from the resolver | 6 | `rg -n -e 'AssumedUser' -e 'SudoUserUUID' api` absent; `api/internal/auth/resolver_test.go` has no sudo stub |
| `1mnX` | assume sessions removed; impersonation is a separate session with fresh `auth_time` | 6, 8 | `rg -n -i -e 'isSelfTargetingOperatorCall' -e '/v1/assume' -e 'sudo' api` absent; `ImpersonationService` exists |
| `7kHY` | `PUT /v1/self` and assume sessions removed; `PUT /v1/me` acts on the session principal | 6 | `rg -n '/v1/self' api` absent; `/v1/me` handler targets `session.principal` |
| `sxjM` | first-user hook deleted (U17); setup-token bootstrap | 6, 7 | `rg -n -i -e 'firstUser' -e 'FirstUserHook' api moduleforge.module.yaml` absent; `BootstrapService` exists |
| `4lm1` | `entities_no_system_actor_owner` trigger deleted | 6 | `rg -n -e 'entities_no_system_actor_owner' -e 'entities_check_no_system_actor_owner' model api` absent |
| `cJcZ` | SSH keys became `ssh_key` credentials under the `credentials` operation | 6, 8 | `api/internal/service/ssh_keys.go` gone; the operator path for SSH-key credentials states its rule for guest or unverified principals in code or `docs/architecture/ssh-keys.md`; if silent, not confirmed |
| `tbmN` | `/v1/user-accounts/{uuid}/ssh-keys` and sibling routes removed | 6 | `rg -n 'user-accounts' api` absent |
| `o1uH` | `SSHKeyService.Revoke` and `ArchiveSSHPublicKey` removed | 6 | `rg -n -e 'ArchiveSSHPublicKey' -e 'SSHKeyService' api model/queries` absent |
| `433h` | `loadAuthorizedAccount` removed | 6 | `rg -n 'loadAuthorizedAccount' api` absent |
| `Rjag` | stale "never step-up-gated" comments removed with the routes | 6 | `rg -n -i 'step-up-gated' api moduleforge.module.yaml` absent |
| `Jtw6` | `SSHKeyResolver` reworked (`ResolveCredential`, `AuthContextForOwner`) | 6, 7 | the constructor takes a narrower read-only interface. The API surface keeps `NewSSHKeyResolver(q usersdb.Querier, coreQ coredb.Querier)`, so this is likely **not confirmed**; confirm only if the merged constructor is narrower |
| `pciu` | HS256 removed (U4) | 6 | `rg -n -e 'NewHS256Signer' -e 'WithHS256Secret' -e 'HS256' api` absent (besides removal tests) |
| `ZJ5N` | `/v1/apps/{uuid}/user-accounts` and `apps_user_accounts` removed (U16) | 6 | `rg -n -e 'ListAppUsers' -e 'apps_user_accounts' -e 'ListAppUserAccounts' api model gui/src` absent |
| `PRvD` | GUI apps client aligned with mod-core or removed | 9 | `gui/src/lib/api.ts` apps create/update body carries `slug` and no `description`, or the apps client is gone |
| `fs0H` | `api/openapi.yaml` rewritten for the new routes | 6, 10 | `rg -n -e '/v1/users\b' -e '/v1/user-accounts' -e '/assume' api/openapi.yaml` absent; `/v1/principals` present |
| `biJk` | `api/openapi.yaml` documents the new self-service surface | 6, 10 | `api/openapi.yaml` documents `/v1/me/credentials`, `/v1/me/identifiers` and `/v1/session/step-up/*`, with 409 `users.step_up_required` via the Action schema |
| `bHKv` | `DirectoryService.PrincipalSummaries` / `ProfileSummaries` batch lookups | 6 | both methods exist in `api/usersservice` and take id slices |
| `HPJi` | GUI 401 handling without a hard-coded navigation | 9 | `rg -n "'/auth/login'" gui/src/lib` shows no hard navigation in the fetch helper |
| `WZTj` | mod-users' own system-actor seed deleted; system actors come from mod-core's `core_ensure_system_actor` | 6 | mod-users' migrations create no `system_actors` table and seed no `system_actor` type or `anonymous` row (`rg -n -i -e 'create table[^;]*system_actors' -e "'system_actor'" -e "'anonymous'" model/migrations` absent), `rg -n 'GetSystemActorBySlug' model/queries` absent, and the anonymous actor uses mod-core's system actor. `resolved_by` is the mod-users task that deleted the seed |
| `UbXY` | mod-authz's stand-in rewritten on `AuthContext` (no sudo, unauthenticated and unknown-op parity) | mod-authz | in `/Users/zane/playground/moduleforge/mod-authz/worktrees/plan/identity-model-impl`, `api/service/type_level_authz_integration_test.go` is gone or no longer uses `opctx.ActorEntityID`, and matches mod-users' engine on all three behaviors. Requirement 5 applies |
| `OlHx` | mod-authz's actor trigger rejects the anonymous system actor's profile as a grant actor or group member (API surface section 9 item 4) | mod-authz | the trigger exists in mod-authz's plan worktree migrations with a test. Requirement 5 applies |

## Validation

- The report has one row for each of the 21 ids in the table.
- Every confirmed row carries file evidence and a `resolved_by` that is either a `plan/`-prefixed
  task-document path present in `plan/TODO.yaml` with a non-empty `git.commit` list, or
  `plan/identity-model-impl` with a stated reason.
- Every stated call names `project_root`, `id` and `reason`; every call except the requirement-5
  cross-project ones names `resolved_by`.
- `git status` in the plan worktree and in mod-users' main checkout shows no change made by this
  task, and no followups tool other than `followups_list` was called.

## Assumptions

- Phases 6 to 10 and task 001 of this phase are merged into the plan branch, and mod-authz's
  `identity-model-impl` phases that cover API surface section 9 are merged into its plan branch.
- `followups_list` is available to this task; mutation tools are not.

## References

- [Work packages](../notes/work-packages.md), the `users-identity-final` section: the superseded
  followup list.
- [The mod-users API surface](../notes/mod-users-api-surface.md): sections 6.3 (`DirectoryService`,
  `SSHKeyResolver`), 7 (removed routes), 9 item 4 (`OlHx`), 10 (`WZTj`).
- [Decisions and resolutions](../notes/decisions-and-resolutions.md): U4, U16, U17 and boundary
  mismatch 3 (`OlHx`).
- Manager Operating Protocol, "Follow-up resolution": how the manager applies the calls.

## Procedure

1. List the 21 items and note any already removed.
2. Run each check in the table, reading the item text alongside it.
3. Map each confirmed item to its resolving task document through `plan/TODO.yaml` and `git log`.
4. Check `UbXY` and `OlHx` in mod-authz's plan worktree.
5. Assemble the table, the stated calls and the summary.
