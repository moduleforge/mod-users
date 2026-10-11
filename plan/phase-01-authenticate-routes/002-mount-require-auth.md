# Mount Require Auth

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Make 401 the first rejection on every authenticated mod-users route, whatever the composer does. Mount mod-core `authz.RequireAuth` (`/Users/zane/playground/moduleforge/mod-core/api/authz/middleware.go`) inside the 8 authenticated registrars. Placement follows decision D1 in the [handler inventory](../notes/handler-inventory.md#planning-decisions) and the `RequireAuth` row of [common section 3](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#3-helper-reference-final-signatures). Public and credential routes are untouched (common rule 2).

## Requirements

1. In each of the 8 registrars named in D1 (`api/internal/handlers/account_routes.go`, `self_routes.go`, `self_identities_routes.go`, `ssh_keys_routes.go`), wrap the route registrations in `r.Group(func(r chi.Router) { r.Use(coreAuthz.RequireAuth); ... })`. Never call `Use` on the router passed in. Keep every signature and the facade `api/handlers/handlers.go` unchanged.
2. Do not touch `authhandlers.RegisterRoutes`, `RegisterOIDCConfigRoutes`, `moduleforge.module.yaml` (no `scope:` change; D1) or `api/cmd/server/main.go`.
3. Add tests that go through the real registrars, mounted on a chi router the way mfgen composes them (a `/v1` route, optionally a shared router carrying a second registrar). A table is fine. Cover:
   - every route: anonymous with a valid request gets 401;
   - every body or UUID route: anonymous with a malformed body or path UUID gets 401, not 400;
   - a sudo-only identity (`opctx.WithSudoActor` only) is not 401. Handlers call `auth.MustFromContext`, so install a matching `UserContext` in the test (or recover handler panics as the mod-core sibling test did) and say which you chose.

   Show that the anonymous bad-body case is red before the mount: run it once against the unmounted registrars and report the result.
4. Update the registrar doc comments, which say the caller supplies `requireAuth`. They should now say that the registrar also enforces `RequireAuth` itself as defense in depth, and that the module's token-verifying `requireAuth` must still run first in production.
5. Confirm by grep that there are no inline 401 checks in `api/internal/handlers` to remove (D1). If you find any, report them and do not delete them here.
6. Append the change to the [wire-risk note](../notes/wire-risk.md#recorded-by-tasks).

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go vet -tags=integration ./...
go test ./...
make lint
grep -c 'r.Use(coreAuthz.RequireAuth)' internal/handlers/account_routes.go internal/handlers/self_routes.go internal/handlers/self_identities_routes.go internal/handlers/ssh_keys_routes.go   # 1, 2, 2, 3 (8 total), each inside r.Group
grep -n 'Use(' internal/handlers/oidcconfig_routes.go internal/handlers/auth/routes.go   # no hits
git diff --stat -- . ':!plan/'   # only internal/handlers/*_routes.go and new or updated *_test.go
```

## Metadata

architectural_impact: false

## Assumptions

- Task 1.1 has landed and confirmed D1.
- chi panics if `Use` is called after routes are registered on the same mux; registering inside `Group` avoids that.

## Checkpoint hints

- After the 8 registrars are wrapped and the existing suite is green.
- After the new 401 and sudo-only tests.
