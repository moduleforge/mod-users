# Self Service Shape

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

On the identities and SSH-key credential routes, run every stateless check before the step-up gate and before authorization. The step-up gate is authorization-dependent ([common rule 6](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#4-rules-all-plans)). This follows decisions D9 and D10 in the [handler inventory](../notes/handler-inventory.md#planning-decisions). The action-required envelope (`409 users.step_up_required`) and its contract are unchanged; only its position in the order moves.

## Requirements

1. **Identities** (`api/internal/handlers/identities.go`). Move the stateless checks ahead of `h.requireStepUp(...)`:
   - `Unlink`: `apiresp.ParseUUIDParam("identity_uuid", ...)`;
   - `SetPassword`: `server.Decode`, plus `new_password` required and at least 12 characters, ideally as a `Validator` that keeps `users.password_too_short` and adds a `FieldError` for the required case;
   - `StartLink`: any check that needs no server state.

   The provider registry lookup in `StartLink` is configuration state. Classify it in your report (keep it after step-up), and do not move it. `SetPassword`'s `current_password` 401 stays after step-up; it is a credential check.
2. **SSH keys** (`api/internal/handlers/ssh_keys.go`). In `RegisterSelf`, `RevokeSelf`, `RegisterForAccount` and `RevokeForAccount`, run these before `checkStepUp`:
   - path-UUID parses (`apiresp.ParseUUIDParam`; delete `parseUUIDPathParam`);
   - the body decode;
   - the `public_key` required check.

   The `isSelfTargetingOperatorCall` comparison may stay where it is (it uses only the context and the parsed UUID).
3. **SSH key parsing** (`api/internal/service/ssh_keys.go`, `Register`). Run `sshkey.Parse` and `sshkey.NormalizeLabel`, and any other stateless key/label validation, before `loadAuthorizedAccount` (D10). The duplicate-fingerprint 409 stays after authorization. Check that the order of audit observer events and transactions is unchanged.
4. **Tests.** Flip the existing assertions that expect step-up (409) or 403 ahead of a malformed body or UUID (`identities_stepup_test.go`, `identities_test.go`, `ssh_keys_test.go`, `api/internal/service/ssh_keys_test.go`). Add:
   - with step-up required and no token, a malformed body or UUID gives 400, and the step-up token store is untouched;
   - a malformed key from a caller without `update` gives 400;
   - a valid body without a token still gives 409 `users.step_up_required` with the same envelope.

   Use identifiers prefixed `selfsvc`, and grep for collisions first. Followup HHFv's note about self-route tests at `ssh_keys_test.go:710/738/759` is the starting point.
5. Append the wire changes (planned items 8 and 9) to the [wire-risk note](../notes/wire-risk.md#recorded-by-tasks).

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go vet -tags=integration ./...
go test ./...
make lint                                          # carve-out check still 4/4
grep -n 'func parseUUIDPathParam' internal/handlers/ssh_keys.go   # no hits
```

If Docker is available, run the Postgres integration suite (AGENTS.md recipe), which includes `internal/authz/ssh_keys_integration_test.go`. Otherwise say so.

## Metadata

architectural_impact: false

## Assumptions

- Tasks 3.1-3.4 have landed. Task 3.6 has not started (same package).

## Checkpoint hints

- After the identities reorder, with its tests green.
- After the SSH-key handler and service changes.
