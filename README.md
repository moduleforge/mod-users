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
| `AUTH_IMPERSONATION_TTL` | `30m` | Lifetime of one impersonation session at its start and of each renewal (`POST /v1/impersonations/current/renew`) |
| `AUTH_IMPERSONATION_MAX` | `60m` | Longest an impersonation session lives from its start, renewals included (not shorter than the TTL) |
| `AUTH_SELF_SERVICE_MAX_AUTH_AGE` | `10m` | How recent `auth_time` must be for self-service credential and identifier changes |
| `AUTH_DEFAULT_AUDIENCE` | `mod-users` | `aud` of a token whose session has no app |
| `AUTH_ACCEPTED_AUDIENCES` | unset: the default audience plus any registered app slug | Comma-separated `aud` values bearer tokens may carry |

Impersonation is a separate child session, not a switch of the administrator's own: `POST /v1/impersonations` (`profile_uuid`, `reason`) needs the `impersonate` operation on the target profile (a party administrator's owned-scope `manage` grant reaches the party's profiles; a personal profile needs an operator-party profile with a wildcard `impersonate` or `manage` grant) and an authentication no older than ten minutes (`409 users.step_up_required` otherwise). While impersonating, the impersonated profile's authorizations decide what the session may do, and every party's authentication policy (the profile's own party's, and that of each party whose content it reaches) applies as for that profile, evaluated on the administrator's own session: its factors for the `credential_rule` and its acr for `min_acr`. The impersonated holder's own credentials never count. A platform operator's impersonation is exempt from a customer party's `credential_rule` (an operator's credentials cannot be authorized for a customer party), never from `min_acr`; any other administrator's credentials must meet every such rule. The answer is a token response without a refresh token whose `act.sub` is the administrator's principal uuid; it expires within `AUTH_IMPERSONATION_TTL`. An impersonation never nests and cannot switch or fork (`403 users.impersonation_not_allowed`); `POST /v1/impersonations/current/renew` extends it up to `AUTH_IMPERSONATION_MAX` from its start and `DELETE /v1/impersonations/current` ends it. Each renewal re-checks that the administrator may still impersonate the profile, from the administrator's own session and as the profile the administrator acted as when starting it (recorded on the impersonation session): a revoked grant, an acting profile that is no longer available (for example suspended) or a party policy the administrator's session no longer meets answers `403` and ends the impersonation. The renewal does not re-apply the ten-minute freshness requirement. Revoking or expiring the administrator's session ends the impersonation on its next request. The audit log records `impersonate`, `update` and `revoke` on resource `impersonation`, with the administrator as the impersonator on every request made inside the session.

CORS is default-deny: set `CORS_ORIGINS` to a comma-separated list of exact front-end origins. An unset value emits no CORS headers outside `DEPLOY_MODE=local`, and any entry containing `*` is rejected at startup outside local mode. Entries are bare `scheme://host[:port]` origins (no trailing slash or path), matched case-insensitively. Upgrading deployments that relied on the old implicit wildcard must read [docs/cors.md](./docs/cors.md).

## First run: setup token, bootstrap, OIDC configuration

A fresh deployment has no operator party and an unconfirmed OIDC configuration. At boot the server mints one setup token and prints it (stderr banner for `TOKEN_DISPLAY=stderr` or `both`; for `localhost`, read it from `GET /v1/oidc-config/setup-token` over loopback). The same token serves both first-run steps and stays valid until both are done:

1. **Bootstrap** the operator account: `POST /v1/bootstrap` with the token in the `X-Setup-Token` header and a body `{"operator": {"kind": "organization", "legal_name": ..., "legal_form": ..., "jurisdiction": ...}, "admin": {"email": ..., "password": ..., "given_name": ..., "family_name": ...}}` (or `"operator": {"kind": "natural_person"}` to make the first user's own person record the operator party). One transaction creates the first user (verified email, password, email-code credential, own person record, personal profile), the operator party (claimed with mod-core's `core_claim_operator_party`, the single winner of concurrent attempts), the administrator's party profile with a wildcard `manage` grant, and the operator party's authentication policy (`credential_rule: authorized`, the bootstrap password authorized and set to land on the administrator profile). It answers `201` with a session on the administrator profile; a second bootstrap gets `409 users.already_bootstrapped`, a missing or wrong token `401 users.setup_token_invalid`. `GET /v1/bootstrap/status` reports `bootstrapped` and `oidc_confirmed`. Both routes are public and are not behind the OIDC-confirmed gate.
2. **Configure OIDC** at `/oidc-config` (`POST /v1/oidc-config/confirm`, possibly as an opt-out). Confirming OIDC while the bootstrap is pending keeps the token for the bootstrap.

Whichever step completes last clears the token. From then on, OIDC configuration requires an operator administrator session: a session whose acting profile is an operator profile holding wildcard `manage` (the administrator's personal-profile session is refused). Signing in with the bootstrap password lands on the administrator profile; signing in with an email code lands on the personal profile, with the administrator profile locked by the operator party's policy. With `TOKEN_DISPLAY=none` no token is shown, so the bootstrap cannot run until a display mode is configured.

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
| `PUT`, `DELETE /v1/parties/{party_uuid}/confinements/{credential_uuid}` | Confine a credential to the party, or clear that (only when it names this party); the administrator door confines only party-controlled credentials, and the holder door cannot clear those |
| `PATCH /v1/profiles/{uuid}` | Suspend, resume or end a party profile (`status`, `end_reason`); needs `update` and a fresh authentication |
| `POST /v1/me/profiles/{uuid}/accept`, `/decline`, `/leave` | The holder's own actions; an invitation appears in `GET /v1/me/profiles` |

Inviting a user whose current credentials cannot meet the party's authentication policy is refused with `409` `users.policy_mismatch_confirmation_required` (its details name the failing rule, what the party requires and what the holder has) unless the request sets `confirm_policy_mismatch`; a confirmed invitation records a `profile_policy_confirmations` row for the new profile in the same transaction. Honoring `confirm_policy_mismatch` needs `update` on the party (403 otherwise), and changing the party's `credential_rule` deletes the party's confirmations. At invite time the `authorized` rule is treated as met when the holder has any usable interactive credential, because no credential can be authorized for a party before a profile exists; no confirmation is written for that case, so only a holder with no usable interactive credential is refused. The member then stays locked (`credential_not_authorized`) after acceptance until an administrator or the holder authorizes a credential. The same gate guards outside collaborators in mod-authz, but strictly (platform operator profiles are exempt from the owning-party authentication gate, so a customer party's policy never binds the operator): the invite-time relaxation does not reach it, because a collaborator holds no profile in the owning party and so can never gain a `credential_party_authorizations` row implicitly. Under an `authorized` party an outside collaborator needs a credential authorized for or confined to the party, or an explicit confirmation (so a collaborator with only a personal password is refused with `409` unless confirmed). The hook is `usersservice.CollaboratorGate` (`CheckOutsideCollaborator`), which mod-authz consults, inside its own write transaction, when a profile outside a party is added to the party's actor group or named as an actor of a grant over its content (a personal profile counts as outside; a profile of the party never reaches the gate). The dev server passes it as the trailing argument of `authzservice.NewFromArgs`, and the module manifest provides it as `collaboratorGate` for mod-authz's `optionalService:collaboratorGate`. A confirmation exempts the profile from the party's credential rule only, never from `min_acr`, and compliance stays continuous: revoking a credential revokes every session holding it as a factor, and the party's `min_acr` and credential rule are re-checked on every request and at each login, so a raised `min_acr` or a downgraded collaborator is locked out of the party's profile (`acr_too_low`, `credential_not_authorized`) on the next request even with a confirmation.

Authorizations and confinements are written by a party administrator (`update` on the party) or by the credential's own holder acting through a session that already satisfies the party's gate through another factor; the evaluation excluding the credential is `mod_users.profile_availability_excluding`, so a credential never authorizes itself. The credential's holder must hold an active profile in the party. The administrator door confines only party-controlled credentials (those of a managed user of the party, and OIDC credentials of a provider bound to the party); an outside collaborator's personal credential needs its holder, and the holder door is refused when clearing a party-controlled credential's confinement. A caller that is neither gets `403`, or the masked `404` when it cannot read the party; every write needs a fresh authentication. Availability changes take effect on the holders' next request.

Ending a profile (an administrator ending it, or the holder declining or leaving) deletes the holder's credential authorizations for the party, revokes the holder's credentials confined to it, and disables a managed user left with no open profile. `ProfileService.AdmitInTx` is the trusted, in-transaction admission another module (mod-billing seats) calls once the holder and party have consented: it returns the holder's active profile in the party, activating an invited one or creating an active one, and refuses a suspended one with `users.profile_suspended`; it has no HTTP route. Other modules react to creations and status changes with `ProfileService.AddLifecycleObserver` (in-transaction). See `api/openapi.yaml` for the request and response shapes.

## Additional documentation

- [AGENTS.md](./AGENTS.md) — build, test, and development commands for contributors and AI agents
- [docs/mod-users-spec.md](./docs/mod-users-spec.md) — feature specification and behavioral contracts
- [docs/architecture.md](./docs/architecture.md) — system design, sub-project relationships, and key design decisions
- [docs/architecture/sessions-and-tokens.md](./docs/architecture/sessions-and-tokens.md) — sessions as the revocation unit, the access-token carrier, liveness, refresh lineages, step-up, impersonation and the setup-token bootstrap
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
