# Consumer upgrade notes

## Purpose and scope

For people who compose, embed or verify tokens from this module (the first consumer is flow-accounts), and for every host app and sibling module that consumes its Go packages, routes or tables. One section per area, each naming the exact public API, configuration or behaviour change and pointing to the detailed doc rather than repeating it. It covers the EdDSA verify-only key, token hardening, CORS, the cookie base path, the GUI, `scripts/checkout-deps.sh`, the OIDC issuer TLS default, HTTP error precedence, authorization and the identity-model rewrite (what was removed, renamed and added, with per-consumer migration notes), and ends with known limits. Design rationale lives under [Key decisions](./architecture.md#key-decisions) (D14 to D31).

## EdDSA verify-only key

Details: [jwt-eddsa.md](./jwt-eddsa.md).

EdDSA (Ed25519) is the only access-token algorithm. The earlier HS256 option and its `JWT_ALG` switch are removed (see [Identity model rewrite](#identity-model-rewrite)).

Configuration (all read by `config.Load`):

| Variable | Meaning |
|---|---|
| `JWT_SIGNING_KEY` | Ed25519 private key, PKCS#8 PEM. Optional: when unset, `config.Load` fetches or generates a key and persists it in `mod_users.auth_signing_keys` |
| `JWT_SIGNING_KID` | optional `kid` header for issued tokens |
| `JWT_VERIFY_PUBLIC_KEYS` | extra accepted public keys: one PKIX PEM, or a JSON `kid` to PEM map |
| `JWT_SECRET` | optional explicit HMAC key for the OAuth `state` parameter of the OIDC flow only; never a token key |

Public Go API, package `github.com/moduleforge/mod-users/api/auth`:

- `auth.NewVerifierWithOptions(ctx, opts...)` with the options `WithOIDC`, `WithInsecureSkipTLSVerify`, `WithLocalIssuer`, `WithEd25519PublicKey` and `WithEd25519PublicKeys`. A verify-only service passes only `WithLocalIssuer` plus public keys and holds nothing that can mint tokens. `WithHS256Secret` is removed.
- `auth.NewLocalVerifier(ctx, cfg.LocalAuth)`: the verifier matching the local-auth config. It accepts only EdDSA: the signing key's public half plus `JWT_VERIFY_PUBLIC_KEYS`.
- `auth.NewSigner(cfg.LocalAuth)`: the signer for the configured Ed25519 key.
- `auth.OAuthStateKey(cfg.LocalAuth)`: resolves the OAuth state HMAC key, returning the explicit `JWT_SECRET` when set and otherwise a key derived from the signing key. An app that builds the OIDC orchestrator itself passes the result to it and should fail startup on error. The returned key is secret: never log it. `auth.StepUpKey` is removed along with the step-up token.

The five-argument `auth.NewVerifier(ctx, issuerURL, clientID, jwtSecret, localIssuer)` is removed: build the verifier with `NewLocalVerifier` (local tokens) or `NewVerifierWithOptions` (external identity providers).

Under the persisted-key bootstrap, `JWT_SECRET` is never required. When it is unset, the OAuth state key is derived from the signing key with HKDF-SHA256 under the label `mod-users/v1/oauth-state`; an explicit `JWT_SECRET` wins. Consequences for upgrades: swapping `JWT_SIGNING_KEY` later invalidates in-flight OAuth `state` (users restart the OIDC login) unless an explicit `JWT_SECRET` is set, and a deployment that relied on the former bootstrapped HMAC secret in `auth_jwt_secrets` finds that table gone (the baseline was rewritten; see [Identity model rewrite](#identity-model-rewrite)). Details: [jwt-eddsa.md](./jwt-eddsa.md#derived-oauth-state-key) and D19. A verify-only service should not run `config.Load`: build the verifier directly with the options above.

## Token hardening

Details: [jwt-eddsa.md](./jwt-eddsa.md#signer-wiring-for-composing-apps).

- The verifier pins the algorithm to `EdDSA`: it rejects an HS256 token (including one keyed with the public key bytes), `alg: none` and every other algorithm. The token must have the carrier shape (uuid `sub` and `prof`, exactly one of `sid` and `cid`, non-empty `aud`).
- The step-up token (JWS header `typ: step-up`), its issue and verify functions and the replay cache are removed. Step-up is a factor added to the session; see [Step-up](./architecture/sessions-and-tokens.md#step-up).
- Audience tokens from `auth.IssueAudienceToken` carry `typ: aud+jwt`. The `Authenticator` rejects them as bearer tokens, so an audience token handed to a relying party can never be replayed against mod-users.
- An app that composes the services itself builds the signer with `auth.NewSigner(cfg.LocalAuth)` and hands it to the session service, and builds the verifier from the same config (`auth.NewLocalVerifier`). The generated composition root gets this from the `signer` and `verifier` services in `moduleforge.module.yaml`.
- The legacy constructors `authhandlers.NewWithSigner` and `handlers.NewAssumeHandlerWithSigner`, and `OIDCHandler.SignerErr()`, are removed: the handlers no longer sign tokens themselves, the `SessionService` does, so a bad key config fails at `auth.NewSigner`.

## CORS

Details: [cors.md](./cors.md).

- The default changed to deny. Outside `DEPLOY_MODE=local`, an unset or empty `CORS_ORIGINS` emits no CORS headers. Previously an unset value produced `Access-Control-Allow-Origin: *` together with credentials.
- Credentials (`Access-Control-Allow-Credentials: true`) are sent only with an explicit allow-list; the wildcard is never combined with credentials.
- Outside local mode, `*` and any entry containing `*` (for example `https://*.example.com`) is rejected at startup by `config.Load`, with an error naming `CORS_ORIGINS`. `server.New` also fails closed if a hand-built config reaches it.
- Deploy manifests: `deploy/k8s/base/configmap.yaml` and `deploy/cloudrun/service.yaml` ship `CORS_ORIGINS: https://app.example.com`, a placeholder you must replace with your real front-end origins.
- Local-mode caveat: with `DEPLOY_MODE=local`, a `*` pattern entry other than the bare token (for example `https://*.example.com`) is still passed to the CORS library as a pattern and reflected with credentials. Only the bare `*` (or an empty value) gets the credential-free wildcard. Do not use pattern entries, even locally, against anything you care about.

## Cookie base path

Details: [cookie-path.md](./cookie-path.md).

- New `USERS_COOKIE_PATH`: the public base path (for example `/manage`) when a proxy strips the mount prefix. The `oidc_state` cookie `Path` becomes the base plus `/v1/auth/oidc/`; unset keeps the request-derived behaviour. It is validated at startup. The `oidc_state` cookie is the only cookie the module sets.
- Facade note: `handlers.NewIdentitiesHandler` (which briefly took a trailing `cookiePath` parameter) is removed with the identities routes. The OIDC handler (`authhandlers.NewOIDCHandler`) now takes the whole `cfg *config.Config` and reads `cfg.Auth.CookiePath` itself, so a composing app passes `cfg` and nothing else.
- The OIDC provider-test redirect now keeps the prefix: with `USERS_COOKIE_PATH` set, the browser lands on `<prefix>/oidc-config` instead of `/oidc-config`. Only the validated configured value is used; `X-Forwarded-Prefix` is deliberately never trusted.

## GUI

Details: [gui/README.md](../gui/README.md), including its Migration notes.

- The client was rebuilt on the final API. Tokens are held per tab in `sessionStorage` (plus a refresh token and the active profile), never in `localStorage` or a cookie, so browsers signed in before the upgrade must sign in again. Every request sends `Authorization: Bearer` with `credentials: 'omit'`.
- One documented exception: the two OIDC authorize-URL start calls, `POST /v1/me/credentials/oidc/{provider}/start` (link a provider, `me.credentials.startOidcLink`) and `POST /v1/session/step-up/oidc/{provider}/start` (step-up, `session.stepUp.startOidc`), send `credentials: 'include'`. They set the signed `oidc_state` cookie that the redirect callback validates, and a browser discards a cross-origin `Set-Cookie` without it. The cookie is anti-forgery state, not authentication, both calls still carry the Bearer token, and the API is unchanged. A host that proxies the API from a different origin must therefore list the front-end origin in `CORS_ORIGINS` (credentials need an explicit allow-list, see [CORS](#cors)). The sign-in start is a plain browser navigation and needs no exception. See [Sessions and tokens](./architecture/sessions-and-tokens.md#client-contract).
- `AuthPage`'s `onAuthenticated` receives `(returnPath, mode)` where `mode` is `'login'` or `'register'` (type `AuthMode`); callbacks declaring fewer parameters keep working. `register` answers a session directly (`201` with a token response), so there is no follow-up login.
- OIDC returns `#handoff=<code>` (or `#error=<code>`), never a token. `OidcCallbackPage` reads those fragments and `useAuth().completeHandoff(code)` replaces `completeExternalLogin(token)`.
- `useAuth()` lost `isAdmin`, `emailVerifiedAt`, `setTokenAndUser` and `completeExternalLogin`; `useAuth().emailVerified` replaces `emailVerifiedAt`. On the wire, the `email_verified_at` timestamp is replaced by the boolean `email_verified` on `GET /v1/me` (true when the user holds at least one verified email identifier). The `UserSelf` and `LoginResponse` schemas and the `UserAccountSelf` type are gone; the token response carries `session`, `profile` and `profiles` instead.
- The client dropped `self`, `userAccounts` (including `grantAdmin`, `revokeAdmin` and `assume`), `sshKeys`, the old `stepUp` flow, `apps` and `auth.verifyEmail`. Use `me.*`, `session.*`, `impersonations.*` and `bootstrap.*`. `SSHKeysPanel` is replaced by `CredentialsPanel`, with step-up through `StepUpDialog`.
- OpenAPI (`api/openapi.yaml`) was rewritten for the new routes; see [HTTP routes](#http-routes).
- Workspace and lockfile: `gui/` has no lockfile of its own. The single committed lockfile is the repo-root `bun.lock`, covering the root Bun workspace (`gui/` plus the sibling `mod-core/gui`); build `mod-core/gui` first.

## scripts/checkout-deps.sh

Details: the header comment and `--help` of [scripts/checkout-deps.sh](../scripts/checkout-deps.sh); tests in `scripts/test-checkout-deps.sh` (run by `make test.unit`).

- The token (`MODULEFORGE_READ_TOKEN`) is read from the environment only. It travels in git's environment config (`GIT_CONFIG_*`), never in argv, the clone URL or `.git/config`, and is never echoed even under xtrace.
- `MODULEFORGE_TOKEN_ALLOWED_HOSTS` (space- or comma-separated hostnames, default `github.com`) lists the hosts that may receive the token; a lockfile host outside the list is refused (exit 3) before any network call. A self-hosted forge must be added explicitly. Not consulted without a token.
- Lockfile values (host, owner, repo, pin keys, override fields) are shape-validated when read, with fixed error messages that never echo the value.
- The auth header is scoped to the fetch target's host and `http.followRedirects` is off, so the token cannot follow a redirect to another host.
- Requirements: bash 4 or newer (the script uses `${var,,}` lowercasing; it has no explicit version check) and git 2.31 or newer for the `GIT_CONFIG_COUNT` environment config.
- The sibling repos `app-mfgit`, `app-mftodo` and `app-mfmanager` carry their own ported copies of this script, and `flow-accounts` has its own. They are not generated from this one: a future fix here must be mirrored into each copy by hand.

## OIDC issuer TLS verification

- Previously, OIDC issuer discovery over `https://` skipped TLS certificate verification. It now verifies the issuer certificate by default, and JWKS fetches follow the same rule.
- A consumer that relied on a self-signed `https://` issuer in local development must build the verifier with `NewVerifierWithOptions`, passing `WithOIDC` and the new opt-in option `WithInsecureSkipTLSVerify()`.
- `WithInsecureSkipTLSVerify` is for local development only. It logs a warning (`TLS verification is disabled for OIDC issuer; local development only`) naming the issuer host. Prefer trusting the CA instead; see [OIDC troubleshooting](./oidc-troubleshooting.md#self-signed-issuer-certificate).

## HTTP error precedence conformance

Request order on authenticated routes is 401, 400, 403, 404, then 409 or 422; see [Request order and existence masking](./mod-users-spec.md#security-requirements). The routes that first carried these changes (`/v1/user-accounts*`, `/v1/self*`, the app-membership routes, the identities and ssh-key credential routes and `UserAccountService`) were removed by the [identity-model rewrite](#identity-model-rewrite); the rules below are the ones still in force on the replacement routes. Clients: `@moduleforge/users-gui`; the apps that compose mod-users (`app-mfdemo`, `app-mfgit`, `app-mfmanager`, `app-mftodo`); and external `/v1/auth/*` clients.

HTTP changes:

- **401 first, mounted by the manifest.** The registrars (`RegisterMeRoutes`, `RegisterSessionRoutes`, `RegisterPrincipalRoutes`, `RegisterPartyRoutes`, `RegisterProfileRoutes`, `RegisterImpersonationRoutes`) do not mount authentication themselves: the manifest lists `requireOIDCConfirmed` then `requireAuth` on every authenticated prefix. A host that mounts a registrar by hand must wrap it in `auth.NewRequireAuth`; handlers mounted outside it answer 500 rather than open. A request with no live session or credential gets 401 before any handler code.
- **Existence is masked.** A principal, party or profile the caller may not read is 404, the same as an unknown one, before any 403 (D21). Admin pages that read 403 as "no such user" must read 404.
- **Strict decoding on every body route, including `/v1/auth/*`.** Unknown fields, trailing data and an empty body give 400 `invalid_input` with detail `invalid_json_body`. A body over 1 MiB gives `body_too_large` (ssh-key register: 32 KiB). Path UUID faults give detail `invalid_uuid`. The old literal "invalid JSON body" messages become fixed apiresp messages.
- **Step-up after shape checks.** On the self-service writes a malformed body, UUID or field gives 400 even with a stale `auth_time`, not 409 `users.step_up_required`. Forms that keyed off 409 for a bad body must read 400.
- **OIDC config.** `POST /v1/oidc-config/confirm` and the provider `POST`/`PUT` evaluate admin bearer, then `X-Setup-Token`, then decode the body. Documented exception: with neither credential, a malformed body gets 400 before the body `setup_token` is checked. Provider bodies are capped at 1 MiB and reject unknown keys and trailing data. `GET /v1/oidc-config/providers/{id}` with a malformed id gives 400 (was 404). The 409 for an existing provider id gains detail `users.oidc_provider_exists`; the top-level message changes to the fixed conflict text.
- **Public auth literals.** Every 500 `internal_error` carries the fixed message "an internal error occurred". Login, email-code verify and password-reset confirm required-field 400s gain field details (`users.password_required`, `users.code_required`, `users.token_required`). `GET /v1/auth/oidc/{provider}/start` no longer echoes internal error text (detail `users.oidc_start_invalid`); the callback JSON errors gain details. Credential-failure 401 messages, the register literals and the anti-enumeration `204` responses are unchanged.

## Authorization: system-actor marker, list row filter and bootstrap

For host apps and sibling modules that compose mod-users. Details: [architecture.md](./architecture.md#api-layer).

- **Unmarked contexts are gated.** The engine's gates are bypassed only for the anonymous context and for a context built with `opctx.WithSystemActor`. A context with no session, no credential and no marker now fails closed. Code that relied on such a context being exempt must either mark it (trusted system flows only, never a user's profile) or authenticate it.
- **Install access functions through the exported generator.** Pass `localAuthz.AccessFuncGenerator()` to `setup.ApplyFuncs` in the composition root, in place of `setup.NewGrantTableGenerator()`, and fail startup if it returns an error. A host that decorates the grant-table generator wraps its decorated generator with `setup.NewRowFilterGenerator(decorated, localAuthz.AccessRowFilter)` instead. The generator wraps the access functions with the predicate `mod_users.access_row_permitted(p_actor_entity_id, p_entity_id, p_owner_id, p_session_id, p_credential_id, p_system_actor)`.
- **Bind every list transaction.** Run each list query that joins an `accessible_<slug>_ids_for_actor` function in one transaction per request context, call `setup.BindAuthInputs(ctx, tx)` first and refuse the request when it returns an error. An unbound list fails closed and silently: it drops every row a restrictive party policy gates, so a list can return fewer rows than single-row reads allow. `GET /v1/principals` now omits rows owned by a party whose authentication policy the caller's session does not meet, consistent with the single-row `403`.
- **The operator is exempt from the owning-party gate.** A platform operator profile (`core_is_operator_profile`) is never bound by a customer party's `credential_rule` or `min_acr` in the party gate, on both the single-row prelude and the list predicate, so the platform can always administer, support and impersonate. Operation-level `min_acr` and step-up still apply, and non-operator outside collaborators stay gated. The exemption is keyed on the acting profile, not on the system-actor marker: `opctx.WithSystemActor` does not make a context an operator.
- **Ownership is gated one level.** A party record is its own owning party, and a record owned by a party is gated by that party's policy; the owner's owner is not followed.
- **`NewBootstrapService` gained an argument.** `usersservice.NewBootstrapService(pool, az, obs, users, creds, sessions, profiles, orgs)` takes mod-core's `OrganizationServicer` as its last parameter; the bootstrap creates the operator organization through `OrganizationService.CreateInTx`. `usersservice.OrganizationFromServices(svcs)` extracts it from mod-core's `coreServices` aggregate, and the module manifest uses it to feed `bootstrapService`. A host that calls the constructor directly passes `coreSvcs.Organization`.
- **Rebuild dev databases.** The mod-users and mod-core baselines were edited in place (new predicate and gate functions, hardened `core_is_operator_profile`, new `profile_policy_confirmations_party_id_idx` index): drop and recreate with `make clean.data`.

## Identity model rewrite

For every consumer of mod-users: host apps, mfgen-generated composition roots and sibling modules. The identity model was rewritten on mod-core's principal and profile model; there is no compatibility layer and no data migration. The module's baseline was rewritten in place, so drop and recreate development databases (`make clean.data`). The new model is described in [architecture.md](./architecture.md), [Sessions and tokens](./architecture/sessions-and-tokens.md) and the [spec](./mod-users-spec.md); this section lists what a consumer must change. Per-consumer steps are under [Migration notes by consumer](#migration-notes-by-consumer).

### Go packages

All packages are under `github.com/moduleforge/mod-users/api`, plus `model/db` (imported as `usersdb`). The facades are the only supported import surface.

`api/auth`:

| Removed or renamed | Use instead |
|---|---|
| `auth.Principal` (including `Principal.Roles`), `auth.UserContext` (including `UserContext.AppRoles`) | `auth.Identity`, read with `auth.IdentityFromContext(ctx)`; or `opctx.Auth(ctx)` for module-agnostic code. There are no roles: authority comes from grants through the acting profile |
| `auth.UserResolver`, `auth.NewUserResolver` | no replacement; the `Authenticator` resolves the caller. Just-in-time user creation moved to the OIDC callback (`auth.OIDCResolver`, `auth.NewOIDCResolver`) |
| `auth.ClaimMapper`, `auth.NewClaimMapper`, `auth.MapperOptions` | no replacement; claim mapping for external id_tokens is internal to the OIDC orchestrator, and local tokens carry the fixed claim set below |
| `auth.RequireAuth(verifier, mapper, resolver)` | `auth.NewRequireAuth(authenticator)`; build the authenticator with `auth.NewAuthenticator(pool, verifier, cfg.Auth)` or, to evaluate the invite-only app `login` gate through the authorization engine, `auth.NewAuthenticatorWithAppGate(pool, verifier, cfg.Auth, authorizer)`. Errors: `auth.ErrNoAuthHeader`, `auth.ErrInvalidToken`, `auth.ErrNotLive` |
| `auth.ResolveActorOrAnonymous`, `auth.NewResolveActorOrAnonymous` | `auth.NewResolveOrAnonymous(authenticator, anonymousActor)` (manifest middleware `resolveOrAnonymous`) |
| `auth.RequireVerifiedEmail(next)` | `auth.NewRequireVerifiedEmail()` |
| `auth.NewVerifier` (five-argument form), `auth.WithHS256Secret` | `auth.NewLocalVerifier(ctx, cfg.LocalAuth)` or `auth.NewVerifierWithOptions` |
| `auth.StepUpKey`, `auth.NewStepUpConsumedCache` | no replacement; step-up is a session factor, not a token |

Added to `api/auth`: `Identity`, `IdentityFromContext`, `Authenticator`, `NewAuthenticator`, `NewAuthenticatorWithAppGate`, `NewRequireAuth`, `NewResolveOrAnonymous`, `OIDCResolver`, `NewOIDCResolver`, and `IssueAudienceToken(signer, issuer, principalUUID, audience, ttl, extra)`, which mints the audience-bound token for offline verifiers (managed apps, relays) with header `typ: aud+jwt`, no `sid`, `cid` or `prof`, and a caller-chosen `sub` (the principal uuid). `NewLocalVerifier`, `NewSigner`, `OAuthStateKey` and `HashPassword` keep their names; `NewLocalVerifier` is EdDSA only.

`api/localAuthz`: `localAuthz.New(authzQ, opReg, pool)` keeps its signature. Added `AccessFuncGenerator()` and `AccessRowFilter`; see [Authorization](#authorization-system-actor-marker-list-row-filter-and-bootstrap). The `Authorizer` now evaluates the acting profile (not a user account), the operation's `min_acr` and `max_auth_age`, the owning party's authentication policy, and owned-scope grants.

`api/usersservice`:

| Removed or renamed | Use instead |
|---|---|
| `UserAccountService`, `NewUserAccountService` | `UserService` (`NewUserService`, `NewUserServiceWithSelfService`), plus `CredentialService`, `SessionService`, `ProfileService` |
| `SSHKeyService`, `NewSSHKeyService`, `SSHKey` | SSH keys are `ssh_key` credentials: `CredentialService` and the `/v1/me/credentials` and `/v1/principals/{uuid}/credentials` routes |
| `SSHKeyResolver.ResolveActor` | `SSHKeyResolver.ResolveCredential(ctx, key)` (returns an `SSHCredential`), then `SSHKeyResolver.AuthContextForOwner(ctx, cred, ownerID)`, which derives the acting profile from the repository owner and returns the `opctx.AuthContext` to install. `ErrSSHKeyNotAuthorized` (read the party with `SSHKeyNotAuthorizedParty`) replaces the old forbidden outcome |
| `ResolveActor` anywhere else | the same two calls |
| `usersdb.UserAccount` lookups for display | `DirectoryService.PrincipalSummaries` and `ProfileSummaries`, or `usersservice.ReadPrincipalSummaries(ctx, tx, ids)` on a caller's own transaction or connection (no second pool connection) |

Added to `api/usersservice` (binding for sibling modules):

- `CredentialService.IssueAPIKeyInTx` creates a service account's API key (client id plus a once-shown secret); `RevokeInTx` revokes a credential and ends the sessions holding it. `CredentialService.LinkOIDCInTx` creates an active `oidc` credential for a known `(issuer, subject)` with no authorization (the caller authorizes), never links by email, and refuses with `409 users.oidc_identity_in_use` when an active credential already holds the pair (the `ErrOIDCIdentityInUse` sentinel is not re-exported by the facade; test the `apiresp` conflict).
- `ProfileService.InviteInTx` invites a user into a party (authorizes `create` on `profile` in the party; `ConfirmPolicyMismatch` needs `update` on the party). `ProfileService.AdmitInTx` trusted-admits a holder: it returns the holder's active profile, activates an invited one or creates one, with no authorization and no policy-mismatch check, and fires lifecycle observers. `ProfileService.EndInTx` ends a profile with departure cleanup. Refusals: `ErrGuestMustUpgrade`, `ErrHolderNotAdmissible`, `ErrProfileSuspended`.
- `ProfileLifecycleObserver` (register with `ProfileService.AddLifecycleObserver`) receives every profile status change in the changing transaction; this is how seat-holding modules release seats.
- `PersonalPartyService.EnsurePersonalParty(ctx, tx)` creates or returns the acting user's personal party (a person record plus an owned-scope grant) for personal team plans. Visibility rule: the grant is invisible to the production `Authorizer` until the transaction commits, because the `Authorizer` reads through its own pool. A caller that must authorize against that grant commits the call's transaction first (the call is idempotent) and authorizes afterwards, in its own committed transaction rather than the plan-creation transaction. Errors: `ErrGuestMustUpgrade`, `ErrPersonalProfileRequired`, `ErrPersonalPartyNotOwned`.
- `DirectoryService.PrincipalSummaries`, `DirectoryService.ProfileSummaries` and `ReadPrincipalSummaries` perform batch display lookups with no authorization; authorize first.
- `CollaboratorGate` (`NewCollaboratorGate`) implements mod-authz's outside-collaborator hook; the composition root injects it into the grant service.
- `UserService`, `SessionService`, `ImpersonationService`, `PartyPolicyService`, `ChallengeService`, `BootstrapService` (with `OrganizationFromServices`), their input and error types, `RegisterDisplayRenderers`, and the SQLSTATE helpers `IsSessionInvariantError`, `IsCredentialInvariantError` and `IsUserInvariantError`.

`api/handlers` and `api/handlers/authhandlers`:

| Removed | Use instead |
|---|---|
| `UserAccountsHandler`, `NewUserAccountsHandler`, `RegisterAccountRoutes` | `PrincipalsHandler` (`NewPrincipalsHandler`, `RegisterPrincipalRoutes`) |
| `AssumeHandler` (`NewAssumeHandler`, `NewAssumeHandlerWithSigner`) and `GrantAdminFn` | `ImpersonationHandler` (`NewImpersonationHandler`, `RegisterImpersonationRoutes`); administrator rights are operator-party profiles plus grants |
| `AppsHandler`, `NewAppsHandler` | app membership is a `login` grant through mod-authz's grants API |
| `SelfHandler`, `NewSelfHandler`, `RegisterSelfGetRoute`, `RegisterSelfPutRoute`, `IdentitiesHandler`, `NewIdentitiesHandler`, `RegisterSelfIdentities*Route(s)`, `SSHKeysHandler`, `NewSSHKeysHandler`, `RegisterSelfSSHKeys*Route(s)`, `RegisterUserAccountSSHKeyRoutes` | `MeHandler` (`NewMeHandler`, `RegisterMeRoutes`), which also takes the OIDC link starter |
| `authhandlers.FirstUserHookFn`, `authhandlers.NewWithSigner`, the `SetFirstUserHook` setters, the `api/users` package (`users.NewFirstUserHook`) | `BootstrapHandler` and `BootstrapService` (see [Bootstrap](#bootstrap-and-first-administrator)) |

Added: `MeHandler`, `SessionHandler` (`RegisterSessionRoutes`, taking the OIDC step-up starter), `PrincipalsHandler`, `PartiesHandler` (`RegisterPartyRoutes`, `RegisterProfileRoutes`), `ImpersonationHandler`, `BootstrapHandler` (`RegisterBootstrapRoutes`), `OperatorAdminCheck` with `NewOperatorAdminChecker`, `PrintSetupToken`. `OIDCConfigHandler` and `ProvidersHandler` are kept (their queries now return the `usersdb` row types listed below). The constructors that stay changed shape: `authhandlers.New` now takes the user, credential, challenge and session services in place of a JWT secret and signer, `authhandlers.NewOIDCHandler` takes the same services plus an `auth.OIDCResolver`, the observer group and `cfg`, and `authhandlers.RegisterRoutes` takes the `OIDCRoutes` interface (pass an untyped `nil` when OIDC is not configured). `OIDCHandler.SignerErr` is removed.

`model/db` (`usersdb`, sqlc output): removed `UserAccount` and every `*UserAccount*` query, the SSH-key, anon-token, email-code, password-reset, OIDC-identity, app-membership and JWT-secret queries, `GetSystemActorBySlug` and `GetSystemActorBySlugRow` (use mod-core's `coredb.GetSystemActorBySlug`, which also returns the personal profile id), and the `OidcConfig` and `OidcProvider` model types: `GetOIDCConfig`, `GetOIDCProvider`, `ListOIDCProviders` and `UpsertOIDCProvider` return `GetOIDCConfigRow`, `GetOIDCProviderRow`, `ListOIDCProvidersRow` and `UpsertOIDCProviderRow`, so fake queriers in tests must change. Added `User` (a row of `mod_users.users`), `GetUserByEntityID`, `GetSessionByUUID`, `GetCredentialByUUID` and the queries over the new tables. Binding for consumers: `usersdb.User`, `GetUserByEntityID`, `GetSessionByUUID`, `GetCredentialByUUID`.

Test helpers: `ordertest.Actor`, `ordertest.Sudo` and `ordertest.SudoOnly` are deleted by mod-core; use `AuthContext`-based identities (a profile identity, an impersonating identity, an anonymous `AuthContext`). `ordertest.Anonymous` still means "no `AuthContext`".

### HTTP routes

The authoritative reference is [`api/openapi.yaml`](../api/openapi.yaml). Removed routes answer `404`.

| Removed | Replaced by |
|---|---|
| `/v1/self`, `/v1/self/identities*`, `/v1/self/credential*`, `/v1/self/ssh-keys*` | `/v1/me` (read and update), `/v1/me/identifiers*`, `/v1/me/credentials*` (password, SSH keys, OIDC link start, label and default profile, revoke), `/v1/me/profiles*`, `/v1/me/sessions*`, `POST /v1/me/upgrade` (guest upgrade) |
| `/v1/user-accounts*` (including `grant-admin` and `revoke-admin`, the per-account SSH-key routes and `POST .../assume`), and the OpenAPI-only `/v1/users*` family | `/v1/principals*` (list, read, disable and enable, credential administration under the `credentials` operation); administrator rights are operator-party profiles plus grants |
| `/v1/assume`, `DELETE /v1/assume` | `/v1/impersonations` (start), `/v1/impersonations/current/renew`, `DELETE /v1/impersonations/current` |
| `/v1/apps/{uuid}/user-accounts*` (`/v1/apps/{app_uuid}/user-accounts*` in the OpenAPI document; app membership and roles) | a `login` grant on the app entity through mod-authz's grants API |
| `/v1/anonymous-demo` | none |
| `POST /v1/auth/email-code` and `POST /v1/auth/password-reset` | `POST /v1/auth/email-code/request` and `POST /v1/auth/password-reset/request` |
| `/v1/users/{user_uuid}/audit` | `GET /v1/audit` and `GET /v1/audit/{resource_type}/{resource_uuid}` (served by mod-audit) |

Added (all behind `Authorization: Bearer` unless noted):

- `/v1/me*`: as above, plus `POST /v1/me/identifiers/{uuid}/verify`, `.../verification`, `PUT .../primary`, `POST /v1/me/profiles/{uuid}/accept`, `/decline` and `/leave`. Self-service writes need a recent `auth_time` (`AUTH_SELF_SERVICE_MAX_AUTH_AGE`).
- `/v1/session*`: `GET /v1/session`, `POST /switch`, `/fork`, `/logout`, and `/step-up/password`, `/step-up/email-code/request`, `/step-up/email-code/verify`, `/step-up/oidc/{provider}/start`.
- `/v1/principals*`, `/v1/impersonations*`, `/v1/profiles/{uuid}` (`PATCH`), and `/v1/parties/{party_uuid}/profiles`, `/managed-users`, `/authn-policy`, `/credential-authorizations*`, `/confinements/{credential_uuid}`.
- Public: `POST /v1/bootstrap`, `GET /v1/bootstrap/status`, `POST /v1/auth/token` (`refresh_token` and `client_credentials` grants), `POST /v1/auth/handoff` (redeems the OIDC handoff code).
- Unchanged: `/v1/auth/register`, `/login`, `/anonymous`, `/email-code/verify`, `/password-reset/confirm`, `/providers`, `/oidc/{provider}/start` and `/callback`, `/v1/oidc-config*`, `/healthz` and `/readyz`. Every sign-in now answers with the token response (access token, per-tab refresh token, session, landing profile and profile list), not a bare token.
- New error and action codes include `users.session_revoked`, `users.profile_unavailable`, `users.refresh_reuse`, `users.step_up_required` (the shared action-required `409`), `users.app_login_not_granted`, `users.impersonation_not_allowed`, `users.last_credential`, `users.already_bootstrapped` and `users.setup_token_invalid`. OIDC redirects carry `#handoff=<code>`, `#linked=1`, `#stepped_up=1` or `#error=<code>` (`account_exists_sign_in_and_link`, `no_account`, `authentication_failed`, `missing_email`).

### Configuration

| Change | Variables |
|---|---|
| Removed | `JWT_ALG` (HS256 is gone), `AUTH_REQUIRE_STEP_UP` (step-up is a session factor, with no per-deployment switch), `AUTH_ADMIN_ROLE` (the admin-role claim setting; there are no roles) |
| Changed | `JWT_SECRET` is optional and keys the OAuth `state` parameter only. `JWT_SIGNING_KEY` is optional: when unset, a key is generated and persisted in `mod_users.auth_signing_keys` (this replaces the `auth_jwt_secrets` bootstrap and needs a resolvable database URL) |
| Added (default) | `AUTH_ACCESS_TOKEN_TTL` (`1h`, at most `1h`), `AUTH_SESSION_MAX_AGE` (`720h`), `AUTH_SESSION_IDLE_TIMEOUT` (`168h`, refresh-lineage expiry), `AUTH_CLIENT_CREDENTIALS_TTL` (`15m`), `AUTH_IMPERSONATION_TTL` (`30m`), `AUTH_IMPERSONATION_MAX` (`60m`), `AUTH_SELF_SERVICE_MAX_AUTH_AGE` (`10m`), `AUTH_DEFAULT_AUDIENCE` (`mod-users`), `AUTH_ACCEPTED_AUDIENCES` (unset: the default audience plus any registered app slug) |
| Kept | `JWT_SIGNING_KID`, `JWT_VERIFY_PUBLIC_KEYS`, `LOCAL_ISSUER`, `NO_OIDC_ACCOUNTS`, the OIDC provider variables, `USERS_COOKIE_PATH`, `CORS_ORIGINS`, `DEPLOY_MODE` |

Go config: `AuthConfig.AdminRole` and `AuthConfig.RequireStepUpForCredentialChange` and `LocalAuthConfig.JWTAlg` are removed; `AuthConfig` gained the lifetime fields, `DefaultAudience` and `AcceptedAudiences`.

### Tokens, headers and cookies

- Access tokens are EdDSA JWTs only. The carrier claims are `iss`, `aud`, `sub` (the principal uuid; under impersonation the impersonated holder's), `sid` (session uuid) or `cid` (client-credentials credential uuid), `prof` (acting profile uuid), `acr`, `amr`, `auth_time`, `act` (`{"sub": "<administrator principal uuid>"}` only while impersonating), `iat` and `exp`. There are no `email`, `roles`, `sudo_user_uuid` or `is_anonymous` claims. The token is a carrier: the database session row is authoritative for `acr` and `auth_time`, and liveness is checked on every request. Details: [jwt-eddsa.md](./jwt-eddsa.md#claims).
- Access tokens live at most one hour (`AUTH_ACCESS_TOKEN_TTL`). Each browser tab owns a refresh lineage (opaque, single-use refresh tokens; reuse revokes the session); a new tab calls `POST /v1/session/fork` with a sibling tab's access token. See [Refresh lineages](./architecture/sessions-and-tokens.md#refresh-lineages).
- The `X-Step-Up-Token` request header and the step-up token are removed. A stale or low-assurance request answers `409 users.step_up_required`; the client runs a step-up and retries once.
- No authentication cookie: the API accepts `Authorization: Bearer` only. `oidc_state` is the only cookie the module sets, and it is anti-forgery state for the OIDC redirect. Browser clients send `credentials: 'omit'`, except the two OIDC authorize-URL start calls, which send `credentials: 'include'` (see [GUI](#gui)).
- The OIDC callback redirects with `#handoff=<code>` (a 60-second, single-use code bound to a verifier the browser generated at start); the browser redeems it with `POST /v1/auth/handoff`. A token, or anything that refreshes one, is never placed in a redirect URL. See [The OIDC handoff code](./architecture/sessions-and-tokens.md#the-oidc-handoff-code).
- The anonymous sign-in (`POST /v1/auth/anonymous`) creates a real guest user and session; the `anon_tokens` table and the `is_anonymous` claim are gone.

### Database

The baseline `0100_baseline.sql` was rewritten in place; `0101` to `0104` are folded in and deleted. A database that recorded earlier versions must be dropped and recreated.

- Every table, function and trigger is in the `mod_users` schema (SSH keys already were). New tables: `users`, `user_identifiers`, `credential_kinds`, `credentials` with `cred_password`, `cred_email_otp`, `cred_oidc`, `cred_ssh_key`, `cred_api_key` and `cred_anon`, `auth_challenges`, `party_authn_policies`, `credential_party_authorizations`, `sessions`, `session_factors`, `session_refresh_tokens` and `auth_signing_keys`. `oidc_config` and `oidc_providers` moved into the schema; providers gained `party_id`, `jit_mode` and `trust_acr`.
- Deleted: `user_accounts`, `auth_local`, `email_codes`, `password_resets`, `auth_oidc_identities`, `anon_tokens`, `apps_user_accounts`, `mod_users.ssh_public_keys`, mod-users' copy of `system_actors` (the system actors are mod-core's), `auth_jwt_secrets`, the `entities_no_system_actor_owner` guard (mod-core's owner-domain rule subsumes it) and `create_email_code_with_cooldown` (now `mod_users.create_challenge_with_cooldown`).
- A user's name now lives on the user's `natural_person` record, and a user's email on `mod_users.user_identifiers`. App membership is a `login` grant, not a join table.
- Read surface: `mod_users.user_identifiers` (columns `user_id`, `kind`, `value_canon`, `verified_at`, `is_primary`) and `mod_users.users` (`entity_id`, `person_id`, `is_guest`) are read directly by sibling modules (mod-billing, mod-tokens). The table name, those columns and their meanings, including the canonical email form (trimmed, lowercased), are binding; other modules only read and every write goes through this module's services. A rename or change of meaning is announced to those consumers.
- SQLSTATE codes: `MF101` (session invariant), `MF102` (credential invariant) and `MF103` (user invariant), with `usersservice.IsSessionInvariantError`, `IsCredentialInvariantError` and `IsUserInvariantError`. They are distinct from mod-core's `MF001` to `MF005`. Unique violations stay `23505` and map to `409` by index: `user_identifiers_kind_value_uq` (`users.identifier_taken`), `cred_oidc_active_issuer_subject_uq` (`users.oidc_identity_in_use`), `cred_ssh_key_active_fingerprint_uq` (`users.ssh_key_in_use`) and mod-core's `profiles_one_open_per_holder_party` (`users.profile_exists`).
- Operational: the baseline issues no `GRANT`; a host's runtime role needs `USAGE` on `mod_users` and table and sequence privileges, and backup, reset and test-teardown tooling must include the `mod_users` schema. See [Operational requirements for composing hosts](./architecture.md#data-model).

### Manifest (`moduleforge.module.yaml`)

- Removed: `hooks.firstUserGrant` and the `closure:grantAdminFn` and `closure:revokeAdminFn` arguments; services `claimMapper`, `userResolver`, `userAccountService`, `assumeHandler`, `appsHandler`, `usersHandler`, `selfHandler`, `identitiesHandler`, `sshKeyService`, `sshKeysHandler` and `stepUpConsumed`; middleware `resolveActorOrAnonymous`; and the routes for the removed handlers.
- Added services: `authenticator`, `signer`, `challengeService`, `credentialService`, `sessionService`, `impersonationService`, `userService`, `profileService`, `partyPolicyService`, `personalPartyService`, `collaboratorGate`, `bootstrapService`, `bootstrapOrganizationService`, `operatorAdminChecker`, `directoryService`, `oidcResolver`, `meHandler`, `sessionHandler`, `impersonationHandler`, `principalsHandler`, `partiesHandler` and `bootstrapHandler`. Kept: `authorizer`, `oauthOrchestrator`, `verifier` (now `auth.NewLocalVerifier`), `anonymousActor`, `sshKeyResolver`, `onboardingHandler`, `authHandler`, `oidcHandler` and `providersHandler`.
- Middleware: `requireAuth` (now `auth.NewRequireAuth`), `resolveOrAnonymous` (replaces `resolveActorOrAnonymous`), `requireVerifiedEmail`, `requireOIDCConfirmed`.
- Routes: `/v1/auth`, `/v1/oidc-config`, `/v1/bootstrap`, `/v1/session`, `/v1/impersonations`, `/v1/me`, `/v1/principals`, `/v1/parties` and `/v1/profiles`. Every authenticated prefix lists `requireOIDCConfirmed` then `requireAuth`.
- New `requires.services`: `naturalPersonService`, `displayRegistry` and `coreServices`, all from mod-core. A host must supply them.
- Display renderers for the `user` and `profile` entity types are registered on mod-core's display registry.

### Bootstrap and first administrator

The first-user hook is removed: registering first no longer grants administrator rights. A fresh deployment prints one setup token (`TOKEN_DISPLAY`); `POST /v1/bootstrap` with `X-Setup-Token` creates, in one transaction, the first user, the operator party, the administrator profile with a wildcard `manage` grant, the operator party's authentication policy and a session. A host that previously cleared the hook with a startup hook calling `SetFirstUserHook(nil)` deletes that code. See [Setup-token bootstrap](./architecture/sessions-and-tokens.md#setup-token-bootstrap).

### Impersonation

`POST /v1/assume` is replaced by a separate child session of the administrator's own session. Consumers that act under it should rely on these rules (see [Impersonation sessions](./architecture/sessions-and-tokens.md#impersonation-sessions) and [Impersonation lifecycle](./architecture.md#api-layer)):

- The impersonated actor's rules apply to every operation except stopping the impersonation: the impersonated profile's authorizations and every party's authentication policy decide what the session may do. The administrator's own rights do not widen it.
- Nested impersonation is disallowed by construction: a database invariant (`MF101`) rejects an impersonation whose parent is itself an impersonation. `POST /v1/impersonations` also refuses a caller already impersonating, and a profile the caller holds, with `403 users.impersonation_not_allowed`.
- The impersonation token has no refresh token and is never refreshed. When it expires, a client drops it and restores the administrator's own token through the administrator's refresh lineage. It must not retry the failed request under the administrator's token, which would run the request as the administrator.
- Logout while impersonating: `POST /v1/session/logout` revokes the session of the token presented, which is the impersonation child. A client that signs a tab out while impersonating restores the administrator's token and logs that session out, which revokes the administrator session and, with it, the impersonation. The reference GUI does this.
- Credential and identification data is off limits to an impersonating context (credential administration, managed-user creation, party policy and credential-authorization writes, ending a profile). A general data-protection-class design is planned (followup `H8qi`) and is not built; see [Known limits](#known-limits-and-open-items).
- Audit: the administrator is the impersonator on every request made inside the session; start, renew and end are observed as `impersonate`, `update` and `revoke` on resource `impersonation`.

### Migration notes by consumer

- **mod-audit.** Every request `AuthContext` from mod-users carries `ProfileID` (the actor), `PrincipalID`, `ImpersonatorPrincipalID`, `SessionID` (`mod_users.sessions.id`) and `CredentialID` (`mod_users.credentials.id`), never both `SessionID` and `CredentialID` (satisfies the `audit_log_session_or_credential` check). Use no foreign keys from `audit_log` to mod-users tables: audit history is append-only. mod-users observes the resources `user`, `user_identifier`, `credential`, `session`, `profile`, `party_authn_policy`, `credential_party_authorization`, `impersonation`, `bootstrap` and, for its trusted grant inserts, `grant`. Ops are limited to mod-audit's vocabulary (`create`, `update`, `delete`, `impersonate`, `login`, `grant`, `revoke`); `assume` is gone with no alias. Flows that run before any `AuthContext` exists (registration, login, anonymous sign-in, email-code and password-reset, the OIDC callback, refresh reuse, bootstrap) install an `AuthContext` for their observations built from the profile the flow lands on or creates, else the principal's personal profile, else a managed user's oldest non-ended profile; a failed sign-in writes no observation.
- **mod-tokens.** Replace `usersdb.UserAccount` lookups with principal ids (`public.principals`) and `DirectoryService.PrincipalSummaries` for display. On a path that already holds a transaction (the mint path), use `usersservice.ReadPrincipalSummaries(ctx, tx, ids)` on that transaction. Refuse a refresh when `public.principals.disabled_at` is set. The device-approval route sits behind `auth.NewRequireAuth` and reads `opctx.Auth(ctx).PrincipalID`. The documented fallback that reads the primary email reads `mod_users.user_identifiers`.
- **mod-billing.** The system actor `billing.paddle_sync` comes from mod-core's `core_ensure_system_actor`. Seats hold profile ids. Admit the holder with `ProfileService.AdmitInTx`; authorize an invitation on mod-billing's side with `AuthorizeTypeIn(create, profile, party)`; release seats through a `ProfileLifecycleObserver`. Personal team plans call `PersonalPartyService.EnsurePersonalParty` in their own committed transaction before authorizing, not in the plan-creation transaction (commit-first visibility rule above). Holder display goes through `DirectoryService.ProfileSummaries`. mod-billing reads `mod_users.users (entity_id, person_id, is_guest)` and the `user_identifiers` read surface directly for invitation matching, trial claims and the Paddle customer email.
- **mod-workflows and mod-tasks.** No direct mod-users API: actors come from `opctx.AuthContext`. Test seeds must create users through mod-users' services, or SQL that satisfies the schema invariants: a bring-your-own user needs its personal profile in the same transaction.
- **mod-repos.** Replace `ResolveActor` with `ResolveCredential` plus `AuthContextForOwner` (passing the repository owner id) and install the returned `AuthContext`. Anonymous HTTP reads keep using `resolveOrAnonymous` (`auth.NewResolveOrAnonymous`). See [D11](./architecture/ssh-keys.md#d11-resolver-consumer-contract) for the resolver contract.
- **app-mfmanager.** Managed-app owner provisioning pre-creates the owner's OIDC link with `CredentialService.LinkOIDCInTx` instead of generated `usersdb` inserts. `app_service_identity` provisioning creates the service account with mod-core's `CreateServiceAccountInTx` and its API key with `CredentialService.IssueAPIKeyInTx`; runtime login is `POST /v1/auth/token` with `grant_type: client_credentials` and a client id, replacing the `MFSVC_ACCOUNT_EMAIL` password login. Managed-app owner tokens use `auth.IssueAudienceToken` with `sub` set to the principal uuid. Move `auth.UserResolver`, `auth.Principal`, `auth.UserContext` and `usersdb.UserAccount` uses to `auth.Identity`, `auth.Authenticator` and principal ids.
- **Non-federated host apps (app-mftodo, app-mfgit, app-mfdemo) and mfgen.** The service, middleware and route names in [Manifest](#manifest-moduleforgemoduleyaml) replace the earlier ones; regenerate the composition root, supply the new `requires.services`, and delete any startup hook that cleared the first-user hook. Update browser clients to the new GUI contract ([GUI](#gui)).

## Known limits and open items

- Local-mode CORS pattern reflection: see the caveat under [CORS](#cors). Pattern entries are not rejected in `DEPLOY_MODE=local` and are still reflected with credentials.
- Impersonation data protection is a fixed list, not a general mechanism. An impersonating context is refused credential administration, managed-user creation, party policy and credential-authorization writes and ending a profile; see [Impersonation](#impersonation). A general **data protection class** design (planned, followup `H8qi`, **not built**) would replace these refusals with per-data `no-write`, `no-read` and `no-access` marks checked only when a function touches marked data. Until it exists, the listed refusals are the whole of the protection.
- The `usersservice` facade does not yet export `IssueAPIKeyInput`, `IssuedAPIKey` or `ProfileSummary`; a caller outside `api/` can call `CredentialService.IssueAPIKeyInTx` and `DirectoryService.ProfileSummaries` but cannot name those types until aliases are added.
- The CORS middleware (`api/internal/server/server.go`) still lists the removed `X-Step-Up-Token` among its allowed request headers; the header no longer exists and the entry is harmless.
