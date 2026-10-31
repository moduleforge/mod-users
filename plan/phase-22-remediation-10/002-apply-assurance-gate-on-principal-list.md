# Apply The Operation Assurance Gate To The Principal List

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task applies the list operation's assurance policy (`min_acr`, `max_auth_age`) to `ListPrincipals` in `api/internal/service/principal_admin.go`, so a list request is refused the same way a single-row `Authorize` is refused. It closes finding `P0yG`, and it narrows the parity wording in the authorization package docs, the local authz package docs (which also carry the stale prelude item 1 of finding `wkvN`) and the OpenAPI description of `GET /v1/principals`.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Roles: read `developer-go.md` and `developer.md` (Go developer role) before starting.

Environment and working rules:

- Set `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users`. Do not assume `/Users/zane/playground/../..` or any other path for it.
- Never run `link-siblings.sh` and never run a bare `make preflight`.
- Integration tests run against a throwaway `postgres:16` container that you start and remove yourself. Remove it even when tests fail.
- Do not edit sibling projects.
- Decisions D through M in `plan/notes/decisions-and-resolutions.md` still apply.

## Requirements

1. `ListPrincipals` in `api/internal/service/principal_admin.go` applies the list operation's assurance policy (`min_acr`, `max_auth_age`) to the request context once, before any query. It uses the same skip rule as `gated()` (anonymous, or a mod-core system actor with no session and no credential) and returns the same `users.step_up_required` refusal that single-row `Authorize` returns. A caller that lacks list grants still receives an empty page; this change adds no new 403.
2. Reuse the engine's assurance check through an exported or package-internal seam. Do not duplicate the policy logic inside the service.
3. Narrow the parity statements ("agree by construction") in three places so they state what is true:
   - the `api/internal/authz/authz.go` package doc
   - the `api/localAuthz/authz.go` package doc, where the stale prelude item 1 (finding `wkvN`) is also fixed
   - the `GET /v1/principals` entry in `api/openapi.yaml`

   The corrected wording must say: the row filter makes list and single-row agree on the grant arms and the owning-party gate. The operation-level assurance gate is applied once per list request by the list caller, and host list paths must do the same.

## Validation

1. Integration test: a session below the list operation's `min_acr`, and a session past its `max_auth_age`, each calling `GET /v1/principals`, receive `users.step_up_required`. A session that meets the policy lists as before. A caller with no grants still gets an empty page.
2. Mutation check: removing the gate call from `ListPrincipals` makes the step-up test fail. Restore the call and confirm the test passes.
3. `grep -n 'agree by construction' api/internal/authz/authz.go api/localAuthz/authz.go api/openapi.yaml` shows only the narrowed wording.
4. `make build`, `make test`, `make lint` and the integration suites pass with `MODULEFORGE_SIBLINGS_DIR` set. The throwaway `postgres:16` container is removed afterwards.

## References

- Finding `P0yG` (plan findings, `plan/findings.yaml`): the principal list path does not apply the operation assurance gate.
- Finding `wkvN` (plan findings, `plan/findings.yaml`): stale prelude item 1 in the `api/localAuthz/authz.go` package doc.
- [Decisions and resolutions](../notes/decisions-and-resolutions.md): Decisions D through M.

## Status

Outcome: succeeded (2026-10-30). Added `Authorizer.CheckAssurance` (api/internal/authz/authz.go), called once by `ListPrincipals` (api/internal/service/principal_admin.go) before any query through an optional `assuranceChecker` capability on the service authorizer (fails closed if absent). Narrowed the parity wording in api/internal/authz/authz.go, api/localAuthz/authz.go (stale prelude item 1 also fixed) and api/openapi.yaml (also documents 409 on GET /v1/principals). Added `TestPrincipalList_AssuranceGate` in api/internal/integration/principal_list_gate_integration_test.go. Validation: new integration test passes; mutation (gate call removed) fails it with status 200 vs 409, restored; grep for the old wording is empty; make build/test/lint and the authz, config and integration suites pass against a throwaway postgres:16 (POSTGRES_USER=users, removed afterwards).
