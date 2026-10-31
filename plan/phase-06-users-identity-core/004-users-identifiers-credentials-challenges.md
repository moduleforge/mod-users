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
   maps to `users.ssh_key_in_use`), the `oidc` creation primitive (requirement 4a), guest `anon`
   credential, list, label and `default_profile_id` update, and `RevokeInTx` (binding signature,
   API surface 6.3) that revokes every session holding the credential as a factor with
   `revoke_reason = 'credential_revoked'`. Refuse removing the last interactive credential
   (`users.last_credential`). Credentials are revoked, never deleted.
4. **`IssueAPIKeyInTx`** (binding, API surface 6.3): service accounts only; authorizes the
   `credentials` operation on the principal entity. Until mod-authz seeds that slug, the prelude's
   unknown-slug fallback admits only wildcard `manage` holders; that is sufficient for this phase.
   The HTTP routes and the owner path come in the engine phase.

   4a. **`LinkOIDCInTx`** (binding, API surface 6.3, decision U24; app-mfmanager request M8):
   `CredentialService.LinkOIDCInTx(ctx, tx, LinkOIDCInput) (LinkedCredential, error)` with the
   input and result types exactly as in section 6.3. It is the trusted `oidc` creation primitive.
   It does no authorization (document this in its doc comment) and creates an active `oidc`
   credential plus its `cred_oidc` row. Task 006's JIT (`byo`, and `managed` with
   `ConfinedToPartyID`) and session-link paths call it rather than a second insert path. app-mfmanager
   calls it to pre-create a managed-app owner's link. A clash on
   `cred_oidc_active_issuer_subject_uq` maps to `ErrOIDCIdentityInUse` (409
   `users.oidc_identity_in_use`). An MF102 refusal surfaces as an internal error. It never matches
   or links by email. Re-export it from `api/usersservice` (task 008 owns the facade file; add the
   re-export here if the facade already builds, otherwise list it for task 008).
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
8. Emit observer resources `user`, `user_identifier`, `credential` with exactly the ops of
   [API surface section 10.1](../notes/mod-users-api-surface.md) (decision U21):
   - user created: `create`;
   - identifier added, verified or made primary, removed: `create`, `update`, `delete`;
   - credential created, including `LinkOIDCInTx`, API keys and `anon`: `create`;
   - credential revoked, including by `RevokeInTx`: `revoke` (never `delete`).

   mod-audit rejects any other verb, and under the `propagate` policy the rejection rolls back the
   operation. `assume` is never used. The primitives that run before an `AuthContext` exists
   (user creation at registration, guest sign-in, JIT) take the acting profile from the section
   10.1 rule for unauthenticated flows. Their callers (tasks 005 and 006) install it.

## Validation

- `cd api && go build ./internal/service/... ./internal/sshkey/...` passes.
- `grep -rnE 'UserAccount|user_accounts|SSHKeyService' api/internal/service --include='*.go' | grep -v _test.go`
  returns nothing.
- `grep -rn 'LinkOIDCInTx' api/internal/service --include='*.go'` shows the method and its use as
  the only `cred_oidc` insert path.
- `grep -rnE 'Observe(AfterCommit)?\([^)]*"assume"' api/internal/service --include='*.go'` returns
  nothing. Every `Observe` op in the files this task wrote is one of the section 10.1 ops.
- Behavior tests for these services are written in tasks 009 (DB invariants) and 010 (service and
  route); the report names any behavior those tasks must cover beyond their documents.

## Assumptions

- mod-core's principal and profile creation helpers (`service.CreateEntity` and the profile
  insertion path named in mod-core's API surface note) are available.

## References

- [API surface sections 2.1 to 2.5, 4, 6.3, 7](../notes/mod-users-api-surface.md).
- [Decisions U1, U7, U11, U12, U13, U21, U24](../notes/decisions-and-resolutions.md).
- app-mfmanager plan worktree, `plan/notes/app-mfmanager-surface.md` section 3.5 and
  `plan/notes/decisions-and-resolutions.md` M8: how `LinkOIDCInTx` is consumed.
- mod-audit plan worktree, `plan/notes/mod-audit-api-surface.md` section 2.2: the op vocabulary.
- [Work packages, package 4](../notes/work-packages.md).

## Checkpoint hints

- After `UserService` creation primitives.
- After identifiers and credentials (including `RevokeInTx` and `IssueAPIKeyInTx`).
- After challenges and the old-service deletions.

## Status

Outcome: succeeded (2026-10-23). `cd api && go build ./internal/service/... ./internal/sshkey/...` passes; the old-name grep and the `assume` grep return nothing; `cred_oidc` is inserted only by `CredentialService.LinkOIDCInTx`.

Validation: build passed (siblings resolved through a staging directory of symlinks under `.flow/siblings`, `MODULEFORGE_SIBLINGS_DIR` set); grep for `UserAccount|user_accounts|SSHKeyService` in non-test code: no matches; grep for `Observe(AfterCommit)?(... "assume")`: no matches. Every observation op in the new files is one of `create`, `update`, `delete`, `revoke` on resources `user`, `user_identifier`, `credential`, `session`.

Files (all under `api/internal/service/`): `errors.go`, `helpers.go`, `user_service.go` (creation primitives and identifiers), `credential_service.go`, `challenge_service.go`, `ssh_key_resolver.go` (rewritten); deleted `user_accounts.go`, `ssh_keys.go`, `identities.go`. New unit tests with fakes: `helpers_test.go`, `identity_fakes_test.go`, `user_service_test.go`, `credential_service_test.go`, `challenge_service_test.go`, `ssh_key_resolver_credential_test.go` (all pass with the stale test files below moved aside).

Decisions and notes for later tasks:

- Stale tests left for task 010: `identities_test.go`, `ssh_key_resolver_test.go`, `ssh_keys_test.go`, `user_accounts_*_test.go` still reference deleted symbols, so `go test ./internal/service` does not compile until 010 removes or rewrites them. `sqlstate_test.go` is unaffected.
- Facade: `api/usersservice/service.go` still re-exports the deleted types, so it does not build. Task 008 owns it and must re-export `LinkOIDCInTx`, `LinkOIDCInput`, `LinkedCredential`, `IssueAPIKeyInput`, `IssuedAPIKey`, `ErrOIDCIdentityInUse` and the new services (`UserService`, `CredentialService`, `ChallengeService`, plus `ErrUnknownSSHKey`, `ErrSSHKeyNotAuthorized`, `SSHCredential`).
- Constructors (all take the querier(s) directly; there is no pool parameter because every write method takes the caller's transaction): `NewCredentialService(q, coreQ, az, obs, hashPassword)`, `NewUserService(q, coreQ, az, obs, naturalPersonServicer, credentialService)`, `NewChallengeService(q, emailSender)` (q must be pool-backed), `NewSSHKeyResolver(q, coreQ)`. `ChallengeService` is new and not in the API surface 6.3 service list; the manifest needs a `challengeService` entry.
- All write methods are `...InTx` and emit in-transaction observations only; no `ObserveAfterCommit` is called, since the caller owns the commit.
- Unauthenticated sign-up: `CreateBYOUserInTx` and `CreateGuestUserInTx` stamp `created_by_profile_id` from the acting profile on ctx and fall back to NULL when ctx has none (the profile does not exist yet). Their observations, and the person record's, run as the new personal profile (`CreatedUser.ActingContext(ctx)` gives callers the same context).
- `CreateManagedUserInTx` authorizes `create` on the `user` type within the party (fails closed with no authorizer); BYO and guest creation do not authorize.
- API key secrets are stored as `HashToken` (SHA-256 hex) and verified with `TokenMatchesHash`; client ids are `mfk_` + 32 hex, secrets `mfs_` + 64 hex. Task 005's client-credentials grant must use `TokenMatchesHash`. Anon device tokens use the same hash.
- Numeric codes (`email_code`, `verify_identifier`, `step_up`) are bcrypt-hashed and attempt-bounded (`VerifyCode`, bound 5, counter and consumption commit on the pool outside any caller transaction); `password_reset` and `login_handoff` are 256-bit tokens hashed with `HashToken` and consumed with `ConsumeTokenInTx` (handoff TTL fixed at 60 s).
- `RevokeInTx` also writes `revoke` `session` observations for the sessions it ends; `RemoveInTx` is the self-service form with the last-interactive-credential guard (`ErrLastCredential`); `RevokeInTx` itself has no guard (guest upgrade and departure revoke the last credential legitimately).
- `AuthContextForOwner` ignores `ownerID` until the parties phase; it returns the personal-profile context, refusing a confined credential or a profile that `credential_profile_availability` does not call `available` with `ErrSSHKeyNotAuthorized` (`*SSHKeyNotAuthorizedError` carries the party uuid).
- Identifier removal is blocked by the schema once an identifier ever had a bound credential: filed as a finding (RemoveIdentifierInTx returns `ErrIdentifierInUse`, 409 `users.identifier_in_use`, not in API surface section 7).
- New codes beyond section 7: `users.password_exists`, `users.identifier_in_use`, `users.principal_not_service_account`, `users.email_invalid`, `users.credential_label_*`.

Behavior beyond these documents that tasks 009 and 010 must cover with a real database: the deferred personal-profile check passing for BYO and guest creation and failing for a managed user given a profile; `CreateBYOUserInTx` end to end with mod-core's `NaturalPersonService.CreateInTx` and mod-audit's `MF401` check under the new profile; `LinkOIDCInTx` clash on `cred_oidc_active_issuer_subject_uq`; `RevokeInTx` ending sessions that hold the credential as a factor; `RemoveIdentifierInTx` savepoint behavior against the real FK; `VerifyCode` attempt counting under concurrent guesses.
