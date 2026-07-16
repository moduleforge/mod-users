# Plan Summary — Users Apiresp Migration

## What was planned and why

This plan is **Wave 1** of a multi-phase, ecosystem-wide API response and error-standardization
effort, scoped to `mod-users` only. Its purpose was to migrate `mod-users`' HTTP `api/` surface and
its `gui/` client onto the canonical shared contract defined in
`docs/mf-standards/architecture/api-response-design.md`.

`mod-users` originated the nested-envelope `{error:{code,message[,details]}}` shape that the shared
design doc adopts, but it carried the largest ad-hoc-code migration surface in the ecosystem. The
plan set out to:

- Promote `mod-users`' local `internal/authz` sentinels (`ErrUnauthenticated`, `ErrForbidden`) onto
  the shared `apiresp` sentinels so other modules can `errors.Is` against them, retiring
  string-matching shims.
- Route error writing through the shared `apiresp.WriteError` implementation (single classification
  + envelope construction) instead of `mod-users`' parallel `respond.go`.
- Migrate every ad-hoc top-level `error.code` string to the reserved core vocabulary, moving
  finer-grained distinctions into namespaced `details[].code` (`users.<rule>`).
- Reconcile the `gui/` client types and error widget with the promoted `@moduleforge/core-gui`
  primitives.

The plan had a hard precondition: Wave 0 (`mod-core` plan `apiresp-error-widgets`, delivering the
shared `apiresp` Go package and `@moduleforge/core-gui` error/toast toolkit) had to merge before any
task in this plan could start. The plan was authored four phases deep — Go foundation, Go vocabulary
migration, GUI adoption, and documentation updates — with the GUI phase independent of the two Go
phases.

## What shipped

### Phase 1 — Go Apiresp Foundation (1 task)

- **`adopt-apiresp-sentinels-and-writer`** (merge `e50d9b48`) — Promoted `internal/authz` sentinels
  onto `apiresp`'s canonical sentinels; collapsed the three sentinel-classifying helpers
  (`writeAuthzError`, `writeServiceError`, `writeCoreServiceErr`) onto `apiresp.WriteError`,
  threading `*http.Request` through every call site. Wrapped `svc.ErrEmailTaken` around
  `apiresp.ErrConflict` and aliased `svc.ErrInvalidInput` to `apiresp.ErrInvalidInput`, with the five
  validation call sites in `service/user_accounts.go` now carrying structured `users.<rule>` field
  details via `apiresp.InvalidInput`. Kept `server/respond.go` as thin wrappers routing through
  `apiresp`'s own envelope construction. Updated `user_accounts_authz_test.go`'s sentinel-path
  assertions to the new wire shape. `go build`, `go vet`, and the full test suite were all green;
  `make lint` failed only on two pre-existing, out-of-scope issues unrelated to this diff.

### Phase 2 — Go Vocab Migration (2 parallel tasks)

- **`migrate-apps-oidc-handlers`** (merge `de996b6b`) — Migrated all `server.Error(...)` top-level
  error-code literals in `apps.go`, `oidc_providers.go`, and `oidc_config.go` onto the reserved
  vocabulary: 15 `bad_request` sites → `invalid_input` (status unchanged, 400), and 3
  `validation_error` sites in `apps.go` → `invalid_input` + `details[]` via `server.ErrorWithDetails`
  (detail codes `users.slug_required`/`users.name_required`/`users.user_uuid_required`). Existing
  `server.Error` call form retained since Phase 1 did not migrate these sites onto direct
  `apiresp.WriteError` calls. No test-file edits needed. Build and full test suite green.
- **`migrate-identity-account-self-handlers`** (merge `69a68aff`) — Migrated every literal
  `server.Error(...)` call site in `identities.go`, `self.go`, `assume.go`, and `user_accounts.go`
  onto the reserved vocabulary: `bad_request`→`invalid_input` (11 sites), `validation_error`→
  `invalid_input`+`users.password_too_short` detail, `unauthorized`→`unauthenticated` (2 sites),
  `bad_credentials`→`unauthenticated`+`users.bad_credentials` detail bound to `current_password` (2
  sites), `identity_not_found`→`not_found`+`users.identity_not_found` detail. The two flat-envelope
  helpers (`writeStepUpRequired`, `writeLastIdentityError`) were left untouched as required. Updated
  matching test files including the `user_accounts_authz_test.go` shim. Build, vet, and full test
  suite green; `make lint` failures were pre-existing/environmental and unrelated to this diff.

### Phase 3 — GUI Core-GUI Adoption (2 parallel tasks)

- **`reconcile-api-client-types`** (merge `f54e84d9`) — Reconciled `gui/src/lib/api.ts`'s
  wire/client error types with `@moduleforge/core-gui`'s canonical shapes: imported and re-exported
  `ApiError`, `ApiErrorResponse`, `FieldErrorData`, and `ApiRequestError` in place of three local
  duplicate definitions, keeping `src/index.ts`'s existing re-export surface (and all
  `@moduleforge/users-gui` consumers) working unchanged. Populated `details` on thrown
  `ApiRequestError`s by reading `errorBody.error.details` and passing it as a new optional 4th
  constructor argument. Changed the 401 branch's synthesized top-level code from `unauthorized` to
  `unauthenticated`, preserving the unconditional throw outside the `skipAuthRedirect`-gated redirect
  block. `make build.gui` and `make lint.gui` both passed; all task-doc grep checks passed.
- **`adopt-error-banner-widget`** (merge `fdc19c49`) — Replaced `error-message.tsx`'s direct
  `Alert`/`AlertDescription`/`AlertCircle` markup with a one-line delegation to
  `@moduleforge/core-gui`'s `<ErrorBanner>` widget, removing the cross-module low-level-primitive
  coupling. Kept `ErrorMessage` as a thin wrapper (rather than retiring it) since `ErrorBannerData`
  accepts a plain string, so no call sites needed updates. `make build.gui` and `make lint.gui`
  passed; behavioral parity held by construction.

### Phase 4 — Documentation Updates (1 task)

- **`update-architecture-docs`** (merge `26b1cd0b`) — Updated `api/openapi.yaml`'s `Error` schema
  (enum of the 6 reserved top-level codes, `example: invalid_input`, new optional
  `details`/`FieldError` array and schema) and swept the rest of the spec for stale ad-hoc codes
  (none found beyond the one already-fixed example). Reviewed `docs/architecture.md` and
  `docs/mod-users-spec.md` in full: `architecture.md` needed only its existing 400
  `anonymous_account` mention annotated as known, intentionally-deferred flat-envelope
  non-conformance; `mod-users-spec.md` needed its general error-shape bullet rewritten plus its two
  `anonymous_account` mentions similarly annotated. All edits were additive/clarifying. This task
  also surfaced a significant, previously-untracked scope gap (see Follow-up items below).

## Key decisions

Drawn primarily from task digests, plus notable entries from `plan/followups.yaml`.

- **Single classification point stays in `apiresp`, but `respond.go` was kept as a thin wrapper**
  rather than migrating every caller directly onto `apiresp.WriteError` — Phase 1 collapsed the three
  local sentinel-classifying helpers onto `apiresp.WriteError` while leaving `server/respond.go` as a
  delegating shim.
- **`svc.ErrEmailTaken`'s conflict envelope is a documented stopgap, not a full delegation** —
  `writeServiceError`'s email-taken branch hand-constructs the 409 response via
  `apiresp.WriteJSON`/`Envelope`/`ErrorBody` (including a literal message string duplicating
  `apiresp`'s own unexported `publicMessage("conflict")` output) because `mod-core/api/apiresp`
  exposes `InvalidInput(...)` but no equivalent public `Conflict(...)` constructor. Flagged as a
  fast-follow for `mod-core` (follow-up `ZVum`).
- **`identity_not_found` maps to `not_found` (not masked)** — the identity-unlink lookup in
  `identities.go` is self-scoped and not `EntityResolver`-mediated, so the usual masking rule doesn't
  apply and a direct 404 is correct. Recorded explicitly in the Phase 2 task doc.
- **Literal `server.Error(status, "code", message)` call sites were deliberately left uncentralized**
  — ~28 sites across `apps.go`, `oidc_providers.go`, `oidc_config.go`, `identities.go`, `self.go`,
  `assume.go`, `user_accounts.go` now carry correct vocabulary but are not yet derived from a single
  sentinel-classification point, per the Phase 2 task docs' explicitly sanctioned scope deferral
  (follow-up `8iRl`).
- **GUI 401 handling: code renamed, throw behavior preserved** — `api.ts`'s synthesized 401 code
  changed `unauthorized` → `unauthenticated`, but the unconditional throw (independent of
  `skipAuthRedirect`, which only gates the redirect side-effect) was intentionally preserved because
  the OAuth-return caller relies on it.
- **`ErrorMessage` was kept as a thin wrapper rather than retired** — since `@moduleforge/core-gui`'s
  `ErrorBannerData` accepts a plain string, delegating from inside `ErrorMessage` avoided any call-site
  churn.
- **The 5 flat-envelope error sites were deliberately deferred out of this plan** (per
  `overview.md`'s "Open scope question"): `require_verified.go`, `require_confirmed.go`, and two
  `identities.go` sites (`step_up_required`, `last_identity`) use a flat `error: string` envelope that
  cross-repo consumers (`app-mfdemo`/`app-mftodo`) key off directly, so migrating them is a breaking
  structural change requiring cross-repo coordination — out of scope for a `mod-users`-only plan.
  The plan was authored assuming this becomes a tracked follow-up rather than an in-plan task.

## Follow-up items

`plan/followups.yaml` in this worktree is a repo-wide log shared across several prior, unrelated
plans that reused this worktree lineage. The list below is filtered to only the items tagged with
this plan's four phase slugs (`go-apiresp-foundation`, `go-vocab-migration`, `gui-core-gui-adoption`,
`doc-updates`) and dated 2026-07-16, i.e. items actually produced by this plan's execution.

**`go-apiresp-foundation`**

- `make lint.model`'s shadow-db-lint step can't reach its ephemeral Postgres container in this
  sandbox (Docker networking timeout) — an environment limitation, not a `model/` code issue;
  `model/` was untouched by this task. (`0x4L`)
- Phase-01's link-chain check found several pre-existing, unrelated project docs unreachable from
  `README.md` (`api/server/CLAUDE.md`, `deploy/k8s/README.md`, `deploy/local/README.md`,
  `deploy/serverless/README.md`, `model/README.md`, `next-steps.md`). Not touched by this plan's
  diff; recommend a manager/tech-writer pass to either link them from `README.md` or confirm they're
  intentionally out-of-band. (`rmQ5`)
- `apiresp` needs a public `Conflict(...)` constructor (mirroring `InvalidInput(...)`) so
  `writeServiceError`'s email-taken branch can collapse fully onto `apiresp.WriteError` instead of
  hand-constructing its envelope — a cross-repo fast-follow on `mod-core`. (`ZVum`)

**`go-vocab-migration`**

- ~28 literal `server.Error(status, "code", message)` call sites across the touched handler files
  are still duplicated rather than derived from a single sentinel-classification point; recommend an
  explicit follow-up phase/task to collapse them onto sentinel-driven `apiresp.WriteError` calls so
  the centralization goal doesn't silently fall out of the plan. (`8iRl`)
- Non-blocking cosmetic inconsistency: the migrated `ErrorWithDetails` call sites use lowercase,
  unpunctuated `"one or more fields are invalid"` while the design doc's worked example uses
  `"One or more fields are invalid."` (capitalized, trailing period). Recommend aligning wording or
  amending the design doc to note the example is illustrative. (`WGZV`)

**`gui-core-gui-adoption`**

- `cd gui && bun test` is not applicable: `gui/` has zero test files and no test script in its
  history; establishing test infrastructure is out of scope for both Phase 3 tasks. (`t5kn`, `KXNZ`)
- Flow's `mid-task-commit.sh`/`finalize-task-commit.sh` `git add -A` is not yalc-aware and repeatedly
  swept up this worktree's intentionally-uncommitted local `file:.yalc/@moduleforge/core-gui` entries
  in `gui/package.json`/`bun.lock`, requiring revert commits mid-task — a friction point for any
  future `gui/`-touching task while the yalc workflow is in place. (`le59`)
- Pre-existing, not investigated further: `mod-core/gui`'s built `dist/` has no `dist/index.css`
  despite `package.json`'s `./styles.css` export; not touched since `error-message.tsx` needed no
  styling changes. (`JFnc`)
- `@moduleforge/core-gui`'s `<ErrorBanner>` drops the `AlertCircle` icon the old hand-rolled markup
  rendered. Not a functional defect, but worth a visual check if icon presence in destructive banners
  matters to the design system — raise with whoever owns `core-gui`'s `ErrorBanner`. (`ygBr`)
- `gui/src/lib/api.ts`'s `request()` casts the parsed error body directly to `ApiErrorResponse` with
  no runtime validation that `details` is actually an array of well-shaped objects. Low-confidence,
  non-blocking (mirrors an existing accepted pattern in `core-gui`'s own `request()`); optional
  hardening to validate/coerce shape after parsing. (`L92R`)
- The design doc names the wire interface `FieldError`, but the shipped `@moduleforge/core-gui`
  export is `FieldErrorData` (`FieldError` is taken by the `<FieldError>` component). `mod-users`
  correctly follows the shipped name; the naming drift is inherited from Wave 0/`core-gui` and needs
  reconciling in `docs-mf-standards` or on the `core-gui` side, not from `mod-users`. (`8oow`)

**`doc-updates`**

- **Major scope-gap finding:** `api/internal/handlers/auth/*.go` (`register.go`, `login.go`,
  `emailcode.go`, `anonymous.go`, `oidc.go`, `reset.go`) was never covered by either Phase 2 task and
  still contains ~24–33 literal `bad_request`/`unauthorized`/`validation_error`/`email_taken` sites in
  the module's highest-traffic unauthenticated endpoints (register, login, anonymous account
  creation, email-code verification, OIDC, password reset). This is distinct from follow-up `8iRl`
  (which covers only the 7 files Phase 2 actually touched). The new `openapi.yaml` `Error.code` enum
  now documents a target contract this slice of the API does not yet meet. Manager-verified via
  33 grep matches across 6 production files. (`biPE`)
- Likely pre-existing, unrelated behavioral/doc mismatch: `anonymous_account` does not appear
  anywhere in `api/internal` Go source, and login/email-code/password-reset show no `IsAnonymous`
  guard logic matching the documented "400 `anonymous_account`" behavior. Not fixed — out of scope
  for a docs-only task; worth a dedicated doc-accuracy pass. (`nnfn`)
- Environment note: the `docs/mf-standards` git submodule was uninitialized at task start in this
  worktree; initialized read-only (pointer unchanged) to read the referenced design doc. (`jg7u`)

**Carried forward from `overview.md` (not a `followups.yaml` entry, but load-bearing):**

- The 5 flat-envelope error sites (`require_verified.go`, `require_confirmed.go`, and two
  `identities.go` sites) remain deferred pending a manager/user decision on whether to fold them into
  a future in-scope task or track them as a coordinated cross-repo follow-up with
  `app-mfdemo`/`app-mftodo`.
- Manager-run cross-repo validation (regenerating `app-mfdemo` and `app-mftodo` via `mfgen generate`
  and confirming they build) is still outstanding — it cannot be performed from within this repo's
  task worktree.
