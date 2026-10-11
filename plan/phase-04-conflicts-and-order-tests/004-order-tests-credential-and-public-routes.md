# Order Tests Credential And Public Routes

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Add dedicated order tests for the routes `ordertest` cannot model:

- the 8 `/oidc-config` routes, which are public or credential-authenticated by admin bearer or setup token;
- the 10 public `/auth` routes.

Follow recipe step 7 in [common section 5](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#5-order-test-recipe-apirespordertest). These tests pin the module-specific order, including the documented D8 exception in the [handler inventory](../notes/handler-inventory.md#planning-decisions), and prove that no public route got `RequireAuth` (common rule 2).

## Requirements

1. Create `api/internal/handlers/oidc_config_order_test.go`, with helpers prefixed `orderOIDC`; grep for collisions first. Mount the real `RegisterOIDCConfigRoutes` with test handlers. For each route, assert:
   - **public routes** (status, saved, setup-token): an anonymous request is not 401, and keeps today's status;
   - **credential routes**, through the real router:
     - no credential with a well-formed body gives 401;
     - a header-token or admin caller with a malformed body gives 400;
     - no header or admin with a malformed body gives 400 (documented exception, with a comment);
     - a valid credential with an invalid id gives 400;
     - a valid credential with a valid request reaches the handler;
     - an existing id on create gives 409 with `users.oidc_provider_exists`.
2. Create `api/internal/handlers/auth/order_test.go` (separate package), with helpers prefixed `orderAuth`. Mount the real `authhandlers` `RegisterRoutes` with test dependencies, and with no `RequireAuth` and no identity. For each of the 10 routes, assert:
   - an anonymous request is never 401 because of authentication middleware;
   - a malformed body or unknown field gives 400 `invalid_input`;
   - credential failures (login, email-code verify, password-reset confirm) give 401 with their preserved messages;
   - the 202 anti-enumeration responses are unchanged.
3. Also assert, through the composed router, that the mod-users registrars of task 1.2 do not wrap these two route groups. An anonymous `GET /v1/auth/providers` gives 200.
4. Show red before green for at least one assertion (recipe step 6), for example by temporarily putting the body decode back before the header credential check in `Confirm`. Report the result.
5. No production code changes. If a route cannot be made to pass without one, halt and report.

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go test ./internal/handlers/... -run 'Order' -v
go test ./...
make lint
git diff --stat -- . ':!plan/'   # only *_test.go under internal/handlers/
```

## Metadata

architectural_impact: false

## Assumptions

- Task 4.3 has landed.
