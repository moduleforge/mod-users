# SSH Key Design

## Purpose and scope

This note is the fixed design contract for the `ssh-public-keys` plan. Every task document in `plan/phase-01-ssh-public-keys/` implements a slice of it. It resolves each open question in the [handoff document](/Users/zane/playground/moduleforge/app-mfgit/docs/handoffs/mod-users-ssh-extension.md) ("Capability 1 — SSH public-key ownership", "Assumptions requiring confirmation", "Open questions for the mod-users round") that applies to Capability 1. For each one it records the decision, the convention or requirement it rests on, and the evidence behind it.

Capability 2 of the handoff (the anonymous read identity) already shipped: `anonymousActor`, `resolveActorOrAnonymous`, and `model/migrations/sql/0101_system_actors.sql`. It is out of scope here. Handoff open questions 1 and 7 belong to Capability 2 and are not addressed.

## Decisions at a glance

| # | Question | Decision | Settled by |
|---|---|---|---|
| D1 | Where does the table live? | `mod_users.ssh_public_keys`, a new table in a new `mod_users` schema, with every reference schema-qualified | Ecosystem standard ([schema ownership](#d1-schema-placement)) |
| D2 | Global or per-user uniqueness? | Globally unique among active keys, enforced by a partial unique index | The handoff's "must not silently authenticate as two users" rule plus the "enforce invariants in the schema" standard |
| D3 | Archive or hard-delete on revoke? | Archive (`archived_at`). All reads and the resolver filter `archived_at IS NULL`. | Ecosystem standard for module-owned domain rows |
| D4 | How is revocation immediate? | No cache. Every resolution is a fresh indexed query, filtered on archival of both the key and the account holder. | Handoff requirement 6 plus the "not the sole writer" standard |
| D5 | Resolution target | `user_accounts.account_holder`, the legal-entity id | Handoff requirement 3 / assumption 3; matches bearer-token resolution |
| D6 | Accepted algorithms | Ed25519, ECDSA P-256/384/521, the FIDO `sk-` variants, and RSA of at least 2048 bits. DSA, short RSA, certificates, and unknown types are refused at registration. | mod-users decision (handoff requirement 7). A user may revisit it. |
| D7 | Key metadata | Optional `label` and `created_at` only. No `last_used_at`, no expiry, in v1. | Consumer call-site evidence ([D7](#d7-key-metadata)) |
| D8 | Step-up on registration | Yes. Self-service register and revoke are step-up-gated when `AUTH_REQUIRE_STEP_UP` is on. | Existing convention: step-up gates every credential-mutating self-service endpoint |
| D9 | Operator surface and its authorization | `/v1/user-accounts/{uuid}/ssh-keys`, authorized by the existing `Authorizer` against the account holder entity | Existing admin user-management convention |
| D10 | Error and masking posture | Masked `403` for every not-found-or-not-permitted outcome. `409` for a duplicate key. `400` for an invalid or refused key. | `api-response-design.md` masking default plus its create-time-uniqueness exception |
| D11 | Consumer-facing Go shape | `usersservice.SSHKeyResolver.ResolveActor(ctx, gossh.PublicKey) (int64, error)` returning `usersservice.ErrUnknownSSHKey`. It is side-effect-free and needs no actor. | `mod-repos`' `transport.KeyResolver` contract |

## D1: schema placement

`docs-mf-standards/architecture/schema-ownership-design.md` and `docs-mf-standards/building-modules.md#data-ownership-and-schema-conventions` set the rule. A module's tables live in `mod_<module>`, here `mod_users` from `module: users`. Only `entities`, `types`, and `user_accounts` stay in `public`. Canonical source: `/Users/zane/playground/moduleforge/docs-mf-standards/`. Note that mod-users' vendored `docs/mf-standards` submodule is pinned to 2026-07-31, which predates the 2026-08-18 standard, so the file is absent there.

The standard is in force for new tables:

- `mod-tokens` (2026-09-25) adopted it with `CREATE SCHEMA IF NOT EXISTS mod_tokens`.
- `mod-repos` predates it (its `public` tables date from 2026-08-02).
- mod-users' existing ten tables also predate it and stay where they are. Moving them is out of scope.

This plan therefore creates the first real table under a `mod_*` schema in the ecosystem. The migration:

- creates the schema with `CREATE SCHEMA IF NOT EXISTS mod_users`
- qualifies every object (`mod_users.ssh_public_keys`, `public.user_accounts`)
- uses `IF NOT EXISTS` DDL so re-application is a no-op
- contains no `GRANT`

sqlc prefixes non-`public` table struct names with the schema, which yields `ModUsersSshPublicKey`. Task 001 may add a sqlc `rename` override if the resulting name is awkward. Either outcome is acceptable, but the task must record which it chose.

## D2: global uniqueness among active keys

Only one outcome satisfies the requirement that a key must not silently authenticate as two users: a fingerprint resolves to at most one active row. Per-user uniqueness would let two accounts hold the same key and make resolution ambiguous. The invariant is enforced in the schema, not in Go, because `building-modules.md#grants` says a module is not the sole writer of its tables. The mechanism:

```sql
CREATE UNIQUE INDEX IF NOT EXISTS ssh_public_keys_active_fingerprint_uq
  ON mod_users.ssh_public_keys (fingerprint_sha256)
  WHERE archived_at IS NULL;
```

The same index serves the resolver's constant-time lookup (handoff requirement 2). Because the index is partial, a revoked key may be registered again later as a new row with a new UUID, by the same user or a different one.

A duplicate registration returns `409 conflict` with detail `users.ssh_key_in_use` on field `public_key`. The response is identical whether the existing holder is the caller or another account, so it never reveals *which* account holds the key. The service must map a Postgres unique violation (`23505`) on this index to the conflict. A Go pre-check alone would lose a concurrent-registration race. This follows the `api-response-design.md` create-time-uniqueness exception (the `users.email_taken` precedent): masking governs UUID resolution, not create-time key conflicts.

## D3: archive on revoke

A key row is a module-owned domain row, not internal bookkeeping. Consuming apps may reasonably reference a key by UUID, for example in push attribution or a "keys" audit view. `schema-ownership-design.md#deletion-module-owned-rows-are-archived-never-dropped` therefore applies, so revoke sets `archived_at = now()`.

The `mod-authz` grant hard-delete exception does not fit here. That exception covers rows no other app hangs data off, and it exists because a lingering revoked grant is permission-shaped. A lingering archived key is not permission-shaped, because every read and the resolver exclude it (D4).

mod-users' own older credential tables (`auth_oidc_identities`, `anon_tokens`) hard-delete. They predate the standard and are not precedent for new tables.

## D4: immediate revocation

- The resolver issues a fresh query on every call. No process-level cache exists, and none may be added: an in-process cache would violate the "not the sole writer" standard and requirement 6.
- The lookup requires `k.archived_at IS NULL`, so a revoked key stops resolving on the next connection.
- The lookup also rejects keys whose account holder entity is archived (`public.entities.archived_at IS NOT NULL`, which is how `UserAccountService.Delete` archives a user). Archiving a user therefore cuts off git access immediately. sqlc cannot see mod-core's `entities` table (mod-users' sqlc schema is only `model/migrations/sql`, and no existing mod-users query references a mod-core table), so this check is a second lookup via mod-core's `coredb.GetEntityByID`, which returns `archived_at`. Both lookups are indexed primary-key or unique lookups.
- Revoking a key while that key's session is already open does not terminate the open session. That is transport behavior and belongs to mod-repos. "Next connection" is the contract.

## D5: resolution target

The resolver returns `user_accounts.account_holder`, the `natural_person`/`legal_entity` entity id that bearer-token resolution also places on `opctx`. It never returns the user-account id or UUID.

## D6: algorithm acceptance policy

Input is one `authorized_keys`-format line, parsed with `golang.org/x/crypto/ssh.ParseAuthorizedKey`, which is already a dependency: `golang.org/x/crypto v0.52.0` in `api/go.mod`.

| Key type (`PublicKey.Type()`) | Accepted? |
|---|---|
| `ssh-ed25519` | Yes |
| `sk-ssh-ed25519@openssh.com` | Yes |
| `ecdsa-sha2-nistp256`, `ecdsa-sha2-nistp384`, `ecdsa-sha2-nistp521` | Yes |
| `sk-ecdsa-sha2-nistp256@openssh.com` | Yes |
| `ssh-rsa` with modulus of at least 2048 bits | Yes |
| `ssh-rsa` with modulus under 2048 bits | No: `users.ssh_key_too_weak` |
| `ssh-dss` (DSA) | No: `users.ssh_key_type_unsupported` |
| Any `*-cert-v01@openssh.com` certificate type | No: `users.ssh_key_type_unsupported` |
| Anything else | No: `users.ssh_key_type_unsupported` |

Further input rules:

- Refuse any line carrying `authorized_keys` options (for example `command="..."`, `no-pty`). They have no meaning here and could mislead a reader of the stored key. Error: `users.ssh_key_invalid`.
- Refuse input with more than one key, meaning a non-empty remainder after the first parsed key once whitespace is trimmed. Error: `users.ssh_key_invalid`.
- Unparseable input returns `users.ssh_key_invalid`.
- Cap the raw input at 16 KiB.
- The stored `public_key` is the canonical form: `strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))`, meaning type plus base64 with no comment and no options.
- The stored `fingerprint_sha256` is `ssh.FingerprintSHA256(key)`, in the form `SHA256:<unpadded base64>`.
- All refusals are `400 invalid_input`, with the detail on field `public_key`.

The `ssh-rsa` *key type* stays acceptable. The deprecated SHA-1 `ssh-rsa` *signature algorithm* is negotiated by the SSH server (mod-repos' transport), not at registration, and is not this module's concern.

The schema backs the policy with a `CHECK (key_type IN (...))` constraint over the accepted list, as defense in depth for non-Go writers. Minimum RSA size cannot be expressed in SQL and is enforced in Go only.

The policy lives in exactly one Go location (task 002's package). It is a fixed policy, not configuration.

## D7: key metadata

- **`label`**: optional, user-supplied, trimmed, at most 100 characters, stored `NOT NULL DEFAULT ''`. When the request omits it, it defaults to the key's `authorized_keys` comment, trimmed and truncated to 100 characters. A label over 100 characters in the request returns `400 invalid_input` with detail `users.ssh_key_label_too_long` on field `label`.
- **`created_at`**: always present.
- **No `last_used_at` in v1.** mod-repos' `transport.PublicKeyAuthHandler` (`mod-repos/api/transport/publickeyauth.go`) calls `ResolveActor` for every *candidate* key during the handshake, including unsigned RFC 4252 query probes that never authenticate. A `last_used_at` write inside `ResolveActor` would let an unauthenticated party bump any registered key's timestamp. Recording real use needs a separate post-handshake hook on the consumer side, which is a follow-up and not part of this plan. `ResolveActor` must be read-only and side-effect-free.
- **No expiry in v1.** Nothing in the handoff requires it. A nullable `expires_at` plus one extra predicate in the resolver can be added later without reshaping anything.

## D8: step-up

`AUTH_REQUIRE_STEP_UP` (`cfg.Auth.RequireStepUpForCredentialChange`) already gates every credential-mutating self-service endpoint: OIDC link and unlink, and password set and remove. A registered SSH key is a standing credential, so **self-service register and revoke are step-up-gated** exactly as those are:

- the `X-Step-Up-Token` header
- `localauth.VerifyStepUpToken` bound to the caller's `UserAccountID`
- the `409 users.step_up_required` action-required response when the token is missing or invalid

Listing is not gated. Operator routes are not step-up-gated, matching every existing `/v1/user-accounts/*` admin route. Each audit record carries `step_up: <bool>`, as the identities handler's records do.

## D9: routes and authorization

| Method and path | Purpose | Middleware (manifest entry) | Service op |
|---|---|---|---|
| `GET /v1/self/ssh-keys` | List own active keys | `requireOIDCConfirmed`, `requireAuth` (reachable with an unverified email, matching `GET /v1/self/identities`) | `read` |
| `POST /v1/self/ssh-keys` | Register own key | `requireOIDCConfirmed`, `requireAuth`, `requireVerifiedEmail`, plus the step-up check in the handler | `update` |
| `DELETE /v1/self/ssh-keys/{key_uuid}` | Revoke own key | as above | `update` |
| `GET /v1/user-accounts/{uuid}/ssh-keys` | Operator: list a user's keys | `requireOIDCConfirmed`, `requireAuth`, `requireVerifiedEmail` (same as the account-routes entry) | `read` |
| `POST /v1/user-accounts/{uuid}/ssh-keys` | Operator: register a key for a user | as above | `update` |
| `DELETE /v1/user-accounts/{uuid}/ssh-keys/{key_uuid}` | Operator: revoke a user's key | as above | `update` |

The operator prefix is `/v1/user-accounts/`, the path this module actually serves (`api/internal/handlers/account_routes.go`). `api/openapi.yaml` and `docs/mod-users-spec.md` still say `/v1/users/{uuid}`. That drift predates this plan and is not fixed here.

Both families call one service. Every service method:

1. loads the target `user_accounts` row by UUID
2. calls `Authorize(ctx, op, &account_holder)` before touching key data, per the AGENTS.md "Authorization is checked first" convention
3. performs the work

The existing `Authorizer`'s own-arm (a natural person self-owns) authorizes the self-service family. A wildcard or explicit grant on the user entity authorizes the operator family. This is the same policy that already governs `GET`/`PUT`/`DELETE /v1/user-accounts/{uuid}`, so no new operation slug or grant shape is introduced. The self family resolves the account UUID from `UserContext`; the operator family takes it from the path.

An operator acting on another user's keys is audited with the operator as actor. Assume/sudo behaves as it does everywhere else.

## D10: responses

- Key object (JSON): `uuid`, `key_type`, `fingerprint` (the `SHA256:` string), `public_key` (canonical form), `label`, `created_at`. Internal ids are never exposed (AGENTS.md convention).
- `GET` list: this module's standard list envelope with `limit`/`offset` pagination (default 20, max 200) and `total`, per the spec's "All list endpoints are paginated". Task 004 matches whatever envelope the existing list handlers emit. Order is `created_at ASC`, then `id ASC`. Only active keys are listed.
- `POST`: `201` with the key object. `DELETE`: `204`.
- Masked `403 forbidden` for all of these, indistinguishable from one another, per `api-response-design.md#existence-masking-not_found-vs-forbidden` (masking is the default):
  - unknown account UUID
  - account the caller may not act on
  - unknown key UUID
  - a key UUID belonging to a different account
  - an already-revoked key
- `409 conflict` + `users.ssh_key_in_use` (D2). `400 invalid_input` + D6/D7 detail codes. `409 users.step_up_required` action-required (D8).
- Every mutation is audited through the existing `observer.ObserverGroup`: `Observe` inside the transaction and `ObserveAfterCommit` after it, as in `api/internal/handlers/identities.go`. The resource type is `ssh_public_key`, the entity id is the account holder, and the snapshot is `{uuid, fingerprint, key_type, label}`. The operation is `create` on register and `delete` on revoke.

## D11: consumer-facing Go shape

`mod-repos/api/transport/key_resolver.go` defines:

```go
ResolveActor(ctx context.Context, key ssh.PublicKey) (actorEntityID int64, err error)
```

Here `ssh` is `charm.land/ssh`, whose `PublicKey` interface embeds `golang.org/x/crypto/ssh.PublicKey` (`charm.land/ssh@v0.4.2/wrap.go`). mod-users must not import mod-repos. It exposes, through the existing public facade package `api/usersservice`:

- `type SSHKeyResolver` with `ResolveActor(ctx context.Context, key gossh.PublicKey) (int64, error)`, where `gossh` is `golang.org/x/crypto/ssh`. It is constructed by `NewSSHKeyResolver(...)` and registered as the manifest service `sshKeyResolver`.
- `var ErrUnknownSSHKey`, returned for unknown keys, revoked keys, and keys whose account holder is archived, identically in all three cases. Any other error is a transient or internal failure, wrapped. The consumer's adapter maps `ErrUnknownSSHKey` to `transport.ErrUnknownKey`, which makes the adapter one function body in app-mfgit.
- `ResolveActor` requires no actor on `ctx` and calls no `Authorize`. It is a pre-authentication credential check, in the same category as the `POST /v1/auth/login` credential check. It never logs key material and never writes.
- A `nil` key returns `ErrUnknownSSHKey`.
- The resolver compares both `fingerprint_sha256` and the canonical `public_key` text, so a stored-row mismatch can never resolve.

`api/usersservice` is chosen over a new public package because mfgen resolves manifest package selectors via goimports at codegen time. Reusing a package the manifest already references (`usersservice.NewUserAccountService`) avoids new selector-resolution risk.

## Table shape

```sql
CREATE SCHEMA IF NOT EXISTS mod_users;

CREATE TABLE IF NOT EXISTS mod_users.ssh_public_keys (
  id                 BIGSERIAL PRIMARY KEY,
  uuid               UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  user_account_id    BIGINT NOT NULL REFERENCES public.user_accounts(id) ON DELETE CASCADE,
  key_type           TEXT NOT NULL CHECK (key_type IN (
                       'ssh-ed25519', 'sk-ssh-ed25519@openssh.com',
                       'ecdsa-sha2-nistp256', 'ecdsa-sha2-nistp384', 'ecdsa-sha2-nistp521',
                       'sk-ecdsa-sha2-nistp256@openssh.com', 'ssh-rsa')),
  public_key         TEXT NOT NULL,
  fingerprint_sha256 TEXT NOT NULL CHECK (fingerprint_sha256 LIKE 'SHA256:%'),
  label              TEXT NOT NULL DEFAULT '' CHECK (char_length(label) <= 100),
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at        TIMESTAMPTZ
);
-- plus ssh_public_keys_active_fingerprint_uq (D2) and
-- ssh_public_keys_user_account_active_idx ON (user_account_id) WHERE archived_at IS NULL
```

`ON DELETE CASCADE` matches the sibling credential tables (`auth_oidc_identities`). User accounts are archived via their entity, never hard-deleted through the API, so the cascade fires only on out-of-band hard deletion, where there is no one left to archive for.

## Known limitations, deliberately not addressed

- **Key squatting.** A user can register someone else's *public* key (public keys are published, for example at `https://github.com/<user>.keys`) before its owner does. The squatter cannot authenticate with it, having no private key, but the owner is then refused with `409`. If the owner connects, the handshake succeeds and resolves to the squatter's identity, which the owner would notice immediately. Proof-of-possession at registration (signing a server challenge) would close this and is a candidate follow-up. GitHub and GitLab accept the same trade.
- **No per-account key cap.** Growth is bounded by authenticated, verified-email, and optionally step-up-gated users. A cap is a candidate follow-up.
- **GUI.** Handoff assumption 4 (key management UI in `@moduleforge/users-gui`) is not built here. It is a candidate follow-up.
