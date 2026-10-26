# Align Last Operator Admin Error And Contract With Principal Disable

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `sDO1` in this plan's `plan/findings.yaml`. The 409 `users.last_operator_admin` returned by `POST /v1/principals/{uuid}/disable` names only the ending/suspension path or a profile `status` field, so a caller disabling the principal of the last effective operator administrator receives a message that does not describe the action it attempted. The error contract, its godoc, the OpenAPI description and the AGENTS.md rows must all cover the disable path.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. The 409 `users.last_operator_admin` returned by `POST /v1/principals/{uuid}/disable` must no longer name only ending/suspension or a profile `status` field. Either widen `ErrLastOperatorAdmin`'s message to cover disabling (with a field that fits both paths), or return a disable-specific error that keeps the code `users.last_operator_admin`.
2. The `ErrLastOperatorAdmin` godoc (and the godoc of any disable-specific error) names every path that returns it, including `requireNotLastOperatorAdminForDisable`.
3. `api/openapi.yaml` documents, on `POST /v1/principals/{uuid}/disable`, that disabling the principal of the last effective operator administrator is refused with 409 `users.last_operator_admin`.
4. The AGENTS.md `profile_last_admin.go` row (and the `principal_admin.go` row, if one exists) mentions `requireNotLastOperatorAdminForDisable` and the disable-path refusal.
5. Do not add links from non-plan docs into `plan/`.

## Validation

1. `grep` shows the new or widened message and the godoc covering disable; any test asserting the old message text is updated.
2. The disable route's description or 409 response in `api/openapi.yaml` mentions `users.last_operator_admin`; the YAML parses, and `make openapi.validate` runs where pyyaml or spectral is available.
3. `make build.api` and `make test.unit` pass. If a Postgres 16 harness is available, the leave-gate integration tests still pass.

## References

- Finding `sDO1` in this plan's `plan/findings.yaml`: the 409 `users.last_operator_admin` from `POST /v1/principals/{uuid}/disable` reuses the ending/suspension message and profile `status` field, which does not describe the disable path enforced by `requireNotLastOperatorAdminForDisable`. This task aligns the error message, its godoc, the OpenAPI contract and the AGENTS.md rows with that path.
