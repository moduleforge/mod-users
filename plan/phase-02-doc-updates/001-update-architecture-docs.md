# Update Architecture Docs

## Purpose and scope

Update the architecture and specification documents to reflect the
identity/credential self-service surface now wired into
`moduleforge.module.yaml` by Phase 1. Both docs currently omit this surface
entirely: `docs/architecture.md`'s "API layer" table has no Identities /
Credentials row, and `docs/mod-users-spec.md` has neither a use-case nor an
API-definition entry for it. This mirrors what the self-route-manifest plan's
documentation phase did for `/self`.

Run the `update-architecture-docs` task-procedure at
`plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.

## Requirements

### Implementation tasks that surfaced these architectural implications

The following Phase 1 task documents introduce the changes this doc update must
capture (both completed by the time this task runs):

- `plan/phase-01-route-wiring/001-registrars-and-stepup-cache.md` — internal
  read/mutating route registrars and the public step-up consumed-cache
  constructor (`auth.NewStepUpConsumedCache`, which starts a background janitor).
- `plan/phase-01-route-wiring/002-wire-identities-manifest.md` — the
  `moduleforge.module.yaml` service + route entries, public-facade re-exports,
  and the `api/cmd/server/main.go` reconciliation.

### Files to review and update

- **`docs/architecture.md`** — add an **Identities / Credentials** row to the
  "API layer" table (the surface by tag group, currently ending with Health /
  Auth / Self / Users / Apps / Audit). The row must list the seven endpoints
  (`GET /v1/self/identities`; `POST /v1/self/identities/oidc/{provider}/start`;
  `DELETE /v1/self/identities/{identity_uuid}`;
  `POST`/`DELETE /v1/self/credential/password`;
  `POST /v1/self/credential/step-up`; `POST /v1/self/credential/step-up/verify`)
  and state the split: List is reachable to accounts with an unverified email
  (`requireOIDCConfirmed + requireAuth`); the six mutating endpoints additionally
  require a verified email. Note that credential-mutating endpoints are
  optionally gated by a step-up challenge when
  `AUTH_REQUIRE_STEP_UP` (`cfg.Auth.RequireStepUpForCredentialChange`) is
  enabled, and that the step-up consumed-JTI cache + its pruning janitor are
  wired via the `stepUpConsumed` `provides.services` entry (a constructor whose
  side effect starts the janitor), not an app-level startup hook. If
  `docs/architecture.md` documents the services/DI wiring or has a component for
  the auth/dependency graph, reflect the new `identitiesHandler` and
  `stepUpConsumed` services and the two-entry per-`middleware:` route split
  (consistent with the note already present about the self-routes two-entry
  split).
- **`docs/mod-users-spec.md`** — add:
  - a **Key use case** for identity/credential self-management (list identities,
    link/unlink an OIDC identity, set/remove a local password, and the step-up
    challenge for credential changes), placed near the existing "View and update
    own profile" (use case 7) and OIDC-linking (use case 6) cases;
  - an **API definition** subsection (mirroring the existing "Self
    (authenticated)" checklist block) listing the seven endpoints with their
    auth/verified-email requirements and the step-up behavior;
  - if a Security requirements bullet is warranted, note the step-up
    single-use-token / anti-replay and anti-enumeration-timing behavior already
    implemented in `api/internal/handlers/identities.go` and
    `api/internal/auth/stepup.go`.

Keep both documents consistent with the shipped design: the two-entry,
per-`middleware:` convention (no `expr:`), and the manifest-service (not
`startupHooks:`) wiring of the consumed cache.

### role_doc

role_doc: plugins/flow/references/roles/architect-backend.md

(The implications are backend / API-surface / component-wiring changes — the
default architect variant applies.)

## Validation

- `docs/architecture.md` contains a new Identities / Credentials row (or section)
  covering all seven endpoints and the unverified-List / verified-mutating split;
  `grep -n "self/identities\|self/credential\|step-up" docs/architecture.md`
  returns the new content.
- `docs/mod-users-spec.md` contains a new use case and an API-definition block
  for the surface; `grep -n "self/identities\|self/credential\|step-up\|step_up" docs/mod-users-spec.md`
  returns the new content.
- Both docs describe the step-up cache as a `provides.services` manifest entry
  (not `startupHooks:`) and the routes as two per-`middleware:` entries (not an
  `expr:` single route), matching the shipped manifest.
- No contradiction remains with `moduleforge.module.yaml` or
  `api/internal/handlers/identities.go` (endpoint paths, verbs, and gating match).
- Markdown lints/renders cleanly; the API-layer table stays well-formed.
- Cross-check that `api/openapi.yaml` already describes these endpoints; if it
  does not, note the gap for the manager rather than expanding scope here (the
  self-route plan verified `/self` was already present in openapi.yaml — do the
  analogous check for this surface).

## References

- `docs/architecture.md:48-63` — the "API layer" table and the existing
  self-routes two-entry-split note to extend.
- `docs/mod-users-spec.md:84-90,204-254` — the "View and update own profile" use
  case and the "Self (authenticated)" / API-definition checklist blocks to mirror.
- `moduleforge.module.yaml` — the shipped service/route entries to document.
- `api/internal/handlers/identities.go`, `api/internal/auth/stepup.go` — the
  authoritative endpoint behavior, step-up token single-use/anti-replay, and
  anti-enumeration timing.
- `plan/plan-summary-self-route-manifest.md` — the precedent doc phase for
  `/self` this task mirrors.
- `plugins/flow/task-procedures/update-architecture-docs/SKILL.md` — the
  task-procedure to run.

## Status

- **Outcome:** succeeded (2026-07-19).
- **Implementation worktree:** `/Users/zane/playground/moduleforge/mod-users/worktrees/2026-07-20-wire-auth/worktrees/phase-02-task-01-update-architecture-docs`, branch `phase-02-task-01-update-architecture-docs`.
- **Files modified (repo-relative, inside the implementation worktree):**
  - `docs/architecture.md` — added an **Identities / Credentials** row to the
    "API layer" table (all seven endpoints; List-unverified /
    six-mutating-verified split; step-up gating noted for the four endpoints
    that actually mutate a credential — link start, unlink, set/remove
    password). Extended the paragraph following the table to mention the new
    identities-routes two-entry split alongside the existing self-routes split,
    and to describe the `stepUpConsumed` `provides.services` entry (constructor
    starts the janitor as a side effect) as the wiring model — explicitly not
    an app-level `startupHooks:` mechanism. No new top-level section was added;
    there is no existing "services/DI wiring" section in this doc to extend
    (the "Runtime service dependencies" section documents `requires.services`
    only, not the module's own `provides.services` entries), so the note was
    folded into the existing API-layer paragraph instead.
  - `docs/mod-users-spec.md` — inserted a new **Key use case 8** ("Manage
    identity/credential self-service") after use case 7 (View/update profile),
    renumbering use cases 8–14 to 9–15 (no other document links to specific
    use-case numbers or anchors; verified by grep before renumbering). Added an
    **Identities & credentials (authenticated)** API-definition checklist block
    after "Self (authenticated)", listing all seven endpoints with their
    verified-email/step-up requirements. Added a **Security requirements**
    bullet ("Step-up challenge for credential changes") describing the
    single-use/anti-replay JTI-cache mechanism and the step-up-request
    endpoint's constant-time anti-enumeration response.
- **Validation summary:**
  - `grep -n "self/identities\|self/credential\|step-up" docs/architecture.md`
    returns the new API-layer row and paragraph — passed.
  - `grep -n "self/identities\|self/credential\|step-up\|step_up" docs/mod-users-spec.md`
    returns the new use case, API-definition block, and security bullet —
    passed.
  - Both docs describe the step-up cache as a `provides.services` entry (not
    `startupHooks:`) and the routes as two per-`middleware:` entries; no
    `expr:` mention was introduced — confirmed via grep (zero `expr:` hits in
    either file).
  - Cross-checked endpoint paths, verbs, and middleware gating against
    `moduleforge.module.yaml` (lines ~185-329) and `api/cmd/server/main.go`
    (lines 516-560): all seven paths/verbs/handler-method mappings and the
    List-unverified / six-mutating-verified split match exactly.
  - Cross-checked step-up single-use/anti-replay and anti-enumeration-timing
    claims against `api/internal/auth/stepup.go` (`IssueStepUpToken`,
    `VerifyStepUpToken`, `StartStepUpJanitor`) and
    `api/internal/handlers/identities.go` (`StepUpRequest`'s 200ms
    constant-time response, `requireStepUp`'s four call sites in `StartLink`,
    `Unlink`, `SetPassword`, `RemovePassword` — confirmed the two step-up
    endpoints themselves are not step-up-gated).
  - Markdown structural sanity checked manually (table pipe-count parity
    across all "API layer" table rows; heading-level and use-case numbering
    checked with grep after renumbering; no markdown lint target exists in
    this repo's `Makefile` to run automatically).
  - `api/openapi.yaml` cross-check: confirmed the identity/credential surface
    is **absent** from `api/openapi.yaml` (`grep -in "identit\|credential\|step.up"`
    returns only unrelated hits — the anonymous-account "identity continuity"
    prose and the admin assume-endpoint's "identity" token description). Per
    this task's own Validation instruction, this gap is noted for the manager
    rather than fixed here (out of this task's scope).
- **Assumptions applied:** none beyond the task doc's own instructions — no
  `## Assumptions` section was present on this task doc.
- **Flagged for manager:** `api/openapi.yaml` does not document the
  identity/credential surface (`GET /v1/self/identities`,
  `POST /v1/self/identities/oidc/{provider}/start`,
  `DELETE /v1/self/identities/{identity_uuid}`,
  `POST`/`DELETE /v1/self/credential/password`,
  `POST /v1/self/credential/step-up`,
  `POST /v1/self/credential/step-up/verify`) at all — unlike the `/self`
  precedent, where the self-route-manifest plan's doc phase confirmed
  `GET`/`PUT /v1/self` were already present. This is a genuine spec/reality gap
  outside this task's scope (docs/architecture.md and docs/mod-users-spec.md
  only); recommend a follow-up task to add these seven paths to
  `api/openapi.yaml`.
