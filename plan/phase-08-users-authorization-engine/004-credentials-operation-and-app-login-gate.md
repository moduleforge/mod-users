# Credentials Operation and App Login Gate

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task does two things:

- **Credential management for other principals.** Serve the routes under `/v1/principals/{uuid}/credentials*`, authorized by mod-authz's new `credentials` operation. This replaces `assume` for SSH keys, and it gives an owner a path to rotate the API key of a service account it owns.
- **The `invite_only` app login gate.** Replace phase 6's stub with a real check through `Authorize(login, app)` per U15.

Suggested role: `plugins/flow/roles/developer-go.md`. No standard skill covers this task.

Scope:

- the principals handler and its credential routes
- `CredentialService` authorization
- the session/landing/availability code paths that evaluate the app gate
- `api/openapi.yaml`
- tests

Dependency: requires task 001. It can run in parallel with tasks 002, 003 and 005.

## Requirements

1. **Credential routes.** Add the following routes to the `/v1/principals` handler from phase 6:
   - `GET /{uuid}/credentials`
   - `POST /{uuid}/credentials/api-keys`
   - `POST /{uuid}/credentials/ssh-keys`
   - `DELETE /{uuid}/credentials/{credential_uuid}`

   Every route authorizes the `credentials` operation on the principal entity. The engine's gate enforces `max_auth_age` of 10 minutes.
   - API keys are issued only to service accounts, and the secret is shown once.
   - SSH keys reuse `api/internal/sshkey/` validation and map `cred_ssh_key_active_fingerprint_uq` to 409 `users.ssh_key_in_use`.
   - `DELETE` revokes through `CredentialService.RevokeInTx` with reason `admin`, which revokes dependent sessions.
   - Keep the error order: 401, 400, 403, 404/409. A principal the caller cannot read yields 404.
2. **`IssueAPIKeyInTx`.** Confirm `usersservice.CredentialService.IssueAPIKeyInTx` authorizes `credentials` on the principal entity through the engine; the signature is binding per [API surface section 6.3](../notes/mod-users-api-surface.md#63-usersservice-public-facade-over-internalservice). Remove any phase 6 comment or code path that relied on the unknown-slug fallback.
3. **Owner rotation.** A user's personal profile that owns a self-owned service account can issue and revoke that account's API keys through the own arm, because `credentials` has `ownership_confers`. A party-owned service account is managed through an owned-scope or entity grant on the party. Neither path needs `manage` or `impersonate`.
4. **SSH keys move off `assume`.** No code path authorizes `assume` or `impersonate` for credential or SSH-key management. Operator SSH-key installation for another principal goes through `POST /{uuid}/credentials/ssh-keys` under `credentials`. Self-service `/v1/me/credentials*` keeps authorizing on `session.principal = target` (U8).
5. **App login gate (U15).** Whenever a session's app has `apps.invite_only = true`, availability of a candidate profile additionally requires `Authorize(ctx-with-candidate-profile, "login", &app.entity_id)`. A denial makes the profile unavailable; the token endpoints surface it as `users.app_login_not_granted` (403). Replace the phase 6 stub ("open unless invite_only, then deny without a wildcard") in every path that evaluates availability with an app:
   - login landing
   - switch
   - refresh
   - the profile list
   - the per-request middleware

   Apps without `invite_only` cost no extra query.
6. **Wiring.**
   - Add the routes to `api/openapi.yaml`.
   - Wire `api/handlers/handlers.go` re-exports and `moduleforge.module.yaml` if the principals handler gains dependencies.
   - Update the `AGENTS.md` and `README.md` rows that describe SSH-key administration or `assume`, where those rows describe code this task changes.
   - **Audit ops** (decision U21, [API surface section 10.1](../notes/mod-users-api-surface.md)): issued API keys and installed SSH keys emit `create` on `credential`, and revocations emit `revoke` on `credential`, through the phase 6 primitives. Today's SSH-key path observes with op `assume` (`sshKeyMutateOp`); that op must not survive anywhere.
7. **Tests.**
   - Operator with a wildcard grant: lists, issues and revokes credentials for a service account and installs an SSH key for a user.
   - Party admin with an owned-scope grant: manages credentials of a party-owned service account, and is refused for a personal user's credentials.
   - Personal-profile owner: rotates its self-owned service account's API key; another user is refused.
   - Stale `auth_time` yields `users.step_up_required` as the shared mod-core `apiresp` action-required error (not a local rendering).
   - Revocation ends dependent sessions on the next request.
   - `invite_only` app: a profile without a `login` grant is locked with `users.app_login_not_granted`. A `login` grant through an actor group admits it, and so does a wildcard holder. A non-invite-only app is unaffected.
   - Route order tests use mod-core's `AuthContext` identities.

## Validation

- `make build`, `make test` and `make lint` pass.
- `cd model && make verify` passes.
- The integration suites pass against a throwaway Postgres per `AGENTS.md`.
- `grep -rnE '"assume"|"impersonate"' api/internal/service api/internal/handlers` shows no credential or SSH-key path. Impersonation code from task 003 is expected.
- `grep -n 'credentials' api/openapi.yaml` shows the four principal credential routes.
- `grep -rni 'stub' api/internal/handlers/auth api/internal/service | grep -i invite` returns nothing.

## Assumptions

- Task 001 has landed, and mod-authz has seeded the `credentials` operation with `ownership_confers = true` and `max_auth_age` of 10 minutes.
- Phase 6 delivered `/v1/principals` read/list/disable/enable, `CredentialService.IssueAPIKeyInTx` and `RevokeInTx`, and the stubbed app gate.

## References

- [API surface section 6.3](../notes/mod-users-api-surface.md#63-usersservice-public-facade-over-internalservice): the `CredentialService` binding methods.
- [API surface section 7](../notes/mod-users-api-surface.md#7-http-api): the `/v1/principals` routes and the error codes.
- [Decisions](../notes/decisions-and-resolutions.md#planning-decisions): U8, U15, U16, and resolution (d).
- [Work packages](../notes/work-packages.md#phase-users-authorization-engine): package 4.

## Checkpoint hints

- After the principal credential routes and their authorization.
- After the app login gate replaces the stub in every availability path.
- After the integration tests.
