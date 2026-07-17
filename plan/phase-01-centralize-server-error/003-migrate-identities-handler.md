# Migrate identities.go server.Error Sites

## Purpose and scope

Collapse the 23 literal `server.Error(...)` / `server.ErrorWithDetails(...)` call sites
in `api/internal/handlers/identities.go` onto sentinel-driven
`apiresp.WriteError(w, r, err)` calls. One of four parallel tasks closing followup
`8iRl`. `identities.go` has **three carve-out sites** (305, 379, 391) and one flat
region that must be left alone.

Read [`plan/overview.md`](../overview.md) and the
[server.Error site inventory](../notes/server-error-site-inventory.md) first — the
inventory has the exact per-site line list, category tags, and the load-bearing
behavior note. This task owns the `identities.go` section (lines 149–608).

## Requirements

### Transform rules (apply per the inventory's category tags for identities.go)

1. **Category 1 — `internal_error` 500 sites** (lines 149, 157, 311, 370, 387, 401,
   418, 472, 591, 608): replace with `apiresp.WriteError(w, r, err)` where `err` is the
   in-scope underlying error wrapped with the op label
   (`fmt.Errorf("identities.SetPassword: %w", err)`), or `errors.New("<msg>")` when
   none is in scope. Remove any immediately-preceding `slog.ErrorContext` that logs
   only this same failure (WriteError logs all 5xx with request context); fold distinct
   structured context into the wrapped error.

2. **Category 2 — 4xx sentinel, no details** (lines 216, 220, 248, 348, 353, 573, 577,
   586, 596): replace with `apiresp.WriteError(w, r, <sentinel>)` —
   `apiresp.ErrInvalidInput` (400 sites), `apiresp.ErrNotFound` (line 216),
   `apiresp.ErrUnauthenticated` (lines 586, 596). Messages genericize; do not invent
   detail codes. **Line 220** currently passes `err.Error()` as the message (dynamic
   text from a provider-link parse) — this genericizes to the standard `invalid_input`
   message via `apiresp.WriteError(w, r, apiresp.ErrInvalidInput)`; the raw err text is
   no longer surfaced to the client (a minor improvement — it avoids leaking internal
   parse detail). Do not wrap the raw err into the client body.

3. **Category 3 — `invalid_input` WITH details** (line 357,
   `users.password_too_short`): replace `server.ErrorWithDetails(w,
   http.StatusBadRequest, "invalid_input", msg, details)` with
   `apiresp.WriteError(w, r, apiresp.InvalidInput(details...))`, passing the exact same
   `apiresp.FieldError` values.

4. **Category 4 — CARVE-OUTS (leave as-is with a documenting comment)**. `apiresp` has
   no public detail-carrying constructor for `not_found` or `unauthenticated` (only
   `InvalidInput`), so these detail-carrying sites cannot route through
   `apiresp.WriteError` without dropping their `details[]` (followup `ZVum`, broadened
   here beyond conflict). **Leave each unchanged** and add a short comment above it
   documenting the exception, mirroring the `writeServiceError`/`svc.ErrEmailTaken`
   precedent:
   - **Line 305** — `server.ErrorWithDetails(w, http.StatusNotFound, "not_found",
     "identity not found", []apiresp.FieldError{{Code: "users.identity_not_found", ...}})`.
     Keep the existing decision comment above it (about masking not applying) and add
     the ZVum-exception note.
   - **Line 379** — `server.ErrorWithDetails(w, http.StatusUnauthorized,
     "unauthenticated", ..., []apiresp.FieldError{{Field: "current_password", Code:
     "users.bad_credentials", ...}})`.
   - **Line 391** — same shape as 379 ("current password is incorrect").

   Suggested comment (adapt per site):
   ```go
   // Documented exception (followup ZVum): apiresp exposes no public
   // detail-carrying constructor for this sentinel (only InvalidInput), so the
   // users.<...> field detail cannot be attached via apiresp.WriteError. Kept as
   // server.ErrorWithDetails, mirroring the writeServiceError/svc.ErrEmailTaken
   // precedent; collapse once mod-core adds a detail-carrying constructor.
   ```

### Do NOT touch (out of scope in this file)

- `writeStepUpRequired(w)` and `writeLastIdentityError(w)` and their call sites (e.g.
  lines ~295, ~341) — flat-envelope helpers deferred by followup `eiF8`. Leave them
  exactly as-is.

### Constraints

- Preserve HTTP status codes and `error.code`/`details` exactly at every site; only
  top-level `error.message` genericizes (carve-outs unchanged entirely).
- Do not modify shared helpers or existing `apiresp.WriteError`/`writeAuthzError`
  calls; only the literal `server.Error`/`server.ErrorWithDetails` sites. Do not touch
  other handler files, `server/respond.go`, or `mod-core/api/apiresp`.
- `server.Decode`/`server.JSON` remain in use, so the `server` import stays. Add/keep
  `fmt`/`errors` as needed. `gofmt`/`goimports` clean; wrap with `%w`.

### Verification that the migration is complete

- After editing, `grep -n "server\.Error\|server\.ErrorWithDetails" identities.go`
  returns **exactly three** matches — the carve-outs at 305, 379, 391 (line numbers
  will shift). No other `server.Error*` call remains.

## Validation

- From `api/`: `make build.api` compiles clean.
- From `api/`: `make test.unit` passes. Check `identities_test.go` and
  `identities_stepup_test.go`; they assert on status codes and `error.code`/`details`.
  The three carve-outs preserve their exact bodies (incl. `details`), so their tests
  must still pass unchanged. Update only assertions checking an old bespoke top-level
  message string.
- `gofmt -l identities.go` reports no diffs; `go vet` clean.
- The `writeStepUpRequired`/`writeLastIdentityError` call sites are untouched.

## Metadata

architectural_impact: true

## References

- [server.Error site inventory](../notes/server-error-site-inventory.md).
- `docs/mf-standards/architecture/api-response-design.md` — "Go-layer ownership".
- Followups `ZVum` (constructor gap the carve-outs document) and `eiF8` (the
  flat-envelope sites to leave alone), in `plan/followups.yaml`.
- `api/internal/handlers/user_accounts.go:56` (`writeServiceError`/`svc.ErrEmailTaken`)
  — the documented-exception precedent the carve-outs mirror.
- `mod-core/api/apiresp/writer.go`, `errors.go`, `invalidinput.go`.
</content>
