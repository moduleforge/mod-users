# Fix NULL Handling In Policy SQL Functions

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `4w6W` (bug, low) and `iaJb` (bug, low). Both are NULL-handling defects in policy SQL functions in the baseline migration. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. `holder_policy_mismatch` returns `holder_acr` NULL when the holder has no usable credential (`CASE WHEN count(*) = 0 THEN NULL ELSE ... END`, as `session_acr_excluding` does), so the `holder_acr IS NULL` guard is live and `PolicyMismatch.HolderACR` is 0 per its documented zero value.
2. `profile_availability_excluding` filters factors with `(p_exclude_credential_id IS NULL OR c.id <> p_exclude_credential_id)`, matching `session_acr_excluding`.
3. Edit `0100_baseline.sql` in place (plan policy). Regenerate sqlc only if signatures change. Keep the function comments accurate.

## Validation

1. An integration test: `holder_policy_mismatch` for a holder with no usable credential yields `holder_acr` NULL and a 409 detail that does not claim acr 1.
2. An integration test: `profile_availability_excluding(session, profile, NULL)` equals `profile_availability(session, profile)`.
3. `make build.api` and the integration suite pass on a fresh DB (`make clean.data`).

## References

- Finding `4w6W` in this plan's `plan/findings.yaml` (bug, low): `model/migrations/sql/0100_baseline.sql` computes `holder_acr` as `LEAST(3, GREATEST(max(base_acr), 1|2))`, which is 1 for a holder with no usable credential. The `holder_acr IS NULL` guard in `holder_policy_mismatch` is therefore dead, and the 409 message is misleading.
- Finding `iaJb` in this plan's `plan/findings.yaml` (bug, low): `profile_availability_excluding` in `model/migrations/sql/0100_baseline.sql` filters with `c.id <> p_exclude_credential_id`, so a NULL argument drops every factor. `session_acr_excluding` already handles NULL. Same inconsistency class as `4w6W`.
