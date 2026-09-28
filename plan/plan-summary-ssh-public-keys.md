# Plan Summary: ssh-public-keys

## What was planned and why

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

1. **[001: Update Architecture Docs](./phase-02-doc-updates/001-update-architecture-docs.md)** (`sonnet-med`): reflect the new table, schema, resolver service, routes, and key policy in `docs/architecture.md` and `docs/mod-users-spec.md`. Runs after Phase 01 lands.

## What shipped

### Phase 01 — SSH Public-Key Ownership

1. **Add SSH Key Storage** (`001-add-ssh-key-storage.md`, tier `sonnet-med`) — Added model/migrations/sql/0103_ssh_public_keys.sql (mod_users schema + ssh_public_keys table with partial unique fingerprint index) and model/queries/ssh_public_keys.sql (six schema-qualified queries). sqlc generate clean, go build/make build.api pass. DB-backed validation not runnable in this worktree (documented, not claimed passing). lint.model fails only with pre-existing followup nAXW.
   Commit `2e487c3`, merged at `d67ad82`.

2. **Add SSH Key Parsing Policy** (`002-add-ssh-key-parsing-policy.md`, tier `sonnet-med`) — Implemented api/internal/sshkey/ per D6/D7: Parse() enforces size cap, rejects options/multi-key, enforces positive key-type allow-list, refuses weak RSA. NormalizeLabel implements label rules. Table-driven tests cover all paths. Folded in an unrelated lint-script line-drift fix and a defense-in-depth label-size guard.
   Commit `3a947ef`, merged at `ebf1289`.

3. **Add SSH Key Service And Resolver** (`003-add-ssh-key-service-and-resolver.md`, tier `sonnet-high`) — Implemented SSHKeyService (Register/List/Revoke, authorize-first, audited) and side-effect-free SSHKeyResolver.ResolveActor, plus usersservice facade re-exports. Fixed a pre-existing unrelated compile break. Full unit + 8 new integration tests against real Postgres, 42/42 passing. Inline security review found no blocking issues.
   Commit `ad76ea3`, merged at `38eee77`.

4. **Add SSH Key HTTP Routes** (`004-add-ssh-key-http-routes.md`, tier `sonnet-med`) — Implemented the full SSH-key HTTP surface (self-service + operator register/list/revoke, step-up gating on self routes only) consuming task 003's service directly, wired through manifest/facade/dev server, response shapes matching task 005's openapi.yaml. go test/build/lint all green. Inline security review: no findings.
   Commit `003eddc`, merged at `35ca4f8`.

5. **Document SSH Key Endpoints In OpenAPI** (`005-document-ssh-key-endpoints-in-openapi.md`, tier `sonnet-low`) — Documented the six SSH-key endpoints (self + operator list/register/revoke) and their schemas in api/openapi.yaml per design-note D6-D10, reusing existing Error/Action/bearer-auth building blocks. Caught and fixed a real path-parameter-name mismatch (AccountUUID vs UserUUID) via the OpenAPI linter. 0 lint errors, same 10 pre-existing warnings as baseline.
   Commit `1f5ccb0`, merged at `3a67001`.

6. **Fix Operator SSH-Key Step-Up Bypass** (`006-fix-operator-ssh-key-step-up-bypass.md`, tier `sonnet-med`) — Closed phase-1 gate finding security-001: RegisterForAccount/RevokeForAccount now resolve the caller's own account UUID and apply the identical step-up gate the self routes use whenever the operator route's path {uuid} equals it; genuine operator-on-behalf-of-another-account calls are unchanged. Added handler-level tests for both cases. All requirements and validation checks pass.
   Commit `d5a1681`, merged at `1873cdc`.

### Phase 02 — Documentation Updates

1. **Update Architecture Docs** (`001-update-architecture-docs.md`, tier `sonnet-med`) — Delivered both the base doc-update scope and the phase-01 gate's architecture findings: new docs/architecture/ssh-keys.md promoting D1/D2/D3/D4/D9/D11 with rationale, a new 'Key decisions' section in architecture.md, a dangling-citation repoint sweep across 8 files, and correction of the stale 'operator routes never step-up-gated' claim in 4 files. Doc/comment/spec only, no behavior change; build/vet/test/YAML validation all pass.
   Commit `afee1a1`, merged at `a8f73dd`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Follow-up items

- **`AAU0`** — **Database-backed Validation checks (goose appl** — Database-backed Validation checks (goose apply/\d/23505 duplicate/archive-reinsert) could not be executed in this worktree — no reachable, correctly-migrated dev Postgres. Recommend running them from the main checkout before this task is considered fully verified, or accepting the schema/query-level static checks as sufficient given they match the design note's contract exactly.

- **`sl4d`** — **Incidental observation, not fixed: the alread** — Incidental observation, not fixed: the already-running users-module-postgres Docker container has a stale goose_db_version table from an older, pre-per-module-migration-scheme setup, unrelated to this task's diff.

- **`LkIi`** — **[security, non-blocking] ResolveActor's unkno** — [security, non-blocking] ResolveActor's unknown/revoked-key branch is measurably faster than its archived-holder/success branches (1 vs 2 DB queries) — a theoretical timing side-channel. Per mod-repos' KeyResolver doc comment, equalizing observable timing across rejection reasons is the transport (caller) layer's documented responsibility, not this resolver's; no action taken.

- **`Jtw6`** — **[security/design, non-blocking] SSHKeyResolve** — [security/design, non-blocking] SSHKeyResolver's constructor is pinned to the task doc's Requirement 8 signature (db.Querier, coredb.Querier), so the 'no writes' guarantee is enforced by convention/tests, not the compiler. A narrower read-only interface would make this compiler-enforced but was out of this task's scope.

- **`0m8H`** — **[process, non-blocking] Reused an already-doc** — [process, non-blocking] Reused an already-documented but not-yet-promoted environment workaround (from git history around followup EMIS) to run the integration suite despite a host Postgres port conflict. Surfacing in case the manager wants this workaround promoted into AGENTS.md or a shared script.

- **`P3rk`** — **Operator update op grants credential-install** — [phase-01 gate, arch-003, minor] Operator Register/Revoke on SSH keys authorize via the generic 'update' op (api/internal/service/ssh_keys.go:131,226), which now also grants credential-install/impersonation-grade capability (SSH git access) to any principal holding only 'update' on a user, not just the impersonation-grade 'assume'/'login' ops. Practical escalation is already limited since 'update' can also change email today (see the separate followup on that). Consider authorizing operator SSH-key register/revoke against 'assume' instead, or record this as an accepted trade-off in docs/architecture.md's key decisions once that section exists.

- **`tbmN`** — **SSH-key routes mask 403, sibling routes 404** — [phase-01 gate, arch-004, minor] /v1/user-accounts/{uuid}/ssh-keys returns masked 403 for an unknown/unpermitted account UUID, while sibling /v1/user-accounts/{uuid} routes return 404. Masking adds no confidentiality here since the parent route already leaks existence. Either record as a deliberate new-routes-vs-legacy-routes inconsistency in architecture docs, or open a follow-up to unify (move legacy routes to masked 403 rather than unmasking the new ones).

- **`o1uH`** — **Revoke does a redundant SELECT before UPDATE** — [phase-01 gate, efficiency-001, suggestion] SSHKeyService.Revoke issues a separate SELECT to build the audit 'before' snapshot, then a second query to archive the row. Change ArchiveSSHPublicKey from :execrows to :many/:one with a RETURNING clause (mirroring InsertSSHPublicKey's :one+RETURNING pattern) so Revoke builds its not-found check and audit snapshot from the UPDATE's own returned row, removing one round trip per revoke call.

- **`433h`** — **loadAuthorizedAccount doesn't mask ErrNoRows** — [phase-01 gate, correctness-001, suggestion] SSHKeyService.loadAuthorizedAccount treats a missing account-holder entity (pgx.ErrNoRows from GetEntityByID) as an opaque 500, while the sibling SSHKeyResolver.ResolveActor masks the identical case defensively (unreachable under the FK today, but masked anyway). For consistency, consider mapping ErrNoRows here to the same masked 403 the archived-holder branch already returns. Optional hardening, not required by task 003's own Requirements.

- **`cJcZ`** — **SSH keys on guest/unverified accounts** — [phase-01 gate, arch-006, suggestion] Self-service SSH-key registration requires a verified email, but the operator path applies no such rule about the target account, so an operator can install a standing SSH credential on a guest (email-NULL) or unverified account. Whether that's intended is an undocumented policy decision — either refuse it explicitly in SSHKeyService.Register or document it as intended.

- **`AWmf`** — **mod_users schema needs a GRANT note** — [phase-01 gate, arch-007, suggestion] The 0103_ssh_public_keys.sql migration is the first real table under a mod_* schema in the ecosystem and deliberately carries no GRANT, per the schema-ownership standard. This assumes the composing app's runtime role already has USAGE/table privileges on mod_users. Nothing documents this operational requirement for composing hosts, or that backup/reset/test tooling scoped to 'public' will miss this table. Add a sentence to docs/architecture.md when the schema decision is recorded (see doc-updates phase).

- **`GrC7`** — **SSH-key label allows control/bidi chars** — [phase-01 gate, security-002, minor] NormalizeLabel (api/internal/sshkey/label.go) only bounds length, not content: a caller-supplied label containing NUL, other control chars, or Unicode bidi overrides passes validation, and NUL then causes Postgres to reject the INSERT as a 500 instead of a 400. Stored control/bidi chars are echoed in API responses and audit snapshots (a display-spoofing vector). Reject unicode.IsControl runes and bidi formatting chars (U+202A-U+202E, U+2066-U+2069) in caller-supplied labels with a new 400 detail code; strip (don't reject) them from comment-derived default labels. Add table-driven test cases (NUL, newline, U+202E).

- **`endf`** — **Orphan docs: model/local-deploy/next-steps** — [phase-01 gate, documentation-001/002/003, major but pre-existing — not caused by this phase's diff] Link-chain check found three non-exempt project docs unreachable from README.md: model/README.md (not linked from any doc), deploy/local/README.md (mentioned in docs/architecture.md only as plain code text, not a Markdown link), and next-steps.md (not linked anywhere). Fix: link model/README.md from docs/project-structure.md or AGENTS.md; convert the deploy/local/README.md mention in docs/architecture.md to an actual Markdown link; link next-steps.md from README.md's "Additional documentation" section.

- **`oti2`** — **Orphan docs: k8s/serverless/CLAUDE stubs** — [phase-01 gate, documentation-004/005/006, minor, pre-existing — not caused by this phase's diff] deploy/k8s/README.md and deploy/serverless/README.md (one-line "Populated in Phase 8" stubs) and api/server/CLAUDE.md (an empty claude-mem-context stub, not covered by the .claude/ exemption since it lives outside that directory) are all unreachable from README.md. Link the two deploy READMEs from docs/project-structure.md's deploy/ section; either populate api/server/CLAUDE.md with real guidance and link it, or remove it if committed unintentionally.

- **`WHKp`** — **Ownership arm extends SSH access to owners** — [phase-01 gate, security lens flagged_for_manager, needs a decision] The Authorizer's ownership arm means any entity that owns another natural_person entity (owner_id not self) can register SSH keys on that person's account and thus authenticate as them over git — an existing authorization pattern that now reaches a new, persistent impersonation capability. Confirm whether such non-self ownership relationships occur in practice for natural_person entities, and whether this is intended for SSH-key registration specifically.

- **`9Abg`** — **Documentation drift (pre-existing, explicitly** — Documentation drift (pre-existing, explicitly out of this task's scope): api/openapi.yaml (~lines 1628-1629, 1679-1680) and plan/notes/ssh-key-design.md (line 124) both state unqualified that 'operator routes are not step-up-gated' — no longer fully accurate now that self-targeting is gated. Left untouched per the task's scope boundary.

- **`trmJ`** — **Integration suite (ssh_keys_integration_test.** — Integration suite (ssh_keys_integration_test.go's TestInteg_SSHKeys_OperatorAuthorization) was not extended to cover the self-targeting-via-operator-route case — service-layer test, requires live Postgres, not required by the task.

- **`Rjag`** — **Stale "never step-up-gated" code comments** — [phase-01 gate, fix-confirmation re-review of task 006] After task 006's step-up-bypass fix, comments in api/cmd/server/main.go:657-659, api/handlers/handlers.go:221-223, and moduleforge.module.yaml:420 still say the operator SSH-key routes are "never"/"not" step-up-gated. They should say the routes are gated when the path UUID is the caller's own account, so a future maintainer doesn't read the stale comment as license to remove the gate. Doc-only, no current security impact.

- **`1mnX`** — **Assume-identity bypasses self step-up gate** — [phase-01 gate, fix-confirmation re-review of task 006, informational, not a regression] During an assume-identity (sudo) session, uc.UserUUID is the admin's own UUID while the Authorizer's effective actor is the impersonated user X. Task 006's new isSelfTargetingOperatorCall check compares the path UUID against uc.UserUUID, so an admin impersonating X can call the operator SSH-key route on X's own UUID with no step-up — isSelf is false even though the Authorizer treats the caller as X via ownership. Not a regression (this path was already fully ungated before task 006, and the same admin token can already mutate X's keys via the designed wildcard-grant operator path) — recorded for completeness in case assume-identity sessions are later brought under step-up requirements generally.

- **`tB6v`** — **Three files still carry bare design-note Dn c** — Three files still carry bare design-note Dn citations to the soon-to-be-torn-down plan/notes/ssh-key-design.md, deliberately left untouched (outside scope, cite non-promoted letters D6/D7/D8): api/internal/sshkey/label.go:20, api/internal/sshkey/sshkey.go:2,26,54,117 (also a stale 'task 003' reference), api/internal/handlers/identities.go:639. Will dangle once the design note is torn down.

- **`6SBq`** — **docs/mf-standards submodule is uninitialized** — docs/mf-standards submodule is uninitialized in this worktree, so pre-existing ./mf-standards/... links could not be click-verified, only pattern-matched. Predates this task.

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — SSH Public-Key Ownership

- [x] [001-add-ssh-key-storage.md](./phase-01-ssh-public-keys/001-add-ssh-key-storage.md) — tier `sonnet-med` · branch `plan/ssh-public-keys-01-001` · commit `2e487c3` · merge `d67ad82`
- [x] [002-add-ssh-key-parsing-policy.md](./phase-01-ssh-public-keys/002-add-ssh-key-parsing-policy.md) — tier `sonnet-med` · branch `plan/ssh-public-keys-01-002` · commit `3a947ef` · merge `ebf1289`
- [x] [003-add-ssh-key-service-and-resolver.md](./phase-01-ssh-public-keys/003-add-ssh-key-service-and-resolver.md) — tier `sonnet-high` · branch `plan/ssh-public-keys-01-003` · commit `ad76ea3` · merge `38eee77`
- [x] [004-add-ssh-key-http-routes.md](./phase-01-ssh-public-keys/004-add-ssh-key-http-routes.md) — tier `sonnet-med` · branch `plan/ssh-public-keys-01-004` · commit `003eddc` · merge `35ca4f8`
- [x] [005-document-ssh-key-endpoints-in-openapi.md](./phase-01-ssh-public-keys/005-document-ssh-key-endpoints-in-openapi.md) — tier `sonnet-low` · branch `plan/ssh-public-keys-01-005` · commit `1f5ccb0` · merge `3a67001`
- [x] [006-fix-operator-ssh-key-step-up-bypass.md](./phase-01-ssh-public-keys/006-fix-operator-ssh-key-step-up-bypass.md) — tier `sonnet-med` · branch `plan/ssh-public-keys-01-006` · commit `d5a1681` · merge `1873cdc`

### Phase 02 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-02-doc-updates/001-update-architecture-docs.md) — tier `sonnet-med` · branch `plan/ssh-public-keys-02-001` · commit `afee1a1` · merge `a8f73dd`
