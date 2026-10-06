# Throwaway Postgres Integration Harness

## Purpose and scope

Make the `api/internal/authz` integration suite (`//go:build integration`, single `TestMain` in `api/internal/authz/authz_integration_test.go`, shared by `anonymous_actor_integration_test.go` and `ssh_keys_integration_test.go`) runnable against a **throwaway** Postgres 16+ container with a unique name and a random host port, instead of only against the shared `users-module-postgres` container on port 5432. This is the prerequisite for the integration reproduction in task 002 and the regression tests in task 003. Implement with the standard `implement-task` procedure. No production code changes.

The Docker host is **shared**. Never run `make dev.start`, `dev.restart`, `dev.stop`, or `clean.data`, and never touch the `users-module-postgres` container or any container this task did not create.

## Requirements

1. Add a port override to the suite's DSN construction. Today `resetDB`, the goose migration DSN, and the pool DSN hard-code `:5432`. Add an `AUTHZ_DEV_PG_PORT` env var (default `5432`), resolved once next to `resolveHost()`, and use it in all three DSNs. Credentials stay `users:users`.
2. When `AUTHZ_DEV_PG_HOST` is set explicitly, `checkPrereqs` must **not** require the `users-module-postgres` container to be running. Skip the `docker inspect` check entirely in that case. Keep the `goose`-on-`PATH` and composed-migrations-dir checks. Behavior with no env vars set must be unchanged.
3. Update the run instructions in the `authz_integration_test.go` file header to document the throwaway-container recipe as the preferred way. For example:

   ```bash
   name="mod-users-authz-integ-$(openssl rand -hex 4)"
   docker run -d --rm --name "$name" -e POSTGRES_USER=users -e POSTGRES_PASSWORD=users \
     -e POSTGRES_DB=postgres -p 127.0.0.1::5432 postgres:16
   port="$(docker port "$name" 5432/tcp | head -1 | sed 's/.*://')"
   # wait for readiness: until docker exec "$name" pg_isready -U users; do sleep 1; done
   make -C model compose
   (cd api && AUTHZ_DEV_PG_HOST=127.0.0.1 AUTHZ_DEV_PG_PORT="$port" \
      go test -tags=integration -p 1 -count=1 ./internal/authz/...)
   docker rm -f "$name"
   ```

   Keep the existing shared-container text as the legacy alternative, and note that it must not be used on a shared Docker host. Update the cross-references in the headers of `anonymous_actor_integration_test.go` and `ssh_keys_integration_test.go` only if they would otherwise be wrong.
4. Add a short subsection to `AGENTS.md` under "Test commands" with the same throwaway recipe for `api/internal/authz`. Keep it brief and link to the test file header for detail.
5. Out of scope: `api/internal/config/jwtsecret_bootstrap_integration_test.go`, which has its own hard-coded 5432 and container name. Do not change it unless trivially parallel. If left alone, record a followup (`followups_add`, `type:test-gap`) noting it shares the limitation.

## Validation

- `cd api && go vet -tags=integration ./internal/authz/...` succeeds (run `make preflight` first in a worktree so the sibling `replace` paths resolve).
- `cd api && go test ./internal/authz/...` (unit, no tag) passes unchanged.
- Start a throwaway `postgres:16` container per the recipe (unique name, random port). Run `make -C model compose`, then the full `-tags=integration` suite against it. Every existing integration test passes, and the run does not skip with "container users-module-postgres is not running". Remove the container afterwards and confirm with `docker ps -a --filter name=<name>` (expect no rows).
- With no env vars set, `checkPrereqs` still performs the `users-module-postgres` inspect. Code review is enough here; do not start or inspect the shared container.
- `git diff --stat` (excluding `plan/`) touches only `api/internal/authz/*_integration_test.go` and `AGENTS.md`.

## Assumptions

- `goose` is on `PATH` (`/Users/zane/go/bin/goose` on the dev machine), Docker is available, and the sibling checkouts mod-core, mod-authz, and mod-audit exist next to mod-users.
- `make -C model compose` writes the gitignored `model/schema/migrations`. It does not need a database.

## References

- `api/internal/authz/authz_integration_test.go`: `TestMain`, `checkPrereqs`, `resolveHost`, `resetDB`, and the goose and pool DSNs.
- `AGENTS.md`: "Test commands" and "Working in worktrees".
- `docs/mf-standards/building-common.md#building-inside-a-task-worktree`: sibling symlinks (`make preflight`).

## Status

- Outcome: succeeded (2026-10-05).
- Validation: `go vet -tags=integration` and unit tests pass; full integration suite passed against a throwaway postgres:16 container (random port, removed afterwards, no leftover rows); no-env path unchanged by review.
- Files: `api/internal/authz/authz_integration_test.go`, `AGENTS.md`.
- jwtsecret_bootstrap_integration_test.go left alone; followup filed (type:test-gap).
