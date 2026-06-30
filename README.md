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

**React component library** (TypeScript/React, requires Bun workspace or yalc for local development):

```sh
npm install @moduleforge/users-gui
# or, within a Bun workspace:
bun add @moduleforge/users-gui
```

> The GUI package depends on `@moduleforge/core-gui` (a peer dependency). For local development the package is linked via yalc rather than published to a registry.

## Additional documentation

- [AGENTS.md](./AGENTS.md) — build, test, and development commands for contributors and AI agents
- [docs/mod-users-spec.md](./docs/mod-users-spec.md) — feature specification and behavioral contracts
- [docs/architecture.md](./docs/architecture.md) — system design, sub-project relationships, and key design decisions
- [docs/project-structure.md](./docs/project-structure.md) — directory layout and sub-project conventions
- [.claude/CLAUDE.md](./.claude/CLAUDE.md) — Claude Code configuration and project-specific AI agent guidance

## License

[Apache 2.0](./LICENSE)
