# Update Architecture Docs

## Purpose and scope

Update mod-users' architecture and spec docs to reflect the `/self` manifest
wiring landed in Phase 1, and correct a stale claim in the plan's own working
notes.

**Revision note:** this task doc originally called for documenting a sanctioned
`expr:` per-route-middleware-split manifest pattern, using `/self` as the
worked example. That premise no longer holds. Phase 1 shipped in two steps:
task 001 wired `/self` using `register_args: [expr:requireVerifiedEmail]`; a
phase-1 architecture-conformance review then found this introduced a second,
inconsistent way of expressing per-route middleware differentiation (every
other manifest entry, including this file's own account-routes entry,
differentiates purely via each entry's own `middleware:` list). The user chose
to redesign rather than document the `expr:` approach as sanctioned; task 002
replaced it with two manifest entries (`RegisterSelfGetRoute` /
`RegisterSelfPutRoute`), each gated purely by its own `middleware:` list —
**no `expr:` register-arg is used for `/self` in the final, merged state.**
This task's scope is revised accordingly: document the `/self` behavioral
contract (unchanged goal) using the two-entries convention as just another
instance of the codebase's existing pattern, and correct a stale note left in
`plan/notes/`. Do **not** document `expr:` as a sanctioned per-route
middleware-split convention — that framing was rejected, and no example of it
exists in the merged code.

This is a documentation-only task. It runs after Phase 1 (both
`phase-01-self-route-wiring/001-wire-self-routes-manifest.md` and
`phase-01-self-route-wiring/002-split-self-routes-two-entries.md`) have landed,
so the docs describe the final manifest shape accurately. Follow the
`update-architecture-docs` task-procedure at
`plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.

role_doc: plugins/flow/references/roles/architect-backend.md

## Requirements

The Phase 1 implementation tasks that surfaced these architectural/spec implications:
- `phase-01-self-route-wiring/001-wire-self-routes-manifest.md` — initial `/self` wiring (later redesigned).
- `phase-01-self-route-wiring/002-split-self-routes-two-entries.md` — the redesign that landed: `RegisterSelfGetRoute`/`RegisterSelfPutRoute`, two manifest entries differentiated by `middleware:` lists, matching the sibling `RegisterAccountRoutes` convention exactly.

Review and update the following files (each named explicitly):

### `docs/architecture.md`
- In the API-layer / route-surface description (the "Self" row of the API-surface
  table, and the surrounding "API layer" section), document that
  `GET /v1/self` is reachable to accounts with an **unverified** email (it bypasses
  the email-verification gate so the GUI can render the "verify your email" page),
  while `PUT /v1/self` requires a **verified** email.
- Do **not** add a subsection about an `expr:` per-route middleware-split pattern.
  If a placeholder or draft mention of it already exists from an earlier session,
  remove it. The final implementation uses two ordinary manifest entries — the
  same convention already used by the account-routes entry — so no new pattern
  needs documenting; `/self` is simply another example of the existing
  one-entry-per-middleware-set convention, worth a one-line mention at most (e.g.
  "per-route middleware differentiation is expressed via separate manifest
  entries, as seen in the account-routes and self-routes entries") rather than a
  dedicated subsection.

### `docs/mod-users-spec.md`
- Update use case 7 ("View and update own profile") and/or the "Self
  (authenticated)" API section to state the verified-email contract:
  `GET /v1/self` is reachable to authenticated accounts even when the email is
  unverified; `PUT /v1/self` requires a verified email. Keep it a behavioral
  contract statement, not implementation detail.

### `plan/notes/mfgen-expr-middleware-pattern.md` (correction, not new content)
- This plan-notes file (written during initial planning, before the redesign)
  documents a "fragility" concern about `expr:requireVerifiedEmail` potentially
  dangling if no route's `middleware:` list still referenced the var. The
  phase-1 security review independently traced mfgen's reachability graph
  (`mfgen/internal/resolver/reachability.go`) and found this premise was
  **factually incorrect** — middleware nodes are unconditional reachability
  roots in mfgen, so the var is always emitted regardless of whether any
  route's `middleware:` list references it. Add a short correction note (do not
  rewrite the whole document) stating: (a) `/self` ultimately did not use the
  `expr:` pattern — it was redesigned to use two manifest entries per a
  phase-1 architecture-conformance review, and (b) the fragility premise above
  was shown incorrect by the phase-1 security review, for the benefit of any
  future reader relying on this note.
- Do **not** treat this as license to document `expr:` as a sanctioned
  pattern elsewhere (see `docs/architecture.md` instruction above) — this is a
  correction to a historical planning artifact, not new project documentation.

### `api/openapi.yaml` (verification only)
- Confirm `GET/PUT /v1/self` are already present in `api/openapi.yaml` (they are
  listed in the spec checklist). Middleware-gating nuance is not typically expressed
  in OpenAPI, so no OpenAPI change is expected. Only note a discrepancy if `/self`
  is missing there.

## Validation

- `docs/architecture.md` states the `GET`-unverified / `PUT`-verified `/self`
  contract. Confirm it does **not** describe an `expr:` per-route
  middleware-split pattern as a sanctioned convention.
- `docs/mod-users-spec.md` use case 7 / Self API section states the verified-email
  contract for `PUT` vs `GET`.
- `plan/notes/mfgen-expr-middleware-pattern.md` contains the correction note
  described above.
- `grep -rn "expr:requireVerifiedEmail\|expr: per-route\|sanctioned" docs/architecture.md AGENTS.md` returns
  nothing describing it as an adopted/sanctioned project convention.
- Cross-references between the docs (and to `moduleforge.module.yaml`) are
  consistent and resolve.
- `grep -rn "self" docs/architecture.md docs/mod-users-spec.md` shows the updated
  contract language.
- Markdown lint / doc-standards checks (if the repo runs any) pass.
- `git status` shows edits only under `mod-users/` (`docs/`, `plan/notes/`) —
  nothing under `mfgen/`, `app-mftodo/`, or sibling modules.

## References

- [mfgen expr middleware-split pattern notes](../notes/mfgen-expr-middleware-pattern.md)
  — the doc to correct (see Requirements above); do not delete or wholesale
  rewrite it, it remains a useful record of the `expr:` mechanism's real
  behavior even though `/self` doesn't use it.
- `moduleforge.module.yaml` — the landed two-entry `/self` shape (search for
  `RegisterSelfGetRoute`/`RegisterSelfPutRoute`) to reference when describing
  current behavior.
- `api/internal/handlers/account_routes.go` and its manifest entry — the
  sibling convention `/self` now matches exactly; useful for a one-line
  "consistent with existing convention" remark if desired.
- `plugins/flow/task-procedures/update-architecture-docs/SKILL.md` — the procedure
  to follow.

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-14
- **Validation summary:**
  - `docs/architecture.md` — updated the "Self" API-surface row to state the
    `GET`-unverified / `PUT`-verified contract, and added a one-line mention (no
    new subsection) that per-route middleware differentiation is expressed via
    separate manifest entries with their own `middleware:` lists, referencing
    the account-routes and self-routes entries. No `expr:` per-route
    middleware-split pattern is documented.
  - `docs/mod-users-spec.md` — updated use case 7 ("View and update own
    profile") and the "Self (authenticated)" API checklist section to state the
    same `GET`-unverified / `PUT`-verified contract.
  - `plan/notes/mfgen-expr-middleware-pattern.md` — appended a "Correction
    (post-implementation)" section (existing content left intact) stating (a)
    `/self` did not end up using the `expr:` pattern — it was redesigned to two
    manifest entries per the phase-1 architecture-conformance review, and (b)
    the documented fragility premise was independently shown incorrect by the
    phase-1 security review (`mfgen/internal/resolver/reachability.go`;
    middleware nodes are unconditional reachability roots).
  - `api/openapi.yaml` — verified `GET`/`PUT /v1/self` are already present
    (`grep -n "/v1/self" api/openapi.yaml` → line 764 plus a reference at line
    507); no OpenAPI change made, none needed.
  - `grep -rn "expr:requireVerifiedEmail\|expr: per-route\|sanctioned"
    docs/architecture.md AGENTS.md` — no matches; confirmed no sanctioned-`expr:`
    framing exists.
  - `grep -rn "self" docs/architecture.md docs/mod-users-spec.md` — confirmed
    the updated contract language appears in both files.
  - Cross-references checked: no links were added, moved, or removed in
    `docs/architecture.md` or `docs/mod-users-spec.md`; the `plan/notes/`
    correction references `mfgen/internal/resolver/reachability.go` (read-only,
    verification-only path, consistent with the phase-1 task docs' own
    references) and does not introduce a broken doc link.
  - Repo has no markdown-lint/doc-standards make target (`Makefile` only
    defines `lint.model`/`lint.api`/`lint.gui`), so none was run.
  - Scope guard (`git status`) — confirmed edits only under `docs/` and
    `plan/notes/` within `mod-users/`; nothing under `mfgen/`, `app-mftodo/`, or
    sibling modules.
- **Affected files:**
  - `docs/architecture.md`
  - `docs/mod-users-spec.md`
  - `plan/notes/mfgen-expr-middleware-pattern.md`
</content>
