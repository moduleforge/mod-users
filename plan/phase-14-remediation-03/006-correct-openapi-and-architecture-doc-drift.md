# Correct Step-Up Order And Bootstrap Exception Docs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `qvpE` (documentation, low) and `kimI` (documentation, low). Both are documentation drift: the OpenAPI description of the step-up order, and the architecture doc's list of documented exceptions for the bootstrap route. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. `api/openapi.yaml` `info.description` states the actual order for principal disable and enable (authorization `read` then `update`, then the step-up freshness check), matching `UserService.writablePrincipal` in `api/internal/service/principal_admin.go`. Verify the order in code before editing.
2. `docs/architecture.md`'s API-layer documented exceptions and D20(3) list `POST /v1/bootstrap` as a public route outside `requireOIDCConfirmed` whose credential is the `X-Setup-Token` header, with the reason (bootstrap may precede OIDC confirmation).
3. No link from either file into `plan/`.

## Validation

1. `grep` shows the corrected sentence in `api/openapi.yaml`, and it matches the code path order in `writablePrincipal`.
2. `grep -n 'bootstrap' docs/architecture.md` shows the exception in both the API layer and D20.
3. The OpenAPI document still validates (the project's existing openapi check, if any).

## References

- Finding `qvpE` in this plan's `plan/findings.yaml` (documentation, low): `api/openapi.yaml` (lines ~33-35) says step-up comes before authorization on principal disable and enable. `UserService.writablePrincipal` in `api/internal/service/principal_admin.go` authorizes first.
- Finding `kimI` in this plan's `plan/findings.yaml` (documentation, low): the API-layer documented exceptions (around `docs/architecture.md` line 101) and D20(3) omit `POST /v1/bootstrap`, which sits outside `requireOIDCConfirmed` with `X-Setup-Token` as its credential.
