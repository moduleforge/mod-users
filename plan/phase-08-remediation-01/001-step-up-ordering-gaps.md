# Close Step-Up Ordering Gaps for SSH Keys and StartLink

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Remediation of three phase-3 review findings (plan findings `jAXe`, `P2k6`, `gKQO`). Common rules: `/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md`; decisions and audit: [handler inventory](../notes/handler-inventory.md) (D9, F7).

1. `jAXe`: on the self SSH-key register routes, `sshkey.Parse` and `NormalizeLabel` still run service-side after `checkStepUp`, so a malformed key or label presented with a single-use step-up token consumes the token and then returns 400 (and without a token returns 409 instead of 400).
2. `P2k6`: `StartLink` runs `checkStepUp` before the `return` query validation inside `oauth.LinkAuthorizeURL`.
3. `gKQO`: the `UserAccountService.AuthorizeCreate` comment says handlers call it before reporting a request-shape error, which is no longer true.

## Requirements

1. Add `sshkey.Parse` and `NormalizeLabel` checks (stateless, no DB) to `registerSSHKeyRequest.Validate` in `api/internal/handlers/ssh_keys.go` so they run before `checkStepUp` on `RegisterSelf` and `RegisterForAccount` (and the self-targeting operator route). Keep the service-side copies for in-process callers. Use detail codes consistent with what the service produces today for these failures.
2. Export or split the return-path validator used by `oauth.LinkAuthorizeURL` so `StartLink` validates the `return` query before `checkStepUp`; keep the provider-registry 404 after step-up (configuration state). Map `LinkAuthorizeURL`'s internal sign/rand failures to a 500 (wrapped error through `apiresp.WriteError`) instead of a bare `ErrInvalidInput` 400. Keep behavior for well-formed requests identical.
3. Reword the `AuthorizeCreate` doc comment (`api/internal/service/user_accounts.go`) as the type-level create gate used by `Create`, noting the HTTP handler validates shape first.
4. Tests (helper prefix `stepord`; `grep -rn` for collisions first): a malformed key or label with a valid step-up token returns 400 and the token is not consumed (the consumed store stays empty); the same malformed request with no token returns 400, not 409; a well-formed request with no token still returns the 409 `users.step_up_required` envelope; same trio for `StartLink` with a malformed `return`; sign/rand failure is a 500.
5. Update the wire-risk entry for task 3.5 in `plan/notes/wire-risk.md` (append a dated entry): malformed key/label and malformed `return` now 400 before step-up and do not consume the token.
6. Do not edit go.mod and do not change any public auth route.

## Validation

Run from `api/` in the task worktree (build setup: [handler inventory](../notes/handler-inventory.md)):

- `gofmt -l .` prints nothing.
- `go vet ./...` and `go vet -tags=integration ./...` pass; `make lint` passes.
- `go test ./...` passes.
- Run the Postgres integration suite per `AGENTS.md` and confirm tests ran (no skips).

## Metadata

architectural_impact: false

## Assumptions

- Findings `jAXe`, `P2k6` and `gKQO` are open in the plan findings store and are closed by this task.
