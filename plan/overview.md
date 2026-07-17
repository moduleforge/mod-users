# Overview

## Purpose and scope

This plan migrates `api/internal/handlers/auth/*.go` (`register.go`, `login.go`, `emailcode.go`,
`anonymous.go`, `oidc.go`, `reset.go`) onto the reserved `apiresp` error-code vocabulary defined in
[`docs/mf-standards/architecture/api-response-design.md`](../docs/mf-standards/architecture/api-response-design.md),
closing the scope gap recorded as follow-up `biPE`. These six files are `mod-users`' highest-traffic
*unauthenticated* HTTP endpoints (local registration, login, anonymous account creation, email-code
verification, OIDC provider start/callback, password reset) and were never touched by the prior
`users-apiresp-migration` plan's Phase 2 (`apps.go`/`oidc_providers.go`/`oidc_config.go` and
`identities.go`/`self.go`/`assume.go`/`user_accounts.go` respectively) — a gap surfaced by that plan's
own Phase 4 documentation task and recorded verbatim in `plan/followups.yaml` (`biPE`).

**In scope:**

- Replacing every literal ad-hoc top-level `error.code` string (`bad_request`, `unauthorized`,
  `validation_error`, `email_taken`) in the six `auth/*.go` production files with the reserved
  vocabulary (`invalid_input`, `unauthenticated`, `invalid_input` + `details[]`, `conflict` +
  `details[]` respectively), per the mechanical mapping table in the design doc's
  "Module-specific extension codes" section.
- Updating the matching `_test.go` files' `wantCode`/`errObj["code"]` assertions so they check the
  new codes, not just HTTP status.
- Updating `api/openapi.yaml`'s `Error` schema description to remove the now-stale disclosure (added
  by the prior plan's Phase 4, citing follow-up `biPE`) that these six endpoint groups don't conform
  to the reserved-code enum — they will, once this plan lands.

**Explicitly out of scope** (per the user's request and established precedent from the prior
`users-apiresp-migration` plan — do not deviate without flagging back to the manager):

- **Centralizing onto `apiresp.WriteError(w, r, err)`.** Every site keeps the existing
  `server.Error(w, status, code, message)` / `server.ErrorWithDetails(w, status, code, message,
  details)` call form. `server.Error`/`server.ErrorWithDetails` are themselves already thin wrappers
  over `apiresp`'s own envelope construction (`apiresp.WriteJSON`/`Envelope`/`ErrorBody`), so the wire
  shape is correct either way — only the vocabulary changes here, not the call form. Collapsing onto
  sentinel-driven `apiresp.WriteError` calls is tracked separately as follow-up `8iRl` (which also
  does not cover these six files, since they were never migrated at all).
- **The 5 deliberately-deferred flat-envelope sites** (follow-up `eiF8`):
  `api/internal/auth/require_verified.go:24,34`, `require_confirmed.go:36`, and
  `api/internal/handlers/identities.go`'s `writeStepUpRequired`/`writeLastIdentityError`. These use a
  flat `{"error": "<code>", ...}` shape that cross-repo consumers (`app-mfdemo`/`app-mftodo`) key off
  directly; migrating them is a breaking, coordinated cross-repo change, not touched here.
- **The `anonymous_account` / `400 anonymous_account` documentation mismatch** (follow-up `nnfn`):
  `docs/architecture.md` and `docs/mod-users-spec.md` describe a `400 anonymous_account` guard in
  `login`/`email-code`/`password-reset` that does not appear anywhere in the current
  `api/internal` Go source. Confirmed again while reading these six files for this plan: no
  `IsAnonymous`-style guard exists in `login.go`, `emailcode.go`, or `reset.go`. This is a
  pre-existing, unrelated doc-accuracy question (is the doc stale, or is the guard simply unwritten?)
  that a docs-only pass should resolve separately — not fixed here.
- **`apiresp`'s missing public `Conflict(...)` constructor** (follow-up `ZVum`, cross-repo in
  `mod-core`) — the one `email_taken` site in this scope (`register.go`) uses the same
  `server.ErrorWithDetails` manual-envelope pattern `writeServiceError` in
  `api/internal/handlers/user_accounts.go` already established for exactly this reason.

## Current status

No prior work has been done on this slice. `api/internal/handlers/auth/*.go` currently emits the
pre-migration ad-hoc codes across all six files (confirmed via grep: 27 literal `server.Error(...,
"bad_request"|"unauthorized"|"validation_error", ...)` and one `"email_taken"` call sites across the
six production files, within the plan's originally-estimated 24-33 range). The plan is single-phase:
one mechanical migration phase (two parallel-eligible tasks split along the module's two independent
handler groupings), followed by an automatically-appended documentation-update phase gated on both
migration tasks landing (this repo's API contract disclosure changes, which trips the
architectural-implications checklist).

`docs/mf-standards` is a git submodule; it was uninitialized in this planning worktree at session
start (mirroring the environment note recorded as follow-up `jg7u` from the prior plan) and was
initialized read-only (pointer unchanged) to read the design doc. Task worktrees dispatched from this
plan will likely need to do the same to read `docs/mf-standards/architecture/api-response-design.md`
and `docs/mf-standards/building-common.md`.

`dependencies_installed` was reported as "not installed" for this planning worktree; task worktrees
dispatched from this plan will need their own `go.work` worktree fix (see follow-up `9EBV` and each
task doc's References section) before `make build.api`/`make test.unit` will succeed.

## Overview

### Phase 01 — Auth Vocab Migration

Two parallel-eligible tasks, split along the module's two independent handler groupings confirmed by
static analysis (no shared production file, no shared `_test.go` file, no shared exported struct
between the two groups):

- **`001-migrate-local-auth-handlers.md`** — `register.go`, `login.go`, `emailcode.go`,
  `anonymous.go`, `reset.go` (all methods on the shared `*Handler` struct defined in `login.go`) plus
  their two `_test.go` files (`guards_test.go`, `anonymous_test.go`). 21 literal ad-hoc-code call
  sites across the five production files.
- **`002-migrate-oidc-auth-handler.md`** — `oidc.go` (methods on the separate `*OIDCHandler` struct)
  plus its two `_test.go` files (`oidc_test.go`, `oidc_linkmode_test.go`). 4 literal ad-hoc-code call
  sites (`bad_request` only — `oidc.go` has no `validation_error`/`email_taken` sites; its existing
  `not_found` sites are already conformant and untouched).

These two tasks can be dispatched concurrently — confirmed by grep that neither production file nor
test file is shared between them, and that no `_test.go` file in the package spans both handler
groupings.

### Phase 02 — Doc Updates (auto-appended)

- **`001-update-architecture-docs.md`** — Removes/updates `api/openapi.yaml`'s `Error` schema
  description's stale non-conformance disclosure (the paragraph the prior plan's Phase 4 added citing
  follow-up `biPE`) now that the six `auth/*.go` endpoint groups conform, and confirms (or updates)
  whether `docs/architecture.md`/`docs/mod-users-spec.md` need any matching edit. Depends on both
  Phase 01 tasks landing, since the disclosure names all six files across both tasks' scope.
