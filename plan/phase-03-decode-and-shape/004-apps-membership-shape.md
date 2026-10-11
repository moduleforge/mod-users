# Apps Membership Shape

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

The four apps-membership handlers in `api/internal/handlers/apps.go` (`AssignUser` ~74, `ListAppUsers` ~141, `RemoveUser` ~171, `UpdateUserRoles` ~217) authorize first and only then parse `{uuid}` and decode the body. Reorder them so every stateless check runs before the nil-target `Authorize`. Then replace the shared-resolver lookup with a post-authorization `GetAppByUUID`. This follows decision D3 in the [handler inventory](../notes/handler-inventory.md#planning-decisions) and the rule in [common section 1](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#1-the-rule-and-spec-summary).

## Requirements

1. In each handler, run these before `h.az.Authorize(...)`:
   - `apiresp.ParseUUIDParam("uuid", ...)`;
   - `ParseUUIDParam("user_account_uuid", ...)` (Remove and UpdateRoles);
   - `server.Decode` (Assign and UpdateRoles). In `UpdateUserRoles` the decode moves ahead of the user lookup.
   - In `AssignUser`, a `Validator` on `assignUserRequest`: `user_uuid` required (keep code `users.user_uuid_required` and its message) and a valid UUID (`FieldError{Field: "user_uuid", Code: "invalid_uuid"}`).

   Keep the operation names and nil targets. This is not the `AuthorizeType` exception.
2. Replace the body of `loadAppByUUIDParam` with a post-authorization lookup that takes the already-parsed UUID. Use `h.coreQ.GetAppByUUID` (its `ID` is the `entities.id`); `ErrNoRows` maps to `apiresp.ErrNotFound`. Drop the `entityResolver.Resolve` call. Before switching, compare archived-app handling between `Resolve` and `GetAppByUUID` (`/Users/zane/playground/moduleforge/mod-core/model/queries/apps.sql`, `/Users/zane/playground/moduleforge/mod-core/api/entity/resolver.go`). Preserve today's observable behavior, and report it if the two differ.
3. In `NewAppsHandler`, remove `entityResolver.AllowNotFound("app")`, because it mutates the resolver shared with mod-core's services. Keep the constructor signature and the `entityResolver` field or parameter, since `moduleforge.module.yaml` and `api/handlers/handlers.go` pass it. Update the doc comments to say why it is kept, and why 404 here is legitimate (the nil-target authorize already passed).
4. Leave the body `user_uuid` miss in `AssignUser` (404 today) and the non-member Remove/UpdateRoles behavior unchanged. Task 4.1 classifies them (D15).
5. Tests in `api/internal/handlers/apps_authz_test.go`. Flip the authorize-before-resolve assertions, then cover:
   - a forbidden caller with a bad path UUID, a malformed body, or a missing or invalid `user_uuid` gets 400;
   - a forbidden caller with a valid request gets 403;
   - an admin gets 404 for an unknown app;
   - no `AllowNotFound` side effect: a fresh `entity.Resolver` passed to `NewAppsHandler` still masks a plain `Resolve` miss for slug `app` with `ErrForbidden`.

   Use identifiers prefixed `apps`, and grep for collisions first.
6. Append the wire changes (planned item 7) to the [wire-risk note](../notes/wire-risk.md#recorded-by-tasks).

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go test ./...
make lint
grep -n 'AllowNotFound\|entityResolver.Resolve' internal/handlers/apps.go   # no calls (doc mentions are fine)
grep -n 'uuid.Parse(' internal/handlers/apps.go                             # no hits
```

## Metadata

architectural_impact: false

## Assumptions

- Tasks 3.1-3.3 have landed.
