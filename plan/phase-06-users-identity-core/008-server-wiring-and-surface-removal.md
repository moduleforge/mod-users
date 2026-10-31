# Server Wiring And Surface Removal

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Delete the superseded handlers, services and packages, rewire the public facades, the dev server,
the module manifest and the OpenAPI document onto the routes tasks 005 to 007 built, and restore
`make build` for the whole of `api/`. This is the task that closes the uncompilable window opened by
task 002.

Scope: deletions listed below; `api/handlers/handlers.go`, `api/handlers/authhandlers/`,
`api/usersservice/`, `api/auth/auth.go` (only if task 003 left gaps), `api/cmd/server/main.go`,
`moduleforge.module.yaml`, `api/openapi.yaml`.

Suggested role: `plugins/flow/roles/developer-go.md`.

Dependencies within the phase: hard on 005, 006 and 007. Task 010 depends on this task.

## Requirements

1. **Delete** `api/internal/handlers/{self,identities,user_accounts,assume,apps,ssh_keys}*.go` (non-test
   and test files), `account_routes.go`, `self_routes.go`, `self_identities_routes.go`,
   `ssh_keys_routes.go`, `api/internal/service/{user_accounts,ssh_keys}*.go` if any remain,
   `api/internal/users/`, `api/users/`, and the first-user hook (U17:
   `users.NewFirstUserHook`, `FirstUserHookFn`, `SetFirstUserHook`).
2. **Mount routes**: `/v1/auth` (task 005 and task 006's OIDC start and callback), `/v1/session`
   (task 005 plus task 006's step-up OIDC start), `/v1/me` (task 007 plus task 006's link start),
   `/v1/principals` (task 007). Removed routes of API surface 7 return 404.
3. **Facades** (API surface 6.3, 6.4, binding names): `api/handlers/handlers.go` and
   `api/handlers/authhandlers/` re-export `MeHandler`, `SessionHandler`, `PrincipalsHandler`,
   `OIDCConfigHandler`, `ProvidersHandler`, `authhandlers.Handler`, `authhandlers.OIDCHandler` and
   `RegisterMeRoutes`, `RegisterSessionRoutes`, `RegisterPrincipalRoutes`,
   `RegisterOIDCConfigRoutes`, `authhandlers.RegisterRoutes`; remove `UserAccountsHandler`,
   `AssumeHandler`, `AppsHandler`, `GrantAdminFn`, `SelfHandler`, `IdentitiesHandler`,
   `SSHKeysHandler`. `api/usersservice/` exposes the phase-6 services (`UserService`,
   `CredentialService`, `SessionService`, `DirectoryService`, `SSHKeyResolver`) and SQLSTATE
   helpers. It also exposes the consumer-requested binding additions: `CredentialService.LinkOIDCInTx`
   with `LinkOIDCInput` and `LinkedCredential` (decision U24, from task 004), and the package-level
   `ReadPrincipalSummaries` (decision U23, from task 007). Remove `ResolveActor`, `UserAccountService`, `NewUserAccountService`, `SSHKeyService`,
   `SSHKey`. Party, impersonation and bootstrap facades come in later phases.
4. **`api/cmd/server/main.go`**: new services, middleware and handlers; slug list per U18
   (`user`, `profile`, `organization` join `authzSlugs`; `corporation` leaves); no first-user hook;
   no grant-admin closures.
5. **`moduleforge.module.yaml`** (API surface 11, binding): the services, handlers and middleware
   this phase provides (`verifier` as `auth.NewLocalVerifier`, `authenticator`, `anonymousActor`,
   `userService`, `credentialService`, `sessionService`, `directoryService`, `sshKeyResolver`,
   `authHandler`, `oidcHandler`, `meHandler`, `sessionHandler`, `principalsHandler`,
   `providersHandler`, `onboardingHandler`, middleware `requireOIDCConfirmed`, `requireAuth`,
   `resolveOrAnonymous`, `requireVerifiedEmail`); remove every item section 11 lists as removed,
   including `hooks.firstUserGrant` and the `closure:grantAdminFn`/`revokeAdminFn` args. Entries for
   services from later phases are added by those phases.
6. **`api/openapi.yaml`**: every route this phase serves, the token response and `ProfileView`
   schemas, the new error codes; removed routes deleted; Bearer as the only security scheme.
7. Update the `README.md` and `AGENTS.md` rows that describe the removed routes, facades, manifest
   entries and the first-user hook.
8. If mod-authz phase 12's or mod-audit phase 20's `AuthContext` adoption has not merged, only
   `api/cmd/server` may stay red; say so in the report. This is a task-level allowance only. Phase
   6 cannot close until both have merged (mod-authz H2, mod-audit E2), and mod-authz phase 13 must
   merge as well (decision U27). Until phase 13 merges, the dev server's `setup.ApplyFuncs` fails
   at startup, because the generated functions read the new grant columns. Report which of the
   three have merged.

## Validation

- `make build` passes (or only `api/cmd/server` is red for the reason in requirement 8).
- `grep -n 'LinkOIDCInTx\|ReadPrincipalSummaries' api/usersservice/*.go` shows both re-exports.
- `cd api && go vet $(go list ./... | grep -v /cmd/server)` may report test-file errors only;
  task 010 migrates those tests. Non-test code is clean.
- `make lint` reports no finding in non-test code (findings in `_test.go` files that task 010
  migrates are listed in the report).
- `grep -rnE 'UserAccount|user_accounts|FirstUserHook|firstUserGrant|grantAdminFn|AssumeHandler|/v1/self|/v1/assume|/v1/user-accounts' api moduleforge.module.yaml --include='*.go' --include='*.yaml' | grep -v _test.go`
  returns nothing.
- mfgen manifest validation used by the project (see `AGENTS.md`) passes for
  `moduleforge.module.yaml`.

## Metadata

architectural_impact: true

## References

- [API surface sections 6.3, 6.4, 7, 11](../notes/mod-users-api-surface.md).
- [Decisions U16, U17, U18](../notes/decisions-and-resolutions.md).
- [Work packages, package 7](../notes/work-packages.md).
- `AGENTS.md` — the facade re-export convention for manifest-referenced constructors.

## Checkpoint hints

- After deletions and route mounting build.
- After the facades and `main.go`.
- After the manifest and OpenAPI document.

## Status

- Outcome: succeeded (2026-10-23).
- `cd api && go build ./...` and `make build.api` pass, including `cmd/server`. `go vet` and `make lint` report only `_test.go` errors (task 010 migrates them).
- Deleted the superseded handlers, services, `api/internal/users`, `api/users` and their tests. Ported `api/internal/handlers/oidc_config.go` to the new db row types.
- Facades: `api/handlers/handlers.go`, `api/handlers/authhandlers/authhandlers.go`, `api/usersservice/service.go`, `api/auth/auth.go` (`OIDCResolver`, `NewOIDCResolver`).
- `cmd/server/main.go` mounts `/v1/auth`, `/v1/session`, `/v1/me`, `/v1/principals` and `/v1/oidc-config`; OIDC link and step-up starts mounted; slugs per U18.
- `moduleforge.module.yaml` rewritten; mfgen validation passes against a scratch app manifest (generated `main.go` wires every new service and route).
- `api/openapi.yaml` rewritten for the served routes, token response and `ProfileView`.
- Remaining: `api/handlers/onboarding_boot_test.go` and other tests fail to compile until task 010. Display renderers are not expressible in the manifest (finding filed).
