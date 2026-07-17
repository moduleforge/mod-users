# Migrate self.go, assume.go, and user_accounts.go server.Error Sites

## Purpose and scope

Collapse the 13 literal `server.Error(...)` call sites across
`api/internal/handlers/self.go` (6 sites), `api/internal/handlers/assume.go` (3 sites),
and `api/internal/handlers/user_accounts.go` (4 sites) onto sentinel-driven
`apiresp.WriteError(w, r, err)` calls. One of four parallel tasks closing followup
`8iRl`. These three files are grouped because each has only a few literal sites and
none is touched by another task. **No carve-out sites** in this group.

Read [`plan/overview.md`](../overview.md) and the
[server.Error site inventory](../notes/server-error-site-inventory.md) first — the
inventory has the exact per-site line list, category tags, and the load-bearing
behavior note. This task owns the `self.go` (lines 35–93), `assume.go` (lines 44–62),
and `user_accounts.go` (lines 84–270) sections.

## Requirements

### Transform rules (apply per the inventory's category tags)

1. **Category 1 — `internal_error` 500 sites** (`self.go`: 35, 41, 67, 75, 93;
   `assume.go`: 62): replace `server.Error(w, http.StatusInternalServerError,
   "internal_error", "<msg>")` with `apiresp.WriteError(w, r, err)` where `err` is the
   in-scope underlying error wrapped with the op label (`fmt.Errorf("self.Get: %w",
   err)`), or `errors.New("<msg>")` when no error value is in scope. Remove any
   immediately-preceding `slog.ErrorContext` that logs only this same failure
   (WriteError logs all 5xx with request context); fold distinct structured context
   into the wrapped error.

2. **Category 2 — 4xx sentinel, no details** (`self.go`: 61; `assume.go`: 44, 51;
   `user_accounts.go`: 84, 160, 195, 270): replace with
   `apiresp.WriteError(w, r, <sentinel>)` — `apiresp.ErrInvalidInput` for the
   `invalid_input` sites, `apiresp.ErrNotFound` for the `not_found` sites (`assume.go:51`,
   `user_accounts.go:160`). Messages genericize; do not invent detail codes.

There are **no Category 3 or Category 4** sites in this group.

### Important — do NOT modify the existing shared helpers in these files

- `user_accounts.go` **defines** `writeServiceError` (line ~56), including its
  documented `svc.ErrEmailTaken` conflict carve-out — **do not touch it**. Only migrate
  the four literal `server.Error` sites (84, 160, 195, 270).
- `self.go` **defines** `writeCoreServiceErr` (line ~138), which already delegates to
  `apiresp.WriteError` — **do not touch it**. Only migrate the six literal sites.
- Existing `writeServiceError(w, r, err)` / `writeCoreServiceErr(w, r, err)` /
  `writeAuthzError(w, r, err)` call sites are already centralized — leave them.

### Constraints

- Preserve HTTP status codes and `error.code`/`details` exactly; only top-level
  `error.message` text genericizes.
- Do not touch other handler files, `server/respond.go`, or `mod-core/api/apiresp`.
- `server.Decode`/`server.JSON` remain in use in these files, so the `server` import
  stays. Add/keep `fmt`/`errors` as needed. `gofmt`/`goimports` clean; wrap with `%w`.
- Note `user_accounts.go:160` is inside an `if err == pgx.ErrNoRows` branch returning a
  plain 404 — replace with `apiresp.WriteError(w, r, apiresp.ErrNotFound)` (keep the
  surrounding `pgx.ErrNoRows` check as-is).

### Verification that the migration is complete

- After editing, `grep -n "server\.Error\|server\.ErrorWithDetails"` returns **zero**
  matches in each of `self.go`, `assume.go`, and `user_accounts.go` (no carve-outs in
  this group; the `writeServiceError` helper uses `apiresp.WriteJSON`, not
  `server.Error`, so it will not appear).

## Validation

- From `api/`: `make build.api` compiles clean.
- From `api/`: `make test.unit` passes. Check `self_routes_test.go` and
  `user_accounts_authz_test.go` (the latter exercises `writeServiceError`'s mapping
  table and the `pgx.ErrNoRows` → 404 path); they assert on status codes and
  `error.code`/`details`. Update only assertions checking an old bespoke top-level
  message string; the `writeServiceError` mapping-table test must remain unchanged and
  passing (that helper is not modified).
- `gofmt -l self.go assume.go user_accounts.go` reports no diffs; `go vet` clean.

## Metadata

architectural_impact: true

## References

- [server.Error site inventory](../notes/server-error-site-inventory.md).
- `docs/mf-standards/architecture/api-response-design.md` — "Go-layer ownership".
- `api/internal/handlers/user_accounts.go:56` (`writeServiceError`) and
  `api/internal/handlers/self.go:138` (`writeCoreServiceErr`) — Phase-1 helpers to
  leave untouched.
- `mod-core/api/apiresp/writer.go`, `errors.go` (sentinels), `invalidinput.go`.
</content>

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-17
- **Validation summary:** `make build.api` compiles clean (run from worktree root);
  `make test.unit` passes (all `api/` packages, including
  `internal/handlers/self_routes_test.go` and
  `internal/handlers/user_accounts_authz_test.go`); `gofmt -l` and `goimports -l`
  report no diffs on `self.go`/`assume.go`/`user_accounts.go`; `go vet ./...` clean.
  `grep -n "server\.Error\|server\.ErrorWithDetails"` returns zero matches in all
  three files.
- **Affected source files:**
  - `api/internal/handlers/self.go`
  - `api/internal/handlers/assume.go`
  - `api/internal/handlers/user_accounts.go`
- **Assumptions applied:** none beyond the task doc's explicit transform rules; no
  `## Assumptions` section was present on this task doc.
- **Decisions made:**
  - Used method-scoped op labels for Category-1 wraps (`self.Get`, `self.Put`,
    `assume.Assume`) per the task doc's `self.Get: %w` example, applied uniformly to
    every 500 site within that method rather than a finer per-call-site label.
  - Removed the `slog.ErrorContext(r.Context(), "assume: issue jwt", "error", err)`
    line preceding `assume.go:62` — it logged only the same failure `WriteError`
    now logs server-side, with no distinct structured context to preserve.
  - No existing test asserted on the old bespoke top-level message strings for any
    of the 13 migrated sites (spot-checked `self_routes_test.go` and
    `user_accounts_authz_test.go`), so no test assertions needed updating;
    `TestWriteServiceError_Mapping` and the `pgx.ErrNoRows` → 404 shim path were
    left untouched and still pass.
