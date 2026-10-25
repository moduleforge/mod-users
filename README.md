# @moduleforge/mod-users

Provides complete model, API, and UI components for user identity, account management, and authentication within the [ModuleForge](https://github.com/moduleforge) ecosystem. It supports creating and merging accounts from multiple channels (email, OAuth/OIDC providers), authenticating those accounts, and managing the full user identity and profile lifecycle.

## Installation

The module ships three independently consumable sub-packages that an application composes as needed.

**Go model** (Postgres schema, migrations, sqlc-generated query code):

```sh
go get github.com/moduleforge/mod-users/model
```

**Go API** (HTTP handlers and business-logic services):

```sh
go get github.com/moduleforge/mod-users/api
```

**React component library** (TypeScript/React):

```sh
npm install @moduleforge/users-gui
```

> The GUI package depends on `@moduleforge/core-gui` (a peer dependency), and is not published to
> any registry today. An app that composes this module (e.g. `app-mftodo`) wires
> `@moduleforge/users-gui` and its `core-gui` peer in together through a **bun workspace** it
> owns — a root `package.json` listing this repo's `gui/` among its `workspaces`, with the app's
> own `gui/package.json` referring to it as `workspace:*`. See
> [`docs/mf-standards/building-applications.md`'s First-time setup
> section](./docs/mf-standards/building-applications.md#first-time-setup) for the mechanism.

For embedding the components in an app (configuration, routes, Next.js and React Router examples), see the [users-gui consumer integration guide](./gui/README.md).

## Configuration

Access tokens are EdDSA (Ed25519) JWTs that carry a server-side session or non-interactive credential; the session or credential and the acting profile are re-checked on every request, so a revocation takes effect on the next request. Bearer tokens only: no route reads an authentication cookie. Settings (see [docs/jwt-eddsa.md](./docs/jwt-eddsa.md); full list in `.env.example`):

| Variable | Default | Purpose |
|---|---|---|
| `JWT_SIGNING_KEY` | unset: a key is generated on first boot and persisted in `mod_users.auth_signing_keys` | Ed25519 private key (PKCS#8 PEM; literal `\n` accepted) that signs access tokens |
| `JWT_SIGNING_KID` | unset | `kid` header for issued tokens |
| `JWT_VERIFY_PUBLIC_KEYS` | unset | Extra accepted public keys: one PKIX PEM, or JSON `kid` to PEM |
| `JWT_SECRET` | unset: derived from the signing key | Optional explicit HMAC key for the OAuth state parameter only |
| `AUTH_ACCESS_TOKEN_TTL` | `1h` (maximum `1h`) | Access-token lifetime; `exp` never outlives the session |
| `AUTH_SESSION_MAX_AGE` | `720h` | Absolute session lifetime |
| `AUTH_SESSION_IDLE_TIMEOUT` | `168h` | Refresh-lineage expiry |
| `AUTH_CLIENT_CREDENTIALS_TTL` | `15m` | Client-credentials access-token lifetime |
| `AUTH_IMPERSONATION_TTL` | `30m` | Lifetime of one impersonation token or renewal |
| `AUTH_IMPERSONATION_MAX` | `60m` | Total impersonation time from its start |
| `AUTH_SELF_SERVICE_MAX_AUTH_AGE` | `10m` | How recent `auth_time` must be for self-service credential and identifier changes |
| `AUTH_DEFAULT_AUDIENCE` | `mod-users` | `aud` of a token whose session has no app |
| `AUTH_ACCEPTED_AUDIENCES` | unset: the default audience plus any registered app slug | Comma-separated `aud` values bearer tokens may carry |

CORS is default-deny: set `CORS_ORIGINS` to a comma-separated list of exact front-end origins. An unset value emits no CORS headers outside `DEPLOY_MODE=local`, and any entry containing `*` is rejected at startup outside local mode. Entries are bare `scheme://host[:port]` origins (no trailing slash or path), matched case-insensitively. Upgrading deployments that relied on the old implicit wildcard must read [docs/cors.md](./docs/cors.md).

## Party profiles and managed users

A party (an organization or a natural person) admits users through profiles. All routes are bearer-authenticated; a party or profile the caller may not read answers `404`, the same as an unknown one, before any `403`.

| Route | Purpose |
|---|---|
| `GET /v1/parties/{party_uuid}/profiles` | List a party's profiles (`status`, `limit`, `offset`); needs `read` on profiles in the party |
| `POST /v1/parties/{party_uuid}/profiles` | Invite a user by `holder_email` or `holder_user_uuid` (status `invited`; no account is created, no email is sent); needs `create` on profiles in the party |
| `POST /v1/parties/{party_uuid}/managed-users` | Create a user owned and managed by the party, with an active party profile and an initial password |
| `GET`, `PUT /v1/parties/{party_uuid}/authn-policy` | Read (needs `read` on profiles in the party) or set (needs `update` on the party and a fresh authentication) the party's credential rule (`any`, `authorized`, `provider`) and `min_acr`; a party with no policy is `any`, `min_acr` 1 |
| `GET /v1/parties/{party_uuid}/credential-authorizations` | List the credentials authorized for the party; needs `update` on the party |
| `PUT`, `DELETE /v1/parties/{party_uuid}/credential-authorizations/{credential_uuid}` | Authorize or deauthorize a credential for the party's `authorized` rule |
| `PUT`, `DELETE /v1/parties/{party_uuid}/confinements/{credential_uuid}` | Confine a credential to the party, or clear that (only when it names this party) |
| `PATCH /v1/profiles/{uuid}` | Suspend, resume or end a party profile (`status`, `end_reason`); needs `update` and a fresh authentication |
| `POST /v1/me/profiles/{uuid}/accept`, `/decline`, `/leave` | The holder's own actions; an invitation appears in `GET /v1/me/profiles` |

Inviting a user whose current credentials cannot meet the party's authentication policy is refused with `409` `users.policy_mismatch_confirmation_required` (its details name the failing rule, what the party requires and what the holder has) unless the request sets `confirm_policy_mismatch`; a confirmed invitation records a `profile_policy_confirmations` row for the new profile in the same transaction. The same rule guards outside collaborators in mod-authz: `usersservice.CollaboratorGate` (`CheckOutsideCollaborator`) is the hook mod-authz consults, inside its own write transaction, when a profile outside a party is added to the party's actor group or named as an actor of a grant over its content (a personal profile counts as outside; a profile of the party never reaches the gate). The dev server passes it as the trailing argument of `authzservice.NewFromArgs`, and the module manifest provides it as `collaboratorGate` for mod-authz's `optionalService:collaboratorGate`. A confirmation exempts the profile from the party's credential rule only, never from `min_acr`, and compliance stays continuous: revoking a credential revokes every session holding it as a factor, and the party's `min_acr` and credential rule are re-checked on every request and at each login, so a raised `min_acr` or a downgraded collaborator is locked out of the party's profile (`acr_too_low`, `credential_not_authorized`) on the next request even with a confirmation.

Authorizations and confinements are written by a party administrator (`update` on the party) or by the credential's own holder acting through a session that already satisfies the party's gate through another factor; the evaluation excluding the credential is `mod_users.profile_availability_excluding`, so a credential never authorizes itself. A caller that is neither gets `403`, or the masked `404` when it cannot read the party; every write needs a fresh authentication. Availability changes take effect on the holders' next request.

Ending a profile (an administrator ending it, or the holder declining or leaving) deletes the holder's credential authorizations for the party, revokes the holder's credentials confined to it, and disables a managed user left with no open profile. `ProfileService.AdmitInTx` is the trusted, in-transaction admission another module (mod-billing seats) calls once the holder and party have consented: it returns the holder's active profile in the party, activating an invited one or creating an active one, and refuses a suspended one with `users.profile_suspended`; it has no HTTP route. Other modules react to creations and status changes with `ProfileService.AddLifecycleObserver` (in-transaction). See `api/openapi.yaml` for the request and response shapes.

## Additional documentation

- [AGENTS.md](./AGENTS.md) — build, test, and development commands for contributors and AI agents
- [docs/mod-users-spec.md](./docs/mod-users-spec.md) — feature specification and behavioral contracts
- [docs/architecture.md](./docs/architecture.md) — system design, sub-project relationships, and key design decisions
- [docs/jwt-eddsa.md](./docs/jwt-eddsa.md) — EdDSA JWT signing, verify-only deployment, key rotation, and the `JWT_*` configuration
- [docs/cors.md](./docs/cors.md) — CORS default-deny policy, `CORS_ORIGINS` / `DEPLOY_MODE` behavior, and the upgrade note for deployments that relied on the implicit wildcard
- [docs/cookie-path.md](./docs/cookie-path.md) — `USERS_COOKIE_PATH`, the `oidc_state` cookie Path, and the strip-prefix proxy example
- [docs/consumer-upgrade-notes.md](./docs/consumer-upgrade-notes.md) — API, configuration and behaviour changes for consumers that compose, embed or verify tokens from this module, plus known limits
- [docs/project-structure.md](./docs/project-structure.md) — directory layout and sub-project conventions
- [model/README.md](./model/README.md) — Postgres schema, goose migrations, and sqlc-generated queries
- [deploy/local/README.md](./deploy/local/README.md) — local development stack (Postgres, Authelia OIDC, Mailpit)
- [deploy/k8s/README.md](./deploy/k8s/README.md) — Kubernetes deployment manifests (Kustomize and CloudNativePG)
- [deploy/serverless/README.md](./deploy/serverless/README.md) — serverless deployment drafts (Cloud Run, App Runner, Fly)
- [next-steps.md](./next-steps.md) — forward-looking residue: pending manual verification and deferred work
- [.claude/CLAUDE.md](./.claude/CLAUDE.md) — Claude Code configuration and project-specific AI agent guidance

## License

[Apache 2.0](./LICENSE)
