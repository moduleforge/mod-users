# Phase 1, Task 1 — Monorepo skeleton + workspaces

## Context
`users-module/` currently contains only a `package.json`. We need the full sub-project layout in place before any code can be written.

## Acceptance
- `users-module/model/` exists with empty `migrations/` and `queries/` subdirs and a placeholder `README.md`.
- `users-module/api/` exists with `cmd/server/main.go` (prints "users-api up" and exits 0), `internal/`, `go.mod` (`module github.com/moduleforge/users-module/api`), Go 1.23.
- `users-module/gui/` exists as a freshly-initialized Next.js 15 App Router project with TypeScript strict mode, Tailwind, and shadcn/ui base. Use `pnpm create next-app@latest` non-interactively if possible.
- `users-module/deploy/local/`, `deploy/serverless/`, `deploy/k8s/` exist with placeholder `README.md` files.
- Root `users-module/go.work` includes `./api`.
- Root `users-module/pnpm-workspace.yaml` includes `gui`.
- Update root `users-module/package.json` to reflect the workspace (no scripts beyond pnpm passthrough).

## Out of scope
Make targets (Task 1.2), docker-compose (Task 1.3), OTel/config (Task 1.4).

## How to verify
- `cd users-module && go work sync` succeeds.
- `cd users-module/api && go run ./cmd/server` prints `users-api up`.
- `cd users-module/gui && pnpm install && pnpm build` succeeds.

## Reference
- Plan summary: `users-module/plan/summary.md`
- Make conventions: user memory `feedback_make_conventions`
