# Order Tests Self Service Routes

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Cover the 15 self-service routes with `ordertest.Run`, or with dedicated tests where the helper cannot model a route:

- `GET`/`PUT /self`;
- the 7 identities and credential routes;
- the 6 SSH-key routes, both self and operator.

Also add the D11 gate-position tests. Reuse the harness from task 4.2, per decision D14 in the [handler inventory](../notes/handler-inventory.md#planning-decisions) and [common section 5](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#5-order-test-recipe-apirespordertest).

## Requirements

1. Create `api/internal/handlers/self_service_order_test.go`. Reuse the `order*` helpers. Declare new helpers only with the prefix `orderSelf`, and grep for collisions first. Extend the shared fakes only if strictly needed, and only in `order_helpers_test.go`; say so in the report. Wire the real `SelfHandler`, `IdentitiesHandler` (step-up on, with a test secret and a consumed-JTI map), `SSHKeysHandler` and `usersservice.NewSSHKeyService` into the real registrars. Mount them the way the manifest composes them: read routes without `RequireVerifiedEmail`, write routes with it.
2. Self routes have no target UUID, so `ordertest`'s missing/existing pair does not apply to most of them. For each route, write either an `ordertest.Route` (the SSH-key operator routes: `AuthorizedMissingStatus: 403`, masked) or a table test that asserts the module order explicitly:
   - anonymous gets 401;
   - a malformed body or UUID gets 400, both with and without a step-up token (D9);
   - a missing step-up token gives 409 `users.step_up_required` with the action-required envelope intact;
   - a stranger on an operator SSH-key route gets 403;
   - for `PUT /self`, blank names give 400 with the `natural_persons.*` codes (D5);
   - for SSH-key register, a malformed key from a forbidden caller gives 400 (D10).

   Credential-failure 401s (current_password, step-up verify) are checked after shape.
3. D11 gate tests. On a verified-email route:
   - an unverified authenticated caller with a malformed body gets 403 action-required `users.email_unverified`, which documents the gate's position;
   - with `requireOIDCConfirmed` in front, an unconfirmed state gives 503 before 401.

   Add a comment linking the reason (documented exception).
4. Show red before green for at least one route (recipe step 6), for example by temporarily moving `checkStepUp` back above the decode in `SetPassword`. Report the result.
5. No production code changes. If a route cannot be made to pass without one, halt and report.

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go test ./internal/handlers/ -run 'Order' -v
go test ./...
make lint
git diff --stat -- . ':!plan/'   # only internal/handlers/*_test.go
```

## Metadata

architectural_impact: false

## Assumptions

- Task 4.2 has landed. Task 4.4 must not run in parallel with this one (same package).
