# Phase 7 — GUI

## Goal
Next.js 15 App Router GUI delivering all end-user and admin features against the api. Runs against the same docker-compose stack.

## Tech
- Next.js 15 App Router, React 19, TypeScript strict
- Tailwind CSS + shadcn/ui
- TanStack Query for data, react-hook-form + zod for forms
- `openid-client` for the OIDC code flow; signed-cookie session via `iron-session`
- API client generated from OpenAPI (Phase 8 Task 8.6 finalizes the spec; for now hand-write the types and migrate later)

## Layout
```
gui/app/
  (auth)/
    login/page.tsx              — local + OIDC login choice
    signup/page.tsx
    forgot/page.tsx
    reset/page.tsx              — token-bearing reset confirm
    verify/page.tsx
  (app)/
    layout.tsx                  — auth-required, app-context picker in header
    profile/page.tsx
    admin/
      users/page.tsx             — search + list
      users/[uuid]/page.tsx      — detail + edit + grant/revoke + assume
      audit/page.tsx             — recent global audit
      apps/page.tsx              — apps list + create
      apps/[uuid]/page.tsx       — app detail + members
api-client/                       — typed wrappers around the api
lib/auth.ts                       — session helpers
```

## Hard rules
- Never put internal IDs in URLs; always uuids.
- App switcher in header reads `/v1/self/apps`; switching sets a cookie that the api client sends as `X-App` on every request.
- Assumption banner: when `assume` claim is set, show a sticky red banner "Assuming as <email> — End assumption" with a click-to-end action.
- Admin routes hide if `is_admin === false` AND show 404 if accessed directly while non-admin.
- All forms server-validate via api responses; client-side zod is for fast UX only.

## Tasks
- 7.1 App shell + auth context + OIDC client
- 7.2 Login (local + OIDC) + signup + forgot/reset/verify
- 7.3 Profile view/edit
- 7.4 Admin user search + detail (edit, grant/revoke, assume)
- 7.5 Admin audit log viewer (per-user, per-object, global)
- 7.6 Admin apps + apps_users management

## Notes
- Use shadcn/ui's data table for users/audit lists.
- Toaster for action results; confirm-modals for destructive actions (delete, revoke admin, end assumption).
- The previously-evaluated `react-best-practices.md` at the project root applies to this phase — agents implementing Phase 7 should read it.
