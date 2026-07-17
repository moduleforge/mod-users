# Migrate oidc_providers.go and oidc_config.go server.Error Sites

## Purpose and scope

Collapse the 33 literal `server.Error(...)` call sites across
`api/internal/handlers/oidc_providers.go` (20 sites) and
`api/internal/handlers/oidc_config.go` (13 sites) onto sentinel-driven
`apiresp.WriteError(w, r, err)` calls. One of four parallel tasks closing followup
`8iRl`. These two files are grouped because they form the OIDC admin/setup surface and
are edited by no other task. **One carve-out site**: `oidc_providers.go:218`.

Read [`plan/overview.md`](../overview.md) and the
[server.Error site inventory](../notes/server-error-site-inventory.md) first — the
inventory has the exact per-site line list, category tags, and the load-bearing
behavior note. This task owns the `oidc_providers.go` (lines 133–411) and
`oidc_config.go` (lines 188–434) sections.

## Requirements

### Transform rules (apply per the inventory's category tags)

1. **Category 1 — `internal_error` 500 sites** (`oidc_providers.go`: 139, 222, 245,
   251, 273, 299, 305, 313, 318, 407; `oidc_config.go`: 188, 294, 326, 333, 344, 349,
   356, 434): replace `server.Error(w, http.StatusInternalServerError,
   "internal_error", "<msg>")` with `apiresp.WriteError(w, r, err)` where `err` is the
   in-scope underlying error wrapped with the op label
   (`fmt.Errorf("oidc provider create: %w", err)`), or `errors.New("<msg>")` when no
   error value is in scope. Remove any immediately-preceding `slog.ErrorContext` that
   logs only this same failure (WriteError logs all 5xx with request context); fold
   distinct structured fields (e.g. `"id", id`) into the wrapped error rather than
   dropping them.

2. **Category 2 — 4xx sentinel, no details** (`oidc_providers.go`: 133, 143, 164, 172,
   190, 199, 207, 239, 411; `oidc_config.go`: 234, 242, 250, 287, 298): replace with
   `apiresp.WriteError(w, r, <sentinel>)` — `apiresp.ErrInvalidInput` (400),
   `apiresp.ErrNotFound` (404), `apiresp.ErrUnauthenticated` (401),
   `apiresp.ErrForbidden` (403). Bespoke top-level messages genericize; do not invent
   detail codes. Note `oidc_providers.go:207` (id-format hint) and
   `oidc_config.go:234` (loopback-only hint) lose a useful message — that loss is
   accepted for this plan and is flagged in the inventory's "Deferred" section; do not
   try to preserve them.

3. **Category 4 — CARVE-OUT** (`oidc_providers.go:218`): this is a `conflict` (409)
   with an actionable message ("provider id already exists; use PUT to update").
   `apiresp` has no public constructor that produces a `conflict` with a custom message
   (followup `ZVum`), and genericizing would drop the actionable "use PUT to update"
   guidance. **Leave this site as `server.Error(w, http.StatusConflict, "conflict",
   "provider id already exists; use PUT to update")`** and add a short comment directly
   above it documenting the exception, e.g.:

   ```go
   // Documented exception (followup ZVum): apiresp exposes no public
   // detail/message-carrying constructor for ErrConflict, so this actionable
   // 409 message cannot be routed through apiresp.WriteError without losing it.
   // Mirrors the writeServiceError/svc.ErrEmailTaken precedent. Collapse this
   // once mod-core adds apiresp.Conflict(...).
   ```

### Constraints

- **Preserve HTTP status codes and `error.code`/`details` exactly**; only top-level
  `error.message` text genericizes (except the carve-out, which is unchanged).
- Do not modify shared helpers or existing `apiresp.WriteError`/`writeAuthzError`
  calls; only the literal `server.Error` sites. Do not touch other handler files,
  `server/respond.go`, or `mod-core/api/apiresp`.
- `server.Decode`/`server.JSON` remain in use, so the `server` import stays. Add/keep
  `fmt`/`errors` as needed. `gofmt`/`goimports` clean.
- Follow Go design/style standards (wrap with `%w`, tidy imports).

### Verification that the migration is complete

- `grep -n "server\.Error" oidc_providers.go` returns **exactly one** match — the
  carve-out at line 218 (line number may shift after edits; it is the
  `StatusConflict` "use PUT to update" site).
- `grep -n "server\.Error" oidc_config.go` returns **zero** matches.

## Validation

- From `api/`: `make build.api` compiles clean.
- From `api/`: `make test.unit` passes. Pay attention to `oidc_providers_test.go`
  (asserts on status codes incl. `StatusConflict` at the carve-out, and `StatusOK`/
  `StatusNotFound`/`StatusUnauthorized`) and `oidc_config_test.go`. Update only
  assertions that check an old bespoke top-level message string; status-code and
  `error.code`/`details` assertions must remain unchanged and passing.
- `gofmt -l oidc_providers.go oidc_config.go` reports no diffs; `go vet` clean.
- The carve-out at `oidc_providers.go:218` still returns 409 with the unchanged
  message and carries its documenting comment.

## Metadata

architectural_impact: true

## References

- [server.Error site inventory](../notes/server-error-site-inventory.md).
- `docs/mf-standards/architecture/api-response-design.md` — "Go-layer ownership".
- Followup `ZVum` (in `plan/followups.yaml`) — the apiresp `Conflict()` constructor
  gap this carve-out documents; and `api/internal/handlers/user_accounts.go:56`
  (`writeServiceError`/`svc.ErrEmailTaken`) — the precedent it mirrors.
- `mod-core/api/apiresp/writer.go`, `errors.go` (sentinels), `invalidinput.go`.

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-17
- **Validation summary:** `make build` (api) compiles clean; `make test.unit`
  (api) passes all packages, including `internal/handlers` (covers
  `oidc_providers_test.go` and `oidc_config_test.go` — all status-code
  assertions, incl. `StatusConflict` at the carve-out, `StatusOK`,
  `StatusNotFound`, `StatusUnauthorized`, pass unchanged); `gofmt -l
  oidc_providers.go oidc_config.go` reports no diffs; `go vet
  ./internal/handlers/...` clean. `grep -n "server\.Error"
  oidc_providers.go` returns exactly one match (the carve-out at the
  `StatusConflict` "use PUT to update" site); `grep -n "server\.Error"
  oidc_config.go` returns zero matches.
- **Affected source files:**
  - `api/internal/handlers/oidc_providers.go`
  - `api/internal/handlers/oidc_config.go`
- **Decisions made:**
  - All 9 `slog.ErrorContext` calls in `oidc_providers.go` immediately
    preceded a Category-1 500 site and were removed; distinct `"id", id`
    fields that were logged were folded into the wrapped error via `%q`.
    The two `oidc_providers.go` rebuild-failure sites that had **no**
    logged `id` field originally now include `id` in the wrapped error
    anyway (cheap, strictly more diagnostic context, does not drop
    anything that was previously present) — flagged here since it is a
    small deviation from "only fold what was logged."
  - `oidc_config.go` retains the `log/slog` import: its two
    `slog.WarnContext` calls (non-fatal `ClearSetupTokenHash` /
    post-clear `RefreshState` failures inside `Confirm`) do not precede a
    `server.Error`/`apiresp.WriteError` site and were left untouched, per
    the task's "only the literal `server.Error` sites" constraint.
  - The `oidc_providers.go:218` carve-out was left byte-for-byte as
    specified, with the documenting comment added directly above it.
  - No test assertions needed updating — both `oidc_providers_test.go`
    and `oidc_config_test.go` already asserted only on HTTP status codes,
    never on top-level message text, so genericizing messages caused no
    test breakage.
- **Assumptions applied:** none beyond the task doc's own
  transform-rule categories; no `## Assumptions` section was present on
  this task doc.
</content>
