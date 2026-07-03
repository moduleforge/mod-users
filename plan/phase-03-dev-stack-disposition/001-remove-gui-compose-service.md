# Remove Gui Compose Service

## Purpose and scope

Remove the broken/orphaned `deploy/local/docker-compose.yml` `gui` service and `gui/Dockerfile.dev`, and reconcile the directly-coupled comments and dev-stack docs so the documented local dev stack matches its actual behavior. **Decision: remove** (option A) — the canonical component preview becomes `make preview` (Ladle, added in Phase 05) and `app-mfdemo` remains the integration testbed. See [../notes/gap-analysis-and-scope.md `## Answer`](../notes/gap-analysis-and-scope.md).

No standard skill; follow the `## Requirements`. The `docs/architecture.md` and `docs/project-structure.md` ripples are handled in Phase 06 (`doc-updates`) to keep single ownership of `docs/`.

## Requirements

1. **Delete the `gui` service from `deploy/local/docker-compose.yml`** — the entire block at lines 183-208 (`gui:` through its `depends_on: - api`). Also fix the services-list header comment (line 13): remove the `#   - gui      : Next.js dev server (hot-reload via volume mount)` line.

2. **Delete `gui/Dockerfile.dev`** (`git rm`). It is broken beyond use: `context: ../../..` with pre-rename `core-module/`/`users-module/` paths, `npm ci` + `corepack pnpm`, and `next dev` against a `gui/` that no longer has any Next.js pages.

3. **Update `AGENTS.md`:**
   - "First-time setup" step 5 (line 58): the sentence `This starts Postgres (5432), Authelia (9091), Mailpit (8025), the API server (8080), and the GUI dev server (3000).` — remove the "and the GUI dev server (3000)" clause (the stack no longer runs a GUI container). Optionally append a short pointer that local component preview is via `make preview` (Ladle) and full integration via `app-mfdemo`.
   - Do not touch the "Known issues" bullets here (owned by Phase 01/04) or the yalc setup step.

4. **Update the root `Makefile`** dev-orchestration comment (lines 71-74):
   ```
   # `dev.start` runs the full stack in Docker containers:
   #   - Postgres, Authelia, Mailpit (infrastructure)
   #   - API server (Go, built from source, runs migrations on start)
   #   - GUI dev server (Next.js with hot-reload via volume mounts)
   ```
   Remove the `#   - GUI dev server (Next.js ...)` line so the comment matches the reduced stack. Leave the `_dev.urls` recipe as-is: its `Demo app: http://localhost:3000/auth/login (see app-mfdemo/)` line already refers to app-mfdemo (not the removed compose service) and remains correct.

## Validation

- `deploy/local/docker-compose.yml`: `grep -n 'gui\|Dockerfile.dev\|3000\|NEXT_PUBLIC' deploy/local/docker-compose.yml` returns nothing referencing a gui service; the file still parses (`make openapi.validate`-style YAML check, or `docker compose -f deploy/local/docker-compose.yml config -q` if docker is available).
- `gui/Dockerfile.dev` no longer exists (`git ls-files gui/Dockerfile.dev` empty).
- `AGENTS.md` step 5 no longer claims a "GUI dev server (3000)" in the stack.
- Root `Makefile` dev-orchestration comment no longer lists a GUI dev server; `_dev.urls` still shows the app-mfdemo demo-app URL.
- `grep -rn 'Dockerfile.dev' deploy AGENTS.md Makefile` returns no live references (historical `plan/summary-*.md` may still mention it and are exempt).

## Assumptions

- The remove decision is final for this session (recorded in the gap-analysis `## Answer`). Repurpose-to-Ladle remains a documented alternative; if the user later prefers it, this is easily amended since it only touches `docker-compose.yml`, `Dockerfile.dev`, and doc references.
- The compose `gui` service's port `3000` collided with app-mfdemo's own dev server; removing it eliminates that latent conflict.

## References

- [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md) — decision 1 (why the service is non-functional; the full doc-ripple list) and `## Answer` (remove).
- `deploy/local/docker-compose.yml` (lines 13, 183-208), `gui/Dockerfile.dev`, `AGENTS.md` (line 58), root `Makefile` (lines 71-74).

## Checkpoint hints

- After removing the compose `gui` service block + `Dockerfile.dev`.
- After the AGENTS.md + root Makefile comment edits.
