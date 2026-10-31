# Use Authentication Failed Code In OIDC Troubleshooting

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates finding cRuN (OIDC troubleshooting documents the wrong redirect error code `auth_failed`).

The work targets the plan branch plan/identity-model-impl and lands through the ordinary per-task loop.

## Requirements

1. In the `docs/oidc-troubleshooting.md` refusals table (around line 121), replace `auth_failed` with `authentication_failed`.
2. Change the matching comment in `api/internal/auth/resolver.go` (around line 121, "refuse auth_failed") to name `authentication_failed`. Comment edit only; no code change.

## Validation

1. `grep -rn 'auth_failed' docs/oidc-troubleshooting.md api/internal/auth/resolver.go` matches only inside `authentication_failed` or the `RedirectErrAuthFailed` identifier.
2. `go vet ./internal/auth/...` passes in `api/`.

## References

- Finding cRuN in this plan's `plan/findings.yaml`.

## Status

- Outcome: succeeded
- Date: 2026-10-31
- Validation: check 1 (grep for `auth_failed`) passed with zero matches in both files; check 2 (`go vet ./internal/auth/...` in `api/`) passed, log at `.flow/validation-logs/02-go-vet-internal-auth.log`.
- Affected source files: `docs/oidc-troubleshooting.md`, `api/internal/auth/resolver.go` (comment only).
- Assumptions applied: none recorded in this task doc.
