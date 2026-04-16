# Phase 7, Task 5 — Admin audit viewer

## Acceptance

Reusable `<AuditTable />` component:
- Props: `source: { kind: 'global' } | { kind: 'user', uuid } | { kind: 'object', uuid }`, optional `defaultFilters`.
- Loads the matching api endpoint, supports pagination + op filter + date range.
- Row layout: timestamp, op badge, resource, actor (clickable to user detail), assumed (if any), target (clickable when resolvable), expandable diff (before vs after JSON, syntax highlighted via `react-syntax-highlighter`).

`/admin/audit` — global view using `<AuditTable source={{kind:'global'}} />`.

In `/admin/users/[uuid]` activity tab — `<AuditTable source={{kind:'user', uuid}} />`.

In a future per-object detail page or the user-detail page itself, an "object history" tab can use `<AuditTable source={{kind:'object', uuid}} />` (for now, just show it on user detail keyed by entity uuid).

## How to verify
- All three views load and paginate.
- Op filter narrows results.
- Date range filter narrows results.
- Diff expand is readable and copyable.

## Notes
- Performance: only render the diff on row expand, not in the list payload.
