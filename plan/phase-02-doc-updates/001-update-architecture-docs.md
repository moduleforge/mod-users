# Update Architecture Docs

## Purpose and scope

Update mod-users' architecture and specification documents to reflect the anonymous system
actor shipped in Phase 01: a new subsystem (a shared, zero-authority actor identity) and a
cross-cutting addition to the authentication pipeline. Also correct two documented behaviors
that the plan's current-state audit proved do not exist in code.

Run the `update-architecture-docs` task-procedure at
`plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.

`role_doc: plugins/flow/references/roles/architect-backend.md`

The implications are primarily backend and API/component in nature — a new middleware in the
auth pipeline, a new manifest-provided service and middleware, and new public facade exports.
The supporting data-model change (one seeded entity type, one CTI table, one trigger) is
documented as part of that, not as the driving concern.

## Requirements

### Planned implementation task documents that surfaced these implications

All six carry `architectural_impact: true` and will have been completed by the time this phase
runs:

- `plan/phase-01-anonymous-actor/001-system-actor-migration.md`
- `plan/phase-01-anonymous-actor/002-anonymous-actor-service.md`
- `plan/phase-01-anonymous-actor/003-requireauth-helper-extraction.md`
- `plan/phase-01-anonymous-actor/004-resolve-actor-or-anonymous-middleware.md`
- `plan/phase-01-anonymous-actor/005-facade-and-manifest-wiring.md`
- `plan/phase-01-anonymous-actor/006-db-invariant-integration-tests.md`

Read the merged implementation before writing: the documentation must describe what actually
shipped, not what was planned.

### Architecture and spec files to review and update

- `docs/architecture.md` — the sections most affected are `## Data model` (the table at lines
  ~30-47), `## API layer` (~48-65, including the "All endpoints except register, login, and
  anonymous require an Authorization: Bearer header" claim, which is now incomplete),
  `## Authentication flow` (~66-88, the `**Anonymous.**` paragraph at line ~70), and
  `## Multi-channel account model` (~89-100, the `is_anonymous` paragraph at line ~91).
- `docs/mod-users-spec.md` (found via the `docs/*-spec.md` glob) — the affected sections are
  `### 15. Create an anonymous account and optionally upgrade it` (~168-177), `## Data model`
  (~200-217), `## API definition` (~218-281, in particular the `is_anonymous` line at ~231), and
  `## Security requirements` (~282-301, in particular the JWT claim statement at ~284).

Do not create new top-level documents; extend the existing ones in their established style.

### Content the updates must carry

1. **The guest-account vs. anonymous-actor distinction.** These are two separate mechanisms that
   are not being converged, and the docs must make that unmistakable. Adopt the proposal's
   terminology split — *guest account* for the existing per-device `POST /v1/auth/anonymous`
   flow, *anonymous actor* for the new shared identity — and carry the proposal's comparison
   dimensions: shape (session bootstrap vs. tokenless), identity (one persisted
   `natural_person` + `user_accounts` row per device vs. one shared row for all callers), state
   (owns things and has `anon_tokens` continuity vs. owns nothing, ever), upgradeability (yes vs.
   never), and authority (a normal actor that can hold grants vs. provably zero, holds no grants
   by construction). State explicitly that a caller who *does* hold a guest token and hits an
   anonymous-enabled route takes the normal authenticated branch and gets their own guest actor,
   not the shared one.
2. **The new subsystem itself.** The `system_actor` entity type and the `system_actors` table in
   the data-model sections; the seeded `anonymous` row; the `entities_no_system_actor_owner`
   guard and why it exists (an owner has unconditional, operation-ungated control, and
   `owner_id` is immutable after insert, so the mistake would be unrecoverable); the deliberate
   omission of `system_actor` from `authzSlugs`, which keeps it invisible to every list path;
   the fact that it cannot join an actor group because it descends from `entity`, not
   `legal_entity`; the boot-time no-grants assertion and the boot-time slug resolution that
   hard-fails a deployment whose migration has not been applied.
3. **The `ResolveActorOrAnonymous` middleware** in the authentication-flow narrative: what it
   does, that a missing `Authorization` header falls through to the anonymous actor while an
   invalid or expired token still returns 401 (no silent downgrade), that it never sets the sudo
   actor or the `UserContext`, and that `auth.IsAnonymousActor(ctx)` is the accessor for
   distinguishing it. Note that it is opt-in per route group via `scope: public` plus a named
   `middleware:` entry, with `requireOIDCConfirmed` first, and that no new `scope:` vocabulary
   was introduced.
4. **The per-IP rate-limiting precondition**, stated as a hard precondition rather than a
   suggestion: every anonymous request shares one actor id, so actor-based throttling is
   meaningless, and any route opting into the middleware must sit behind a per-IP/CIDR limiter
   at the edge or in the composition root. mod-users documents this and does not ship the
   limiter. Anonymous-enabled list endpoints should also enforce a hard maximum page size.
5. **The `Vary: Authorization` caching note.** Anonymous and authenticated responses for the same
   URL differ; any cache or CDN in front of an anonymous-enabled route must key on the
   `Authorization` header, or the first anonymous response gets served to an authenticated
   caller and vice versa. Consuming modules' handlers emit the header; mod-users' middleware does
   not.
6. **The composition rule.** `resolveActorOrAnonymous` must never appear on the same route group
   as `requireAuth` (contradictory) or `requireVerifiedEmail` (fails closed with a 500, which is
   acceptable but ugly). Document the fail-closed behavior so the 500 is recognizable when it
   happens.
7. **Audit attribution.** `audit_log.actor_entity_id` collapses to one value for all anonymous
   traffic, so the per-actor activity index is useless for that id; attribution for anonymous
   traffic is `opctx.RequestID` plus HTTP access logs. No module may authorize a mutating
   operation for the anonymous actor.
8. **Correct the two stale claims** the current-state audit found (§3e). Both are currently
   documented behaviors that do not exist in code:
   - The `is_anonymous` JWT claim (`api/internal/auth/local_jwt.go`) is **written but never
     read** — nothing in `jwt.go`, the claim mappers, or `resolver.go` extracts it, and neither
     `Principal` nor `UserContext` (`api/internal/auth/principal.go`) has a field for it. Where
     the docs mention the claim (`docs/architecture.md:70`, `docs/mod-users-spec.md:174`, `:231`,
     `:284`), say plainly that it is currently inert and is not consulted by any middleware.
   - `RequireVerifiedEmail` does **not** distinguish anonymous sessions via that claim. It checks
     only `EmailVerifiedAt == nil` (`api/internal/auth/require_verified.go:30`), so a
     guest account with no email is blocked from a `scope: verified` route exactly like an
     unverified named account. Correct any text implying otherwise.
   - The same stale claim is carried by a code comment at
     `api/internal/handlers/auth/anonymous.go:46-48` ("so middleware (e.g. RequireVerifiedEmail)
     can distinguish anonymous sessions without a database round-trip"). Correct that comment
     too. This is a **comment-only** edit: do not change the handler's behavior, and do not wire
     the inert claim through.

Do **not** repurpose the inert `is_anonymous` claim for the new mechanism — the anonymous actor
has no JWT at all, so a JWT claim is the wrong carrier by definition. Say so in the docs.

### Must not change

- No production behavior. The only code touched by this task is the stale comment in
  `api/internal/handlers/auth/anonymous.go`.
- Do not document a `scope: anonymous` manifest literal — it does not exist and is explicitly out
  of scope. If the docs mention future direction at all, frame it as a possible ergonomic
  follow-up on mfgen's side.
- Do not document mod-repos' side of the work (route-scope flips, its visibility arm, its
  end-to-end test) as mod-users' responsibility; at most, note that consuming modules must add
  their own public-visibility arm to **both** their decorating `Authorizer` and their
  list-scoping query, because the generated `accessible_*_ids_for_actor` function returns the
  empty set for the anonymous actor by design.
- Do not restructure either document; extend the existing sections.

## Validation

- `docs/architecture.md` and `docs/mod-users-spec.md` were both reviewed, and every one of the
  eight content items above is either present in the updated text or explicitly justified as
  not applicable in the task report.
- `grep -n -i "anonymous actor\|guest account" docs/architecture.md docs/mod-users-spec.md`
  shows the terminology split is used consistently, and no remaining bare use of "anonymous"
  is ambiguous between the two mechanisms.
- `grep -n "is_anonymous" docs/architecture.md docs/mod-users-spec.md
  api/internal/handlers/auth/anonymous.go` — every remaining occurrence either describes the
  guest-account derived boolean (`email IS NULL`) or explicitly states the JWT claim is inert
  and unread. No occurrence claims any middleware reads it.
- `grep -rn "RequireVerifiedEmail" docs/` shows no text claiming it distinguishes anonymous
  sessions by JWT claim.
- `grep -rn "Vary: Authorization" docs/` and `grep -rn -i "rate.limit" docs/architecture.md`
  each return at least one hit.
- `grep -rn "scope: anonymous" docs/` returns no match.
- `git diff api/internal/handlers/auth/anonymous.go` shows a comment-only change.
- The documented shape matches what actually shipped: cross-check the described middleware
  behavior, the manifest entry names (`anonymousActor`, `resolveActorOrAnonymous`), the facade
  export names, the table name (`system_actors`), the type slug (`system_actor`), the actor slug
  (`anonymous`), and the trigger name (`entities_no_system_actor_owner`) against the merged code.
- `make lint` and `make test.unit` pass (the comment edit must not break the build).

## References

- [Anonymous-actor architecture proposal](../notes/anonymous-actor-architecture-proposal.md) —
  the authoritative design; §"Composition with POST /v1/auth/anonymous" carries the comparison
  table and the two stale claims, §"Security considerations" carries items 4-7 above, and
  §"Migration / rollout notes — Phase 1" step 6 is this task's charter.
- [Current-state audit](../notes/current-state-audit.md) — §3e, the evidence that the
  `is_anonymous` claim is inert and that `RequireVerifiedEmail` only checks `EmailVerifiedAt`.
- [mod-repos consumer context](../notes/mod-repos-consumer-context.md) — why the capability
  exists and what the consuming module owes in return.
- `plan/overview.md` — plan scope, constraints, and the out-of-scope list.

## Status

**Outcome:** succeeded. Date: 2026-08-04.

`docs/architecture.md` and `docs/mod-users-spec.md` were updated in place (no restructuring,
no new top-level sections) to cover all eight content items: the guest-account vs.
anonymous-actor terminology split with the proposal's comparison table (added to
`docs/architecture.md`'s Authentication flow and referenced from `docs/mod-users-spec.md`'s
use case 15 and Security requirements); the `system_actor`/`system_actors` subsystem
(type hierarchy, ownership guard rationale, `authzSlugs` omission, actor-group ineligibility,
boot-time slug resolution + no-grants assertion) in both docs' Data model sections; the
`resolveActorOrAnonymous` middleware behavior (missing-header fallthrough vs. 401 on bad
credentials, no sudo/`UserContext`, `IsAnonymousActor` accessor, opt-in wiring, no new
`scope:` vocabulary); the per-IP rate-limiting precondition and max-page-size note; the
`Vary: Authorization` caching note; the `requireAuth`/`requireVerifiedEmail` composition rule
and its fail-closed 500; and the audit-attribution collapse. The two stale `is_anonymous`
claims were corrected at all four cited locations (`docs/architecture.md:70` and `:91` at the
time of research, `docs/mod-users-spec.md:174`, `:231`, `:284`) plus the code comment at
`api/internal/handlers/auth/anonymous.go:46-48` (comment-only change, verified via
`git diff`). All validation grep checks pass; `make lint.api` (the only sub-project touched by
this task's one Go-file comment edit) and the full `api` unit-test suite pass — see
`flagged_for_manager` in this task's structured report for two pre-existing, unrelated
environment/flake notes surfaced while running `make lint`/`make test.unit` at the repo-root
target level.

Two additional documentation-accuracy items from the phase-01 boundary review were checked
against this task's target files and found not to apply within scope: (1) the Down-migration
rollback description — `docs/architecture.md` and `docs/mod-users-spec.md` do not describe
migration rollback behavior at all, so there was no in-scope claim to correct; the inaccurate
claim lives only in `plan/notes/anonymous-actor-architecture-proposal.md` (a plan artifact
outside this task's editable scope), flagged for the manager. (2) The no-grants-is-boot-time-only
distinction was written precisely into the new content from the start (ownership: continuous
DB trigger; no-grants: boot-time assertion only) rather than needing correction of an existing
claim, since none of this content existed in the docs before this task.

Affected files: `docs/architecture.md`, `docs/mod-users-spec.md`,
`api/internal/handlers/auth/anonymous.go` (comment-only), and this task document.
