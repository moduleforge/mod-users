# Session Credential Route Tests

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Migrate every unit and route test under `api/` that tasks 002 to 008 left red, and add the
session, credential, OIDC and route tests that prove this phase's behavior. This is the phase's
closing task: it ends with every phase-exit check green.

Scope: `api/internal/auth/*_test.go`, `api/internal/config/*_test.go`,
`api/internal/handlers/*_test.go`, `api/internal/handlers/auth/*_test.go`,
`api/internal/service/*_test.go`, `api/auth/*_test.go`, `api/handlers/*_test.go`; integration tests
for session behavior using the same throwaway-Postgres harness as task 009.

Suggested role: `plugins/flow/roles/developer-go.md`.

Dependencies within the phase: hard on 008 (the whole `api/` builds) and on 009 (integration suites
and fixtures in place, so phase-exit checks can run together).

## Requirements

1. **Migrate route-order tests** from `ordertest.Actor`/`Sudo`/`SudoOnly` to mod-core's
   `AuthContext` identities (`ordertest.Anonymous` keeps meaning "no `AuthContext`"):
   `account_routes_order_test.go` (delete with its route file or replace with `/v1/me` and
   `/v1/principals` order tests), `order_helpers_test.go`, `require_auth_mount_test.go`,
   `self_service_order_test.go` and the rest of the handler and auth tests. Delete tests of removed
   behavior (HS256, step-up token, JTI cache, sudo, assume, first-user hook, cookie authentication)
   and replace them where the behavior has a successor.
2. **Session behavior**: login and landing order (credential default, user default, personal, only
   available, else `selection_required`); refresh rotation, and reuse of a spent token revoking the
   session; fork mints an independent lineage; switch writes nothing to the session row;
   per-request revocation (logout, credential revocation, principal disable, profile ended) takes
   effect on the next request; step-up raises `acr` and `auth_time`; handoff code is single use and
   expires.
3. **Carrier and middleware**: claims of API surface 5 only; `alg` pinned to EdDSA (an HS256 or
   `none` token is rejected); audience checks; `AuthContext` populated for authenticated and
   anonymous requests; `acr`/`auth_time` taken from the session row; persisted signing key reused
   across restarts (migrate `api/internal/config/jwtsecret_bootstrap_integration_test.go` to the
   `mod_users.auth_signing_keys` bootstrap).
4. **Credentials**: password, email OTP (created on identifier verification), SSH key and API key
   flows; `PUT /v1/me` update shape (successor of the deleted
   `shape_self_update_integration_test.go`); `users.last_credential`; self-service writes refused past
   `AUTH_SELF_SERVICE_MAX_AUTH_AGE` with `users.step_up_required`, asserting the response is the shared mod-core `apiresp` action-required error as rendered by `WriteError` (status 409, action code and details); guest upgrade.
5. **OIDC**: no email link (`account_exists_sign_in_and_link`); JIT `byo`, `managed` (confined
   credential, `idp_jit` profile), `none`; link and step-up refused without an authenticated
   session; never linking to managed or service principals.
6. **Client credentials**: `cid` token issue and `credential_liveness` refusing a revoked
   credential on the next request.
7. **Consumer-requested additions** (decisions U23, U24):
   - `LinkOIDCInTx` creates a credential that logs in on the known `(issuer, subject)` path;
     a second active link of the same pair is 409 `users.oidc_identity_in_use`; it never links by
     email.
   - `ReadPrincipalSummaries` on an open transaction sees rows written earlier in that
     transaction and uses no second connection (for example, run it on a pool of size 1 while
     the transaction is held). Its result equals `DirectoryService.PrincipalSummaries` for the
     same ids.
8. **Audit vocabulary** (decision U21): with a recording observer, the sign-in, step-up, logout,
   refresh-reuse, OIDC JIT, credential and identifier flows emit exactly the op and resource pairs
   of [API surface section 10.1](../notes/mod-users-api-surface.md), and no op outside mod-audit's
   set. Each unauthenticated flow's observation carries a non-zero `ProfileID` whose holder is the
   observed `PrincipalID`, and never both `SessionID` and `CredentialID`. If mod-audit phase 20 has
   merged, run one sign-in through mod-audit's real observer against a composed `audit_log` to
   prove the row is accepted.
9. **mod-authz phase 13 schema** (decision U27): if task 003 left it, add the `scope = 'entity'`
   filter to the minimal port's wildcard and entity arms. Test that an owned-scope grant on a party
   does not authorize an operation on the party entity itself.
10. Run the phase-exit checks and fix test-side failures; implementation defects found are fixed
   only when local and obvious, otherwise reported with the failing test.

## Validation

- `make build`, `make test`, `make lint`, `cd model && make verify` pass.
- The integration suites (`make test.integration` and the throwaway-Postgres authz/config suites
  per `AGENTS.md`) pass with mod-core and mod-authz linked to their plan worktrees.
- `grep -rnE 'ordertest\.(Actor|Sudo|SudoOnly)|WithActor\(|ActorEntityID|UserAccount' api --include='*_test.go'`
  returns nothing.
- The phase closes only when mod-authz phase 12, mod-authz phase 13 and mod-audit phase 20 have
  merged on their plan branches (mod-authz H2 and H3, mod-audit E2, decision U27). Run the
  integration suites and `cd model && make verify` with mod-authz phase 13's migrations composed.
  If any of the three is still pending, this task may finish with only `api/cmd/server` red (12 or
  20 pending) or with only the phase-13-dependent suites unverified (13 pending). The report names
  the pending phase, and the manager does not close the phase until it merges and the checks are
  re-run.

## References

- [API surface sections 5 to 8, 12](../notes/mod-users-api-surface.md).
- [Decisions U4 to U8, U12, U14, U21, U23, U24, U27](../notes/decisions-and-resolutions.md).
- mod-audit plan worktree, `plan/notes/mod-audit-api-surface.md` sections 2 and 5.1: the
  `audit_log` CHECKs, trigger and observer mapping the audit test exercises.
- [Work packages, package 9](../notes/work-packages.md).
- mod-core plan notes `mod-core-api-surface.md` — the `ordertest` `AuthContext` identities.

## Checkpoint hints

- After the existing tests compile and pass again.
- After the session and carrier tests.
- After the credential, OIDC and client-credentials tests.
