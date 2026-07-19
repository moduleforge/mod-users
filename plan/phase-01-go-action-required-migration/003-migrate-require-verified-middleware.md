# Migrate RequireVerifiedEmail Middleware To Apiresp

## Purpose and scope

Migrate both response branches of `RequireVerifiedEmail` (`api/internal/auth/require_verified.go`)
off the bespoke `server.JSON` flat envelope: the internal-error (missing-context) branch onto
`apiresp.WriteError` with an untyped programmer error (reserved-core `internal_error` 500 default,
per decision D6), and the email-unverified branch onto `apiresp.WriteActionRequired` using the new
`useraction.EmailUnverified` action code and the resolved `/verify-email` navigation path (Answer,
Reading A). Depends on task `add-action-code-registry` (imports `useraction.EmailUnverified`).

## Requirements

- Internal-error branch (currently lines 20-27 — the `!ok` case): replace the
  `server.JSON(w, http.StatusInternalServerError, map[string]any{"error":"internal_error",
  "message":"server misconfiguration: RequireVerifiedEmail mounted before RequireAuth"})` call with:
  ```go
  apiresp.WriteError(w, r, errors.New("RequireVerifiedEmail mounted before RequireAuth"))
  ```
  This untyped programmer error classifies to the reserved-core `internal_error`/500 default via
  `apiresp.WriteError`'s `classify` fallback (no sentinel match). No new `apiresp` capability is
  used here — per D6.
- Email-unverified branch (currently lines 30-38 — `uc.EmailVerifiedAt == nil`): replace the
  `server.JSON(w, http.StatusForbidden, map[string]any{"error":"email_unverified","message":…,
  "verify_path":"/v1/auth/email-code/request"})` call with:
  ```go
  apiresp.WriteActionRequired(w, r, useraction.EmailUnverified,
      "Verify your email address before continuing.", "/verify-email", nil)
  ```
  This reuses the exact existing message text (already matches the design doc's worked example
  verbatim) and the resolved path value `/verify-email` (Answer, Reading A — see
  [action-path values and decisions](../notes/action-path-values-and-decisions.md)), replacing the
  old API-endpoint path value `/v1/auth/email-code/request`. Pass `nil` for `data` (this action
  carries no auxiliary state).
- Add imports: `errors`, `github.com/moduleforge/core-api/apiresp`, and
  `github.com/moduleforge/mod-users/api/internal/useraction`. Remove the now-unused
  `github.com/moduleforge/mod-users/api/internal/server` import once no remaining code in this file
  references `server.JSON` (check the whole file, not just these two branches).
- Update the function's doc comment (lines 9-15): it currently describes "a stable machine-parseable
  body" in flat-envelope terms; revise to describe the new mechanisms (internal_error via
  `apiresp.WriteError`'s reserved-core default; email_unverified via the action-required envelope).
- Update the "Stable wire contract" comment (lines 31-32, directly above the email-unverified
  branch): it currently says "the GUI in Phase 5 depends on the 'email_unverified' error code and
  'verify_path' field" — revise to reference the action-required envelope's `action.code` /
  `action.path` fields instead of the retired bespoke field names (the code value
  `users.email_unverified` and the underlying navigation intent are unchanged; only the envelope
  shape and field names change).
- Rewrite `api/internal/auth/require_verified_test.go` to assert the new wire shape. Keep the
  existing four test functions (or convert to an equivalent table-driven form covering the same
  four cases) but change their body assertions:
  - `TestRequireVerifiedEmail_MissingContext`: status 500 unchanged; decode the body as
    `{"error":{"code":"internal_error","message":"an internal error occurred"}}` — assert on the
    nested `error.code`/`error.message` fields, not a flat top-level `error` string. The specific
    "mounted before RequireAuth" text is server-logged only via `apiresp.WriteError`'s 5xx logging
    path, never returned to the client — do not assert on it in the response body.
  - `TestRequireVerifiedEmail_Unverified` and `TestRequireVerifiedEmail_AnonymousAccount`: status
    403 unchanged; decode the body as `{"action":{"code":"users.email_unverified","message":"Verify
    your email address before continuing.","path":"/verify-email"}}` — assert the top-level member
    present is `action` (not `error`), and that no top-level `action.data` member is present in the
    decoded map (omitted, since `data` is passed as `nil`).
  - `TestRequireVerifiedEmail_Verified`: unchanged (200, `next.called` true) — no wire-shape
    assertions needed; confirm it still passes as-is.
- Verification note (read-only, no code change expected in this task): `apiresp.WriteActionRequired`
  panics if `path` is not application-relative (open-redirect guard), and its caller obligation
  requires panic-recovery middleware installed upstream. Confirm
  `api/internal/server/server.go:28` (`r.Use(Recoverer)`, defined in
  `api/internal/server/middleware.go:50`) is a global, top-level middleware that sits upstream of
  every route this middleware can gate, so the panic-recovery precondition is already satisfied.

## Validation

- Build prerequisite: worktree-local `go.work` setup (see Assumptions).
- `cd api && go build ./...` succeeds.
- `cd api && go vet ./internal/auth/...` is clean.
- `cd api && go test ./internal/auth/... -run TestRequireVerifiedEmail` passes.
- `cd api && go test ./...` (full suite) passes — no untouched-path regressions.
- `gofmt -l api/internal/auth/require_verified.go api/internal/auth/require_verified_test.go`
  reports no files.
- `grep -n "server.JSON" api/internal/auth/require_verified.go` returns no matches.
- `grep -n "verify_path" api/internal/auth/require_verified*.go` returns no matches (old field name
  retired everywhere it was asserted).

## Metadata

architectural_impact: true

## Assumptions

- Task `add-action-code-registry` has landed (or is present on the same base) so
  `useraction.EmailUnverified` is importable.
- Build prerequisite: worktree-local `go.work` recipe per
  `docs/mf-standards/building-common.md` § "Building inside a task worktree".
- `/verify-email` is the resolved path value (Answer, Reading A) — requiring the consuming apps
  (`app-mfdemo`/`app-mftodo`) to mount that route is a cross-repo coordination item flagged for the
  manager, out of scope for this task and this plan.

## References

- [Action-path values and decisions](../notes/action-path-values-and-decisions.md) — decisions D1,
  D6, and the Answer section.
- Design doc: `docs/mf-standards/architecture/api-response-design.md` — "The `action` object"
  worked example for `users.email_unverified` (message text matches verbatim).
- `api/internal/server/middleware.go:50` (`Recoverer`), `api/internal/server/server.go:28`
  (`r.Use(Recoverer)`) — confirms the panic-recovery precondition (read-only reference).
