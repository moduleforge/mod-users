# Route First Verification-Code Send Failure to the Dialog

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses finding Qo20. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. sendCode takes an explicit in-dialog flag or target instead of reading the render-time verifyTarget state, so a failure on the first open shows inside the open dialog.

## Validation

1. identifiers-panel test: a failing first send on opening the verify dialog shows the error in the dialog and not in the page banner.
2. cd gui && bun test, bun run typecheck pass.

## References

- Finding `Qo20` in this plan's `plan/findings.yaml`.
