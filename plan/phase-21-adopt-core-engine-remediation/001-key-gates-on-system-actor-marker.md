# Key The Engine Gates On The Explicit System-Actor Marker

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task adopts mod-core's explicit system-actor marker in mod-users' authorization engine. It closes finding `FZSa` and the mod-users side of `j75L`.

mod-core's remediation phase 11 task 006 added `AuthContext.SystemActor`, set by `opctx.WithSystemActor(ctx, principalID, profileID)` and read through `opctx.IsSystemActor(ctx)`. `IsSystemActor` is true only when the marker is set, `Anonymous` is false, `ImpersonatorPrincipalID` is 0 and `ProfileID` is positive. mod-core's contract (`api/authz/authz.go`, "Trust and assurance") now says a context with no session, no credential and no marker is **not** a trusted context.

mod-users' `gated()` (`api/internal/authz/authz.go`) still infers trust from absent fields: `!ac.Anonymous && (ac.SessionID != 0 || ac.CredentialID != 0)`. Any non-anonymous context without a session and a credential skips both the assurance gate and the owning-party gate. mod-users builds such contexts for real users in its unauthenticated flows. Today they reach only observations and trusted creation writes, and `api/internal/service/sessionless_contexts_test.go` pins that. This task makes the engine fail closed for them.

Roles: read `developer-go.md` and `developer.md` (Go developer role) before starting. No standard skill covers this task.

Scope:

- `api/internal/authz/authz.go` (`gated`, its callers, the package and engine doc comments that describe the system-actor rule)
- every sessionless trust-bearing context builder in `api/`
- `api/internal/service/sessionless_contexts_test.go` and the authz tests that relied on the inferred exemption

The list-side predicate and `setup.BindAuthInputs` are task 002. Project docs are task 004, apart from the code comments this task changes.

This task runs first in the phase. Task 002 builds on the semantics fixed here.

## Requirements

1. **Prerequisite check.** Confirm that the mod-core sibling resolves to mod-core's plan worktree and contains the marker. In `<siblings>/mod-core/api/opctx/opctx.go`, `grep -n 'func IsSystemActor\|SystemActor bool'` must show both. If either is missing, stop and report `blocked`.
2. **`gated()` keys on the marker.** The prelude's gates (the assurance gate and the owning-party gate) are skipped only for these two contexts:
   - a context for which `opctx.IsSystemActor(ctx)` is true
   - the anonymous context, unchanged (U14: the anonymous profile holds no grants)

   Every other context is gated, including a non-anonymous context with no session and no credential. Pass `ctx`, or the result of `IsSystemActor`, into `gated` as needed. Do not re-derive the rule from `AuthContext` fields: call `opctx.IsSystemActor` so mod-users and mod-core cannot drift.

   A gated sessionless context then fails closed in two ways:
   - the assurance gate refuses it when the operation's `min_acr` exceeds its ACR, or when `max_auth_age` is set and `AuthTime` is zero
   - `mod_users.session_meets_party_policy` returns false for a restrictive-policy party it is outside of

   Confirm both by test (requirement 6) rather than by reading.
3. **Keep Decisions D, G and J.** Exemptions keyed on the acting profile or the session stay as they are:
   - operators exempt from the owning-party gate (D)
   - the impersonation credential-rule exemption keyed on the recorded `impersonator_profile_id` (G as resolved, J)
   - non-operator confinement (L)

   The marker must never be read as an operator signal. An impersonating context is never a system actor (`IsSystemActor` is false whenever `ImpersonatorPrincipalID != 0`). Do not change the SQL in `0100_baseline.sql` in this task.
4. **Review every sessionless trust-bearing context builder.** Enumerate the non-test code that builds an `opctx.AuthContext` with neither a session nor a credential. At least these:
   - `withActingProfile` (`api/internal/service/helpers.go`) and its callers
   - `CreatedUser.ActingContext` (`api/internal/service/user_service.go`), used by sign-up, guest creation, `register.go` and the bootstrap
   - `UnauthenticatedActingContext` and the builder near `session_service.go:789` (`api/internal/service/session_service.go`), used by password reset and the OIDC link and step-up flows
   - the `idp_jit` `actCtx` (`api/internal/handlers/auth/oidc_flows.go`, around line 294, built after the JIT profile is created) and `sessionActingContext` in the same file
   - `withFallbackActor` (`api/internal/service/session_flows.go`)
   - the bootstrap's `adminCtx` (`api/internal/service/bootstrap_service.go`)
   - `api/internal/auth/anonymous_actor.go` (anonymous, stays exempt as anonymous, not as a system actor)
   - any other hit of `grep -rn 'opctx.WithAuthContext(' api --include=*.go`

   Classify each builder:
   - **(a) Acting for a real user without a session.** It must **not** carry the marker. It stays built with `opctx.WithAuthContext` and is now gated: if it ever reached `Authorize`, the engine would apply the gates and refuse where policy or assurance is not met.
   - **(b) A genuine system flow** (trusted code acting as a system actor profile, not for a user). It uses `opctx.WithSystemActor`.

   Record the classification of every builder in the task report (file, function, class, reason). Expect most or all of them to be class (a). A real user's personal or party profile must never be marked a system actor. If no class (b) builder exists, say so: the marker then has no mod-users producer, which is the intended outcome.
5. **No behavior change for the unauthenticated flows.** Sign-up (password and email code), guest creation and upgrade, password reset, the OIDC JIT, link and step-up callbacks, bootstrap and refresh-reuse revocation keep working exactly as before. They must not start calling `Authorize`. If a flow does call an Authorize entry point with a class (a) context and now fails, do not add the marker to make it pass. Stop and report it as a decision for the manager, naming the flow, the operation and the target.
6. **Tests.**
   - Unit tests in `api/internal/authz` show that the gates apply to a non-anonymous context with no session, no credential and no marker:
     - an operation with `min_acr` above the context's ACR is refused with the step-up error
     - a cross-party target under a restrictive `credential_rule` is refused with `ErrForbidden`, through the party-policy stub
   - The same calls with an `opctx.WithSystemActor` context skip both gates.
   - An anonymous context still skips them.
   - An impersonating context that carries `SystemActor: true` set by a struct literal is gated (fail closed).
   - An integration test in `api/internal/authz` (real Postgres) shows that a `withActingProfile`-style context for a real user is refused by the owning-party gate on a restrictive third party's row. The same profile with an interactive session meeting the policy is allowed.
   - Rework `api/internal/service/sessionless_contexts_test.go` so that it pins the new rule:
     - Pin 1: every helper in requirement 4's list returns a context for which `opctx.IsSystemActor` is **false** (or, for a class (b) helper, true). The test names each helper, so a new builder fails the test until someone classifies it.
     - Pin 3: the static audit flags any non-test call of `opctx.WithSystemActor` outside an allow-list (empty if no class (b) builder exists), and any `AuthContext` literal that sets `SystemActor`.
     - Pin 2: keep it as defense in depth, or replace it with an engine-level refusal test. Say which in the report.
   - Tests that relied on the inferred exemption must be updated to state their intent: they used a sessionless, non-anonymous context to reach an arm without the gates. Use `opctx.WithSystemActor` where the test means "trusted system context", or a session with sufficient ACR where it means "a user who meets policy". Do not weaken any assertion.
7. **Inline comments at every new or changed branch (user requirement).** Add an inline code comment at the branch itself:
   - In `gated()`: why the gates are skipped only for `IsSystemActor` and anonymous. What that implies:
     - a sessionless, credential-less context without the marker is gated and fails closed
     - only trusted code acting as a system actor uses `opctx.WithSystemActor`
     - the marker is never an operator signal, and Decision D's operator exemption lives in `mod_users.session_meets_party_policy`, not here
     - impersonating contexts are never system actors (Decisions G, J and L still apply to them)
   - In each builder reviewed under requirement 4: why it does or does not carry the marker, and what that implies. A class (a) builder must never reach `Authorize`, and would be refused there.
   - At every `opctx.WithSystemActor` call, if there is one: why the flow is a genuine system flow.
   - Delete the existing "Sessionless trust is inferred from absent fields ... pending an explicit mod-core system-actor marker" comment. It no longer describes the code.
8. **Working rules.**
   - Never run `scripts/link-siblings.sh` and never run a bare root `make preflight` in a plan or task worktree.
   - Every build and `make` invocation sets `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users`. Afterwards, verify with `ls -l /Users/zane/playground/moduleforge/mod-users/worktrees/plan` that `mod-core`, `mod-authz` and `mod-audit` still resolve (through `/Users/zane/playground/.flow/siblings/mod-users`) to the siblings' `worktrees/plan/identity-model-impl` directories.
   - Run `make -C model compose`, then the unit suites and the integration suites (`./internal/authz/...`, `./internal/config/...`, `./internal/integration/...`). Run them against a throwaway `postgres:16` container with superuser `users` that you start and remove yourself (`AGENTS.md`, "Integration tests against a throwaway Postgres"). Never use `make dev.start` or the shared `users-module-postgres` container. Remove your container even when tests fail.
   - Never edit mod-core, mod-authz, mod-audit or any other sibling.
9. **Findings.** File out-of-scope issues with `followups_add` (`plan_slug: identity-model-impl`, `phase_slug: adopt-core-engine-remediation`, a `type:` tag). Never edit any `followups.yaml`, never call `followups_patch`, never remove a followup.

   One known sibling issue: mod-authz's `api/service/stepup.go` infers sessionless trust the same way (`SessionID == 0 && CredentialID == 0` returns "no step-up needed"). File it as a `needs-sibling: mod-authz` finding unless a followup for it already exists. Check with `followups_list` on `/Users/zane/playground/moduleforge/mod-authz` and `/Users/zane/playground/moduleforge/mod-core`.
10. **Report.** Return the structured report the Go developer role defines. Include the builder classification (requirement 4), the tests changed and why, and any decision raised under requirement 5.

## Validation

1. With `MODULEFORGE_SIBLINGS_DIR` set: `make build`, `make test` and `make lint` pass, and `make -C model verify` passes.
2. The `authz`, `config` and `integration` suites pass against the throwaway `postgres:16`, and no container of yours is left running.
3. The new unit and integration tests from requirement 6 pass. Mutation check: temporarily revert `gated()` to the field-inferred rule. The sessionless-refusal tests must then fail. Restore the code.
4. `grep -n 'SessionID != 0 || ac.CredentialID != 0\|inferred from absent fields' api/internal/authz/authz.go` returns nothing.
5. `grep -rn 'opctx.WithSystemActor\|SystemActor:' api --include=*.go | grep -v _test.go` lists only the class (b) builders named in the report (or nothing).
6. **Comment check (user requirement).** A review of the diff confirms the WHY and IMPLIES comment from requirement 7 at `gated()`'s branch, at each reviewed builder and at each `WithSystemActor` call. `grep -n -B2 -A8 'func gated' api/internal/authz/authz.go` shows a comment that names `IsSystemActor`, the fail-closed outcome for unmarked sessionless contexts, and Decisions D, G and J.
7. `ls -l /Users/zane/playground/moduleforge/mod-users/worktrees/plan` shows `mod-core`, `mod-authz` and `mod-audit` resolving to the siblings' `worktrees/plan/identity-model-impl`. `git -C` on each sibling plan worktree shows no change made by you.

## Metadata

architectural_impact: true

## Assumptions

- mod-core's remediation phase 11 is merged on mod-core's plan branch, which is what the mod-core sibling link resolves to.
- mod-core's hardened `core_is_operator_profile` (false for the anonymous profile, ended or suspended profiles and disabled holders; invited profiles still qualify) is already in the composed baseline. A test that fails because it used an ended, suspended or disabled operator profile as an operator is fixed by giving the fixture a live operator profile, not by weakening the assertion. Report each such case.
- `AuthContext.SystemActor` is an exported field. A struct literal can set it (finding `erS4`, mod-core backlog). Pin 3 of the static audit is mod-users' guard against that inside this repo.

## References

- mod-core plan worktree (read only): `plan/phase-11-identity-engine-proof-remediation/006-system-actor-marker-and-authz-contract.md`, `api/opctx/opctx.go` (`SystemActor`, `WithSystemActor`, `IsSystemActor`), `api/authz/authz.go` ("Trust and assurance", "Implementation-level gates"), `AGENTS.md` (`opctx/` row).
- Finding `FZSa` (mod-core project followups): adopt the marker in `gated()` and review the sessionless builders.
- Finding `j75L` (mod-users project followups): the original report that `gated` fails open for sessionless contexts.
- Finding `erS4` (mod-core project followups, informational): the marker field is exported.
- [Decisions](../notes/decisions-and-resolutions.md): U5 and U14 (the inferred rule this task replaces), amendments D, G, J and L.
- [Phase 8 task 001](../phase-08-users-authorization-engine/001-rewrite-authorization-engine.md): the prelude and the owning-party gate.
- [Phase summary](../phases/adopt-core-engine-remediation.md).

## Checkpoint hints

- After `gated()` and its unit tests are green.
- After the builder review and the reworked `sessionless_contexts_test.go`.
- After the integration suites pass.
