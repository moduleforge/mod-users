# Rewrite Baseline Schema

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Rewrite `model/migrations/sql/0100_baseline.sql` in place so that it creates the ADR's principal,
credential, session and party-gating schema in the `mod_users` Postgres schema, fold `0101` to
`0104` into it and delete them. This is the phase's first task and the riskiest one (deferred
constraint triggers, cross-schema triggers on mod-core's `public.profiles`, availability and
liveness SQL); every other phase-6 task builds on it.

Scope: SQL only (`model/migrations/sql/`, plus `model/sqlc.yaml` `rename` overrides so the Go names
stay unprefixed). Queries and generated Go code are task 002. No Go code under `api/` changes here.

Suggested role: `plugins/flow/roles/developer-sql.md` (primary), with
`plugins/flow/roles/architect-data.md` for the invariant design. No standard skill covers this; follow
the contract below.

Dependencies within the phase: none (first task). Hard prerequisite outside the phase: mod-core
phases 1 to 3 merged (`principals`, `profiles`, `system_actors`, `profile_policy_confirmations`,
`apps.invite_only`, `is_profile`, `profile_own_subject`, MF003 to MF005).

## Requirements

1. **One schema rule (U3).** Every mod-users table, function and trigger is created in `mod_users`
   and referenced schema-qualified; references to mod-core and mod-authz objects are
   `public.`-qualified. Trigger and SQL functions are `SECURITY INVOKER` with
   `SET search_path = pg_catalog, pg_temp`. Remove the `current_schema()` search-path baking.
2. **Tables**, exactly as fixed in [API surface section 2](../notes/mod-users-api-surface.md):
   `users` (2.1), `user_identifiers` (2.2, named index `user_identifiers_kind_value_uq`),
   `credential_kinds` seeded with the six kinds of 2.3 (reserved kinds not seeded), `credentials`
   and `cred_password`, `cred_email_otp`, `cred_oidc`, `cred_ssh_key`, `cred_api_key`, `cred_anon`
   (2.4, with the `active` mirror columns maintained by trigger and the named partial unique indexes
   `cred_oidc_active_issuer_subject_uq`, `cred_ssh_key_active_fingerprint_uq`,
   `cred_password_active_identifier_uq`, one active unbound password per principal, one active
   email OTP per identifier), `auth_challenges` (2.5, including kind `login_handoff`, U7),
   `oidc_config` (shape unchanged) and `oidc_providers` with `party_id`, `jit_mode`, `trust_acr`
   and the managed-requires-party check (2.6), `party_authn_policies` (2.7, no `invite_only`),
   `credential_party_authorizations` (2.8), `sessions`, `session_factors`,
   `session_refresh_tokens` (2.9, U6), `auth_signing_keys` single row (2.10, U4), and the
   `public.accessible_user_ids_for_actor` stub (2.11, U18) in the same shape as mod-core's stubs.
3. **Invariants** with mod-users' own SQLSTATE codes (U13): `MF103` user invariants 1 to 4 of
   section 2.1; the mod-users-owned `BEFORE INSERT` trigger on `public.profiles` (invariant 5); the
   deferred constraint trigger enforcing "bring-your-own user has exactly one personal profile,
   managed user has none" at commit (invariant 6); `MF102` credential invariants of 2.4 (immutable
   `principal_id`/`kind`, kinds per principal type via `service_allowed`, none for system actors,
   guests only `anon`, non-guests gain no new `anon`, `default_profile_id` held by the principal,
   `cred_*` kind match); `MF101` session invariants of 2.9 (no impersonation nesting, impersonated
   profile held by the principal, immutable columns, session principal is a `user`, factor
   credential principal equals session principal).
4. **SQL functions** of [API surface section 3](../notes/mod-users-api-surface.md), all `STABLE`,
   invoker rights, pinned search path: `profile_availability`, `credential_profile_availability`,
   `session_liveness`, `credential_liveness`, `session_acr`, with the exact return shapes and lock
   reasons listed there. Resolution (a): the acting profile's party policy only;
   `profile_policy_confirmations` exempts the credential rule, never `min_acr`; personal profiles
   and impersonation sessions skip the party gate. Plus
   `mod_users.create_challenge_with_cooldown(...)` keeping today's advisory-lock cooldown from
   `create_email_code_with_cooldown`.
5. **Deletions** (section 2): `user_accounts`, `auth_local`, `email_codes`, `password_resets`,
   `auth_oidc_identities`, `anon_tokens`, `apps_user_accounts`, `mod_users.ssh_public_keys`, the
   unqualified `system_actors` table, the `system_actor` type seed, the `anonymous` seed,
   `entities_no_system_actor_owner` and its function, `auth_jwt_secrets`,
   `create_email_code_with_cooldown`. Delete `0101_*.sql` to `0104_*.sql`.
6. **sqlc renames** in `model/sqlc.yaml` so generated Go names stay unprefixed (`User`,
   `Credential`, `Session`, ...). Do not regenerate `model/db/` here unless `make verify` requires
   it; task 002 owns the queries and generated code.
7. Update the `model/README.md` rows and the `AGENTS.md` migrations paragraph (currently describing
   `0101` to `0104`, unqualified tables and the displaced-schema guard) that this task makes wrong.
8. Any change to a **(binding)** name in the API surface note is recorded in this task document and
   flagged in the report.

## Validation

- `cd model && make verify && make lint` pass (migrations apply to an ephemeral shadow Postgres
  composed with mod-core and mod-authz; `make compose` resolves mod-core and mod-authz to their
  plan worktrees through `scripts/link-siblings.sh`).
- `ls model/migrations/sql/` lists only `0100_baseline.sql`.
- `grep -nE 'user_accounts|auth_local|email_codes|password_resets|auth_oidc_identities|anon_tokens|apps_user_accounts|auth_jwt_secrets|entities_no_system_actor_owner|current_schema\(\)' model/migrations/sql/0100_baseline.sql`
  returns nothing.
- Every `CREATE TABLE`/`CREATE FUNCTION`/`CREATE TRIGGER` in the baseline is `mod_users.`- or
  (for the access stub only) `public.`-qualified.
- A manual smoke script against the shadow database (kept out of the diff) shows: inserting a
  bring-your-own user without a personal profile fails at `COMMIT`; a guest gaining a `password`
  credential raises `MF102`; a nested impersonation session raises `MF101`.
- `api/` compile is not expected to pass after this task (task 002 regenerates the code;
  task 008 restores the build).

## Metadata

architectural_impact: true

## Assumptions

- mod-core's phase 1 to 3 schema is merged on the mod-core plan worktree and reachable through the
  sibling link.
- mod-authz's migrations do **not** compose unchanged. mod-authz phase 13
  (`authz-identity-schema`) rewrites `0500` to `0505` and must merge before this phase closes
  (decision U27, mod-authz H3). Compose mod-authz from its plan worktree. This task's SQL
  references no mod-authz object, so it must be valid against both the pre- and post-phase-13
  mod-authz schema. If phase 13 has not merged yet, say so in the report; task 010 re-runs
  `make verify` against it.
- No data migration or backward compatibility is needed (pre-release); existing dev databases are
  dropped (`make clean.data`).

## References

- [API surface sections 2 to 4](../notes/mod-users-api-surface.md) — the binding table, function
  and SQLSTATE contract.
- [Decisions U3, U4, U6, U7, U13, U18](../notes/decisions-and-resolutions.md) — reasons for the
  schema move, signing keys, refresh lineages, handoff challenge, SQLSTATE codes and the stub.
- [Work packages, package 1](../notes/work-packages.md).
- mod-core ADR `docs/architecture/identity-model.md` sections "Principals, users and the personal
  profile", "Credential gating and profile availability", "Sessions, tokens and the active
  profile", "Invariants: database versus service layer".
- `docs-mf-standards/architecture/schema-ownership-design.md` — the `mod_<module>` schema rule.

## Checkpoint hints

- After the tables, indexes and seeds compile under `make lint`.
- After the MF101 to MF103 triggers and the `public.profiles` trigger.
- After the availability, liveness and `session_acr` functions.

## Status

**Outcome: succeeded** (2026-10-23).

Affected files: [`model/migrations/sql/0100_baseline.sql`](../../model/migrations/sql/0100_baseline.sql)
(rewritten), `model/migrations/sql/0101_*.sql` to `0104_*.sql` (deleted),
[`model/sqlc.yaml`](../../model/sqlc.yaml) (19 `rename` overrides),
[`model/queries/oidc_config.sql`](../../model/queries/oidc_config.sql) and
[`model/queries/oidc_providers.sql`](../../model/queries/oidc_providers.sql) (tables qualified
`mod_users.`), ten obsolete `model/queries/*.sql` files (deleted), [`AGENTS.md`](../../AGENTS.md)
(migrations and sqlc paragraphs), [`model/README.md`](../../model/README.md) (layout rows).

Validation summary:

- `cd model && make verify && make lint` pass, with `MODULEFORGE_SIBLINGS_DIR` pointing at a staging
  directory whose `mod-core`, `mod-authz`, `mod-audit` links resolve to the three
  `worktrees/plan/identity-model-impl` checkouts (mod-core `1f0a097`, mod-authz `937df24`, mod-audit
  `c98ec56`). mod-authz phase 13 is merged there (`grants.scope`, `ownership_confers`, `is_profile`
  triggers), so the baseline was linted against the post-phase-13 mod-authz schema. `sqlc compile`
  also passes under CI's pinned sqlc 1.28.0 (container image), and the renames yield unprefixed
  names under 1.28.0 and 1.31.1.
- `ls model/migrations/sql/` lists only `0100_baseline.sql`; the deleted-names grep returns nothing;
  every `CREATE TABLE`/`FUNCTION` is `mod_users.`-qualified except the `public.` access stub, and
  every trigger targets `mod_users.*` or `public.profiles`.
- Smoke script against a throwaway shadow database (kept out of the diff): a bring-your-own user
  without a personal profile fails at `COMMIT` with `MF103`; a guest gaining a `password`
  credential raises `MF102`; a nested impersonation session raises `MF101`. An extended suite (84
  checks: every MF101 to MF103 rule, the `public.profiles` triggers, the named unique indexes, the
  `active` mirrors, availability lock reasons, `session_acr`, both liveness functions, the
  challenge cooldown) passes, and `goose down` then `up` round-trips cleanly.
- `api/` is not compiled (expected red until task 002 regenerates `model/db/` and task 008 restores
  the build). `model/db/` was not regenerated, so it is stale against the queries until task 002.

Decisions (none changes a **(binding)** name of the API surface note):

- **Queries touched for `make verify`.** `sqlc compile` cannot pass with queries over deleted tables,
  so the ten obsolete query files task 002 deletes anyway were deleted here, and the two surviving
  `oidc_*` files were only schema-qualified. `model/db/` is untouched.
- **Mirror columns beyond the sketch.** Active-scoped uniqueness needs an `active` mirror on
  `cred_password` and `cred_email_otp` too (not only `cred_oidc`/`cred_ssh_key`), and "one active
  unbound password per principal" needs a `principal_id` mirror on `cred_password`. Both are
  trigger-maintained (`cred_subtable_check`, `credentials_sync_active`), never caller-written. The
  extra named indexes are `cred_password_active_unbound_uq`, `cred_email_otp_active_identifier_uq`.
- **`session_factors.asserted_acr`** (nullable, 1 to 3) was added: `session_acr` counts an IdP's
  asserted acr only with `trust_acr`, and the sketch had no column to hold it.
- **Liveness functions use OUT parameters**, not `RETURNS TABLE`: identical one-row result for SQL
  callers, but sqlc only types the OUT form. `credential_liveness` statuses beyond the lock reasons
  are `credential_revoked` (also for an unknown credential) and `credential_expired`; an unknown
  session is `session_revoked`, an unknown profile `not_held`. `session_acr` returns NULL for a
  session with no unrevoked factor.
- **Availability interpretation.** `profile_availability` and `credential_profile_availability`
  share one internal function (`mod_users.profile_availability_for`). Party `min_acr` applies to
  every party profile, including under impersonation and with a policy confirmation ("always");
  impersonation and confirmations skip only the credential rule.
- **Additional guards** (all `MF102`/`MF103`): the `users` row must exist before a user's profiles
  and credentials; a password/email-OTP credential's identifier must belong to its principal; the
  personal-profile check also re-runs (deferred) when a user's personal profile is deleted.
  `CHECK`s: email `value_canon` is trimmed and lowercased; `revoked_at`/`revoke_reason` are set
  together on credentials and sessions; a `service_allowed` kind is non-interactive; a refresh token
  is replaced only after use.
- **`auth_signing_keys`** stores the key as `private_key BYTEA` (PKCS#8 DER, at least 48 bytes)
  and keeps `CREATE TABLE IF NOT EXISTS`, because the config bootstrap may create it before
  migrations run (the same reason as the deleted `auth_jwt_secrets`).
- **Challenge cooldown subject** is (kind, principal, identifier, session) with NULLs equal,
  served by an expression index; the function's parameter list follows the API surface note.
- **Down migration** drops every object by name, never `CASCADE`, so another module's foreign key
  into `mod_users` blocks the rollback instead of being dropped silently.

Findings filed: identifier deletion conflicts with never-deleted credentials bound by `RESTRICT`
foreign keys; sqlc's schema does not include mod-core tables, so queries joining `public.*`
relations will not compile (affects task 002 and later).
