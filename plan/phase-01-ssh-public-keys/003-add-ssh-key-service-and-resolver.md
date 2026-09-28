# Add SSH Key Service And Resolver

## Purpose and scope

Build the business layer of the SSH key capability: `SSHKeyService` (Register, List, Revoke) and `SSHKeyResolver` (`ResolveActor`). Re-export both through the public facade `api/usersservice`, and prove the security-relevant behavior against a real Postgres. This task produces the Go shape app-mfgit's future `transport.KeyResolver` adapter will wrap. It adds no HTTP handlers, routes, or manifest entries; that is task 004.

No standard skill covers this. The contract is the [SSH key design note](../notes/ssh-key-design.md), especially D2–D5 and D8–D11.

**Depends on:** task 001 (`model/db` queries, and the generated model type name recorded in its Status) and task 002 (`api/internal/sshkey`).

## Requirements

### `SSHKeyService`

Put this in `api/internal/service/ssh_keys.go`.

1. **Constructor.** Model it on `NewUserAccountService`. It takes:
   - a transaction-capable DB handle (`txhelper.DB`)
   - the users `db.Querier`
   - the `coredb.Querier`
   - a `coreAuthz.Authorizer`
   - an `*observer.ObserverGroup`
2. **Methods.** Each method is keyed by the **target account's UUID**, so the self and operator route families share it. A handler passes `UserContext`'s account UUID or the path UUID.
   - `Register(ctx, accountUUID uuid.UUID, publicKeyLine string, label *string, stepUpUsed bool) (SSHKey, error)`
   - `List(ctx, accountUUID uuid.UUID, limit, offset int32) ([]SSHKey, int64, error)`, returning the keys and the total
   - `Revoke(ctx, accountUUID, keyUUID uuid.UUID, stepUpUsed bool) error`
3. **Order of work in every method** (AGENTS.md "Authorization is checked first"; design note D9):
   1. Load the `user_accounts` row by UUID. A miss returns the **masked** `ErrForbidden` (`apiresp.ErrForbidden`), not `ErrNotFound` (D10).
   2. Load the account holder entity via `coredb.GetEntityByID`. If it is archived, return the same masked `ErrForbidden`.
   3. Call `Authorize(ctx, op, &account_holder)`, with `read` for List and `update` for Register and Revoke. Propagate its error unchanged.
   4. Only then parse input, touch key data, and write.
4. **Register.**
   - Call `sshkey.Parse` and `sshkey.NormalizeLabel`, mapping each task 002 sentinel to `apiresp.InvalidInput(apiresp.FieldError{Field: ..., Code: "users.ssh_key_*", ...})` per D6/D7.
   - Insert inside `txhelper.Run`. If the insert fails with a Postgres unique violation (`23505`) on `ssh_public_keys_active_fingerprint_uq`, return `apiresp.Conflict(apiresp.FieldError{Field: "public_key", Code: "users.ssh_key_in_use", ...})`. The response must be identical whether the existing holder is the caller or someone else (D2). Do not add a Go pre-check as the enforcement.
   - Audit with `obs.Observe(ctx, tx, "create", "ssh_public_key", &accountHolder, nil, after)` inside the transaction and `ObserveAfterCommit` after it. `after` carries `{uuid, fingerprint, key_type, label, step_up}`. Follow the pattern in `api/internal/handlers/identities.go`, including its `safeObserve` nil-guard.
5. **Revoke.**
   - Load the active key via `GetActiveSSHPublicKeyByUUIDForUserAccount` for the before-snapshot. A miss (unknown UUID, another account's key, or already revoked) returns the masked `ErrForbidden`.
   - Archive with `ArchiveSSHPublicKey` in the same transaction. Zero rows affected is the same masked `ErrForbidden`.
   - Audit `delete` / `ssh_public_key` with the before-snapshot and `step_up`.
6. **List** returns active keys only, with total, paginated. Clamp `limit`/`offset` to the module's list conventions: default 20, max 200.
7. **`SSHKey` output type.** Fields: `UUID`, `KeyType`, `Fingerprint`, `PublicKey` (canonical), `Label`, `CreatedAt`. No internal ids.

### `SSHKeyResolver`

Put this in `api/internal/service/ssh_key_resolver.go` (D4, D5, D11).

8. **Constructor.** `NewSSHKeyResolver(q db.Querier, coreQ coredb.Querier) *SSHKeyResolver`.
9. **`ResolveActor(ctx context.Context, key gossh.PublicKey) (int64, error)`**, where `gossh` is `golang.org/x/crypto/ssh`:
   - A `nil` key returns `ErrUnknownSSHKey`.
   - Compute `sshkey.Fingerprint(key)` and `sshkey.Canonical(key)`, then call `ResolveActiveSSHPublicKey`.
   - `pgx.ErrNoRows` returns `ErrUnknownSSHKey`.
   - Then call `coredb.GetEntityByID(account_holder)`. An archived holder, or a missing entity row, returns `ErrUnknownSSHKey`.
   - Any other error returns wrapped with `%w`, and must *not* satisfy `errors.Is(err, ErrUnknownSSHKey)`.
   - Success returns `account_holder`.
10. **Hard constraints** on `ResolveActor`:
    - no `Authorize` call and no actor required on `ctx`
    - **no writes of any kind** (D7: the consumer calls it for unauthenticated probe keys)
    - no caching
    - no logging of key material

    State all four in its doc comment, together with the rationale: it is a pre-authentication credential check, and the consumer calls it per candidate key.
11. **Sentinel.** Define `var ErrUnknownSSHKey = errors.New(...)`. Use one sentinel for unknown, revoked, and archived-holder keys, with no distinguishing wrapping between those three cases.

### Public facade

12. In `api/usersservice/service.go`, add:
    - type aliases `SSHKeyService`, `SSHKey`, and `SSHKeyResolver`
    - thin wrapper constructors `NewSSHKeyService` and `NewSSHKeyResolver`
    - `var ErrUnknownSSHKey = inner.ErrUnknownSSHKey`, re-exported as the same value so `errors.Is` matches across the facade

    Add a compile-time assertion that `*SSHKeyResolver` has the method `ResolveActor(context.Context, gossh.PublicKey) (int64, error)`, for example via a local one-method interface. This change set touches **no** manifest entries. Task 004 adds `sshKeyService` and `sshKeyResolver` to `moduleforge.module.yaml` and must reference exactly these facade symbols.

### Tests

13. **Unit tests** (`go test`, no DB), using stub queriers and a stub authorizer in the style of `user_accounts_upgrade_test.go`'s `allowAllAuthorizer`:
    - Authorize is called before any key query.
    - An authorization denial propagates unchanged.
    - A missing account returns masked `ErrForbidden`.
    - Each parse sentinel maps to its detail code.
    - A `23505` maps to `users.ssh_key_in_use`.
    - Revoke with zero rows affected returns masked `ErrForbidden`.
    - Resolver: `nil` key, a no-rows lookup, and an archived holder each return `ErrUnknownSSHKey`.
    - Resolver: a DB error does not satisfy `errors.Is(..., ErrUnknownSSHKey)`.
    - Resolver: the stub records no write call.
14. **Integration tests** (`//go:build integration`) against real Postgres. Preferred location is `api/internal/authz/ssh_keys_integration_test.go`, which reuses that package's existing `TestMain`/`resetDB`/`wireServices`/`seedUser`/`actorCtx` harness and real `Authorizer`, so the existing `make -C api test.integration` target runs it unchanged. If an import cycle or harness mismatch prevents that, create a dedicated package and extend the `test.integration` target in `api/Makefile` minimally to include it. Keep the existing scoping comment accurate. Cover:
    - Self-registration succeeds for the owning user, and `ResolveActor` returns that user's `account_holder`.
    - A second active registration of the same key, by the same user or a different user, returns `users.ssh_key_in_use`, and exactly one active row exists.
    - After `Revoke`, the very next `ResolveActor` returns `ErrUnknownSSHKey`, and the same key can then be registered again.
    - After `UserAccountService.Delete` archives the holder, `ResolveActor` returns `ErrUnknownSSHKey`.
    - A different, non-admin user cannot List, Register, or Revoke for another account (`ErrForbidden`), while a wildcard-`manage` user can.
    - An unknown key returns `ErrUnknownSSHKey`.
    - Audit rows are written for register and revoke, if the harness wires the real observer group; otherwise assert via a recording observer.

## Validation

- `cd api && go test ./internal/service/... ./internal/sshkey/... ./usersservice/...` passes.
- `make -C api test.integration` passes with the new tests **actually executed**, not skipped: dev Postgres up per AGENTS.md and followups `EMIS`/`MwXo`. Paste the `go test -v -run SSH` summary lines, or equivalent, into Status. If prerequisites are unavailable and the suite skips, report `partial` and say so.
- `make build.api` and `make lint.api` pass.
- `grep -n "Authorize\|Observe\|Exec\|Insert\|Archive\|Touch\|Update" api/internal/service/ssh_key_resolver.go` shows no authorization call and no write query.
- `git diff --stat` shows no changes to `moduleforge.module.yaml`, `api/handlers/`, or `api/cmd/`.

## Metadata

architectural_impact: true

## Assumptions

- Task 001 has landed. Its Status records the sqlc model type name (`ModUsersSshPublicKey` or a renamed form) and any sqlc quirk.
- Task 002 has landed with the sentinels and helpers named in its Requirements.
- `coredb.GetEntityByID` returns `archived_at` (verified in `mod-core/model/queries/entities.sql`).
- `apiresp.InvalidInput`, `apiresp.Conflict`, and `apiresp.FieldError` exist in `mod-core/api/apiresp` (verified).
- The `Authorizer` own-arm lets a natural person act on their own `account_holder` entity for `read` and `update`, so self-service needs no grant.

## Status

**Outcome:** succeeded. Date: 2026-09-28.

- Created `api/internal/service/ssh_keys.go`: `SSHKeyService` with `Register`/`List`/`Revoke`, each keyed by the target account's UUID, sharing a `loadAuthorizedAccount` helper that implements the common order of work (load `user_accounts` by UUID -> masked `apiresp.ErrForbidden` on miss; load the account holder entity via `coredb.GetEntityByID` -> masked `ErrForbidden` if archived; `Authorize(ctx, op, &accountHolder)`, error propagated unchanged; only then touch key data). `Register` calls `sshkey.Parse`/`sshkey.NormalizeLabel` and maps each task-002 sentinel to its `users.ssh_key_*` detail code (`mapSSHKeyParseError`/`mapSSHKeyLabelError`); inserts inside `txhelper.Run` with no Go pre-check for uniqueness, mapping a `23505` on `ssh_public_keys_active_fingerprint_uq` to `apiresp.Conflict(users.ssh_key_in_use)` identically regardless of who holds the existing key (D2); audits `create`/`ssh_public_key` via `Observe`+`ObserveAfterCommit` with `{uuid, fingerprint, key_type, label, step_up}`, through a `safeObserve`/`safeObserveAfterCommit` nil-guard pair mirroring `identities.go`. `Revoke` loads the before-snapshot via `GetActiveSSHPublicKeyByUUIDForUserAccount` (masked `ErrForbidden` on miss), archives via `ArchiveSSHPublicKey` inside a transaction, and masks a zero-rows race to the same `ErrForbidden`. `List` clamps `limit`/`offset` to the module's default-20/max-200 convention and returns active keys plus the total.
- Created `api/internal/service/ssh_key_resolver.go`: `SSHKeyResolver.NewSSHKeyResolver(q, coreQ)` and `ResolveActor(ctx, gossh.PublicKey) (int64, error)`. A `nil` key, a `pgx.ErrNoRows` from `ResolveActiveSSHPublicKey`, and a missing-or-archived account holder entity all return the single sentinel `ErrUnknownSSHKey`, indistinguishably. Any other error is wrapped with `%w` and does not satisfy `errors.Is(err, ErrUnknownSSHKey)`. The doc comment states all four hard constraints (no `Authorize`, no writes, no caching, no key-material logging) with rationale. `grep -n "Authorize\|Observe\|Exec\|Insert\|Archive\|Touch\|Update" api/internal/service/ssh_key_resolver.go` shows exactly two matches, both benign: the doc-comment line stating "No Authorize call" and the `entity.ArchivedAt` field read (not a call) — no authorization call and no write query exist in the file.
- Updated `api/usersservice/service.go`: added `SSHKeyService`/`SSHKey`/`SSHKeyResolver` type aliases, `NewSSHKeyService`/`NewSSHKeyResolver` thin wrappers, `var ErrUnknownSSHKey = inner.ErrUnknownSSHKey` (same value, so `errors.Is` matches across the facade), and a local one-method `sshKeyResolverActor` interface asserting `*SSHKeyResolver` satisfies `ResolveActor(context.Context, gossh.PublicKey) (int64, error)` without importing mod-repos. No manifest, handler, or `api/cmd/` changes — `git diff --stat` confines the change to files under `api/internal/service/`, `api/internal/authz/` (new integration test), and `api/usersservice/`.
- **Pre-existing compile break fixed (required for this task's own `go test ./internal/service/...` to run at all):** `api/internal/service/user_accounts_upgrade_test.go`'s `stubUAQuerier` did not implement the six `db.Querier` methods task 001 added (`ArchiveSSHPublicKey`, `CountActiveSSHPublicKeysByUserAccount`, `GetActiveSSHPublicKeyByUUIDForUserAccount`, `InsertSSHPublicKey`, `ListActiveSSHPublicKeysByUserAccount`, `ResolveActiveSSHPublicKey`), so `go vet ./internal/service/...` failed before any of this task's own code was added. Added the six trivial stub methods (returning zero values / `pgx.ErrNoRows`, consistent with the file's existing "not implemented" pattern) — none of task 001's own tests call them, so behavior is unchanged; only compilation is restored.
- **Unit tests** (`api/internal/service/ssh_keys_test.go`, `ssh_key_resolver_test.go`): stub `db.Querier`/`coredb.Querier` implementations that embed a nil interface (so any unoverridden method panics loudly if called — an intentional fail-fast tripwire, not a full 50/34-method manual stub) plus a dedicated `fakeSSHTx`/`fakeSSHDB` pair for the `db.New(tx)` calls `Register`/`Revoke` make inside `txhelper.Run`. Covers: Authorize called before any SSH-key query, with a denying authorizer's error propagated unchanged and no write reached, for all three methods; a missing account and an archived holder each returning masked `ErrForbidden` before `Authorize` is ever called; each `sshkey` parse/label sentinel mapping to its exact `{field, code}` (verified by routing the returned error through `apiresp.WriteError` and decoding the envelope, mirroring `user_accounts_anon_test.go`'s established pattern, since `apiresp` exposes no public detail-carrying accessor); a `23505` mapping to `users.ssh_key_in_use`; `Revoke` returning masked `ErrForbidden` on both a zero-rows-affected archive and an unknown key UUID; a happy-path `Register`/`List`. Resolver: nil key, a no-rows lookup, an archived holder, and a missing-entity-row lookup all return `ErrUnknownSSHKey`; a transient DB error from either the key lookup or the entity lookup does *not* satisfy `errors.Is(..., ErrUnknownSSHKey)`; every scenario asserts the stub's write tripwire (`writeCalled`) stays `false`.
- **Integration tests** (`api/internal/authz/ssh_keys_integration_test.go`, `//go:build integration`, package `authz_test`): landed in the preferred location, reusing `authz_integration_test.go`'s `TestMain`/`resetDB`/`wireServices`/`seedUser`/`actorCtx`/`integPool`/`integAZ` unchanged — no import cycle or harness mismatch, so `api/Makefile`'s existing `test.integration` target (`go test -tags=integration -p 1 ./internal/authz/...`) picks it up with no Makefile change. Covers, against a real `SSHKeyService`/`SSHKeyResolver` wired to `integPool`/`integAZ`: self-registration then resolution; a duplicate active registration by the same user and by a different user, both mapping to `users.ssh_key_in_use` with exactly one active row surviving (verified by direct SQL against `mod_users.ssh_public_keys`); revoke -> immediate resolve failure -> successful re-registration of the same key; archiving the account holder entity (via `coreQ.ArchiveEntity`, the same call `UserAccountService.Delete` makes internally — see the decision below) then observing `ResolveActor` fail; a non-admin denied `List`/`Register`/`Revoke` on another account while a wildcard-`manage` admin succeeds at all three; an unknown key resolving to `ErrUnknownSSHKey`; and audit rows recorded in-tx and post-commit for both register and revoke, via a recording `observer.MutationObserver`.
- **Environment note (do not rediscover), matching followup `EMIS`:** this host's native Homebrew `postgresql@14` intercepts `localhost:5432`/`127.0.0.1:5432` ahead of the `users-module-postgres` container's Docker port-forward, and the container's Docker-network IP (`172.21.0.2`) is not routable directly from the host (`dial tcp 172.21.0.2:5432: connect: operation timed out`), so a plain `AUTHZ_DEV_PG_HOST=localhost` invocation on the host cannot reach the intended container. Reused the documented, previously-successful workaround (see followup `EMIS`'s referenced task Status): ran `go test -tags=integration -p 1 -v ./internal/authz/...` inside an ephemeral `golang:1.26` container started with `--network container:users-module-postgres` (shares that container's network namespace, so `AUTHZ_DEV_PG_HOST=127.0.0.1` inside the test container reaches Postgres directly), with `/Users/zane/playground/moduleforge` and `/Users/zane/go` bind-mounted at their identical host paths (preserving `link-siblings.sh`'s absolute symlinks and `api/go.mod`'s relative `replace` directives and the Go module cache), a read-only Docker-socket mount plus a curl-installed static `docker` CLI (so `checkPrereqs`'s `docker inspect users-module-postgres` check runs for real), and `go install`ed `goose`. Also ran `make -C model compose` first (composed migrations dir did not yet exist in this worktree; it is gitignored, so nothing needed restoring). No host service or existing container was stopped, modified, or touched; the ephemeral container was `--rm`.
- **`go test -tags=integration -p 1 -v -run 'TestInteg_SSHKeys' ./internal/authz/...` summary** (8/8 passed, 0 failed):
  ```
  --- PASS: TestInteg_SSHKeys_SelfRegisterThenResolve (0.01s)
  --- PASS: TestInteg_SSHKeys_DuplicateRegistration_BySameUser_ReturnsConflict (0.00s)
  --- PASS: TestInteg_SSHKeys_DuplicateRegistration_ByDifferentUser_ReturnsConflict (0.00s)
  --- PASS: TestInteg_SSHKeys_RevokeThenResolveFails_ThenReRegisterSucceeds (0.00s)
  --- PASS: TestInteg_SSHKeys_ArchivedHolder_ResolveFails (0.00s)
  --- PASS: TestInteg_SSHKeys_OperatorAuthorization (0.00s)
  --- PASS: TestInteg_SSHKeys_UnknownKey_ResolveFails (0.00s)
  --- PASS: TestInteg_SSHKeys_AuditRowsWrittenForRegisterAndRevoke (0.00s)
  PASS
  ok  	github.com/moduleforge/mod-users/api/internal/authz	0.777s
  ```
  The full package suite (`go test -tags=integration -p 1 -v ./internal/authz/...`, 42 tests: 34 pre-existing + 8 new) also passed in full, confirming no regression against the pre-existing authz integration coverage.
- **`dependencies_installed` note:** the dispatch's `dependencies_installed` value was `not installed`, but the Go module cache (`/Users/zane/go/pkg/mod`) was in fact already populated and `go build ./...` succeeded on the first attempt with no network fetch, so the task proceeded per Phase 1 step 8 (dependencies were, in practice, available).
- **Decisions made (within task scope):**
  - Factored the common "load account -> load entity -> authorize" sequence into a private `loadAuthorizedAccount` helper shared by `Register`/`List`/`Revoke`, rather than repeating it three times — reduces the risk of the three methods drifting out of sync on the security-critical ordering.
  - Scenario 4's integration test archives the account holder entity via `coreQ.ArchiveEntity(ctx, entity.Uuid)` directly rather than constructing a full `UserAccountService` (which additionally needs a `NaturalPersonServicer` and a `types.Resolver` this scenario has no other use for) and calling its `Delete` method. This is the exact same underlying query `UserAccountService.Delete` calls internally, so it reproduces an identical DB-level effect.
  - Used a nil-embedded-interface pattern for the new unit-test `db.Querier`/`coredb.Querier` stubs (`stubSSHQuerier`, `stubCoreQuerier`) rather than the file-local convention of manually implementing every interface method (as `stubUAQuerier` does): the two interfaces have grown to 50 and 34 methods respectively, and `SSHKeyService`/`SSHKeyResolver` only ever call five and one of them; an unexpected call to any other method panics loudly (fail-fast), which is the desired outcome for a genuine bug, not something to suppress.
- **`[security self-fix]`** None needed — see the inline security review below.
- **Security review (`review_focus: ["security"]`), applied inline:** No blocking findings. Reviewed: authorize-first ordering (enforced by `loadAuthorizedAccount` and pinned by unit tests asserting the authorizer is called before any SSH-key query and that a denial leaves no write); SQL injection posture (every query is a sqlc-generated, parameterized call; no string-built SQL added by this task, including in the new integration test's one direct `SELECT count(*) ... WHERE fingerprint_sha256 = $1`); D10 anti-enumeration masking (unknown account, archived holder, unknown key UUID, another account's key UUID, and an already-revoked key all return the identical `apiresp.ErrForbidden`, verified in both unit and integration tests, including the same-user/different-user duplicate-registration cases returning an identical `users.ssh_key_in_use` response); D2's schema-enforced (not Go-pre-checked) global uniqueness with post-hoc `23505` mapping, closing the concurrent-registration TOCTOU race a Go-side pre-check would leave open; `ResolveActor`'s four hard constraints (no `Authorize`, no writes, no caching, no key-material logging), verified both by the task's required `grep` check and by unit tests asserting the stub querier's write tripwire is never tripped across every scenario, including success; error-message hygiene (every 5xx path is wrapped with `%w` and, per `apiresp.WriteError`'s own design, the client never sees the raw error text — only a fixed generic message — so no internal detail, such as a Postgres constraint name, can leak through a 500); and audit-snapshot minimality (the audit detail carries only `{uuid, fingerprint, key_type, label, step_up}`, never the raw canonical public-key text or any secret). Two non-blocking considerations, neither self-fixed nor requiring a task-doc-scope change, recorded in `flagged_for_manager` below.

## References

- [SSH key design note](../notes/ssh-key-design.md): D2–D5, D8–D11.
- `api/internal/service/user_accounts.go`: `Get`/`Update`/`Delete` show the load, Authorize, then transaction-plus-observe shape, and `Delete` shows how accounts are archived via their entity.
- `api/internal/handlers/identities.go`: `safeObserve`/`safeObserveAfterCommit` and the `step_up` audit-detail convention.
- `api/internal/authz/authz.go`: Authorize policy (wildcard, grant, or own).
- `api/internal/authz/authz_integration_test.go` and `anonymous_actor_integration_test.go`: the integration harness.
- `api/usersservice/service.go`: facade pattern.
- `/Users/zane/playground/moduleforge/mod-repos/api/transport/key_resolver.go` and `publickeyauth.go`: the consumer contract and the per-candidate-key call site that makes side-effect-freedom mandatory.

## Checkpoint hints

- After `SSHKeyService` and its unit tests pass.
- After `SSHKeyResolver` and its unit tests pass.
- After the facade re-exports compile.
- After the integration tests run green.
