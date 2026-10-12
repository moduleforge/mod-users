# GUI Apps Client Alignment

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Align the `@moduleforge/users-gui` apps client in `gui/src/lib/api.ts` with the server contracts that now decode strictly (project followup PRvD):

- mod-core's apps contract for create and update;
- mod-users' membership contract for `addMember`.

No component calls these wrappers today (planner grep), so this is a type and payload fix with no UI change.

## Requirements

1. Edit `CreateAppRequest` (`gui/src/lib/api.ts` ~332-335) and the `apps.create`/`apps.update` wrappers (~615-624) to match mod-core's `createAppRequest`/`updateAppRequest`: `slug` required, `name` required, and no `description`. Read the structs in `/Users/zane/playground/moduleforge/mod-core/api/httpapi/apps.go` (~129, ~269). Check whether `update` requires both fields. Adjust the `App` response type (`~306-312`) to the fields mod-core returns (`uuid`, `slug`, `name`, `created_at`; see `appRowResponse`), and keep any field another component reads.
2. Edit `AddAppMemberRequest` (~337-340) to the server's `assignUserRequest`: `user_uuid` plus an optional `roles: string[]` instead of `role: string`. Read it from `api/internal/handlers/apps.go`.
3. Do not change the response types of `getMembers` (`AppMembersResponse`). The server's member list returns internal ids and has a different shape; that is tracked by project followup ZJ5N. Note it in the report.
4. Grep `gui/src` and `/Users/zane/playground/moduleforge/app-*/gui/src` for any use of the changed types or wrappers. If an app uses them, list it in the report and do not edit app code.

## Validation

GUI dependencies are not installed in task worktrees. If the dispatch permits dependency installation, run from the worktree root `bun install --frozen-lockfile`, then `cd gui && bun run typecheck && bun test`. This needs `mod-core/gui` built from mod-core's own checkout (AGENTS.md "Working in worktrees"; [build notes](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree)). Otherwise report the typecheck and tests as not run; do not claim they pass.

```bash
git diff --stat -- . ':!plan/'                  # only gui/src/lib/api.ts
grep -n 'description' gui/src/lib/api.ts | grep -i app   # no CreateAppRequest description left
git diff --quiet -- bun.lock                    # lockfile untouched
```

## Metadata

architectural_impact: false

## Assumptions

- This task touches only `gui/`, so it is parallel-eligible with task 5.2.

## Status

- Outcome: succeeded (2026-10-11).
- Source files changed: `gui/src/lib/api.ts`, `gui/src/components/reset-password-page.tsx`, `gui/src/components/oidc-provider-add-modal.tsx`.
- `CreateAppRequest` is now `{ slug, name }` (both required); `App` is `{ uuid, slug, name, created_at }`. `update` takes `Partial<CreateAppRequest>`: mod-core treats a blank field on update as "leave unchanged", so neither field is required on update.
- `AddAppMemberRequest` is `{ user_uuid, roles?: string[] }` per mod-users `assignUserRequest`.
- `ResetPasswordRequest` field renamed `new_password` to `password` to match mod-users `auth/reset.go` (manager finding 9qmR); the call site in `reset-password-page.tsx` updated to match.
- Add-provider modal (manager finding jzT7) now surfaces the structured `ApiRequestError.details` messages when present, falling back to `err.message`.
- `getMembers` / `AppMembersResponse` unchanged (tracked by followup ZJ5N).
- No app GUI under `app-*/gui/src` uses the changed wrappers or types (grep).
- Validation: `bun run typecheck` passed; `bun test` passed (230 pass, 0 fail). Logs under `.flow/validation-logs/`. `bun.lock` untouched.
