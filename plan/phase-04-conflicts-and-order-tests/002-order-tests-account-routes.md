# Order Tests Account Routes

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Build the shared order-test harness for `api/internal/handlers`, and cover the 12 routes of `RegisterAccountRoutes` with `ordertest.Run`:

- the 7 user-account routes;
- assume;
- the 4 apps-membership routes.

Follow the recipe in [common section 5](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#5-order-test-recipe-apirespordertest) and decision D14 in the [handler inventory](../notes/handler-inventory.md#planning-decisions). Working examples:

- `/Users/zane/playground/moduleforge/mod-audit/api/httpapi/order_test.go`: fakes that embed the querier interfaces;
- `/Users/zane/playground/moduleforge/mod-core/api/httpapi/order_helpers_test.go`, together with the `*_order_test.go` files beside it.

## Requirements

1. Create `api/internal/handlers/order_helpers_test.go`. It is the only file that declares shared order helpers, and every identifier in it is prefixed `order`. Before adding anything, grep the package for collisions (`grep -rn 'order[A-Z]' internal/handlers/*_test.go`). It contains:
   - `orderAuthorizer`. It allows the owner or admin actor and denies the stranger. It denies target 0 quietly (common rule 5) and records calls. It implements `AuthorizeType` for the type-level routes.
   - Fake `db.Querier` and `coredb.Querier` (embed the interface; implement only what is used), seeded with one existing user account and its holder entity, one app, and one stranger actor.
   - A fake `txhelper.DB`, a `types.Resolver` and an observer group.
   - The `orderUserContext` shim from D14. When `opctx` carries an effective actor, it installs a matching verified `localauth.UserContext`. With no actor it does nothing, so `RequireAuth` still answers 401.
   - `orderAccountRouter()`. It wires the real `svc.NewUserAccountService(...)`, `NewUserAccountsHandler` (with grant/revoke closures shaped like `api/cmd/server/main.go:464-496`), `NewAssumeHandler` and `NewAppsHandler` into the real `RegisterAccountRoutes` under `/v1`, behind `orderUserContext` and `auth.RequireVerifiedEmail`, the way the manifest composes them. It installs no token-verifying middleware, because `ordertest` sets the identity.
2. Create `api/internal/handlers/account_routes_order_test.go`, with one `ordertest.Route` per route:
   - Use `BadBody` for decode-layer rejections, with an extra entry per distinct `Validator` rule.
   - Set `AuthorizedMissingStatus: 403` for the user-account `{uuid}` routes and assume (masked, D2).
   - Use the default 404 for the apps-membership missing app (nil-target authorization first, D3).
   - Put task 4.1's outcomes in `Late`: duplicate email 409 on update, unknown body `user_uuid` 422.
   - Routes without a path id (list, create) follow recipe step 3. Use plain table tests where `ordertest`'s assertions do not fit, and say so in the audit table.
3. Add one recording-authorizer test proving that a masked user-account miss calls `Authorize(op, 0)` exactly once.
4. Show the tests are red before the fix (recipe step 6). For example, temporarily move a decode after authorization in `apps.go`, or reinstate the `Create` special case; confirm the expected failures, revert, and report what you saw.
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

- Task 4.1 has landed. Tasks 4.3 and 4.4 reuse this task's helpers and must not run in parallel with it (same package).

## Checkpoint hints

- After `order_helpers_test.go` compiles and one route passes.
- After all 12 routes pass.
