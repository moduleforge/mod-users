# Plan Session Summary — Anonymous System Actor

## What was planned and why

`mod-users` had no way to give a genuinely tokenless HTTP request (no `Authorization`
header at all) a real, low-privilege actor identity before authorization checks ran — such
requests either 401'd via `RequireAuth` or had to be special-cased per route. This session
implemented, entirely within `mod-users`, the anonymous-actor mechanism specified by the
[anonymous-actor architecture proposal](./notes/anonymous-actor-architecture-proposal.md): a
zero-authority, well-known `system_actor` entity (slug `anonymous`) that a missing
`Authorization` header now resolves to via a new `ResolveActorOrAnonymous` middleware,
instead of a 401. Closing this gap on the `mod-users` side is what unblocks mod-repos
followup `Jhkw`.

The proposal document was treated as the source of truth; this plan implemented it without
re-deriving or re-litigating its decisions. Three supporting research notes (current-state
audit, mod-repos consumer context, design-space survey) and one verification note (mfgen
expr/middleware pattern) provided background.

Hard constraints carried through the whole session: no changes to `authz.go`, mod-core's
`opctx`, `grant_table.go`, `anon_tokens`, `user_accounts`, `RequireAuth`'s behavior, or any
existing route's `scope:`; `system_actor` never added to `authzSlugs`; the existing
`POST /v1/auth/anonymous` guest-account flow left unconverged with this mechanism (only
terminology/docs disambiguated); no mfgen `scope: anonymous` literal; and the anonymous actor
must never hold a grant, own an entity, join an actor group, or be resolvable from a JWT.

The work was planned as two phases: Phase 01 (six tasks implementing the mechanism) followed
by the standard Phase 02 documentation phase, because the change introduces a new subsystem
(a zero-authority shared actor identity) plus a cross-cutting auth-pipeline addition.

## What shipped

### Phase 01 — Anonymous System Actor

1. **`001-system-actor-migration`** (merge `6ec008c2`) — Added
   `model/migrations/sql/0101_system_actors.sql` seeding the `system_actor` type, the
   `system_actors` class-table-inheritance child table with its type-check trigger, the
   single seeded `anonymous` row, and the `entities_no_system_actor_owner`
   `BEFORE INSERT OR UPDATE` ownership-guard trigger on `entities`, plus the
   `GetSystemActorBySlug` sqlc query and regenerated `model/db/` output. Every behavioral
   claim — seed values, type shape, ownership-guard firing, type-check firing, and
   trigger-ordering on INSERT/UPDATE — was independently verified against a live Postgres
   instance. One known divergence from the task doc: the Down migration cannot delete the
   seeded `types` row because of mod-core's pre-existing append-only trigger on `types`;
   resolved by following the established mod-authz `0501` precedent and documenting it
   inline (tracked as followups `Hzvv` / `b1vv`, see below).

2. **`002-anonymous-actor-service`** (merge `da425de5`) — `AnonymousActor` holder and
   `NewAnonymousActor` constructor in `api/internal/auth/`: slug lookup, missing-row hard
   failure, and the no-grants boot assertion. The task's retry attempt found the actual scope
   already complete and intact from a prior commit; the one blocking issue was a stale
   hand-written `db.Querier` test stub not updated for the `GetSystemActorBySlug` method task
   001 added — fixed with one mechanical stub method, restoring `go vet`/build/lint/test
   module-wide. (Followup `VgLt` notes this service ended up unreferenced elsewhere in the
   diff — no blocking dependency on it materialized.)

3. **`003-requireauth-helper-extraction`** (merge `835d6ff0`) — Added nine characterization
   tests pinning every branch of `RequireAuth`'s pre-refactor behavior, confirmed green
   against the original code, then extracted the error-mapping and success-population logic
   into two unexported helpers (`writeAuthError`, `contextWithAuthenticatedActor`) with zero
   behavioral change, verified by diff review and the unchanged test suite passing
   post-refactor. `RequireAuth` is now a thin composition of `AuthenticateRequest` plus the
   two helpers, ready for task 004 to reuse.

4. **`004-resolve-actor-or-anonymous-middleware`** (merge `9746b136`) — Added
   `ResolveActorOrAnonymous` in `api/internal/auth/anonymous_actor.go` with the proposal's
   exact signature, plus the `WithAnonymousActor`/`IsAnonymousActor` context accessor pair.
   Delegates every non-diverging outcome to task 003's shared helpers; the body contains
   exactly one branch `RequireAuth` lacks — missing `Authorization` header routes to the
   anonymous actor plus a marker, instead of 401. Malformed header, bad signature, expired
   token, `ErrUserGone`, and mapper/resolver faults all keep their pre-existing 401/500
   responses, closing the confused-deputy downgrade risk (mutation-tested). `principal.go`'s
   context-key declaration was regrouped into one `iota` block so the new key cannot alias
   `userContextKey`, with a test that detects aliasing. Sixteen test functions/rows cover all
   nine required cases plus the key-aliasing hazard.

5. **`005-facade-and-manifest-wiring`** (merge `7fe3a564`) — Wired the capability through to
   the outside world: `api/auth/auth.go` re-exports `AnonymousActor`, `NewAnonymousActor`,
   the manifest-facing adapter `NewResolveActorOrAnonymous` (unwraps the holder to a bare
   entity id), the plain `ResolveActorOrAnonymous` re-export, and `IsAnonymousActor`.
   `moduleforge.module.yaml` gained the `anonymousActor` service and
   `resolveActorOrAnonymous` middleware entries verbatim to the proposal. The hand-written
   dev server got equivalent construction plus a minimal demonstration route, added without
   touching any existing route or the `authzSlugs` list. `mfgen` confirmed the manifest
   resolves as specified; a live boot test confirmed both fail-fast-on-missing-seed-row and
   successful-boot-with-correct-middleware-gating. This completed Phase 01's functional
   scope.

6. **`006-db-invariant-integration-tests`** (merge `297e43b7`) — Added a build-tag-gated
   (`integration`) test file proving every DB-level invariant that makes the anonymous actor
   zero-authority: ownership-guard firing on INSERT and UPDATE (with the alphabetical
   trigger-order message pinned), a regression guard that ordinary entity writes still
   succeed, actor-group-membership rejection via the type-check trigger, the structural
   impossibility of a `user_accounts` row (FK violation), `Authorize` returning
   `ErrForbidden` (not `ErrUnauthenticated`) for nil and real targets, and list/single-row
   symmetry of zero grants and empty accessible-ids. All 8 new tests pass alongside the full
   pre-existing suite (29 total); `go vet` clean; diff confined to one new file.

**Post-gate review fix (Phase 01):** the phase's boundary review gate flagged a doc-comment
precision issue on `AnonymousActor` and a missing nil-check on
`NewResolveActorOrAnonymous`. Fixed in commit `3ee5649` ("narrow `AnonymousActor` doc
comment; nil-guard `NewResolveActorOrAnonymous`"), merged via `1f2eba1`. This was handled as
an ad-hoc post-gate hygiene fix rather than its own plan task.

### Phase 02 — Documentation Updates

1. **`001-update-architecture-docs`** (merge `56ca5b25`) — Updated `docs/architecture.md`
   and `docs/mod-users-spec.md` to document the anonymous-system-actor subsystem, cross-
   checked directly against the merged code. Added the guest-account vs. anonymous-actor
   terminology split (with a comparison table), the `system_actors` data-model entry and its
   enforcement rationale, the middleware's opt-in wiring and never-downgrade-a-bad-token
   behavior, the per-IP rate-limiting and max-page-size preconditions, the
   `Vary: Authorization` caching note, the composition rule and its fail-closed 500, and the
   audit-attribution collapse. Corrected the two stale `is_anonymous` JWT-claim claims at all
   four cited doc locations plus the matching code comment (comment-only change). All
   task-specified grep validations passed. This completed the anonymous-actor plan.

**Post-gate review fix (Phase 02):** the phase's boundary review gate flagged a
documentation-accuracy gap about `mfgen` reachability roots. Fixed in commit `906a9aa`
("clarify `resolveActorOrAnonymous` is an unconditional mfgen reachability root"), merged via
`d1abcef`. Also handled as an ad-hoc post-gate hygiene fix rather than its own plan task.

## Key decisions

- **Down-migration precedent over unattainable rollback.** The seeded `system_actor` row in
  `types` cannot be deleted on rollback because mod-core's pre-existing `types_reject_mutation`
  trigger rejects every `DELETE FROM types`. Task 001 followed the established mod-authz
  `0501_authz_actor_groups.sql` precedent (leave the row in place, document inline) rather
  than fighting the platform-wide constraint. The task doc's own Validation wording and the
  architecture proposal's Down-migration description were left inaccurate on this point;
  correcting them is tracked as followups `Hzvv` and `b1vv`.

- **Shared-helper extraction before the new middleware, not alongside it.** Task 003 pinned
  `RequireAuth`'s pre-refactor behavior with characterization tests first, then extracted
  `writeAuthError`/`contextWithAuthenticatedActor` with zero behavioral change, so that task
  004's `ResolveActorOrAnonymous` could reuse proven-identical logic rather than duplicating
  or subtly diverging from `RequireAuth`.

- **Empty `Authorization` header is treated as absent.** `ResolveActorOrAnonymous` uses
  `r.Header.Get`, which cannot distinguish a present-but-empty header from a genuinely
  missing one, so an empty header takes the anonymous branch — consistent with
  `RequireAuth`'s pre-existing behavior and left unchanged per task instruction (followup
  `0EsQ`).

- **Context-key collision risk closed structurally, not just tested.** Task 004 regrouped
  `principal.go`'s context-key declarations into one `iota` block specifically so the new
  anonymous-actor context key cannot alias `userContextKey`, backed by a test that would
  detect aliasing if it recurred.

- **No production route consumes the mechanism yet, by design.** Task 005 wired the facade,
  manifest, and dev server, plus one demonstration route, but no real route opts into
  `ResolveActorOrAnonymous` in this plan — the middleware's hard preconditions (per-IP rate
  limiting, never combining with `requireAuth`/`requireVerifiedEmail`, `Vary: Authorization`)
  are enforced by neither the middleware nor any phase-01 code, by design, pending a first
  real consumer (followup `w4ob`).

- **`system_actor` stays invisible to authorization listing.** Consistent with the hard
  constraint, `system_actor` was never added to `authzSlugs` in `api/cmd/server/main.go`, and
  no `accessible_system_actor_ids_for_actor` function was generated — the anonymous actor
  remains structurally excluded from every list path.

## Follow-up items

Filtered to items tagged `plan/phase:anonymous-actor` or `plan/phase:doc-updates` in
`plan/followups.yaml`, all dated during this session (2026-08-04/05):

- **`t5ut`** — `make lint` (full) fails on a pre-existing `model/` shadow-db-lint Postgres
  schema issue, unrelated to this diff.
- **`cDFE`** — `make test.unit` (full) fails on the already-tracked flaky
  `TestNewStepUpConsumedCache_JanitorStopsOnCancel` (see `5RbD`, `ljG1`); recurred here.
- **`VJg2`** — `AssumedUser` characterization gap: `resolver_test.go`'s stub pattern alone
  doesn't cover the `AssumedUser`/sudo-actor branch.
- **`b1vv`** — Task 001's Down-migration prose and a Validation bullet claim the seeded
  `system_actor` row is gone after rollback; unsatisfiable given mod-core's
  `types_reject_mutation` trigger. Recommend correcting the architecture proposal and task
  doc wording. (Duplicate/companion of `Hzvv` below.)
- **`edGE`** — Environment: a stale, orphaned `users-module-postgres` Docker container (from
  a pre-rename repo layout) binds host port 5432 and blocks `make dev.start`; recommend
  cleanup or rename.
- **`ljG1`** — `TestNewStepUpConsumedCache_JanitorStopsOnCancel` in `api/auth` is flaky
  (goroutine-count timing), reproduces on branch HEAD independent of this plan's changes;
  already tracked as `5RbD`.
- **`WAvY`** — Bare `make lint` from repo root doesn't pass `SHADOW_DB_PREREQ_DIRS` to
  `lint.model`, so it fails unless invoked with the mod-core migrations path set explicitly.
- **`nAXW`** — `make lint` (whole) doesn't pass: `lint.model`'s shadow-db-lint fails applying
  mod-users' `0100_schema.sql` against an ephemeral Postgres lacking mod-core migrations
  (`legal_entities` missing); reproduced at branch base, unrelated to this plan's files.
  Standing tooling gap.
- **`0EsQ`** — Not a defect but load-bearing: an empty `Authorization` header is
  indistinguishable from absent, so it takes the anonymous branch — worth a sentence in the
  phase-02 docs (addressed at least partly by task 001 of phase-02; confirm coverage if
  revisited).
- **`W9Er`** — Coupling note: `anonymous_actor_test.go` deliberately consumes
  `middleware_test.go`'s and `resolver_test.go`'s helpers, so a future rename in
  `middleware_test.go` would break it; documented in a header comment.
- **`VgLt`** — Task 002 (`AnonymousActor`/`NewAnonymousActor`) ended up not referenced
  anywhere in the final diff; no blocking dependency on it ever materialized.
- **`A4Mo`** — Minor: `lint.model`'s shadow-db-lint step fully succeeded on one run rather
  than reproducing the `legal_entities`-missing failure flagged elsewhere; possibly
  intermittent/environment-dependent, worth noting.
- **`EMIS`** — Environmental: this host's native Homebrew `postgresql@14` intercepts
  `localhost:5432` ahead of the `users-module-postgres` container's port-forward, a distinct
  issue from the credential-mismatch scenario in `edGE`; documented as a workaround so future
  tasks don't need to rediscover it.
- **`MwXo`** — `make dev.start` cannot be run from inside a task worktree (only the main
  checkout) due to a fixed-depth Docker build context assumption; pre-existing limitation,
  worth a follow-up if task worktrees should support this validation step directly.
- **`Hzvv`** *(type:bug)* — Task 001's task doc Validation section and the architecture
  proposal's Down-migration description both wrongly claim the seeded `system_actor` `types`
  row is gone after `goose down`; unsatisfiable given mod-core's `types_reject_mutation`
  trigger. The Down migration itself correctly follows mod-authz `0501` precedent
  (leave the row, document inline) — only the prose needs correcting. Also notes a
  down-then-up round trip fails on a duplicate-key violation for `types_slug_key` since the
  row survives rollback.
- **`9PN1`** *(type:optimization, efficiency-001)* — `ResolveActorOrAnonymous`'s no-header
  fallback branch re-reads `r.Header.Get("Authorization")` even though
  `AuthenticateRequest` already read the same header one call earlier. Small cost (Go's
  textproto fast-paths a canonical key) but duplicated work on a stated hot path. Recommend
  `AuthenticateRequest` communicate the header state it already computed back to the caller.
- **`4lm1`** *(type:optimization, efficiency-002, informational)* — The
  `entities_no_system_actor_owner` trigger is unscoped `BEFORE INSERT OR UPDATE ON entities`,
  so it runs on every `entities` write across the entire composed platform (not just
  mod-users) whenever `owner_id` is non-NULL. Per-invocation cost is a small indexed `EXISTS`
  lookup, already reasoned about in the migration's comment, but the platform-wide blast
  radius extends beyond the "entirely within mod-users" framing and is worth confirming
  against the feature's performance envelope.
- **`npOU`** *(type:documentation, pre-existing)* — `next-steps.md` at repo root is a
  substantive prose document not reachable from the README/AGENTS.md link chain and not
  covered by the `plan/` exemption. Recommend linking it or relocating it under `plan/` or
  `docs/`.
- **`3QbL`** *(type:security, security-002)* — Task 005's `/v1/anonymous-demo` route in
  `api/cmd/server/main.go` is gated only by `requireConfirmed` + `resolveActorOrAnonymous`
  (no `RequireAuth`), echoing `IsAnonymousActor` and the resolved actor entity id to any
  caller. It ships in the real, independently buildable `api/cmd/server` image
  (`make image.build`) with no build tag or config flag excluding it. Disclosed info is
  low-sensitivity, but the route confirms the anonymous-actor mechanism is live with zero
  rate limiting. Needs a manager/team decision: gate behind a dev/local-only flag, or remove
  once a real consuming route exists.
- **`w4ob`** *(type:security, security-004, informational)* — `ResolveActorOrAnonymous`'s
  documented hard preconditions (per-IP rate limiting, never composing with
  `requireAuth`/`requireVerifiedEmail`, `Vary: Authorization`) are enforced by neither the
  middleware nor any phase-01 code, by design. No consuming route opts in yet. Track as a
  checklist item for the first real route that adopts the middleware (e.g. the mod-repos
  consumer per the architecture proposal).
- **`1ZLT`** *(type:documentation, pre-existing)* — `docs/CLAUDE.md` is an empty,
  auto-generated stub not reachable from the README link chain; the standards' link-chain
  exemption covers only `.claude/`, not stray `CLAUDE.md` files elsewhere. Flagged for
  manager/standards judgment rather than treated as a hard defect.

Not carried forward here (out of this plan's own phase tags): `5RbD` (`route-wiring`
phase — duplicate of `ljG1`/`cDFE` already listed above), `biJk` (`route-wiring` phase,
openapi.yaml gap), `HPJi` (`tags-frontend-foundation` phase, cross-repo `app-mftodo` issue).
