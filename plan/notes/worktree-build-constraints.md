# Worktree build constraints

## Purpose and scope

What an implementing agent must do before a Go build or test succeeds in a
mod-users task worktree for this plan, and which validation command to use.
Sourced from `AGENTS.md` ("Working in worktrees", "Test commands"), the root
`Makefile`, `api/Makefile`, and `scripts/link-siblings.sh`.

## Sibling symlinks are required

`api/go.mod` carries `replace` directives with sibling-relative paths
(`../../mod-core/{api,model}`, `../../mod-audit/*`, `../../mod-authz/*`). They
resolve from the main checkout but break at the extra path depth of anything
under `mod-users/worktrees/`. `scripts/link-siblings.sh` fixes this at any
nesting depth by planting compatibility symlinks in the worktree's parent
directory. Run it first, from the worktree root:

```sh
make preflight.siblings     # == bash scripts/link-siblings.sh
```

`make preflight` (the full version) also runs the model, api, and gui
preflights; `preflight.siblings` alone is sufficient for a Go-only change and
avoids the gui toolchain entirely.

## Dependencies are not installed

Go module dependencies are not pre-installed in this checkout, so a first
`go build` or `go test` may need to download modules. Report the outcome rather
than silently narrowing scope if that download is unavailable.

## Use the api-scoped test command, not the root one

Root `make test.unit` fans out to `test.unit.model`, `test.unit.api`, **and**
`test.unit.gui`. The gui leg needs `bun install` plus a prebuilt sibling
`mod-core/gui`, which `AGENTS.md` documents as a known rough edge inside
worktrees and which has nothing to do with a Go-only change. Scope to the api
sub-project instead:

```sh
cd api && go test ./...                  # module-wide unit suite (primary gate)
cd api && go test ./internal/authz/...   # the directly affected package (minimum bar)
```

## Integration tests are build-tagged and out of reach

`api/internal/authz/authz_integration_test.go` and
`anonymous_actor_integration_test.go` both carry `//go:build integration` and
require a live Postgres (`make dev.start`). They are excluded from an untagged
`go test` run. Do not attempt them; the unit suite is the gate for this change.

## `docs/mf-standards/` may be empty

It is a git submodule and may be uninitialized (an empty directory) in a
worktree. No task in this plan reads or edits anything under it — the canonical
authorization-design doc is reconciled by mod-core's own documentation phase.
Never commit into or modify that submodule from here.
