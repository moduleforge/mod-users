# Plan Summary: identity-model-impl

## What was planned and why

This is mod-users' share (participant 2 of 10) of the federated plan `identity-model-impl`, which
implements the accepted identity ADR (mod-core `docs/architecture/identity-model.md`). mod-core's
planning pass is complete; its plan notes are binding for the mod-core surface mod-users consumes.
mod-users owns the principal, credential, session and profile layer and the authorization engine's
consumer side.

### What must change in mod-users

1. **Principals instead of `user_accounts`:** the `user` principal subtype table (`person_id`,
   `managing_party_id`, `is_guest`, `default_profile_id`, `default_app_id`), consumption of mod-core's
   generic `service_account` and `system_actor` (the mod-users copies of `system_actors`, the
   `anonymous` seed and `entities_no_system_actor_owner` are deleted), identifiers, guests upgraded in
   place, managed users.
2. **Credentials:** the `credential_kinds` registry and per-kind credentials (password, email OTP,
   OIDC, SSH key, API key, anonymous device token) with DB-enforced kind rules per principal type,
   `auth_challenges`, and OIDC linking that never auto-links by email.
3. **Sessions and tokens:** the `sessions` row as the revocation unit with factors and assurance,
   a 1-hour EdDSA carrier token (`sid`, `prof`, `acr`, `amr`, `act`), per-request liveness,
   per-tab refresh lineages, profile landing and switching, step-up as factor composition, client
   credentials for machines, and the Bearer-only API contract (resolution (c)).
4. **Profiles and parties:** party profile lifecycle and invitations, party authn policies,
   `credential_party_authorizations`, `default_profile_id` and `confined_to_party_id` (confinement in
   v1), `acr`/`min_acr`/`max_auth_age` enforcement, outside-collaborator confirmations and continuous
   compliance (factor removal revokes sessions).
5. **Impersonation** as a separate session with reason, TTL and step-up, and a DB trigger forbidding
   nesting (fixing the nested-assume audit bug).
6. **Bootstrap:** setup-token bootstrap creating the operator party, first user, admin profile and
   wildcard `manage` grant (fixing the first-user `count(*)` race); personal team plans
   (resolution (b)) through an idempotent personal-party service.
7. **Authorization engine** (`api/internal/authz/authz.go`): the profile as the only subject, the
   per-operation own arm through `authz_operations.ownership_confers` (never `impersonate`, personal
   profiles only; with mod-core's generator and mod-authz's column it closes followup `rHzV`),
   owned-scope grants with `include_subtypes`, `AuthorizeTypeIn`, the `login` grant for
   `apps.invite_only`.
8. **GUI, docs, tests:** users-gui on the new contract, mod-users docs, and mod-users' slice of the
   final documentation drift scan.

### What must not change

- The HTTP error order (401, 400, 403, 404/409), masked lookups, and "internal ids never leave the
  process".
- `localAuthz.New(authzQ, opReg, pool)` and the `Authorize` / `AuthorizeType` signatures; the
  instance semantics of type-entity grants.
- The OIDC onboarding gate (`RequireOIDCConfirmed`) and provider merge behavior, apart from the new
  provider columns and the setup-token lifecycle of decision U9.
- Field encryption, the CORS default-deny policy and the `oidc_state` cookie path behavior.

### Hard constraints

- No production release: rewrite `0100_baseline.sql` in place, fold `0101` to `0104` into it; no
  incremental migrations, no backward compatibility, no data migration. Refactor size is not a
  constraint.
- Names and signatures fixed in mod-core's API surface note are used as written; this plan's
  [API surface](./notes/mod-users-api-surface.md) is binding for later participants.
- `created_by_profile_id` is never an authority input; liveness is never cached in v1.

### Success criteria

- Every DB invariant the ADR assigns to mod-users holds whatever the writer (MF101 to MF103,
  credential kinds, managed-user rules, no impersonation nesting), proven by integration tests.
- Sessions, liveness and revocation, credential gating and confinement, outside-collaborator
  compliance, impersonation and bootstrap are proven by service and HTTP tests; the engine's arms,
  assurance gate and `AuthorizeTypeIn` are proven by unit, integration and list-function parity
  tests.
- The contract in [the API surface](./notes/mod-users-api-surface.md) is implemented with the
  names it fixes; mod-users builds and its tests pass at the end of each phase (subject to the
  cross-project prerequisites each phase names).
- mod-users' docs describe the new model; its drift-scan slice is done; superseded followups are
  reconciled.

### Planning notes

- [The mod-users API surface](./notes/mod-users-api-surface.md): the binding contract for later
  participants (schema, SQL functions, SQLSTATE codes, token claims, Go packages, HTTP routes,
  configuration, manifest, frontend contract, requirements placed on mod-authz).
- [Decisions and resolutions](./notes/decisions-and-resolutions.md): how resolutions (a) to (d) are
  implemented, the person-record ownership choice (U1), decisions U2 to U20, the cross-plan
  reconciliation decisions U21 to U28 with one open question for the manager, the mod-users versus
  mod-authz boundary confirmation with flagged mismatches, cross-project ordering risks, and the
  mapping of ADR impact notes to phases.
- [Work packages](./notes/work-packages.md): files, tests, suggested task boundaries and tiers for
  the phase-decomposition agents, the drift-scan term list and the superseded-followup list.

| # | Phase | Summary | Depends on |
|---|---|---|---|
| 6 | [Users Identity Core](./phases/users-identity-core.md) | Schema rewrite into `mod_users`; users, identifiers, credentials (including the trusted `LinkOIDCInTx`), challenges, sessions, refresh lineages; EdDSA carrier and per-request liveness populating `AuthContext`; `/v1/auth`, `/v1/session`, `/v1/me`, `/v1/principals`; `DirectoryService` and the transaction-scoped `ReadPrincipalSummaries`; OIDC resolver without email auto-link; old surfaces removed; invariant and session tests. | mod-core 1 to 3; before closing: mod-authz 12 and 13, mod-audit 20 |
| 7 | [Parties, Profiles and Bootstrap](./phases/parties-profiles-and-bootstrap.md) | Party profile lifecycle and invitations, managed users, trusted admission (`AdmitInTx`), party authn policies, credential authorizations and confinement, outside-collaborator confirmations and compliance, setup-token bootstrap, party-aware SSH profile derivation. | 6; mod-authz 14 task 004 before task 003 |
| 8 | [Users Authorization Engine](./phases/users-authorization-engine.md) | `authz.go` engine (assurance gate, owned scope, per-operation own arm, `AuthorizeTypeIn`) with parity tests; impersonation sessions; `credentials` operation; invite-only app gate; `EnsurePersonalParty`. | 7; mod-authz 13 (already required by 6) |
| 9 | [Users GUI and Frontend Contract](./phases/users-gui-and-frontend-contract.md) | Per-tab tokens, refresh lineages, handoff, profile chrome, self-service panels, bootstrap and OIDC pages. | 8 |
| 10 | [Documentation Updates](./phases/doc-updates.md) | Architecture and spec documents; repository documents review. | 6 to 9 |
| 11 | [Users Identity Final](./phases/users-identity-final.md) | mod-users drift-scan slice; superseded-followup reconciliation. | 10; precedes mod-core phase 5 |
| 21 | [Adopt Mod-Core Identity Engine Remediation](./phases/adopt-core-engine-remediation.md) | Adopt mod-core remediation phase 11: `gated()` keyed on `opctx.IsSystemActor` with the sessionless builders reviewed; the list-side owning-party gate through `setup.NewRowFilterGenerator`, `mod_users.access_row_permitted` (six arguments) and `setup.BindAuthInputs`, with parity tests; `dblock` profile lock and `OrganizationService.CreateInTx` in the bootstrap; docs. | 8 and its remediation rounds; mod-core phase 11 merged; **runs before 9, 10 and 11** |

Within phase 6, the schema task comes first and the queries task second; the token/middleware task
precedes the credentials, sessions, OIDC and wiring tasks, which can then largely run in parallel;
the two test suites close the phase. Within phase 7, the profile, policy, bootstrap and SSH tasks are
parallel after the profile service exists; the collaborator task (003) follows the policy task and
mod-authz 14 task 004. The trusted-admission task (007, listed last) depends only on task 001,
runs in parallel with 002 to 005, and merges before the closing integration task 006. Within
phase 8, the engine task comes first; impersonation, `credentials` and personal-party tasks are
parallel after it. Phase 9's client task precedes its UI tasks. Phase 10's two tasks are parallel.
Phase 11's scan precedes the reconciliation. Phase 21 was added after phase 8's remediation
rounds and, despite its number, runs before phases 9, 10 and 11 (phase 9 consumes the list
endpoints it changes; phases 10 and 11 must describe the adopted behavior); within it, task 001
precedes task 002, task 003 is independent of both, and task 004 runs last. The [work packages](./notes/work-packages.md) note gives
the suggested task boundaries.

## What shipped

### Phase 06 — Users Identity Core

1. **Rewrite Baseline Schema** (`001-rewrite-baseline-schema.md`, tier `opus-high`) — Rewrote 0100_baseline.sql so everything in API surface sections 2 and 3 lives in the mod_users schema (tables, invariant triggers incl. two on public.profiles, availability, liveness and ACR functions, challenge cooldown). Folded and deleted 0101-0104; added sqlc renames. make verify + lint pass against mod-core, mod-authz (phase 13 merged), mod-audit plan worktrees; 84-check smoke suite passes. Judgment calls beyond the sketch: trigger-maintained active/principal mirrors on more subtables; session_factors.asserted_acr column; OUT-parameter liveness functions for sqlc. Obsolete query files deleted early and oidc queries schema-qualified; model/db left stale for task 002. Two findings filed (2yby identifier deletion vs bound credentials; tC3F sqlc schema scope).
   Commit `5c342b0`, merged at `ef800f747da13d951185ff07012837e7de3ef297`.

2. **Rewrite Queries And Generated Code** (`002-rewrite-queries-and-generated-code.md`, tier `sonnet-med`) — Replaced the query set (users, identifiers, credentials, challenges, sessions, party_policies, signing_keys; oidc queries updated) and regenerated usersdb with make gen; old generated files removed. Added SQLSTATE helpers (MF101-MF103, unique-violation mapping), tests, and usersservice re-exports. api packages stay red until task 008. No findings filed.
   Commit `461c891`, merged at `5ba1530a419f3845b0d900c30f2507793ec833d2`.

3. **Token Carrier And Auth Middleware** (`003-token-carrier-and-auth-middleware.md`, tier `opus-med`) — Replaced HS256 tokens and actor/sudo plumbing with an EdDSA-only carrier (signer and verifier pinned to EdDSA; carrier.go with session, client-credentials and audience-token issuers; auth.Identity; Authenticator with one-statement liveness check; NewRequireAuth, NewResolveOrAnonymous, NewRequireVerifiedEmail install Identity and opctx.AuthContext; persisted Ed25519 signing-key bootstrap; AUTH_* configuration; API surface 6.1 facade). authz.go uses opctx.ProfileID; own arm via profile_own_subject; wildcard and entity grants limited to scope='entity'. Resolver.go and its test deleted (task 006 rewrites). Scoped packages build, vet, and pass unit tests. Finding CLi0 filed (deploy manifests still require JWT_SECRET).
   Commit `cb9176d`, merged at `5126cdaf44027caee40b874f59dd6b521d6fc32c`.

4. **Users Identifiers Credentials Challenges** (`004-users-identifiers-credentials-challenges.md`, tier `sonnet-high`) — Added service layer under api/internal/service: user_service (BYO, guest, managed creation; email identifier add/verify/set-primary/remove), credential_service (password, email_otp, ssh_key, anon, API key; LinkOIDCInTx as sole cred_oidc insert path; RevokeInTx; RemoveInTx with last-credential guard), challenge_service (numeric and opaque tokens, attempt-bounded VerifyCode), ssh_key_resolver rewritten. Deleted user_accounts.go, ssh_keys.go, identities.go. Build of service and sshkey packages passes; stale tests in service left for task 010; usersservice facade re-exports deferred to task 008. Finding oi9B filed (identifier delete vs RESTRICT FK).
   Commit `2fd92b1`, merged at `ccb36c0d4d17fda005b64861e5949fb8a53e221b`.

5. **Sessions And Auth Routes** (`005-sessions-and-auth-routes.md`, tier `opus-med`) — Added SessionService (creation and landing, refresh lineages with reuse revocation, client credentials, handoff, switch, fork, step-up, logout, revocation helpers). Rewrote /v1/auth handlers on it; added handlers/session package for /v1/session. OIDC handler file fenced with build constraint oidc_resolver_pending until task 006.
   Commit `2d4025e`, merged at `81b4af00862fa4694747bd1f086e40d716abc33c`.

6. **OIDC Resolver** (`006-oidc-resolver.md`, tier `sonnet-high`) — Added OIDC resolver decision table (auth/resolver.go), reworked oauth_state (Flow login/link/step_up), SessionAuthorizeURL, claim enrichment, provider party/jit_mode/trust_acr. Rewrote handlers/auth/oidc.go (Start, Callback, LinkStart, StepUpStart) and removed the oidc_resolver_pending fence. Providers CRUD gained party_uuid/jit_mode/trust_acr. Build and internal/auth and internal/config tests pass; handler tests deferred to task 010.
   Commit `2482d7f`, merged at `d6ba9ff4b14f37bd4fb91d72d102bf5465908fd0`.

7. **Self Service And Principals** (`007-self-service-and-principals.md`, tier `sonnet-high`) — Added /v1/me and /v1/principals handlers with UserService self-service and principal-administration methods, DirectoryService, ReadPrincipalSummaries and user/profile display renderers. Authority derives from AuthContext; step-up gate cannot be skipped. Throwaway Postgres run exercised all new SQL and routes.
   Commit `b7addc9`, merged at `bf4704575cff17caf3d7c2e382e6ab2a9b1c94f4`.

8. **Server Wiring And Surface Removal** (`008-server-wiring-and-surface-removal.md`, tier `sonnet-med`) — Deleted superseded self, identities, user_accounts, assume, apps, ssh_keys handlers and services, api/internal/users, api/users. Ported oidc_config.go. Facades in api/handlers/handlers.go and usersservice re-exports; cmd/server wired; moduleforge.module.yaml rewritten; api/openapi.yaml rewritten. make build.api passes; test-file vet errors remain for task 010.
   Commit `c017fde`, merged at `09c1f887873f99516e663a7d4d5450fd7c29e01e`.

9. **DB Invariant Integration Tests** (`009-db-invariant-integration-tests.md`, tier `sonnet-med`) — Rewrote and added DB invariant integration suites (MF101/MF102/MF103, availability, session_acr and liveness, cooldown concurrency) and adapted authz integration suites to the profile model. Shared fixtures in fixtures_integration_test.go. Authz integration suite passes against a throwaway Postgres; go vet and unit tests pass with and without the integration tag. jwtsecret_bootstrap integration test left for task 010.
   Commit `cf2d7e2`, merged at `eb421b966a12f19d14592f435e8962a2afe6ec5e`.

10. **Session Credential Route Tests** (`010-session-credential-route-tests.md`, tier `sonnet-high`) — Cleared stale test packages in internal/service, internal/handlers and handlers/auth; rewrote OIDC handler tests for the new API; added HTTP-level integration suite in api/internal/integration covering requirements 2 to 8, plus U27 and U23/U24 tests. Integration suite green against throwaway Postgres 16; make build/lint/test pass for model and api. No implementation defects found.
   Commit `6ef9004`, merged at `f24fa926bf03fb55ccbc59906014e98833bb0c5b`.

### Phase 07 — Parties, Profiles and Bootstrap

1. **Profile Lifecycle and Invitations** (`001-profile-lifecycle-and-invitations.md`, tier `sonnet-high`) — Implemented ProfileService and its routes. Invite, accept, decline, leave, admin suspend/resume/end with step-up, departure cleanup, managed-user provisioning and lifecycle observers are in place, with the checkInvitePolicyMismatch seam for task 003. Wired through the manifest, main.go, the facades, api/openapi.yaml, AGENTS.md and README. Unit, integration and route-order tests pass, as do make build, make test and make lint. One latent test bug in session_integration_test.go that the new fixtures exposed was fixed.
   Commit `796d296`, merged at `27fa2896ce3a6930978cb11265b8b013f4680e69`.

2. **Party Gating Administration** (`002-party-gating-administration.md`, tier `sonnet-med`) — Added PartyPolicyService and seven routes under /v1/parties/{party_uuid} (authn-policy GET/PUT, credential-authorizations GET, PUT/DELETE per credential, confinements PUT/DELETE). New SQL function mod_users.profile_availability_excluding; session_acr delegates to session_acr_excluding. Edited 0100_baseline.sql in place. Fixed byo JIT sign-up from a party-bound provider so it confines its credential; test added.
   Commit `3abab2b`, merged at `d01cdf7e931d7a17893c9f008e3c5129710a8a81`.

3. **Outside Collaborators and Continuous Compliance** (`003-outside-collaborators-and-compliance.md`, tier `sonnet-high`) — Filled the invite seam and added the outside-collaborator CollaboratorGate. Mismatch rule is one SQL function mod_users.holder_policy_mismatch in the 0100 baseline (rewritten in place), shared by invite and gate. Gate wired as trailing argument of authzservice.NewFromArgs in main.go; manifest provides collaboratorGate. Tests cover compliance behaviors and the HTTP flow through mod-authz's real route.
   Commit `01fade3`, merged at `7b79af9ac19aee1954cd6a215b34c9d4fd3c352d`.

4. **Setup-Token Bootstrap** (`004-setup-token-bootstrap.md`, tier `opus-med`) — BootstrapService.Bootstrap runs the requirement 1 steps in one transaction, locking platform_settings FOR UPDATE, claiming via core_claim_operator_party (409 users.already_bootstrapped on loss). Admin profile via createInTx so lifecycle observers fire. Wildcard manage grant via authzdb.CreateGrant. BootstrapHandler serves GET /v1/bootstrap/status and POST /v1/bootstrap (public). Setup-token lifecycle (U9) in OIDCConfigHandler; IsOperatorAdmin replaces the manage-only admin check. Docs updated.
   Commit `768bb8f`, merged at `390093d3ef1510f9eca9f35b7fff368921599c0a`.

5. **Party-Aware SSH Profile Derivation** (`005-party-aware-ssh-profile-derivation.md`, tier `sonnet-med`) — SSHKeyResolver.AuthContextForOwner implements the ADR derivation: owner party with active principal profile and credential_profile_availability available gives that profile; otherwise ErrSSHKeyNotAuthorized carrying the party uuid; confined keys refused for their confining party. Exported ErrSSHKeyNotAuthorized, SSHKeyNotAuthorizedError and SSHKeyNotAuthorizedParty(err). Two round trips via coredb.ListProfilesByHolder then CredentialProfileAvailability.
   Commit `4aa0ac5`, merged at `d1f7d44e64e5e8478507f2cc015e10c2ff52ab11`.

6. **Party Flows Integration Tests** (`006-party-flows-integration-tests.md`, tier `sonnet-high`) — Added api/internal/integration/party_flows_integration_test.go with 11 tests: the ADR worked example (steps 1-6) and its confined variant, token behavior across suspend and resume, holder vs admin authority, route-created managed user not invitable, provider-policy remedy, min_acr lock and step-up unlock, confined factor, outside collaborator via routes, trusted admission via routes. No defects found in 001-005 or 007; no fixes made.
   Commit `3a115f7`, merged at `bb8dce9f127eb42b15bdafb2838beb3a0708aba0`.

7. **Trusted Profile Admission** (`007-trusted-profile-admission.md`, tier `sonnet-med`) — Added ProfileService.AdmitInTx and AdmitInput in api/internal/service/profile_admit.go. ErrProfileSuspended (409 users.profile_suspended) is new; all three are re-exported from api/usersservice. Locks the users row via GetUserByEntityIDForUpdate, then locks and re-reads the holder's open profile; reuses checkAdmissible, createInTx and transitionLocked. AGENTS.md and README.md updated.
   Commit `a60cf76`, merged at `78aca5ad65cdadca255b68b90b8884cbd359a309`.

### Phase 08 — Users Authorization Engine

1. **Rewrite Authorization Engine** (`001-rewrite-authorization-engine.md`, tier `opus-high`) — Production engine rewritten (api/internal/authz/authz.go). Prelude per check: assurance gate (409 users.step_up_required), owning-party gate (403), then wildcard arm, entity/type-entity arm, owned-scope arm with include_subtypes, own arm gated on ownership_confers, plus AuthorizeTypeIn. Requirement 13 literal reading holds operator profiles to customer-party policies, which broke 14 phase-7 HTTP scenarios; these were narrowly skipped and raised for a decision. Two mod-core gaps filed (Ddx9, qvSR).
   Commit `b81ed0a`, merged at `3955f4d6fc9d30a6b87367db0e32e36bc11f1482`.

2. **Authorization Engine Tests** (`002-authorization-engine-tests.md`, tier `sonnet-high`) — Authorization engine test suite added (requirements 2-6): own-arm, owned-scope, AuthorizeTypeIn, assurance gate, parity matrix, client-credentials gate. Test code only; no production change. Mutation check confirmed new tests catch removed owner and ownership_confers predicates. Integration run: 132 passing, no skips.
   Commit `c44b3e5`, merged at `4386907a9a770235605018caa4617b2579109785`.

3. **Impersonation Sessions** (`003-impersonation-sessions.md`, tier `sonnet-high`) — Impersonation is now a child session: ImpersonationService (start, renew, end) and ImpersonationHandler on /v1/impersonations, wired through facades, main.go, manifest and openapi. Switch and fork refuse inside an impersonation (Fork returns ErrImpersonationNotAllowed). Start order 400, 403, authorize impersonate, 404, 409. Operators reach restrictive-policy party members per Decision D; no skip helper. Filed followups 0chQ (renew does not re-authorize administrator impersonate authority) and Q1lB (stale docs for removed /assume route).
   Commit `5ffa05b`, merged at `cd10cc4d1abaeaa55fc9c5248161dc4dc25aedac`.

4. **Credentials Operation and App Login Gate** (`004-credentials-operation-and-app-login-gate.md`, tier `sonnet-med`) — Added four /v1/principals/{uuid}/credentials routes under the credentials operation (owner rotation, party-admin owned-scope, operator wildcard). Replaced the invite_only stub with a real Authorize(login, app) check in session availability and in the per-request authentication middleware. NewAuthenticatorWithAppGate and SetAppGate added; NewAuthenticator signature unchanged. SSH-key administration no longer authorizes assume. Tests: unit and integration, including ordertest route-order cases.
   Commit `974157a`, merged at `3d8cd9db69540c98514aef48d383fc93d6bd26d8`.

5. **Personal Party** (`005-personal-party.md`, tier `sonnet-med`) — Added EnsurePersonalParty (user-row lock for idempotency, owned-scope manage grant, three audit ops) with facade, manifest entry and unit and integration tests, including the B6 sequence and the U28 limitation. Re-ran phase 7 party flows, the ADR worked example and remediation tests with non-operator owned-scope administrators, plus a refusal case per flow. All suites pass against a real Postgres.
   Commit `ca1ae46`, merged at `b8afa14489472a31c39a102a802ed05e75d09468`.

### Phase 09 — Users GUI and Frontend Contract

1. **Rebuild API Client and Auth Context** (`001-rebuild-api-client-and-auth-context.md`, tier `sonnet-high`) — Rebuilt gui/src/lib on final API; lib tests pass 223/0; component-level failures deferred to tasks 002-005 by design; jRXp filed as type:conflict.
   Commit `dd54577`, merged at `2f1332083240238e7e6cefdf182bffcc40095ef0`.

2. **Build Profile Chrome** (`002-build-profile-chrome.md`, tier `sonnet-med`) — Built profile chrome (switcher, lock notice, selection page, invitations view, impersonation banner); updated SidebarNav, RequireAuth, ClientLayout with no isAdmin; 28 new tests, stories, README. Remaining failures belong to tasks 003-005.
   Commit `cbdeee5`, merged at `1ac49188f0fa2d05fa0ad4a1ba9fbfbfb72748c5`.

3. **Port Auth Pages to Session API** (`003-port-auth-pages-to-session-api.md`, tier `sonnet-med`) — Ported public sign-in pages to the final session API and the #handoff flow; selection_required routing; users.* error mapping; OIDC start via api.auth.oidcStart; VerifyEmailPage via /v1/me/identifiers; tests, stories, README updated. Finding AReg filed (selection page loses return path on reload).
   Commit `a250ccb`, merged at `76a062c83bfcb859bc835be03429caa363524bce`.

4. **Build Self Service Panels** (`004-build-self-service-panels.md`, tier `sonnet-med`) — Built self-service panels (StepUpDialog, CredentialsPanel, IdentifiersPanel, SessionsPanel, GuestUpgradeForm) on the rebuilt client; SSHKeysPanel removed; step-up, last-credential, sign-out and guest-upgrade paths tested. Known out-of-scope: docs/mod-users-spec.md line 188 still describes old X-Step-Up-Token semantics.
   Commit `e311879`, merged at `02349fca64ff8e4f79a9cfd0d471e0c5c0c8ebd8`.

5. **Build Bootstrap and OIDC Config Pages** (`005-build-bootstrap-and-oidc-config-pages.md`, tier `sonnet-med`) — Built bootstrap page, setup gate routing across bootstrap and OIDC state, OIDC config page without isAdmin, JIT-mode and trust-ACR provider fields, README first-run section. 421 tests pass; lint and build pass.
   Commit `564a7c5`, merged at `42e8c441372cc5e87f5489aee584fda6da6edd36`.

6. **Review GUI Exports and Contract Docs** (`006-review-gui-exports-and-contract-docs.md`, tier `sonnet-low`) — Verification pass over merged GUI work: exports, stories and contract docs consistent. README Other exports table added. Manager to remove followups HPJi and PRvD as resolved.
   Commit `2588970`, merged at `73845c51f150715396f9deae0cc187183c17b9b4`.

### Phase 10 — Documentation Updates

1. **Update Architecture Docs** (`001-update-architecture-docs.md`, tier `sonnet-high`) — Rewrote architecture, spec, ssh-keys and jwt-eddsa docs against merged code; added sessions-and-tokens.md; reflected OIDC credentials:include exception, step-up correction, impersonation rules and planned data-protection-class design. Docs only.
   Commit `62648fd8`, merged at `9d8a5be031da5904c161440ac76c643d295f384e`.

2. **Write Consumer Upgrade Notes** (`002-write-consumer-upgrade-notes.md`, tier `sonnet-med`) — Added Identity model rewrite section to consumer-upgrade-notes.md (Go packages, routes, config, tokens, database, manifest, bootstrap, impersonation, per-consumer migration); rewrote EdDSA, GUI, cookie, error-precedence and authorization sections against merged code; fixed hPqK anchor. Filed findings ndUS (CORS X-Step-Up-Token) and iZ3E (usersservice facade aliases). Docs only.
   Commit `24f6ef32`, merged at `6030f9691fa43f994017fe33304e675aea4adb02`.

3. **Review Repository Docs** (`003-review-repository-docs.md`, tier `sonnet-low`) — Repository docs reviewed and corrected: removed stale step-up, account-merge and plan-phase references; documented config variables in .env.example; rewrote model/README.md baseline section; removed completed or obsolete next-steps items. Code issues (CORS headers, credentials) recorded as findings. Docs only.
   Commit `23ceca5f`, merged at `1795184ae2d27d4ba9b1b9af0ac75c2106101e62`.

### Phase 11 — Users Identity Final

1. **Mod-Users Documentation Drift Scan** (`001-mod-users-drift-scan.md`, tier `sonnet-low`) — Scanned mod-users for old-model terms; docs already clean. Fixed 10 stale code comments, left legitimate history matches, reported 3 followups. All build, test, lint and verify checks pass.
   Commit `f52c194a`, merged at `a8d222d786044af3fda8c05510281af6d58456d8`.

2. **Reconcile Superseded Mod-Users Followups** (`002-reconcile-superseded-followups.md`, tier `haiku-med`) — Reconciled 21 mod-users followups read-only: 13 confirmed superseded with proposed removals, 2 closed cross-project, 6 retained for manager decision. No followups mutation made.
   Commit `4fc8ace6`, merged at `296305fa9c431996c281d22bfcea11259f3dfc18`.

### Phase 12 — Remediation Round 1

1. **Enforce Allow BYO Identity on Guest Sign-In and Guest Upgrade** (`001-enforce-byo-identity-on-guest-paths.md`, tier `sonnet-med`) — Added the allow_byo_identity check to POST /v1/auth/anonymous (create and device_token sign-in) and to SelfUpgradeGuest. Both return 403 users.byo_identity_disabled, with an integration test and openapi.yaml docs.
   Commit `050fd51`, merged at `1af9df6`.

2. **Exclude Revoked Factors From Profile Availability** (`002-exclude-revoked-factors-from-availability.md`, tier `sonnet-med`) — profile_availability now joins session_factors to credentials with revoked_at IS NULL. RevokeCredentialsOfPrincipal and RevokeCredentialsConfinedToParty were removed as uncalled; sqlc regenerated. Integration test TestInteg_Availability_RevokedFactorDoesNotCount added.
   Commit `476468a`, merged at `1d45242`.

3. **Bind OIDC Sign-In To The Credential Provider** (`003-bind-oidc-login-to-provider.md`, tier `sonnet-med`) — Added a provider_id comparison to OIDCResolver.Resolve so a credential presented through a different provider with the same issuer and subject is refused with RedirectErrAuthFailed. Unit test covers it.
   Commit `baa0176`, merged at `d343508`.

4. **Harden Audience Token Claims** (`004-harden-audience-token-claims.md`, tier `sonnet-low`) — Audience tokens can no longer be given acr, amr or auth_time through extra. They now carry the aud+jwt typ header, and the access-token verifier rejects that header. Tests and docs note included.
   Commit `327645a`, merged at `d4b89a3`.

5. **Compose Mod-Audit Migrations Into The Model Schema** (`005-compose-mod-audit-migrations.md`, tier `sonnet-low`) — Compose now includes the mod-audit migrations, and the integration harness uses the composed directory directly. Integration suite passes.
   Commit `5e56852`, merged at `fc07a3a`.

6. **Register Display Renderers Through The Manifest** (`006-register-display-renderers-in-manifest.md`, tier `sonnet-med`) — Manifest now registers the user and profile display renderers via NewUserServiceWithSelfService taking a trailing display.Registry; main.go uses the facade. Unit test added.
   Commit `7ffcc83`, merged at `b9d20b3`.

7. **Add Missing Password And Challenge Indexes** (`007-index-credential-and-challenge-lookups.md`, tier `sonnet-low`) — Added five missing indexes to the baseline migration and rewrote NULL-equal subject predicates in the two challenge queries to the COALESCE form so the cooldown index serves them. Verify, integration and EXPLAIN pass.
   Commit `eb61cb6`, merged at `891392e`.

8. **Update Deploy Manifests For EdDSA Signing Key** (`008-update-deploy-manifests-signing-key.md`, tier `sonnet-low`) — Updated both deploy manifests to the EdDSA signing-key configuration, with JWT_SECRET optional and comments on the DB fallback and AUTH_* variables; manifests parse.
   Commit `603c765`, merged at `0cfa2ab`.

9. **Link Deploy And Model READMEs From Root README** (`009-link-unreachable-readmes.md`, tier `sonnet-low`) — Added four README links from the root README to model, deploy/local, deploy/k8s and deploy/serverless. Docs-only.
   Commit `ab8ccb6`, merged at `7daac3e`.

10. **Confine Linked OIDC Credentials To The Provider Party** (`010-confine-party-bound-oidc-link.md`, tier `sonnet-med`) — Linked OIDC credentials from a party-bound provider are now confined to that party. Party-less providers unchanged. Both cases have integration route tests and a docs note.
   Commit `eb6bdea`, merged at `8dff09f`.

11. **Make Identifier Removal Work With Nullable Credential FKs** (`011-identifier-removal-set-null-fks.md`, tier `sonnet-high`) — cred_password and cred_email_otp identifier_id now nullable with ON DELETE SET NULL, plus CHECK cred_email_otp_active_needs_identifier. RemoveIdentifierInTx unchanged (revoke-first). Integration tests added and existing 409 test updated to 204.
   Commit `3d38718`, merged at `2729fe4`.

12. **Copy Party Authorizations When A Password Is Superseded** (`012-copy-party-authorizations-on-supersede.md`, tier `sonnet-med`) — Added CopyCredentialPartyAuthorizations query and CredentialService.SupersedePasswordInTx; password reset now revokes old credential, creates replacement, and copies party authorizations atomically. Integration tests added.
   Commit `ff07d2c`, merged at `398050f`.

13. **Bind The OIDC Handoff Code To A Browser Verifier** (`013-bind-handoff-code-to-verifier.md`, tier `sonnet-high`) — Bound the OIDC handoff code to a browser-held S256 verifier. Challenge travels in signed state and is stored on the login_handoff challenge. Exchange verifies verifier in constant time inside the consuming transaction. Schema, API, openapi and docs updated; unit and integration tests pass.
   Commit `5bd0bba`, merged at `fb0fab1`.

14. **Exclude Archived Apps From Token Audience Checks** (`014-exclude-archived-apps-from-audience.md`, tier `sonnet-low`) — Both audience-liveness queries now exclude apps whose entity row is archived. Integration test covers active-accepted and archived-rejected cases; spec notes the policy.
   Commit `042c198`, merged at `640b50f`.

### Phase 13 — Remediation Round 2

1. **Guard Active Passwords Against Identifier Unbinding** (`001-guard-active-password-identifier-unbind.md`, tier `sonnet-high`) — Added DB-level guard so an active password cannot be unbound from its identifier (SET NULL cascade fails 23503, mapped to 409 identifier_in_use). removeIdentifier row-locks the identifier first. Covered by new integration and unit tests; suites green.
   Commit `fa061be`, merged at `de9ae2ff2ebafbc9523f4aaceea11ec27bc03e90`.

### Phase 14 — Remediation Round 3

1. **Require Verified Email For Invitations And Authorize Managed-User Passwords** (`001-require-verified-invitee-and-authorize-managed-password.md`, tier `sonnet-high`) — Unverified email identifiers now resolve as not-found for invitations (profile_service.go resolveInvitee); managed-user provisioning under an authorized credential rule authorizes the new password in the same transaction; integration and unit tests added; full integration suite passed.
   Commit `d8eef20`, merged at `c9014c5`.

2. **Gate Holder Leave On Profile Availability** (`002-gate-holder-leave-on-profile-availability.md`, tier `sonnet-high`) — LeaveProfile now requires profile_availability to be available for the caller's session, checked in the leave transaction. LeaveProfile and the administrator's PATCH to ended also refuse with 409 users.last_operator_admin to end the last active wildcard-manage operator admin. The last-admin check serializes on the platform_settings row. Suspended-profile gap filed as finding twos.
   Commit `d977d63`, merged at `2164583219c3cda8243a5dea85d12926c15d2972`.

3. **Scope The Setup Token On OIDC Configuration Routes** (`003-scope-setup-token-on-oidc-routes.md`, tier `sonnet-high`) — Narrowed the setup token on the OIDC config routes to the unconfirmed-OIDC window using a fresh DB-backed check. Bootstrap behaviour is unchanged, and docs and tests are updated.
   Commit `16678b8`, merged at `b2a026a1ff33abc3d82afdba7d20769b3e38a46f`.

4. **Serialize Credential Gate And Invitations With Profile Writers** (`004-serialize-profile-writers-with-endings.md`, tier `sonnet-high`) — gateCredentialWrite locks the credential then takes FOR SHARE on the holder's open profile; resolveInvitee takes the same holder lock as AdmitInTx. Two concurrency integration tests added. Lock order credential-then-profile differs from EndInTx; a deadlock is theoretically possible, flagged for review.
   Commit `dba63b9`, merged at `e210c0902bbcbdc86c351ace00fb89f579d64bd6`.

5. **Fix NULL Handling In Policy SQL Functions** (`005-fix-policy-sql-null-handling.md`, tier `sonnet-med`) — Fixed both NULL-handling defects in 0100_baseline.sql, added one integration test for each, and tweaked the 409 message for a zero holder acr. Verified on a throwaway Postgres.
   Commit `7b08d96`, merged at `366a81406d146a5235918daecf018cd69232307a`.

6. **Correct Step-Up Order And Bootstrap Exception Docs** (`006-correct-openapi-and-architecture-doc-drift.md`, tier `sonnet-low`) — Corrected the OpenAPI step-up ordering for principal disable and enable and added POST /v1/bootstrap to the two documented-exception lists in docs/architecture.md. openapi.validate could not run (python3 yaml module and spectral missing); Ruby YAML parse only.
   Commit `5ade1e4`, merged at `81ed4fcec679f3f8baffda8d0634743827439ad0`.

7. **Restrict Administrator Confinement To Party-Controlled Credentials** (`007-restrict-admin-confinement-to-party-controlled-credentials.md`, tier `sonnet-high`) — The administrator door of ConfineCredential now confines only party-controlled credentials (managed-user, and OIDC from a party-bound provider) and otherwise returns 404. The holder door of UnconfineCredential is refused for party-controlled credentials. The held-profile check requires active. No schema change. Four existing tests adapted. OpenAPI, README, AGENTS.md and doc comments updated.
   Commit `843012c`, merged at `d9a8e781381f51fa7cee9b012918291a323e91ba`.

8. **Treat Authorized Credential Rule As Satisfied At Invite Time** (`008-authorized-rule-invite-time-semantics.md`, tier `sonnet-high`) — Implemented decision B. The authorized rule is met at invite time by any usable interactive credential, so no confirmation row is written for that case. A confirmed mismatch now needs update on the party, else 403, and a credential_rule change clears the party's confirmations. Four integration tests added and four existing tests migrated to the provider rule. Docs and openapi updated.
   Commit `f3014ca`, merged at `488b807967f7c28d45cb66956666eede212c41b7`.

### Phase 15 — Remediation Round 4

1. **Take The Holder Profile Lock Before The Credential Lock In The Credential Write Gate** (`001-order-credential-gate-locks-profile-first.md`, tier `sonnet-high`) — Swapped the lock order in the credential write gate to profile (FOR SHARE) then credential (FOR UPDATE), commented the order, and added deterministic and looped race integration tests. The deterministic test reproduces 40P01 on the old order and passes on the new one.
   Commit `dccd764`, merged at `4d61b975ff55c66a26d116c6e795a66b86997918`.

2. **Evaluate The Party Gate When A Holder Leaves A Suspended Profile** (`002-gate-suspended-profile-leave.md`, tier `sonnet-high`) — LeaveProfile now decides on a status-agnostic party-gate evaluation for both active and suspended profiles. A locked-out session leaving a suspended profile gets 401 users.profile_unavailable and nothing changes. The departure cleanup runs only when the gate is met. Edited the baseline migration in place, as earlier tasks did.
   Commit `804fcdc`, merged at `fd62b6610d477f5914aae157050b700a5328471b`.

3. **Extend The Last Operator Admin Guard To Suspension And EndInTx** (`003-extend-last-operator-admin-guard.md`, tier `sonnet-med`) — The last-operator-admin guard now runs inside transitionLocked for every transition out of active, so suspension, EndInTx, admin ending and holder leave inherit it. The explicit guard calls in profile_admin.go and profile_holder.go were removed. The guard takes the platform_settings lock first (operator-party profiles only), then runs one count query.
   Commit `4724343`, merged at `1284830528a10d3ac66a7c518df37535c2a3638e`.

4. **Verify The Setup Token From A Local Fresh Read Without Rewriting The Shared Cache** (`004-verify-setup-token-without-cache-writes.md`, tier `sonnet-med`) — verifySetupToken now short-circuits on cached state, does a local fresh read, and never touches the shared cache. Docs and unit tests are updated. The integration test compiles but did NOT run: no usable Postgres (no users superuser reachable; the shared localhost:5432 instance is refused without AUTHZ_DEV_PG_CONFIRM_SHARED=1, which was not set).
   Commit `2dc69b9`, merged at `e7fcb652ea6b5cd441d6135cddfdde1fec58a7d2`.

5. **Purge Policy Confirmations On Provider Change And Serialize Them With Policy Writes** (`005-purge-confirmations-on-provider-change.md`, tier `sonnet-med`) — PutAuthnPolicy purges confirmations on a change to credential_rule or required_oidc_provider_id, under a per-party advisory lock plus policy-row lock. Invite and collaborator-gate paths take the shared lock before policy or confirmation access. Integration suite ran for real against a throwaway postgres:16 container (not the shared 5432 instance).
   Commit `c20e828`, merged at `2fe2cf9045669e20c31cfd3f2a89b7a170dbdaf2`.

6. **Require Party Update Before Auto-Authorizing A Managed User Password** (`006-require-party-update-for-managed-password-authorization.md`, tier `sonnet-med`) — ProvisionManagedUserInTx writes the credential_party_authorizations row under an authorized party only when the caller holds update on the party. Without update the user is still provisioned and the password stays unauthorized. New integration test covers the no-update case; all validation passed.
   Commit `ab51ada`, merged at `c6eca2494481fe3ac4d07487df50f367f31944c2`.

7. **Exclude Disabled Principals From The Last Operator Admin Count** (`007-exclude-disabled-principals-from-admin-count.md`, tier `sonnet-med`) — operatorAdminProfileSQL joins principals on holder_id with disabled_at IS NULL, so disabled-principal admins are not counted as remaining. New integration test covers the disabled second admin. Remaining gaps (mod-authz grant revocation, gate-locked admins, group-inherited admins) are documented as the guard boundary.
   Commit `b6b6a51`, merged at `27752c73fe1d0e3361d00a8691cc04c1a5bb5ee7`.

8. **Serialize Managed User Provisioning With Authn Policy Writes** (`008-serialize-managed-user-provisioning-with-policy.md`, tier `sonnet-med`) — ProvisionManagedUserInTx calls lockPartyPolicyShared before GetPartyAuthnPolicy (FOR SHARE on the policy row, or shared advisory lock with no row), serializing provisioning against PutAuthnPolicy. Two deterministic lock-ordering integration tests added; both fail with the lock removed and pass with it. Integration suite ran in full against a throwaway postgres:16-alpine container.
   Commit `b6e9f7c`, merged at `8cdf04a2d788728c3cbefc0f1f282f52d8ef9767`.

### Phase 16 — Remediation Round 5

1. **Keep Collaborator Policy Mismatch Strict Under Authorized Rule** (`001-strict-collaborator-policy-mismatch.md`, tier `sonnet-high`) — _No digest recorded for **Keep Collaborator Policy Mismatch Strict Under Authorized Rule** — its outcome is in `plan/phase-16-remediation-05/001-strict-collaborator-policy-mismatch.md`._
   Commit `64b4de5`, merged at `c59b436ff64b0727e4dc388cefa64b8d08ae9da2`.

2. **Serialize Principal Disable With Last-Admin Guard And Fail Closed On Nil Policy Lock Tx** (`002-close-admin-disable-race-and-fail-closed-policy-lock.md`, tier `sonnet-med`) — _No digest recorded for **Serialize Principal Disable With Last-Admin Guard And Fail Closed On Nil Policy Lock Tx** — its outcome is in `plan/phase-16-remediation-05/002-close-admin-disable-race-and-fail-closed-policy-lock.md`._
   Commit `e69d2cd`, merged at `e7fc44e5fb938373df38b6ed09a6e475a09e154a`.

3. **Document Lock Order And Correct Phase 15 Contract Drift** (`003-document-lock-order-and-phase-15-contracts.md`, tier `sonnet-med`) — _No digest recorded for **Document Lock Order And Correct Phase 15 Contract Drift** — its outcome is in `plan/phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md`._
   Commit `8512aca`, merged at `669aeb893894822d9112bf21ede0e2a6cf1c54ee`.

4. **Run Bootstrap Token Lifecycle Integration Test And Confirm RefreshState Wiring** (`004-run-bootstrap-token-lifecycle-integration.md`, tier `sonnet-med`) — _No digest recorded for **Run Bootstrap Token Lifecycle Integration Test And Confirm RefreshState Wiring** — its outcome is in `plan/phase-16-remediation-05/004-run-bootstrap-token-lifecycle-integration.md`._
   Commit `11be668`, merged at `46baa372332f16d5aca4e10b26bda20c777fc19a`.

### Phase 17 — Remediation Round 6

1. **Align Last Operator Admin Error And Contract With Principal Disable** (`001-last-operator-admin-disable-contract.md`, tier `sonnet-low`) — Widened the shared last-operator-admin 409 (field and message) to cover the disable path. Updated godoc, OpenAPI disable description and AGENTS.md row. Build, unit and integration tests pass.
   Commit `7d76b18`, merged at `a4ca8c626583d60fbbbf0794bd3ceaa1abe38e26`.

2. **Scope The Policy Lock Test Seam Per Test And Assert InviteTime Mode** (`002-scope-policy-lock-test-seam-and-assert-invite-time.md`, tier `sonnet-med`) — Replaced the package-level locker var and test init() with a per-instance nil-defaulting policyLock seam on CollaboratorGate and ProfileService. Non-opted-in tests run the fail-closed production locker. HolderPolicyMismatch fake captures params; InviteTime asserted for both paths; flips confirmed to fail their tests.
   Commit `1eebe71`, merged at `c0760b505052bc570a5868f0c9b3a1e64112abf0`.

### Phase 18 — Remediation Round 7

1. **Gate The Party Record By Its Own Policy And Document One-Level Ownership** (`001-party-is-own-owning-party.md`, tier `opus-med`) — Authorize treats a legal_entity target as its own owning party (Decision F); checkPartyPolicy requires both target party policy and direct owner policy. Closes bYK4 and N4y2. One-level ownership documented. Owner gate kept (confirm reading). Integration suites pass on throwaway postgres:16.
   Commit `51d7959`, merged at `9f9539e734a30ce291f225b55def83eac8b4c87f`.

2. **Apply The Impersonated Actor's Party Rules During Impersonation** (`002-impersonated-actor-rules-apply.md`, tier `opus-med`) — Removed the blanket impersonation skip of credential_rule. New SQL helper mod_users.session_policy_factors makes impersonation sessions face the impersonated profile's own party gate and every owning-party gate using the administrator's parent-session factors and acr. Only a platform-operator administrator skips credential_rule; min_acr always applies. Operator status approximated at principal level until task 004 records the acting profile (finding crQE); confinement of parent factors left as before (finding bFGy). Integration tests ran and passed against throwaway postgres:16.
   Commit `d143bca`, merged at `11e3902c3a82efdb59e66cf59f9fe7c3dc455fe3`.

3. **Refuse Credential Administration And Personal-Party Creation While Impersonating** (`003-credentials-refuse-impersonation.md`, tier `sonnet-high`) — Credential administration (list, issue, install, revoke) and EnsurePersonalParty refuse an impersonating context with users.impersonation_not_allowed. Credential routes also refuse the caller's own principal with 403, closing the last-credential and max-auth-age bypass. Child impersonation session inherits the parent's auth_time. Unit and integration tests cover each; all suites pass. Integration tests ran.
   Commit `bfd3919`, merged at `d1d7eaaf80f6bf73ec97c46a4135795b0af98ab3`.

4. **Re-Check Impersonate Authority From The Parent Session On Renew** (`004-impersonation-renew-recheck.md`, tier `opus-med`) — Added mod_users.sessions.impersonator_profile_id (baseline edited in place, check and trigger extended, sqlc regenerated), set in Start. Renew re-authorizes impersonate from the parent session as the recorded acting profile, after checking that profile is still available. A refusal revokes the child (impersonation_ended) and returns 403. max_auth_age deliberately not re-applied. Integration tests ran and passed (authz, config, integration). Validation check 3 (API surface 2.9 amendment) not done: plan note is manager-owned.
   Commit `0b4d07e`, merged at `9f3f777fde366ba6cbe0f6b6da7362f3c126fd47`.

5. **Pin Sessionless Contexts Away From Authorize** (`005-pin-sessionless-contexts.md`, tier `sonnet-high`) — Added sessionless-context pinning tests: behavioral test that the sessionless write paths never reach Authorize; AST audit pinning the set of 11 functions that build or call sessionless-context helpers and asserting none calls an Authorize entry point. Comment on authz gated documents that sessionless trust is inferred from absent fields pending a mod-core system-actor marker. No gate logic or SQL changed. Integration tests ran and passed (authz, config, integration). The mod-core system-actor marker (j75L real fix) remains to be filed separately.
   Commit `56213b5`, merged at `c3bf76474fc65f9150b8624331787abf60bbed5c`.

6. **Map Step-Up Refusals In The Invite-Only App Gates** (`006-app-gate-step-up-mapping.md`, tier `sonnet-high`) — _No digest recorded for **Map Step-Up Refusals In The Invite-Only App Gates** — its outcome is in `plan/phase-18-remediation-07/006-app-gate-step-up-mapping.md`._
   Commit `5c96e4c`, merged at `57b94b1d6b72a828243cce230d93b9ae3f7ed278`.

### Phase 19 — Remediation Round 8

1. **Key The Impersonation Credential-Rule Exemption On The Recorded Acting Profile** (`001-impersonator-profile-credential-exemption.md`, tier `opus-med`) — Fixed DChD, crQE, bKBt: credential-rule exemption depends on the recorded impersonator_profile_id (active, core_is_operator_profile). Baseline edited in place; new HTTP-level dual-profile integration test; green on throwaway postgres:16.
   Commit `b721fcc`, merged at `20d83de`.

2. **Apply Credential Confinement To Non-Operator Impersonators** (`002-confine-impersonator-parent-factors.md`, tier `opus-med`) — Applied Decision L in 0100_baseline.sql: profile_gate_for and session_meets_party_policy confine non-operator impersonations (keyed on impersonator_profile_id); operators stay exempt. New integration and SQL-level tests; mutation-checked; green on throwaway postgres:16.
   Commit `042f83d`, merged at `51822c3`.

3. **Refuse Managed-User Creation And Credential Writes While Impersonating** (`003-refuse-managed-user-creation-impersonating.md`, tier `opus-med`) — Managed-user creation refuses impersonating context with 403 users.impersonation_not_allowed in CreateManagedUser and ProvisionManagedUserInTx; integration test shows no rows written; fails without fix. Profile-ending departure cleanup under impersonation raised as a decision.
   Commit `927252d`, merged at `b99e5fc`.

4. **Tighten Nesting, Party-Record Gate And Sessionless Audit Tests** (`004-test-precision-nesting-and-static-audit.md`, tier `sonnet-high`) — Tightened three test suites with no production changes: nesting SQL check isolates no-nesting invariant; party-record gate pins 404 with positive control; static audit ignores literal-zero SessionID/CredentialID. Mutation and literal demonstrations failed as expected and were reverted.
   Commit `71d8c39`, merged at `2b21a71`.

5. **Document Impersonation Re-Auth, Refusals And Decisions G J K L M** (`005-docs-impersonation-reauth-and-decisions.md`, tier `sonnet-high`) — Brought api/openapi.yaml, AGENTS.md, the credentialsTarget comment and docs/architecture.md in line with merged behavior of tasks 001-004 (Decisions G/J/K/L, impersonation lifecycle, app-gate step-up, sessionless limitation). Verified against Go code.
   Commit `89c1c01`, merged at `a45d13a`.

### Phase 20 — Remediation Round 9

1. **Refuse Ending A Profile Under Impersonation** (`001-refuse-impersonated-profile-end.md`, tier `sonnet-med`) — Added a 403 refusal for ending a profile under impersonation in UpdateProfileStatus, ordered after validation and before the profile lookup. An integration test confirms the profile, the holder's party authorizations and the confined credentials are unchanged, and it fails when the refusal is removed. The openapi description and the Decision K doc bullet were updated.
   Commit `ee6dae3`, merged at `576ea98`.

2. **Align SQL Exemption Comments, Credential Route Error Order And Decision L Liveness Assertion** (`002-precision-sql-comments-openapi-order-liveness-test.md`, tier `sonnet-low`) — Comment-only SQL rewording, corrected refusal-order text on the four OpenAPI credential operations, and a tightened liveness assertion in the test. No behavior change.
   Commit `bda3e54`, merged at `691e7d7`.

### Phase 21 — Adopt Mod-Core Identity Engine Remediation

1. **Key The Engine Gates On The Explicit System-Actor Marker** (`001-key-gates-on-system-actor-marker.md`, tier `opus-high`) — gated() now skips the engine's assurance and owning-party gates only for opctx.IsSystemActor and the anonymous context. An unmarked sessionless context is gated and fails closed. Every AuthContext builder in api/ reviewed and commented; no class (b) producer. Findings filed: J86E (mod-authz stepUpStale), c6Dh (stale SQL comment).
   Commit `f8cfc04`, merged at `230a32d803cc66fec69f367a91a4f0092d1a933d`.

2. **Adopt The List-Side Row Filter Hook For The Owning-Party Gate** (`002-adopt-list-row-filter-hook.md`, tier `opus-high`) — Added mod_users.owning_party_gate as the one SQL owning-party decision, used by checkPartyPolicy and the new list predicate mod_users.access_row_permitted. ListPrincipals runs in a read-only transaction bound with setup.BindAuthInputs. mod-users installs mod-core's row-filter hook via localAuthz.AccessFuncGenerator in the dev server and test harnesses. Mutation checks confirm the tests catch an always-true predicate and a missing binding. Integration tests ran against a throwaway postgres:16 (removed).
   Commit `9cd73dc`, merged at `57e945943ddb5ecb003078190bb5dd6b3ea160f2`.

3. **Adopt The Profile Row Lock Query And Organization CreateInTx** (`003-adopt-profile-lock-and-org-create-in-tx.md`, tier `sonnet-med`) — Moved lockProfile onto mod-core's generated dblock.GetProfileByEntityIDForUpdate row lock and made the bootstrap create the operator organization through OrganizationService.CreateInTx with WithoutOwner(). NewBootstrapService gains a trailing orgs parameter; main.go, the harness and the manifest (new bootstrapOrganizationService entry via usersservice.OrganizationFromServices) are wired. Integration suites passed against a throwaway postgres:16 (removed); mutation check confirmed the new tests fail when the raw inserts are restored.
   Commit `e65c520`, merged at `1a6568222ee5fcc79dd2f744f4af245ef2bbc002`.

4. **Align Docs With The Six-Argument Predicate And The System-Actor Marker** (`004-align-docs-with-adopted-core-apis.md`, tier `sonnet-med`) — Rewrote the superseded sessionless-trust and list-gate statements across AGENTS.md, the architecture doc, the spec, the upgrade notes, model/README.md and the OpenAPI principals description. All claims were checked against the merged code. One stale code comment was filed as a finding (wkvN).
   Commit `255e343`, merged at `95ff9fa81b5850e7e328c00354e4c9c9526b7f3e`.

### Phase 22 — Remediation Round 10

1. **Gate System-Actor Contexts That Carry A Session Or Credential** (`001-require-sessionless-system-actor.md`, tier `sonnet-med`) — Marked system-actor contexts carrying a session or credential are now gated in gated() and access_row_permitted; stale session_meets_party_policy comment fixed; tests added and both mutation directions verified.
   Commit `f6e8dc9`, merged at `64d83d7ec053869ebbc7b1b4e073874c2444bc9e`.

2. **Apply The Operation Assurance Gate To The Principal List** (`002-apply-assurance-gate-on-principal-list.md`, tier `sonnet-med`) — ListPrincipals now runs the operation assurance gate via new Authorizer.CheckAssurance before the transaction; fails closed when the authorizer lacks the capability; OpenAPI 409 added.
   Commit `260aa65`, merged at `9f70bf3d54751adbdb3d59fc1ab3bcb3ae22b0fd`.

3. **Dispatch After-Commit Observations For CreateInTx Creations** (`003-dispatch-after-commit-observations.md`, tier `sonnet-med`) — After-commit create observations now dispatched post-commit at Bootstrap, Register, OIDC just-in-time creation, and guest upgrade; no dispatch on rollback; payloads carry no ssn or tax_id.
   Commit `e4bea51`, merged at `68c6cb9483db169e9601347ca2b3f42fd20bc148`.

### Phase 23 — Remediation Round 11

1. **Fix Auth Context User Loading, Hydrate Failures and Logout Revocation** (`001-fix-auth-context-session-lifecycle.md`, tier `sonnet-med`) — Fixed all three auth-context lifecycle defects: pending user load counts as loading; hydrate keeps tokens and exposes sessionError and retrySession on status 0 and 5xx; logout refreshes once after 401 and retries revoke, with documented local-only fallback. Tests added for each.
   Commit `18ee982`, merged at `a2ebcf6b82bd0d7fda26f798e407a748e3be8075`.

2. **Handle OIDC Link and Step-Up Return Fragments** (`002-handle-oidc-link-and-step-up-returns.md`, tier `sonnet-med`) — OIDC return page handles linked=1 and stepped_up=1 without a handoff: validates return path with isSafeReturnPath falling back to default; stepped_up runs a refresh grant before returning; refresh failure surfaces via onError. Five tests added in oidc-callback-page.test.tsx.
   Commit `5525f60`, merged at `3e1c1cc79d0754c0b00c5b63e695ea2519dd4a1f`.

3. **Stop Refresh-and-Replay of Impersonated Requests as the Administrator** (`003-stop-impersonation-refresh-replay.md`, tier `sonnet-med`) — Stopped refresh-and-replay of impersonated requests: per-tab impersonation marker; non-revocation 401 on impersonated token clears the dead impersonation token and emits impersonation_ended with no refresh and no replay; End handles impersonation_ended without replaying DELETE and restores the administrator via refresh grant with dedup; reload with expired impersonation token restores the administrator. Tests added in api.test.ts and impersonation-banner.test.tsx.
   Commit `177f169`, merged at `9c74ec8e3c66b57b2ac301950254b7f21ef6b0e0`.

4. **Persist Pending Profile Selection and Return Path Across Reload** (`004-persist-pending-profile-selection.md`, tier `sonnet-med`) — Persisted pending profile selection list and safe return path per tab (config keys selection and selection_return); doRefresh without profile uuid publishes selection_required and throws 401 instead of signing out when a stored selection exists; establishSession restores selection state on reload; finishSignIn stores the return path; SelectProfilePage reads prop or stored path and passes resumePath to onSelected.
   Commit `9658c06`, merged at `3d80ff28b19ddb17856c77bb9cc37c905723a045`.

5. **Route First Verification-Code Send Failure to the Dialog** (`005-route-first-verify-send-error-to-dialog.md`, tier `sonnet-low`) — sendCode now routes its failure by an explicit inDialog flag instead of the render-time verifyTarget; a first-send failure on opening the verify dialog shows in the dialog, not the page banner. One test added in identifiers-panel.test.tsx.
   Commit `ad47372`, merged at `ac2e7c4bb25c6c6c28eece595b4d4d7a205d1e19`.

6. **Document the App Option as a UUID** (`006-document-app-option-as-uuid.md`, tier `haiku-med`) — Switched five required app-option doc comments plus one story doc comment from slug to uuid, matching api/openapi.yaml and api.ts. Both validation checks pass.
   Commit `c4a6ecf`, merged at `399279c2a49331c53813b026a90d9b6f5200e838`.

### Phase 24 — Remediation Round 12

1. **Route the OIDC Step-Up Refresh Through the Single-Flight Refresh** (`001-route-step-up-refresh-through-single-flight.md`, tier `sonnet-med`) — Routed post-step-up rotation through shared per-tab single-flight via new client method; concurrent hydrate test added.
   Commit `dc7c836`, merged at `d87369b247d12473bfb1d37b9289942b4be4b4c9`.

2. **Make Impersonated-Request Detection Fail Closed** (`002-fail-closed-impersonation-detection.md`, tier `sonnet-med`) — Impersonation detection checks marker and token's unverified act claim; step-up token-store path keeps marker consistent; two tests added; suite green.
   Commit `1d6c29f`, merged at `4caaf219270959cd296e9035214e47f68b06297b`.

3. **Revoke the Administrator Session When Logging Out While Impersonating** (`003-revoke-admin-session-on-impersonated-logout.md`, tier `sonnet-med`) — Logout restores the administrator session on users.impersonation_ended and revokes it; local state cleared in every case; one test added; all gui checks pass.
   Commit `d4a2648`, merged at `7224fe14de70d598b1e20512bcd29d5a0e5f40e4`.

### Phase 25 — Remediation Round 13

1. **Revoke the Administrator Session on Every Impersonated Logout and Guard Late State Commits** (`001-harden-logout-while-impersonating.md`, tier `sonnet-med`) — _No digest recorded for **Revoke the Administrator Session on Every Impersonated Logout and Guard Late State Commits** — its outcome is in `plan/phase-25-remediation-13/001-harden-logout-while-impersonating.md`._
   Commit `89fe62a`, merged at `d6af915a9e575c53dab643865899a3e3cf026100`.

2. **Make the Impersonation Marker Fail Closed for Undecodable Tokens and Handler-Supplied Step-Up Tokens** (`002-tighten-impersonation-marker-fail-closed.md`, tier `sonnet-med`) — Tightened the impersonation marker so undecodable stored tokens are treated as impersonating, and made the step-up path set-only. Migrated the GUI test fixtures to JWT-shaped tokens and added tests for both scenarios.
   Commit `91a3006`, merged at `c316182af02cc71bf117086dee32ed946d81c047`.

3. **Signal and Test the Impersonating No-Op in refreshAfterStepUp** (`003-signal-step-up-refresh-skip-while-impersonating.md`, tier `sonnet-low`) — Made refreshAfterStepUp return false when skipped while impersonating. OidcCallbackPage now reports an error instead of completing, and both behaviours are covered by tests.
   Commit `9709ad7`, merged at `70e85ee4a26e40a1402704c1e81e5760245a5527`.

### Phase 26 — Remediation Round 14

1. **Document Persisted Signing Key Exposure** (`001-document-persisted-signing-key-risk.md`, tier `sonnet-low`) — Documented in the Persisted signing key section that the key is stored unencrypted, that access to the table or a backup allows forging tokens, and that production should set JWT_SIGNING_KEY from a secret store. Doc-only change.
   Commit `8f0c9652`, merged at `0efd1dc53b60e5ca197763fb0b8053876ea6921c`.

2. **Re-Export API Key And Profile Summary Types From Facade** (`002-alias-facade-api-key-and-profile-summary.md`, tier `sonnet-low`) — Added three facade type aliases in api/usersservice/service.go next to their services, plus a compile-only external-package test. No surprises.
   Commit `961ec79a`, merged at `af8fee69059be5fcba8297fe252f43e015080381`.

3. **Align CORS Methods Headers And Docs With Routes** (`003-align-cors-options-and-docs.md`, tier `sonnet-med`) — Aligned CORS AllowedMethods and AllowedHeaders with the routes, added a test, and documented the OIDC start POST credentials exception in docs/cors.md. No surprises.
   Commit `179d2ec3`, merged at `b070bf1690265d433cfcb40171f56cbbd2fbde14`.

4. **Correct Route Mounting And OpenAPI Coverage Notes** (`004-fix-api-layer-route-docs.md`, tier `sonnet-low`) — Reworded the AGENTS.md error-precedence bullet to match docs/architecture.md and added the /v1/oidc-config OpenAPI omission note to the architecture OIDC row. Nothing else was touched.
   Commit `55c2b891`, merged at `a20465fd4abd3968503cbb5571bbd1d81d8996c8`.

5. **Use Authentication Failed Code In OIDC Troubleshooting** (`005-fix-oidc-redirect-error-code.md`, tier `haiku-med`) — Replaced auth_failed with authentication_failed in the refusals table of docs/oidc-troubleshooting.md and in the resolver decision-table comment. Both validation checks pass.
   Commit `8de549bf`, merged at `826e63510bcd6f182b56dc4fe932d63e04c228db`.

6. **Document CORE_FIELD_KEY_HEX Ownership In Env Example** (`006-document-core-field-key-env.md`, tier `haiku-med`) — Moved CORE_FIELD_KEY_HEX out from under the Authelia/Timezone heading in .env.example into its own mod-core-owned comment block stating first-boot-only seed semantics and pointing rotation at POST /v1/field-crypto-keys/rotations. Value unchanged. No code changed.
   Commit `eb5a8a31`, merged at `7627f1ebb7733fd1d6cae43e49221546c60e26da`.

### Phase 27 — Remediation Round 15

1. **Allow X-Setup-Token In CORS And Correct CORS Docs** (`001-align-cors-setup-token-and-docs.md`, tier `sonnet-med`) — Added X-Setup-Token to CORS allowed headers with a test, corrected docs/cors.md, removed two stale Known limits lines.
   Commit `badf8c15`, merged at `ce9fcdbd`.

2. **Mark Committed Field-Encryption Seed As Development Only** (`002-mark-field-key-seed-dev-only.md`, tier `sonnet-low`) — Added a development-only / generate-fresh comment to the field-encryption seed block in .env.example. The CORE_FIELD_KEY_HEX value is unchanged.
   Commit `6617e64e`, merged at `07d788b5`.

### Phase 28 — Credential Profile Derivation Liveness

1. **Add Liveness Predicate to credential_profile_availability** (`001-add-liveness-to-credential-profile-availability.md`, tier `sonnet-high`) — credential_profile_availability now checks revoked_at (credential_revoked) then expires_at <= now() (credential_expired) in one SELECT before profile_availability_for; unknown credential stays not_held; signature, STABLE, SECURITY INVOKER, search_path unchanged; baseline edited in place, no new migration.
   Commit `dbf93997`, merged at `5706235d1afa1b5d0ea03d3cc5539daf59f0be10`.

### Phase 29 — Remediation Round 16

1. **Document CredentialProfileAvailability Status Vocabulary In sqlc Query** (`001-document-credential-profile-availability-statuses.md`, tier `haiku-med`) — Added status-vocabulary comment above CredentialProfileAvailability SELECT and regenerated model/db with sqlc; comment-only generated diff.
   Commit `d3be4994`, merged at `128eadc296dfe6df13e17c6a1a6e7a1e40054d3a`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Findings

- **`2yby`** — **Identifier delete vs bound credentials** — dismissed — 2026-10-24 — reason: Duplicate of aA5K: the same conflict. DELETE /v1/me/identifiers/{uuid} cannot delete an
  identifier once a password or email_otp credential was ever bound to it, because the
  cred_password/cred_email_otp identifier_id ON DELETE RESTRICT foreign keys meet the
  "credentials are revoked, never deleted" rule. aA5K carries the escalation.

- **`tC3F`** — **sqlc schema lacks mod-core tables** — promoted — ref: `tC3F` — 2026-10-24

- **`oi9B`** — **Identifier delete blocked by cred FKs** — dismissed — 2026-10-24 — reason: Duplicate of aA5K. It describes the same blocked identifier removal (RemoveIdentifierInTx,
  api/internal/service/user_service.go:581-629, returns ErrIdentifierInUse on 23503) and asks
  the same contract question. aA5K carries the escalation.

- **`Fhun`** — **Task 006: remove the build constraint from ap** — dismissed — 2026-10-24 — reason: Already fixed. api/internal/handlers/auth/oidc.go has no build constraint (it starts with
  "package auth"), OIDCHandler.Start (oidc.go:110) and Callback (oidc.go:156) exist, and
  `go build ./...` and `go vet ./internal/handlers/...` pass in api/.

- **`1Nqm`** — **Task 008: wire NewSessionService(pool, q, cor** — dismissed — 2026-10-24 — reason: Already fixed. api/cmd/server/main.go:376 constructs NewSessionService. main.go:428 mounts
  session.RegisterSessionRoutes and main.go:429 mounts the OIDC step-up start on /v1/session.
  api/openapi.yaml documents device_token (lines 276 and 425), the lock remedy (351-360) and
  users.app_login_not_granted (113 and 1078).

- **`ApCx`** — **Managed JIT user creation duplicates service** — promoted — ref: `ApCx` — 2026-10-24

- **`EyHp`** — **Server-error literal check has stale count** — dismissed — 2026-10-24 — reason: Already fixed. The header of api/scripts/check-server-error-literals.sh (lines 10-13) now
  states an expected count of 0. Running the script exits 0 and reports "handlers/auth/: 0/0
  documented carve-outs accounted for".

- **`C1zt`** — **Task 008 mounting: OIDCHandler.LinkStart on P** — dismissed — 2026-10-24 — reason: Already fixed. api/cmd/server/main.go:439 mounts LinkStart on POST
  /credentials/oidc/{provider}/start under /v1/me. main.go:429 mounts StepUpStart under
  /v1/session. main.go:396 sets ProvidersDeps.Parties to coreQueries, and
  api/internal/handlers/oidc_providers.go:77 consumes it.

- **`Oczs`** — **Task 010: rewrite handlers/auth/oidc_test.go** — dismissed — 2026-10-24 — reason: Already fixed. oidc_linkmode_test.go no longer exists under api/internal/handlers/auth/.
  oidc_test.go compiles against the current OIDCHandler API: `go vet ./internal/handlers/...`
  passes.

- **`LKRp`** — **Task 008 must call UserService.WithSelfServic** — dismissed — 2026-10-24 — reason: Already fixed. api/cmd/server/main.go:379 calls userSvc.WithSelfService(...), and
  moduleforge.module.yaml:134 uses the constructor usersservice.NewUserServiceWithSelfService,
  which wraps WithSelfService (api/usersservice/service.go:51-65).

- **`nRoQ`** — **Task 008 must call service.RegisterDisplayRen** — dismissed — 2026-10-24 — reason: Already fixed for the dev server: api/cmd/server/main.go:247 calls
  usersservice.RegisterDisplayRenderers(displayReg, pool). The separate gap for
  mfgen-generated apps is xgGT.

- **`bGov`** — **Task 008 must re-export ReadPrincipalSummarie** — dismissed — 2026-10-24 — reason: Already fixed. api/usersservice/service.go re-exports DirectoryService (129-136) and
  ReadPrincipalSummaries (139-143). main.go:438 mounts me.RegisterMeRoutes and main.go:445
  mounts principals.RegisterPrincipalRoutes, each inside a route group that uses
  requireAuth = auth.NewRequireAuth(authenticator) (main.go:416, 437, 444).

- **`s2Tg`** — **Task 008 must add api/openapi.yaml entries fo** — dismissed — 2026-10-24 — reason: Already fixed. api/openapi.yaml has 39 /v1/auth, /v1/session, /v1/me and /v1/principals
  path entries, including /v1/me/identifiers/{uuid} (1889), /v1/session/switch (1608) and the
  anonymous device_token route (1262).

- **`1uxT`** — **internal/handlers/oidc_config.go (kept file)** — dismissed — 2026-10-24 — reason: Already fixed. api/internal/handlers/oidc_config.go compiles: `go build ./...` and
  `go vet ./internal/handlers/...` pass. main.go:144 constructs handlers.NewOIDCConfigHandler
  and main.go:406 mounts its routes.

- **`WyI2`** — **docs/architecture.md and docs/mod-users-spec.** — dismissed — 2026-10-24 — reason: Already planned: 10/001 (update-architecture-docs). Its requirement 3 rewrites
  docs/architecture.md and docs/mod-users-spec.md for the new model, and its validation greps
  both files for /v1/self, X-Step-Up-Token and the other removed surfaces.

- **`INrh`** — **Confirm the mod-authz phase 13 merge: the age** — dismissed — 2026-10-24 — reason: Stale. mod-authz phase 13 is merged on its plan branch: mod-authz plan/identity-model-impl
  has commit 17d8d37 "plan: record merge on
  phase-13-authz-identity-schema/004-add-schema-invariant-tests.md", and the dispatch's owner
  constraints state that O2, O3 and O4 are met. Nothing remains to confirm.

- **`w9CQ`** — **Remove the leftover throwaway Postgres contai** — dismissed — 2026-10-24 — reason: Already fixed. `docker ps -a` on this host lists no container named
  mod-users-authz-integ-t009, so the leftover container is gone.

- **`CHws`** — **Aggregate make build, make test and make lint** — dismissed — 2026-10-24 — reason: Not actionable in mod-users. The failure was an environment precondition:
  @moduleforge/core-gui did not resolve because the sibling mod-core/gui was unbuilt. No
  mod-users change fixes it, and the agent noted that mod-core was not touched. The sibling
  build output now exists (/Users/zane/playground/moduleforge/mod-core/gui/dist holds
  index.css and index.d.ts). The aggregate gui build is re-run by 9/001's validation.

- **`t7jz`** — **Integration suite requires a Postgres whose c** — dismissed — 2026-10-24 — reason: Not actionable. This is an operational note: the integration suite needs a Postgres whose
  clock is within 10 s of the host. It describes no defect and no change could resolve it.
  The manager can apply it when re-running the phase-close suite.

- **`lfAN`** — **DeleteExpired (challenges), DeleteExpiredRef…** — promoted — ref: `lfAN` — 2026-10-24

- **`JFFG`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive. The link target
  ./docs/mf-standards/building-applications.md#first-time-setup is in the docs/mf-standards
  git submodule (.gitmodules; gitlink c2dc98c in the index). That submodule is not
  initialized in this plan worktree, so the checker saw an empty directory. In an initialized
  checkout (/Users/zane/playground/moduleforge/mod-users/docs/mf-standards) the file exists
  and has "## First-time setup". No document change resolves this.

- **`Bj60`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive, like JFFG. ./mf-standards/architecture.md is in the
  uninitialized docs/mf-standards submodule and exists in an initialized checkout.

- **`ukbg`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive, like JFFG. ./mf-standards/architecture/entity-typing.md is
  in the uninitialized docs/mf-standards submodule and exists in an initialized checkout.

- **`RkWd`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive, like JFFG. ./mf-standards/architecture/db-considerations.md
  is in the uninitialized docs/mf-standards submodule and exists in an initialized checkout.

- **`57rZ`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive, like JFFG. ./mf-standards/architecture.md is in the
  uninitialized docs/mf-standards submodule and exists in an initialized checkout.

- **`O1Ym`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive, like JFFG. ./mf-standards/architecture.md (from
  docs/mod-users-spec.md) is in the uninitialized docs/mf-standards submodule and exists in an
  initialized checkout.

- **`bYyY`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive, like JFFG.
  ./mf-standards/building-applications.md#first-time-setup is in the uninitialized
  docs/mf-standards submodule, and the file and its anchor exist in an initialized checkout.

- **`F6lw`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive, like JFFG.
  ./mf-standards/architecture/api-response-design.md is in the uninitialized
  docs/mf-standards submodule and exists in an initialized checkout.

- **`Vows`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive, like JFFG.
  ./mf-standards/architecture/authorization-design.md is in the uninitialized
  docs/mf-standards submodule and exists in an initialized checkout.

- **`tlux`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive, like JFFG.
  ./docs/mf-standards/building-common.md#building-inside-a-task-worktree is in the
  uninitialized docs/mf-standards submodule, and the file and its anchor ("## Building inside
  a task worktree") exist in an initialized checkout.

- **`io3Q`** — **Dangling forward link; target does not exist…** — dismissed — 2026-10-24 — reason: Not actionable: a false positive, like JFFG.
  ./docs/mf-standards/architecture/http-error-precedence.md is in the uninitialized
  docs/mf-standards submodule and exists in an initialized checkout.

- **`3s85`** — **Verified (a). replacePassword supersedes the…** — dismissed — 2026-10-24 — reason: Duplicate of 1xmT. It is the code-level description (reset.go:154-196 replacePassword does
  not carry credential_party_authorizations to the new credential) of the decision 1xmT
  escalates. 1xmT carries the escalation, and its context cites this finding's detail.

- **`LUrz`** — **Verified (b). callbackLink never calls h.oau…** — dismissed — 2026-10-24 — reason: Duplicate of Nobq. It describes the same callbackLink gap
  (oidc_flows.go:381-434, no PolicyFor and no ConfinedToPartyID) and asks the same
  confine/refuse/allow decision. Nobq (security) and wZsk carry the escalation.

- **`2TCI`** — **The package and Authorize doc comments still…** — dismissed — 2026-10-24 — reason: Already planned: 8/001 (rewrite-authorization-engine). Its requirement 9 rewrites the
  package documentation of api/internal/authz/authz.go and removes all assume, sudo and
  "account holder" wording. Its validation greps for the stale terms.

- **`mEJv`** — **next-steps.md describes MERGE-V2 as implemen…** — dismissed — 2026-10-24 — reason: Already planned: 10/003 (review-repository-docs). Its requirement 1 lists "next-steps.md:
  remove items this plan completed; keep open ones", and its validation greps for /v1/self
  and X-Step-Up-Token.

- **`q3Ip`** — **loadProfileViews calls profile_availability…** — promoted — ref: `q3Ip` — 2026-10-24

- **`CTF3`** — **Project doc not reachable from README.md via…** — dismissed — 2026-10-24 — reason: Not actionable. api/server/CLAUDE.md holds only an empty `<claude-mem-context>` block: it is
  a tool-generated agent-memory stub, not a project document. A README link would add
  nothing for readers. It predates this plan (added in 17bc64e).

- **`yyDp`** — **session_liveness (hot path) performs about t…** — promoted — ref: `yyDp` — 2026-10-24

- **`o7LL`** — **ListPrincipals evaluates the accessible_*_id…** — promoted — ref: `o7LL` — 2026-10-24

- **`CLi0`** — **Deploy manifests: signing key wiring** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 008 (merge 0cfa2ab). deploy/k8s/base/deployment.yaml:33-64 now wires JWT_SIGNING_KEY (with optional JWT_SIGNING_KID / JWT_VERIFY_PUBLIC_KEYS) from a secret and marks JWT_SECRET optional: true; deploy/cloudrun/service.yaml:64-84 wires JWT_SIGNING_KEY and leaves JWT_SECRET as a commented optional entry. A leftover comment in deploy/k8s/base/configmap.yaml is tracked separately as QTuI.

- **`1xmT`** — **Decision needed: password reset does not copy** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 012 (merge 398050f), per the owner decision to copy authorizations. api/internal/service/credential_service.go:336-340 calls CopyCredentialPartyAuthorizations (model/queries/party_policies.sql:70) when a password is superseded. Reclassified from ambiguity to bug since the decision has been made.

- **`xgGT`** — **Manifest cannot register display renderers** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 006 (merge b9d20b3). moduleforge.module.yaml:128-150 builds userService through usersservice.NewUserServiceWithSelfService with a service:displayRegistry argument, and that constructor (api/usersservice/service.go:56-68) registers the user and profile display renderers, so mfgen-generated apps now register them.

- **`LPl2`** — **Compose omits mod-audit audit_log migration** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 005 (merge fc07a3a). model/Makefile:9 resolves AUDIT_DIR through go list -m github.com/moduleforge/audit-model with the sibling fallback, and the compose target (line 33) copies $(AUDIT_DIR)/migrations/*.sql into schema/migrations; the integration harness now applies the composed directory directly (harness_integration_test.go:350).

- **`aA5K`** — **RemoveIdentifierInTx (reached from SelfRemov…** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 011 (merge 2729fe4), per the owner decision for nullable FK with SET NULL. model/migrations/sql/0100_baseline.sql:538 (cred_password.identifier_id) and :563 (cred_email_otp.identifier_id) now use ON DELETE SET NULL, and removeIdentifier (api/internal/service/user_service.go:605-637) revokes bound credentials and then deletes, so verified identifiers can be removed. The integrity gap this change opened for active passwords is tracked separately as fDFi, and the stale error message as JWO3.

- **`86kP`** — **ListActivePasswordsByPrincipal filters cred_…** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 007 (merge 891392e). model/migrations/sql/0100_baseline.sql:553-554 adds cred_password_principal_id_idx ON mod_users.cred_password (principal_id) WHERE active.

- **`Pjuz`** — **auth_challenges has FK columns credential_id…** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 007 (merge 891392e). model/migrations/sql/0100_baseline.sql:987-994 adds partial indexes on auth_challenges principal_id, identifier_id, session_id and credential_id, and model/queries/challenges.sql:38-40 now uses COALESCE(col,0) = COALESCE(arg,0) predicates that match auth_challenges_cooldown_idx.

- **`W55I`** — **Project doc not reachable from README.md via…** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 009 (merge 7daac3e). README.md:71 links deploy/k8s/README.md.

- **`Kkmz`** — **Project doc not reachable from README.md via…** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 009 (merge 7daac3e). README.md:70 links deploy/local/README.md.

- **`fWsd`** — **Project doc not reachable from README.md via…** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 009 (merge 7daac3e). README.md:72 links deploy/serverless/README.md.

- **`xqMm`** — **Project doc not reachable from README.md via…** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 009 (merge 7daac3e). README.md:69 links model/README.md.

- **`nD8J`** — **The audience registration check (EXISTS SELE…** — dismissed — 2026-10-25 — reason: Already fixed by phase 12 task 014 (merge 640b50f), per the owner decision to exclude archived apps. Both audience liveness queries in api/internal/auth/authenticator.go now require AND ae.archived_at IS NULL (lines 246 and 269).

- **`JWO3`** — **ErrIdentifierInUse keeps the client-facing m…** — promoted — ref: `JWO3` — 2026-10-25

- **`3rM6`** — **profile_availability now joins session_facto…** — dismissed — 2026-10-25 — reason: Not actionable: the finding itself concludes "No change required". The per-factor primary-key join in profile_availability (model/migrations/sql/0100_baseline.sql:1237-1242) is the intended correctness fix from WpsB, and its cost is a small constant over a session's few factors. No change is warranted.

- **`QTuI`** — **The header comment still lists JWT_SECRET am…** — promoted — ref: `QTuI` — 2026-10-25

- **`z28C`** — **The public constructor usersservice.NewUserS…** — promoted — ref: `z28C` — 2026-10-25

- **`dvet`** — **The audience-token rejection compares the JW…** — promoted — ref: `dvet` — 2026-10-25

- **`Sr0s`** — **Four new partial single-column indexes on au…** — promoted — ref: `Sr0s` — 2026-10-25

- **`IZTR`** — **COALESCE-form predicates in GetLatestOpenCha…** — dismissed — 2026-10-25 — reason: Not actionable: the finding records an improvement and recommends "None". The COALESCE predicates at model/queries/challenges.sql:38-40 now match auth_challenges_cooldown_idx, as task 007 (merge 891392e) intended.

- **`nnGn`** — **Dangling docs/mf-standards links; orphan doc** — promoted — ref: `nnGn` — 2026-10-25

- **`TrRi`** — **platform_settings.allow_byo_identity is enfo…** — fixed — ref: `phase-12-remediation-01/001-enforce-byo-identity-on-guest-paths.md` — 2026-10-25

- **`wZsk`** — **Decision needed: link does not confine the ne** — fixed — ref: `phase-12-remediation-01/010-confine-party-bound-oidc-link.md` — 2026-10-25

- **`Nobq`** — **callbackLink adds the verified external iden…** — fixed — ref: `phase-12-remediation-01/010-confine-party-bound-oidc-link.md` — 2026-10-25

- **`VxMT`** — **OIDCResolver.Resolve returns ActionLogin for…** — fixed — ref: `phase-12-remediation-01/003-bind-oidc-login-to-provider.md` — 2026-10-25

- **`z443`** — **The OIDC sign-in callback creates a session…** — fixed — ref: `phase-12-remediation-01/013-bind-handoff-code-to-verifier.md` — 2026-10-25

- **`WpsB`** — **mod_users.profile_availability passes every…** — fixed — ref: `phase-12-remediation-01/002-exclude-revoked-factors-from-availability.md` — 2026-10-25

- **`UT68`** — **IssueAudienceToken signs offline-verifier to…** — fixed — ref: `phase-12-remediation-01/004-harden-audience-token-claims.md` — 2026-10-25

- **`fDFi`** — **mod_users.cred_password.identifier_id change…** — fixed — ref: `phase-13-remediation-02/001-guard-active-password-identifier-unbind.md` — 2026-10-25

- **`FAK8`** — **Request mod-core profile row-lock query** — promoted — ref: `FAK8` — 2026-10-26

- **`0b75`** — **mod-core: in-tx org create** — promoted — ref: `0b75` — 2026-10-26

- **`Jwo8`** — **Stale architecture service table** — promoted — ref: `Jwo8` — 2026-10-26

- **`k0Jt`** — **departureCleanupInTx loads every credential…** — promoted — ref: `k0Jt` — 2026-10-26

- **`DOZW`** — **ListCredentialAuthorizations returns every a…** — promoted — ref: `DOZW` — 2026-10-26

- **`HzgJ`** — **Not reachable from README.md by any link cha…** — dismissed — 2026-10-26 — reason: Not actionable: api/server/CLAUDE.md contains only an empty <claude-mem-context> block. It is a
  tool-generated agent-memory stub, not a project document, so a README link would add nothing.
  It predates this phase (the finding says "Pre-existing, not touched this phase"). The same
  file was already dismissed on these grounds as CTF3 in this plan's closed findings.

- **`Zmvl`** — **Link ./docs/mf-standards/building-applicatio…** — dismissed — 2026-10-26 — reason: Not actionable: a false positive from the environment. docs/mf-standards is a git submodule
  (.gitmodules, url git@github.com:moduleforge/docs-mf-standards.git) left uninitialized in this
  plan worktree. In the initialized main checkout,
  /Users/zane/playground/moduleforge/mod-users/docs/mf-standards/building-applications.md
  exists, so README.md:33's link resolves. No document change fixes it. This is the same
  situation as the closed JFFG/bYyY, and the broader item is already in the durable backlog as
  nnGn.

- **`d6Rm`** — **Links to docs/mf-standards/building-common.m…** — dismissed — 2026-10-26 — reason: Not actionable: false positive from the uninitialized docs/mf-standards submodule.
  docs/mf-standards/building-common.md and docs/mf-standards/architecture/http-error-precedence.md
  both exist in the initialized checkout /Users/zane/playground/moduleforge/mod-users/docs/mf-standards/.
  Same as the closed tlux and io3Q. Pre-existing, and the broader item is already promoted as nnGn.

- **`XicG`** — **Links into docs/mf-standards (architecture.m…** — dismissed — 2026-10-26 — reason: Not actionable: false positive from the uninitialized docs/mf-standards submodule.
  architecture.md, building-applications.md, architecture/api-response-design.md and
  architecture/authorization-design.md all exist in the initialized checkout
  /Users/zane/playground/moduleforge/mod-users/docs/mf-standards/. Same as the closed O1Ym, bYyY,
  F6lw and Vows. Pre-existing.

- **`CGcM`** — **Link ../docs/mf-standards/architecture/db-co…** — dismissed — 2026-10-26 — reason: Not actionable: false positive from the uninitialized docs/mf-standards submodule.
  architecture/db-considerations.md exists in the initialized checkout
  /Users/zane/playground/moduleforge/mod-users/docs/mf-standards/architecture/db-considerations.md.
  Same as the closed RkWd. Pre-existing.

- **`B4U1`** — **activePartyProfile calls ListProfilesByHolde…** — promoted — ref: `B4U1` — 2026-10-26

- **`7OY7`** — **On the confirmed path CheckOutsideCollaborat…** — promoted — ref: `7OY7` — 2026-10-26

- **`FGLX`** — **partyProfilesCountSQL reuses the full FROM c…** — promoted — ref: `FGLX` — 2026-10-26

- **`ThVQ`** — **Sections: API layer; Authentication flow; Ke…** — dismissed — 2026-10-26 — reason: Reclassified from conflict to documentation (architecture doc lags new integration points;
  no contradiction to decide). Already planned: phase 10 task 001 (update-architecture-docs).
  It passes every plan/phase-07-parties-profiles-and-bootstrap task document (including 003
  collaborator gate and 007 AdmitInTx) as completed_task_docs to update docs/architecture.md.
  Its requirements 3 and 4 explicitly cover SSHKeyResolver AuthContextForOwner (ssh-keys.md)
  and the setup-token bootstrap lifecycle (sessions-and-tokens.md).

- **`255B`** — **For a caller holding only AuthorizeTypeIn(cr…** — promoted — ref: `255B` — 2026-10-26

- **`E3ly`** — **Through the holder door, UnconfineCredential…** — promoted — ref: `E3ly` — 2026-10-26

- **`t2OR`** — **An invitation with confirm_policy_mismatch w…** — promoted — ref: `t2OR` — 2026-10-26

- **`me7s`** — **Location: /v1/parties and /v1/profiles entri…** — promoted — ref: `me7s` — 2026-10-26

- **`RU9G`** — **CheckOutsideCollaborator compares the owning…** — dismissed — ref: `plan/phase-08-users-authorization-engine/001-rewrite-authorization-engine.md` — 2026-10-26 — reason: Resolved by user decision (option 1): the owning party's authn policy (credential_rule, min_acr) is enforced per request in mod-users' engine for actors whose profile is outside that party, implemented by phase 8 task 001 (new requirement 13; amends resolution (a)/U5). mod-authz unchanged.

- **`qvpE`** — **OpenAPI step-up order note is stale** — dismissed — 2026-10-26 — reason: Already fixed by phase 14 task 006 (commit 5ade1e4). api/openapi.yaml:33-37 now says that
  on principal disable and enable, step-up comes after authorization ("`read` (404
  otherwise) then `update` (403), and only then is step-up freshness checked"). That matches
  UserService.writablePrincipal in api/internal/service/principal_admin.go, which calls
  Authorize update and then requireFresh.

- **`oO0S`** — **gateCredentialWrite checks that the credenti…** — dismissed — 2026-10-26 — reason: Already fixed by phase 14 task 004 (commit dba63b9). gateCredentialWrite now reads the
  holder's profile with SELECT ... FOR SHARE (api/internal/service/party_policy.go:571-579)
  instead of the bare EXISTS. The concurrency tests are in
  api/internal/integration/profile_writers_integration_test.go. The lock-order inversion
  this fix introduced is tracked as 9Mvf/PL6d/zwbM.

- **`2mUw`** — **ProvisionManagedUserInTx creates the managed…** — dismissed — 2026-10-26 — reason: Already fixed by phase 14 task 001 (commit d8eef20). Under credential_rule 'authorized',
  ProvisionManagedUserInTx (api/internal/service/profile_service.go:938-958) inserts the
  credential_party_authorizations row for the new password and emits `create`
  `credential_party_authorization`. The "an authorized party's managed user can sign in"
  subtest covers it. The authority question this fix raises is tracked as 1cWY.

- **`4w6W`** — **The function comment says holder_acr is NULL…** — dismissed — 2026-10-26 — reason: Already fixed by phase 14 task 005 (commit 7b08d96). holder_policy_mismatch in
  model/migrations/sql/0100_baseline.sql (around lines 1303-1306) computes holder_acr with
  CASE WHEN count(*) = 0 THEN NULL ELSE ... END. policy_mismatch.go:79-80 renders the
  HolderACR 0 case as "the account has no usable credential".

- **`5iFg`** — **AdmitInTx serializes per holder by locking t…** — dismissed — 2026-10-26 — reason: Already fixed by phase 14 task 004 (commit dba63b9). resolveInvitee takes
  GetUserByEntityIDForUpdate on the holder (api/internal/service/profile_service.go:635)
  before the duplicate check and insert, the same lock AdmitInTx takes. AdmitInTx's doc
  comment (profile_admit.go) states the shared-lock guarantee. A concurrency test is in
  profile_writers_integration_test.go.

- **`iaJb`** — **profile_availability_excluding filters facto…** — dismissed — 2026-10-26 — reason: Already fixed by phase 14 task 005 (commit 7b08d96). profile_availability_excluding in
  model/migrations/sql/0100_baseline.sql:1401 filters with (p_exclude_credential_id IS NULL
  OR c.id <> p_exclude_credential_id), which matches session_acr_excluding (:1116).

- **`kimI`** — **Location: /v1/bootstrap entry (431-438); als…** — dismissed — 2026-10-26 — reason: Already fixed by phase 14 task 006 (commit 5ade1e4). The API-layer exceptions in
  docs/architecture.md:101 and D20(3) at docs/architecture.md:260 both list POST
  /v1/bootstrap as a public route outside requireOIDCConfirmed that uses the X-Setup-Token
  header, with the reason.

- **`Fjaa`** — **Sibling symlinks mod-core, mod-audit and mod-** — dismissed — 2026-10-26 — reason: Not actionable (environmental). scripts/link-siblings.sh repoints the sibling symlinks
  to the main checkouts by design, unless MODULEFORGE_SIBLINGS_DIR is set (see its header).
  That is harness and workspace layout, not a defect in mod-users code. At evaluation time,
  the mod-core, mod-audit and mod-authz links under both worktrees/plan/ and worktrees/ point
  to /Users/zane/playground/.flow/siblings/mod-users/<sibling> (Flow-managed, 20:08). No
  change to the plan's code would resolve it. The manager's verification request is
  repeated in flagged_for_manager.

- **`D0sv`** — **profile_availability_for's authorized-rule a…** — dismissed — 2026-10-26 — reason: Not actionable. The concern is moot under the plan's own hard constraint (plan/overview.md
  "Hard constraints": "No production release: rewrite 0100_baseline.sql in place, fold 0101
  to 0104 into it; no incremental migrations, no backward compatibility, no data
  migration"). model/migrations/sql/ holds only 0100_baseline.sql, so a CREATE OR REPLACE
  migration would contradict that constraint. The stale migration-history sentence in
  docs/architecture.md:57 ("Migrations 0101 through 0104 have since been added") sits in the
  data-model section that
  phase-10-doc-updates/001-update-architecture-docs.md rewrites (its requirement 3: "docs/architecture.md
  — data model ... baseline schema rewrite into mod_users").

- **`VIcU`** — **Orphan doc: api/server/CLAUDE.md is tracked…** — promoted — ref: `VIcU` — 2026-10-26

- **`rAhy`** — **Dangling forward-link: ./docs/mf-standards/b…** — dismissed — 2026-10-26 — reason: Not actionable (environmental). docs/mf-standards is a git submodule (.gitmodules) that
  is uninitialized in the plan worktree. In the main checkout's initialized submodule
  (/Users/zane/playground/moduleforge/mod-users/docs/mf-standards),
  building-applications.md exists, so the link in README.md:33 is valid. No change to this
  repo applies.

- **`Pg5e`** — **Dangling forward-links into the uninitialize…** — dismissed — 2026-10-26 — reason: Not actionable (environmental, same as rAhy). The targets architecture.md,
  architecture/entity-typing.md and architecture/db-considerations.md all exist in the main
  checkout's initialized docs/mf-standards submodule. The links in docs/architecture.md
  resolve once the submodule is initialized.

- **`ZfaZ`** — **Dangling forward-links into the uninitialize…** — dismissed — 2026-10-26 — reason: Not actionable (environmental, same as rAhy). architecture.md, building-applications.md,
  architecture/api-response-design.md and architecture/authorization-design.md all exist in
  the main checkout's initialized docs/mf-standards submodule.

- **`lUew`** — **Dangling forward-link into the uninitialized…** — dismissed — 2026-10-26 — reason: Not actionable (environmental, same as rAhy). building-common.md exists in the main
  checkout's initialized submodule, and its heading "## Building inside a task worktree"
  (line 21) supplies the #building-inside-a-task-worktree anchor that AGENTS.md:166
  targets.

- **`mBIH`** — **Dangling forward-link into the uninitialized…** — dismissed — 2026-10-26 — reason: Not actionable (environmental, same as rAhy). architecture/db-considerations.md exists in
  the main checkout's initialized docs/mf-standards submodule, so the link in
  model/README.md:6 is valid.

- **`8qpY`** — **gateCredentialWrite now always calls holderS…** — dismissed — 2026-10-26 — reason: Not actionable. The finding itself concludes that the extra holderSatisfiesGate queries
  are purposeful (the admin/holder flags drive the confine/unconfine restriction of
  decision A) and are "Not worth the complexity on this cold path". It recommends no
  change.

- **`jpDE`** — **resolveInvitee now locks the invitee's mod_u…** — promoted — ref: `jpDE` — 2026-10-26

- **`4ZP1`** — **Invite by email matches unverified addresses** — fixed — ref: `phase-14-remediation-03/001-require-verified-invitee-and-authorize-managed-password.md` — 2026-10-26

- **`T1E6`** — **resolveInvitee resolves holder_email through…** — fixed — ref: `phase-14-remediation-03/001-require-verified-invitee-and-authorize-managed-password.md` — 2026-10-26

- **`Pxlw`** — **gateCredentialWrite lets a party administrat…** — fixed — ref: `phase-14-remediation-03/007-restrict-admin-confinement-to-party-controlled-credentials.md` — 2026-10-26

- **`xhLc`** — **Under credential_rule 'authorized', holder_p…** — fixed — ref: `phase-14-remediation-03/008-authorized-rule-invite-time-semantics.md` — 2026-10-26

- **`aNYW`** — **holderProfile authorizes POST /v1/me/profile…** — fixed — ref: `phase-14-remediation-03/002-gate-holder-leave-on-profile-availability.md` — 2026-10-26

- **`cvMO`** — **tokenActiveLocked keeps the setup token acti…** — fixed — ref: `phase-14-remediation-03/003-scope-setup-token-on-oidc-routes.md` — 2026-10-26

- **`h0m8`** — **dropPolicyConfirmations runs DELETE FROM pro…** — promoted — ref: `h0m8` — 2026-10-26

- **`Nj1F`** — **Lock ordering between the credential write g…** — dismissed — 2026-10-26 — reason: Already planned: phase-15-remediation-04/001-order-credential-gate-locks-profile-first. Its title
  states the work this finding asks for (the credential write gate takes the holder profile lock
  before the credential lock, matching EndInTx). It covers 9Mvf/PL6d/zwbM, which describe the same
  credential-then-profile versus profile-then-credential cycle in gateCredentialWrite and
  departureCleanupInTx, with the same 40P01-as-500 outcome. Stamped conflict; reclassified as a bug.

- **`DuW4`** — **Lock order inversion (task 004). gateCredent…** — dismissed — 2026-10-26 — reason: Already planned: phase-15-remediation-04/001-order-credential-gate-locks-profile-first, which
  reorders gateCredentialWrite to lock the holder profile before the credential. That is the same
  lock-order inversion at party_policy.go:540-580 that PL6d (one of the findings the task covers)
  describes, including the interleaved confine-versus-leave test.

- **`WVBe`** — **LeaveProfile gate bypass for suspended profi…** — dismissed — 2026-10-26 — reason: Already planned: phase-15-remediation-04/002-gate-suspended-profile-leave, which evaluates the
  party gate when a holder leaves a suspended profile. This is the same gap (profile_availability_for
  returns 'inactive' before the gate, so LeaveProfile skips it for suspended profiles) recorded as
  twos and nA1G, which that task covers.

- **`w1dQ`** — **verifySetupToken now calls RefreshState (Get…** — dismissed — 2026-10-26 — reason: Already planned: phase-15-remediation-04/004-verify-setup-token-without-cache-writes. Reading the
  token hash and confirmed state locally without writing the shared cache resolves both points
  here (per-guess query cost, and concurrent refreshes overwriting the cache with an older
  snapshot), as in 77AP, which the task covers.

- **`gD2Y`** — **gateCredentialWrite now always calls holderS…** — promoted — ref: `gD2Y` — 2026-10-26

- **`59q6`** — **verifySetupToken now does a DB RefreshState…** — dismissed — 2026-10-26 — reason: Already planned: phase-15-remediation-04/004-verify-setup-token-without-cache-writes. It replaces
  the per-attempt RefreshState with a local fresh read, which removes the cost this note describes.
  It covers HOWM, the equivalent efficiency note. The finding itself also recommends no action.

- **`dWRn`** — **Dangling links into docs/mf-standards/ (an e…** — dismissed — 2026-10-26 — reason: Not actionable: the links are not dangling in the repository. docs/mf-standards is a git
  submodule (.gitmodules: path docs/mf-standards, gitlink c2dc98c). In the initialized main checkout
  (/Users/zane/playground/moduleforge/mod-users/docs/mf-standards) every named target exists:
  building-applications.md, building-common.md, architecture.md, and architecture/http-error-precedence.md,
  db-considerations.md, entity-typing.md, api-response-design.md and authorization-design.md. Only
  the plan worktree's copy is an uninitialized, empty submodule directory. This environment state
  existed before this phase and was not caused by it, and no change to repository content would
  resolve it.

- **`76Ma`** — **api/server/CLAUDE.md is not reachable from R…** — promoted — ref: `76Ma` — 2026-10-26

- **`R4R0`** — **holder_policy_mismatch is shared with Collab…** — dismissed — ref: `ggb7` — 2026-10-26 — reason: Duplicate of ggb7 (same defect at holder_policy_mismatch/CollaboratorGate and README.md:84). Manager-approved by the user via interactive decision: ggb7 fixed with a remediation task (strict collaborator path).

- **`X56M`** — **Lock-order inversion (availability, not an a…** — dismissed — ref: `9Mvf` — 2026-10-26 — reason: Duplicate of 9Mvf, fixed by phase-15-remediation-04/001-order-credential-gate-locks-profile-first (profile lock before credential lock removes the deadlock). User-approved dismissal via interactive decision.

- **`LlcR`** — **Confirmations are dropped only when credenti…** — dismissed — ref: `iFeg` — 2026-10-26 — reason: Duplicate of iFeg, fixed by phase-15-remediation-04/005-purge-confirmations-on-provider-change (purge confirmations when required OIDC provider changes). User-approved dismissal via interactive decision.

- **`nOsE`** — **PutAuthnPolicy reads \`before\` without a lock…** — dismissed — ref: `iFeg` — 2026-10-26 — reason: Duplicate of iFeg, fixed by phase-15-remediation-04/005-purge-confirmations-on-provider-change (serialize with policy writes; same FOR UPDATE/FOR SHARE remedy). User-approved dismissal via interactive decision.

- **`vPQI`** — **verifySetupToken now calls RefreshState (Get…** — dismissed — ref: `77AP` — 2026-10-26 — reason: Duplicate of 77AP/HOWM, fixed by phase-15-remediation-04/004-verify-setup-token-without-cache-writes (local fresh read, no shared cache rewrite). User-approved dismissal via interactive decision.

- **`9Mvf`** — **gateCredentialWrite locks the credential fir…** — fixed — ref: `phase-15-remediation-04/001-order-credential-gate-locks-profile-first.md` — 2026-10-26

- **`PL6d`** — **gateCredentialWrite locks the credential row…** — fixed — ref: `phase-15-remediation-04/001-order-credential-gate-locks-profile-first.md` — 2026-10-26

- **`zwbM`** — **Lock-ordering inversion between the credenti…** — fixed — ref: `phase-15-remediation-04/001-order-credential-gate-locks-profile-first.md` — 2026-10-26

- **`twos`** — **Party gate not evaluated for suspended leave** — fixed — ref: `phase-15-remediation-04/002-gate-suspended-profile-leave.md` — 2026-10-26

- **`nA1G`** — **The leave guard accepts avail == 'inactive'…** — fixed — ref: `phase-15-remediation-04/002-gate-suspended-profile-leave.md` — 2026-10-26

- **`Kfjo`** — **requireNotLastOperatorAdmin is called only o…** — fixed — ref: `phase-15-remediation-04/003-extend-last-operator-admin-guard.md` — 2026-10-26

- **`kPpy`** — **The exported EndInTx (in-process primitive f…** — fixed — ref: `phase-15-remediation-04/003-extend-last-operator-admin-guard.md` — 2026-10-26

- **`Ts9r`** — **requireNotLastOperatorAdmin runs one admin-c…** — fixed — ref: `phase-15-remediation-04/003-extend-last-operator-admin-guard.md` — 2026-10-26

- **`Xqat`** — **The last-operator-admin guard reads mod-auth…** — fixed — ref: `phase-15-remediation-04/003-extend-last-operator-admin-guard.md` — 2026-10-26

- **`77AP`** — **verifySetupToken now calls RefreshState for…** — fixed — ref: `phase-15-remediation-04/004-verify-setup-token-without-cache-writes.md` — 2026-10-26

- **`HOWM`** — **verifySetupToken now calls RefreshState (a D…** — fixed — ref: `phase-15-remediation-04/004-verify-setup-token-without-cache-writes.md` — 2026-10-26

- **`ICke`** — **The setup-token lifecycle changed: the OIDC…** — fixed — ref: `phase-15-remediation-04/004-verify-setup-token-without-cache-writes.md` — 2026-10-26

- **`iFeg`** — **PutAuthnPolicy drops a party's profile_polic…** — fixed — ref: `phase-15-remediation-04/005-purge-confirmations-on-provider-change.md` — 2026-10-26

- **`1cWY`** — **Under an \`authorized\` party, ProvisionManage…** — fixed — ref: `phase-15-remediation-04/006-require-party-update-for-managed-password-authorization.md` — 2026-10-26

- **`DO7p`** — **The last-operator-admin guard covers only th…** — fixed — ref: `phase-15-remediation-04/007-exclude-disabled-principals-from-admin-count.md` — 2026-10-26

- **`vHBJ`** — **ProvisionManagedUserInTx reads the party's a…** — fixed — ref: `phase-15-remediation-04/008-serialize-managed-user-provisioning-with-policy.md` — 2026-10-26

- **`TA9q`** — **Sibling links under worktrees/plan/ (mod-core** — dismissed — 2026-10-26 — reason: Not actionable: an unverified-observation note, not a defect. The sibling links under
  worktrees/plan/ (mod-audit, mod-authz, mod-core) resolve to /Users/zane/playground/.flow/siblings/
  mod-users/<sibling> (ls -la, 2026-10-26), and the caller independently verified them; there is
  nothing to change.

- **`nLkT`** — **Manager action: no users-module-postgres cont** — dismissed — 2026-10-26 — reason: Not actionable as a code or doc change: it is a manager-facing environment note (the integration
  suite needs a throwaway postgres:16 with a users superuser via AUTHZ_DEV_PG_HOST/PORT). Its
  substance is carried into the PlQx validation task's requirements, which is where it acts.

- **`62Rc`** — **Task 007 builds on this: operatorAdminProfile** — dismissed — 2026-10-26 — reason: Already fixed: task 007 put the disabled-principal exclusion in operatorAdminProfileSQL —
  api/internal/service/profile_last_admin.go:33 joins public.principals with
  pr.disabled_at IS NULL (doc comment at line 58).

- **`sWRq`** — **Finding line numbers (566-590, 388-400) cited** — dismissed — 2026-10-26 — reason: Stale: it reports that line numbers cited in earlier (now-resolved) findings drifted against the
  current file; those findings were handled by phase-15 tasks and line citations in closed findings
  carry no remaining action.

- **`oufL`** — **Out of scope, existing behavior: whether PutA** — promoted — ref: `oufL` — 2026-10-26

- **`oJhu`** — **The platform_settings FOR UPDATE is now take…** — promoted — ref: `oJhu` — 2026-10-26

- **`ZrCC`** — **Unreachable CLAUDE.md, dangling mf-standards** — promoted — ref: `ZrCC` — 2026-10-26

- **`CZcA`** — **Lock contention in new guard/policy locks** — promoted — ref: `CZcA` — 2026-10-26

- **`YpJ6`** — **verifySetupToken is reachable unauthenticate…** — promoted — ref: `YpJ6` — 2026-10-26

- **`ggb7`** — **mod_users.holder_policy_mismatch now treats…** — fixed — ref: `phase-16-remediation-05/001-strict-collaborator-policy-mismatch.md` — 2026-10-26

- **`6dyh`** — **The guard now counts only admins whose princ…** — fixed — ref: `phase-16-remediation-05/002-close-admin-disable-race-and-fail-closed-policy-lock.md` — 2026-10-26

- **`8bH2`** — **lockPartyPolicyShared silently returns nil w…** — fixed — ref: `phase-16-remediation-05/002-close-admin-disable-race-and-fail-closed-policy-lock.md` — 2026-10-26

- **`PlQx`** — **Integration subtest in TestBootstrap_TokenLif** — fixed — ref: `phase-16-remediation-05/004-run-bootstrap-token-lifecycle-integration.md` — 2026-10-26

- **`hyek`** — **Behavioral consequence: a handler whose cache** — fixed — ref: `phase-16-remediation-05/004-run-bootstrap-token-lifecycle-integration.md` — 2026-10-26

- **`A202`** — **Lock-order contract undocumented** — fixed — ref: `phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md` — 2026-10-26

- **`J6jP`** — **The diff introduces several architectural de…** — fixed — ref: `phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md` — 2026-10-26

- **`u2bW`** — **requireNotLastOperatorAdmin takes the platfo…** — fixed — ref: `phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md` — 2026-10-26

- **`jBbn`** — **Decide whether to queue a docs task for the g** — fixed — ref: `phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md` — 2026-10-26

- **`EvkR`** — **The collaborator_gate.go row states that \`Pu…** — fixed — ref: `phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md` — 2026-10-26

- **`xWC3`** — **The PUT authn-policy description says changi…** — fixed — ref: `phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md` — 2026-10-26

- **`yPFO`** — **The PATCH profile status description says 40…** — fixed — ref: `phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md` — 2026-10-26

- **`8mcq`** — **POST managed-users is documented as authoriz…** — fixed — ref: `phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md` — 2026-10-26

- **`VXFQ`** — **After task 008 MismatchCredentialNotAuthoriz…** — fixed — ref: `phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md` — 2026-10-26

- **`tmcn`** — **Run TestLeaveGate_ConcurrentDisableAndSuspend** — dismissed — 2026-10-26 — reason: Reclassified from the default ambiguity stamp: it is an unverified-test
  deficiency (bug), not an open question. Already fixed: the test has now been
  observed to execute and pass against a real postgres:16. Evidence under
  project_root: .flow/validation-logs/leavegate-x5.log lines 1-12 (five
  "=== RUN / --- PASS: TestLeaveGate_ConcurrentDisableAndSuspendLeaveAnEffectiveAdministrator",
  "ok github.com/moduleforge/mod-users/api/internal/integration"),
  .flow/validation-logs/leavegate-race.log lines 1-4 (the -race run, PASS), and
  .flow/validation-logs/integration-full.log line 852 (the full integration
  package "ok", no SKIP or FAIL lines).

- **`cKK9`** — **Optional: install pyyaml or spectral so make** — dismissed — 2026-10-26 — reason: Reclassified from the default ambiguity stamp: it is an optional tooling
  suggestion, not an open question. Not actionable: installing pyyaml or
  spectral on one agent's machine is a local environment step. No change to
  this repository resolves it, and the Makefile target make openapi.validate
  already exists. Task 003's status records that the OpenAPI YAML parses and
  that the validator could not run for lack of tooling. The sDO1 spec below
  asks for make openapi.validate where the tooling is available.

- **`WA7h`** — **The concurrency test only requires that not…** — promoted — ref: `WA7h` — 2026-10-26

- **`Faly`** — **Project doc api/server/CLAUDE.md is not reac…** — promoted — ref: `Faly` — 2026-10-26

- **`jk9n`** — **Dangling forward link: ./docs/mf-standards/b…** — dismissed — 2026-10-26 — reason: Not actionable: no change to this repository resolves it, because the link
  is not broken at the pinned revision. docs/mf-standards is a git
  submodule (.gitmodules) that is merely uninitialized in this worktree.
  `git submodule status` in project_root shows
  "-c2dc98c9a070abe5449b21bc7c67ae6b51b53086 docs/mf-standards". At that
  exact commit (the initialized submodule in the main mod-users checkout,
  HEAD c2dc98c9) building-applications.md exists and has
  "## First-time setup" at line 175. The only remedy is the environment
  step `git submodule update --init` in the worktree.

- **`OSoT`** — **Dangling forward links into the uninitialize…** — dismissed — 2026-10-26 — reason: Not actionable: the targets exist at the pinned submodule revision, so no
  repository change applies. The gitlink pins docs/mf-standards at
  c2dc98c9a070abe5449b21bc7c67ae6b51b53086 (git submodule status in
  project_root). At that commit building-common.md exists, with
  "## Building inside a task worktree" at line 21, and
  architecture/http-error-precedence.md exists. The submodule is only
  uninitialized in this worktree, which is an environment step, not a
  defect.

- **`nrI1`** — **Dangling forward links into the uninitialize…** — dismissed — 2026-10-26 — reason: Not actionable: the targets exist at the pinned submodule revision, so no
  repository change applies. At the gitlink commit
  c2dc98c9a070abe5449b21bc7c67ae6b51b53086, architecture.md,
  architecture/entity-typing.md and architecture/db-considerations.md all
  exist. The submodule is only uninitialized in this worktree, which is an
  environment step, not a defect.

- **`fNoH`** — **Dangling forward links into the uninitialize…** — dismissed — 2026-10-26 — reason: Not actionable: the targets exist at the pinned submodule revision, so no
  repository change applies. At the gitlink commit
  c2dc98c9a070abe5449b21bc7c67ae6b51b53086, architecture.md,
  building-applications.md (with "## First-time setup" at line 175),
  architecture/api-response-design.md and
  architecture/authorization-design.md all exist. The submodule is only
  uninitialized in this worktree, which is an environment step, not a
  defect.

- **`FPKQ`** — **Dangling forward link: ../docs/mf-standards/…** — dismissed — 2026-10-26 — reason: Not actionable: the target exists at the pinned submodule revision, so no
  repository change applies. At the gitlink commit
  c2dc98c9a070abe5449b21bc7c67ae6b51b53086,
  architecture/db-considerations.md exists. The submodule is only
  uninitialized in this worktree, which is an environment step, not a
  defect.

- **`sDO1`** — **ErrLastOperatorAdmin is now also returned by…** — fixed — ref: `phase-17-remediation-06/001-last-operator-admin-disable-contract.md` — 2026-10-26

- **`Eqyx`** — **The test seam is swapped by a package-level…** — fixed — ref: `phase-17-remediation-06/002-scope-policy-lock-test-seam-and-assert-invite-time.md` — 2026-10-26

- **`inY7`** — **The fake HolderPolicyMismatch ignores its pa…** — fixed — ref: `phase-17-remediation-06/002-scope-policy-lock-test-seam-and-assert-invite-time.md` — 2026-10-26

- **`qvSR`** — **Core Authorizer contract omits party gate** — promoted — ref: `qvSR` — 2026-10-27

- **`IIA3`** — **Party gate adds a query per check** — dismissed — 2026-10-27 — reason: Duplicate of MaQM: both describe the same extra round trip that
  checkPartyPolicy adds before the wildcard and grant-or-own queries in
  api/internal/authz/authz.go authorizePrelude, with the same remedy (fold the
  gate and the wildcard arm into one statement). MaQM is promoted and carries
  the issue.

- **`Lt3W`** — **Two ErrStepUpRequired sentinels** — promoted — ref: `Lt3W` — 2026-10-27

- **`JgvC`** — **\[decision\] Are operator profiles subject to t** — dismissed — 2026-10-27 — reason: Stale: the open question was settled by Decision D (plan/notes/
  decisions-and-resolutions.md, Amendments 2026-10-26 phase 8): operator
  profiles are exempt from the owning-party gate via a core_is_operator_profile
  skip in session_meets_party_policy, and the skipOperatorUnderOwningPartyGate
  helper is removed (grep -rn skipOperatorUnderOwningPartyGate api/ returns
  nothing). Superseded by Decision D per the user.

- **`pKAO`** — **\[docs\] Phase 10 task 001 source list names th** — dismissed — 2026-10-27 — reason: already planned: phase-10-doc-updates/001-update-architecture-docs.md is the
  phase 10 architecture-doc rewrite, which is being amended to cover the
  owning-party gate, the exact gap this item flags in its source list.

- **`Q1lB`** — **Spec still documents /assume** — dismissed — 2026-10-27 — reason: already planned: phase-10-doc-updates/001-update-architecture-docs.md is the
  phase 10 rewrite of the architecture docs, being amended to cover the /assume
  removal, which is exactly the stale /assume wording in docs/mod-users-spec.md
  and docs/architecture.md this item reports.

- **`Hd71`** — **Spec section 10 and related lines still desc…** — dismissed — 2026-10-27 — reason: already planned: phase-10-doc-updates/001-update-architecture-docs.md is
  being amended to cover the /assume removal, which covers rewriting spec
  section 10 and the architecture.md lines this item cites (it is the same
  issue as Q1lB, which it names).

- **`EfE8`** — **Decision D9 in docs/architecture.md, and the…** — dismissed — 2026-10-27 — reason: Reclassified from bug to documentation (doc drift). already planned:
  phase-10-doc-updates/001-update-architecture-docs.md is being amended to
  cover D9 and docs/architecture/ssh-keys.md, which is exactly the stale D9
  text in docs/architecture.md and ssh-keys.md this item reports.

- **`MaQM`** — **Every gated Authorize with a target issues u…** — promoted — ref: `MaQM` — 2026-10-27

- **`6tQK`** — **api/server/CLAUDE.md is a tracked project do…** — dismissed — 2026-10-27 — reason: Duplicate of the durable project followup DPZi (plan/followups.yaml line
  1004 in the main checkout), which already tracks the orphaned
  api/server/CLAUDE.md; the item itself says so, and the orphan predates this
  plan. Promoting it would create a second backlog entry for the same issue.

- **`SjEH`** — **For invite-only apps every request runs a fu…** — promoted — ref: `SjEH` — 2026-10-27

- **`Ddx9`** — **List path skips owning-party gate** — promoted — ref: `Ddx9` — 2026-10-27

- **`J55x`** — **ImpersonationService.Start only observes a s…** — promoted — ref: `J55x` — 2026-10-27

- **`l8jM`** — **NEEDS USER DECISION (halt): choose option A (** — dismissed — ref: `plan/notes/decisions-and-resolutions.md` — 2026-10-29 — reason: Resolved by user decision G (option A) recorded in plan/notes/decisions-and-resolutions.md.

- **`u7Fd`** — **Confirm decision F owner-gate reading** — dismissed — ref: `plan/notes/decisions-and-resolutions.md` — 2026-10-29 — reason: Resolved by user decision F (both gates apply, confirmed) in plan/notes/decisions-and-resolutions.md.

- **`4MjU`** — **API surface 6.2 amendment for decision F** — dismissed — ref: `plan/notes/mod-users-api-surface.md` — 2026-10-29 — reason: Done by manager amendment of API surface note (2.9, 6.1, 6.2), committed.

- **`FZVc`** — **Sibling links repointed by make runs** — promoted — ref: `FZVc` — 2026-10-29

- **`bYK4`** — **checkPartyPolicy resolves the owning party o…** — fixed — ref: `phase-18-remediation-07/001-party-is-own-owning-party.md` — 2026-10-29

- **`N4y2`** — **The owning party is resolved only as the tar…** — fixed — ref: `phase-18-remediation-07/001-party-is-own-owning-party.md` — 2026-10-29

- **`iacw`** — **The owning-party gate resolves the owner onl…** — fixed — ref: `phase-18-remediation-07/001-party-is-own-owning-party.md` — 2026-10-29

- **`Y3B8`** — **mod_users.session_meets_party_policy passes…** — fixed — ref: `phase-18-remediation-07/002-impersonated-actor-rules-apply.md` — 2026-10-29

- **`ejk3`** — **credentialsTarget and the IssuePrincipalAPIK…** — fixed — ref: `phase-18-remediation-07/003-credentials-refuse-impersonation.md` — 2026-10-29

- **`Mdgf`** — **EnsurePersonalParty uses adminCaller, which…** — fixed — ref: `phase-18-remediation-07/003-credentials-refuse-impersonation.md` — 2026-10-29

- **`Vj21`** — **Because users are self-owned and credentials…** — fixed — ref: `phase-18-remediation-07/003-credentials-refuse-impersonation.md` — 2026-10-29

- **`0chQ`** — **Renew skips impersonate re-check** — fixed — ref: `phase-18-remediation-07/004-impersonation-renew-recheck.md` — 2026-10-29

- **`LAgn`** — **Renew extends the child session to now plus…** — fixed — ref: `phase-18-remediation-07/004-impersonation-renew-recheck.md` — 2026-10-29

- **`QKwM`** — **checkAppGate maps only ErrForbidden and ErrU…** — fixed — ref: `phase-18-remediation-07/006-app-gate-step-up-mapping.md` — 2026-10-29

- **`7KQz`** — **Option A ordering note: if A is chosen, consi** — dismissed — 2026-10-29 — reason: Stale. This was an ordering note that applied only if Option A were chosen. The user chose
  Option A (decisions-and-resolutions.md, Decision G resolution 2026-10-29). Phase 18 tasks
  002 and 004 have since both completed (their Status sections say "succeeded 2026-10-29"),
  so task order can no longer change. The substance of the note, keying the operator
  exemption on the recorded acting profile, is now settled by Decision J. It is fixed by
  remediation spec impersonator-profile-credential-exemption (DChD, crQE, bKBt).

- **`HAsS`** — **Conflict evidence: model/migrations/sql/0100_** — dismissed — 2026-10-29 — reason: Stale. This item recorded evidence for the Decision G versus Decision D conflict. The user
  resolved that conflict on 2026-10-29 (Decision G resolution, option A):
  operator impersonators are exempt from credential_rule, and
  TestImpersonation_OperatorReachesMembersOfRestrictiveParties stays valid.
  Task 002 implemented that resolution. Decision J (2026-10-29, phase 18 gate) then
  narrowed the exemption to the recorded acting profile. No open decision remains.

- **`Jj1G`** — **Validation 3 for task 004 (manager-owned plan** — dismissed — 2026-10-29 — reason: Already fixed. plan/notes/mod-users-api-surface.md lines 718-723, under "Amendments
  (2026-10-29, phase 8 remediation)", now hold the Section 2.9 amendment for
  sessions.impersonator_profile_id. It covers the administrator's acting profile, the
  NULL-pairing check with the impersonation columns, MF101 coverage, and its use by Renew
  to re-check impersonate from the parent session. That is the manager-owned validation 3
  of task 004.

- **`5M3G`** — **session_policy_factors is plpgsql (not inlin…** — promoted — ref: `5M3G` — 2026-10-29

- **`140Z`** — **Renew now runs liveSession for the child and…** — promoted — ref: `140Z` — 2026-10-29

- **`DuBq`** — **Project doc not reachable from README.md via…** — promoted — ref: `DuBq` — 2026-10-29

- **`yfao`** — **Dangling forward-links into docs/mf-standard…** — dismissed — 2026-10-29 — reason: Not actionable, a false positive. The links point into the docs/mf-standards git submodule
  (.gitmodules: path docs/mf-standards, url moduleforge/docs-mf-standards). That submodule is
  not initialized in this checkout: `git submodule status` prints
  "-c2dc98c9a070abe5449b21bc7c67ae6b51b53086 docs/mf-standards" (leading '-' = uninitialized),
  and the directory is empty. The links only dangle because the submodule content is absent.
  In an initialized checkout they resolve, so no change to the linking documents is needed.

- **`IDjP`** — **The listPrincipalCredentials description sti…** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 005 (docs-impersonation-reauth-and-decisions).
  api/openapi.yaml:3096-3098 (listPrincipalCredentials) now places the 403 for an
  impersonating caller before the 404. The issuePrincipalAPIKey, ssh-key and revoke
  operations (openapi.yaml:3141-3146, 3183-3188, 3227-3232) each state the impersonation
  and own-principal refusals. The finer misordering of the own-principal 403 against the
  masked 404 is a separate issue, tracked as lGXo.

- **`7TSm`** — **The renewImpersonation description lists onl…** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 005. The renewImpersonation description
  (api/openapi.yaml:2409-2416) now states:
    - renewal re-authorizes impersonate as the recorded acting profile, from the
      administrator's session;
    - a refusal answers 403 and ends the impersonation;
    - an ended parent answers 401 users.session_revoked;
    - max_auth_age is not re-applied.

- **`SO3A`** — **The PersonalPartyService row lists the refus…** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 005. The AGENTS.md:196 PersonalPartyService row now lists
  "ErrImpersonationNotAllowed (403 users.impersonation_not_allowed: an impersonating
  context)" among its Refusals.

- **`gxvu`** — **The "inserting a child of an impersonation s…** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 004 (test-precision-nesting-and-static-audit). The SQL
  nesting check in TestImpersonation_NoNesting (impersonation_integration_test.go:311-329)
  now supplies impersonator_profile_id (m.profileID, held by the parent principal), so
  only the nesting invariant can fire. It also asserts the "no nesting" message text.

- **`HGTD`** — **The credentialsTarget doc comment still desc…** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 005. The credentialsTarget doc comment
  (api/internal/service/principal_credentials.go:69-77) now lists the impersonation 403
  (before the lookup) and the own-principal 403 (before the read check), in code order.

- **`ehkS`** — **TestPartyPolicy_PartyRecordGate_NonMemberUpd…** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 004.
  TestPartyPolicy_PartyRecordGate_NonMemberUpdateGrantCannotWeakenThePolicy
  (api/internal/integration/party_policy_integration_test.go:~269) now pins a single
  status (404) instead of accepting 403 or 404. It also adds a positive control at ~276-282:
  once the party authorizes the outsider's password, the PUT succeeds.

- **`FvL1`** — **The static audit treats any opctx.AuthContex…** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 004. In api/internal/service/sessionless_contexts_test.go,
  a SessionID or CredentialID key with the literal value 0 now counts as absent
  (isZeroLiteral, lines 451-460). The file comment (lines 32-36) now says a variable-valued
  SessionID is out of scope and that the receiver walk covers the same package only.

- **`33w3`** — **Phase 18 added behavior the architecture doc…** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 005. docs/architecture.md:94-97 now records the
  impersonation lifecycle:
    - the credential refusals and own-principal 403 (Decisions K and M);
    - the EnsurePersonalParty refusal;
    - child auth_time inheritance;
    - Renew re-authorization without re-applying max_auth_age (Decision I).

- **`MGUW`** — **The invite-only app gate (Authenticator.chec…** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 005. The action-required section of docs/architecture.md
  (line 101) now says the invite-only app gate's login operation (checkAppGate, gateFor)
  surfaces 409 users.step_up_required, and that the operator-admin checker fails closed on
  it.

- **`MkEx`** — **The doc states sessionless contexts are exem…** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 005. docs/architecture.md:99 has a "Sessionless contexts
  (known limitation)" paragraph. It records that trust is inferred from absent
  SessionID/CredentialID until mod-core adds a system-actor marker, and that
  sessionless_contexts_test.go pins these contexts so they never reach Authorize.

- **`SV3I`** — **Plan notes need no edit; phase-18 task doc St** — dismissed — 2026-10-29 — reason: Not actionable. Its default ambiguity stamp is reclassified: this is an informational
  note from a phase 19 task agent saying that the plan notes need no edit and that the
  phase-18 task-doc Status text is historical. It states that no change is needed, raises
  no open decision, and names no defect, so no change could resolve it.

- **`ygAT`** — **Plan-notes / docs: the docs/architecture.md s** — dismissed — 2026-10-29 — reason: Already fixed by phase 19 task 005. The Decision K bullet in docs/architecture.md:95 now
  covers managed-user creation (POST /v1/parties/{party_uuid}/managed-users refused with
  403 users.impersonation_not_allowed). The createManagedUser description in
  api/openapi.yaml (~3355-3356) states the same refusal. The note itself says no plan-notes
  edit is needed.

- **`crQE`** — **Operator impersonation test is principal-wide** — fixed — ref: `phase-19-remediation-08/001-impersonator-profile-credential-exemption.md` — 2026-10-29

- **`DChD`** — **mod_users.session_policy_factors sets skip_c…** — fixed — ref: `phase-19-remediation-08/001-impersonator-profile-credential-exemption.md` — 2026-10-29

- **`bKBt`** — **mod_users.session_policy_factors sets skip_c…** — fixed — ref: `phase-19-remediation-08/001-impersonator-profile-credential-exemption.md` — 2026-10-29

- **`bFGy`** — **Confinement not applied to parent factors** — fixed — ref: `phase-19-remediation-08/002-confine-impersonator-parent-factors.md` — 2026-10-29

- **`KkGF`** — **profile_gate_for now evaluates an impersonat…** — fixed — ref: `phase-19-remediation-08/002-confine-impersonator-parent-factors.md` — 2026-10-29

- **`YqJd`** — **ProvisionManagedUserInTx (reached from Profi…** — fixed — ref: `phase-19-remediation-08/003-refuse-managed-user-creation-impersonating.md` — 2026-10-29

- **`AXFG`** — **Informational: Decision K in plan/notes/decis** — dismissed — 2026-10-29 — reason: Decision K wording aligned with the implemented plain 403 refusals in plan/notes/decisions-and-resolutions.md; docs follow the code.

- **`uBfI`** — **ProfileService.UpdateProfileStatus (PATCH /v…** — fixed — ref: `phase-20-remediation-09/001-refuse-impersonated-profile-end.md` — 2026-10-29

- **`zzo0`** — **Decision needed: when an impersonating caller** — fixed — ref: `phase-20-remediation-09/001-refuse-impersonated-profile-end.md` — 2026-10-29

- **`u1NG`** — **Two function header comments still describe…** — fixed — ref: `phase-20-remediation-09/002-precision-sql-comments-openapi-order-liveness-test.md` — 2026-10-29

- **`lGXo`** — **The four principal-credential operations doc…** — fixed — ref: `phase-20-remediation-09/002-precision-sql-comments-openapi-order-liveness-test.md` — 2026-10-29

- **`wRQn`** — **The Decision L liveness assertion for the im…** — fixed — ref: `phase-20-remediation-09/002-precision-sql-comments-openapi-order-liveness-test.md` — 2026-10-29

- **`j75L`** — **gated treats every non-anonymous AuthContext…** — promoted — ref: `j75L` — 2026-10-29

- **`x0mv`** — **The exported primitive EndInTx runs the full…** — promoted — ref: `x0mv` — 2026-10-29

- **`eIMh`** — **Suspend of a profile under impersonation is…** — promoted — ref: `eIMh` — 2026-10-29

- **`c6Dh`** — **Stale party-policy SQL comment** — promoted — ref: `c6Dh` — 2026-10-31

- **`owkI`** — **mod-repos lists lack BindAuthInputs** — promoted — ref: `owkI` — 2026-10-31

- **`QbSc`** — **mod-tasks list lacks BindAuthInputs** — promoted — ref: `QbSc` — 2026-10-31

- **`ZEu4`** — **mod-workflows lists lack BindAuthInputs** — promoted — ref: `ZEu4` — 2026-10-31

- **`wyTd`** — **needs-sibling: mod-core organizationService** — promoted — ref: `wyTd` — 2026-10-31

- **`7sEV`** — **Manifest change in moduleforge.module.yaml (b** — promoted — ref: `7sEV` — 2026-10-31

- **`QHzM`** — **Agent used python3 once for a multi-edit patc** — dismissed — 2026-10-31 — reason: Not actionable: a process note that a task agent used python3 once to patch two test
  files; the result was verified by build and tests, and no code change could resolve a
  past tool-use deviation.

- **`Ysh7`** — **Plan-note edit needed (manager-only): mod-use** — dismissed — 2026-10-31 — reason: Already fixed: plan/notes/mod-users-api-surface.md lines 741-743 (Amendments 2026-10-30,
  phase 21, "Bootstrap") record the NewBootstrapService organization-service parameter and
  OrganizationFromServices.

- **`wkvN`** — **Stale localAuthz package doc on step 1** — promoted — ref: `wkvN` — 2026-10-31

- **`GNRh`** — **Plan-note edits (manager-only, plan/notes/):** — promoted — ref: `GNRh` — 2026-10-31

- **`VZLf`** — **Manager to grep plan/overview.md and plan/not** — dismissed — 2026-10-31 — reason: Already fixed: grep for 'inferred', 'sessionless', 'five-argument' and 'raw
  organization' in plan/overview.md and plan/notes/work-packages.md finds only
  plan/overview.md line 161 (the phase-21 row, which describes the current behavior) and
  nothing in work-packages.md.

- **`j7Ym`** — **Coordination note on link-siblings.sh in deci** — promoted — ref: `j7Ym` — 2026-10-31

- **`yZTA`** — **Integration tests did not run (docs-only task** — dismissed — 2026-10-31 — reason: Not actionable: a status note that task 004 (documentation only, no database needed) did
  not run integration tests; tasks 001-003 ran the integration suites per their Status
  sections. No change could resolve it.

- **`3JAO`** — **The list-side row filter evaluates mod_users…** — promoted — ref: `3JAO` — 2026-10-31

- **`Xyl5`** — **ListPrincipals now opens a read-only transac…** — promoted — ref: `Xyl5` — 2026-10-31

- **`blvz`** — **Project doc api/server/CLAUDE.md is not reac…** — promoted — ref: `blvz` — 2026-10-31

- **`knC0`** — **Dangling relative link: ./docs/mf-standards/…** — promoted — ref: `knC0` — 2026-10-31

- **`XNI4`** — **Dangling relative link: ./docs/mf-standards/…** — promoted — ref: `XNI4` — 2026-10-31

- **`SOih`** — **Dangling relative link: ./docs/mf-standards/…** — promoted — ref: `SOih` — 2026-10-31

- **`oAFR`** — **Dangling relative link: ./mf-standards/archi…** — promoted — ref: `oAFR` — 2026-10-31

- **`4NOn`** — **Dangling relative link: ./mf-standards/archi…** — promoted — ref: `4NOn` — 2026-10-31

- **`gpKq`** — **Dangling relative link: ./mf-standards/archi…** — promoted — ref: `gpKq` — 2026-10-31

- **`d4xh`** — **Dangling relative link: ./mf-standards/archi…** — promoted — ref: `d4xh` — 2026-10-31

- **`mj5E`** — **Dangling relative link: ./mf-standards/archi…** — promoted — ref: `mj5E` — 2026-10-31

- **`uE7e`** — **Dangling relative link: ./mf-standards/build…** — promoted — ref: `uE7e` — 2026-10-31

- **`lWS6`** — **Dangling relative link: ./mf-standards/archi…** — promoted — ref: `lWS6` — 2026-10-31

- **`IvGP`** — **Dangling relative link: ./mf-standards/archi…** — promoted — ref: `IvGP` — 2026-10-31

- **`U5ln`** — **Dangling relative link: ../docs/mf-standards…** — promoted — ref: `U5ln` — 2026-10-31

- **`J86E`** — **mod-authz stepUpStale infers system trust** — promoted — ref: `J86E` — 2026-10-31

- **`ck8O`** — **app-mftodo installs unfiltered access fns** — promoted — ref: `ck8O` — 2026-10-31

- **`yDll`** — **app-mfgit installs unfiltered access fns** — promoted — ref: `yDll` — 2026-10-31

- **`tpls`** — **gated() now exempts any context for which op…** — fixed — ref: `phase-22-remediation-10/001-require-sessionless-system-actor.md` — 2026-10-31

- **`P0yG`** — **The new package documentation (and api/local…** — fixed — ref: `phase-22-remediation-10/002-apply-assurance-gate-on-principal-list.md` — 2026-10-31

- **`DDfb`** — **createOrganization now creates the operator…** — fixed — ref: `phase-22-remediation-10/003-dispatch-after-commit-observations.md` — 2026-10-31

- **`bwMq`** — **createOrganization uses OrganizationService.…** — fixed — ref: `phase-22-remediation-10/003-dispatch-after-commit-observations.md` — 2026-10-31

- **`06rd`** — **Missing ObserveAfterCommit for CreateInTx** — fixed — ref: `phase-22-remediation-10/003-dispatch-after-commit-observations.md` — 2026-10-31

- **`8Wfo`** — **Comments still say the unauthenticated setup…** — dismissed — 2026-10-31 — reason: Already planned: 11/001 (mod-users-drift-scan). Its requirements scan code comments in gui/src
  for the term `/v1/self` and name the rewrite "`/v1/self` to `/v1/me`", which is exactly the
  change this item asks for in oidc-setup-gate.tsx and client-layout.tsx; its validation rg
  includes /v1/self over gui/src. Reclassified from bug to documentation (stale comments).

- **`hcdm`** — **The panel calls api.auth.providers() on ever…** — promoted — ref: `hcdm` — 2026-10-31

- **`jRXp`** — **OIDC state cookie vs credentials omit** — promoted — ref: `jRXp` — 2026-10-31

- **`AReg`** — **Selection page loses return path on reload** — dismissed — 2026-10-31 — reason: Already fixed by phase 23 task 004. The pending selection is persisted per tab
  (gui/src/lib/config.ts:401-475, selection list under `<key>.selection` and return
  path under `<key>.selection_return`; written in commitTokenResponse at
  gui/src/lib/api.ts:744-749), a reload restores selectionRequired and the profile list
  (gui/src/lib/auth-context.tsx:406-423), and SelectProfilePage takes a `returnPath`
  prop, else the stored path, validated with isSafeReturnPath
  (gui/src/components/select-profile-page.tsx:27, 53-61).

- **`zwCX`** — **Login, register, email-code verify, handoff…** — dismissed — 2026-10-31 — reason: Already fixed by phase 23 task 001. loadUser raises a counter-backed userLoading flag
  synchronously with the token commit (gui/src/lib/auth-context.tsx:283-307), the
  context's isLoading folds it in (`isLoading: isLoading || userLoading`, line 673),
  and RequireAuth returns early while isLoading (gui/src/components/require-auth.tsx:39,
  65). This covers login, register, email-code, handoff and the fork path, which all
  reach loadUser through the token_response subscription (auth-context.tsx:360-368).

- **`4ohc`** — **The API's OIDC callback redirects every outc…** — dismissed — 2026-10-31 — reason: Already fixed by phase 23 task 002. OidcCallbackPage now parses `linked=1` and
  `stepped_up=1` (gui/src/components/oidc-callback-page.tsx:96-97), and the
  `!handoff && (linked || steppedUp)` branch (lines 115-147) validates `return` with
  isSafeReturnPath, refreshes on step-up, and calls onComplete without onError. The
  residual concurrency defect in that step-up refresh is tracked separately as 6qD2.

- **`b1HZ`** — **After a sign-in that answers selection_requi…** — dismissed — 2026-10-31 — reason: Already fixed by phase 23 task 004. doRefresh with a refresh token and no profile
  uuid now re-publishes the stored selection as selection_required and throws
  `selection_required` instead of calling endTabSession (gui/src/lib/api.ts:843-869),
  and hydrate restores the selection state without calling session.get when a pending
  selection is stored (gui/src/lib/auth-context.tsx:406-423).

- **`4EHb`** — **While impersonating, the tab holds the imper…** — dismissed — 2026-10-31 — reason: Already fixed by phase 23 task 003. execute captures `sentAsImpersonation` at send
  time (gui/src/lib/api.ts:1002-1005) and, on a non-revocation 401, neither refreshes nor
  replays: it drops the dead token and marker, emits impersonation_ended and throws
  users.impersonation_ended (api.ts:1071-1094). impersonations.end treats that error as
  already ended and restores the administrator without a replayed DELETE
  (api.ts:1575-1608). The fail-open residual of the marker is tracked as vAo0.

- **`oFfM`** — **In hydrate, any failure of session.get other…** — dismissed — 2026-10-31 — reason: Already fixed by phase 23 task 001. Hydrate keeps the tokens on status 0 and 5xx and
  sets a retryable sessionError (gui/src/lib/auth-context.tsx:460-470), exposed with
  retrySession (lines 497-507, 673-675); RequireAuth does not call onUnauthenticated
  while sessionError is set (gui/src/components/require-auth.tsx:51, 77).

- **`A6aq`** — **logout() calls api.session.logout(), which i…** — dismissed — 2026-10-31 — reason: Already fixed by phase 23 task 001. logout now retries once on a 401 when a refresh
  token exists (session.get refreshes through the shared lineage, then logout again),
  and the local-only fallback is documented in comments (gui/src/lib/auth-context.tsx:545-574).
  The remaining impersonation-specific gap in that retry is tracked as Bq75.

- **`Qo20`** — **openVerify calls setVerifyTarget(identifier)…** — dismissed — 2026-10-31 — reason: Already fixed by phase 23 task 005. sendCode takes an explicit `inDialog` flag
  (gui/src/components/identifiers-panel.tsx:174, 183-188) and openVerify calls
  `sendCode(identifier, true)` (line 203), so a first-send failure is shown in the dialog.

- **`paQk`** — **The \`app\` option is documented as an app "sl…** — dismissed — 2026-10-31 — reason: Already fixed by phase 23 task 006. Every named doc comment now says "The app (uuid)":
  gui/src/lib/auth-context.tsx:67, gui/src/components/login-form.tsx:87,
  register-form.tsx:20, email-code-page.tsx:31 and auth-page.tsx:59. A grep for "slug"
  in those files finds no remaining hit.

- **`wafz`** — **The stepped_up=1 branch calls api.auth.token…** — dismissed — 2026-10-31 — reason: Duplicate of 6qD2. Both describe the same defect at the same place: the stepped_up=1
  branch in gui/src/components/oidc-callback-page.tsx:124-136 calls api.auth.token with
  the stored refresh token directly, bypassing the refreshTabToken single-flight
  (gui/src/lib/api.ts:786-810), so a concurrent hydrate refresh can trip refresh_reuse.
  6qD2, which carries the security classification, is kept and fixed.

- **`LdOO`** — **logout on a 401 retries via api.session.get(…** — dismissed — 2026-10-31 — reason: Duplicate of Bq75. Both describe the same gap in logout's 401 retry
  (gui/src/lib/auth-context.tsx:545-574) while an expired impersonation token is
  stored. Bq75, which carries the security classification, is kept and fixed.

- **`UmzX`** — **api/server/CLAUDE.md unreachable from README** — promoted — ref: `UmzX` — 2026-10-31

- **`cLtT`** — **getStoredImpersonating now base64-decodes an…** — promoted — ref: `cLtT` — 2026-10-31

- **`6qD2`** — **On "#stepped_up=1" the callback calls api.au…** — fixed — ref: `phase-24-remediation-12/001-route-step-up-refresh-through-single-flight.md` — 2026-10-31

- **`vAo0`** — **The impersonation guard is fail-open: an abs…** — fixed — ref: `phase-24-remediation-12/002-fail-closed-impersonation-detection.md` — 2026-10-31

- **`Bq75`** — **logout while the tab holds an impersonation…** — fixed — ref: `phase-24-remediation-12/003-revoke-admin-session-on-impersonated-logout.md` — 2026-10-31

- **`FhDO`** — **Routing the step-up rotation through the sha…** — promoted — ref: `FhDO` — 2026-10-31

- **`NfAS`** — **On the impersonation_ended path, the session…** — dismissed — 2026-10-31 — reason: Already fixed in phase 25 task 001 (commit 89fe62a). runLogout awaits any
  in-flight restoreRef before its own restore/revoke (auth-context.tsx:579-580), and
  logout sets loggingOutRef and bumps logoutGenRef (lines 679-680) so the session
  event handler ignores non-signed_out events during logout (lines 355-362) and
  late loadUser/hydration commits are dropped (lines 303-308, 453-458).

- **`Wstk`** — **refreshAfterStepUp silently returns while th…** — dismissed — 2026-10-31 — reason: Already fixed in phase 25 task 003 (commit 9709ad7). The requested coverage
  exists: gui/src/lib/api.test.ts:787 ("refreshAfterStepUp while impersonating
  issues no token request, keeps the token and signals the skip") and
  gui/src/components/oidc-callback-page.test.tsx:228 ("#stepped_up=1 while
  impersonating issues no token request and does not report raised assurance").

- **`BWVZ`** — **loadUser's generation guard covers only the…** — promoted — ref: `BWVZ` — 2026-10-31

- **`H5Po`** — **Intent (3) is that logout while impersonatin…** — promoted — ref: `H5Po` — 2026-10-31

- **`moDM`** — **getStoredImpersonating is documented and fra…** — promoted — ref: `moDM` — 2026-10-31

- **`m3Jr`** — **After a handler-supplied step-up token is st…** — promoted — ref: `m3Jr` — 2026-10-31

- **`LJFE`** — **The refreshAfterStepUp no-op while impersona…** — promoted — ref: `LJFE` — 2026-10-31

- **`D3Q9`** — **The impersonation_ended event starts restore…** — promoted — ref: `D3Q9` — 2026-10-31

- **`hPqK`** — **consumer-upgrade-notes links a removed anchor** — dismissed — 2026-10-31 — reason: Already fixed (task 002 repaired it). docs/consumer-upgrade-notes.md:31 now links
  ./jwt-eddsa.md#derived-oauth-state-key, which matches the heading "## Derived OAuth state key"
  at docs/jwt-eddsa.md:145. No link to #derived-state-and-step-up-keys remains in the file.
  The "EdDSA verify-only key" and "Token hardening" sections were rewritten. :11 says HS256
  and JWT_ALG are removed, :24 says WithHS256Secret is removed, :27 says auth.StepUpKey is
  removed, and :38 says the step-up token is removed. The intro pointer at :5 now reads
  "D14 to D31".

- **`BpMF`** — **api/server/CLAUDE.md unreachable from README** — dismissed — 2026-10-31 — reason: Not actionable. api/server/CLAUDE.md contains only an empty <claude-mem-context> block,
  which makes it a tool-generated agent-memory stub rather than a project document, so a
  README link would add nothing. This plan's closed findings already dismissed the same
  file on these grounds as CTF3 and HzgJ. The durable question of whether to keep, ignore
  or exempt the file is already in the project backlog as VIcU and 76Ma (both promoted).
  The phase did not change the file; it predates the plan (17bc64e, per CTF3).

- **`x3It`** — **CORS AllowCredentials on Bearer-only API** — promoted — ref: `x3It` — 2026-10-31

- **`N2lP`** — **AGENTS.md misplaces RequireAuth mounting** — dismissed — 2026-10-31 — reason: Already fixed by phase 26 task 004: AGENTS.md:228 now says the manifest
  (moduleforge.module.yaml) mounts requireOIDCConfirmed then requireAuth on
  every authenticated prefix, that registrars never mount
  auth.NewRequireAuth themselves, and that handlers answer 500 when no
  auth.Identity is present. The old "Registrars of authenticated routes
  mount" wording is gone.

- **`ndUS`** — **Stale X-Step-Up-Token in CORS headers** — dismissed — 2026-10-31 — reason: Already fixed by phase 26 task 003: api/internal/server/server.go:90
  AllowedHeaders is now Accept, Authorization, Content-Type, X-App,
  X-Request-ID (no X-Step-Up-Token), and
  api/internal/server/cors_test.go:210-237 (TestCorsOptions_MethodsAndHeaders)
  asserts the header is absent.

- **`iZ3E`** — **Facade lacks input/summary type aliases** — dismissed — 2026-10-31 — reason: Already fixed by phase 26 task 002: api/usersservice/service.go:97
  (IssueAPIKeyInput), :101 (IssuedAPIKey) and :366 (ProfileSummary) now
  alias the inner types, with a compile-only test in
  api/usersservice/facade_aliases_test.go.

- **`RVUM`** — **CORE_FIELD_KEY_HEX read by no code here** — dismissed — 2026-10-31 — reason: Already fixed by phase 26 task 006: .env.example:207-215 now carries a
  "Field-encryption seed (owned by mod-core)" block stating that mod-core's
  fieldcrypto.NewFromEnvOrGenerate reads CORE_FIELD_KEY_HEX, that the
  mod-users dev server calls it from api/cmd/server/main.go, its first-boot
  seed semantics, and the rotation route.

- **`cRuN`** — **The refusals table lists the redirect error…** — dismissed — 2026-10-31 — reason: Already fixed by phase 26 task 005: docs/oidc-troubleshooting.md:121 now
  lists `authentication_failed`, matching RedirectErrAuthFailed at
  api/internal/auth/resolver.go:26; the resolver comment at :121 was updated
  too.

- **`fb9H`** — **New paragraph says browser clients "do not n…** — dismissed — 2026-10-31 — reason: Already fixed by phase 26 task 003: docs/cors.md:11 now documents the
  credentials 'include' exception for the two OIDC start POSTs, the
  explicit CORS_ORIGINS requirement, and links
  architecture/sessions-and-tokens.md#client-contract (heading exists at
  sessions-and-tokens.md:231). The new sentence names the wrong two calls;
  that error is tracked separately by EqWE and Yo85.

- **`BNNN`** — **CORS AllowedMethods is GET, POST, PUT, DELET…** — dismissed — 2026-10-31 — reason: Already fixed by phase 26 task 003: api/internal/server/server.go:86
  AllowedMethods now includes PATCH, and cors_test.go:220-228 asserts it.

- **`kmxU`** — **architecture.md states the HTTP API is defin…** — dismissed — 2026-10-31 — reason: Already fixed by phase 26 task 004: in docs/architecture.md:97 the OIDC
  configuration row now states that these routes are served by the
  manifest-mounted handlers but are not described in api/openapi.yaml.

- **`AMeC`** — **The main.go comment at api/cmd/server/main.go** — promoted — ref: `AMeC` — 2026-10-31

- **`GZNQ`** — **AllowedHeaders still omits X-Setup-Token, wh…** — dismissed — 2026-10-31 — reason: Duplicate of gz8M: the same missing X-Setup-Token entry in
  api/internal/server/server.go AllowedHeaders, with the same cross-origin
  preflight failure for the bootstrap and oidc-config routes. gz8M is
  dispositioned fix, and its spec adds the header.

- **`AQhb`** — **api/server/CLAUDE.md unreachable from README** — dismissed — 2026-10-31 — reason: Duplicate of open project-backlog items DPZi ("Orphan:
  api/server/CLAUDE.md is not reachable", plan users-security-hardening)
  and oti2 ("Orphan docs: k8s/serverless/CLAUDE stubs"). Both are still
  open in plan/followups.yaml and report the same unreachable file.
  api/server/CLAUDE.md is a pre-existing 2-line empty claude-mem-context
  stub that is unrelated to the identity-model goals. Promoting it would
  add a third copy to the backlog.

- **`e4f6`** — **The "Persisted signing key" section explains…** — promoted — ref: `e4f6` — 2026-10-31

- **`UuyR`** — **Confirmed: the comment does not name CORE_FI…** — promoted — ref: `UuyR` — 2026-10-31

- **`NqKT`** — **Verified accurate against model/migrations/s…** — promoted — ref: `NqKT` — 2026-10-31

- **`gz8M`** — **X-Setup-Token is not in CORS AllowedHeaders…** — fixed — ref: `phase-27-remediation-15/001-align-cors-setup-token-and-docs.md` — 2026-10-31

- **`EqWE`** — **The new text says the two OIDC start POSTs a…** — fixed — ref: `phase-27-remediation-15/001-align-cors-setup-token-and-docs.md` — 2026-10-31

- **`Yo85`** — **The new text says the exception covers "the…** — fixed — ref: `phase-27-remediation-15/001-align-cors-setup-token-and-docs.md` — 2026-10-31

- **`7Yab`** — **Still says the CORS middleware "still lists…** — fixed — ref: `phase-27-remediation-15/001-align-cors-setup-token-and-docs.md` — 2026-10-31

- **`CubF`** — **A concrete 32-byte hex field-encryption seed…** — fixed — ref: `phase-27-remediation-15/002-mark-field-key-seed-dev-only.md` — 2026-10-31

- **`7O8G`** — **The new sentence links the X-Setup-Token sta…** — promoted — ref: `7O8G` — 2026-10-31

- **`7eEW`** — **Update /v1/self paths in oidc_config_test (ty** — promoted — ref: `7eEW` — 2026-10-31

- **`VtaW`** — **Fix corporation wording in authz_test comment** — promoted — ref: `VtaW` — 2026-10-31

- **`4HXk`** — **Scan docs/mf-standards for old identity terms** — promoted — ref: `4HXk` — 2026-10-31

- **`1JCd`** — **PROPOSED REMOVALS for manager to apply after** — promoted — ref: `1JCd` — 2026-10-31

- **`usDr`** — **PROPOSED CROSS-PROJECT REMOVALS (no resolved_** — promoted — ref: `usDr` — 2026-10-31

- **`A5PF`** — **PROPOSED PATCHES, manager decision only if re** — promoted — ref: `A5PF` — 2026-10-31

- **`8yRH`** — **Note for manager: removals of VJg2 and PRvD c** — promoted — ref: `8yRH` — 2026-10-31

- **`tEs4`** — **REASON_COPY has no entries for credential_re…** — promoted — ref: `tEs4` — 2026-11-10

- **`IzAq`** — **The CredentialProfileAvailability query (and…** — fixed — ref: `phase-29-remediation-16/001-document-credential-profile-availability-statuses.md` — 2026-11-10

- **`ta2T`** — **Root make build (Validation #3) not run per d** — dismissed — 2026-11-10 — reason: Accepted: diff verified comment-only (24 added comment lines across sessions.sql, querier.go, sessions.sql.go; no SQL/Go behavior change); go build in model/ is a sufficient substitute here, and the manager-tier root make build is run separately before landing.

- **`XXb9`** — **Ambiguous ProfileAvailability wording** — promoted — ref: `XXb9` — 2026-11-10

## Remediation

- Rounds used: 16 across all gates (resolved max_rounds: 4 per gate).
- Remediation tasks added: 74 (resolved max_tasks_per_remediation_round per round: 25).
- Remediation phases:
  - `remediation-01` — 14 task(s)
  - `remediation-02` — 1 task(s)
  - `remediation-03` — 8 task(s)
  - `remediation-04` — 8 task(s)
  - `remediation-05` — 4 task(s)
  - `remediation-06` — 2 task(s)
  - `remediation-07` — 6 task(s)
  - `remediation-08` — 5 task(s)
  - `remediation-09` — 2 task(s)
  - `remediation-10` — 3 task(s)
  - `remediation-11` — 6 task(s)
  - `remediation-12` — 3 task(s)
  - `remediation-13` — 3 task(s)
  - `remediation-14` — 6 task(s)
  - `remediation-15` — 2 task(s)
  - `remediation-16` — 1 task(s)
- Security review required: yes (at least one remediation phase carries `security_review: required`).

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 06 — Users Identity Core

- [x] [001-rewrite-baseline-schema.md](./phase-06-users-identity-core/001-rewrite-baseline-schema.md) — tier `opus-high` · branch `plan/identity-model-impl-06-001` · commit `5c342b0` · merge `ef800f747da13d951185ff07012837e7de3ef297`
- [x] [002-rewrite-queries-and-generated-code.md](./phase-06-users-identity-core/002-rewrite-queries-and-generated-code.md) — tier `sonnet-med` · branch `plan/identity-model-impl-06-002` · commit `461c891` · merge `5ba1530a419f3845b0d900c30f2507793ec833d2`
- [x] [003-token-carrier-and-auth-middleware.md](./phase-06-users-identity-core/003-token-carrier-and-auth-middleware.md) — tier `opus-med` · branch `plan/identity-model-impl-06-003` · commit `cb9176d` · merge `5126cdaf44027caee40b874f59dd6b521d6fc32c`
- [x] [004-users-identifiers-credentials-challenges.md](./phase-06-users-identity-core/004-users-identifiers-credentials-challenges.md) — tier `sonnet-high` · branch `plan/identity-model-impl-06-004` · commit `2fd92b1` · merge `ccb36c0d4d17fda005b64861e5949fb8a53e221b`
- [x] [005-sessions-and-auth-routes.md](./phase-06-users-identity-core/005-sessions-and-auth-routes.md) — tier `opus-med` · branch `plan/identity-model-impl-06-005` · commit `2d4025e` · merge `81b4af00862fa4694747bd1f086e40d716abc33c`
- [x] [006-oidc-resolver.md](./phase-06-users-identity-core/006-oidc-resolver.md) — tier `sonnet-high` · branch `plan/identity-model-impl-06-006` · commit `2482d7f` · merge `d6ba9ff4b14f37bd4fb91d72d102bf5465908fd0`
- [x] [007-self-service-and-principals.md](./phase-06-users-identity-core/007-self-service-and-principals.md) — tier `sonnet-high` · branch `plan/identity-model-impl-06-007` · commit `b7addc9` · merge `bf4704575cff17caf3d7c2e382e6ab2a9b1c94f4`
- [x] [008-server-wiring-and-surface-removal.md](./phase-06-users-identity-core/008-server-wiring-and-surface-removal.md) — tier `sonnet-med` · branch `plan/identity-model-impl-06-008` · commit `c017fde` · merge `09c1f887873f99516e663a7d4d5450fd7c29e01e`
- [x] [009-db-invariant-integration-tests.md](./phase-06-users-identity-core/009-db-invariant-integration-tests.md) — tier `sonnet-med` · branch `plan/identity-model-impl-06-009` · commit `cf2d7e2` · merge `eb421b966a12f19d14592f435e8962a2afe6ec5e`
- [x] [010-session-credential-route-tests.md](./phase-06-users-identity-core/010-session-credential-route-tests.md) — tier `sonnet-high` · branch `plan/identity-model-impl-06-010` · commit `6ef9004` · merge `f24fa926bf03fb55ccbc59906014e98833bb0c5b`

### Phase 07 — Parties, Profiles and Bootstrap

- [x] [001-profile-lifecycle-and-invitations.md](./phase-07-parties-profiles-and-bootstrap/001-profile-lifecycle-and-invitations.md) — tier `sonnet-high` · branch `plan/identity-model-impl-07-001` · commit `796d296` · merge `27fa2896ce3a6930978cb11265b8b013f4680e69`
- [x] [002-party-gating-administration.md](./phase-07-parties-profiles-and-bootstrap/002-party-gating-administration.md) — tier `sonnet-med` · branch `plan/identity-model-impl-07-002` · commit `3abab2b` · merge `d01cdf7e931d7a17893c9f008e3c5129710a8a81`
- [x] [003-outside-collaborators-and-compliance.md](./phase-07-parties-profiles-and-bootstrap/003-outside-collaborators-and-compliance.md) — tier `sonnet-high` · branch `plan/identity-model-impl-07-003` · commit `01fade3` · merge `7b79af9ac19aee1954cd6a215b34c9d4fd3c352d`
- [x] [004-setup-token-bootstrap.md](./phase-07-parties-profiles-and-bootstrap/004-setup-token-bootstrap.md) — tier `opus-med` · branch `plan/identity-model-impl-07-004` · commit `768bb8f` · merge `390093d3ef1510f9eca9f35b7fff368921599c0a`
- [x] [005-party-aware-ssh-profile-derivation.md](./phase-07-parties-profiles-and-bootstrap/005-party-aware-ssh-profile-derivation.md) — tier `sonnet-med` · branch `plan/identity-model-impl-07-005` · commit `4aa0ac5` · merge `d1f7d44e64e5e8478507f2cc015e10c2ff52ab11`
- [x] [006-party-flows-integration-tests.md](./phase-07-parties-profiles-and-bootstrap/006-party-flows-integration-tests.md) — tier `sonnet-high` · branch `plan/identity-model-impl-07-006` · commit `3a115f7` · merge `bb8dce9f127eb42b15bdafb2838beb3a0708aba0`
- [x] [007-trusted-profile-admission.md](./phase-07-parties-profiles-and-bootstrap/007-trusted-profile-admission.md) — tier `sonnet-med` · branch `plan/identity-model-impl-07-007` · commit `a60cf76` · merge `78aca5ad65cdadca255b68b90b8884cbd359a309`

### Phase 08 — Users Authorization Engine

- [x] [001-rewrite-authorization-engine.md](./phase-08-users-authorization-engine/001-rewrite-authorization-engine.md) — tier `opus-high` · branch `plan/identity-model-impl-08-001` · commit `b81ed0a` · merge `3955f4d6fc9d30a6b87367db0e32e36bc11f1482`
- [x] [002-authorization-engine-tests.md](./phase-08-users-authorization-engine/002-authorization-engine-tests.md) — tier `sonnet-high` · branch `plan/identity-model-impl-08-002` · commit `c44b3e5` · merge `4386907a9a770235605018caa4617b2579109785`
- [x] [003-impersonation-sessions.md](./phase-08-users-authorization-engine/003-impersonation-sessions.md) — tier `sonnet-high` · branch `plan/identity-model-impl-08-003` · commit `5ffa05b` · merge `cd10cc4d1abaeaa55fc9c5248161dc4dc25aedac`
- [x] [004-credentials-operation-and-app-login-gate.md](./phase-08-users-authorization-engine/004-credentials-operation-and-app-login-gate.md) — tier `sonnet-med` · branch `plan/identity-model-impl-08-004` · commit `974157a` · merge `3d8cd9db69540c98514aef48d383fc93d6bd26d8`
- [x] [005-personal-party.md](./phase-08-users-authorization-engine/005-personal-party.md) — tier `sonnet-med` · branch `plan/identity-model-impl-08-005` · commit `ca1ae46` · merge `b8afa14489472a31c39a102a802ed05e75d09468`

### Phase 09 — Users GUI and Frontend Contract

- [x] [001-rebuild-api-client-and-auth-context.md](./phase-09-users-gui-and-frontend-contract/001-rebuild-api-client-and-auth-context.md) — tier `sonnet-high` · branch `plan/identity-model-impl-09-001` · commit `dd54577` · merge `2f1332083240238e7e6cefdf182bffcc40095ef0`
- [x] [002-build-profile-chrome.md](./phase-09-users-gui-and-frontend-contract/002-build-profile-chrome.md) — tier `sonnet-med` · branch `plan/identity-model-impl-09-002` · commit `cbdeee5` · merge `1ac49188f0fa2d05fa0ad4a1ba9fbfbfb72748c5`
- [x] [003-port-auth-pages-to-session-api.md](./phase-09-users-gui-and-frontend-contract/003-port-auth-pages-to-session-api.md) — tier `sonnet-med` · branch `plan/identity-model-impl-09-003` · commit `a250ccb` · merge `76a062c83bfcb859bc835be03429caa363524bce`
- [x] [004-build-self-service-panels.md](./phase-09-users-gui-and-frontend-contract/004-build-self-service-panels.md) — tier `sonnet-med` · branch `plan/identity-model-impl-09-004` · commit `e311879` · merge `02349fca64ff8e4f79a9cfd0d471e0c5c0c8ebd8`
- [x] [005-build-bootstrap-and-oidc-config-pages.md](./phase-09-users-gui-and-frontend-contract/005-build-bootstrap-and-oidc-config-pages.md) — tier `sonnet-med` · branch `plan/identity-model-impl-09-005` · commit `564a7c5` · merge `42e8c441372cc5e87f5489aee584fda6da6edd36`
- [x] [006-review-gui-exports-and-contract-docs.md](./phase-09-users-gui-and-frontend-contract/006-review-gui-exports-and-contract-docs.md) — tier `sonnet-low` · branch `plan/identity-model-impl-09-006` · commit `2588970` · merge `73845c51f150715396f9deae0cc187183c17b9b4`

### Phase 10 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-10-doc-updates/001-update-architecture-docs.md) — tier `sonnet-high` · branch `plan/identity-model-impl-10-001` · commit `62648fd8` · merge `9d8a5be031da5904c161440ac76c643d295f384e`
- [x] [002-write-consumer-upgrade-notes.md](./phase-10-doc-updates/002-write-consumer-upgrade-notes.md) — tier `sonnet-med` · branch `plan/identity-model-impl-10-002` · commit `24f6ef32` · merge `6030f9691fa43f994017fe33304e675aea4adb02`
- [x] [003-review-repository-docs.md](./phase-10-doc-updates/003-review-repository-docs.md) — tier `sonnet-low` · branch `plan/identity-model-impl-10-003` · commit `23ceca5f` · merge `1795184ae2d27d4ba9b1b9af0ac75c2106101e62`

### Phase 11 — Users Identity Final

- [x] [001-mod-users-drift-scan.md](./phase-11-users-identity-final/001-mod-users-drift-scan.md) — tier `sonnet-low` · branch `plan/identity-model-impl-11-001` · commit `f52c194a` · merge `a8d222d786044af3fda8c05510281af6d58456d8`
- [x] [002-reconcile-superseded-followups.md](./phase-11-users-identity-final/002-reconcile-superseded-followups.md) — tier `haiku-med` · branch `plan/identity-model-impl-11-002` · commit `4fc8ace6` · merge `296305fa9c431996c281d22bfcea11259f3dfc18`

### Phase 12 — Remediation Round 1

- [x] [001-enforce-byo-identity-on-guest-paths.md](./phase-12-remediation-01/001-enforce-byo-identity-on-guest-paths.md) — tier `sonnet-med` · branch `plan/identity-model-impl-12-001` · commit `050fd51` · merge `1af9df6`
- [x] [002-exclude-revoked-factors-from-availability.md](./phase-12-remediation-01/002-exclude-revoked-factors-from-availability.md) — tier `sonnet-med` · branch `plan/identity-model-impl-12-002` · commit `476468a` · merge `1d45242`
- [x] [003-bind-oidc-login-to-provider.md](./phase-12-remediation-01/003-bind-oidc-login-to-provider.md) — tier `sonnet-med` · branch `plan/identity-model-impl-12-003` · commit `baa0176` · merge `d343508`
- [x] [004-harden-audience-token-claims.md](./phase-12-remediation-01/004-harden-audience-token-claims.md) — tier `sonnet-low` · branch `plan/identity-model-impl-12-004` · commit `327645a` · merge `d4b89a3`
- [x] [005-compose-mod-audit-migrations.md](./phase-12-remediation-01/005-compose-mod-audit-migrations.md) — tier `sonnet-low` · branch `plan/identity-model-impl-12-005` · commit `5e56852` · merge `fc07a3a`
- [x] [006-register-display-renderers-in-manifest.md](./phase-12-remediation-01/006-register-display-renderers-in-manifest.md) — tier `sonnet-med` · branch `plan/identity-model-impl-12-006` · commit `7ffcc83` · merge `b9d20b3`
- [x] [007-index-credential-and-challenge-lookups.md](./phase-12-remediation-01/007-index-credential-and-challenge-lookups.md) — tier `sonnet-low` · branch `plan/identity-model-impl-12-007` · commit `eb61cb6` · merge `891392e`
- [x] [008-update-deploy-manifests-signing-key.md](./phase-12-remediation-01/008-update-deploy-manifests-signing-key.md) — tier `sonnet-low` · branch `plan/identity-model-impl-12-008` · commit `603c765` · merge `0cfa2ab`
- [x] [009-link-unreachable-readmes.md](./phase-12-remediation-01/009-link-unreachable-readmes.md) — tier `sonnet-low` · branch `plan/identity-model-impl-12-009` · commit `ab8ccb6` · merge `7daac3e`
- [x] [010-confine-party-bound-oidc-link.md](./phase-12-remediation-01/010-confine-party-bound-oidc-link.md) — tier `sonnet-med` · branch `plan/identity-model-impl-12-010` · commit `eb6bdea` · merge `8dff09f`
- [x] [011-identifier-removal-set-null-fks.md](./phase-12-remediation-01/011-identifier-removal-set-null-fks.md) — tier `sonnet-high` · branch `plan/identity-model-impl-12-011` · commit `3d38718` · merge `2729fe4`
- [x] [012-copy-party-authorizations-on-supersede.md](./phase-12-remediation-01/012-copy-party-authorizations-on-supersede.md) — tier `sonnet-med` · branch `plan/identity-model-impl-12-012` · commit `ff07d2c` · merge `398050f`
- [x] [013-bind-handoff-code-to-verifier.md](./phase-12-remediation-01/013-bind-handoff-code-to-verifier.md) — tier `sonnet-high` · branch `plan/identity-model-impl-12-013` · commit `5bd0bba` · merge `fb0fab1`
- [x] [014-exclude-archived-apps-from-audience.md](./phase-12-remediation-01/014-exclude-archived-apps-from-audience.md) — tier `sonnet-low` · branch `plan/identity-model-impl-12-014` · commit `042c198` · merge `640b50f`

### Phase 13 — Remediation Round 2

- [x] [001-guard-active-password-identifier-unbind.md](./phase-13-remediation-02/001-guard-active-password-identifier-unbind.md) — tier `sonnet-high` · branch `plan/identity-model-impl-13-001` · commit `fa061be` · merge `de9ae2ff2ebafbc9523f4aaceea11ec27bc03e90`

### Phase 14 — Remediation Round 3

- [x] [001-require-verified-invitee-and-authorize-managed-password.md](./phase-14-remediation-03/001-require-verified-invitee-and-authorize-managed-password.md) — tier `sonnet-high` · branch `plan/identity-model-impl-14-001` · commit `d8eef20` · merge `c9014c5`
- [x] [002-gate-holder-leave-on-profile-availability.md](./phase-14-remediation-03/002-gate-holder-leave-on-profile-availability.md) — tier `sonnet-high` · branch `plan/identity-model-impl-14-002` · commit `d977d63` · merge `2164583219c3cda8243a5dea85d12926c15d2972`
- [x] [003-scope-setup-token-on-oidc-routes.md](./phase-14-remediation-03/003-scope-setup-token-on-oidc-routes.md) — tier `sonnet-high` · branch `plan/identity-model-impl-14-003` · commit `16678b8` · merge `b2a026a1ff33abc3d82afdba7d20769b3e38a46f`
- [x] [004-serialize-profile-writers-with-endings.md](./phase-14-remediation-03/004-serialize-profile-writers-with-endings.md) — tier `sonnet-high` · branch `plan/identity-model-impl-14-004` · commit `dba63b9` · merge `e210c0902bbcbdc86c351ace00fb89f579d64bd6`
- [x] [005-fix-policy-sql-null-handling.md](./phase-14-remediation-03/005-fix-policy-sql-null-handling.md) — tier `sonnet-med` · branch `plan/identity-model-impl-14-005` · commit `7b08d96` · merge `366a81406d146a5235918daecf018cd69232307a`
- [x] [006-correct-openapi-and-architecture-doc-drift.md](./phase-14-remediation-03/006-correct-openapi-and-architecture-doc-drift.md) — tier `sonnet-low` · branch `plan/identity-model-impl-14-006` · commit `5ade1e4` · merge `81ed4fcec679f3f8baffda8d0634743827439ad0`
- [x] [007-restrict-admin-confinement-to-party-controlled-credentials.md](./phase-14-remediation-03/007-restrict-admin-confinement-to-party-controlled-credentials.md) — tier `sonnet-high` · branch `plan/identity-model-impl-14-007` · commit `843012c` · merge `d9a8e781381f51fa7cee9b012918291a323e91ba`
- [x] [008-authorized-rule-invite-time-semantics.md](./phase-14-remediation-03/008-authorized-rule-invite-time-semantics.md) — tier `sonnet-high` · branch `plan/identity-model-impl-14-008` · commit `f3014ca` · merge `488b807967f7c28d45cb66956666eede212c41b7`

### Phase 15 — Remediation Round 4

- [x] [001-order-credential-gate-locks-profile-first.md](./phase-15-remediation-04/001-order-credential-gate-locks-profile-first.md) — tier `sonnet-high` · branch `plan/identity-model-impl-15-001` · commit `dccd764` · merge `4d61b975ff55c66a26d116c6e795a66b86997918`
- [x] [002-gate-suspended-profile-leave.md](./phase-15-remediation-04/002-gate-suspended-profile-leave.md) — tier `sonnet-high` · branch `plan/identity-model-impl-15-002` · commit `804fcdc` · merge `fd62b6610d477f5914aae157050b700a5328471b`
- [x] [003-extend-last-operator-admin-guard.md](./phase-15-remediation-04/003-extend-last-operator-admin-guard.md) — tier `sonnet-med` · branch `plan/identity-model-impl-15-003` · commit `4724343` · merge `1284830528a10d3ac66a7c518df37535c2a3638e`
- [x] [004-verify-setup-token-without-cache-writes.md](./phase-15-remediation-04/004-verify-setup-token-without-cache-writes.md) — tier `sonnet-med` · branch `plan/identity-model-impl-15-004` · commit `2dc69b9` · merge `e7fcb652ea6b5cd441d6135cddfdde1fec58a7d2`
- [x] [005-purge-confirmations-on-provider-change.md](./phase-15-remediation-04/005-purge-confirmations-on-provider-change.md) — tier `sonnet-med` · branch `plan/identity-model-impl-15-005` · commit `c20e828` · merge `2fe2cf9045669e20c31cfd3f2a89b7a170dbdaf2`
- [x] [006-require-party-update-for-managed-password-authorization.md](./phase-15-remediation-04/006-require-party-update-for-managed-password-authorization.md) — tier `sonnet-med` · branch `plan/identity-model-impl-15-006` · commit `ab51ada` · merge `c6eca2494481fe3ac4d07487df50f367f31944c2`
- [x] [007-exclude-disabled-principals-from-admin-count.md](./phase-15-remediation-04/007-exclude-disabled-principals-from-admin-count.md) — tier `sonnet-med` · branch `plan/identity-model-impl-15-007` · commit `b6b6a51` · merge `27752c73fe1d0e3361d00a8691cc04c1a5bb5ee7`
- [x] [008-serialize-managed-user-provisioning-with-policy.md](./phase-15-remediation-04/008-serialize-managed-user-provisioning-with-policy.md) — tier `sonnet-med` · branch `plan/identity-model-impl-15-008` · commit `b6e9f7c` · merge `8cdf04a2d788728c3cbefc0f1f282f52d8ef9767`

### Phase 16 — Remediation Round 5

- [x] [001-strict-collaborator-policy-mismatch.md](./phase-16-remediation-05/001-strict-collaborator-policy-mismatch.md) — tier `sonnet-high` · branch `plan/identity-model-impl-16-001` · commit `64b4de5` · merge `c59b436ff64b0727e4dc388cefa64b8d08ae9da2`
- [x] [002-close-admin-disable-race-and-fail-closed-policy-lock.md](./phase-16-remediation-05/002-close-admin-disable-race-and-fail-closed-policy-lock.md) — tier `sonnet-med` · branch `plan/identity-model-impl-16-002` · commit `e69d2cd` · merge `e7fc44e5fb938373df38b6ed09a6e475a09e154a`
- [x] [003-document-lock-order-and-phase-15-contracts.md](./phase-16-remediation-05/003-document-lock-order-and-phase-15-contracts.md) — tier `sonnet-med` · branch `plan/identity-model-impl-16-003` · commit `8512aca` · merge `669aeb893894822d9112bf21ede0e2a6cf1c54ee`
- [x] [004-run-bootstrap-token-lifecycle-integration.md](./phase-16-remediation-05/004-run-bootstrap-token-lifecycle-integration.md) — tier `sonnet-med` · branch `plan/identity-model-impl-16-004` · commit `11be668` · merge `46baa372332f16d5aca4e10b26bda20c777fc19a`

### Phase 17 — Remediation Round 6

- [x] [001-last-operator-admin-disable-contract.md](./phase-17-remediation-06/001-last-operator-admin-disable-contract.md) — tier `sonnet-low` · branch `plan/identity-model-impl-17-001` · commit `7d76b18` · merge `a4ca8c626583d60fbbbf0794bd3ceaa1abe38e26`
- [x] [002-scope-policy-lock-test-seam-and-assert-invite-time.md](./phase-17-remediation-06/002-scope-policy-lock-test-seam-and-assert-invite-time.md) — tier `sonnet-med` · branch `plan/identity-model-impl-17-002` · commit `1eebe71` · merge `c0760b505052bc570a5868f0c9b3a1e64112abf0`

### Phase 18 — Remediation Round 7

- [x] [001-party-is-own-owning-party.md](./phase-18-remediation-07/001-party-is-own-owning-party.md) — tier `opus-med` · branch `plan/identity-model-impl-18-001` · commit `51d7959` · merge `9f9539e734a30ce291f225b55def83eac8b4c87f`
- [x] [002-impersonated-actor-rules-apply.md](./phase-18-remediation-07/002-impersonated-actor-rules-apply.md) — tier `opus-med` · branch `plan/identity-model-impl-18-002` · commit `d143bca` · merge `11e3902c3a82efdb59e66cf59f9fe7c3dc455fe3`
- [x] [003-credentials-refuse-impersonation.md](./phase-18-remediation-07/003-credentials-refuse-impersonation.md) — tier `sonnet-high` · branch `plan/identity-model-impl-18-003` · commit `bfd3919` · merge `d1d7eaaf80f6bf73ec97c46a4135795b0af98ab3`
- [x] [004-impersonation-renew-recheck.md](./phase-18-remediation-07/004-impersonation-renew-recheck.md) — tier `opus-med` · branch `plan/identity-model-impl-18-004` · commit `0b4d07e` · merge `9f3f777fde366ba6cbe0f6b6da7362f3c126fd47`
- [x] [005-pin-sessionless-contexts.md](./phase-18-remediation-07/005-pin-sessionless-contexts.md) — tier `sonnet-high` · branch `plan/identity-model-impl-18-005` · commit `56213b5` · merge `c3bf76474fc65f9150b8624331787abf60bbed5c`
- [x] [006-app-gate-step-up-mapping.md](./phase-18-remediation-07/006-app-gate-step-up-mapping.md) — tier `sonnet-high` · branch `plan/identity-model-impl-18-006` · commit `5c96e4c` · merge `57b94b1d6b72a828243cce230d93b9ae3f7ed278`

### Phase 19 — Remediation Round 8

- [x] [001-impersonator-profile-credential-exemption.md](./phase-19-remediation-08/001-impersonator-profile-credential-exemption.md) — tier `opus-med` · branch `plan/identity-model-impl-19-001` · commit `b721fcc` · merge `20d83de`
- [x] [002-confine-impersonator-parent-factors.md](./phase-19-remediation-08/002-confine-impersonator-parent-factors.md) — tier `opus-med` · branch `plan/identity-model-impl-19-002` · commit `042f83d` · merge `51822c3`
- [x] [003-refuse-managed-user-creation-impersonating.md](./phase-19-remediation-08/003-refuse-managed-user-creation-impersonating.md) — tier `opus-med` · branch `plan/identity-model-impl-19-003` · commit `927252d` · merge `b99e5fc`
- [x] [004-test-precision-nesting-and-static-audit.md](./phase-19-remediation-08/004-test-precision-nesting-and-static-audit.md) — tier `sonnet-high` · branch `plan/identity-model-impl-19-004` · commit `71d8c39` · merge `2b21a71`
- [x] [005-docs-impersonation-reauth-and-decisions.md](./phase-19-remediation-08/005-docs-impersonation-reauth-and-decisions.md) — tier `sonnet-high` · branch `plan/identity-model-impl-19-005` · commit `89c1c01` · merge `a45d13a`

### Phase 20 — Remediation Round 9

- [x] [001-refuse-impersonated-profile-end.md](./phase-20-remediation-09/001-refuse-impersonated-profile-end.md) — tier `sonnet-med` · branch `plan/identity-model-impl-20-001` · commit `ee6dae3` · merge `576ea98`
- [x] [002-precision-sql-comments-openapi-order-liveness-test.md](./phase-20-remediation-09/002-precision-sql-comments-openapi-order-liveness-test.md) — tier `sonnet-low` · branch `plan/identity-model-impl-20-002` · commit `bda3e54` · merge `691e7d7`

### Phase 21 — Adopt Mod-Core Identity Engine Remediation

- [x] [001-key-gates-on-system-actor-marker.md](./phase-21-adopt-core-engine-remediation/001-key-gates-on-system-actor-marker.md) — tier `opus-high` · branch `plan/identity-model-impl-21-001` · commit `f8cfc04` · merge `230a32d803cc66fec69f367a91a4f0092d1a933d`
- [x] [002-adopt-list-row-filter-hook.md](./phase-21-adopt-core-engine-remediation/002-adopt-list-row-filter-hook.md) — tier `opus-high` · branch `plan/identity-model-impl-21-002` · commit `9cd73dc` · merge `57e945943ddb5ecb003078190bb5dd6b3ea160f2`
- [x] [003-adopt-profile-lock-and-org-create-in-tx.md](./phase-21-adopt-core-engine-remediation/003-adopt-profile-lock-and-org-create-in-tx.md) — tier `sonnet-med` · branch `plan/identity-model-impl-21-003` · commit `e65c520` · merge `1a6568222ee5fcc79dd2f744f4af245ef2bbc002`
- [x] [004-align-docs-with-adopted-core-apis.md](./phase-21-adopt-core-engine-remediation/004-align-docs-with-adopted-core-apis.md) — tier `sonnet-med` · branch `plan/identity-model-impl-21-004` · commit `255e343` · merge `95ff9fa81b5850e7e328c00354e4c9c9526b7f3e`

### Phase 22 — Remediation Round 10

- [x] [001-require-sessionless-system-actor.md](./phase-22-remediation-10/001-require-sessionless-system-actor.md) — tier `sonnet-med` · branch `plan/identity-model-impl-22-001` · commit `f6e8dc9` · merge `64d83d7ec053869ebbc7b1b4e073874c2444bc9e`
- [x] [002-apply-assurance-gate-on-principal-list.md](./phase-22-remediation-10/002-apply-assurance-gate-on-principal-list.md) — tier `sonnet-med` · branch `plan/identity-model-impl-22-002` · commit `260aa65` · merge `9f70bf3d54751adbdb3d59fc1ab3bcb3ae22b0fd`
- [x] [003-dispatch-after-commit-observations.md](./phase-22-remediation-10/003-dispatch-after-commit-observations.md) — tier `sonnet-med` · branch `plan/identity-model-impl-22-003` · commit `e4bea51` · merge `68c6cb9483db169e9601347ca2b3f42fd20bc148`

### Phase 23 — Remediation Round 11

- [x] [001-fix-auth-context-session-lifecycle.md](./phase-23-remediation-11/001-fix-auth-context-session-lifecycle.md) — tier `sonnet-med` · branch `plan/identity-model-impl-23-001` · commit `18ee982` · merge `a2ebcf6b82bd0d7fda26f798e407a748e3be8075`
- [x] [002-handle-oidc-link-and-step-up-returns.md](./phase-23-remediation-11/002-handle-oidc-link-and-step-up-returns.md) — tier `sonnet-med` · branch `plan/identity-model-impl-23-002` · commit `5525f60` · merge `3e1c1cc79d0754c0b00c5b63e695ea2519dd4a1f`
- [x] [003-stop-impersonation-refresh-replay.md](./phase-23-remediation-11/003-stop-impersonation-refresh-replay.md) — tier `sonnet-med` · branch `plan/identity-model-impl-23-003` · commit `177f169` · merge `9c74ec8e3c66b57b2ac301950254b7f21ef6b0e0`
- [x] [004-persist-pending-profile-selection.md](./phase-23-remediation-11/004-persist-pending-profile-selection.md) — tier `sonnet-med` · branch `plan/identity-model-impl-23-004` · commit `9658c06` · merge `3d80ff28b19ddb17856c77bb9cc37c905723a045`
- [x] [005-route-first-verify-send-error-to-dialog.md](./phase-23-remediation-11/005-route-first-verify-send-error-to-dialog.md) — tier `sonnet-low` · branch `plan/identity-model-impl-23-005` · commit `ad47372` · merge `ac2e7c4bb25c6c6c28eece595b4d4d7a205d1e19`
- [x] [006-document-app-option-as-uuid.md](./phase-23-remediation-11/006-document-app-option-as-uuid.md) — tier `haiku-med` · branch `plan/identity-model-impl-23-006` · commit `c4a6ecf` · merge `399279c2a49331c53813b026a90d9b6f5200e838`

### Phase 24 — Remediation Round 12

- [x] [001-route-step-up-refresh-through-single-flight.md](./phase-24-remediation-12/001-route-step-up-refresh-through-single-flight.md) — tier `sonnet-med` · branch `plan/identity-model-impl-24-001` · commit `dc7c836` · merge `d87369b247d12473bfb1d37b9289942b4be4b4c9`
- [x] [002-fail-closed-impersonation-detection.md](./phase-24-remediation-12/002-fail-closed-impersonation-detection.md) — tier `sonnet-med` · branch `plan/identity-model-impl-24-002` · commit `1d6c29f` · merge `4caaf219270959cd296e9035214e47f68b06297b`
- [x] [003-revoke-admin-session-on-impersonated-logout.md](./phase-24-remediation-12/003-revoke-admin-session-on-impersonated-logout.md) — tier `sonnet-med` · branch `plan/identity-model-impl-24-003` · commit `d4a2648` · merge `7224fe14de70d598b1e20512bcd29d5a0e5f40e4`

### Phase 25 — Remediation Round 13

- [x] [001-harden-logout-while-impersonating.md](./phase-25-remediation-13/001-harden-logout-while-impersonating.md) — tier `sonnet-med` · branch `plan/identity-model-impl-25-001` · commit `89fe62a` · merge `d6af915a9e575c53dab643865899a3e3cf026100`
- [x] [002-tighten-impersonation-marker-fail-closed.md](./phase-25-remediation-13/002-tighten-impersonation-marker-fail-closed.md) — tier `sonnet-med` · branch `plan/identity-model-impl-25-002` · commit `91a3006` · merge `c316182af02cc71bf117086dee32ed946d81c047`
- [x] [003-signal-step-up-refresh-skip-while-impersonating.md](./phase-25-remediation-13/003-signal-step-up-refresh-skip-while-impersonating.md) — tier `sonnet-low` · branch `plan/identity-model-impl-25-003` · commit `9709ad7` · merge `70e85ee4a26e40a1402704c1e81e5760245a5527`

### Phase 26 — Remediation Round 14

- [x] [001-document-persisted-signing-key-risk.md](./phase-26-remediation-14/001-document-persisted-signing-key-risk.md) — tier `sonnet-low` · branch `plan/identity-model-impl-26-001` · commit `8f0c9652` · merge `0efd1dc53b60e5ca197763fb0b8053876ea6921c`
- [x] [002-alias-facade-api-key-and-profile-summary.md](./phase-26-remediation-14/002-alias-facade-api-key-and-profile-summary.md) — tier `sonnet-low` · branch `plan/identity-model-impl-26-002` · commit `961ec79a` · merge `af8fee69059be5fcba8297fe252f43e015080381`
- [x] [003-align-cors-options-and-docs.md](./phase-26-remediation-14/003-align-cors-options-and-docs.md) — tier `sonnet-med` · branch `plan/identity-model-impl-26-003` · commit `179d2ec3` · merge `b070bf1690265d433cfcb40171f56cbbd2fbde14`
- [x] [004-fix-api-layer-route-docs.md](./phase-26-remediation-14/004-fix-api-layer-route-docs.md) — tier `sonnet-low` · branch `plan/identity-model-impl-26-004` · commit `55c2b891` · merge `a20465fd4abd3968503cbb5571bbd1d81d8996c8`
- [x] [005-fix-oidc-redirect-error-code.md](./phase-26-remediation-14/005-fix-oidc-redirect-error-code.md) — tier `haiku-med` · branch `plan/identity-model-impl-26-005` · commit `8de549bf` · merge `826e63510bcd6f182b56dc4fe932d63e04c228db`
- [x] [006-document-core-field-key-env.md](./phase-26-remediation-14/006-document-core-field-key-env.md) — tier `haiku-med` · branch `plan/identity-model-impl-26-006` · commit `eb5a8a31` · merge `7627f1ebb7733fd1d6cae43e49221546c60e26da`

### Phase 27 — Remediation Round 15

- [x] [001-align-cors-setup-token-and-docs.md](./phase-27-remediation-15/001-align-cors-setup-token-and-docs.md) — tier `sonnet-med` · branch `plan/identity-model-impl-27-001` · commit `badf8c15` · merge `ce9fcdbd`
- [x] [002-mark-field-key-seed-dev-only.md](./phase-27-remediation-15/002-mark-field-key-seed-dev-only.md) — tier `sonnet-low` · branch `plan/identity-model-impl-27-002` · commit `6617e64e` · merge `07d788b5`

### Phase 28 — Credential Profile Derivation Liveness

- [x] [001-add-liveness-to-credential-profile-availability.md](./phase-28-credential-profile-liveness/001-add-liveness-to-credential-profile-availability.md) — tier `sonnet-high` · branch `plan/identity-model-impl-28-001` · commit `dbf93997` · merge `5706235d1afa1b5d0ea03d3cc5539daf59f0be10`

### Phase 29 — Remediation Round 16

- [x] [001-document-credential-profile-availability-statuses.md](./phase-29-remediation-16/001-document-credential-profile-availability-statuses.md) — tier `haiku-med` · branch `plan/identity-model-impl-29-001` · commit `d3be4994` · merge `128eadc296dfe6df13e17c6a1a6e7a1e40054d3a`
