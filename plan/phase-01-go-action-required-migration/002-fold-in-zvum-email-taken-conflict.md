# Fold In ZVum Email Taken Conflict

## Purpose and scope

Close out followup `ZVum` (the constructor-gap observation originally raised against
`writeServiceError`/`svc.ErrEmailTaken`) by moving the `users.email_taken` field detail into the
sentinel itself, via the now-available `apiresp.Conflict(...)` constructor, and collapsing
`writeServiceError` to a plain `apiresp.WriteError(w, r, err)` call with no local envelope
construction. Scope: `api/internal/service/user_accounts.go` (`ErrEmailTaken` definition) and
`api/internal/handlers/user_accounts.go` (`writeServiceError`), plus the tests that exercise the
email-taken response body. This task has no dependency on the action-code registry task and can
run in parallel with every other task in this phase.

## Pre-implementation state (re-verified 2026-07-20, after main rebase)

This task is **partially, but not fully, satisfied on `main`**. Commit `d91b699` ("Use apiresp.Conflict
for email_taken 409 response") — landed independently of this plan — already did the *handler-side*
mechanism swap: `writeServiceError` no longer hand-builds an `apiresp.Envelope{}`/`apiresp.WriteJSON`
literal; its `svc.ErrEmailTaken` branch now calls
`apiresp.WriteError(w, r, apiresp.Conflict(apiresp.FieldError{Field:"email", Code:"users.email_taken",
Message:"email is already registered"}))`, and the stale "no such constructor exists" comment is gone.
So the wire output is already correct (`grep "apiresp.Envelope{"` is already clean) and the two
handler-level tests (`user_accounts_authz_test.go` `email_taken` case + `TestShim_Create_EmailTaken`)
already pass.

**Still outstanding (the actual mechanical crux of the ZVum fold-in — do these):**
1. `svc.ErrEmailTaken` is **not** yet redefined — it is still
   `var ErrEmailTaken = fmt.Errorf("%w: email already registered", apiresp.ErrConflict)` at
   `api/internal/service/user_accounts.go:108`, carrying no field detail. Redefine it via
   `apiresp.Conflict(...)` per the requirement below.
2. `writeServiceError` still has the `errors.Is(err, svc.ErrEmailTaken)` **special case** (it only
   changed *how* that branch builds the response, not that it exists). Collapse it to a plain
   pass-through so email-taken flows through the same `apiresp.WriteError(w, r, err)` as everything
   else — which is only correct *after* outstanding item 1 moves the detail onto the sentinel.
3. The doc comments above `ErrEmailTaken` (`user_accounts.go:96-107`) and `writeServiceError`
   (`handlers/user_accounts.go:41-51`) still describe the old "detail attached at the handler mapping
   point / no public detail-carrying constructor" framing — both are now stale and must be revised.
4. The service-level direct test (`TestErrEmailTaken_ConflictDetail`) does **not** exist yet — only
   `user_accounts_anon_test.go:59`'s non-wrapping identity check references `ErrEmailTaken`. Add it.

The requirement prose below still describes the correct end state; note only that the handler-side
`WriteJSON`/`Envelope` construction it says to remove is *already* removed by `d91b699` — the
remaining handler work is collapsing the special-case branch, not deleting a `WriteJSON` call.

## Requirements

- In `api/internal/service/user_accounts.go`, redefine `ErrEmailTaken` (currently
  `var ErrEmailTaken = fmt.Errorf("%w: email already registered", apiresp.ErrConflict)`, line 108)
  to:
  ```go
  var ErrEmailTaken = apiresp.Conflict(apiresp.FieldError{
      Field:   "email",
      Code:    "users.email_taken",
      Message: "email is already registered",
  })
  ```
  Keep it a package-level singleton (as today) so identity-based `errors.Is` checks elsewhere
  continue to compile and behave the same: `apiresp.Conflict(...)`'s returned `*conflictError`
  implements `Unwrap() error { return apiresp.ErrConflict }`, so `errors.Is(err, apiresp.ErrConflict)`
  classification is unchanged, and `errors.Is(ErrAnonymousAccount, ErrEmailTaken)` in
  `user_accounts_anon_test.go:59` still resolves to `false` by the same identity-based reasoning as
  today (no `Is` method is defined on `conflictError`, so `errors.Is` falls back to `==`/`Unwrap`
  chain comparison against the single package-level `ErrEmailTaken` variable).
- Revise the doc comment above `ErrEmailTaken` (lines 96-107): it currently explains why the
  field-level detail is attached at the handler mapping point (`writeServiceError`) rather than at
  the sentinel. Replace that rationale — the detail is now carried directly on the sentinel via
  `apiresp.Conflict(...)`, so `writeServiceError` needs no special case at all.
- In `api/internal/handlers/user_accounts.go`, collapse `writeServiceError` (lines 41-70) to:
  ```go
  func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
      apiresp.WriteError(w, r, err)
  }
  ```
  removing the `errors.Is(err, svc.ErrEmailTaken)` special case and its hand-built
  `apiresp.WriteJSON(w, http.StatusConflict, apiresp.Envelope{...})` call entirely — the
  `Envelope`/`ErrorBody`/`FieldError` composite literal that duplicated `apiresp`'s own
  `publicMessage("conflict")` string goes away. If, after this collapse, `writeServiceError` is
  merely a one-line pass-through to `apiresp.WriteError`, either keep it (for call-site readability
  and future extensibility) or inline it at call sites and remove it — check how many call sites
  reference `writeServiceError` by name before deciding, and prefer the option that reads more
  clearly; either is acceptable.
- Revise the doc comment above `writeServiceError` (lines 41-55): it currently explains at length
  why the email-taken case needed special handling; that rationale is now obsolete. Replace it with
  a short comment (or remove the function-level comment if the function is inlined away).
- The wire output must not change: status 409, top-level `error.code: "conflict"`, top-level
  `error.message: "the request conflicts with the current state"` (apiresp's generic conflict
  message — it happens to already match the current hand-written value byte-for-byte), and
  `error.details: [{"field":"email","code":"users.email_taken","message":"email is already registered"}]`.
- `api/internal/handlers/user_accounts_authz_test.go` already contains a table-driven case (around
  line 567, name `"email_taken"`) asserting exactly this shape (`wantStatus: http.StatusConflict`,
  `wantCode: "conflict"`, `wantDetails: []apiresp.FieldError{{Field:"email", Code:"users.email_taken",
  Message:"email is already registered"}}`) — confirm it still passes unmodified after the fold-in;
  do not weaken or remove this assertion. `user_accounts_authz_test.go:354` (`TestShim_Create_EmailTaken`)
  also exercises this path end-to-end through the handler; confirm it still passes.
- Add a small direct test in `api/internal/service/` (e.g. a new `TestErrEmailTaken_ConflictDetail`
  in `user_accounts_test.go`, or alongside `user_accounts_anon_test.go`) asserting, at the point of
  definition rather than only transitively through the handler test:
  - `errors.Is(ErrEmailTaken, apiresp.ErrConflict)` is `true`.
  - The field detail recoverable from `ErrEmailTaken` (via `errors.As` against `apiresp`'s
    detail-carrying interface, or by re-deriving it through `apiresp.WriteError` into an
    `httptest.ResponseRecorder` and decoding the body — whichever is simpler given what `apiresp`
    exposes publicly) matches `{Field:"email", Code:"users.email_taken", Message:"email is already
    registered"}` exactly.
  - This is the mechanical crux of the ZVum fold-in (the sentinel itself, not just its
    downstream rendering), so it deserves its own direct assertion even though the handler-level
    table test above already covers the end-to-end wire shape.
- Sweep `api/internal/handlers/user_accounts.go` and `api/internal/service/user_accounts.go` for
  any other comment referencing the old "attached at the mapping point" framing (e.g.
  `oidc_providers.go:220`'s comment mirrors this precedent — that file is out of this task's scope,
  but do not edit it; a stale cross-reference comment there pointing at the old
  `writeServiceError` pattern is acceptable to leave, since `oidc_providers.go` itself is untouched
  by this plan).

## Validation

- Build prerequisite: worktree-local `go.work` setup (see Assumptions).
- `cd api && go build ./...` succeeds.
- `cd api && go vet ./internal/service/... ./internal/handlers/...` is clean.
- `cd api && go test ./internal/service/... ./internal/handlers/...` passes, including
  `user_accounts_anon_test.go`'s sentinel-identity test and `user_accounts_authz_test.go`'s
  `email_taken` table case and `TestShim_Create_EmailTaken`.
- `cd api && go test ./...` (full suite) passes — no untouched-path regressions.
- `gofmt -l api/internal/service/user_accounts.go api/internal/handlers/user_accounts.go` reports
  no files.
- `grep -n "apiresp.Envelope{" api/internal/handlers/user_accounts.go` returns no matches (confirms
  the hand-built envelope construction is gone).
- Manual check: inspect the JSON body produced by the updated `email_taken` test case (or a quick
  ad hoc handler invocation) to confirm it is byte-for-byte identical to the pre-migration shape.

## Metadata

architectural_impact: true

## Checkpoint hints

- After redefining `ErrEmailTaken` in `api/internal/service/user_accounts.go`.
- After collapsing `writeServiceError` in `api/internal/handlers/user_accounts.go`.
- After adding/confirming the service-level sentinel test and re-running the full test suite.

## Assumptions

- Build prerequisite: worktree-local `go.work` recipe per
  `docs/mf-standards/building-common.md` § "Building inside a task worktree".
- No dependency on the `add-action-code-registry` task — this task uses `apiresp.Conflict` /
  `apiresp.WriteError` directly and can run fully in parallel with every other task in this phase.

## References

- [Action-path values and decisions](../notes/action-path-values-and-decisions.md) — decision D5.
- `mod-core/api/apiresp/conflict.go` — the `Conflict(...)` constructor and `conflictError` type
  (read-only reference; not modified by this plan).
- `api/internal/service/user_accounts.go:96-108`, `api/internal/handlers/user_accounts.go:41-70` —
  the current sites this task migrates.
- `api/internal/handlers/user_accounts_authz_test.go:354,567` — existing tests already pinned to
  the target wire shape.
