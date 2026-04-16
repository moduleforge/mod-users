# Phase 3, Task 5 — Audit-log writer

## Context
Every mutation in the system must produce an audit row. We centralize this in a small writer that handlers call explicitly (rather than a magical interceptor) so the `before`/`after` snapshots are precise.

## Acceptance

`api/internal/audit/audit.go`:
```go
type Writer interface {
    Write(ctx context.Context, op string, resource string, targetEntityID *int64, before, after any) error
}

type pgWriter struct { q *modelaudit.Queries }
func New(q *modelaudit.Queries) Writer

// In WithUserContext, expose helpers:
func ActorID(ctx context.Context) int64           // panics if missing — middleware guarantees it
func AssumedID(ctx context.Context) *int64
```

Behavior:
- `Write` serializes `before`/`after` to JSONB (use `encoding/json`; nil → SQL NULL).
- Pulls `actor_user_id` and `assumed_user_id` from `UserContext` on `ctx`.
- Failures are LOGGED but do not fail the request — audit gaps are bad, but breaking the user's write is worse. Add metric `audit_write_errors_total` (Phase 8 will scrape).

Wire-up:
- Inject `Writer` into handler constructors that mutate (`PUT /v1/self`, future user/app/auth handlers).
- `PUT /v1/self` writes audit with `before` = user JSON pre-update, `after` = post-update.

## How to verify
- Unit test: writer call produces an `audit_log` row with the right `actor_user_id`, `op`, `resource`, and JSONB shape.
- When `assumed_user_id` is set on context, the row records both actor and assumed.
- Forced DB error in writer logs an error and increments the metric, but the test handler returns 200.

## Notes
- For writes that affect multiple objects (rare in v1), call `Write` per object.
- `op='login'` audits are written by the auth handlers (Phase 4) on successful login, with `target_entity_id = user.entity_id`.
- `op='assume'` is written by the assume-identity handler (Phase 5).
