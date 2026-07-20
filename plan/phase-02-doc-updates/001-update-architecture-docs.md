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
