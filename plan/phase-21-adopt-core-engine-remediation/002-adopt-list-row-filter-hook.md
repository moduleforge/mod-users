# Adopt The List-Side Row Filter Hook For The Owning-Party Gate

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task restores list/single-row parity for the owning-party gate. It closes the mod-users side of findings `Ddx9` and `8hQB`.

mod-users' single-row engine refuses an acting profile outside a target's owning party whose session or credential does not meet that party's `credential_rule` or `min_acr`. The path is `authorizePrelude` -> `checkPartyPolicy` -> `mod_users.session_meets_party_policy`. The list functions `accessible_<slug>_ids_for_actor` have no such gate, so a below-policy collaborator still sees the party's rows in lists, for example `GET /v1/principals` through `accessible_user_ids_for_actor`.

mod-core's remediation phase 11 (tasks 005 and 006) shipped a module-agnostic hook:

- `setup.NewRowFilterGenerator(inner, "schema.function")` wraps any generator's body and filters every arm once through a module predicate.
- The predicate signature has six arguments:

  ```
  (p_actor_entity_id BIGINT, p_entity_id BIGINT, p_owner_id BIGINT,
   p_session_id BIGINT, p_credential_id BIGINT, p_system_actor BOOLEAN) RETURNS BOOLEAN
  ```

  NULL counts as false.
- `setup.BindAuthInputs(ctx, tx)` binds the request's `SessionID`, `CredentialID` and `opctx.IsSystemActor(ctx)` as transaction-local settings. When bound, an absent id is passed as 0 and a non-system actor as false. When unbound, all three are NULL.
- `setup/paritytest.Compare` checks a module's list functions against its `Authorizer`.

mod-users adopts all of it.

Roles: read `developer-go.md` and `developer.md` (Go developer role) before starting. Use `developer-sql.md` for the predicate. No standard skill covers this task.

Scope:

- `model/migrations/sql/0100_baseline.sql` (edited in place, no new migration)
- `api/internal/authz/authz.go` (`checkPartyPolicy`)
- `api/cmd/server/main.go` (`ApplyFuncs`)
- a public facade for host composition roots
- every mod-users list path that joins an `accessible_*_ids_for_actor` function (today `api/internal/service/principal_admin.go`)
- the test harnesses that call `ApplyFuncs`
- the parity and integration tests

This task runs after task 001, which fixes `gated()`'s marker semantics that the predicate mirrors.

## Requirements

1. **Prerequisite check.** In the mod-core sibling, confirm:
   - `api/authz/setup/row_filter.go` exports `NewRowFilterGenerator` and `BindAuthInputs`, and its template passes six arguments ending in the `moduleforge.authz_system_actor` setting
   - `api/authz/setup/paritytest/paritytest.go` exports `Compare`

   Confirm also that task 001 has merged (`gated()` calls `opctx.IsSystemActor`). If anything is missing, stop and report `blocked`.
2. **The predicate `mod_users.access_row_permitted`.** Add it to `0100_baseline.sql` with the six-argument signature, in the argument order above. Make it `RETURNS BOOLEAN`, `STABLE`, `SECURITY INVOKER`, `SET search_path = pg_catalog, pg_temp`, with schema-qualified references like every mod-users function. It must return exactly what the single-row prelude decides for the same actor, row, session and credential:
   - **System actor.** `p_system_actor IS TRUE` returns true. This mirrors `gated()`'s `IsSystemActor` skip. NULL and false never exempt (the 8FYk reading). Do not write `p_system_actor IS NOT FALSE` or `COALESCE(p_system_actor, true)`.
   - **Owning-party gate.** Otherwise, map `p_session_id` and `p_credential_id` from 0 to NULL, as `checkPartyPolicy` does with `NULLIF`. Then require `mod_users.session_meets_party_policy` to pass for `p_owner_id` and, when `p_entity_id` is a `legal_entity`, also for `p_entity_id` (Decision F: a party is its own owning party, and both gates apply). Decision D (operators exempt), the acting-profile operator exemption for impersonation (Decisions G and J, via `session_policy_factors`' `impersonator_profile_id`) and Decision L confinement all stay inside `session_meets_party_policy` and its helpers. Do not re-implement them.
   - **Unbound call.** When all three authentication inputs are NULL (the caller skipped `BindAuthInputs`), the predicate must fail closed for every row the gate would refuse. Rows the gate does not refuse stay visible: the actor inside the owning party, the actor's own subject, an operator, an owner with no restrictive policy. `session_meets_party_policy` already returns false for "neither id" on a restrictive party, so this should follow from the bullet above. Prove it by test (requirement 7).
   - **Anonymous (finding `7Bb9`).** The single-row prelude exempts the anonymous context, but the predicate receives no anonymous flag. Decide one of two options and record the decision in the function comment and the report:
     - (i) derive anonymity from `p_actor_entity_id` (the holder is the `system_actors` row with slug `anonymous`)
     - (ii) apply no anonymous branch, because the anonymous profile holds no grants (mod-authz's actor trigger) and lists nothing either way

     Either way, a parity case with the anonymous context must pass.
   - **Optional hardening (findings `erS4` and `6nAj`, mod-core backlog).** You may additionally require the actor profile's holder to be a `system_actor` principal before honoring `p_system_actor`. Do this only if the single-row side applies the identical check (requirement 3), so parity holds. Otherwise leave it out and say so in the report.
3. **One SQL definition shared by both sides.** Refactor so the single-row gate and the list predicate share one SQL definition of the owning-party decision. For example, a `mod_users` function taking the acting profile, session, credential, target entity and owner, that both `checkPartyPolicy`'s query and `access_row_permitted` call. The single-row side's `AuthorizeTypeIn` has an owner but no target entity, and that must still work. Keep `checkPartyPolicy`'s Go contract (`partyPolicyQuery`, the stub dispatch used by unit tests) unchanged unless a change is needed, and report any change.
4. **Install the hook.**
   - In `api/cmd/server/main.go`, replace `setup.ApplyFuncs(ctx, pool, setup.NewGrantTableGenerator(), authzSlugs)` with a generator built by `setup.NewRowFilterGenerator(setup.NewGrantTableGenerator(), "mod_users.access_row_permitted")`. Handle the constructor's error by failing startup. Every slug in `authzSlugs` is filtered, because single-row `Authorize` applies the owning-party gate to every target with an owner.
   - Export the predicate name and a constructor for host composition roots from a public package (`api/localAuthz` or `api/usersservice`; pick the one hosts already import for the `Authorizer` and justify it). For example, a constant `AccessRowFilter = "mod_users.access_row_permitted"` and a `func AccessFuncGenerator() (setup.AccessFuncGenerator, error)`. A host that composes mod-users must install the access functions through it.
   - Update every test harness that calls `ApplyFuncs`. At least these: `api/internal/integration/harness_integration_test.go`, `api/internal/authz/authz_integration_test.go` and `api/internal/authz/parity_integration_test.go`. Tests that deliberately need the unfiltered body must say so in a comment.
5. **Bind in every list transaction.** Find every non-test mod-users query that joins an `accessible_*_ids_for_actor` function. Search `api/` and `model/queries/`; today it is `ListPrincipals` in `principal_admin.go`, which runs the count and page queries directly on the pool. For each:
   - Run the queries inside one read-only transaction opened with the request context.
   - Call `setup.BindAuthInputs(ctx, tx)` first and **check its error**. An error refuses the request (return it); never continue with an unbound or stale binding (finding `aQow`).
   - Run every list query of that request in that transaction.

   One transaction serves one request context. Never reuse a bound transaction for a different context. mod-users must never call `set_config` with session scope (`is_local = false`) on these settings (finding `6nAj`).
6. **Inline comments at every new branch (user requirement).** In SQL, put a comment at each branch of `access_row_permitted` and of the shared gate function saying why it exists and what it implies:
   - the system-actor clause: only `IS TRUE` exempts; only contexts built with `opctx.WithSystemActor` produce it; NULL and false fail closed
   - the 0-to-NULL mapping
   - the unbound all-NULL case: the caller skipped `BindAuthInputs`, which fails closed, silently, for gated rows
   - the Decision F legal-entity branch
   - the anonymous decision
   - where Decisions D, G, J and L are applied (inside `session_meets_party_policy`), so nobody adds a second operator exemption

   In Go, put a comment at each `BindAuthInputs` call and its error branch (why the binding is required, and that an error refuses rather than lists unfiltered or stale), at the hooked-generator construction in `main.go`, and in the exported facade's doc comment (what a host must do, and that skipping it breaks list/single-row parity).
7. **Tests.**
   - **Parity (`setup/paritytest`).** Extend `api/internal/authz/parity_integration_test.go` (or add a sibling file) to call `paritytest.Compare` with the hooked generator installed, across the arms the fixtures cover (wildcard, entity, type-entity, owned-scope, own). Cover these contexts:
     - a cross-party collaborator below the owning party's `min_acr`
     - a collaborator whose credential is not authorized under `credential_rule` `authorized`
     - the same collaborator meeting the policy
     - a collaborator with a `profile_policy_confirmations` row (exempts the credential rule only, never `min_acr`)
     - an operator profile (Decision D)
     - an operator impersonation and a non-operator impersonation (Decisions G, J and L)
     - an `opctx.WithSystemActor` context
     - the anonymous context
     - a personal profile reaching its own records
     - a party record under its own policy (Decision F)

     `Compare` returns an error for non-`ErrForbidden` refusals, such as the step-up error, so give each context an ACR and auth time that pass the operation-level assurance gate.
   - **All-NULL conformance (finding `8FYk`).** A SQL-level test calls `mod_users.access_row_permitted(actor, row, owner, NULL, NULL, NULL)` on a row that the gate refuses for that actor and asserts the result is not true. It also asserts that `p_system_actor` NULL or false with an otherwise-failing session is not true, and that `p_system_actor` true is true.
   - **Unbound list.** A list query run in a transaction without `BindAuthInputs` drops the gated rows and keeps the ungated rows for the same actor.
   - **HTTP integration (`api/internal/integration`).** A cross-party collaborator below the owning party's policy calls `GET /v1/principals` and sees none of that party's gated rows, for example its managed users reached through a grant. The same collaborator meeting the policy sees them. The single-row route for one of those principals refuses the below-policy collaborator, the parity check at HTTP level.
   - Mutation check: temporarily make the predicate return true unconditionally, then temporarily remove the `BindAuthInputs` call. The collaborator tests must fail each time. Restore the code.
8. **Siblings and host apps (report and findings, no edits).** With the hook installed, every list query in any composed module that does not call `BindAuthInputs` now drops rows of restrictive-policy parties for cross-party collaborators. It fails closed, silently. Grep the sibling plan worktrees under `/Users/zane/playground/moduleforge/*/worktrees/plan/identity-model-impl` (read only) for non-test callers of `accessible_*_ids_for_actor` and for `ApplyFuncs` calls. Then:
   - For each sibling whose list paths lack `BindAuthInputs`, file one `needs-sibling: <repo>` finding naming the files.
   - For each host composition root that composes mod-users but installs access functions without mod-users' generator, file one finding as well. Check existing followups first; mod-core already filed the app-mfmanager rewriter gap, so do not duplicate it.

   List all of these in the report.
9. **Dev database.** `0100_baseline.sql` is edited in place. State in the report that existing dev databases must be rebuilt (`make clean.data`). Task 004 records it in the docs.
10. **Working rules.**
    - Never run `scripts/link-siblings.sh` and never run a bare root `make preflight` in a plan or task worktree.
    - Every build and `make` invocation sets `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users`. Afterwards, verify with `ls -l /Users/zane/playground/moduleforge/mod-users/worktrees/plan` that `mod-core`, `mod-authz` and `mod-audit` resolve to the siblings' `worktrees/plan/identity-model-impl`.
    - Run `make -C model compose`, then the unit suites and the `authz`, `config` and `integration` suites against a throwaway `postgres:16` container with superuser `users` that you start and remove yourself (`AGENTS.md` recipe). Never use the shared `users-module-postgres` container. Remove your container even when tests fail.
    - Never edit any sibling.
11. **Findings.** File out-of-scope issues with `followups_add` (`plan_slug: identity-model-impl`, `phase_slug: adopt-core-engine-remediation`, a `type:` tag). Never edit any `followups.yaml`, never call `followups_patch`, never remove a followup.
12. **Report.** Return the structured report the Go developer role defines. Include:
    - the predicate's final logic and the anonymous decision
    - the shared SQL definition
    - the facade name hosts must use
    - every list path now bound
    - the sibling and host findings filed
    - the mutation-check results

## Validation

1. With `MODULEFORGE_SIBLINGS_DIR` set: `make build`, `make test` and `make lint` pass, and `make -C model verify` passes.
2. The `authz`, `config` and `integration` suites pass against the throwaway `postgres:16`, including the parity, all-NULL, unbound-list and HTTP collaborator tests. No container of yours is left running.
3. The mutation checks in requirement 7 fail as expected, and the code is restored.
4. `grep -n 'access_row_permitted' model/migrations/sql/0100_baseline.sql api/cmd/server/main.go` shows the function and its installation. The exported facade constant or constructor exists.
5. `grep -rn 'accessible_[a-z_]*_ids_for_actor' api --include=*.go | grep -v _test.go`: every hit runs in a transaction that called `setup.BindAuthInputs` with its error checked. Show this in the report, file by file.
6. `grep -rn 'NewGrantTableGenerator()' api --include=*.go` shows no non-test `ApplyFuncs` call without the row filter. Every remaining test use carries a comment explaining why it is unfiltered.
7. `grep -rn "set_config" api model/migrations` shows no session-scoped (`false`) write of a `moduleforge.authz_*` setting.
8. **Comment check (user requirement).** A review of the diff confirms the WHY and IMPLIES comment from requirement 6 at each branch of `access_row_permitted` and of the shared gate function (in SQL), at each `BindAuthInputs` call and its error branch, at the generator construction and on the facade (in Go). The `access_row_permitted` comments name the system-actor clause, the NULL/unbound fail-closed case, Decision F and where Decisions D, G, J and L apply.
9. `ls -l /Users/zane/playground/moduleforge/mod-users/worktrees/plan` shows the three sibling links resolving to the siblings' `worktrees/plan/identity-model-impl`. No sibling worktree has changes from you.

## Metadata

architectural_impact: true

## Assumptions

- Task 001 has merged: `gated()` skips the gates only for `opctx.IsSystemActor` and anonymous contexts.
- `setup.ApplyFuncs` creates the access functions after migrations, so `mod_users.access_row_permitted` exists when they are created.
- The predicate runs per candidate row and reads tables, so it is not inlined. One extra round trip per list transaction (`BindAuthInputs`) is accepted. The mod-core cost findings (`PtOU`, `UlWW`) are mod-core backlog; do not work around them here.
- mod-core's hardened `core_is_operator_profile` (false for anonymous, ended or suspended profiles and disabled holders; invited still qualifies, finding `OUgZ`) is what Decision D's exemption now evaluates. Tests that used a non-live operator profile as an operator are fixed by giving the fixture a live one. Report each such case.

## References

- mod-core plan worktree (read only):
  - `plan/phase-11-identity-engine-proof-remediation/005-list-side-row-filter-hook.md` and `006-system-actor-marker-and-authz-contract.md` (Status sections)
  - `api/authz/setup/row_filter.go` (the predicate contract, `BindAuthInputs`)
  - `api/authz/setup/paritytest/paritytest.go`
  - `api/authz/setup/row_filter_integration_test.go` (a worked test predicate)
  - `AGENTS.md` (`authz/setup` row)
  - `docs/architecture/type-level-authorization.md`
- Findings `8hQB` (mod-core project followups) and `Ddx9` (mod-users project followups): the list path skips the owning-party gate.
- Informational mod-core findings: `8FYk` (all-NULL conformance), `aQow` (stale binding), `6nAj` (session-scoped settings), `erS4` (exported marker field), `7Bb9` (anonymous derivation), `PtOU` and `UlWW` (cost).
- [Decisions](../notes/decisions-and-resolutions.md): amendments C, D, F, G, J and L.
- [Phase 8 task 001](../phase-08-users-authorization-engine/001-rewrite-authorization-engine.md) and [phase 19 task 001](../phase-19-remediation-08/001-impersonator-profile-credential-exemption.md): the single-row gate and its impersonation exemption.
- [Task 001 of this phase](./001-key-gates-on-system-actor-marker.md).
- [Phase summary](../phases/adopt-core-engine-remediation.md).

## Checkpoint hints

- After the predicate and the shared gate function compile and the SQL-level tests pass.
- After `main.go`, the facade and the harnesses install the hooked generator.
- After `ListPrincipals` (and any other list path) binds in a transaction.
- After the parity and HTTP integration tests pass.
