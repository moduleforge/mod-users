# Decisions and Resolutions

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Records every planning decision mod-users' slice of the federated `identity-model-impl` plan makes
on top of the accepted ADR (mod-core `docs/architecture/identity-model.md`), the shared plan brief,
and mod-core's committed plan notes, with the reason for each, so implementers do not re-litigate
them and the doc-updates and final phases can carry the durable ones into project docs. The
contract these decisions produce is in [the mod-users API surface](./mod-users-api-surface.md);
the file-level guidance for phase decomposition is in [work packages](./work-packages.md).

mod-core's notes are binding for the mod-core surface this project consumes (`opctx.AuthContext`,
`authz.AuthorizeTypeIn`, `service.CreateEntity`, the `principals`/`profiles`/`system_actors`/
`platform_settings`/`profile_policy_confirmations` tables, the SQL functions and the SQLSTATE codes
MF003 to MF005). This note never redefines them; it names them.

## Resolutions of the ADR's "Points to settle at implementation"

mod-core adopted all four manager-recommended resolutions and rewrites the ADR accordingly. mod-users
implements them as follows.

| Point | mod-users implementation | Notes |
|---|---|---|
| (a) Outside-collaborator enforcement | The per-request availability check evaluates the policy of the **acting** profile's party only. `profile_policy_confirmations` exempts the profile from that party's `credential_rule` (`authorized`/`provider`) and never from `min_acr`. Group or grant membership never changes which policy applies. | Implemented once, in the `mod_users.profile_availability` SQL function (API surface section 3), used by login landing, profile switch, refresh, per-request liveness and the profile list. |
| (b) Personal team plans | `PersonalPartyService.EnsurePersonalParty(ctx, tx)` (API surface section 6.4): idempotently creates the creator's `natural_person` party when `users.person_id` is NULL, links it, and creates the creator's owned-scope grant, all in the caller's transaction. Refused for guests (upgrade first) and for managed users (no personal profile). | Two refinements, decisions U1 and U2 below (accepted by the manager, unchanged). mod-billing calls it, but **outside** its purchase transaction (mod-billing decision B6, recorded as U28 below). |
| (c) Several sessions in one browser | No authentication cookie anywhere in mod-users. The API accepts only `Authorization: Bearer`. Access tokens and refresh tokens are returned in JSON bodies and held per tab by the client. | The only remaining cookie is the short-lived OIDC `oidc_state` CSRF cookie, which carries no authentication. Refresh design is decision U6. |
| (d) Party-level `invite_only` | Not implemented. `party_authn_policies` has no `invite_only` column. Only `apps.invite_only` (mod-core column) gates session start, through a `login` grant on the app entity. | A party restricts its members to specific apps through group grants of `login`. |

## The open point from mod-core's planner: who owns a self-created person record

**Decision U1: a person record a user creates for themselves (at sign-up, at guest upgrade, or
through `EnsurePersonalParty`) is owned by that user (`entities.owner_id = users.entity_id`), not by
itself.** This follows the ADR's decision text ("A person record is owned by whoever recorded it ...
or the user for their own record") and its worked example (`P1` owned by `U1`); resolution (b)'s
"owner = itself" is not adopted.

Reasons:

- **Fit with the design.** The ADR's ownership rule is "owned by whoever recorded it"; a
  self-owned person is the special case MF002 used to force and the ADR deliberately removed.
- **Usability.** With the user as owner, the user's personal profile reaches its own legal record
  through the own arm (read, update of the name, grant), with no extra grant. A self-owned person
  would be outside every own arm and need a dedicated grant for routine self-edits.
- **No loss.** Party profiles and everything the person party owns are owned by the person party,
  not by the user, so the own arm still never reaches team seats or team content; those are reached
  only through the explicit grant of decision U2. mod-core's owner domain accepts both forms, so no
  mod-core change is needed.
- **Single-person self-host.** When bootstrap makes the operator's own `natural_person` the operator
  party, that person is owned by the first user by the same rule.

There is a clear best answer, so no user question is raised.

## Planning decisions

| ID | Decision | Reason |
|---|---|---|
| U2 | The grant `EnsurePersonalParty` creates is an owned-scope `manage` grant held by the creator's **personal profile** with `target_id` = the person party and **`target_type_id` NULL** (everything the person party owns), not restricted to type `profile` as resolution (b) words it. Idempotent: an existing equal grant is reused. | The person party *is* the user's own legal person; the team plan's billing account, team, seats (party profiles) and later team content are all owned by it. A profile-only grant would force mod-billing and every later module to add one owned-scope grant per type for the same person. The grant never reaches the person record itself (mod-core D7: owned-scope grants do not confer authority over the owner's own record) and is not wildcard or type-entity, so the operator-actor rule does not apply. Flagged for the manager as a deliberate widening of (b). |
| U3 | Every mod-users table moves into the `mod_users` Postgres schema and is schema-qualified; `0100_baseline.sql` is rewritten in place and `0101` to `0104` are folded into it and deleted. sqlc `rename` overrides keep unprefixed Go names (`User`, `Credential`, `Session`, ...). | The schema-ownership standard (`docs-mf-standards/architecture/schema-ownership-design.md`, already applied to `mod_users.ssh_public_keys`) puts module-owned tables in `mod_<module>`; the baseline is being rewritten anyway and no backward compatibility is required. One rule ("every mod-users table is `mod_users.*`") replaces today's mixed layout and removes the per-function `current_schema()` search-path baking. |
| U4 | The access-token carrier is **EdDSA only**. `JWT_ALG` and HS256 session tokens are removed. When `JWT_SIGNING_KEY` is unset, a persisted Ed25519 key is bootstrapped in `mod_users.auth_signing_keys` (replacing `auth_jwt_secrets`, same single-row race-free pattern). `JWT_SECRET` remains only as an optional explicit key for the OAuth state HMAC (derived from the signing key otherwise). | The ADR fixes EdDSA; offline verifiers (mod-tokens relay, managed apps) need public-key verification; keeping HS256 would keep the algorithm-confusion surface for no benefit. The persisted dev key keeps zero-config local development working. |
| U5 | Per-request **liveness is evaluated once per request by the authentication middleware**, in one SQL round trip (`mod_users.session_liveness`), which also enforces profile availability (confinement, party gate, party `min_acr`). `Authorizer` trusts the installed `AuthContext` and enforces only the operation-level assurance gate (`op.min_acr`, `op.max_auth_age`). | One query per request satisfies the ADR's "same round trip as the authorization prelude" and zero revocation latency; repeating it in every `Authorize` call multiplies the cost with no latency gain inside one request. System-actor contexts (no session, no credential) are trusted code and skip both. |
| U6 | Refresh: each tab holds its own **refresh lineage** (`mod_users.session_refresh_tokens`), rotated on every use with reuse detection; reuse of a spent token revokes the whole session. `POST /v1/session/fork` mints a new lineage for a new tab from a valid access token. The refresh request names the profile to renew, and availability is re-run. | Per-tab storage means tabs cannot share one rotating refresh token without false reuse alarms; one lineage per tab keeps rotation and reuse detection sound while the session row stays the single revocation unit. Profile is not stored on the lineage, so a switch writes nothing (ADR). |
| U7 | The OIDC callback no longer puts a token in the redirect. It redirects with a one-time `#handoff=<code>` (an `auth_challenges` row of kind `login_handoff`, 60 s TTL, single use) that the tab exchanges at `POST /v1/auth/handoff` for its tokens. | Refresh tokens must not land in browser history; the code is useless after one exchange. `auth_challenges.kind` gains `login_handoff` beyond the ADR sketch's four kinds (the ADR marks names as a sketch). |
| U8 | Step-up is "add or re-verify a factor in the current session": it recomputes `sessions.acr`, sets `sessions.auth_time = now()`, and returns a fresh access token. The HMAC `X-Step-Up-Token`, `AUTH_REQUIRE_STEP_UP` and the step-up JTI cache are deleted. Self-service credential and identifier changes always require `auth_time` within `AUTH_SELF_SERVICE_MAX_AUTH_AGE` (default 10 minutes). | ADR "MFA is factor composition"; a just-logged-in user passes without friction, so the gate can be unconditional. |
| U9 | The single setup token (existing `oidc_config.setup_token_hash` machinery) is active while `platform_settings.operator_party_id` is NULL **or** OIDC is unconfirmed. `POST /v1/bootstrap` consumes it for the operator bootstrap; confirming OIDC with it no longer clears it while bootstrap is pending. After bootstrap, OIDC configuration requires an operator admin session. | One operator secret for first run; removes the ordering trap where confirming OIDC first would destroy the token bootstrap needs. |
| U10 | Bootstrap creates, in one transaction: the first user (BYO, email identifier, password bound to it, email OTP credential, personal profile, own person record per U1), the operator party (an `organization` with a chosen `legal_form`, owner NULL; or the first user's own `natural_person` for single-person self-host), `core_claim_operator_party` (false: roll back, `409 users.already_bootstrapped`), the admin party profile (active), a wildcard `manage` grant on it, an operator `party_authn_policies` row with `credential_rule = 'authorized'`, a `credential_party_authorizations` row for the bootstrap password, and `default_profile_id` = the admin profile on that credential. | Delivers "the business login exposes the admin profile, the personal login does not" from the first run; `core_claim_operator_party` is the race-free single winner (mod-core D11), replacing the unlocked `count(*)`. |
| U11 | Registration (password or email code), OIDC `byo` JIT and guest upgrade create the user's own `natural_person` (U1) and link `users.person_id`; the user's and personal profile's display names come from it. Guests have no person. Managed users get no person by default (display falls back to the primary identifier). | The user's name has to live somewhere; the ADR's worked example creates it at sign-up; `EnsurePersonalParty` stays idempotent for users without one. |
| U12 | Verifying an email identifier automatically creates an `email_otp` credential bound to it (passwordless login keeps working); the user may revoke it. At most one active password per identifier and one unbound password per user. | Preserves today's email-code login under the "credentials, not identifiers, are evidence" rule. |
| U13 | SQLSTATE codes `MF101` (session invariant), `MF102` (credential invariant) and `MF103` (user invariant) are mod-users' own, mirroring its migration range 100 to 199; helpers live in `usersservice`. Unique violations stay `23505` with named indexes. | mod-core owns MF001 to MF005; a per-module numbering avoids collisions. Recorded for mod-core's SQLSTATE documentation. |
| U14 | The `Authorizer`'s assurance gate applies only when the context carries a `SessionID` or `CredentialID`; trusted system-actor contexts (`opctx.WithSystemActor`) and the anonymous context skip it. A `max_auth_age` failure returns the existing action-required error `users.step_up_required` (409). | `AuthContext` has no explicit system flag (mod-core 7.1); an interactive or non-interactive caller always has one of the two ids. The anonymous profile holds no grants, so the gate is moot for it. |
| U15 | The `login` app gate is evaluated by the Go `Authorizer` (`Authorize(ctx-with-candidate-profile, "login", &app.entity_id)`) as part of availability whenever the session's app has `invite_only`, rather than inside the availability SQL. | Reuses the engine (wildcard, entity, owned-scope arms, actor groups) instead of duplicating it in SQL; costs one extra query only for invite-only apps. |
| U16 | `/v1/self*`, `/v1/user-accounts*`, `/v1/assume`, `grant-admin`/`revoke-admin` and `/v1/apps/{uuid}/user-accounts*` are removed, replaced by `/v1/me*`, `/v1/session*`, `/v1/principals*`, `/v1/parties/{uuid}/*`, `/v1/impersonations*` and `/v1/bootstrap` (API surface section 7). App membership is a `login` grant through mod-authz's grants API. Platform admins are operator-party profiles plus grants. | No backward compatibility; the ADR replaces every one of these concepts. `/v1/me` matches the ADR's `GET /v1/me/profiles`. |
| U17 | The first-user hook (`users.NewFirstUserHook`, `FirstUserHookFn`, `SetFirstUserHook`, the manifest `hooks.firstUserGrant`) is deleted. | The ADR's setup-token bootstrap replaces it (closes the `count(*)` race and followup `sxjM`). |
| U18 | `user` gets an `accessible_user_ids_for_actor` stub in mod-users' baseline and `user`/`profile`/`organization` join the access-function slug list in the dev server (`authzSlugs`); `corporation` leaves it. | The principal admin list is a row-scoped list path; mod-core's generator is slug-generic and mod-core ships no `user` stub (it does not own the `users` table). |
| U19 | Display renderers for `user` (person name, else primary email, else "Guest") and `profile` ("<holder name> (Personal)" or "<holder name> @ <party name>") are registered by mod-users. | mod-core assigns the profile renderer to mod-users (it needs the holder's name). |
| U20 | Phases are ordered so mod-users' identity core lands first, party administration second, and everything that needs mod-authz's new schema (owned scope, `ownership_confers`, `impersonate`, `credentials`) third. Each service calls `authz.AuthorizeTypeIn` from the start; until the engine phase lands, mod-core's helper falls back to `AuthorizeType` (wildcard and type grants only). | Keeps phases green against mod-authz's current schema for as long as possible; the engine phase adds the owned-scope test matrix for the earlier services. Refined by U27: mod-authz's schema now lands before phase 6 closes. |

### Decisions from the cross-plan reconciliation

The ten later planning passes placed requirements on mod-users. These decisions record how the
committed plan absorbs them. U1 and U2 stay as decided.

| ID | Decision | Reason |
|---|---|---|
| U21 | **Audit op vocabulary.** Every mod-users observation uses an op from mod-audit's closed set (`create`, `update`, `delete`, `impersonate`, `login`, `grant`, `revoke`), per the event table in [API surface section 10.1](./mod-users-api-surface.md). Session start is `login` and session revocation is `revoke` (resource `session`, target the principal). Impersonation start is `impersonate`, renew is `update` and end is `revoke` (resource `impersonation`, target the impersonated profile). `assume` is not used anywhere. Unauthenticated flows install an `AuthContext` for their observations built by the section 10.1 rule. Phase 6 task 005, phase 8 task 003 and every other emitting task name their rows. | mod-audit rejects unknown ops with a CHECK. Under the `propagate` observer policy, a rejected audit write rolls back the whole operation, so an invented verb would break sign-in. mod-audit's observer also refuses a context without an acting profile (`ErrNoActor`), which every pre-authentication flow would otherwise hit. |
| U22 | **`ProfileService.AdmitInTx`** (mod-billing request, its B9) is added as binding: a trusted, in-transaction admission that reuses an active profile, activates an invited one, or creates an active one, firing lifecycle observers. It is a new phase 7 task, 007 (`trusted-profile-admission`), not folded into task 001. | Profile lifecycle belongs to mod-users' service layer, and mod-billing's fallback would bypass observers and audit. A separate task keeps the already large task 001 unchanged. 007 depends only on 001, runs in parallel with 002 to 005, and merges before task 006 so the phase gate covers it. Errors `users.profile_suspended` and `users.holder_not_admissible` are new. The second is shared with invite's MF103 mapping in task 001. |
| U23 | **`usersservice.ReadPrincipalSummaries(ctx, db usersdb.DBTX, ids)`** (mod-tokens request, its T16) is added as binding and folded into phase 6 task 007. `DirectoryService.PrincipalSummaries` delegates to it. Task 008 re-exports it. | It is the same query as the directory lookup, run on the caller's connection, so it belongs with the `DirectoryService` work. mod-tokens' mint path holds a transaction, and a pool-based read there needs a second connection per mint. |
| U24 | **`CredentialService.LinkOIDCInTx`** (app-mfmanager request, its M8) is added as binding and folded into phase 6 task 004. It becomes the `oidc` creation primitive task 006's JIT and link paths use. It is trusted (no authorization), refuses an identity already active elsewhere (`users.oidc_identity_in_use`) and never links by email. | Task 004 already owns an `oidc` creation primitive. Making that primitive the public trusted helper adds one facade method instead of a second code path, and keeps `MF102` and the active-uniqueness index as the only rules. app-mfmanager then needs no hand-written `usersdb` inserts. |
| U25 | **`mod_users.user_identifiers (user_id, kind, value_canon, verified_at, is_primary)` is a binding read surface** (mod-billing request, its B20). It is read-only for other projects. A rename or change of meaning is flagged to mod-billing and mod-tokens. | mod-billing matches invitations against verified emails in SQL, as it did against `user_accounts`. mod-tokens' T16 fallback reads the primary email. Marking the columns binding makes such a change a flagged contract change rather than a silent break. |
| U26 | **Collaborator gate wiring: hard dependency, not a later wiring task.** Phase 7 task 003 starts only after mod-authz phase 14 task 004 (`collaborator-gate-and-group-rules`) has merged. If the hook is absent at dispatch, the task halts and reports. It does not defer the wiring. Phase 7 task 006 drops its "hook not landed" fallback. | mod-authz's own execution order already puts its phase 14 before mod-users phase 7 (its H5 and interleaving step 4), so the dependency costs no schedule in the planned order. The alternative, a separate wiring task after mod-authz 14, would leave mod-users' dev server with no gate in the meantime. mod-authz fails closed without a gate, refusing every outside profile with `collaborator_gate_unavailable`. It would also leave task 006's group-membership collaborator scenarios untestable inside the phase. A silent defer is the failure mode mod-authz's coordination risk 3 names. |
| U27 | **mod-authz phase 13 (`authz-identity-schema`) merges before mod-users phase 6 closes** (mod-authz H3), in addition to mod-authz phase 12 and mod-audit phase 20. Phase 6 task docs no longer say mod-authz's migrations compose unchanged. There are three consequences before the engine phase. (1) The minimal `authz.go` port's entity arm reads only `scope = 'entity'` grants, so an owned-scope grant never acts as an entity grant on its target. (2) Phase 6 and 7 fixtures satisfy mod-authz's grant triggers: actors are profiles, and wildcard and type-entity grants go only to operator profiles. (3) Until phase 8, `AuthorizeTypeIn`-gated party administration (invite, managed users) is reachable only by operator-party administrators. Phase 7 tests use an operator-party administrator for those flows, and phase 8 task 005 re-tests them with owned-scope grants held by a non-operator party administrator. | After mod-core phase 3, the generated `accessible_*` functions read the new grant columns. Every `setup.ApplyFuncs` run against the old mod-authz schema fails, including mod-users' dev server and integration suites. mod-authz's operator-actor trigger then rejects the non-operator wildcard and type grants that U20's fallback relied on. |
| U28 | **`EnsurePersonalParty` and mod-billing's B6.** mod-billing deviates from resolution (b)'s "in the plan-creation transaction". It calls `EnsurePersonalParty` in its own committed transaction, then authorizes, then runs the purchase transaction. The production `Authorizer` reads grants through its own pool, so a grant in the caller's uncommitted transaction is invisible to it. U1 and U2 are unchanged. The binding comment on `EnsurePersonalParty` (API surface 6.3) now documents the visibility rule and the commit-first pattern. Phase 8 task 005 adds a test proving the B6 sequence through the production `Authorizer`. The open question below asks whether a committed variant should also be offered. | The method is idempotent and serialized on the user row, so committing first only leaves a person and a grant that the next attempt reuses. The deviation is correct and needs no mod-users change beyond documentation and a test. |

### Open question for the manager: a committed `EnsurePersonalParty` variant

**Question.** Should `PersonalPartyService` also offer a variant for callers that must authorize
against the new grant? A candidate is `EnsurePersonalPartyCommitted(ctx) (PersonalParty, error)`,
which opens, runs and commits its own transaction from the service's pool. mod-billing's B6
pattern would then be a mod-users API rather than consumer-side code.

**Recommended answer: no.** Document the pattern instead (done in U28 and API surface 6.3) and
prove it with a test (phase 8 task 005). Reasons:

- The only consumer has already planned B6 with its own transaction. A variant would add a
  binding method that no planned task calls.
- The variant would hide the actual constraint without removing it. The `Authorizer` interface
  (mod-core) has no transaction parameter, so any in-transaction grant is invisible to it. A
  committed variant solves this for one method, not for the next trusted grant writer, such as
  bootstrap.
- `pgx.BeginFunc` around `EnsurePersonalParty` is three lines for the caller. It keeps the caller
  in control of retries and of the context it passes.

If the manager prefers the variant, phase 8 task 005 adds it next to `EnsurePersonalParty`, with
the same errors, and the API surface lists it as binding. No other task changes. Either way, a
transaction-aware `Authorizer` stays out of scope, because it is a mod-core contract question.

## Boundary confirmation: mod-users versus mod-authz and mod-core

Confirmed against the ADR and the code.

| Component | Owner | Evidence |
|---|---|---|
| `users`, `user_identifiers`, `credential_kinds`, `credentials` and per-kind subtables, `auth_challenges`, `oidc_providers` changes, `party_authn_policies`, `credential_party_authorizations`, `sessions`, `session_factors`, refresh lineages, signing keys | mod-users | ADR data model "-- mod-users"; mod-core decisions "Components confirmed to belong to other projects". |
| Production `Authorizer`, `AuthorizeType`, `AuthorizeTypeIn` (`api/internal/authz/authz.go`), liveness, availability, landing, invitations, bootstrap, impersonation, the resolver (`api/internal/auth/resolver.go`) | mod-users | Same; `authz.go` is the only production `coreAuthz.Authorizer`. |
| `authz_operations` (`ownership_confers`, `min_acr`, `max_auth_age`, `assume` renamed `impersonate`, new `credentials`, `login` retargeted), `grants` (`scope`, `target_type_id`, `include_subtypes`), actor and member triggers on `is_profile`, the operator-actor wildcard/type-entity trigger, grant-service step-up rules, `OperationRegistry` | mod-authz (migrations 0500 to 0506, `api/authz`, `api/service`) | mod-authz repository; mod-core API surface 7.3 fixes the column names. |
| The access-function **generator** (`api/authz/setup/grant_table.go`) and stubs for mod-core types | mod-core | mod-core work packages, phase `access-function-generator`. |
| `accessible_user_ids_for_actor` stub; the slug list passed to `setup.ApplyFuncs` in mod-users' dev server | mod-users | U18. Host apps' own composition roots pass their own lists (followups filed by mod-core for app-mftodo, app-mfgit). |

**Boundary mismatches flagged (not planned twice):**

1. The shared brief places "generated `accessible_*_ids_for_actor` functions" in mod-authz. They are
   generated by mod-core's generator, stubbed by each type's owning module, and installed by the
   host composition root. mod-authz owns only the grant columns the generator reads.
2. mod-users' single-row engine needs the per-operation policy that mod-authz stores. This plan
   **requires** mod-authz to expose it on `OperationRegistry` (API surface section 9). The own arm
   itself reads `authz_operations.ownership_confers` in SQL exactly as mod-core's generator does
   (mod-core D6), so single-row and list paths agree by construction.
3. Followup `OlHx` (the anonymous actor's zero-grant rule has only a boot-time check) is best closed
   by mod-authz's actor trigger rejecting the anonymous system actor's profile as a grant actor or
   group member; requested of mod-authz rather than planned here as a cross-module trigger.
4. Step-up for granting or revoking `manage`, wildcard or owned-scope grants (ADR "Assurance") is
   enforced by mod-authz's `GrantService` from `opctx.Auth(ctx).AuthTime`; mod-users only supplies
   the `AuthContext`.

## Cross-project ordering and coordination risks

- **mod-users phase 6 cannot close before three sibling phases merge.** mod-users' dev server
  (`api/cmd/server/main.go`) imports `authz-api/service`, `audit-api/service` and
  `audit-api/httpapi`. All of them stop compiling when mod-core removes the `opctx` actor
  accessors (mod-core D8). The `AuthContext` adoption phases, mod-authz 12 and mod-audit 20, must
  therefore merge before phase 6 closes. A task may report `cmd/server` red only while they are
  pending.
- **mod-authz's schema phase (13) must merge before phase 6 closes, not only before phase 8**
  (U27, mod-authz H3). mod-users' integration tests compose mod-authz's migrations
  (`model/Makefile` `compose`). After mod-core phase 3, the generated `accessible_*` functions
  read the new grant columns, so any `setup.ApplyFuncs` run against the old schema fails. The
  engine phase additionally needs `ownership_confers`, `impersonate`, `credentials` and profiles
  admitted as actor-group members.
- **mod-authz phase 14 task 004 (the collaborator gate hook) must merge before phase 7 task 003
  starts** (U26, mod-authz H5).
- **Downstream participants wait on mod-users' new surfaces.**
  - mod-tokens phase 30 uses `ReadPrincipalSummaries` (phase 6 task 007).
  - app-mfmanager phase 80 uses `LinkOIDCInTx` (phase 6 task 004).
  - mod-billing phase 41 uses `AdmitInTx` (phase 7 task 007) and the phase 8 engine.

  The full order is in [the overview](../overview.md#execution-order).
- **Sibling builds resolve mod-core and mod-authz to their main checkouts** through
  `scripts/link-siblings.sh` symlinks (mod-core's coordination risk applies to mod-users too):
  tasks must point those links at the plan worktrees while the plan runs.
- **Downstream breakage is by design.** Removing `user_accounts`, `usersdb.UserAccount`, the
  `system_actors` query and the auth types breaks mod-billing, mod-tokens, mod-repos,
  app-mfmanager and the non-federated host apps until their own phases; the API surface's
  section 10 lists the replacement for every symbol they use today.

## Migration and impact notes owned by mod-users

Each ADR impact note with a mod-users share maps to a phase of this plan (phase slugs in
[the overview](../overview.md)).

| ADR impact note | mod-users share | Phase |
|---|---|---|
| `user_accounts` superseded by `principal`/`user`/`service_account`/`system_actor`; every credential table folds into `credentials` | `users`, identifiers, credentials and subtables, sessions; delete `user_accounts`, `auth_local`, `email_codes`, `password_resets`, `auth_oidc_identities`, `anon_tokens`, `mod_users.ssh_public_keys`, `apps_user_accounts`, `system_actors` copy, the `system_actor` type seed, `entities_no_system_actor_owner`; drop `Principal.Roles`, `UserContext.AppRoles`, `adminRole` | `users-identity-core` |
| `app_service_identity` becomes a `service_account` subtype with client credentials | API-key credential kind, `IssueAPIKeyInTx`, `POST /v1/auth/token` client credentials (endpoint in `users-identity-core`; owner and operator issuance under the `credentials` operation in `users-authorization-engine`) | `users-identity-core`, `users-authorization-engine` |
| MF002 dropped; owner-domain trigger subsumes `entities_no_system_actor_owner` | delete mod-users' guard and its displaced-schema test | `users-identity-core` |
| Nested-assume audit bug removed structurally | impersonation sessions with the no-nesting trigger; audit fields supplied through `AuthContext` | `users-identity-core` (trigger), `users-authorization-engine` (service and routes) |
| First-user bootstrap race removed | setup-token bootstrap; delete the first-user hook | `parties-profiles-and-bootstrap` |
| The 13 mod-billing/mod-tokens foreign keys re-keyed | none in mod-users beyond publishing replacements (principal, profile, directory lookups) | owned by mod-billing, mod-tokens |
| mod-authz: `assume`→`impersonate`, `login`→apps, `credentials` op, grant scope columns, `is_profile` triggers | engine consumption; SSH-key and credential management move from `assume` to `credentials` | `users-authorization-engine` |
| mod-audit: actor is the profile; add principal, impersonator, session, credential | populate every `AuthContext` field | `users-identity-core` |
| mod-repos: SSH key to principal, profile from the repository owner | `SSHKeyResolver.ResolveCredential` / `AuthContextForOwner` | `users-identity-core` (resolve), `parties-profiles-and-bootstrap` (party-aware derivation) |
| Followups `ygkL` and `rHzV` | `rHzV`'s single-row per-operation own arm | `users-authorization-engine` |

## Retained behavior and known limits

- `UNIQUE (kind, value_canon)` on identifiers keeps today's `user_accounts.email UNIQUE` behavior,
  including the squatting limit for unverified addresses; not changed by this plan.
- Rate limiting and lockout (followups `GDol`, `rMRg`) stay open and out of scope.
