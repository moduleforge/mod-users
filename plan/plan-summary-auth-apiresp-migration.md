# Session Summary — `auth-apiresp-migration`

## What was planned and why

`api/internal/handlers/auth/*.go` (`register.go`, `login.go`, `emailcode.go`, `anonymous.go`,
`oidc.go`, `reset.go`) are `mod-users`' highest-traffic *unauthenticated* HTTP endpoints (local
registration, login, anonymous account creation, email-code verification, OIDC provider
start/callback, password reset). They were never touched by the prior `users-apiresp-migration`
plan's Phase 2 (which migrated `apps.go`/`oidc_providers.go`/`oidc_config.go` and
`identities.go`/`self.go`/`assume.go`/`user_accounts.go` respectively) onto the reserved `apiresp`
error-code vocabulary defined in `docs/mf-standards/architecture/api-response-design.md`. That gap
was surfaced by the prior plan's own Phase 4 documentation task and recorded verbatim as follow-up
`biPE`. This plan's purpose was to close that gap: replace every literal ad-hoc top-level
`error.code` string (`bad_request`, `unauthorized`, `validation_error`, `email_taken`) in the six
`auth/*.go` production files with the reserved vocabulary (`invalid_input`, `unauthenticated`,
`invalid_input` + `details[]`, `conflict` + `details[]` respectively), update the matching
`_test.go` assertions, and remove the now-stale non-conformance disclosure from
`api/openapi.yaml`'s `Error` schema description.

Explicitly out of scope (per established precedent from the prior plan): centralizing onto
`apiresp.WriteError(w, r, err)` (tracked separately as follow-up `8iRl`); the 5
deliberately-deferred flat-envelope sites (follow-up `eiF8`); the pre-existing
`anonymous_account`/documentation mismatch (follow-up `nnfn`); and `apiresp`'s missing public
`Conflict(...)` constructor (follow-up `ZVum`, cross-repo in `mod-core`).

## What shipped

### Phase 1 — Auth Vocab Migration (`auth-vocab-migration`)

- **`001-migrate-local-auth-handlers`** (merge `b5a12d8afe583cf36e3375e07c452700fcecff06`) —
  Migrated all 18 mechanical `bad_request`/`unauthorized` -> `invalid_input`/`unauthenticated`
  code swaps, the 6 `validation_error` -> `invalid_input` + `details[]` sites, and the one
  `email_taken` -> `conflict` + `details[]` site across `register.go`, `login.go`,
  `emailcode.go`, `anonymous.go`, `reset.go`, per the task doc's mechanical mapping table and
  reference implementations. Left the six generic `unauthorized`->`unauthenticated`
  anti-enumeration sites without `details[]`, per the task's Requirement 4. Updated
  `guards_test.go` and `anonymous_test.go` assertions. All required validation passed except
  `make lint`'s model sub-target (unrelated pre-existing Docker/Postgres environment
  limitation); `lint.api` was clean. Manual correctness/security review found no diff-introduced
  issues; surfaced one pre-existing, task-doc-forbidden-to-touch message-text inconsistency in
  `emailcode.go`'s anti-enumeration paths.

- **`002-migrate-oidc-auth-handler`** (merge `81aeaed2984c792e106d1c60535159040fb40cf8`) —
  Swapped `bad_request` -> `invalid_input` at 4 call sites in `oidc.go` (Start/Callback).
  `not_found` and `internal_error` sites were left unchanged. No test-file edits were needed.
  Build/vet/test/grep checks passed. `make lint` hit the same unrelated environmental Docker
  timeout in `lint.model`; `lint.api` passed clean. Inline correctness/security review found no
  issues.

### Phase 2 — Documentation Updates (`doc-updates`)

- **`001-update-architecture-docs`** (merge `f67dc61df2a23c47f3f141fd468b75b26506aed5`) —
  Removed the stale non-conformance disclosure from `api/openapi.yaml`'s `Error` schema
  description (added by the prior plan's Phase 4, citing follow-up `biPE`) naming the six auth
  endpoint groups. Confirmed via git history that this description field was net-new in that
  prior commit and that no sibling schema carries a top-level description, so full removal (not
  a rewrite) restores convention. Reviewed `docs/architecture.md` and `docs/mod-users-spec.md` in
  full; the only related mentions concern the separate, already-tracked
  `anonymous_account`/follow-up `nnfn` question, correctly left untouched. `make openapi.validate`
  confirmed valid YAML; a full `make lint` was not viable in this dependencies-not-installed
  docs-only worktree and was not the applicable check for this change.

Both phase-boundary gate reviews (correctness/efficiency/baseline-security lenses, plus an
escalated full security lens for Phase 1 given the unauthenticated auth surface) returned zero
findings.

## Key decisions

- Reused established `users.<rule>` detail codes from `service/user_accounts.go` rather than
  inventing new ones for the `invalid_input`/`conflict` `details[]` sites.
- Deliberately did not add `details[]` to the generic `unauthorized`->`unauthenticated`
  anti-enumeration sites (login, email-code, reset) to avoid leaking which sub-case occurred —
  preserving the existing anti-enumeration posture of those endpoints.
- Kept the existing `server.Error`/`server.ErrorWithDetails` call form rather than centralizing
  onto `apiresp.WriteError`; this was explicitly out of scope for this plan and remains tracked
  as follow-up `8iRl`.
- Removed (rather than rewrote) the stale `openapi.yaml` `Error` schema description, since no
  sibling schema in that file carries a top-level description — full removal restores convention
  rather than leaving a partially-accurate rewrite.

## Follow-up items

Added during this session (tag `auth-vocab-migration`):

- `DQU4` — `make lint`'s sequential run fails at `lint.model` due to an ephemeral-Postgres/Docker
  networking timeout; environmental and pre-existing, unrelated to this plan's diff (`model/` was
  untouched). `lint.api` was run directly as a substitute and passed clean in both Phase 1 tasks.
- `1PV8` and `vJEy` — Tooling gap: neither Phase 1 task agent had a Task tool available to
  dispatch `review-changes-correctness`/`review-changes-security` as independent sub-agents;
  both lenses' checklists were applied manually inline against the diffs instead (no findings
  surfaced). The manager may want to confirm this substitution is acceptable for the plan's audit
  trail, or re-run a formal `review-changes-*` dispatch against commits `46db010`/`48fb16e` if a
  structured review report is required.
- `KeV1` — Pre-existing (not introduced by this plan) anti-enumeration message-text divergence in
  `emailcode.go`: `EmailCodeVerify`'s user-not-found branch returns `"invalid code"` while its
  code-not-active/code-mismatch branches return `"invalid or expired code"`. Status and code are
  now identical (401 `unauthenticated`) across all three, but the differing message text remains
  a potential enumeration channel. The task doc's Requirement 1 explicitly forbade changing
  message text at these sites, so this was left as-is; worth a follow-up if not already tracked
  elsewhere.

Also: follow-up `biPE` — the original scope gap this plan was created to close — was resolved and
removed from `plan/followups.yaml` during this session's Phase 2 doc-updates task, now that the
six `auth/*.go` endpoint groups conform to the reserved-code vocabulary and the `openapi.yaml`
disclosure citing it has been removed.

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — Auth Vocab Migration

- [x] [001-migrate-local-auth-handlers.md](./phase-01-auth-vocab-migration/001-migrate-local-auth-handlers.md) — tier `sonnet-high` · branch `phase-01-task-01-migrate-local-auth-handlers-ap` · commit `46db010` · merge `b5a12d8afe583cf36e3375e07c452700fcecff06`
- [x] [002-migrate-oidc-auth-handler.md](./phase-01-auth-vocab-migration/002-migrate-oidc-auth-handler.md) — tier `sonnet-high` · branch `phase-01-task-02-migrate-oidc-auth-handler-apir` · commit `48fb16e` · merge `81aeaed2984c792e106d1c60535159040fb40cf8`

### Phase 02 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-02-doc-updates/001-update-architecture-docs.md) — tier `sonnet-high` · branch `phase-02-task-01-update-architecture-docs-bipe` · commit `6264495` · merge `f67dc61df2a23c47f3f141fd468b75b26506aed5`
