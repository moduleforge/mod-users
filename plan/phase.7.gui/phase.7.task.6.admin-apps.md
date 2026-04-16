# Phase 7, Task 6 — Admin apps + apps_users management

## Acceptance

`/admin/apps`:
- Table: slug, name, member_count, archived, created_at.
- "New app" button → modal `{ slug, name }` → POST `/v1/apps`.
- Row actions: edit, archive.

`/admin/apps/[uuid]`:
- App details (editable name/slug).
- Members table: user (link to user detail), roles (chip array, editable), assigned_at.
- "Add member" → user picker (autocomplete from `/v1/users?q=…`) + roles input → POST `/v1/apps/:uuid/users`.
- Remove member action with confirm.
- Archive button (with double-confirm modal listing default-app side effects).

## How to verify
- Create app, add members, edit roles, remove member; counts and listings update consistently.
- Archive shows the side-effect summary (X users will lose default app) before confirming.

## Notes
- Roles input is a comma-separated chip editor; v1 doesn't validate role names beyond format.
