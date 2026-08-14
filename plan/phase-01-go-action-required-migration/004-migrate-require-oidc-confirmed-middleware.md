# Migrate RequireOIDCConfirmed Middleware To Apiresp

## Purpose and scope

Migrate the OIDC-not-confirmed branch of `RequireOIDCConfirmed`
(`api/internal/auth/require_confirmed.go`) off the bespoke `server.JSON` flat envelope onto
`apiresp.WriteActionRequired`, using the new `useraction.OIDCNotConfirmed` action code (503, never
remapped, per D2/decision already resolved), the already-correct `/oidc-config` navigation path,
and `action.data.state` carrying the boot state. Depends on task `add-action-code-registry`.

## Requirements

- Replace the `server.JSON(w, http.StatusServiceUnavailable, map[string]any{"error":
  "oidc_not_confirmed","config_path":"/oidc-config","state":string(state)})` call (lines 35-39)
  with:
  ```go
  apiresp.WriteActionRequired(w, r, useraction.OIDCNotConfirmed,
      "Single sign-on is not finished configuring.", "/oidc-config",
      map[string]any{"state": string(state)})
  ```
  This reuses the design doc's worked-example message verbatim, the already-correct `/oidc-config`
  path (D2, unchanged from the current code value), and the existing `string(state)` value — now
  nested under `action.data.state` instead of a top-level `state` field.
- Add imports: `github.com/moduleforge/core-api/apiresp` and
  `github.com/moduleforge/mod-users/api/internal/useraction`. Remove the now-unused
  `github.com/moduleforge/mod-users/api/internal/server` import if this file no longer references
  `server.JSON` anywhere else (check the whole file).
- Update the function's doc comment (lines 10-21), specifically the note "The 503 response body is
  deliberately machine-parseable (config_path in particular) so the GUI can redirect without
  string-parsing HTTP status text" — revise to describe the action-required envelope
  (`action.path`) instead of the bespoke `config_path`/`state`/`error` fields, while preserving the
  underlying rationale (machine-parseable, GUI navigates without status-text parsing).
- Update `api/internal/handlers/oidc_config_test.go`'s `TestRequireOIDCConfirmed_Gates` (lines
  510-556) to assert the new wire shape for the "unconfirmed" branch (the `handler.ServeHTTP(rr,
  req)` call around line 532):
  - Status 503 unchanged.
  - Decode the body and assert a top-level `action` object (not a flat `error`/`config_path`
    map) with `code == "users.oidc_not_confirmed"`, `message == "Single sign-on is not finished
    configuring."`, `path == "/oidc-config"`.
  - **New assertion, not present in the current test**: assert `action.data.state` equals the
    string form of the `config.BootStateInitFailed` value under test (the test currently checks
    only `error` and `config_path`; it does not check `state` today). This closes the phase's
    stated deliverable of asserting `data.state` presence.
  - Add an explicit assertion or comment confirming the 503 status is written verbatim and never
    remapped to any other status (403/409/500) — this is the "verbatim-503 (never remapped)"
    invariant the phase's Outputs section calls for; a straightforward `rr.Code ==
    http.StatusServiceUnavailable` check already covers the mechanical assertion, but make the
    invariant explicit in a comment so a future reader understands why this status must never be
    remapped (per `apiresp.WriteActionRequired`'s own doc comment: it "writes action.Status
    verbatim... and MUST NOT remap it onto an error status... under any circumstance").
  - Leave the "confirmed" branch of the test (the second `handler.ServeHTTP(rr2, req2)` call,
    asserting `rr2.Code == http.StatusOK`) unchanged — that pass-through path is untouched by this
    migration.

## Validation

- Build prerequisite: worktree-local `go.work` setup (see Assumptions).
- `cd api && go build ./...` succeeds.
- `cd api && go vet ./internal/auth/...` is clean.
- `cd api && go test ./internal/handlers/... -run TestRequireOIDCConfirmed_Gates` passes (the test
  lives in package `handlers`, not `auth`, per the current file layout — it imports `auth` and
  drives `auth.RequireOIDCConfirmed` directly).
- `cd api && go test ./...` (full suite) passes.
- `gofmt -l api/internal/auth/require_confirmed.go api/internal/handlers/oidc_config_test.go`
  reports no files.
- `grep -n "server.JSON" api/internal/auth/require_confirmed.go` returns no matches.
- `grep -n "config_path" api/internal/auth/require_confirmed.go api/internal/handlers/oidc_config_test.go`
  returns no matches (old field name retired everywhere it was asserted).

## Metadata

architectural_impact: true

## Assumptions

- Task `add-action-code-registry` has landed so `useraction.OIDCNotConfirmed` is importable.
- Build prerequisite: worktree-local `go.work` recipe per
  `docs/mf-standards/building-common.md` § "Building inside a task worktree".
- `/oidc-config` (D2) and the `action.data.state` shape (D3) are already resolved — no open
  questions here; `/oidc-config` is already the GUI's real navigation route
  (`gui/src/components/client-layout.tsx`'s `CONFIG_PATH`), not a value this task changes.

## References

- [Action-path values and decisions](../notes/action-path-values-and-decisions.md) — decisions D2,
  D3.
- Design doc: `docs/mf-standards/architecture/api-response-design.md` — "The `action` object"
  worked example for `users.oidc_not_confirmed`; "Action-required status set" (the verbatim-503
  invariant).
- `api/internal/handlers/oidc_config_test.go:510-556` — the existing test this task updates.

## Status

- **Outcome:** succeeded (2026-08-14).
- **Validation:** `cd api && go build ./...` — passed. `cd api && go vet ./internal/auth/...` —
  passed, clean. `cd api && go test ./internal/handlers/... -run TestRequireOIDCConfirmed_Gates` —
  passed. `cd api && go test ./...` (full suite) — passed, all packages. `gofmt -l
  api/internal/auth/require_confirmed.go api/internal/handlers/oidc_config_test.go` — no files
  listed. `grep -n "server.JSON" api/internal/auth/require_confirmed.go` — no matches. `grep -n
  "config_path" api/internal/auth/require_confirmed.go api/internal/handlers/oidc_config_test.go` —
  no matches.
- **Files:** `api/internal/auth/require_confirmed.go`,
  `api/internal/handlers/oidc_config_test.go`.
- **Build-prerequisite correction (Assumptions).** As previously flagged by task
  `add-action-code-registry` (phase-01 task 001), the Assumptions section's "three `../` up" figure
  for the worktree-local `go.work` recipe did not hold for this worktree either — the aggregate
  sibling root (`.../moduleforge/`) is empirically **four** `../` up from this worktree's root
  (`mod-users/worktrees/plan/users-action-required-migration-01-004/`), not three, because the
  branch name contains a `/`. Built the worktree-local `go.work` with `use` directives at the
  four-`../` depth for `mod-core/api`, `mod-core/model`, `mod-audit/api`, `mod-audit/model`,
  `mod-authz/api`, `mod-authz/model` (mirroring `api/go.mod`'s `replace`-listed siblings), then
  resolved the resulting "conflicting replacements" errors with `go work edit -replace
  <module>@v0.0.0=<four-dot-dot path>` for `core-model`, `core-api`, `audit-model`, `audit-api`,
  `authz-model`, `authz-api` — same remediation pattern as task 001, same corrected depth.
  `go.work`/`go.work.sum` are gitignored; no committed file changed for this.
- No mfgen/stale-interface build break was encountered in this task's own build/test steps beyond
  the one task 001 already fixed (`fieldcrypto.NewFromEnv` → `NewFromEnvOrGenerate` in
  `api/cmd/server/main.go`, already present on the branch this worktree was cut from).
