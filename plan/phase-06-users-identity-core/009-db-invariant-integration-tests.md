# DB Invariant Integration Tests

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Prove every database invariant and SQL function task 001 created, writing directly through SQL so
the rules hold whatever the writer, and migrate the existing `api/internal/authz` integration suites
off `user_accounts`.

Scope: `api/internal/authz/*_integration_test.go` (the established throwaway-Postgres harness), new
suites there or in a sibling test package using the same harness.

Suggested role: `plugins/flow/roles/developer-go.md` with `plugins/flow/roles/developer-sql.md`.

Dependencies within the phase: hard on 001, 002 and 003 (the `api/internal/authz` package and its
unit tests must compile). Parallel-eligible with tasks 004 to 008.

## Requirements

1. **MF103**: user type check; `managing_party_id` immutable; `is_guest` only true to false; owner
   is self or the managing party; `default_profile_id` held by the user; the `public.profiles`
   trigger (no personal profile for a managed user, no foreign-party profile for a managed user, no
   party profile for a guest); the deferred check (bring-your-own user without a personal profile
   fails at commit, succeeds when created in the same transaction; managed user with one fails).
2. **MF102**: kinds per principal type (`service_allowed` for service accounts, none for system
   actors), guest only `anon`, non-guest gains no new `anon`, immutable `principal_id`/`kind`,
   `default_profile_id` held, subtable kind match; active uniqueness (OIDC issuer/subject and SSH
   fingerprint unique only while active, re-adding after revocation succeeds; one active password
   per identifier; one unbound password per user; one active email OTP per identifier).
3. **MF101**: no impersonation nesting; impersonated profile held; immutable principal and
   impersonation columns; session principal must be a `user`; factor principal mismatch.
4. **Availability** (`profile_availability`, `credential_profile_availability`): every lock reason;
   confinement over every factor; `credential_rule` `authorized` and `provider`; a
   `profile_policy_confirmations` row exempts the credential rule but never `min_acr`; personal
   profiles skip the party gate; impersonation sessions skip it.
5. **`session_acr`** (single factor, two distinct factor classes, `trust_acr` handling) and
   **liveness** (`session_liveness` and `credential_liveness`: `ok`, `session_revoked`,
   `session_expired`, `parent_session_ended`, `principal_mismatch`, and a lock reason passed
   through; `impersonator_principal_id` is the parent session's principal).
6. **Cooldown concurrency**: concurrent `create_challenge_with_cooldown` calls yield one row.
7. **Migrate or delete** `displaced_guard_integration_test.go` (delete: the guard is gone),
   `anonymous_actor_integration_test.go` (rewrite on mod-core's system actor),
   `email_code_integration_test.go` (rewrite on challenges), `ssh_keys_integration_test.go`
   (rewrite on `ssh_key` credentials), `user_account_*_integration_test.go` (delete or replace by
   the suites above), `shape_self_update_integration_test.go` (delete; task 010 covers the `PUT /v1/me`
   update shape). Adapt the fixtures of `authz_integration_test.go`,
   `type_grant_integration_test.go`, `type_target_integration_test.go` and
   `instance_semantics_integration_test.go` to create actors as profiles of `mod_users.users`
   (the engine phase extends them further). `api/internal/config/jwtsecret_bootstrap_integration_test.go`
   belongs to task 010 (its package's unit tests must compile first).

## Validation

- Run the authz integration suite per `AGENTS.md` "Authz integration tests against a throwaway
  Postgres" (`go test -tags=integration -p 1 -count=1 ./internal/authz/...`), with mod-core and
  mod-authz linked to their plan worktrees: all green.
- `ls api/internal/authz/ | grep -E 'displaced_guard|user_account_'` returns nothing.
- Each rule in requirements 1 to 6 maps to at least one named test; list the mapping in the report.

## Assumptions

- If tasks 004 to 008 are not merged yet, tests in this task insert rows through SQL only and do
  not depend on the new services.

## References

- [API surface sections 2 to 4](../notes/mod-users-api-surface.md).
- [Decisions U3, U13](../notes/decisions-and-resolutions.md), resolution (a).
- [Work packages, package 8](../notes/work-packages.md).
- `api/internal/authz/authz_integration_test.go` header — harness detail.

## Checkpoint hints

- After the MF101 to MF103 suites.
- After the availability, `session_acr` and liveness suites.
- After migrating or deleting the existing integration suites.
