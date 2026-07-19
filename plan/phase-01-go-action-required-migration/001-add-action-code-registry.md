# Add Action-Code Registry

## Purpose and scope

Introduce a new internal Go package, `api/internal/useraction`, that declares mod-users' three
registered `apiresp.ActionCode` values for this migration wave. Per the design doc
(`docs/mf-standards/architecture/api-response-design.md`, "Action-code vocabulary"): "the closed
set of action codes and their bound statuses is owned per-module, in each module's own API
reference" — `mod-core/api/apiresp` deliberately holds no registry or validation logic of its own.
This task is the shared foundation the other Go migration tasks in this phase (`require_verified.go`,
`require_confirmed.go`, `identities.go`) import, so the three codes are declared once rather than
re-stated as inline literals at each call site. This is a self-contained addition: no other file is
modified, and nothing imports the new package yet (the follow-on tasks wire it in).

## Requirements

- Create `api/internal/useraction/action_codes.go` in package `useraction`, importing
  `github.com/moduleforge/core-api/apiresp`.
- Export three package-level `apiresp.ActionCode` values, exactly:
  - `EmailUnverified = apiresp.ActionCode{Code: "users.email_unverified", Status: http.StatusForbidden}`
  - `StepUpRequired = apiresp.ActionCode{Code: "users.step_up_required", Status: http.StatusConflict}`
  - `OIDCNotConfirmed = apiresp.ActionCode{Code: "users.oidc_not_confirmed", Status: http.StatusServiceUnavailable}`
- Use `net/http` status constants (not bare integer literals), matching this codebase's existing
  convention (e.g. `require_verified.go`'s `http.StatusForbidden` / `http.StatusInternalServerError`).
- Add a package doc comment explaining the package's ownership role — why this small registry
  exists as its own package rather than `apiresp.ActionCode` literals declared inline at each
  call site — mirroring the design doc's per-module-ownership statement.
- Add `api/internal/useraction/action_codes_test.go` with a table-driven test asserting each
  exported value's `.Code` and `.Status` fields against the literal strings/statuses above.
- Do not add a runtime registry/lookup map, a validation function, or any other mechanism beyond
  the three plain value declarations — `apiresp` itself performs no registry validation, and the
  design doc does not call for one here. Keep this package minimal.

## Validation

- Build prerequisite: run the worktree-local `go.work` setup first (see Assumptions) before any Go
  command below.
- `cd api && go build ./...` succeeds.
- `cd api && go vet ./internal/useraction/...` is clean.
- `cd api && go test ./internal/useraction/...` passes.
- `gofmt -l api/internal/useraction/` reports no files.
- `go build ./...` for the whole `api` module succeeds unchanged (this task is additive-only; no
  other package imports `useraction` yet).

## Metadata

architectural_impact: true

## Assumptions

- Build prerequisite: before running any `go build`/`go vet`/`go test`, follow the worktree-local
  `go.work` recipe in `docs/mf-standards/building-common.md` § "Building inside a task worktree"
  (a gitignored `go.work` at the worktree root with `use` directives for the sibling
  `mod-core/api`/`mod-core/model` checkouts, three `../` up from the worktree root). No committed
  `go.mod`/`go.sum` change results from this task — `api/go.mod`'s `core-api` dependency is a
  local-path `replace`, not a version pin.
- `github.com/moduleforge/core-api/apiresp` already exports `ActionCode{Code string; Status int}`
  (verified present in the sibling `mod-core/api/apiresp` checkout as of this plan's research) —
  no `apiresp` changes are needed or in scope; `mod-core/api/apiresp` itself is a different repo,
  off limits to this plan.
- No dependency on any other task in this phase — this is the first task other tasks depend on.

## References

- [Action-path values and decisions](../notes/action-path-values-and-decisions.md) — decision D4
  (registry ownership and the exact three `ActionCode` values), and the Answer section (path values,
  for context on why these three codes exist).
- Design doc: `docs/mf-standards/architecture/api-response-design.md` — "Action-code vocabulary"
  section (the `users.email_unverified` / `users.step_up_required` / `users.oidc_not_confirmed`
  table with bound statuses) and the `ActionCode` struct definition under "Go-layer ownership".
- `mod-core/api/apiresp/action.go` — the `ActionCode` struct and `WriteActionRequired` function
  this registry's values feed into (read-only reference; not modified by this plan).
