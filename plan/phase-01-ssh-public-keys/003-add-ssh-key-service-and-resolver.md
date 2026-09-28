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
