# Bind The OIDC Handoff Code To A Browser Verifier

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `z443` (security, medium). The OIDC handoff code is not bound to the browser that started the flow, so an intercepted code can be redeemed by another party. Owner decision: a PKCE-style verifier is carried in the signed state. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

The GUI side is recorded in phase 9 tasks 001 and 003 and is not part of this task.

Suggested role: `plugins/flow/roles/developer-go.md`

## Requirements

1. `GET /v1/auth/oidc/{provider}/start` accepts a handoff challenge parameter, the SHA-256 of a client-generated random verifier encoded as base64url. The challenge is carried in the signed oauth state and is required for sign-in flows.
2. The callback stores the challenge hash on the `login_handoff` challenge it creates.
3. `POST /v1/auth/handoff` (`SessionService.ExchangeHandoff`) requires the verifier in the request body. It hashes the verifier and compares in constant time. A mismatch or a missing verifier is refused with a section 7 error code. The code is consumed only on success, or per the existing single-use rules.
4. Update `api/openapi.yaml` and the relevant docs.

## Validation

1. Tests show: an exchange with the correct verifier succeeds; a wrong or missing verifier is refused; the code cannot be redeemed without the verifier.
2. `make build`, `make test` and `make lint` pass in `api/`, and the integration suites pass.

## References

- Finding `z443` in this plan's `plan/findings.yaml` (security, medium): the OIDC handoff code is not bound to a browser verifier, so an intercepted code can be redeemed without the originating client.

## Status

Outcome: succeeded (2026-10-25). Validation: `make build`, `make test`, `make lint` pass in the worktree; the authz, config and integration suites pass against a throwaway Postgres 16 (container removed). Sibling links resolved through `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users` (mod-core, mod-authz, mod-audit plan worktrees).

Changes: `auth_challenges.binding_hash` column and `CreateChallenge` query (regenerated `model/db`); `api/internal/auth/handoff_challenge.go` (S256 challenge, verifier shape, constant-time `VerifyHandoff`); the challenge is carried in the signed state (`hc`) and required by `/start` for sign-ins (`handoff_challenge`, 400 `users.handoff_challenge_invalid`); the callback stores it on the `login_handoff` challenge; `SessionService.ExchangeHandoff(code, verifier)` verifies inside the consuming transaction, so a wrong verifier rolls back and does not spend the code; `POST /v1/auth/handoff` requires `verifier`. `api/openapi.yaml`, `docs/mod-users-spec.md` and `docs/architecture.md` updated.
