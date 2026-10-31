# Handle OIDC Link and Step-Up Return Fragments

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses finding 4ohc. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. The OIDC return page handles `#linked=1` and `#stepped_up=1` (with `return=<path>`): it strips the fragment, validates `return` with isSafeReturnPath, and routes back to it instead of reporting a sign-in failure.
2. On `stepped_up=1` the tab obtains a fresh access token (for example via the refresh grant) before returning, so the next call sees the raised assurance.
3. The comments in step-up-dialog.tsx and credentials-panel.tsx describing where the provider callback returns are accurate for the delivered code.

## Validation

1. oidc-callback-page tests cover `linked=1` and `stepped_up=1` with a safe and an unsafe return path, and assert no onError call on success.
2. cd gui && bun test, bun run typecheck; make build.gui and make lint.gui pass.

## References

- Finding `4ohc` in this plan's `plan/findings.yaml`.

## Status

Outcome: succeeded (2026-10-31). `OidcCallbackPage` now handles `#linked=1` and `#stepped_up=1` (with `return`): fragment stripped, return validated with `isSafeReturnPath`, `onComplete` called with it, no `onError`. On `stepped_up=1` it runs the refresh grant first; a failed refresh reports via `onError`. Comments in step-up-dialog.tsx and credentials-panel.tsx updated. Validation: `bun test` (445 pass), typecheck, `make build.gui`, `make lint.gui` all pass. Files: `gui/src/components/oidc-callback-page.tsx`, `gui/src/components/oidc-callback-page.test.tsx`, `gui/src/components/step-up-dialog.tsx`, `gui/src/components/credentials-panel.tsx`.
