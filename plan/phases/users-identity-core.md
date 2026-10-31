# Users Identity Core

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Goals

Replace `user_accounts` and its credential tables with the ADR's principal model in mod-users:
the `user` principal subtype table, identifiers, the credential registry and per-kind credentials,
challenges, sessions with factors and refresh lineages, the 1-hour EdDSA carrier token (`sid`,
`prof`, `acr`, `amr`, `act`) with per-request liveness, the OIDC resolver without email
auto-linking, the Bearer-only API (resolution (c)), self-service under `/v1/me`, and the
`opctx.AuthContext` population every other module reads. Schema, services, handlers and tests share
one phase because the generated query API change breaks `api/` until every consumer is rewritten.
It comes first because every later mod-users phase and every downstream participant consumes the
principal, profile and session identities it establishes.

## Inputs

- mod-core phases 1 to 3: `opctx.AuthContext`, `authz.AuthorizeTypeIn`, `service.CreateEntity`,
  `principals`, `profiles`, `system_actors` (with the seeded anonymous actor), `platform_settings`,
  `profile_policy_confirmations`, `apps.invite_only`, the SQL functions and MF003 to MF005, and the
  `ordertest` `AuthContext` identities (mod-core plan notes `mod-core-api-surface.md`).
- Merged before this phase closes (decision U27, overview execution order U-X2 to U-X4):
  - mod-authz phase 12 and mod-audit phase 20, which adopt `opctx.AuthContext` (needed for
    `api/cmd/server` to compile);
  - mod-authz phase 13 (`authz-identity-schema`), because the generated `accessible_*` functions
    read its new grant columns.
- mod-audit's op vocabulary (mod-audit API surface section 2.2), mapped per event in
  [the API surface](../notes/mod-users-api-surface.md) section 10.1.
- The ADR sections "Principals, users and the personal profile", "Credential gating and profile
  availability", "Sessions, tokens and the active profile", "Credentials", "Machine principals,
  system actors, guests and apps", "Assurance" and "Invariants: database versus service layer".
- [The API surface](../notes/mod-users-api-surface.md) sections 2 to 8, 10 to 12;
  [decisions](../notes/decisions-and-resolutions.md) U1, U3 to U8, U11 to U19;
  [work packages](../notes/work-packages.md) for this phase.
- Existing code: `model/migrations/sql/`, `model/queries/`, `model/db/`, `api/internal/auth/`,
  `api/internal/authz/authz.go`, `api/internal/config/`, `api/internal/handlers/`,
  `api/internal/service/`, `api/auth/`, `api/handlers/`, `api/usersservice/`, `api/cmd/server/main.go`,
  `moduleforge.module.yaml`, `api/openapi.yaml`.

## Outputs

- `0100_baseline.sql` rewritten into the `mod_users` schema (users, identifiers, credential kinds,
  credentials and subtables, challenges, OIDC provider columns, party authn policies, credential
  party authorizations, sessions, session factors, refresh lineages, signing keys, the user access
  stub), with MF101 to MF103 invariants, availability and liveness functions; `0101` to `0104`
  deleted; old tables, the `system_actor` copy and `entities_no_system_actor_owner` gone.
- Regenerated `model/db/` with the binding names; SQLSTATE helpers.
- EdDSA-only signer and verifier with the persisted dev key; `auth.Identity`,
  `auth.Authenticator`, `NewRequireAuth`, `NewResolveOrAnonymous`, `NewRequireVerifiedEmail`,
  `IssueAudienceToken`; `AuthContext` populated on every authenticated and anonymous request.
- `/v1/auth` (register, login, anonymous, email code, password reset, token refresh and client
  credentials, handoff, OIDC), `/v1/session`, `/v1/me`, `/v1/principals` (read, list, disable,
  enable) served; removed routes and handlers deleted; first-user hook deleted.
- The resolver (`api/internal/auth/resolver.go`) rewritten: no email auto-link, JIT by `jit_mode`,
  link and step-up only from authenticated sessions.
- `authz.go` ported to the profile actor (full engine arms come in `users-authorization-engine`).
- Display renderers for `user` and `profile`; facades, manifest and OpenAPI current.
- Consumer-requested binding additions: `CredentialService.LinkOIDCInTx` (app-mfmanager) and
  `usersservice.ReadPrincipalSummaries` (mod-tokens).
- DB-invariant, session, liveness, revocation, credential and route tests green.

## Notes

- mod-core D8 removes the `opctx` actor accessors with no shims, so mod-users does not build from
  mod-core's phase 1 merge until this phase lands; that is expected.
- Downstream participants that import `usersdb.UserAccount`, `auth.UserResolver` and related
  symbols break when this phase merges and migrate in their own phases (API surface section 10).
