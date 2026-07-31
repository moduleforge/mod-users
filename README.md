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

## Additional documentation

- [AGENTS.md](./AGENTS.md) — build, test, and development commands for contributors and AI agents
- [docs/mod-users-spec.md](./docs/mod-users-spec.md) — feature specification and behavioral contracts
- [docs/architecture.md](./docs/architecture.md) — system design, sub-project relationships, and key design decisions
- [docs/project-structure.md](./docs/project-structure.md) — directory layout and sub-project conventions
- [.claude/CLAUDE.md](./.claude/CLAUDE.md) — Claude Code configuration and project-specific AI agent guidance

## License

[Apache 2.0](./LICENSE)
