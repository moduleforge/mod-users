# Migrate apps.go server.Error Sites

## Purpose and scope

Collapse the 27 literal `server.Error(...)` / `server.ErrorWithDetails(...)` call
sites in `api/internal/handlers/apps.go` onto sentinel-driven
`apiresp.WriteError(w, r, err)` calls, so the status/code/envelope decision is derived
from the shared `mod-core/api/apiresp` classification point rather than restated as
literals at each site. This is one of four parallel tasks (split by handler file)
closing followup `8iRl`. `apps.go` has **no carve-out sites** — every site collapses.

Read [`plan/overview.md`](../overview.md) and the
[server.Error site inventory](../notes/server-error-site-inventory.md) first — the
inventory has the exact per-site line list and the load-bearing behavior note. This
task owns the `apps.go` section of that inventory (lines 55–499).

## Requirements

### Transform rules (apply per the inventory's category tags for apps.go)

1. **Category 1 — `internal_error` 500 sites** (lines 101, 132, 227, 268, 324, 339,
   366, 407, 416, 454, 473, 499): replace
   `server.Error(w, http.StatusInternalServerError, "internal_error", "<msg>")` with
   `apiresp.WriteError(w, r, err)`, where `err` is the underlying error already in
   scope in that `if err != nil` block, wrapped with the op label so the single
   server-side log line stays useful — e.g.
   `apiresp.WriteError(w, r, fmt.Errorf("apps.Create: %w", err))`. If no error value
   is in scope, construct one: `apiresp.WriteError(w, r, errors.New("<msg>"))`.
   Remove any **immediately-preceding `slog.ErrorContext` that logs only this same
   failure** — `apiresp.WriteError` logs all 5xx with request context via its own
   `logServerError`, so the manual log is redundant; fold any distinct structured
   fields (`"id", id`, etc.) into the wrapped error instead of dropping them.

2. **Category 2 — 4xx sentinel, no details** (lines 55, 179, 301, 313, 319, 396, 402,
   443, 449, 460, 489, 494): replace with `apiresp.WriteError(w, r, <sentinel>)` using
   the matching sentinel — `apiresp.ErrInvalidInput` for the `invalid_input` sites and
   `apiresp.ErrNotFound` for the `not_found` sites. The bespoke top-level message is
   intentionally dropped in favour of `apiresp`'s generic per-code message. **Do not**
   invent new `details[]` codes to preserve messages.

3. **Category 3 — `invalid_input` WITH details** (lines 61, 67, 305): replace
   `server.ErrorWithDetails(w, http.StatusBadRequest, "invalid_input", msg, details)`
   with `apiresp.WriteError(w, r, apiresp.InvalidInput(details...))`. Pass the exact
   same `apiresp.FieldError` values through — `InvalidInput` preserves them in
   `error.details` byte-for-byte. (`details` is currently a `[]apiresp.FieldError`
   literal; spread it or pass the elements directly to the variadic `InvalidInput`.)

### Constraints

- **Preserve HTTP status codes and `error.code`/`details` exactly** at every site;
  only the top-level `error.message` text may change (it genericizes).
- Do **not** modify the shared helpers (`writeAuthzError`, `writeServiceError`) or any
  `apiresp.WriteError(...)` / `writeAuthzError(...)` calls that already exist in
  `apps.go` — only the literal `server.Error`/`server.ErrorWithDetails` sites.
- Do **not** touch any other handler file; do not touch `server/respond.go` or
  `mod-core/api/apiresp`.
- Keep imports tidy: `apiresp` is already imported; add/keep `fmt` and `errors` as the
  transforms require, and drop the `server` import only if it becomes entirely unused
  in the file (`server.Decode`/`server.JSON` are still used, so it will remain).
- Follow the Go design/style standards: `gofmt`/`goimports` clean, wrap errors with
  `%w`, no needless allocations.

### Verification that the migration is complete

- After editing, `grep -n "server\.Error\|server\.ErrorWithDetails" apps.go` returns
  **zero** matches (apps.go has no carve-outs).

## Validation

- From `api/`: `make build.api` compiles clean.
- From `api/`: `make test.unit` passes. `apps.go` has no dedicated `*_test.go`, but run
  the full unit suite; update any test that asserts an old bespoke top-level message
  string to the generic `apiresp` message (status-code and `error.code`/`details`
  assertions must remain unchanged and passing).
- `gofmt -l apps.go` reports no formatting diffs; `go vet ./...` is clean for the
  package.
- `grep -c "server\.Error" api/internal/handlers/apps.go` returns `0`.

## Metadata

architectural_impact: true

## References

- [server.Error site inventory](../notes/server-error-site-inventory.md) — per-site
  list and transform rules.
- `docs/mf-standards/architecture/api-response-design.md` — "Go-layer ownership"
  (the `apiresp.WriteError` end-state this task realizes).
- `api/internal/handlers/user_accounts.go:56` (`writeServiceError`) and
  `api/internal/handlers/errors.go` (`writeAuthzError`) — the Phase-1 precedent for
  routing through `apiresp.WriteError`.
- `mod-core/api/apiresp/writer.go` (`WriteError`, `classify`, `publicMessage`,
  `logServerError`) and `invalidinput.go` (`InvalidInput`).
</content>
