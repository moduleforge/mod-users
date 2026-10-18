# Users Identifiers Credentials Challenges

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Add the service layer for users, identifiers, credentials and challenges in
`api/internal/service/`: the user-creation primitives every sign-up path shares, identifier
management, per-kind credentials with revocation that revokes dependent sessions, and challenges.
No HTTP routes are added here; tasks 005 to 007 expose these services.

Suggested role: `plugins/flow/roles/developer-go.md`.

Dependencies within the phase: hard on 002 (queries) and 003 (`AuthContext`, ported `authz.go`).
Tasks 005, 006 and 007 depend on this task.

`api/` as a whole stays uncompilable until task 008; this task makes `api/internal/service` build
(non-test code).

## Requirements

1. **`UserService` creation primitives**, used by registration (005), guest sign-in (005), OIDC JIT
   (006) and guest upgrade (007), each in the caller's transaction: create a bring-your-own user
   (principal through mod-core, `mod_users.users` row, the user's own `natural_person` owned by the
   user per U1 and U11, personal profile, `users.person_id` link); create a guest user (no person,
   `is_guest`, `anon` credential); create a managed user for a party (no person, `managing_party_id`).
   Each satisfies the task 001 invariants inside one transaction (the deferred personal-profile
   check passes at commit).
2. **Identifiers.** Add, verify, set primary, remove for `email` identifiers (canonical value
   trimmed and lowercased); `23505` on `user_identifiers_kind_value_uq` maps to
   `users.identifier_taken`. Verifying an email identifier creates an `email_otp` credential bound to
   it (U12).
3. **Credentials.** `CredentialService` with password (argon2id via the existing hashing; at most
   one active password per identifier and one unbound per user, U12), `email_otp`, `ssh_key`
   (reusing `api/internal/sshkey/` parsing and label rules; `cred_ssh_key_active_fingerprint_uq`
   maps to `users.ssh_key_in_use`), `oidc` creation primitive used by task 006
   (`cred_oidc_active_issuer_subject_uq` maps to `users.oidc_identity_in_use`), guest `anon`
   credential, list, label and `default_profile_id` update, and `RevokeInTx` (binding signature,
   API surface 6.3) that revokes every session holding the credential as a factor with
   `revoke_reason = 'credential_revoked'`. Refuse removing the last interactive credential
   (`users.last_credential`). Credentials are revoked, never deleted.
4. **`IssueAPIKeyInTx`** (binding, API surface 6.3): service accounts only; authorizes the
   `credentials` operation on the principal entity. Until mod-authz seeds that slug, the prelude's
   unknown-slug fallback admits only wildcard `manage` holders; that is sufficient for this phase.
   The HTTP routes and the owner path come in the engine phase.
5. **Challenges.** Create through `mod_users.create_challenge_with_cooldown`, verify with the
   failed-attempt counter and bound, consume once; kinds `email_code`, `verify_identifier`,
   `password_reset`, `step_up`, `login_handoff` (single use, 60 s, U7). Reuse the existing email
   sender for code delivery.
6. **Errors** use the codes of API surface section 7 and SQLSTATE helpers from task 002; MF102 and
   MF103 violations surface as internal errors unless a documented mapping applies.
7. Delete `api/internal/service/{user_accounts,ssh_keys,identities}.go` once their replacements
   exist; their tests are handled by task 010. Keep `ssh_key_resolver.go` building:
   `SSHKeyResolver.ResolveCredential` resolves an active `ssh_key` credential (API surface 6.3);
   `AuthContextForOwner` returns the personal-profile context (or `ErrSSHKeyNotAuthorized` for a
   confined credential) until the party-aware rules land in phase `parties-profiles-and-bootstrap`.
8. Emit observer resources `user`, `user_identifier`, `credential` (API surface 10, mod-audit).

## Validation

- `cd api && go build ./internal/service/... ./internal/sshkey/...` passes.
- `grep -rnE 'UserAccount|user_accounts|SSHKeyService' api/internal/service --include='*.go' | grep -v _test.go`
  returns nothing.
- Behavior tests for these services are written in tasks 009 (DB invariants) and 010 (service and
  route); the report names any behavior those tasks must cover beyond their documents.

## Assumptions

- mod-core's principal and profile creation helpers (`service.CreateEntity` and the profile
  insertion path named in mod-core's API surface note) are available.

## References

- [API surface sections 2.1 to 2.5, 4, 6.3, 7](../notes/mod-users-api-surface.md).
- [Decisions U1, U7, U11, U12, U13](../notes/decisions-and-resolutions.md).
- [Work packages, package 4](../notes/work-packages.md).

## Checkpoint hints

- After `UserService` creation primitives.
- After identifiers and credentials (including `RevokeInTx` and `IssueAPIKeyInTx`).
- After challenges and the old-service deletions.
