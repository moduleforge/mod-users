# Mod-Users API Surface

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This note is the contract mod-users publishes to the later participants of the federated
`identity-model-impl` plan (mod-authz, mod-audit, mod-tokens, mod-billing, mod-workflows,
mod-tasks, mod-repos, app-mfmanager) and to its own implementers. It fixes the schema, SQL
functions, SQLSTATE codes, token claims, Go names and HTTP routes mod-users adds, changes or
removes. It builds on mod-core's binding note (mod-core plan worktree,
`plan/notes/mod-core-api-surface.md`): everything mod-core defines (`opctx.AuthContext`,
`authz.AuthorizeTypeIn`, `service.CreateEntity`, `principals`, `profiles`, `system_actors`,
`platform_settings`, `profile_policy_confirmations`, `apps.invite_only`, `is_profile`,
`profile_ownership_subject`, `profile_own_subject`, `core_is_operator_profile`,
`core_claim_operator_party`, `core_ensure_system_actor`, MF003 to MF005) is referenced, never
redefined. Reasons for each choice are in [decisions and resolutions](./decisions-and-resolutions.md)
(decision ids U1 to U28; U21 to U28 come from the cross-plan reconciliation with the later
participants' planning passes).

**Binding levels.** Items marked **(binding)** are consumed by other projects or by mfgen-generated
composition roots; a task that must change one records the change in its task document and flags it.
Everything else is the implementer's to refine, provided the behavior described holds.

Notation: "entity id" is `entities.id` (internal, never on the wire); "uuid" is `entities.uuid` or
the row's own `uuid` column. Every mod-users table is in the `mod_users` schema (U3).

## 1. Ownership summary

| Area | mod-users provides | Consumes from |
|---|---|---|
| Principal subtype `user` | `mod_users.users` (the `user` type is seeded by mod-core, D1) | mod-core `principals`, `profiles`, owner-domain trigger |
| Identifiers, credentials, challenges, OIDC providers | tables and services in sections 2 and 6 | mod-core `apps`, `legal_entities` |
| Sessions, tokens, liveness, landing, switching | sections 2, 3, 5, 7 | mod-core `opctx.AuthContext` |
| Party authn policy, credential authorization, confinement, collaborator confirmations | sections 2, 3, 6, 7 | mod-core `profile_policy_confirmations` |
| Bootstrap, personal party, impersonation | sections 6, 7 | mod-core `core_claim_operator_party`, `platform_settings`; mod-authz grants |
| Production `Authorizer` (`Authorize`, `AuthorizeType`, `AuthorizeTypeIn`) | section 6.2 | mod-authz `grants`, `authz_operations`, `OperationRegistry` (section 9) |

## 2. Schema (`model/migrations/sql/0100_baseline.sql`, rewritten in place)

`0101` to `0104` are folded in and deleted. Every table, function and trigger below is created in
`mod_users` and referenced schema-qualified; references to mod-core and mod-authz tables are
`public.`-qualified. Trigger functions are `SECURITY INVOKER` with
`SET search_path = pg_catalog, pg_temp` (all names qualified). Deleted outright: `user_accounts`,
`auth_local`, `email_codes`, `password_resets`, `auth_oidc_identities`, `anon_tokens`,
`apps_user_accounts`, `mod_users.ssh_public_keys`, the unqualified `system_actors` table, the
`system_actor` type seed, the `anonymous` seed, `entities_no_system_actor_owner` and its function,
`auth_jwt_secrets`, `create_email_code_with_cooldown`.

### 2.1 `mod_users.users` (binding)

```text
entity_id           BIGINT PRIMARY KEY REFERENCES public.principals(entity_id)
person_id           BIGINT NULL REFERENCES public.natural_persons(entity_id)      -- not unique
managing_party_id   BIGINT NULL REFERENCES public.legal_entities(entity_id)       -- immutable
is_guest            BOOLEAN NOT NULL DEFAULT false                                -- may only go true -> false
default_profile_id  BIGINT NULL REFERENCES public.profiles(entity_id)
default_app_id      BIGINT NULL REFERENCES public.apps(id) ON DELETE SET NULL
created_at, updated_at
CHECK (NOT is_guest OR (person_id IS NULL AND managing_party_id IS NULL))
INDEX (managing_party_id) WHERE managing_party_id IS NOT NULL
INDEX (person_id) WHERE person_id IS NOT NULL
```

DB invariants (SQLSTATE `MF103` unless stated):

1. Type check: the entity descends from `user`.
2. `managing_party_id` is immutable; `is_guest` cannot change from false to true.
3. The user entity's `owner_id` is the user itself (bring-your-own) or `managing_party_id`
   (managed) — checked on insert against the already-inserted `entities` row.
4. `default_profile_id`, when set, names a profile held by this user.
5. A trigger on `public.profiles` (owned by mod-users, `BEFORE INSERT`) rejects, for a holder that
   is a user: a personal profile for a managed user; a party profile whose party is not
   `managing_party_id` for a managed user; any party profile for a guest.
6. Deferred constraint trigger (commit time) on `users`: a bring-your-own user has exactly one
   personal profile; a managed user has none.

### 2.2 `mod_users.user_identifiers` (binding read surface)

`id BIGSERIAL PK`, `uuid UUID UNIQUE`, `user_id BIGINT NOT NULL REFERENCES mod_users.users(entity_id)`,
`kind TEXT NOT NULL CHECK (kind IN ('email','phone','username'))`, `value_canon TEXT NOT NULL`
(email: trimmed, lowercased), `verified_at TIMESTAMPTZ NULL`, `is_primary BOOLEAN NOT NULL DEFAULT false`,
`created_at`. `UNIQUE (kind, value_canon)` (index `user_identifiers_kind_value_uq`);
`UNIQUE (user_id) WHERE is_primary AND kind = 'email'`. Only users have identifiers (the foreign
key excludes service accounts and system actors).

The table name and the columns `user_id`, `kind`, `value_canon`, `verified_at` and `is_primary`,
with the meanings above, are a **binding read surface** (decision U25). mod-billing's SQL reads
them for invitation matching, trial claims and the Paddle customer email. mod-tokens' documented
fallback (its decision T16) reads the primary email from them. Other projects only read; every
write goes through mod-users' services. A rename, a type change or a change of meaning (for
example of the email canonical form) is flagged to mod-billing and mod-tokens.

### 2.3 `mod_users.credential_kinds` (seeded registry)

`kind TEXT PK`, `class TEXT CHECK IN ('interactive','non_interactive')`,
`factor TEXT CHECK IN ('knowledge','possession','federated')`, `base_acr SMALLINT CHECK 1..3`,
`phishing_resistant BOOLEAN`, `service_allowed BOOLEAN`, `amr TEXT NOT NULL`.

| kind | class | factor | base_acr | phishing_resistant | service_allowed | amr |
|---|---|---|---|---|---|---|
| `password` | interactive | knowledge | 1 | false | false | `pwd` |
| `email_otp` | interactive | possession | 1 | false | false | `otp` |
| `oidc` | interactive | federated | 1 | false | false | `fed` |
| `ssh_key` | non_interactive | possession | 1 | false | true | `swk` |
| `api_key` | non_interactive | knowledge | 1 | false | true | `swk` |
| `anon` | interactive | possession | 1 | false | false | `anon` |

Reserved, not seeded: `totp`, `passkey`, `mtls`, `federated_jwt`.

### 2.4 `mod_users.credentials` and per-kind subtables (binding for `id`/`uuid`/`principal_id`)

```text
credentials: id BIGSERIAL PK, uuid UUID UNIQUE, principal_id BIGINT NOT NULL -> public.principals,
  kind TEXT NOT NULL -> credential_kinds, label TEXT NOT NULL DEFAULT '' (<= 100 chars),
  confined_to_party_id BIGINT NULL -> public.legal_entities,
  default_profile_id BIGINT NULL -> public.profiles,
  created_by_profile_id BIGINT NULL -> public.profiles,
  created_at, last_used_at NULL, expires_at NULL, revoked_at NULL,
  revoke_reason TEXT NULL CHECK IN ('user','admin','departure','superseded','guest_upgraded','expired')
cred_password(credential_id PK, identifier_id NULL -> user_identifiers, hash TEXT)   -- argon2id
cred_email_otp(credential_id PK, identifier_id NOT NULL -> user_identifiers)
cred_oidc(credential_id PK, provider_id TEXT NOT NULL, issuer TEXT, subject TEXT, email_at_link TEXT NULL, active BOOLEAN)
cred_ssh_key(credential_id PK, key_type TEXT CHECK (existing allow-list), public_key TEXT,
  fingerprint_sha256 TEXT CHECK LIKE 'SHA256:%', active BOOLEAN)
cred_api_key(credential_id PK, client_id TEXT UNIQUE, secret_hash TEXT)
cred_anon(credential_id PK, token_hash TEXT UNIQUE, device_id TEXT)
```

`active` on `cred_oidc` and `cred_ssh_key` mirrors `credentials.revoked_at IS NULL` (maintained by
trigger) so partial unique indexes can express "active" uniqueness:
`cred_oidc_active_issuer_subject_uq (issuer, subject) WHERE active`,
`cred_ssh_key_active_fingerprint_uq (fingerprint_sha256) WHERE active`. Also
`cred_password_active_identifier_uq` (one active password per identifier) and one active unbound
password per principal; `cred_email_otp` one active per identifier.

DB invariants (`MF102`): `principal_id` and `kind` immutable; a service account holds only
`service_allowed` kinds; a system actor holds none; a guest user holds only `anon`; a non-guest user
may not gain a new `anon`; `default_profile_id` names a profile held by `principal_id`;
`confined_to_party_id` names a legal entity; `cred_*` row kind matches `credentials.kind`.
Credentials are revoked, never deleted.

### 2.5 `mod_users.auth_challenges`

`id BIGSERIAL PK`, `uuid UUID UNIQUE`,
`kind TEXT CHECK IN ('email_code','verify_identifier','password_reset','step_up','login_handoff')`,
`principal_id NULL`, `identifier_id NULL`, `credential_id NULL`, `session_id NULL -> sessions`,
`code_hash TEXT NOT NULL`, `failed_attempts INT NOT NULL DEFAULT 0`, `expires_at`, `consumed_at NULL`,
`created_at`. Function `mod_users.create_challenge_with_cooldown(p_kind, p_principal_id,
p_identifier_id, p_session_id, p_code_hash, p_expires_at, p_cooldown_secs) RETURNS SETOF auth_challenges`
keeps the advisory-lock cooldown of today's `create_email_code_with_cooldown`. `login_handoff` rows
are single-use, 60 s (U7).

### 2.6 `mod_users.oidc_config`, `mod_users.oidc_providers`

`oidc_config` unchanged in shape (singleton; setup token hash). `oidc_providers` gains
`party_id BIGINT NULL -> public.legal_entities`, `jit_mode TEXT NOT NULL DEFAULT 'byo'
CHECK IN ('none','byo','managed')`, `trust_acr BOOLEAN NOT NULL DEFAULT false`,
`CHECK (jit_mode <> 'managed' OR party_id IS NOT NULL)`. Env-only providers behave as
`party_id NULL, jit_mode 'byo'` (`'none'` when `NO_OIDC_ACCOUNTS` is set), `trust_acr false`.

### 2.7 `mod_users.party_authn_policies`

`party_id BIGINT PK -> public.legal_entities`, `credential_rule TEXT NOT NULL DEFAULT 'any'
CHECK IN ('any','authorized','provider')`, `required_oidc_provider_id TEXT NULL -> oidc_providers(id)`,
`min_acr SMALLINT NOT NULL DEFAULT 1 CHECK (min_acr BETWEEN 1 AND 3)`, `created_at`, `updated_at`,
`CHECK ((credential_rule = 'provider') = (required_oidc_provider_id IS NOT NULL))`. No row means
`any` / `min_acr 1`. No `invite_only` column (resolution (d)). Reserved columns not created.

### 2.8 `mod_users.credential_party_authorizations`

`credential_id -> credentials`, `party_id -> public.legal_entities`,
`authorized_by_profile_id NOT NULL -> public.profiles`, `authorized_at`, `PRIMARY KEY (credential_id, party_id)`.

### 2.9 `mod_users.sessions` (binding for `id`/`uuid`)

```text
id BIGSERIAL PK, uuid UUID UNIQUE, principal_id BIGINT NOT NULL -> public.principals,
app_id BIGINT NULL -> public.apps, acr SMALLINT NOT NULL CHECK 1..3, amr TEXT[] NOT NULL,
auth_time TIMESTAMPTZ NOT NULL,
impersonator_session_id BIGINT NULL -> mod_users.sessions,
impersonated_profile_id BIGINT NULL -> public.profiles,
impersonation_reason TEXT NULL,
created_at, last_seen_at, expires_at NOT NULL, revoked_at NULL,
revoke_reason TEXT NULL CHECK IN ('logout','admin','credential_revoked','principal_disabled',
  'refresh_reuse','impersonation_ended','parent_revoked','profile_ended')
CHECK ((impersonator_session_id IS NULL) = (impersonated_profile_id IS NULL)
   AND (impersonator_session_id IS NULL) = (impersonation_reason IS NULL))
```

DB invariants (`MF101`): an impersonation session's parent is not itself an impersonation (no
nesting); `impersonated_profile_id` is held by `principal_id`; `principal_id` and the impersonation
columns are immutable; a session's principal is a `user` (service accounts authenticate only
through non-interactive credentials and carry `cid`, never a session; system actors never
authenticate).

`mod_users.session_factors (session_id, credential_id, verified_at, PRIMARY KEY (session_id, credential_id))`;
the credential's principal equals the session's principal (`MF101`).

`mod_users.session_refresh_tokens (id, session_id, lineage_uuid UUID, token_hash TEXT UNIQUE,
created_at, expires_at, used_at NULL, replaced_by_id NULL)` (U6).

### 2.10 `mod_users.auth_signing_keys`

Single row (`id = 1`), PKCS#8 Ed25519 private key, `created_at`; bootstrapped when
`JWT_SIGNING_KEY` is unset (U4).

### 2.11 Access-function stub

`public.accessible_user_ids_for_actor(p_actor_entity_id BIGINT, p_op_ids INT[])` stub (U18), the
same shape as mod-core's stubs; replaced at startup by `setup.ApplyFuncs`.

## 3. SQL functions (`mod_users`, `STABLE`, invoker rights, pinned search path)

| Function | Returns | Semantics |
|---|---|---|
| `profile_availability(p_session_id BIGINT, p_profile_id BIGINT)` | `TEXT` | `'available'` or a lock reason: `not_held`, `inactive`, `principal_disabled`, `confined`, `credential_not_authorized`, `provider_required`, `acr_too_low`. Rules (ADR "Credential gating"): held by the session principal (or equals `impersonated_profile_id`), profile `active`, principal not disabled; confinement over every factor; party gate of the profile's party (skipped for personal profiles and impersonation sessions) where `profile_policy_confirmations(profile_id, party_id)` exempts only the credential rule (resolution (a)); `sessions.acr >= min_acr` always. |
| `credential_profile_availability(p_credential_id BIGINT, p_profile_id BIGINT)` | `TEXT` | Same rules with the single credential as the only factor and its kind's `base_acr`; used by client credentials and SSH. |
| `session_liveness(p_session_uuid UUID, p_principal_uuid UUID, p_profile_uuid UUID)` | one row: `session_id, principal_id, profile_id, party_id, impersonator_principal_id, app_id, acr, amr, auth_time, status` | `status` is `ok` or `session_revoked`, `session_expired`, `parent_session_ended`, `principal_mismatch`, or a `profile_availability` lock reason. Under impersonation, `impersonator_principal_id` is the parent session's own principal. One round trip per request (U5). |
| `credential_liveness(p_credential_uuid UUID, p_principal_uuid UUID, p_profile_uuid UUID)` | one row: `credential_id, principal_id, profile_id, party_id, acr, status` | For client-credentials tokens (`cid`). |
| `session_acr(p_session_id BIGINT)` | `SMALLINT` | Derived from factors: max `base_acr`; 2 when two distinct `factor` classes are present; 3 reserved for phishing-resistant plus user verification. `oidc` factors count the IdP's asserted `acr` only when `trust_acr`. |

## 4. SQLSTATE codes and named indexes (binding)

| Code | Meaning | Go helper (`usersservice`) |
|---|---|---|
| `MF101` | Session invariant (nesting, impersonated profile not held, immutable column, factor principal mismatch) | `SessionInvariantSQLState`, `IsSessionInvariantError(err) bool` |
| `MF102` | Credential invariant (kind not allowed for principal type or guest status, immutable column, default profile not held) | `CredentialInvariantSQLState`, `IsCredentialInvariantError(err) bool` |
| `MF103` | User invariant (type, owner, managed-user profile rules, guest rules, default profile) | `UserInvariantSQLState`, `IsUserInvariantError(err) bool` |

Named unique indexes mapped to `409`: `user_identifiers_kind_value_uq` (`users.identifier_taken`),
`cred_oidc_active_issuer_subject_uq` (`users.oidc_identity_in_use`),
`cred_ssh_key_active_fingerprint_uq` (`users.ssh_key_in_use`); mod-core's
`profiles_one_open_per_holder_party` (`users.profile_exists`).

## 5. Token carrier (binding for offline verifiers)

EdDSA JWT only (U4), header `alg: EdDSA`, `kid` when configured.

| Claim | Interactive (`sid`) | Client credentials (`cid`) |
|---|---|---|
| `iss` | `LOCAL_ISSUER` | same |
| `aud` | the session app's slug, else `AUTH_DEFAULT_AUDIENCE` | `AUTH_DEFAULT_AUDIENCE` or the requested app slug |
| `sub` | principal uuid (under impersonation: the target holder's) | principal uuid |
| `sid` / `cid` | session uuid | credential uuid |
| `prof` | profile uuid | profile uuid (credential `default_profile_id`, else personal profile) |
| `acr` | `"1"`, `"2"` or `"3"` | the kind's `base_acr` |
| `amr` | factor `amr` values | `["swk"]` |
| `auth_time` | `sessions.auth_time` | issue time |
| `act` | `{"sub": "<impersonator principal uuid>"}` when impersonating | absent |
| `iat`, `exp` | `exp = min(iat + AUTH_ACCESS_TOKEN_TTL (<= 1h), sessions.expires_at)` | `iat + AUTH_CLIENT_CREDENTIALS_TTL` (15 min) |

No `email`, `roles`, `sudo_user_uuid` or `is_anonymous` claims. Verification pins `EdDSA`, checks
`iss`, `exp`, and `aud` against `AUTH_ACCEPTED_AUDIENCES` (empty: the default audience plus any
registered app slug), then runs `session_liveness` or `credential_liveness`; the request's
`AuthContext` takes `acr`/`auth_time` from the session row (authoritative), not from the token.

Audience-bound tokens for offline verifiers (managed apps, relays) are separate:
`auth.IssueAudienceToken` (section 6.1).

## 6. Go packages (`github.com/moduleforge/mod-users/api/...`)

### 6.1 `auth` (public facade) (binding)

```go
// Identity is the resolved caller, stored on the request context by the middleware alongside
// opctx.AuthContext (which carries the same ids for module-agnostic code).
type Identity struct {
    PrincipalID             int64
    PrincipalUUID           uuid.UUID
    PrincipalKind           string // "user" | "service_account"
    ProfileID               int64
    ProfileUUID             uuid.UUID
    PartyID                 int64 // 0 for a personal profile
    SessionID               int64 // 0 for client-credentials calls
    SessionUUID             uuid.UUID
    CredentialID            int64 // 0 for session calls
    AppID                   int64 // 0 when the session has no app
    ACR                     int16
    AMR                     []string
    AuthTime                time.Time
    ImpersonatorPrincipalID int64 // 0 unless impersonating
    IsGuest                 bool
    EmailVerified           bool // user holds at least one verified email identifier
}
func IdentityFromContext(ctx context.Context) (*Identity, bool)

type Authenticator struct{ /* pool, verifier, config */ }
func NewAuthenticator(pool *pgxpool.Pool, verifier *Verifier, cfg config.AuthConfig) *Authenticator
func (a *Authenticator) Authenticate(r *http.Request) (*Identity, error)
// errors: ErrNoAuthHeader, ErrInvalidToken, ErrNotLive (wraps the liveness status; HTTP 401
// users.session_revoked | users.profile_unavailable with the lock reason in details)

func NewRequireAuth(a *Authenticator) func(http.Handler) http.Handler           // 401 unless live
func NewResolveOrAnonymous(a *Authenticator, anon *AnonymousActor) func(http.Handler) http.Handler
func NewRequireVerifiedEmail() func(http.Handler) http.Handler                  // 403 action users.email_unverified
func RequireOIDCConfirmed(statusFn func() config.BootState) func(http.Handler) http.Handler // unchanged

type AnonymousActor struct{ /* ... */ }
func NewAnonymousActor(ctx context.Context, pool *pgxpool.Pool) (*AnonymousActor, error)
func (a *AnonymousActor) PrincipalID() int64
func (a *AnonymousActor) ProfileID() int64   // installs AuthContext{..., Anonymous: true}
func IsAnonymousActor(ctx context.Context) bool // delegates to opctx.Auth(ctx).Anonymous

func NewLocalVerifier(ctx context.Context, lc config.LocalAuthConfig) (*Verifier, error) // EdDSA only
func NewVerifierWithOptions(ctx context.Context, opts ...VerifierOption) (*Verifier, error) // external IdPs
// kept options: WithOIDC, WithInsecureSkipTLSVerify, WithLocalIssuer, WithEd25519PublicKey(s)
// removed: NewVerifier (5-arg HS256 form), WithHS256Secret

func NewSigner(lc config.LocalAuthConfig) (*Signer, error)
func IssueAudienceToken(s *Signer, issuer string, principalUUID uuid.UUID, audience string,
    ttl time.Duration, extra map[string]any) (string, error)
func HashPassword(plain string) (string, error) // unchanged
```

Removed from the facade: `UserResolver`, `NewUserResolver`, `ClaimMapper`, `NewClaimMapper`,
`MapperOptions`, `Principal`, `UserContext`, `RequireAuth(verifier, mapper, resolver)`,
`ResolveActorOrAnonymous`, `NewResolveActorOrAnonymous`, `StepUpKey`,
`NewStepUpConsumedCache`, `IssueLocalJWT`. The OIDC claim mappers remain internal to the OAuth
orchestrator (they map external id_token claims; they no longer touch local tokens).

### 6.2 `localAuthz` (binding)

`localAuthz.New(authzQ authzdb.Querier, opReg *authzapi.OperationRegistry, pool *pgxpool.Pool) *Authorizer`
(unchanged signature). `*Authorizer` implements `coreAuthz.Authorizer`, `coreAuthz.TypeAuthorizer`
and `coreAuthz.OwnerScopedTypeAuthorizer`. Semantics after the engine phase:

1. Actor = `opctx.ProfileID`; none: `ErrUnauthenticated`.
2. Assurance gate when `SessionID != 0 || CredentialID != 0` (U14): `ACR >= op.MinACR`, and
   `now - AuthTime <= op.MaxAuthAge` when set (else action-required `users.step_up_required`, returned as the shared `apiresp` action-required error added in mod-core phase 1 (followup `Uure`); wrap or construct that type, never a mod-users-local rendering).
3. Wildcard arm: actor chain holds a grant with `scope = 'entity' AND target_id IS NULL`.
4. Entity and type-entity arm (instance semantics unchanged), `scope = 'entity'` only.
5. Owned-scope arm: `scope = 'owned' AND target_id = target.owner_id` and type match
   (`target_type_id` NULL, equal, or `include_subtypes` with descent), held by the actor chain.
6. Own arm: `target.owner_id = public.profile_own_subject(actor)` and
   `EXISTS (SELECT 1 FROM public.authz_operations o WHERE o.id = ANY(op_ids) AND o.ownership_confers)`
   — the same predicate as mod-core's generated functions (parity by construction).

`AuthorizeTypeIn(ctx, op, typeID, ownerID)`: prelude; wildcard; type-entity grant on the exact
type; owned-scope grant with `target_id = ownerID` and the type match; own arm when
`ownerID = profile_own_subject(actor)` and `ownership_confers`.

Amendment (2026-10-26, decision C, finding `RU9G`; amends U5): the prelude (items 1 and 2) also
enforces the owning party's authentication policy (`credential_rule`, `min_acr`) per request when the
acting profile is outside the target's owning party, for example through
`mod_users.session_meets_party_policy(session, party)` (one extra query per cross-party
`Authorize`). The SSH personal-profile fallback is gated the same way. mod-authz is unchanged.

### 6.3 `usersservice` (public facade over `internal/service`)

Constructors take `(pool *pgxpool.Pool, q usersdb.Querier, coreQ coredb.Querier, az coreAuthz.Authorizer,
obs *observer.ObserverGroup, ...)` in the existing style; exact parameter lists are the implementer's
except where marked binding. Services: `UserService`, `CredentialService`, `SessionService`,
`ProfileService`, `PartyPolicyService`, `PersonalPartyService`, `ImpersonationService`,
`BootstrapService`, `DirectoryService`, `SSHKeyResolver`, `CollaboratorGate`, plus the
package-level `ReadPrincipalSummaries`.

Binding methods and types:

```go
// Credentials (app-mfmanager provisioning, operator tooling)
type IssueAPIKeyInput struct { PrincipalID int64; Label string; DefaultProfileID *int64; ExpiresAt *time.Time }
type IssuedAPIKey struct { CredentialID int64; CredentialUUID uuid.UUID; ClientID string; ClientSecret string } // secret shown once
func (s *CredentialService) IssueAPIKeyInTx(ctx context.Context, tx pgx.Tx, in IssueAPIKeyInput) (IssuedAPIKey, error)
// Authorizes "credentials" on the principal entity; the principal must be a service account.
func (s *CredentialService) RevokeInTx(ctx context.Context, tx pgx.Tx, credentialID int64, reason string) error
// Revokes every session holding the credential as a factor (revoke_reason 'credential_revoked').

// Trusted OIDC link (app-mfmanager managed-app owner provisioning, decision U24; also the oidc
// creation primitive the resolver's JIT and link paths use).
type LinkOIDCInput struct {
    PrincipalID       int64
    ProviderID        string // cred_oidc.provider_id (a configured or env provider id)
    Issuer            string
    Subject           string
    EmailAtLink       string // "" stores NULL
    Label             string
    ConfinedToPartyID *int64
}
type LinkedCredential struct { CredentialID int64; CredentialUUID uuid.UUID }
func (s *CredentialService) LinkOIDCInTx(ctx context.Context, tx pgx.Tx, in LinkOIDCInput) (LinkedCredential, error)
// No authorization: the caller authorizes. Creates an active `oidc` credential, so the
// (issuer, subject) pair logs the principal in on the known-identity path; it never links by
// email. Errors: ErrOIDCIdentityInUse (409 users.oidc_identity_in_use) when an active credential
// already holds (issuer, subject). The principal must be a non-guest user; an MF102 refusal
// (guest, service account, system actor) is a caller bug and surfaces as an internal error, as
// for every unmapped invariant. Writes a `credential` `create` observation, so ctx must carry an
// AuthContext with an acting profile.

// Profiles (mod-billing seats)
type InviteInput struct { PartyID int64; HolderUserID int64; HolderEmail string; ConfirmPolicyMismatch bool } // one of HolderUserID/HolderEmail
type Profile struct { ID int64; UUID uuid.UUID; HolderPrincipalID int64; PartyID int64; Status string; Source string }
func (s *ProfileService) InviteInTx(ctx context.Context, tx pgx.Tx, in InviteInput) (Profile, error)
// AuthorizeTypeIn(create, profile, party); 409 users.policy_mismatch_confirmation_required when the
// holder's credentials cannot meet the party policy and ConfirmPolicyMismatch is false.
// Amended 2026-10-26: credential_rule 'authorized' counts as met at invite time when the holder has any
// usable interactive credential (authorization follows acceptance; members stay locked until a
// credential is authorized). ConfirmPolicyMismatch=true requires `update` on the party (else 403).
func (s *ProfileService) EndInTx(ctx context.Context, tx pgx.Tx, profileID int64, reason string) error
// Departure cleanup: deletes the holder's credential authorizations for the party, revokes
// credentials confined to it, disables a managed user whose last profile ended.
type ProfileStatusChange struct { ProfileID, HolderPrincipalID, PartyID int64; From, To string }
type ProfileLifecycleObserver interface {
    ProfileStatusChanged(ctx context.Context, tx pgx.Tx, ch ProfileStatusChange) error // in-transaction
}
func (s *ProfileService) AddLifecycleObserver(o ProfileLifecycleObserver)
// Invite and admission refusals for a guest, a managed user of another party or a non-user holder
// (the MF103 profiles trigger, checked in Go first): ErrGuestMustUpgrade (409
// users.guest_upgrade_required) and ErrHolderNotAdmissible (409 users.holder_not_admissible).

// Trusted admission into a party (mod-billing seats S3 and S7, decision U22; phase 7 task 007)
type AdmitInput struct {
    PartyID            int64
    HolderUserID       int64
    InvitedByProfileID int64 // 0 when none
}
func (s *ProfileService) AdmitInTx(ctx context.Context, tx pgx.Tx, in AdmitInput) (Profile, error)
// Returns the holder's active profile in the party: an active one as is (no write), an invited
// one made active, a missing one (none or only ended) created active (source 'manual', owner =
// the party, invited_by_profile_id set, created_by_profile_id = the acting profile). No
// authorization (the caller has the holder's and the party's consent) and no policy-mismatch
// check (an unqualified holder's profile is simply locked by profile_availability). Lifecycle
// observers and `profile` observations fire for every status change. Serialized per holder.
// Errors: ErrProfileSuspended (409 users.profile_suspended), ErrGuestMustUpgrade,
// ErrHolderNotAdmissible.

// Personal team plans (resolution (b), U1, U2)
type PersonalParty struct { PartyID int64; PartyUUID uuid.UUID; GrantID int64; Created bool }
func (s *PersonalPartyService) EnsurePersonalParty(ctx context.Context, tx pgx.Tx) (PersonalParty, error)
// Acting profile must be a user's personal profile. Errors: ErrGuestMustUpgrade (409
// users.guest_upgrade_required), ErrPersonalProfileRequired (403), ErrPersonalPartyNotOwned
// (409 users.person_not_owned: users.person_id links a person the user does not own).
// The grant it creates is invisible to the production Authorizer until tx commits (the
// Authorizer reads through its own pool). A caller that must authorize against that grant
// commits this call's transaction first; the call is idempotent, so a later failure leaves
// only a person and a grant that the next attempt reuses (mod-billing B6, decision U28).

// Batch directory lookups (resolves followup bHKv). No authorization: callers authorize first.
type PrincipalSummary struct { PrincipalID int64; PrincipalUUID uuid.UUID; Kind string; DisplayName string; PrimaryEmail string; Disabled bool; IsGuest bool }
type ProfileSummary struct { ProfileID int64; ProfileUUID uuid.UUID; HolderPrincipalID int64; HolderPrincipalUUID uuid.UUID;
    PartyID int64; PartyUUID uuid.UUID; Status string; DisplayName string; HolderDisplayName string; HolderPrimaryEmail string }
func (d *DirectoryService) PrincipalSummaries(ctx context.Context, principalIDs []int64) (map[int64]PrincipalSummary, error)
func (d *DirectoryService) ProfileSummaries(ctx context.Context, profileIDs []int64) (map[int64]ProfileSummary, error)
// Transaction-scoped form (mod-tokens mint path, decision U23): a package-level function in
// usersservice that runs on the caller's connection or transaction (usersdb.DBTX), so a caller
// holding a transaction needs no second pool connection. DirectoryService.PrincipalSummaries
// delegates to it. Same semantics, no authorization; unknown ids are absent from the map.
func ReadPrincipalSummaries(ctx context.Context, db usersdb.DBTX, principalIDs []int64) (map[int64]PrincipalSummary, error)

// SSH (mod-repos). Read-only: no writes, no cache, no key-material logging.
type SSHCredential struct { CredentialID int64; CredentialUUID uuid.UUID; PrincipalID int64; PrincipalUUID uuid.UUID; ConfinedToPartyID int64 }
func NewSSHKeyResolver(q usersdb.Querier, coreQ coredb.Querier) *SSHKeyResolver
func (r *SSHKeyResolver) ResolveCredential(ctx context.Context, key gossh.PublicKey) (SSHCredential, error) // ErrUnknownSSHKey
func (r *SSHKeyResolver) AuthContextForOwner(ctx context.Context, cred SSHCredential, ownerID *int64) (opctx.AuthContext, error)
// Profile derivation: owner is a party in which the principal holds an active profile -> that
// profile if credential_profile_availability is 'available', else ErrSSHKeyNotAuthorized
// (carries the party uuid: "key not authorized for X"); otherwise the personal profile, unless
// the credential is confined (then ErrSSHKeyNotAuthorized). AuthContext: PrincipalID, ProfileID,
// CredentialID, ACR = base_acr, AuthTime = now.

// Outside-collaborator gate injected into mod-authz (section 9)
type CollaboratorGate struct{ /* ... */ }
func (g *CollaboratorGate) CheckOutsideCollaborator(ctx context.Context, tx pgx.Tx, owningPartyID, memberProfileID int64, confirmed bool) error

// SQLSTATE helpers: section 4.
```

`ResolveActor`, `UserAccountService`, `NewUserAccountService`, `SSHKeyService`, `SSHKey` are
removed.

### 6.4 `handlers` and `handlers/authhandlers` (public facades) (binding names for mfgen)

Every constructor and route-registration function referenced by `moduleforge.module.yaml` is
re-exported here (AGENTS.md convention). New handlers: `MeHandler`, `SessionHandler`,
`PrincipalsHandler`, `PartiesHandler`, `ImpersonationHandler`, `BootstrapHandler`; kept:
`OIDCConfigHandler`, `ProvidersHandler`, `authhandlers.Handler`, `authhandlers.OIDCHandler`.
Registration functions: `RegisterMeRoutes`, `RegisterSessionRoutes`, `RegisterPrincipalRoutes`,
`RegisterPartyRoutes`, `RegisterImpersonationRoutes`, `RegisterBootstrapRoutes`,
`RegisterOIDCConfigRoutes`, `authhandlers.RegisterRoutes`. Removed: `UserAccountsHandler`,
`AssumeHandler`, `AppsHandler`, `GrantAdminFn`, `SelfHandler`, `IdentitiesHandler`,
`SSHKeysHandler` and their register functions; `users.NewFirstUserHook`; `authhandlers.FirstUserHookFn`.

### 6.5 `model/db` (`usersdb`) (binding subset)

sqlc renames keep unprefixed names (U3). Binding for consumers: `usersdb.User` (row of
`mod_users.users`), `GetUserByEntityID`, `GetSessionByUUID`, `GetCredentialByUUID`. Removed:
`UserAccount` and every `*UserAccount*` query, `GetSystemActorBySlug` and
`GetSystemActorBySlugRow` (use mod-core's `coredb.GetSystemActorBySlug`, which also returns the
personal profile id), the SSH, anon-token, email-code, password-reset, OIDC-identity and
apps-membership queries.

### 6.6 Test helpers

mod-users tests stop using `ordertest.Actor`, `ordertest.Sudo` and `ordertest.SudoOnly` (deleted by
mod-core, phase 1 task 002) and use the `AuthContext`-based identities mod-core's task report names
(planned as a profile identity, an impersonating identity, and an anonymous `AuthContext` identity);
`ordertest.Anonymous` keeps meaning "no `AuthContext`". `api/internal/authz/export_test.go` stubs
gain an owned-scope stub alongside the existing wildcard, grant-or-own and type-grant stubs.

## 7. HTTP API

All authenticated routes require `Authorization: Bearer`; no route reads an authentication cookie.
Error order unchanged (401, 400, 403, 404/409). Token response shape (binding for GUIs):

```json
{
  "access_token": "<jwt>", "token_type": "Bearer", "expires_in": 3600,
  "refresh_token": "<opaque, per tab>",
  "session": {"uuid": "...", "acr": 1, "amr": ["pwd"], "auth_time": "RFC3339", "impersonating": false},
  "profile": ProfileView,
  "selection_required": false,
  "profiles": [ProfileView]
}
ProfileView = {"uuid", "kind": "personal"|"party", "party": {"uuid","kind","display_name"}|null,
               "status", "display_name", "available": bool,
               "lock": {"reason": "<lock reason>", "remedy": {"kind": "sign_in_with_provider", "provider_id": "..."}|...}|null}
```

When the landing chain (credential `default_profile_id`, `users.default_profile_id`, personal
profile, only available profile) yields no profile, `access_token` is null and
`selection_required` is true; the client calls the refresh grant with a chosen `profile_uuid`.

| Group | Routes |
|---|---|
| `/v1/auth` (public, behind `requireOIDCConfirmed`) | `POST /register`, `POST /login` (`email`, `password`, optional `app`, `profile_uuid`), `POST /anonymous`, `POST /email-code/request`, `POST /email-code/verify`, `POST /password-reset/request`, `POST /password-reset/confirm`, `POST /token` (`grant_type` `refresh_token` with `refresh_token` and `profile_uuid`; or `client_credentials` with `client_id`, `client_secret`, optional `profile_uuid`), `POST /handoff` (`code`), `GET /providers`, `GET /oidc/{provider}/start`, `GET /oidc/{provider}/callback` (redirects with `#handoff=` or `#error=`) |
| `/v1/session` | `GET /`, `POST /switch` (`profile_uuid`; new access token, no session write), `POST /fork` (new refresh lineage), `POST /step-up/password`, `POST /step-up/email-code/request`, `POST /step-up/email-code/verify`, `POST /step-up/oidc/{provider}/start` (returns `authorize_url`), `POST /logout` |
| `/v1/me` | `GET`, `PUT` (`given_name`, `family_name`, `default_profile_uuid`, `default_app_uuid`); `GET /profiles`; `POST /profiles/{uuid}/accept`, `/decline`, `/leave`; `GET/POST /identifiers`, `POST /identifiers/{uuid}/verify`, `POST /identifiers/{uuid}/verification`, `PUT /identifiers/{uuid}/primary`, `DELETE /identifiers/{uuid}`; `GET /credentials`, `POST /credentials/password`, `POST /credentials/ssh-keys`, `POST /credentials/oidc/{provider}/start`, `PATCH /credentials/{uuid}` (`label`, `default_profile_uuid`), `DELETE /credentials/{uuid}` (409 `users.last_credential` for the last interactive credential); `GET /sessions`, `DELETE /sessions/{uuid}`; `POST /upgrade` (guest). Self-service writes require `auth_time` within `AUTH_SELF_SERVICE_MAX_AUTH_AGE` (U8) and authorize on `session.principal = target`, not grants. |
| `/v1/principals` | `GET /` (`kind`, paging; list op through `accessible_user_ids_for_actor` / `accessible_service_account_ids_for_actor`), `GET /{uuid}`, `POST /{uuid}/disable` (`update` + step-up; revokes sessions), `POST /{uuid}/enable`, `GET /{uuid}/credentials`, `POST /{uuid}/credentials/api-keys`, `POST /{uuid}/credentials/ssh-keys`, `DELETE /{uuid}/credentials/{credential_uuid}` (credential routes: `credentials` operation) |
| `/v1/parties/{party_uuid}` | `GET /profiles`, `POST /profiles` (invite: `holder_email` or `holder_user_uuid`, `confirm_policy_mismatch`), `POST /managed-users`, `GET/PUT /authn-policy`, `GET /credential-authorizations`, `PUT/DELETE /credential-authorizations/{credential_uuid}`, `PUT/DELETE /confinements/{credential_uuid}` |
| `/v1/profiles` | `PATCH /{uuid}` (`status`: `active` / `suspended` / `ended`, `end_reason`; `update` on the profile + step-up) |
| `/v1/impersonations` | `POST /` (`profile_uuid`, `reason`; `impersonate` on the target profile, fresh `auth_time`; token response without refresh token, `expires_in <= AUTH_IMPERSONATION_TTL`), `POST /current/renew` (up to `AUTH_IMPERSONATION_MAX` from start), `DELETE /current` |
| `/v1/bootstrap` (public) | `GET /status` (`bootstrapped`, `oidc_confirmed`), `POST /` (`X-Setup-Token`; body `operator` = `{kind: "organization", legal_name, legal_form, jurisdiction?}` or `{kind: "natural_person"}`, `admin` = `{email, password, given_name, family_name}`; 201 token response landing on the admin profile; 409 `users.already_bootstrapped`) |
| `/v1/oidc-config` | unchanged routes; providers gain `party_uuid`, `jit_mode`, `trust_acr`; the ambient admin check is an operator profile with wildcard `manage`; the setup token follows U9 |

Amendment (2026-10-26, decision A): `PUT /confinements/{credential_uuid}` through the administrator door confines only party-controlled credentials (managed-user credentials and OIDC credentials from a party-bound provider) and needs an `active` held profile; `DELETE` through the holder door is refused for party-controlled credentials. Changing `credential_rule` through `PUT /authn-policy` clears the party's policy confirmations.

Removed routes: `/v1/self`, `/v1/self/identities*`, `/v1/self/credential*`, `/v1/self/ssh-keys*`,
`/v1/user-accounts*`, `/v1/assume`, `/v1/apps/{uuid}/user-accounts*`, `/v1/anonymous-demo`.

New error and action codes: `users.invalid_credentials` (401), `users.session_revoked` (401),
`users.profile_unavailable` (401, lock reason in details), `users.refresh_reuse` (401),
`users.step_up_required` (409 action; built on the shared mod-core `apiresp` action-required error, so `WriteError` renders it uniformly, including party `min_acr` refusals and impersonation/credentials `max_auth_age` refusals), `users.email_unverified` (403 action, existing),
`users.app_login_not_granted` (403), `users.byo_identity_disabled` (403),
`users.guest_upgrade_required` (409), `users.person_not_owned` (409),
`users.holder_not_admissible` (409), `users.profile_suspended` (409, `AdmitInTx`),
`users.policy_mismatch_confirmation_required` (409, mismatch details),
`users.impersonation_not_allowed` (403, including nesting), `users.already_bootstrapped` (409),
`users.setup_token_invalid` (401), `users.last_credential` (409), plus the index-mapped 409s in
section 4. OIDC redirect error codes: `account_exists_sign_in_and_link`, `no_account`,
`authentication_failed`, `missing_email`.

## 8. Configuration

Removed: `JWT_ALG`, `AUTH_REQUIRE_STEP_UP`, the admin-role claim setting. Kept: `JWT_SIGNING_KEY`
(optional; persisted key bootstrapped when unset), `JWT_SIGNING_KID`, `JWT_VERIFY_PUBLIC_KEYS`,
`JWT_SECRET` (optional OAuth-state key only), `LOCAL_ISSUER`, `NO_OIDC_ACCOUNTS`, OIDC provider
variables, `USERS_COOKIE_PATH` (the `oidc_state` cookie only). Added, with defaults:
`AUTH_ACCESS_TOKEN_TTL` (1h, maximum 1h), `AUTH_SESSION_MAX_AGE` (720h),
`AUTH_SESSION_IDLE_TIMEOUT` (168h; refresh lineage expiry), `AUTH_CLIENT_CREDENTIALS_TTL` (15m),
`AUTH_IMPERSONATION_TTL` (30m), `AUTH_IMPERSONATION_MAX` (60m),
`AUTH_SELF_SERVICE_MAX_AUTH_AGE` (10m), `AUTH_DEFAULT_AUDIENCE`, `AUTH_ACCEPTED_AUDIENCES`.

## 9. Required from mod-authz (to be planned by mod-authz; names fixed here or by mod-core)

1. `authz_operations`: `ownership_confers BOOLEAN NOT NULL` (mod-core 7.3), `min_acr SMALLINT NOT
   NULL DEFAULT 1 CHECK (min_acr BETWEEN 1 AND 3)`, `max_auth_age INTERVAL NULL`. Operation 8
   renamed `impersonate`; operation 9 `login` described as "start a session for an app"; new
   `credentials` (next free id) implied by `manage`. `ownership_confers` true for `read`, `sread`,
   `list`, `update`, `supdate`, `delete`, `create`, `grant`, `revoke`, `connect`, `credentials`;
   false for `manage`, `impersonate`, `login` (mod-core's implication invariant). `max_auth_age`
   10 minutes on `impersonate` and `credentials`.
2. `OperationRegistry.Policy(slug string) (OperationPolicy, error)` with
   `type OperationPolicy struct { ID int32; OwnershipConfers bool; MinACR int16; MaxAuthAge time.Duration }`
   (zero `MaxAuthAge` = none); `SeedOperation` gains the same three fields for
   `NewRegistryFromSeed`.
3. `grants` columns exactly as mod-core 7.3 (`scope`, `target_type_id`, `include_subtypes`), and the
   generated `authzdb.CreateGrantParams` carrying them, so mod-users' trusted flows (bootstrap
   wildcard grant, `EnsurePersonalParty` owned-scope grant) insert in their own transaction.
4. Actor and member triggers on `is_profile` (plus groups); the operator-actor wildcard/type-entity
   trigger; rejection of the anonymous system actor's profile as a grant actor or group member
   (closes mod-users followup `OlHx`).
5. Step-up (`AuthTime` freshness) in `GrantService` for granting or revoking `manage`, wildcard or
   owned-scope grants.
6. An injectable `CollaboratorGate`-shaped hook consulted when an actor-group member or grant actor
   is a profile outside the owning party, with a `confirm_policy_mismatch` request flag; mod-users'
   `usersservice.CollaboratorGate` implements it and the composition root wires it.
7. The `AuthContext` adoption in `api/service` must merge before mod-users' first phase closes
   (mod-users' dev server imports it).
8. Items 1 to 4 (mod-authz phase 13, `authz-identity-schema`) must **also** merge before mod-users
   phase 6 closes, not only before phase 8 (mod-authz constraint H3, decision U27). After mod-core
   phase 3, the generated `accessible_*` functions read `grants.scope`, `target_type_id`,
   `include_subtypes` and `authz_operations.ownership_confers`. Every `setup.ApplyFuncs` run
   against the old mod-authz schema fails, including mod-users' dev server, its principal-list
   path and its integration suites.
9. Item 6 (mod-authz phase 14 task 004) must merge before mod-users phase 7 task 003 starts
   (mod-authz constraint H5). That task halts rather than defers when the hook is absent
   (decision U26).

## 10. Notes for specific consumers

- **mod-audit:** every request `AuthContext` from mod-users carries `ProfileID` (actor),
  `PrincipalID`, `ImpersonatorPrincipalID`, `SessionID` (`mod_users.sessions.id`) and
  `CredentialID` (`mod_users.credentials.id`), never both `SessionID` and `CredentialID` (mod-audit
  CHECK `audit_log_session_or_credential`); recommend no foreign keys from `audit_log` to
  mod-users tables (append-only history). Observer resources emitted by mod-users: `user`,
  `user_identifier`, `credential`, `session`, `profile`, `party_authn_policy`,
  `credential_party_authorization`, `impersonation`, `bootstrap`, and `grant` for mod-users'
  trusted grant inserts. Ops are only those of section 10.1. mod-audit phase 20
  (`audit-auth-context-adoption`) merges before mod-users phase 6 closes (mod-audit constraint
  E2).
- **mod-tokens:** `usersdb.UserAccount` lookups become principal ids (`public.principals`) plus
  `DirectoryService.PrincipalSummaries` for display. On a path that already holds a transaction
  (the mint path), use `usersservice.ReadPrincipalSummaries(ctx, tx, ids)` on that transaction
  (decision U23, delivered in phase 6 task 007). Refuse refresh when
  `public.principals.disabled_at` is set. The device approval route sits behind `NewRequireAuth`
  and reads `opctx.Auth(ctx).PrincipalID`.
- **mod-billing:** system actor `billing.paddle_sync` via mod-core `core_ensure_system_actor`
  (closes mod-users followup `WZTj`). Seats hold profile ids. The holder is admitted with
  `ProfileService.AdmitInTx` (decision U22, phase 7 task 007), an invitation is authorized with
  `AuthorizeTypeIn(create, profile, party)` on mod-billing's side, and seats are released through
  a `ProfileLifecycleObserver`. Personal team plans call `PersonalPartyService.EnsurePersonalParty`
  in **their own committed transaction before authorizing** (mod-billing decision B6, decision
  U28), not in the plan-creation transaction. Holder display goes through
  `DirectoryService.ProfileSummaries`. mod-billing reads `mod_users.users (entity_id, person_id,
  is_guest)` and the section 2.2 read surface of `mod_users.user_identifiers` directly.
- **mod-workflows, mod-tasks:** no direct mod-users API; actors come from `opctx.AuthContext`;
  test seeds create users through mod-users' services or SQL that satisfies section 2's
  invariants (a bring-your-own user needs its personal profile in the same transaction).
- **mod-repos:** replace `ResolveActor` with `ResolveCredential` plus `AuthContextForOwner`
  (repository owner id), and install the returned `AuthContext`; anonymous HTTP reads keep using
  `NewResolveOrAnonymous`.
- **app-mfmanager:** managed-app owner provisioning pre-creates the owner's `cred_oidc` link with
  `CredentialService.LinkOIDCInTx` (decision U24, delivered in phase 6 task 004) instead of
  generated `usersdb` inserts. `app_service_identity` provisioning creates the service account with mod-core's
  `CreateServiceAccountInTx` and its API key with `CredentialService.IssueAPIKeyInTx`; runtime login
  is `POST /v1/auth/token` (`client_credentials`) with `MFSVC_ACCOUNT_EMAIL` replaced by a client
  id; managed-app owner tokens use `auth.IssueAudienceToken` with `sub` = principal uuid;
  `auth.UserResolver`, `auth.Principal`, `auth.UserContext` and `usersdb.UserAccount` uses move to
  `auth.Identity`, `auth.Authenticator` and principal ids.
- **Non-federated host apps (app-mftodo, app-mfgit, app-mfdemo) and mfgen:** the manifest service,
  middleware and route names in section 11 replace today's; followups are filed.

### 10.1 Audit op vocabulary per event (binding)

mod-audit's `audit_log.op` accepts only `create`, `update`, `delete`, `impersonate`, `login`,
`grant` and `revoke` (mod-audit API surface section 2.2). `assume` is gone with no alias. Any
other verb fails the in-transaction `Observe` call, and under the `propagate` policy the whole
operation rolls back. Every mod-users observation uses the op this table names (decision U21).
A task that needs an event not listed here picks the closest row's op, and never a new verb. It
records the choice in its task document.

| Event | op | resource | target | Task |
|---|---|---|---|---|
| User created (registration, guest sign-in, OIDC JIT, managed user, bootstrap) | `create` | `user` | the user | 6/004, 6/006, 7/001, 7/004 |
| User updated (name, defaults, guest upgrade, disable, enable) | `update` | `user` | the user | 6/007, 7/001 |
| Identifier added / verified or made primary / removed | `create` / `update` / `delete` | `user_identifier` | the user | 6/004, 6/007 |
| Credential created (password, email OTP, SSH key, API key, OIDC link incl. `LinkOIDCInTx`, `anon`) | `create` | `credential` | the principal | 6/004, 6/006, 6/007, 8/004 |
| Credential label, default profile or confinement changed | `update` | `credential` | the principal | 6/007, 7/002 |
| Credential revoked (any `revoke_reason`) | `revoke` | `credential` | the principal | 6/004, 7/001, 8/004 |
| Sign-in creating a session (registration, password, email code, anonymous, OIDC callback, bootstrap) | `login` | `session` | the principal | 6/005, 6/006, 7/004 |
| Step-up adding or re-verifying a factor | `update` | `session` | the principal | 6/005, 6/006 |
| Sign-out or session revocation (logout, refresh reuse, credential revoked, principal disabled, admin) | `revoke` | `session` | the principal | 6/005, 6/007 |
| Refresh rotation, fork, profile switch | none | | | 6/005 |
| Profile created (invite, managed user, bootstrap admin, `AdmitInTx`) | `create` | `profile` | the profile | 7/001, 7/004, 7/007 |
| Profile status changed (accept, decline, leave, suspend, resume, end, admission) | `update` | `profile` | the profile | 7/001, 7/007 |
| Party authn policy created / changed / removed | `create` / `update` / `delete` | `party_authn_policy` | the party | 7/002 |
| Credential authorized for / removed from a party | `create` / `delete` | `credential_party_authorization` | the party | 7/002 |
| Bootstrap completed | `create` | `bootstrap` | the operator party | 7/004 |
| Trusted grant insert (bootstrap wildcard, `EnsurePersonalParty` owned-scope grant) | `grant` | `grant` | the grant's target, none for a wildcard | 7/004, 8/005 |
| Person record created by `EnsurePersonalParty` (only if mod-core's entity helper does not already observe it) | `create` | `natural_person` | the person | 8/005 |
| Impersonation start | `impersonate` | `impersonation` | the impersonated profile; `after` carries the child session uuid and the reason | 8/003 |
| Impersonation renew / end | `update` / `revoke` | `impersonation` | the impersonated profile | 8/003 |

**Acting profile for unauthenticated flows.** mod-audit's observer refuses an observation without
`AuthContext.ProfileID` (`ErrNoActor`), and its trigger requires `principal_id` to be that
profile's holder (`MF401`). Several flows run before any `AuthContext` exists: registration,
login, anonymous sign-in, the email-code and password-reset flows, the OIDC callback and JIT,
refresh reuse and bootstrap. Each of them installs, for its observations, an
`AuthContext` built as follows:

- `ProfileID` is the profile the flow lands on or creates. Otherwise it is the principal's
  personal profile. Otherwise (a managed user) it is the principal's oldest non-ended profile.
- `PrincipalID` is that profile's holder.
- `SessionID` is the new or affected session when there is one.
- `CredentialID` stays 0 whenever `SessionID` is set.

A failed sign-in writes no observation.

## 11. Module manifest (`moduleforge.module.yaml`) (binding)

Services: `authorizer` (unchanged), `oauthOrchestrator`, `verifier` (`auth.NewLocalVerifier`,
args `context`, `field:cfg.LocalAuth`), `authenticator` (`auth.NewAuthenticator`), `anonymousActor`,
`userService`, `credentialService`, `sessionService`, `profileService`, `partyPolicyService`,
`personalPartyService`, `impersonationService`, `bootstrapService`, `directoryService`,
`sshKeyResolver`, `collaboratorGate`, handlers `onboardingHandler`, `providersHandler`,
`authHandler`, `oidcHandler`, `meHandler`, `sessionHandler`, `principalsHandler`, `partiesHandler`,
`impersonationHandler`, `bootstrapHandler`. Middleware: `requireOIDCConfirmed`, `requireAuth`
(`auth.NewRequireAuth`), `resolveOrAnonymous` (`auth.NewResolveOrAnonymous`),
`requireVerifiedEmail`. Removed: `claimMapper`, `userResolver`, `userAccountService`,
`assumeHandler`, `appsHandler`, `usersHandler`, `selfHandler`, `identitiesHandler`,
`sshKeyService`, `sshKeysHandler`, `stepUpConsumed`, `resolveActorOrAnonymous`,
`hooks.firstUserGrant`, the `closure:grantAdminFn`/`revokeAdminFn` args. Display renderers for
`user` and `profile` are registered (U19).

## 12. Frontend contract (resolution (c))

- Tokens are held per tab (`sessionStorage` or memory), never in `localStorage` or cookies;
  requests send `Authorization: Bearer` with `credentials: 'omit'`.
- Each tab owns one refresh lineage; a new tab obtains a lineage by `POST /v1/session/fork` using an
  access token handed over from a sibling tab (`BroadcastChannel`), or by signing in.
- The active profile is the profile named by the tab's access token; switching calls
  `POST /v1/session/switch` and replaces only that tab's token.
- On `401 users.profile_unavailable` the client shows the lock reason and remedy; on
  `401 users.session_revoked` / `users.refresh_reuse` it signs out; on `409 users.step_up_required`
  it runs a step-up and retries.
- OIDC login and link return through `#handoff=<code>`, exchanged once at `POST /v1/auth/handoff`.
