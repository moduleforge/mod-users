# Rewrite Queries And Generated Code

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Replace `model/queries/*.sql` with queries over the new `mod_users` schema from task 001,
regenerate `model/db/` (`usersdb`) with the binding names, and add the SQLSTATE helpers. Scope:
`model/queries/`, `model/db/` (generated only), `api/internal/service/sqlstate.go`, and the
SQLSTATE re-exports in `api/usersservice/`.

Suggested role: `plugins/flow/roles/developer-sql.md` with `plugins/flow/roles/developer-go.md`
for the helpers.

Dependencies within the phase: hard on 001 (the schema the queries compile against).

**This task leaves `api/` uncompilable**: removing `UserAccount` and the old queries breaks every
consumer under `api/`. Tasks 003 to 007 rewrite the consumers; task 008
(`server-wiring-and-surface-removal`) restores `make build`.

## Requirements

1. Delete the obsolete query files (`user_accounts.sql`, `auth_local.sql`, `email_codes.sql`,
   `password_resets.sql`, `auth_oidc_identities.sql`, `anon_tokens.sql`, `apps_user_accounts.sql`,
   `ssh_public_keys.sql`, `system_actors.sql`, `auth_jwt_secrets.sql`) and write the new set:
   `users.sql`, `identifiers.sql`, `credentials.sql` (including per-kind subtables, `active`
   lookups by issuer/subject and fingerprint, revoke, revoke-sessions-holding-credential),
   `challenges.sql` (calling `mod_users.create_challenge_with_cooldown`, consume, attempt counter),
   `sessions.sql` (sessions, factors, refresh lineages with rotation and reuse lookups, the
   `session_liveness`, `credential_liveness`, `profile_availability`,
   `credential_profile_availability` and `session_acr` wrappers), `party_policies.sql`,
   `oidc_config.sql`/`oidc_providers.sql` updated for the new provider columns, and
   `signing_keys.sql`. Adjust `oidc_*.sql` file names as convenient.
2. The binding `usersdb` subset of [API surface 6.5](../notes/mod-users-api-surface.md) exists:
   `usersdb.User`, `GetUserByEntityID`, `GetSessionByUUID`, `GetCredentialByUUID`; `UserAccount`,
   every `*UserAccount*` query, `GetSystemActorBySlug`/`GetSystemActorBySlugRow` and the SSH,
   anon-token, email-code, password-reset, OIDC-identity and apps-membership queries are gone.
3. Run `cd model && make gen` (never hand-edit `model/db/`).
4. Add `api/internal/service/sqlstate.go` with `SessionInvariantSQLState` (`MF101`),
   `CredentialInvariantSQLState` (`MF102`), `UserInvariantSQLState` (`MF103`) and the
   `IsSessionInvariantError`, `IsCredentialInvariantError`, `IsUserInvariantError` predicates, plus a
   helper mapping the named unique indexes of API surface section 4 to their `409` codes; re-export
   them from `api/usersservice` (binding names).
5. Queries cover what tasks 003 to 007 need; later tasks may add queries, but should not need to
   restructure these.
6. Update `model/README.md` rows describing the query files.

## Validation

- `cd model && make verify` passes (sqlc compile-check against the composed migrations).
- `cd model && make gen` produces no diff after a second run.
- `grep -rn 'UserAccount\|GetSystemActorBySlug' model/db/` returns nothing.
- `cd api && gofmt -l internal/service/sqlstate.go` is empty. `sqlstate.go` imports nothing from
  the removed code; add `sqlstate_test.go` covering the three predicates and the index mapping (it
  runs once task 008 restores the package build; task 010 confirms it green).
- The report lists which `api/` packages are red after this task and names task 008 as the task
  that restores them.

## Assumptions

- Task 001's baseline is merged into the phase branch and `make verify` passes on it.

## References

- [API surface sections 2 to 4 and 6.5](../notes/mod-users-api-surface.md).
- [Decisions U3, U13](../notes/decisions-and-resolutions.md).
- [Work packages, package 2](../notes/work-packages.md).
- `.claude/CLAUDE.md` — `model/db/` is sqlc output; regenerate rather than edit.

## Checkpoint hints

- After the query files are written and `make verify` passes.
- After `make gen` and the SQLSTATE helpers.
