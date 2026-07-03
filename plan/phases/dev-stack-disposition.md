# Phase — Dev-Stack Disposition

## Goals

Resolve the broken/orphaned `deploy/local/docker-compose.yml` `gui` service and `gui/Dockerfile.dev`, then reconcile every doc/comment that describes the local dev stack's GUI. **Blocked on the user decision** (remove vs. repurpose to Ladle) — the task content branches on the answer, which is why full decomposition is deferred to re-invocation.

## Inputs

- The user's answer to the docker-compose disposition question.
- [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md), decision 1 (why the service is currently non-functional; the doc ripple list).
- Current `deploy/local/docker-compose.yml` (lines 13, 183-204), `gui/Dockerfile.dev`, `AGENTS.md` (lines 54-58 dev stack; line 145 known issue), root `Makefile` (line 74 comment; line 129 demo-app URL), `docs/architecture.md` (line 103), `docs/project-structure.md` (line 88).

## Outputs

- **If remove:** `gui` service block deleted from `docker-compose.yml` (+ line 13 comment); `gui/Dockerfile.dev` deleted; `AGENTS.md` dev-stack step no longer claims a port-3000 GUI dev server as part of the stack (points to `make preview`/app-mfdemo instead); root `Makefile` line 74 comment corrected; architecture/project-structure port-3000 rows reconciled (handled in doc-updates phase).
- **If repurpose:** `Dockerfile.dev` rewritten (bun, no pnpm/npm, no `core-module`/`users-module` paths, runs `ladle serve --port 61002`/`--hostname 0.0.0.0`); compose `gui` service updated (correct build context, port, volume mounts to existing files only); AGENTS.md/Makefile/docs describe the containerized Ladle preview.
- A dev stack whose documented GUI behavior matches its actual behavior, with no references to deleted files (`next.config.ts`, `postcss.config.mjs`, `eslint.config.mjs`, `components.json` if removed).
