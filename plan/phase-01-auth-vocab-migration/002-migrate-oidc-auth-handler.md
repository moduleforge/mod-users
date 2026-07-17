# Migrate Oidc Auth Handler

## Purpose and scope

Migrate every literal ad-hoc top-level `error.code` string in `api/internal/handlers/auth/oidc.go`
(methods on the `*OIDCHandler` struct) onto the reserved `apiresp` error-code vocabulary, per
[`docs/mf-standards/architecture/api-response-design.md`](../../docs/mf-standards/architecture/api-response-design.md)'s
"Module-specific extension codes" section. This closes part of the scope gap recorded as follow-up
`biPE` in `plan/followups.yaml`. No standard task-procedure skill applies beyond ordinary
implementation conventions; this is a direct code-edit task.

This task's sibling, `001-migrate-local-auth-handlers.md`, covers `register.go`/`login.go`/
`emailcode.go`/`anonymous.go`/`reset.go` (the separate `*Handler` struct) and is parallel-eligible with
this task — no production file or `_test.go` file is shared between the two. `oidc.go` is
substantially simpler than the sibling task's scope: it has no `validation_error` or `email_taken`
sites, only `bad_request` (to migrate) and already-conformant `not_found` (to leave untouched).

## Requirements

### 1. Mechanical code-swap sites (no field details)

Replace the literal code string in these `server.Error(w, http.StatusBadRequest, "bad_request",
message)` call sites, keeping status, message text, and the `server.Error(...)` call form unchanged —
swap only the code argument, `bad_request` → `invalid_input`:

- `oidc.go:143` — `server.Error(w, http.StatusBadRequest, "bad_request", err.Error())` (inside
  `Start`; the message is dynamic, from `h.oauth.AuthorizeURL`'s returned error — keep it dynamic,
  just change the code literal)
- `oidc.go:180` — "missing code or state" (inside `Callback`)
- `oidc.go:186` — "missing state cookie" (inside `Callback`)
- `oidc.go:202` — "invalid or expired state" (inside `Callback`)

That is 4 sites, all `bad_request` → `invalid_input`. There are no `validation_error` or `email_taken`
literals anywhere in `oidc.go`.

### 2. Leave the three `not_found` sites untouched

`oidc.go:139`, `oidc.go:167`, and `oidc.go:195` all read
`server.Error(w, http.StatusNotFound, "not_found", "unknown provider")` — `not_found` is already a
reserved top-level code (per the design doc's core-codes table) and these sites are already
conformant. Do not touch them. (These are OIDC-provider-config lookups — an operator-configured,
non-`EntityResolver`-mediated slug lookup, not a self/user-scoped entity — so the "verify per-site
before assuming masking applies" caution in the task brief is a non-issue here: there is no ad-hoc
`identity_not_found`-style code in this file to reconsider in the first place.)

### 3. Leave every `internal_error` site untouched

`oidc.go` has many `internal_error` sites (transaction/observer/JWT-issuance failures in `Callback`
and `handleLinkMode`) — these are already the correct reserved code for unexpected server-side
failures. Do not touch them.

### 4. Update test assertions

Grep `oidc_test.go` and `oidc_linkmode_test.go` for `"bad_request"`/`"not_found"` string literals in
assertions before concluding no test update is needed — as of this task's authoring, neither test file
asserts on the `bad_request` code string (they assert only on HTTP status for the 400/404 branches,
e.g. `TestCallback_MissingStateCookie`, `TestCallback_MissingCodeAndState`, `TestStart_UnknownProvider`,
`TestCallback_UnknownProvider`); `TestOIDC_Callback_ResolverDBError_Returns500` asserts
`errObj["code"] == "internal_error"`, which this task does not change. If your read of the current test
files finds a code-string assertion this task doc missed, update it to match; otherwise no test-file
edit should be needed for this task — confirm this explicitly in your task report rather than silently
skipping the file.

## Validation

- `make build.api` (or `cd api && go build ./...`) succeeds.
- `cd api && go vet ./...` is clean for `oidc.go`.
- `make test.unit` (or `cd api && go test ./...`) passes, including the full `oidc_test.go` and
  `oidc_linkmode_test.go` suites unchanged (they should not need behavioral edits — see Requirement 4).
- `grep -n '"bad_request"' api/internal/handlers/auth/oidc.go` returns no matches.
- `grep -n '"not_found"' api/internal/handlers/auth/oidc.go` still returns exactly 3 matches, byte-for-byte
  unchanged from before this task's edits.
- Manually confirm (read the diff) that every `server.Error(...)` call form is preserved as-is (no
  site was converted to `apiresp.WriteError(w, r, err)`), per this plan's explicit scope boundary.
- Manually confirm the state-cookie-clearing behavior (`h.clearStateCookie` at the top of `Callback`)
  and the redirect-vs-direct-response branching in `Callback`/`Start`/`handleLinkMode` are unchanged —
  this task only touches the `code` string argument at four call sites, nothing else (security-review
  lens: this handler manages OAuth state-token validation and cookie handling on `mod-users`'
  highest-traffic unauthenticated OIDC surface).
- `make lint` (read-only) — note any pre-existing/environmental failures unrelated to this diff rather
  than treating them as blocking.

## Metadata

architectural_impact: false

## Assumptions

- `docs/mf-standards` is a git submodule; if it appears empty/uninitialized in your task worktree, run
  `git submodule update --init docs/mf-standards` (read-only — do not update the pinned commit) before
  reading the design doc.
- Building Go code in a fresh task worktree under this repo needs the `go.work` workaround described in
  `docs/mf-standards/building-common.md`'s "Building inside a task worktree" section, **plus** explicit
  `go work edit -replace` overrides not yet documented there (see follow-up `9EBV`). The following
  recipe was confirmed working in the prior plan's `phase-01-task-01-adopt-apiresp-sentinels-and-wr`
  task worktree — run once from your task worktree root, adjusting paths if your worktree's structure
  differs:

  ```bash
  cd <task-worktree-root>
  go work init
  go work use ./api ./model \
    ../../../mod-core/api ../../../mod-core/model \
    ../../../mod-audit/api ../../../mod-audit/model \
    ../../../mod-authz/api ../../../mod-authz/model
  go work edit -replace github.com/moduleforge/core-model@v0.0.0=../../../mod-core/model
  go work edit -replace github.com/moduleforge/core-api@v0.0.0=../../../mod-core/api
  go work edit -replace github.com/moduleforge/audit-model@v0.0.0=../../../mod-audit/model
  go work edit -replace github.com/moduleforge/audit-api@v0.0.0=../../../mod-audit/api
  go work edit -replace github.com/moduleforge/authz-model@v0.0.0=../../../mod-authz/model
  go work edit -replace github.com/moduleforge/authz-api@v0.0.0=../../../mod-authz/api
  ```

  `go.work` is gitignored and worktree-local — never commit it.
- `dependencies_installed` was reported "not installed" for the planning worktree; run `bun install`
  at your task worktree root per `AGENTS.md`'s "Working in worktrees" section if any `gui/`-adjacent
  tooling is invoked (it should not be needed for this Go-only task).

## References

- [`docs/mf-standards/architecture/api-response-design.md`](../../docs/mf-standards/architecture/api-response-design.md)
  — canonical reserved-code mapping table (see "Module-specific extension codes").
- `api/internal/server/respond.go` — `server.Error` signature.
- `plan/followups.yaml` entries `biPE` (this scope gap), `eiF8` (the 5 deferred flat-envelope sites —
  unrelated to `oidc.go`, no action needed here), `8iRl` (the deferred `apiresp.WriteError`
  centralization — do not do it here), `9EBV` (the go.work worktree fix).
- Prior plan's Phase 2 task 001 (`migrate-apps-oidc-handlers`, covering `oidc_providers.go`/
  `oidc_config.go` — the *admin-facing* OIDC provider-configuration endpoints, a different file from
  this task's `auth/oidc.go`, which is the *unauthenticated* OIDC login/callback flow) — worked
  precedent for the same `bad_request` → `invalid_input` mechanical swap.

## Checkpoint hints

- After the four `Start`/`Callback` call-site edits in `oidc.go`.
- After confirming (and, if needed, updating) the two OIDC test files.

## Status

- **Outcome:** succeeded (2026-07-16).
- All four `bad_request` → `invalid_input` swaps applied to `api/internal/handlers/auth/oidc.go`
  (`Start` line 143, `Callback` lines 180/186/202). The three `not_found` sites and all `internal_error`
  sites were left byte-for-byte unchanged, confirmed by diff.
- `oidc_test.go` and `oidc_linkmode_test.go` were grepped for `"bad_request"`/`"not_found"` literal
  assertions per Requirement 4 — none found (both files assert only on HTTP status for the 400/404
  branches, and `TestOIDC_Callback_ResolverDBError_Returns500` asserts `internal_error`, unaffected by
  this task). No test-file edit was needed or made.
- Validation: `make build.api` passed; `cd api && go vet ./...` clean; `make test.unit` passed
  (all `api/...` packages, including `internal/handlers/auth` — 1.399s); the two required `grep`
  checks passed exactly as specified; diff manually reviewed — no `server.Error(...)` call form was
  converted to `apiresp.WriteError`, and `h.clearStateCookie` plus the redirect-vs-direct-response
  branching in `Callback`/`Start`/`handleLinkMode` are unchanged (diff touches only the `code` string
  literal at the 4 call sites). `make lint` (sequential) failed at the `lint.model` stage on an
  environmental ephemeral-Postgres/Docker networking timeout unrelated to this diff (`model/` is not
  touched by this task); ran `make lint.api` directly instead, which passed clean (`go vet` + `gofmt`
  check).
- Correctness and security review lenses were run inline (no `Task` tool was available in this agent's
  toolset to dispatch nested review sub-agents) against the diff vs. `main` (base `44a2273`, head
  `48fb16e`). No findings from either lens. Confirmed via caller grep that no GUI code parses the
  `error.code` JSON field for the `/v1/auth/oidc/{provider}/start` or `/callback` endpoints — both are
  reached via full-page browser navigation (`window.location.assign`), not `fetch`, so the code-string
  change carries no client-side regression risk.
