# Work Packages

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Input for the phase-decomposition agents: the concrete files, tests and suggested task boundaries
the planning investigation found for each mod-users phase. It is a recommendation, not a task list;
decomposition agents may split or merge packages but must keep every file, behavior and test named
here covered. Contracts are in [the API surface note](./mod-users-api-surface.md); decisions in
[decisions and resolutions](./decisions-and-resolutions.md).

Paths are relative to the mod-users repository root. Integration tests use the throwaway-Postgres
recipe in `AGENTS.md` ("Authz integration tests against a throwaway Postgres"), which composes
mod-core, mod-authz and mod-users migrations (`make -C model compose`); tasks must make sure the
sibling links (`scripts/link-siblings.sh`) resolve mod-core and mod-authz to their plan worktrees.
Every phase ends with `make build`, `make test`, `make lint`, `cd model && make verify` and the
integration suites green, subject to the cross-project prerequisites each phase names. Inside a
phase, a task may leave `api/` uncompilable only when its task document says so and names the task
that restores it. `AGENTS.md`, `README.md`, `model/README.md` and `gui/README.md` rows are updated by
the task that changes the described code; the doc-updates phase reviews them.

## Phase `users-identity-core`

Prerequisites: mod-core phases 1 to 3 merged. Before the phase closes, mod-authz phase 12 and
mod-audit phase 20 (`AuthContext` adoption; until then only `api/cmd/server` stays red) and
mod-authz phase 13 (the grant columns the generated functions read) must have merged (decision
U27). Consumer-requested additions folded into this phase: `LinkOIDCInTx` in package 4 (U24) and
`ReadPrincipalSummaries` in package 7 (U23). Audit ops follow API surface section 10.1 (U21).

1. **Baseline schema rewrite** (`model/migrations/sql/0100_baseline.sql`; delete `0101` to `0104`;
   `model/sqlc.yaml` renames): every table, trigger, function and stub in API surface sections 2
   and 3, all in `mod_users`; MF101 to MF103 triggers; the mod-users-owned trigger on
   `public.profiles`; the deferred personal-profile check; the `active` mirror columns; the
   challenge cooldown function; deletion list of section 2. Riskiest package (deferred constraints,
   cross-schema triggers, availability SQL). Validate with `cd model && make verify && make lint`.
   Suggested tier: `opus-high`.
2. **Queries and generated code** (`model/queries/*.sql` rewritten: `users.sql`, `identifiers.sql`,
   `credentials.sql`, `challenges.sql`, `sessions.sql`, `party_policies.sql`, `oidc_*.sql`,
   `signing_keys.sql`; `make gen`; SQLSTATE helpers in `api/internal/service/sqlstate.go` re-exported
   by `api/usersservice`). Leaves `api/` uncompilable until packages 3 to 7 land. Suggested tier:
   `sonnet-med`.
3. **Token carrier and authentication middleware** (`api/internal/auth/`: `signer.go`, `jwt.go`,
   `local_jwt.go` replaced by a carrier file, `derived_keys.go` (OAuth state only), `middleware.go`,
   `principal.go` replaced by `Identity`, `anonymous_actor*.go` on mod-core's system actor,
   `require_verified.go`; `api/internal/config/jwt_signing.go`, `jwtsecret_bootstrap.go` becomes the
   signing-key bootstrap, `config.go` new variables; `api/auth/auth.go` facade; delete `stepup.go`,
   `claims_*.go` move under the OAuth orchestrator; every `opctx.WithActor`/`ActorEntityID`/sudo call
   site; minimal `api/internal/authz/authz.go` port: actor = `opctx.ProfileID`, own arm through
   `profile_own_subject` without `ownership_confers`, existing arms otherwise). Suggested tier:
   `opus-med`.
4. **Identifiers, credentials and challenges** (new `api/internal/service/` files for identifiers,
   credentials, challenges; `api/internal/sshkey/` reused for `ssh_key` credentials; password
   hashing reused; email sender reuse; credential revocation revoking dependent sessions; guest
   `anon` credential; `CredentialService.IssueAPIKeyInTx` authorizing the `credentials` operation —
   until mod-authz seeds that slug the prelude's unknown-slug fallback admits only wildcard `manage`
   holders, which is enough for this phase's tests; the HTTP routes for it and the owner path come
   in the engine phase). Suggested tier: `sonnet-high`.
5. **Sessions and the `/v1/auth` and `/v1/session` routes** (`api/internal/handlers/auth/*.go`
   rewritten: register, login, anonymous, email code, password reset, token (refresh and client
   credentials), handoff; new session handler: switch, fork, step-up, logout; landing chain,
   availability calls, refresh lineages with reuse detection, `allow_byo_identity` enforcement,
   guest upgrade; app `invite_only` gate stubbed to "open unless invite_only, then deny without a
   wildcard" until the engine phase). Suggested tier: `opus-med`.
6. **OIDC resolver** (`api/internal/auth/resolver.go`, `oauth.go`, `oauth_state.go`,
   `api/internal/handlers/auth/oidc.go`, `api/internal/handlers/oidc_providers.go`,
   `api/internal/config/provider_merge.go`, `providers.go`): decision table — known
   `(issuer, subject)` logs in; link and step-up modes only from an authenticated session (state
   carries the session uuid); unknown identity JIT by `jit_mode` (`byo` creates user, person,
   personal profile, identifier, credential; `managed` creates a managed user, active `idp_jit`
   party profile and a credential confined to the party; `none` refuses); never link by email
   (an existing email identifier yields `account_exists_sign_in_and_link`); never link to managed or
   service principals; handoff redirect. Suggested tier: `sonnet-high`.
7. **Self-service, principals, wiring** (new `/v1/me` handlers and services, `/v1/principals` read,
   list, disable and enable; display renderers for `user` and `profile`; delete
   `api/internal/handlers/{self,identities,user_accounts,assume,apps,ssh_keys}*.go`,
   `account_routes.go`, `api/internal/service/{user_accounts,ssh_keys}.go`, `api/internal/users/`,
   `api/users/`; `api/handlers/handlers.go`, `api/handlers/authhandlers/`, `api/usersservice/`
   facades; `api/cmd/server/main.go` (slug list per U18, no first-user hook, no grant-admin closures);
   `moduleforge.module.yaml`; `api/openapi.yaml` for every route this phase serves). Suggested
   tier: `sonnet-high`.
8. **DB invariant integration tests** (`model/` or `api/internal/authz/*_integration_test.go`
   style): every MF101 to MF103 rule, the profiles trigger, deferred personal-profile check, kinds
   per principal type and guest status, active uniqueness, availability function rules (confinement,
   credential rule, provider rule, confirmation exempts credential rule but never `min_acr`,
   personal profile skips the party gate), `session_acr`, liveness statuses, cooldown concurrency.
   Rewrites or deletes `displaced_guard_integration_test.go`, `anonymous_actor_integration_test.go`,
   `email_code_integration_test.go`, `ssh_keys_integration_test.go`,
   `user_account_*_integration_test.go`, `shape_self_update_integration_test.go`. Suggested tier:
   `sonnet-high`.
9. **Session, credential and route tests** (unit and integration): login and landing order;
   refresh rotation and reuse revoking the session; fork; switch writes nothing; per-request
   revocation (logout, credential revocation, principal disable, profile end) takes effect on the
   next request; step-up raises `acr` and `auth_time`; handoff single use; OIDC no-email-link,
   JIT modes, link only when authenticated; client-credentials token and `cid` liveness; route
   order tests migrated from `ordertest.Actor`/`Sudo`/`SudoOnly` to mod-core's `AuthContext`
   identities (`account_routes_order_test.go`, `order_helpers_test.go`,
   `require_auth_mount_test.go`, `self_service_order_test.go` and the rest of
   `api/internal/handlers/*_test.go`, `api/internal/handlers/auth/*_test.go`,
   `api/internal/auth/*_test.go`). Suggested tier: `sonnet-high`.

## Phase `parties-profiles-and-bootstrap`

1. **Profile lifecycle and invitations** (`ProfileService`: invite by email or user, accept,
   decline, leave, suspend, resume, end with departure cleanup, managed-user creation,
   `ProfileLifecycleObserver`; `/v1/parties/{uuid}/profiles`, `/managed-users`, `/v1/profiles/{uuid}`,
   `/v1/me/profiles/*`; `AuthorizeTypeIn(create, profile|user, party)`; step-up on profile
   administration). Suggested tier: `sonnet-high`.
2. **Party gating administration** (`PartyPolicyService`: authn policy CRUD, credential
   authorizations and confinements by a party admin or by a session already satisfying the gate
   through another factor; JIT credentials confined by default). Suggested tier: `sonnet-high`.
3. **Outside collaborators and continuous compliance** (policy-mismatch detection on invite with
   recorded `profile_policy_confirmations`; `CollaboratorGate` for mod-authz; compliance: factor
   removal revokes dependent sessions (from the core phase), downgraded collaborator locked at next
   login and on the next request through `min_acr`). Suggested tier: `sonnet-high`.
4. **Setup-token bootstrap** (`BootstrapService`, `/v1/bootstrap`, U9 token lifecycle in
   `api/internal/handlers/oidc_config.go` and `api/handlers/onboarding_boot.go`, U10 transaction,
   admin check by operator wildcard). Suggested tier: `opus-med`.
5. **Party-aware SSH profile derivation** (`SSHKeyResolver.AuthContextForOwner` full rules).
   Suggested tier: `sonnet-med`.
6. **Tests**: invitation and status transitions through the API; managed-user rules;
   gating and confinement (worked example of the ADR end to end: personal Google login lands in
   the personal profile with the Acme profile locked, the authorized password lands in the Acme
   profile, departure ends access on the next request); collaborator compliance; bootstrap
   concurrency (two concurrent bootstraps, exactly one wins; no `count(*)` path remains), token
   lifecycle per U9. Suggested tier: `sonnet-high`.
7. **Trusted admission** (added by the cross-plan reconciliation, U22; task 007, listed after
   the tests package but merged before it): `ProfileService.AdmitInTx` for mod-billing seats.
   Package 3 starts only after mod-authz phase 14 task 004 (U26). Suggested tier: `sonnet-med`.

## Phase `users-authorization-engine`

Prerequisite: mod-authz's schema phase (API surface section 9, items 1 to 4) merged.

1. **Engine** (`api/internal/authz/authz.go` rewritten per API surface 6.2, package documentation
   without `assume`/sudo wording; `api/localAuthz/authz.go` asserts `OwnerScopedTypeAuthorizer`;
   `export_test.go` stubs). Suggested tier: `opus-high`.
2. **Engine tests** (`authz_test.go`, `authz_integration_test.go`, `type_grant_integration_test.go`,
   `type_target_integration_test.go`, `instance_semantics_integration_test.go`, new owned-scope and
   own-arm suites): owned scope exact versus `include_subtypes`; owned scope never reaches the owner's
   own record; own arm only for users' personal profiles and only for conferring operations, never
   `impersonate`; assurance gate (`min_acr`, `max_auth_age`, system-actor exemption);
   `AuthorizeTypeIn` matrix; parity with mod-core's generated `accessible_*` functions on the same
   fixtures (followup `rHzV` single-row half). Suggested tier: `sonnet-high`.
3. **Impersonation** (`ImpersonationService`, `/v1/impersonations`, reason, TTL, renewal cap,
   fresh `auth_time`, no switching inside, parent liveness; tests: no nesting through the API and
   through direct SQL (MF101), the administrator recorded from the parent session's own principal,
   an Acme admin cannot impersonate a personal or other-party profile, a personal profile needs
   operator authority). Suggested tier: `sonnet-high`.
4. **`credentials` operation and app login gate** (`/v1/principals/{uuid}/credentials*`,
   `IssueAPIKeyInTx`, owner rotation of a self-owned service account through the own arm; SSH-key
   management moves from `assume` to `credentials`; `invite_only` app gate through
   `Authorize(login, app)` per U15). Suggested tier: `sonnet-med`.
5. **Personal party** (`PersonalPartyService.EnsurePersonalParty`, U1 and U2, idempotency under
   concurrency, guest and managed refusals; owned-scope admin flows from the party phase re-tested
   with owned-scope grants instead of wildcard). Suggested tier: `sonnet-med`.

## Phase `users-gui-and-frontend-contract`

1. **API client and auth context** (`gui/src/lib/api.ts`, `auth-context.tsx`, `config.ts`,
   `return-path.ts`; per-tab storage, refresh lineage, fork over `BroadcastChannel`, handoff
   exchange, Bearer only with `credentials: 'omit'`, 401 handling without a hard-coded navigation
   (followup `HPJi`), apps client aligned or removed (followup `PRvD`)). Suggested tier: `sonnet-high`.
2. **Profile chrome** (profile switcher with locked profiles and remedies, landing selection page,
   invitations, impersonation banner; `sidebar-nav.tsx`, `require-auth.tsx`, `client-layout.tsx`,
   new components, `routes.ts`, `index.ts`). Suggested tier: `sonnet-high`.
3. **Self-service panels** (credentials, identifiers, sessions panels replacing the identities and
   SSH-key panels; step-up flows on 409; guest upgrade; `login-form.tsx`, `register-form.tsx`,
   `email-code-page.tsx`, `oidc-callback-page.tsx`, `verify-email-page.tsx`,
   `forgot/reset-password-page.tsx`, `ssh-keys-panel.tsx`). Suggested tier: `sonnet-med`.
4. **Bootstrap and OIDC config pages** (setup-token bootstrap page, `oidc-config-page.tsx`,
   `oidc-setup-gate.tsx`, provider modals with `party_uuid`, `jit_mode`, `trust_acr`), stories and
   `gui/README.md` frontend contract (API surface section 12). Suggested tier: `sonnet-med`.

## Phase `doc-updates`

1. **Update architecture docs** (the planning procedure's `update-architecture-docs` task,
   `role_doc: plugins/flow/roles/architect-backend.md`): `docs/architecture.md` (data model,
   authentication flow, multi-channel account model rewritten as principals and credentials, key
   decisions), `docs/mod-users-spec.md` (use cases and API definition), `docs/architecture/ssh-keys.md`
   (keys as credentials, `credentials` operation, profile derivation), `docs/jwt-eddsa.md` (EdDSA-only
   carrier, claims, persisted key), and a new `docs/architecture/sessions-and-tokens.md` for sessions,
   liveness, availability, landing, refresh lineages, impersonation and bootstrap, linked from
   `docs/architecture.md` and `README.md`. Suggested tier: `sonnet-high`.
2. **Repository docs review** (`README.md`, `AGENTS.md`, `model/README.md`, `gui/README.md`,
   `docs/consumer-upgrade-notes.md` (one section listing every removed and renamed symbol, route and
   variable), `docs/cookie-path.md`, `docs/oidc-troubleshooting.md`, `docs/project-structure.md`,
   `docs/cors.md`, `next-steps.md`, `.env.example`, `deploy/local/`, `deploy/k8s/`,
   `api/openapi.yaml` descriptions, `.claude/CLAUDE.md`). Suggested tier: `sonnet-low`.

## Phase `users-identity-final`

1. **mod-users drift-scan slice**: scan mod-users' README, AGENTS.md, `docs/`, `api/openapi.yaml`,
   `gui/README.md`, `deploy/`, manifest and code comments for `user_accounts`, `user_account`,
   `account_holder`, `corporation`, `MF002`, service-account ownership "under review",
   `app_service_identity` login, `.invalid`, stateless JWT, 24-hour token, `assume`, `sudo`,
   `SudoUserUUID`, `AssumedUser`, `apps_user_accounts`, `first user`, `HS256`, `X-Step-Up-Token`,
   `/v1/self`; fix what is clear; file followups for the rest. Suggested tier: `sonnet-low`.
2. **Followup reconciliation**: verify and hand the manager the `followups_remove` calls (with
   `resolved_by` naming the resolving mod-users ref) for the mod-users followups this plan
   supersedes: `VJg2`, `1mnX`, `7kHY`, `sxjM`, `4lm1`, `cJcZ`, `tbmN`, `o1uH`, `433h`, `Rjag`,
   `Jtw6`, `pciu`, `ZJ5N`, `PRvD`, `fs0H`, `biJk`, `bHKv`, `HPJi`, and `WZTj` (resolved by mod-core's
   `core_ensure_system_actor`); `UbXY` and `OlHx` only if mod-authz's work closed them. Each removal
   is checked against the merged code first. Suggested tier: `haiku-med`.
