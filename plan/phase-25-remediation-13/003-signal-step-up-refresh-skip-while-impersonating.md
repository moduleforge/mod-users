# Signal and Test the Impersonating No-Op in refreshAfterStepUp

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses findings `LJFE` and `Wstk`. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. refreshAfterStepUp keeps its recorded no-op while impersonating (no refresh, no token change) but tells the caller the assurance was not raised, for example by returning a boolean or rejecting with a distinct ApiRequestError code.
2. OidcCallbackPage does not call onComplete as though assurance were raised when the refresh was skipped. It reports a clear message (or returns without implying elevation), so the interrupted action does not loop on step_up_required.

## Validation

1. api test: with the tab impersonating, refreshAfterStepUp issues no POST /v1/auth/token, leaves the stored token unchanged and signals the skip.
2. oidc-callback-page test: stepped_up=1 while impersonating issues no token request and does not report success as raised assurance.
3. `cd gui && bun test` and `bun run typecheck`; `make build.gui` and `make lint.gui` pass.

## References

- Finding `LJFE` in this plan's `plan/findings.yaml`.
- Finding `Wstk` in this plan's `plan/findings.yaml`.
