# SSH Keys

## Purpose and scope

This document records the durable architectural decisions behind `mod-users`' SSH public-key ownership capability: the credential that lets a user authenticate a git-over-SSH session against a composing application (for example `app-mfgit`/`mod-repos`). See [Authentication flow](../architecture.md#authentication-flow) for how this credential fits alongside the module's other authentication channels, and [Multi-channel account model](../architecture.md#multi-channel-account-model) for how it relates to `user_accounts`.

The decisions below were made during the `ssh-public-keys` plan and are condensed here from that plan's design note (`plan/notes/ssh-key-design.md`), a transient planning artifact scoped to the plan's lifetime and torn down once the plan completes. This document is the decisions' durable home; code and manifest comments that once cited the design note by its `Dn` label now cite this document instead. Only the decisions with lasting architectural weight are covered here — schema placement, uniqueness, deletion, caching, authorization, and the consumer-facing Go shape. Narrower implementation detail (the accepted key-algorithm list, request/response shapes, masking rules, step-up gating) is covered in [docs/mod-users-spec.md](../mod-users-spec.md)'s Security requirements and API definition, and in [`api/openapi.yaml`](../../api/openapi.yaml).

## Table of contents

1. [D1: schema placement](#d1-schema-placement)
2. [D2: global uniqueness enforced in the schema](#d2-global-uniqueness-enforced-in-the-schema)
3. [D3: archive on revoke](#d3-archive-on-revoke)
4. [D4: no cache, immediate revocation](#d4-no-cache-immediate-revocation)
5. [D9: operator-on-behalf-of authorization](#d9-operator-on-behalf-of-authorization)
6. [D11: resolver consumer contract](#d11-resolver-consumer-contract)
7. [Related documents](#related-documents)

## D1: schema placement

`mod_users.ssh_public_keys` lives in a new `mod_users` Postgres schema, with every reference to it schema-qualified (`mod_users.ssh_public_keys`, `public.user_accounts`). It is the **first table in the ecosystem created under a `mod_*`-prefixed schema** (`mod-tokens` created an empty `mod_tokens` schema earlier, with no table in it yet).

**Rationale.** `docs-mf-standards/architecture/schema-ownership-design.md` sets the ecosystem-wide rule: a module's own tables live in `mod_<module>`, while the platform-canonical tables (`entities`, `types`, `user_accounts`) stay in `public`. `mod-users`' own ten pre-existing tables (`user_accounts`, `auth_local`, `auth_oidc_identities`, `anon_tokens`, and the rest — see [Data model](../architecture.md#data-model)) predate that standard and stay in `public`; moving them is out of scope for this capability. The rule going forward is therefore **mixed by design, not an inconsistency to resolve**: every pre-existing table stays in `public`, and every genuinely new module-owned table — starting with this one — goes in `mod_users`, schema-qualified.

**Operational consequence.** Because this is the first real `mod_*`-schema table in the ecosystem, it is also the first place two categories of tooling need updating that previously could assume "this module's tables are all in `public`": a composing application's runtime database role needs `USAGE` on the `mod_users` schema and table privileges on `mod_users.ssh_public_keys` (`SELECT` to resolve keys; also `INSERT`, `UPDATE`, and `USAGE` on the id sequence to serve the register and revoke routes; see [Data model](../architecture.md#data-model)) (the migration itself issues no `GRANT`, per `building-modules.md`'s grant conventions — the composing application's own provisioning owns that), and any backup, reset, or test-fixture tooling that enumerates "this module's tables" by scanning `public` alone will silently miss this table until it is taught to look in `mod_users` too.

## D2: global uniqueness enforced in the schema

A given SSH public key resolves to **at most one active row**, enforced by a partial unique index:

```sql
CREATE UNIQUE INDEX IF NOT EXISTS ssh_public_keys_active_fingerprint_uq
  ON mod_users.ssh_public_keys (fingerprint_sha256)
  WHERE archived_at IS NULL;
```

**Rationale.** A key must never silently authenticate as two different users — per-user uniqueness would let two accounts register the same key and make resolution ambiguous, and the handoff that originated this capability required the ambiguity not be left to the consuming transport. The invariant is enforced **in the schema, not by a Go pre-check**, because `building-modules.md`'s grants section establishes that a module is not the sole writer of its own tables — a Go-level uniqueness check alone would lose a concurrent-registration race, letting two callers both pass the check before either commits. The service maps the resulting Postgres `23505` unique violation to a `409 conflict` (`users.ssh_key_in_use`), identically whether the existing holder is the caller or a different account, so the response never reveals which account holds a contested key. Because the index is partial (`WHERE archived_at IS NULL`), a revoked key can be registered again later, by the same or a different user, as a new row with a new UUID — this index is also the resolver's lookup index (see [D4](#d4-no-cache-immediate-revocation)).

## D3: archive on revoke

Revoking a key sets `archived_at = now()` on its row rather than deleting it. Every read, and the resolver's lookup, filter on `archived_at IS NULL`.

**Rationale.** `schema-ownership-design.md`'s deletion rule applies: a module-owned domain row that another composing application may reasonably reference — for example a key UUID appearing in push-attribution or an audit view — is archived, never hard-deleted, because no single composing app can know whether a sibling app still depends on it. This is **unlike** `mod-users`' own older credential tables, `auth_oidc_identities` and `anon_tokens`, which hard-delete: those tables predate the archive-on-revoke standard and are not precedent for a new table. It is also unlike `mod-authz`'s grant rows, which are hard-deleted deliberately — a grant is `mod-authz`'s own internal bookkeeping that no other app's data hangs off, and a lingering revoked grant would itself be a permission-shaped record whose absence is the point (`schema-ownership-design.md`'s bookkeeping exception). An SSH key row is not permission-shaped in that sense — every read and the resolver already exclude an archived row — so the exception does not apply here.

## D4: no cache, immediate revocation

The resolver (`sshKeyResolver`, see [D11](#d11-resolver-consumer-contract)) issues a fresh, indexed query on every call. No process-level cache exists at any layer, and none may be added.

**Rationale.** A revoked key must stop authenticating on the very next SSH connection, not at the next token refresh — there is no token to refresh on the SSH path. A cache at any layer, even a short-lived one, would delay that by definition. The lookup also excludes a key whose account holder entity is archived (`public.entities.archived_at IS NOT NULL`), so archiving a user account cuts off git access immediately too, through the same mechanism. Revoking a key does **not** terminate an already-open SSH session; that is transport behavior belonging to `mod-repos`. "The next connection sees it" is the contract this module guarantees; anything about an already-open session is out of scope.

## D9: operator-on-behalf-of authorization

The operator routes (`/v1/user-accounts/{uuid}/ssh-keys[/{key_uuid}]`) authorize identically to the self-service routes: every `SSHKeyService` method calls `Authorizer.Authorize(ctx, op, &accountHolderEntity)` with the plain `update` operation (`read` for List), the same operation and target shape the existing `/v1/user-accounts/{uuid}` admin routes already use. There is no new operation slug or grant shape for SSH keys.

**Rationale.** Reusing the existing `update`-on-account-holder authorization avoids introducing a new grant vocabulary for a capability that is, from the `Authorizer`'s point of view, just another mutation on the account-holder entity. The accepted trade-off (tracked as finding arch-003 in the phase-01 boundary review) is that `update` on an account holder already carries other sensitive capability elsewhere in this codebase — it is not a capability scoped narrowly to "manage this account's SSH keys" — so a grant that authorizes an operator to update a user's account also authorizes them to manage that user's SSH keys. This is accepted as consistent with how every other `/v1/user-accounts/{uuid}` admin capability is already authorized, not treated as a gap this plan set out to fix.

**Step-up gating on the operator routes.** The `Authorizer`'s ownership arm means a natural person self-owns their own account-holder entity, so the same `Authorize` call that authorizes a genuine operator also succeeds when a caller targets their *own* account UUID through the operator route. The self-service routes (`/v1/self/ssh-keys`) enforce step-up (`X-Step-Up-Token`, when `AUTH_REQUIRE_STEP_UP` is on) at the HTTP handler layer, not in `SSHKeyService` — so an un-gated operator route reachable by a self-targeting caller would bypass step-up entirely for a standing credential, exactly what step-up exists to prevent. This was caught post-landing (phase-01 boundary review, finding `security-001`) and fixed at the handler layer: `RegisterForAccount`/`RevokeForAccount` (`api/internal/handlers/ssh_keys.go`) now compare the path UUID against the caller's own account UUID and apply the identical step-up gate the self routes use whenever they match. A genuine operator-on-behalf-of-a-different-account call is unaffected — no step-up gate, matching every other `/v1/user-accounts/*` admin route.

## D11: resolver consumer contract

`mod-repos`' SSH transport consumes key resolution through one interface, `transport.KeyResolver` (`mod-repos/api/transport/key_resolver.go`):

```go
ResolveActor(ctx context.Context, key ssh.PublicKey) (actorEntityID int64, err error)
```

`mod-users` must not import `mod-repos`, so it cannot implement that interface directly. Instead it exposes, through the public facade `api/usersservice`, a Go shape whose method signature matches exactly:

- `type SSHKeyResolver` with `ResolveActor(ctx context.Context, key gossh.PublicKey) (int64, error)` (`gossh` is `golang.org/x/crypto/ssh`, which `mod-repos`' `charm.land/ssh.PublicKey` embeds), constructed by `NewSSHKeyResolver(...)` and registered as the manifest service `sshKeyResolver`.
- `var ErrUnknownSSHKey`, returned identically for an unregistered key, a revoked key, and a key whose account holder is archived — the resolver never distinguishes these three cases to its caller. Any other error is wrapped and does **not** satisfy `errors.Is(err, ErrUnknownSSHKey)`.
- `ResolveActor` is pre-authentication, side-effect-free, and uncached: no actor is required on `ctx` and it makes no `Authorize` call (it is a credential check, in the same category as the `POST /v1/auth/login` password check — there is no authenticated caller yet to authorize); it performs no writes of any kind, including no "last used" bookkeeping (see the consumer precondition below); and it never logs key material.
- A compile-time assertion (`api/usersservice/service.go`) checks `*SSHKeyResolver` against a local one-method interface shaped like `transport.KeyResolver`, so a drift in either signature fails to build rather than failing at `mfgen generate` time in a consuming application.

`sshKeyResolver` is `mod-users`' **second exported cross-module integration point**, alongside `resolveActorOrAnonymous` (see [Authentication flow](../architecture.md#authentication-flow)). Unlike `resolveActorOrAnonymous`, which a consuming module wires as HTTP middleware, `sshKeyResolver` is consumed directly as a Go value by a composing application's own transport code — no `mod-users` route calls it, and `mfgen`'s manifest resolver prunes the `provides.services` node as an unreachable root from any `mod-users` route, which is expected: the node exists to be constructed and handed to the composing application, not to be reached from this module's own HTTP surface.

**Consumer preconditions (finding arch-005/security-003, deferred from the phase-01 boundary review).** `ResolveActor` runs once per *candidate* key a client offers during an SSH handshake, including unsigned RFC 4252 "query" probes that never complete authentication (`mod-repos/api/transport/publickeyauth.go`) — a genuinely unauthenticated peer can therefore trigger arbitrarily many `ResolveActor` calls simply by opening SSH connections and offering keys. `mod-users` deliberately keeps `ResolveActor` cheap (one or two indexed lookups, no cache to warm, no write) so that this is inexpensive per call, but it does not itself rate-limit — that would require state and defeats the "no cache" guarantee ([D4](#d4-no-cache-immediate-revocation)) that makes revocation immediate. The composing transport (`mod-repos`, composed by `app-mfgit`) is therefore responsible for **rate-limiting SSH handshakes and capping the number of candidate keys accepted per connection** at the transport layer, before or alongside calling `ResolveActor`. This is a precondition on `sshKeyResolver`'s consumers, not something `mod-users` enforces.

## Related documents

- [docs/architecture.md](../architecture.md) — system overview; [Data model](../architecture.md#data-model), [API layer](../architecture.md#api-layer), and [Authentication flow](../architecture.md#authentication-flow) sections this document supports.
- [docs/mod-users-spec.md](../mod-users-spec.md) — the functional specification, including the SSH key algorithm-acceptance policy, request/response shapes, and step-up/masking requirements not covered here.
- [`api/openapi.yaml`](../../api/openapi.yaml) — the authoritative endpoint reference for the six SSH-key routes.
- `docs-mf-standards/architecture/schema-ownership-design.md`, the canonical, sibling-repo standard for the schema-ownership and archive-on-revoke rules D1 and D3 apply — not linked here because this repo's vendored `docs/mf-standards` submodule copy is pinned before that standard existed and does not carry the file.
