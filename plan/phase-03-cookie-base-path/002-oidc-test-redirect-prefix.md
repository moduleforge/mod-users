# 002 OIDC Test Redirect Keeps Mount Prefix

## Purpose and scope
Followup hDB7: `api/internal/handlers/auth/oidc.go` (~line 334) sets `u.Path = "/oidc-config"`, dropping the mount prefix behind a strip proxy. The flow-accounts finding text is not available; review the line (and `buildSuccessRedirect`/`redirectToLinkSuccess`) for the same base-path assumption.

## Requirements
1. Derive the `/oidc-config` target from the configured base path (task 001) prepended to the path, instead of replacing the whole path.
2. Audit other redirect builders in the same file for the same assumption; fix or list in the report.
3. Unset base path → unchanged.

## Validation
- Tests: redirect with and without base path; fragment/query still correct.
- `cd api && go test ./...`.

## Status
- Outcome: succeeded (2026-10-10).
- Validation: new `TestRedirectToTestResultBasePath` (unset, configured, nested, invalid ignored; fragment/query checked; X-Forwarded-Prefix ignored) passes; `cd api && go test ./...` passes.
- Files: `api/internal/handlers/auth/oidc.go` (`testResultPath`), `api/internal/handlers/auth/oidc_cookie_path_test.go`.
- Audit: `buildSuccessRedirect`, `redirectToFrontendError`, `redirectToLinkSuccess` use the configured `FrontendReturnURL` verbatim (no path overwrite), so no change needed.
