# Token Carrier And Auth Middleware

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Replace the stateless HS256/EdDSA local JWT, `Principal`/`UserContext` and the actor/sudo plumbing
with the 1-hour EdDSA carrier token, `auth.Identity`, the `Authenticator` with per-request liveness,
and `opctx.AuthContext` population on every authenticated and anonymous request; port
`api/internal/authz/authz.go` minimally to the profile actor. Every later phase-6 task consumes this
task's types.

Scope: `api/internal/auth/` (except the OIDC resolver and orchestrator logic, task 006),
`api/internal/config/` (signing, new variables), `api/auth/auth.go` facade,
`api/internal/authz/authz.go` with its `authz_test.go` and `export_test.go`, and every
`opctx.WithActor`/`ActorEntityID`/sudo call site.

Suggested role: `plugins/flow/roles/developer-go.md`; `plugins/flow/roles/architect-backend.md`
for the authentication-layer shape.

Dependencies within the phase: hard on 002 (`usersdb` liveness and signing-key queries). Tasks 004
to 007 and 009 depend on this task.

`api/` as a whole stays uncompilable until task 008; this task makes `api/internal/auth`,
`api/internal/config` and `api/internal/authz` build.

## Requirements

1. **Carrier (U4, API surface 5).** EdDSA only: delete HS256 signing and verification, `JWT_ALG`,
   `WithHS256Secret` and the 5-argument `NewVerifier`. `signer.go` signs the claims of section 5
   (`iss`, `aud`, `sub`, `sid` or `cid`, `prof`, `acr`, `amr`, `auth_time`, `act`, `iat`, `exp` with
   `exp = min(iat + AUTH_ACCESS_TOKEN_TTL, sessions.expires_at)`); no `email`, `roles`,
   `sudo_user_uuid`, `is_anonymous`. Replace `local_jwt.go` with a carrier file; `jwt.go` verification
   pins `EdDSA` and checks `iss`, `exp`, and `aud` against `AUTH_ACCEPTED_AUDIENCES` (empty: default
   audience plus registered app slugs). `IssueAudienceToken` for offline verifiers.
2. **Signing key bootstrap.** `jwtsecret_bootstrap.go` becomes the persisted Ed25519 key bootstrap in
   `mod_users.auth_signing_keys` (same single-row race-free pattern) when `JWT_SIGNING_KEY` is unset;
   `jwt_signing.go` loads it. `JWT_SECRET` remains only as an optional OAuth-state HMAC key, derived
   from the signing key otherwise (`derived_keys.go` keeps OAuth state only).
3. **Config (API surface 8).** Remove `JWT_ALG`, `AUTH_REQUIRE_STEP_UP`, the admin-role claim
   setting; add `AUTH_ACCESS_TOKEN_TTL` (1h, max 1h), `AUTH_SESSION_MAX_AGE`,
   `AUTH_SESSION_IDLE_TIMEOUT`, `AUTH_CLIENT_CREDENTIALS_TTL`, `AUTH_IMPERSONATION_TTL`,
   `AUTH_IMPERSONATION_MAX`, `AUTH_SELF_SERVICE_MAX_AUTH_AGE`, `AUTH_DEFAULT_AUDIENCE`,
   `AUTH_ACCEPTED_AUDIENCES` with the listed defaults; update `.env.example`.
4. **Identity and Authenticator (API surface 6.1, binding).** `principal.go` replaced by `Identity`
   and `IdentityFromContext`; `Authenticator.Authenticate` verifies the token, then runs
   `session_liveness` (for `sid`) or `credential_liveness` (for `cid`) in one round trip (U5),
   taking `acr`/`auth_time` from the row, and returns `ErrNoAuthHeader`, `ErrInvalidToken` or
   `ErrNotLive` (401 `users.session_revoked` or `users.profile_unavailable` with the lock reason).
   Bearer only: no route reads an authentication cookie.
5. **Middleware.** `NewRequireAuth`, `NewResolveOrAnonymous`, `NewRequireVerifiedEmail`
   (`require_verified.go`), `RequireOIDCConfirmed` unchanged. Each installs both `Identity` and
   `opctx.AuthContext` (`PrincipalID`, `ProfileID`, `SessionID`, `CredentialID`,
   `ImpersonatorPrincipalID`, `ACR`, `AMR`, `AuthTime`, and the field names mod-core's API surface
   fixes).
6. **Anonymous actor.** `anonymous_actor*.go` built on mod-core's seeded anonymous system actor
   (`coredb.GetSystemActorBySlug`, which returns the personal profile id); `IsAnonymousActor`
   delegates to `opctx.Auth(ctx).Anonymous`.
7. **Removals.** Delete `stepup.go` and the step-up JTI cache (U8), `ClaimMapper`/`UserResolver`
   public facade entries, `ResolveActorOrAnonymous`, `IssueLocalJWT`, `StepUpKey`,
   `NewStepUpConsumedCache` per API surface 6.1. Move `claims_*.go` under the OAuth orchestrator as
   internal mappers of external id_token claims (task 006 consumes them).
8. **Call sites.** Replace every `opctx.WithActor`, `ActorEntityID` and sudo call site in non-test
   code under `api/internal/auth`, `api/internal/config` and `api/internal/authz` with
   `AuthContext`; other packages' call sites are rewritten by the tasks that own them (004 to 008).
9. **Minimal `authz.go` port.** Actor = `opctx.ProfileID` (none: `ErrUnauthenticated`); own arm
   through `public.profile_own_subject` without `ownership_confers`; wildcard, entity and type arms
   otherwise unchanged; `AuthorizeTypeIn` falls back as mod-core's helper does (U20). The full engine
   comes in phase `users-authorization-engine`. Keep `authz_test.go` and `export_test.go` compiling
   and green for the ported behavior. Once mod-authz phase 13 has merged (it must before this
   phase closes, decision U27), the wildcard and entity arms read only `scope = 'entity'` grants,
   so an owned-scope grant never acts as an entity grant on its target. If phase 13 has merged
   when this task runs, add the filter here. Otherwise leave it to task 010 and say so in the
   report.
10. `api/auth/auth.go` re-exports the section 6.1 facade exactly; any change to a binding name is
    recorded here and flagged in the report.
11. Update the `AGENTS.md` and `README.md` rows that describe the token, signing key and
    configuration variables this task changes.

## Validation

- `cd api && go build ./internal/auth/... ./internal/config/... ./internal/authz/... ./auth/...`
  passes.
- `cd api && go test ./internal/authz/...` (unit) passes.
- `grep -rnE 'HS256|JWT_ALG|AUTH_REQUIRE_STEP_UP|X-Step-Up-Token|sudo_user_uuid|IssueLocalJWT|WithActor\(|ActorEntityID' api/internal/auth api/internal/config api/internal/authz api/auth --include='*.go' | grep -v _test.go`
  returns nothing.
- `api/internal/auth/*_test.go` and `api/internal/config/*_test.go` that reference removed symbols
  may stay red; task 010 migrates them. List them in the report.

## Metadata

architectural_impact: true

## Assumptions

- mod-core's `opctx.AuthContext` and `coredb.GetSystemActorBySlug` are available through the
  sibling link to the mod-core plan worktree.
- mod-authz phase 13 may or may not have merged when this task runs (requirement 9).

## References

- [API surface sections 5, 6.1, 6.2, 8](../notes/mod-users-api-surface.md).
- [Decisions U4, U5, U8, U14, U20](../notes/decisions-and-resolutions.md).
- [Work packages, package 3](../notes/work-packages.md).
- mod-core plan notes `mod-core-api-surface.md` (mod-core plan worktree) — `opctx.AuthContext`
  field names.
- `docs/jwt-eddsa.md` — current EdDSA design (updated in the doc-updates phase).

## Checkpoint hints

- After the signer, verifier and signing-key bootstrap.
- After `Identity`, `Authenticator` and the middleware.
- After the `authz.go` port and call-site sweep.

## Status

- **Outcome:** succeeded (2026-10-23).
- **Validation:** `go build` of `./internal/auth/... ./internal/config/... ./internal/authz/... ./auth/...`
  passes; `go test ./internal/authz/...` passes; the forbidden-symbol grep returns nothing. Beyond
  the required checks, `go vet` and `go test -race` pass for all four packages (every unit test
  file in them now compiles and passes), and `go vet -tags integration ./internal/config/` passes.
  The liveness SQL, the authz arms, the anonymous-actor lookup and the signing-key bootstrap
  (including 4 concurrent callers on an empty database) were exercised against a throwaway
  Postgres 16 with mod-core, mod-authz and mod-users migrations applied.
- **Requirement 9 (U27):** mod-authz phase 13 has merged into its plan branch, so the wildcard,
  entity and type-entity arms of `authz.go` now read only `scope = 'entity'` grants.
- **Binding names (requirement 10):** the section 6.1 facade is re-exported as listed. Additions
  not named in 6.1 are kept for existing consumers. `OAuth`, `NewOAuth` and `OAuthStateKey` are
  neither listed nor removed, and the OIDC orchestrator and composition root still need them.
  `ErrNoAuthHeader`, `ErrInvalidToken` and `ErrNotLive` are re-exported as variables. No binding
  name changed.
- **Affected sources:** `api/internal/auth/` (`carrier.go`, `identity.go`, `authenticator.go`,
  `middleware.go`, `signer.go`, `jwt.go`, `derived_keys.go`, `anonymous_actor*.go`,
  `require_verified.go`, `oauth_claims*.go` (moved from `claims*.go`), `oauth.go`,
  `oauth_state.go`); `api/internal/config/` (`config.go`, `jwt_signing.go`,
  `signing_key_bootstrap.go` (was `jwtsecret_bootstrap.go`), `provider_merge.go`);
  `api/internal/authz/authz.go`; `api/auth/auth.go`; `.env.example`, `README.md`, `AGENTS.md`,
  `deploy/k8s/base/configmap.yaml`, `deploy/local/docker-compose.yml`.
- **Deleted:** `local_jwt.go`, `principal.go`, `stepup.go`, `resolver.go` (built entirely on the
  removed account schema; the OIDC resolver task rewrites it) and the tests of the deleted
  code.
- **Decisions under the assumptions:** mod-core's `opctx.AuthContext` and
  `coredb.GetSystemActorBySlug` resolved through the sibling links to the mod-core plan
  worktree, as assumed.
