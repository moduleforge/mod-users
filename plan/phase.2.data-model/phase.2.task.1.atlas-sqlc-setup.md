# Phase 2, Task 1 — Atlas + sqlc setup

## Context
We use Atlas for declarative, versioned migrations and sqlc for type-safe Go queries. Both must be configured before any migration is written.

## Acceptance
- `model/atlas.hcl` with environments `local` (URL from env), `ci`, and `dev` shadow DB target.
- `model/sqlc.yaml` v2 config: engine `postgresql`, queries `./queries`, schema `./migrations`, generates Go into `model/internal/<concept>/` packages with snake_case → CamelCase mapping; emits `EmitInterface` + `EmitJSONTags`.
- `model/Makefile` adds: `migrate.new NAME=…`, `migrate.up`, `migrate.status`, `gen` (runs sqlc), `verify` (atlas validate + sqlc compile dry-run). All delegated to from root via `model.migrate.up` etc.
- README in `model/` documents the install + commands.

## How to verify
- `make model.verify` succeeds on an empty migrations dir.
- `make model.gen` produces no files (no queries yet) and exits 0.

## Reference
- atlas.hcl examples: https://atlasgo.io
- sqlc v2 docs: https://docs.sqlc.dev
