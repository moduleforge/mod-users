# Add SSH Key HTTP Routes

## Purpose and scope

Expose the SSH key lifecycle over `/v1`: self-service and operator-on-behalf-of register, list, and revoke. Wire the new services and routes into `moduleforge.module.yaml`, the public facade `api/handlers/handlers.go`, and the hand-written dev server `api/cmd/server/main.go`.

No standard skill covers this. The contract is the [SSH key design note](../notes/ssh-key-design.md), sections D8, D9, D10, and D11 (manifest service names).

**Depends on:** task 003 (`usersservice.SSHKeyService`/`SSHKeyResolver` and the constructors `NewSSHKeyService`/`NewSSHKeyResolver`).

## Requirements

1. **Handler.** Create `api/internal/handlers/ssh_keys.go` with `SSHKeysHandler`, kept **thin**: parse input, call one service method, shape the response (AGENTS.md "Handlers are thin").
   - Dependencies: the `SSHKeyService`, the JWT secret, the step-up consumed-JTI cache (`*sync.Map`), and the `stepUpRequired` flag. These are the step-up deps `NewIdentitiesHandler` already takes.
   - Constructor: `NewSSHKeysHandler(svc, jwtSecret string, consumed *sync.Map, stepUpRequired bool)`, with argument order matching the manifest entry below.
   - Handler methods:
     - `ListSelf`, `RegisterSelf`, `RevokeSelf`: account UUID taken from `localauth.UserContext` (`uc.UserUUID`)
     - `ListForAccount`, `RegisterForAccount`, `RevokeForAccount`: account UUID taken from the `{uuid}` path param, with the key UUID from `{key_uuid}`
   - A malformed UUID path param returns `400 invalid_input`.
2. **Request and response shapes** (D10):
   - `POST` body: `{"public_key": string, "label": string | null}`. Cap the body with `http.MaxBytesReader` at 32 KiB. Malformed JSON or a missing `public_key` returns `400 invalid_input`.
   - Key JSON: `uuid`, `key_type`, `fingerprint`, `public_key`, `label`, `created_at`.
   - `POST` returns `201` with the key. `DELETE` returns `204`.
   - `GET` uses `limit`/`offset` query params and returns the **same list envelope the existing list handlers emit**. Inspect `UserAccountsHandler.List` in `api/internal/handlers/user_accounts.go` and match it, including `total`.
   - All errors go through `apiresp.WriteError`, which already maps the service's `ErrForbidden`/`InvalidInput`/`Conflict` values. Do not re-decide status codes in the handler, and do not add new `server.Error` literal calls, since `make lint.api` checks for that drift.
3. **Step-up** (D8). `RegisterSelf` and `RevokeSelf` must enforce step-up exactly as `IdentitiesHandler.requireStepUp` does:
   - when `stepUpRequired` is true, verify `X-Step-Up-Token` with `localauth.VerifyStepUpToken(secret, token, uc.UserAccountID, consumed)`
   - on failure, respond via the existing `writeStepUpRequired` (`409 users.step_up_required`)
   - pass `stepUpUsed` into the service for the audit detail

   Reuse the logic without copy-paste drift. Extracting a small package-level helper both handlers call is preferred, and if you do, keep `IdentitiesHandler`'s behavior byte-identical: its existing tests must pass unchanged. The operator handlers are **not** step-up-gated.
4. **Route registration functions.** Put these in a new `api/internal/handlers/ssh_keys_routes.go`, following `self_identities_routes.go`:
   - `RegisterSelfSSHKeysReadRoute(r, h)` mounts `GET /self/ssh-keys`.
   - `RegisterSelfSSHKeysWriteRoutes(r, h)` mounts `POST /self/ssh-keys` and `DELETE /self/ssh-keys/{key_uuid}`.
   - `RegisterUserAccountSSHKeyRoutes(r, h)` mounts `GET`/`POST /user-accounts/{uuid}/ssh-keys` and `DELETE /user-accounts/{uuid}/ssh-keys/{key_uuid}`.

   Do **not** change `RegisterAccountRoutes`' signature. Doc comments state which middleware each group expects, as the identities ones do.
5. **Public facade.** In `api/handlers/handlers.go`, add:
   - `type SSHKeysHandler = inner.SSHKeysHandler`
   - a `NewSSHKeysHandler` wrapper taking the facade `usersservice.SSHKeyService` type
   - wrappers for all three `Register*` functions

   This is mandatory per AGENTS.md's Conventions (the self-route-manifest incident): every `constructor:`/`register:` symbol the manifest references must resolve through this facade.
6. **Manifest** (`moduleforge.module.yaml`). Give each entry a comment in the file's existing style.
   - `provides.services`:
     - `sshKeyService`: type `"*usersservice.SSHKeyService"`, constructor `usersservice.NewSSHKeyService`, args `infra:pool`, `queries:usersdb`, `queries:coredb`, `service:authorizer`, `service:observerGroup`. Match task 003's constructor order exactly.
     - `sshKeyResolver`: type `"*usersservice.SSHKeyResolver"`, constructor `usersservice.NewSSHKeyResolver`, args `queries:usersdb`, `queries:coredb`. Comment it as the service a composing app (for example app-mfgit) adapts into mod-repos' `transport.KeyResolver`, that no mod-users route consumes it, and that it is pre-authentication, read-only, and uncached.
     - `sshKeysHandler`: constructor `handlers.NewSSHKeysHandler`, args `service:sshKeyService`, `field:cfg.LocalAuth.JWTSecret`, `service:stepUpConsumed`, `field:cfg.Auth.RequireStepUpForCredentialChange`.
   - `provides.routes` (three new entries, all `prefix: /v1`, `handler: sshKeysHandler`):
     - self read: `register: handlers.RegisterSelfSSHKeysReadRoute`, middleware `[requireOIDCConfirmed, requireAuth]`
     - self write: `register: handlers.RegisterSelfSSHKeysWriteRoutes`, middleware `[requireOIDCConfirmed, requireAuth, requireVerifiedEmail]`
     - operator: `register: handlers.RegisterUserAccountSSHKeyRoutes`, middleware `[requireOIDCConfirmed, requireAuth, requireVerifiedEmail]`
7. **Dev server** (`api/cmd/server/main.go`, hand-written and non-generated). Construct the service and handler and mount the three groups inside the matching existing middleware groups, next to the identities routes and the account routes. Mirror the comments explaining which group each lands in.
8. **Tests** (`api/internal/handlers/ssh_keys_test.go`, and `api/handlers/` if facade tests exist there), using `httptest` and a stub service:
   - Each route is mounted at the right path and method by its `Register*` function.
   - Step-up: with `stepUpRequired=true`, `RegisterSelf`/`RevokeSelf` without a token return `409` with an `action.code` of `users.step_up_required` and never call the service. With `stepUpRequired=false`, they proceed.
   - Operator routes never require step-up.
   - Status mapping: service `ErrForbidden` returns `403`, `InvalidInput` returns `400` with detail codes preserved, `Conflict` returns `409` with `users.ssh_key_in_use`, register success returns `201` with the documented fields and no internal ids, revoke success returns `204`.
   - A malformed `key_uuid` or `uuid` returns `400`.
   - An oversized body returns `400`.

## Validation

- `cd api && go test ./...` passes, including the unchanged pre-existing `identities_test.go`/`identities_stepup_test.go`.
- `make build.api` and `make lint.api` pass.
- Facade completeness: every symbol named under `constructor:`/`register:` in the new manifest entries resolves in `api/handlers/handlers.go` or `api/usersservice/service.go`. Check with `grep -nE 'SSHKey|SSHKeys' moduleforge.module.yaml api/handlers/handlers.go api/usersservice/service.go`.
- If an `mfgen` validate or dry-run command exists in `/Users/zane/playground/moduleforge/mfgen` that can read this manifest without writing outside this worktree, run it and record the result. Otherwise state in Status that manifest resolution was checked only by inspection. Do **not** run `mfgen generate` against any consuming app.
- Manual smoke test, if the dev stack can run (`make dev.start`; see followup `MwXo` for task-worktree limits). As a verified user:
  1. `POST /v1/self/ssh-keys` with an Ed25519 key returns `201`.
  2. `GET` lists it.
  3. `POST` of the same key again returns `409`.
  4. `DELETE` returns `204`.
  5. `GET` no longer lists it.

  Record whether this was run.

## Metadata

architectural_impact: true

## Assumptions

- Task 003's facade symbols and constructor argument orders are as named in its Requirements. If task 003's Status records a deviation, follow task 003.
- mfgen prunes, rather than rejects, a `provides.services` entry (`sshKeyResolver`) that no route in this module consumes. If evidence shows otherwise, halt and report rather than adding an artificial consumer.

## References

- [SSH key design note](../notes/ssh-key-design.md): D8–D11.
- `api/internal/handlers/identities.go`: `requireStepUp`, `writeStepUpRequired`, and the thin-handler plus `apiresp.WriteError` style.
- `api/internal/handlers/self_identities_routes.go` and `account_routes.go`: registration-function style and the actual `/user-accounts` prefix.
- `api/internal/handlers/user_accounts.go`: list envelope and pagination parsing.
- `api/handlers/handlers.go`: facade alias and wrapper pattern.
- `moduleforge.module.yaml`: the identities service and route entries are the closest precedent. Note the REMINDER comment above `routes:`.
- `api/cmd/server/main.go`: identities mounting (around lines 478–603) and the account-routes group.
- AGENTS.md Conventions: facade re-export rule.

## Checkpoint hints

- After the handler and route functions pass their unit tests.
- After the facade and manifest entries are added.
- After the `main.go` wiring builds.
