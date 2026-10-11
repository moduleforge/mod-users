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

Local-auth JWT settings (see [docs/jwt-eddsa.md](./docs/jwt-eddsa.md); full list in `.env.example`):

| Variable | Default | Purpose |
|---|---|---|
| `JWT_SECRET` | required for HS256; optional under EdDSA | HS256 secret; also signs OAuth state and step-up tokens. Under `JWT_ALG=EdDSA`, when unset those keys are derived from `JWT_SIGNING_KEY` |
| `JWT_ALG` | `HS256` | `HS256` or `EdDSA` signing for locally-issued tokens |
| `JWT_SIGNING_KEY` | unset | Ed25519 private key (PKCS#8 PEM; literal `\n` accepted); required for `EdDSA` |
| `JWT_SIGNING_KID` | unset | `kid` header for issued tokens |
| `JWT_VERIFY_PUBLIC_KEYS` | unset | Extra accepted public keys: one PKIX PEM, or JSON `kid` to PEM |

CORS is default-deny: set `CORS_ORIGINS` to a comma-separated list of exact front-end origins. An unset value emits no CORS headers outside `DEPLOY_MODE=local`, and any entry containing `*` is rejected at startup outside local mode. Entries are bare `scheme://host[:port]` origins (no trailing slash or path), matched case-insensitively. Upgrading deployments that relied on the old implicit wildcard must read [docs/cors.md](./docs/cors.md).

## Additional documentation

- [AGENTS.md](./AGENTS.md) — build, test, and development commands for contributors and AI agents
- [docs/mod-users-spec.md](./docs/mod-users-spec.md) — feature specification and behavioral contracts
- [docs/architecture.md](./docs/architecture.md) — system design, sub-project relationships, and key design decisions
- [docs/jwt-eddsa.md](./docs/jwt-eddsa.md) — EdDSA JWT signing, verify-only deployment, key rotation, and the `JWT_*` configuration
- [docs/cors.md](./docs/cors.md) — CORS default-deny policy, `CORS_ORIGINS` / `DEPLOY_MODE` behavior, and the upgrade note for deployments that relied on the implicit wildcard
- [docs/cookie-path.md](./docs/cookie-path.md) — `USERS_COOKIE_PATH`, the `oidc_state` cookie Path, and the strip-prefix proxy example
- [docs/consumer-upgrade-notes.md](./docs/consumer-upgrade-notes.md) — API, configuration and behaviour changes for consumers that compose, embed or verify tokens from this module, plus known limits
- [docs/project-structure.md](./docs/project-structure.md) — directory layout and sub-project conventions
- [next-steps.md](./next-steps.md) — forward-looking residue: pending manual verification and deferred work
- [.claude/CLAUDE.md](./.claude/CLAUDE.md) — Claude Code configuration and project-specific AI agent guidance

## License

[Apache 2.0](./LICENSE)
