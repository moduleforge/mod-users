# SSH Public Keys

## Purpose and scope

This plan gives `mod-users` SSH public-key ownership: Capability 1 of `app-mfgit`'s handoff document, `/Users/zane/playground/moduleforge/app-mfgit/docs/handoffs/mod-users-ssh-extension.md`. That handoff is the authoritative requirements source.

Today mod-users has no SSH-key capability at all. That gap is the single hard blocker on real git push/clone/fetch against `app-mfgit`. `mod-repos`' git-over-SSH transport has shipped, but app-mfgit composes it with a fail-closed `transport.KeyResolver` that refuses every key.

**What must change**, entirely inside this checkout:

1. **Storage.** Store SSH public keys against a `UserAccount`, several per user, in a new module-owned table, `mod_users.ssh_public_keys`.
2. **Lookup.** Provide a constant-time fingerprint-to-actor lookup, safe to call once per SSH connection. It resolves to the **account-holder entity id** (`user_accounts.account_holder`), never the user-account id or UUID.
3. **Lifecycle API.** Serve register, list, and revoke over `/v1`, both self-service (`/v1/self/ssh-keys`) and operator-on-behalf-of (`/v1/user-accounts/{uuid}/ssh-keys`).
4. **Duplicates.** A key is globally unique among active keys, enforced in the schema, so it can never authenticate as two users.
5. **Immediate revocation.** Revocation archives the row. Resolution is uncached and excludes archived keys and archived account holders, so a revoked key stops authenticating on the very next connection.
6. **Key-type policy.** A stated algorithm acceptance policy refuses weak or deprecated key types at registration.
7. **Consumer shape.** A Go resolver whose shape makes app-mfgit's `transport.KeyResolver` adapter trivial.

Every design decision is recorded, with its rationale and the convention or evidence that settles it, in the [SSH key design note](./notes/ssh-key-design.md). Task documents reference that note as the fixed contract and do not restate it.

**What must not change:**

- the existing `Authorizer` contract
- existing routes and their middleware
- existing tables, which stay in `public` and are not migrated to `mod_users`
- the `anonymousActor` / `resolveActorOrAnonymous` mechanism

**Out of scope:**

- any HTTPS git credential path
- any SSH server, transport, or command dispatch (mod-repos owns these, and they have shipped)
- any "may this identity push" authorization (mod-repos' `DecoratingAuthorizer` owns that)
- Capability 2 of the handoff (the anonymous read identity, which has already shipped)
- a key-management GUI in `@moduleforge/users-gui`
- fixing the pre-existing `/v1/users` vs `/v1/user-accounts` drift in `api/openapi.yaml` and the spec

**Success criteria:**

- A user with a verified email can register an allowed key and then list and revoke it through `/v1/self/ssh-keys`. Registration and revocation are step-up-gated when `AUTH_REQUIRE_STEP_UP` is on.
- An operator holding the appropriate grant can do the same for another user through `/v1/user-accounts/{uuid}/ssh-keys`.
- `usersservice.SSHKeyResolver.ResolveActor` returns the account-holder entity id for an active key.
- For an unknown key, a revoked key, or a key whose account holder is archived, `ResolveActor` returns `usersservice.ErrUnknownSSHKey`, identically in all three cases. The call immediately after a revocation already sees it.
- A second registration of an active key, by any account, is refused with `409` and never produces a second active row.
- Refused algorithms and malformed input are rejected with `400` and the design note's detail codes.
- Every mutation is audited.
- `make build`, `make test`, and `make lint` stay green, apart from the documented pre-existing `lint.model` shadow-db gap (followup `nAXW`). The new integration tests run against the dev Postgres.

**Downstream context, not work in this plan.** Once this ships, `app-mfgit` writes a one-function adapter from `usersservice.SSHKeyResolver` to `transport.KeyResolver`: it passes the `charm.land/ssh` key through, since that type embeds `x/crypto/ssh.PublicKey`, and maps `ErrUnknownSSHKey` to `transport.ErrUnknownKey`. It then replaces its fail-closed resolver. That is separate, later, cross-repo work in app-mfgit, and no task here builds it.

## Current status

Nothing is implemented yet. Execution starts with Phase 01. Its tasks 001, 002, and 005 have no prerequisites and can start immediately, in parallel.

Pre-conditions and known environment caveats that implementers should expect:

- `lint.model`'s shadow-db lint may fail applying `0100_schema.sql` without mod-core migrations (followup `nAXW`, pre-existing and not caused by this plan).
- On this host, native Homebrew Postgres can shadow the dev container on `localhost:5432` (followup `EMIS`).
- Worktrees need `make preflight` for the sibling-module `replace` symlinks (AGENTS.md "Working in worktrees").
- The vendored `docs/mf-standards` submodule is pinned before the schema-ownership standard existed. The canonical standard is at `/Users/zane/playground/moduleforge/docs-mf-standards/`.

## Overview

### Phase 01: SSH public-key ownership

A single coherent feature slice across `model/` and `api/`, delivered as five tasks. Dependency order:

- `001` and `002` are independent of each other.
- `003` depends on both `001` and `002`.
- `004` depends on `003`.
- `005` depends only on the design note and can run at any time.

Parallel-eligible groups:

- first wave: `001`, `002`, and `005` together
- second wave: `003`
- third wave: `004`

Tasks:

1. **[001: Add SSH Key Storage](./phase-01-ssh-public-keys/001-add-ssh-key-storage.md)** (`sonnet-med`): migration `0103_ssh_public_keys.sql` creating the `mod_users` schema and the `mod_users.ssh_public_keys` table with its partial unique fingerprint index, sqlc queries for insert, list, get-by-UUID, archive, and resolve, and regenerated `model/db/`.
2. **[002: Add SSH Key Parsing Policy](./phase-01-ssh-public-keys/002-add-ssh-key-parsing-policy.md)** (`sonnet-med`): a pure Go package that parses one `authorized_keys` line, applies the algorithm acceptance policy, and produces the canonical key, type, fingerprint, and default label, with table-driven unit tests.
3. **[003: Add SSH Key Service And Resolver](./phase-01-ssh-public-keys/003-add-ssh-key-service-and-resolver.md)** (`sonnet-high`): `SSHKeyService` (authorize-first, audited Register/List/Revoke) and the side-effect-free `SSHKeyResolver`, re-exported through `api/usersservice`, plus DB-backed integration tests for uniqueness, revocation immediacy, and account archival.
4. **[004: Add SSH Key HTTP Routes](./phase-01-ssh-public-keys/004-add-ssh-key-http-routes.md)** (`sonnet-med`): a thin `SSHKeysHandler`, self-service read and write route groups with step-up gating, operator routes, facade re-exports in `api/handlers/handlers.go`, `moduleforge.module.yaml` service and route entries, and the hand-written `api/cmd/server/main.go` wiring.
5. **[005: Document SSH Key Endpoints In OpenAPI](./phase-01-ssh-public-keys/005-document-ssh-key-endpoints-in-openapi.md)** (`sonnet-low`): the six endpoints and the key schema in `api/openapi.yaml`, reusing the existing `Error`/`FieldError`/`Action` schemas.

### Phase 02: Documentation updates

1. **[001: Update Architecture Docs](./phase-02-doc-updates/001-update-architecture-docs.md)** (`sonnet-high`): reflect the new table, schema, resolver service, routes, and key policy in `docs/architecture.md` and `docs/mod-users-spec.md`. Runs after Phase 01 lands.
