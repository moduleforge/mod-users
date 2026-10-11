# User Accounts And Self Shape

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Move every stateless check on the user-account, assume and `PUT /self` routes ahead of authorization, using `DecodeJSON` `Validator`s and `apiresp.ParseUUIDParam`. This follows decisions D4 and D5 in the [handler inventory](../notes/handler-inventory.md#planning-decisions) and the "Decode and shape" shape in [common section 6](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#6-standard-per-plan-task-template). It also handles the mod-core change at `self.go:79` and project followups N9AG and part of 1eYt.

## Requirements

1. **Create** (`api/internal/handlers/user_accounts.go`). Give `createUserAccountRequest` a `Validator` with the exact codes and messages the service uses today (`api/internal/service/user_accounts.go:218-240`):
   - email required after trim and lowercase (`users.email_required`);
   - `given_name` and `family_name` required after trim (`users.given_name_required`, `users.family_name_required`);
   - password at least 12 characters when present (`users.password_too_short`).

   Keep the service checks for in-process callers.
2. **Update.** Give `updateUserAccountRequest` a `Validator` that rejects a present-but-blank `email`, `given_name` or `family_name` (after trim) with the `users.*` codes. Optionally add the same checks in `UserAccountService.Update` for in-process callers, after its masked authorization. Record your choice.
3. **Path UUIDs.** Replace the local `parseUUIDParam` (`user_accounts.go:259`) and the `uuid.Parse` in `assume.go:61-66` with `apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))`, then delete `parseUUIDParam`. This covers `Get`, `Update`, `Delete`, `GrantAdmin`, `RevokeAdmin` and `Assume`. The detail becomes `invalid_uuid`.
4. **PUT /self** (`api/internal/handlers/self.go`). Give `selfUpdateRequest` a `Validator` that rejects a present-but-blank `given_name` or `family_name` with mod-core's codes `natural_persons.given_name_required` and `natural_persons.family_name_required` (D5). Read the exact codes and messages from `/Users/zane/playground/moduleforge/mod-core/api/service/natural_person.go`. Then verify and record, with a test each where feasible:
   - a non-admin caller updates their own names (200);
   - the behavior when the account holder is a corporation or service account and the body carries names;
   - the behavior in an assume (sudo) session.

   If a non-admin now gets 403 because `AuthorizeResolve` rejects the caller's own entity, halt and report: that would be a regression from the mod-core change.
5. **Tests.** Replace the hand-copied shim in `api/internal/handlers/user_accounts_authz_test.go` (followup N9AG) with tests through the real `UserAccountsHandler` and a real `UserAccountService` built with fakes. For `POST /user-accounts`:
   - a caller without create permission and a malformed body gets 400;
   - the same caller with a missing email gets 400;
   - the same caller with a valid body gets 403.

   Add `PUT` blank-field 400 cases and `PUT /self` blank-name 400 cases. Use identifiers prefixed `shape`, and grep for collisions first.
6. Append the wire changes (planned items 4, 5 and 6) to the [wire-risk note](../notes/wire-risk.md#recorded-by-tasks).

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go vet -tags=integration ./...
go test ./...
make lint
grep -rn 'func parseUUIDParam' internal/handlers/   # no hits
grep -n 'uuid.Parse(' internal/handlers/user_accounts.go internal/handlers/assume.go internal/handlers/self.go   # no hits
```

## Metadata

architectural_impact: false

## Assumptions

- Tasks 2.1, 3.1 and 3.2 have landed, so the service lookups are already masked and `server.Decode` runs `Validator`s.

## Checkpoint hints

- After the `Create`/`Update` `Validator`s and the UUID swap, with existing tests green.
- After `PUT /self` and the new real-handler tests.
