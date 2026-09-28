# 006: Fix Operator SSH-Key Step-Up Bypass

## Purpose and scope

Phase 1's boundary review (phase-review, security lens, finding `security-001`, major/high-confidence) found that the step-up gate on SSH public-key mutations can be bypassed. `RegisterSelf`/`RevokeSelf` (`api/internal/handlers/ssh_keys.go`) correctly call `checkStepUp` when `AUTH_REQUIRE_STEP_UP` is on. `RegisterForAccount`/`RevokeForAccount` — the operator routes, `POST`/`DELETE /v1/user-accounts/{uuid}/ssh-keys[/{key_uuid}]` — never call `checkStepUp` at all.

`SSHKeyService.loadAuthorizedAccount` (`api/internal/service/ssh_keys.go`) authorizes both the self and operator paths identically, via `Authorizer.Authorize(ctx, "update", &accountHolder)`. Per `api/internal/authz/authz.go`'s ownership arm, a natural person self-owns their own account-holder entity, so that `Authorize` call succeeds when the caller targets their *own* account UUID — including through the un-gated operator route. The consequence: any ordinary authenticated, email-verified user can register or revoke an SSH key on their own account by calling the operator endpoint with their own UUID in the path, and never present an `X-Step-Up-Token`. This defeats the step-up control for a credential that grants git-over-SSH access — exactly the stolen-bearer-token scenario step-up exists to stop.

This task closes that gap. It does not touch the self-service routes (already correct), the service layer's authorization logic (correct as designed — `update` self-ownership is intentional elsewhere), or anything outside `api/internal/handlers/ssh_keys.go` and its tests.

## Requirements

1. In `RegisterForAccount` and `RevokeForAccount` (`api/internal/handlers/ssh_keys.go`), after parsing the path UUID and before calling into `SSHKeyService`, determine whether the target account UUID equals the caller's own account UUID (`uc.UserUUID`, from `localauth.MustFromContext(r.Context())` — mirror however `RegisterSelf`/`RevokeSelf` already obtain the caller's identity in this same file).
2. When the target is the caller's own account, apply the identical step-up gate the self routes already use: call `checkStepUp(r, uc.UserAccountID, h.stepUpRequired, h.jwtSecret, h.consumed)` (the shared helper extracted in task 004) and return the existing `writeStepUpRequired` response (`409 users.step_up_required`) on failure, exactly matching `RegisterSelf`/`RevokeSelf`'s behavior. Thread the resulting `stepUpUsed` value into the service call in place of the current hard-coded `false`, matching how the self routes already do it.
3. When the target is a *different* account (the genuine operator-on-behalf-of case), behavior is unchanged: no step-up gate, proceed to `SSHKeyService` as today.
4. Do not change `SSHKeyService`'s authorization logic, the self-service routes, or any other handler in this package.
5. Add a handler-level test (in `api/internal/handlers/ssh_keys_test.go`, following the existing stub-service pattern already used for step-up tests in this file) asserting: a non-admin `POST`/`DELETE` on `/v1/user-accounts/{own-uuid}/ssh-keys[...]` with step-up required and no token returns `409 users.step_up_required`; the same call with a valid step-up token succeeds; and a `POST`/`DELETE` on `/v1/user-accounts/{other-uuid}/ssh-keys[...]` by an operator continues to succeed with no step-up token required (unchanged behavior — do not regress the existing operator-authorization coverage).

## Validation

- `cd api && go test ./internal/handlers/...` passes, including the new self-targeting-operator-route step-up tests and all pre-existing tests unchanged.
- `make build.api` and `make lint.api` pass.
- `grep -n "checkStepUp" api/internal/handlers/ssh_keys.go` shows it called in all four of `RegisterSelf`, `RevokeSelf`, and (new) the self-targeting branches of `RegisterForAccount`/`RevokeForAccount`.
- Manually trace: an operator (non-self, different account) call still requires no step-up token and is unaffected by this change.
- Record a Status section noting whether the integration suite (`api/internal/authz/ssh_keys_integration_test.go`'s `TestInteg_SSHKeys_OperatorAuthorization`) was also extended or re-run to cover the self-targeting-via-operator-route case at the integration level; not required, but note whether it was done.

## Status

**Outcome:** succeeded (2026-09-28)

`RegisterForAccount` and `RevokeForAccount` (`api/internal/handlers/ssh_keys.go`) now resolve the caller's own account UUID via a new `isSelfTargetingOperatorCall` helper and, when the path `{uuid}` equals it, apply the identical `checkStepUp`/`writeStepUpRequired` gate `RegisterSelf`/`RevokeSelf` already use, threading the resulting `stepUpUsed` into the `SSHKeyService` call in place of the previous hard-coded `false`. A genuine operator-on-behalf-of-another-account call (target UUID differs from the caller's own) is unaffected: no gate, `stepUpUsed` stays `false`.

Validation:
- `cd api && go test ./internal/handlers/...` — passed (all pre-existing tests green, plus new self-targeting-operator-route step-up tests: `TestSSHKeysHandler_StepUp_OperatorRoutes_SelfTargeting_FlagOn_NoToken_Returns409`, `TestSSHKeysHandler_StepUp_OperatorRoutes_SelfTargeting_FlagOn_ValidToken_Proceeds`, `TestSSHKeysHandler_StepUp_OperatorRoutes_DifferentAccount_FlagOn_NoTokenNeeded`). Two pre-existing tests (`TestSSHKeysRoutes_MountedAtRightPathAndMethod`'s operator-register/revoke cases, `TestSSHKeysHandler_OperatorRoutes_NeverStepUpGated`) were updated to attach a `UserContext` to the request (via the existing `withUC` test helper) with a *different* account UUID than the path target, since the handlers now call `localauth.MustFromContext` unconditionally — matching how the routes are always wired behind `RequireAuth` in `cmd/server/main.go`. Their assertions (operator call to a different account succeeds / is never step-up-gated) are unchanged.
- `make build.api` — passed.
- `make lint.api` — passed (`go vet` + `check-server-error-literals`; `gofmt` needed on `ssh_keys_test.go`, applied).
- `grep -n "checkStepUp" api/internal/handlers/ssh_keys.go` — shows 4 call sites: `RegisterSelf` (unchanged), `RevokeSelf` (unchanged), and the new self-targeting branches of `RegisterForAccount`/`RevokeForAccount` (each inlines its own `checkStepUp` call, matching the self routes literally rather than going through one shared step-up-calling helper).
- Manual trace: an operator (non-self, different account) call parses the path UUID, resolves the caller's own UUID via `isSelfTargetingOperatorCall`, finds it does not equal the target, and proceeds straight to `h.register`/`h.revoke` with `stepUpUsed=false` — identical to the pre-fix code path. No step-up token is required or checked in that branch.
- Integration suite (`TestInteg_SSHKeys_OperatorAuthorization`): **not extended or re-run.** It exercises `SSHKeyService` directly (via `actorCtx`), below the HTTP handler layer where this fix lives, so it does not exercise `checkStepUp`/step-up headers at all — extending it to cover the self-targeting-via-operator-route case would mean adding HTTP-level assertions foreign to that file's established service-layer pattern. It also requires a live Postgres (`//go:build integration`), not available in this worktree run. Left untouched, per the task's "not required" allowance.

Files touched: `api/internal/handlers/ssh_keys.go`, `api/internal/handlers/ssh_keys_test.go` (plus this task document).
