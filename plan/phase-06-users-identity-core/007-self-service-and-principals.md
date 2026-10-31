# Self Service And Principals

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Add the `/v1/me` self-service handlers and services, the `/v1/principals` read, list, disable and
enable routes, guest upgrade, and the `user` and `profile` display renderers. Route mounting,
facades, manifest and old-surface deletion are task 008.

Scope: new handler files under `api/internal/handlers/` (`MeHandler`, `PrincipalsHandler`), the
`UserService` self-service and principal-administration methods and `DirectoryService` lookups
under `api/internal/service/`, display-renderer registration.

Suggested role: `plugins/flow/roles/developer-go.md`.

Dependencies within the phase: hard on 004 (identifier and credential services) and 005 (session
list, revoke, `auth_time`). Parallel-eligible with task 006. Task 008 mounts the routes.

`api/` as a whole stays uncompilable until task 008; this task makes its own packages build.

## Requirements

1. **`/v1/me`** (API surface 7): `GET`, `PUT` (`given_name`, `family_name` on the user's own
   person per U1 and U11, `default_profile_uuid`, `default_app_uuid`); `GET /profiles` (with
   availability and lock per `ProfileView`); `GET/POST /identifiers`,
   `POST /identifiers/{uuid}/verify`, `POST /identifiers/{uuid}/verification`,
   `PUT /identifiers/{uuid}/primary`, `DELETE /identifiers/{uuid}`; `GET /credentials`,
   `POST /credentials/password`, `POST /credentials/ssh-keys`, `PATCH /credentials/{uuid}`
   (`label`, `default_profile_uuid`), `DELETE /credentials/{uuid}` (409 `users.last_credential`);
   `GET /sessions`, `DELETE /sessions/{uuid}`; `POST /upgrade` (guest). `POST
   /profiles/{uuid}/accept`, `/decline`, `/leave` are phase `parties-profiles-and-bootstrap`;
   `POST /credentials/oidc/{provider}/start` is task 006's handler, mounted by task 008.
2. **Self-service authority**: authorize on `session.principal = target`, not grants. Every
   self-service write requires `auth_time` within `AUTH_SELF_SERVICE_MAX_AUTH_AGE`, else the
   action `users.step_up_required` (U8), returned as the shared `apiresp` action-required error added in mod-core phase 1 (followup `Uure`) so `apiresp.WriteError`
   renders it (409); no mod-users-local rendering.
3. **Guest upgrade** (moved here from work package 5 because its route is `/v1/me/upgrade`): in one
   transaction, clear `is_guest`, create the user's person and personal profile (U11), add the email
   identifier and password, revoke the `anon` credential with `guest_upgraded`.
4. **`/v1/principals`**: `GET /` (`kind`, paging; list through `accessible_user_ids_for_actor` /
   `accessible_service_account_ids_for_actor`), `GET /{uuid}`, `POST /{uuid}/disable` (`update` +
   step-up; revokes the principal's sessions with `principal_disabled`), `POST /{uuid}/enable`.
   Masked lookups (404 for unauthorized reads) as today. The credential sub-routes come in the
   engine phase.
5. **`DirectoryService`** `PrincipalSummaries` and `ProfileSummaries` (binding, API surface 6.3;
   resolves followup `bHKv`), with no authorization inside. Add the package-level, transaction-scoped
   `usersservice.ReadPrincipalSummaries(ctx context.Context, db usersdb.DBTX, principalIDs []int64) (map[int64]PrincipalSummary, error)`
   (binding, decision U23; mod-tokens request). It runs its queries on `db`, the caller's
   connection or transaction, never on a pool. `DirectoryService.PrincipalSummaries` delegates to
   it with the service's pool. It has the same semantics and no authorization, and unknown ids are
   absent from the map. Add any query it needs to `model/queries/` and regenerate. mod-tokens'
   phase 30 tasks consume it.
6. **Display renderers (U19)**: `user` (person name, else primary email, else "Guest") and
   `profile` ("<holder name> (Personal)" or "<holder name> @ <party name>"), registered through
   mod-core's renderer registry.
7. Export `MeHandler`, `PrincipalsHandler`, `RegisterMeRoutes`, `RegisterPrincipalRoutes` for
   task 008. Emit `user`, `user_identifier`, `credential` observer events from the writes, with
   only the ops of [API surface section 10.1](../notes/mod-users-api-surface.md) (decision U21):
   - `update` on `user` for name, defaults, guest upgrade, disable and enable;
   - `create`, `update` and `delete` on `user_identifier`;
   - `create`, `update` (label, default profile) and `revoke` on `credential`.

   Session revocations from `DELETE /v1/me/sessions/{uuid}` and principal disable go through task
   005's helpers, which emit `revoke` on `session`. No other verb is allowed.

## Validation

- `cd api && go build ./internal/handlers/... ./internal/service/...` builds the new handler and
  service files (packages may still be red only from files task 008 deletes; name them in the
  report).
- `grep -rnE '/v1/self|SelfHandler|IdentitiesHandler' api/internal/handlers --include='*.go' | grep -v _test.go`
  matches only files task 008 deletes.
- `grep -rn 'func ReadPrincipalSummaries' api/` shows the package-level function, and
  `DirectoryService.PrincipalSummaries` calls it.
- Behavior tests are task 010.

## References

- [API surface sections 6.3, 7, 11](../notes/mod-users-api-surface.md).
- [Decisions U1, U8, U11, U16, U19, U21, U23](../notes/decisions-and-resolutions.md).
- mod-tokens plan worktree, `plan/notes/mod-tokens-api-surface.md` section 7 (U-a) and
  `plan/notes/decisions-and-resolutions.md` T16: why the read must run on the caller's transaction.
- [Work packages, package 7](../notes/work-packages.md).

## Checkpoint hints

- After the `/v1/me` profile, identifier and credential routes.
- After sessions and guest upgrade.
- After `/v1/principals`, `DirectoryService` and the renderers.

## Status

Outcome: succeeded (2026-10-23).

Validation:

- `cd api && go build ./internal/handlers/... ./internal/service/...`: the new packages `internal/service`, `internal/handlers/me` and `internal/handlers/principals` build. The `internal/handlers` package itself stays red only from files task 008 deletes (`apps.go`, `assume.go`, `identities.go`, `self.go`, `ssh_keys.go`, `user_accounts.go`) plus `oidc_config.go` (`db.OidcConfig` and `db.OidcProvider` no longer exist; it is kept and needs its own port).
- The `/v1/self`, `SelfHandler`, `IdentitiesHandler` grep matches only `assume.go`, `identities.go`, `self_identities_routes.go`, `self_routes.go`, `self.go` and `ssh_keys.go`, all deleted by task 008.
- `func ReadPrincipalSummaries` appears once, in `api/internal/service/directory_service.go`; `DirectoryService.PrincipalSummaries` calls it with the service pool.
- A throwaway integration run against Postgres 16 with the composed migrations (mod-core, mod-authz, mod-users) exercised every service method and every handler route below, then was deleted (behavior tests are task 010).

Files:

- `api/internal/service/user_self_service.go`, `user_self_access.go`: `/v1/me` self-service methods and guest upgrade.
- `api/internal/service/principal_admin.go`: principal list, read, disable, enable.
- `api/internal/service/directory_service.go`: `DirectoryService`, `ReadPrincipalSummaries`, profile summaries.
- `api/internal/service/display_renderers.go`: `RegisterDisplayRenderers` for `user` and `profile`.
- `api/internal/service/session_profiles.go`: `SessionService.Profiles`.
- `api/internal/service/user_service.go`: self-service wiring fields on `UserService`.
- `api/internal/handlers/me/`: `MeHandler`, `NewMeHandler`, `RegisterMeRoutes`.
- `api/internal/handlers/principals/`: `PrincipalsHandler`, `NewPrincipalsHandler`, `RegisterPrincipalRoutes`.

Decisions:

- Self-service authority is structural: every `Self*` method reads the target from the `AuthContext` on ctx and takes no principal argument. Writes are refused (403) while impersonating, and require `auth_time` within `AUTH_SELF_SERVICE_MAX_AUTH_AGE` (409 `users.step_up_required`), checked after input validation.
- The handlers live in subpackages `handlers/me` and `handlers/principals` (like `handlers/session`) so they build while `internal/handlers` is red. Task 008 re-exports them as `MeHandler`, `PrincipalsHandler`, `RegisterMeRoutes`, `RegisterPrincipalRoutes`.
- `UserService.WithSelfService(pool, sessions, challenges, ops, maxAuthAge)` wires the self-service dependencies without changing `NewUserService`; task 008 calls it from the facade or manifest.
- The new queries are raw SQL in the service package, not in `model/queries/`: sqlc compiles `model/queries/` against the mod_users migrations alone and these read `public.*`. No regeneration was needed.
- Audit: only `create`, `update`, `delete`, `revoke` are used. Disabling or enabling a service account is an `update` on resource `service_account` (section 10.1 lists no resource for it); users use `user`.
- Guest upgrade revokes the `anon` credential through `CredentialService.RevokeInTx`, which also ends every session holding it, including the requesting one. The response is the user record and the client signs in again with the new password.
- A wrong or expired identifier verification code is a 400 field error (`users.code_invalid`), not the 401 `ErrInvalidCode`, so a client does not read it as a dead session.
