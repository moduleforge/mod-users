# Migrate Identities Step-Up And Last-Identity Responses

## Purpose and scope

Migrate `identities.go`'s two flat-envelope helpers — `writeStepUpRequired` (409,
`users.step_up_required`) and `writeLastIdentityError` (409, `users.last_identity`) — onto
`apiresp.WriteActionRequired` and `apiresp.WriteError(apiresp.Conflict(...))` respectively. Both
helpers currently take only `w http.ResponseWriter`; both need an `r *http.Request` parameter
threaded through from each of their call sites, since both target writers require a request value.
Depends on task `add-action-code-registry` (for `useraction.StepUpRequired`; the `last_identity`
branch uses `apiresp.Conflict` directly and has no such dependency).

## Requirements

- `writeStepUpRequired` (currently lines 648-654 in `api/internal/handlers/identities.go`): change
  the signature to `func writeStepUpRequired(w http.ResponseWriter, r *http.Request)` and replace
  the body with:
  ```go
  apiresp.WriteActionRequired(w, r, useraction.StepUpRequired,
      "Confirm your identity to continue.", "/step-up", nil)
  ```
  The message text is a deliberate generalization of the design doc's worked example ("Confirm your
  identity to remove this login method.") — this one helper backs four distinct operations
  (`SetPassword`, `RemovePassword`, `Unlink`, `StartLink`), not only identity removal, so the
  message must read correctly regardless of which operation triggered it. Keep it operation-agnostic
  rather than copying the design doc's identity-removal-specific wording verbatim.
- Update all four call sites from `writeStepUpRequired(w)` to `writeStepUpRequired(w, r)`: inside
  `StartLink` (line 203), `Unlink` (line 238), `SetPassword` (line 345), `RemovePassword`
  (line 451). `r` is already in scope at every call site (each is a `func (h *IdentitiesHandler)
  X(w http.ResponseWriter, r *http.Request)` handler method).
- `writeLastIdentityError` (currently lines 669-675): change the signature to
  `func writeLastIdentityError(w http.ResponseWriter, r *http.Request)` and replace the body with:
  ```go
  apiresp.WriteError(w, r, apiresp.Conflict(apiresp.FieldError{
      Code:    "users.last_identity",
      Message: "You can't remove your last sign-in method. Add another first.",
  }))
  ```
  per D7 — `Field` is deliberately left empty (zero value `""`); this is a message-only detail, not
  bound to a specific request field.
- Update both call sites from `writeLastIdentityError(w)` to `writeLastIdentityError(w, r)`: inside
  `Unlink` (line 293) and `RemovePassword` (line 482).
- Add imports to `identities.go`: `github.com/moduleforge/mod-users/api/internal/useraction`
  (confirm `github.com/moduleforge/core-api/apiresp` is already imported — it is, per existing
  `apiresp.WriteError`/`apiresp.ErrNotFound`/`apiresp.ErrInvalidInput` usage elsewhere in this file).
- Update the doc comment above `writeStepUpRequired` (lines 648-649, "writes the 409
  step_up_required response body. The wire format is stable — the Phase 5 GUI keys off the 'error'
  field value.") and above `writeLastIdentityError` (line 669, "writes the 409 response mandated by
  task 4.4.") to describe the new action-required / conflict-envelope mechanisms instead of the
  retired flat-envelope framing.
- Update `api/internal/handlers/identities_stepup_test.go`:
  - `stepUpGatedHandler.ServeHTTP` (around line 286) calls `writeStepUpRequired(w)` directly —
    update to `writeStepUpRequired(w, r)` (the method already receives `r *http.Request`).
  - `TestWrappedEndpoints_FlagOn_NoHeader_Returns409` (lines 292-325): change the body assertions
    from `body["error"] != "step_up_required"` / `body["challenge_path"] != "/v1/self/credential/
    step-up"` to the new action-required shape: status 409 unchanged; decode a top-level `action`
    object with `code == "users.step_up_required"`, `path == "/step-up"`, and `message` non-empty
    (the exact message text is an implementation detail of this task, not a wire contract other
    code depends on — assert non-empty rather than pinning the literal string, to avoid over-coupling
    the test to task-author wording).
- Update `api/internal/handlers/identities_test.go`'s `TestLastIdentityErrorBody` (lines 705-722):
  - It currently calls `writeLastIdentityError(rec)` with only a `*httptest.ResponseRecorder` — add
    an `httptest.NewRequest(...)`-constructed `*http.Request` argument (any method/path/body is
    fine; the value is unused by this specific write path beyond being non-nil, since
    `apiresp.WriteError` does not read from `r` for this code path — but the concrete type must be
    a real `*http.Request` for the call to compile and to match production call sites).
  - Change the assertions from `body["error"] != "last_identity"` / a flat `message` field to the
    new nested shape: status 409 unchanged; top-level `error.code == "conflict"`,
    `error.message == "the request conflicts with the current state"` (apiresp's generic conflict
    message), and `error.details == [{"field":"","code":"users.last_identity","message":"You can't
    remove your last sign-in method. Add another first."}]` (note `field` is present as an empty
    string, not omitted — `apiresp.FieldError.Field` has no `omitempty` tag).
- Sweep the rest of `identities.go`, `identities_test.go`, and `identities_stepup_test.go` for any
  other reference to the retired field names (`step_up_required`'s old top-level `error` string
  value and `challenge_path` field; `last_identity`'s old top-level `error` string value) and
  update them alongside the call sites named above.

## Validation

- Build prerequisite: worktree-local `go.work` setup (see Assumptions).
- `cd api && go build ./...` succeeds.
- `cd api && go vet ./internal/handlers/...` is clean.
- `cd api && go test ./internal/handlers/... -run 'TestWrappedEndpoints|TestLastIdentityErrorBody'`
  passes; also re-run the broader identities test files in full
  (`go test ./internal/handlers/... -run 'Identit|StepUp|Unlink|SetPassword|RemovePassword|StartLink'`,
  adjusting the pattern as needed) to catch any handler-level test exercising these two helpers
  indirectly through a full request.
- `cd api && go test ./...` (full suite) passes.
- `gofmt -l api/internal/handlers/identities.go api/internal/handlers/identities_test.go api/internal/handlers/identities_stepup_test.go`
  reports no files.
- `grep -n "func writeStepUpRequired(w http.ResponseWriter)$" api/internal/handlers/identities.go`
  returns no matches (old single-arg signature retired).
- `grep -n "func writeLastIdentityError(w http.ResponseWriter)$" api/internal/handlers/identities.go`
  returns no matches (old single-arg signature retired).
- `grep -n '"challenge_path"' api/internal/handlers/identities*.go` returns no matches.
- `grep -rn '"error":\s*"step_up_required"\|"error":\s*"last_identity"' api/internal/handlers/identities*.go`
  returns no matches.

## Metadata

architectural_impact: true

## Checkpoint hints

- After migrating `writeStepUpRequired` and its four call sites.
- After migrating `writeLastIdentityError` and its two call sites.
- After updating `identities_stepup_test.go` and `identities_test.go` and re-running the full test
  suite.

## Assumptions

- Task `add-action-code-registry` has landed so `useraction.StepUpRequired` is importable
  (`writeLastIdentityError`'s `apiresp.Conflict` path has no such dependency and could technically
  proceed independently, but both helpers live in the same file and are migrated together here).
- Build prerequisite: worktree-local `go.work` recipe per
  `docs/mf-standards/building-common.md` § "Building inside a task worktree".
- `/step-up` is the resolved path value (Answer, Reading A) — requiring the consuming apps to mount
  that route is a cross-repo coordination item flagged for the manager, out of scope here.
  `users.step_up_required` currently has no reachable GUI call site in this repo (per the notes'
  "GUI call-site reachability finding" — `gui/src/lib/api.ts` exposes no identities/credential/
  step-up client methods) — this does not change this task's scope; the backend contract still
  migrates regardless of current GUI reachability.

## References

- [Action-path values and decisions](../notes/action-path-values-and-decisions.md) — decision D7,
  the Answer section, and the "GUI call-site reachability finding" section.
- Design doc: `docs/mf-standards/architecture/api-response-design.md` — "The `action` object"
  worked example for `users.step_up_required` (message text is illustrative/generalized here, not
  copied verbatim, since one helper serves four different operations).
- `api/internal/handlers/identities.go:648-675` (the two helpers) and `:200-345,440-490` (call
  sites) — the sites this task migrates.
- `api/internal/handlers/identities_stepup_test.go:275-333`,
  `api/internal/handlers/identities_test.go:704-722` — the tests this task updates.
