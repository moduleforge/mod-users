# Phase 7, Task 4 — Admin users (search + detail + actions)

## Acceptance

`/admin/users`:
- Search bar (debounced) → GET `/v1/users?q=…`.
- Filter chips: kind, archived, admin-only, app.
- Data table columns: email, display name, kind, default app, is_admin, archived, created_at.
- "New user" button → modal with kind picker + per-kind fields → POST `/v1/users`. Optional "send invite" checkbox.

`/admin/users/[uuid]`:
- Profile section with same edit fields as `/profile`, plus admin-only fields (email, kind switch is NOT supported in v1).
- Actions: Grant admin / Revoke admin (with last-admin guard message), Assume identity, Archive.
- Memberships: list of apps + roles, with add/remove (POST/DELETE on `/v1/apps/:uuid/users`).
- Activity tab: rendered audit list (uses Phase 7 Task 5 component).

## How to verify
- Search returns matches; pagination works.
- Create flow lands the user in DB and the row appears in the list.
- Assume → header shows red banner; navigation continues as the assumed user.
- Archive → row disappears from default view, present with `?archived=all`.

## Notes
- For destructive actions, confirm-modal pattern is mandatory.
- Banner end-assume button posts to `/v1/self/end-assumption` and replaces session.
