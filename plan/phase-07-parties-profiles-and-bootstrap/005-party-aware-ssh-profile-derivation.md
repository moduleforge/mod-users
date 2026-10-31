# Party-Aware SSH Profile Derivation

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Complete `SSHKeyResolver.AuthContextForOwner` with the ADR's full profile-derivation rules, so
mod-repos derives the acting profile of an SSH push or pull from the repository owner. Phase 6
delivered `ResolveCredential` and a personal-profile-only derivation; this task adds the party
rules.

Role: `plugins/flow/roles/developer-go.md`.

No dependency inside this phase: it can start as soon as the phase starts, in parallel with every
other task. Touches only the SSH resolver files, its tests and docs rows.

## Requirements

1. `AuthContextForOwner(ctx, cred SSHCredential, ownerID *int64) (opctx.AuthContext, error)`
   (binding signature, API surface 6.3) derives the profile as follows:
   1. `ownerID` names a party (a legal entity) in which `cred.PrincipalID` holds an **active**
      profile: use that profile if `mod_users.credential_profile_availability(cred, profile)` is
      `available`; otherwise return `ErrSSHKeyNotAuthorized` carrying the party uuid ("key not
      authorized for X").
   2. Otherwise (no owner, the owner is not a party, the principal has no active profile in it, or
      the owner is the principal's own user or person): the principal's personal profile, unless
      the credential is confined (`ConfinedToPartyID != 0`), in which case
      `ErrSSHKeyNotAuthorized` for the confining party.
   3. A confined credential used against its own party follows rule 1.
   The returned `AuthContext` carries `PrincipalID`, `ProfileID`, `CredentialID`,
   `ACR = base_acr` of `ssh_key`, `AuthTime = now`; no `SessionID`.
2. Read-only: no writes, no cache, no key-material logging (API surface 6.3). Use a single query
   where practical (resolve active profile in party plus availability in one round trip).
3. `ErrSSHKeyNotAuthorized` is exported from `api/usersservice` with an accessor for the party uuid;
   document it in the package comment and in `AGENTS.md` for mod-repos.
4. Tests (integration, throwaway Postgres): owner party with an active profile and an authorized
   key (party `authorized` policy) → party profile; same with an unauthorized key → error with the
   party uuid; party policy `any` → party profile; `provider` policy → refused (an SSH key is never
   an OIDC factor); party `min_acr` 2 → refused; suspended or ended profile → falls to personal;
   no owner → personal; confined key with no owner → refused; confined key against its party →
   party profile; managed user (no personal profile) with no matching party → refused.

## Validation

- `make build`, `make test`, `make lint` pass; the new integration tests pass.
- `grep -n "ErrSSHKeyNotAuthorized" api/usersservice/*.go` shows the export.
- No `log`/`slog` call in the resolver prints the key or fingerprint
  (`grep -n "slog\|log\." api/internal/service/ssh_key_resolver.go`).

## Assumptions

- Phase 6 delivered `SSHKeyResolver`, `SSHCredential`, `ResolveCredential` and
  `credential_profile_availability`. The resolver file may have moved during phase 6; locate it
  with `grep -rn "func (r \*SSHKeyResolver)" api`.

## References

- [API surface](../notes/mod-users-api-surface.md) sections 3 and 6.3 (SSH), 10 (mod-repos).
- [Work packages](../notes/work-packages.md), phase `parties-profiles-and-bootstrap`, package 5.
- mod-core ADR `docs/architecture/identity-model.md`, "Credential gating" counter-case table (SSH
  row).

## Status

Outcome: succeeded (2026-10-25). `make build`, `make test`, `make lint` pass; the new integration tests pass against a throwaway Postgres 16; the `grep` checks hold (export present; no log call in the resolver).

- Resolver: `api/internal/service/ssh_key_resolver.go` (party-aware `AuthContextForOwner`).
- Facade export, accessor and package comment: `api/usersservice/service.go`.
- Tests: `api/internal/integration/ssh_profile_integration_test.go`, `api/internal/service/ssh_key_resolver_credential_test.go`, `api/usersservice/ssh_error_test.go`.
- Docs: `AGENTS.md` conventions row.
- Decision: the active party profile is found via mod-core `ListProfilesByHolder` followed by `CredentialProfileAvailability` (two round trips) because the sqlc schema for this module cannot see mod-core's `public.profiles`, so a single combined query is not expressible without a new migration.
