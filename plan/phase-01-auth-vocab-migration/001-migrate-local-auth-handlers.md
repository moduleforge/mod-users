# Migrate Local Auth Handlers

## Purpose and scope

Migrate every literal ad-hoc top-level `error.code` string in `api/internal/handlers/auth/register.go`,
`login.go`, `emailcode.go`, `anonymous.go`, and `reset.go` (all methods on the shared `*Handler` struct
defined in `login.go`) onto the reserved `apiresp` error-code vocabulary, per
[`docs/mf-standards/architecture/api-response-design.md`](../../docs/mf-standards/architecture/api-response-design.md)'s
"Module-specific extension codes" section. This closes part of the scope gap recorded as follow-up
`biPE` in `plan/followups.yaml`. No standard task-procedure skill applies beyond ordinary
implementation conventions; this is a direct code-edit task guided by the mechanical mapping table
below and the worked reference implementation in `api/internal/handlers/user_accounts.go`.

This task's sibling, `002-migrate-oidc-auth-handler.md`, covers `oidc.go` (a separate `*OIDCHandler`
struct with its own test files) and is parallel-eligible with this task — no production file or
`_test.go` file is shared between the two.

## Requirements

### 1. Mechanical code-swap sites (no field details)

Replace the literal code string in these `server.Error(w, status, "<old-code>", message)` call sites,
keeping status, message text, and the `server.Error(...)` call form unchanged — swap only the code
argument:

- **`bad_request` → `invalid_input`** (status stays 400):
  - `register.go:109` ("invalid JSON body")
  - `login.go:29` ("invalid JSON body"), `login.go:35` ("email and password are required")
  - `emailcode.go:39`, `emailcode.go:125` (both "invalid JSON body"), `emailcode.go:131` ("email and
    code are required")
  - `anonymous.go:26` ("invalid JSON body")
  - `reset.go:37`, `reset.go:95` (both "invalid JSON body"), `reset.go:100` ("token is required")
- **`unauthorized` → `unauthenticated`** (status stays 401; do **not** add `details[]` — see
  Requirement 3 for why):
  - `login.go:62`, `login.go:73` (both "invalid email or password")
  - `emailcode.go:140` ("invalid code"), `emailcode.go:154`, `emailcode.go:164` (both "invalid or
    expired code")
  - `reset.go:114` ("invalid or expired reset token")

That is 12 `bad_request` sites and 6 `unauthorized` sites — 18 mechanical swaps across the five files.
Do **not** touch any `internal_error` or (there are none in this file group) `not_found` site — those
are already conformant.

### 2. `validation_error` → `invalid_input` + `details[]` via `server.ErrorWithDetails`

Replace each of the following `server.Error(w, http.StatusBadRequest, "validation_error", message)`
calls with `server.ErrorWithDetails(w, http.StatusBadRequest, "invalid_input", message, []apiresp.FieldError{...})`.
**Use the exact field/code pairs already established by `api/internal/service/user_accounts.go`'s
`Create` and `CreateAnonymousUser` methods** (which validate the same fields for the admin
create-user-account and anonymous-account flows respectively) — do not invent new detail codes for
fields that already have an established `users.<rule>` name:

| Site | Field | Detail code | Message (keep existing wording) |
|---|---|---|---|
| `register.go:116` | `email` | `users.email_required` | "email is required" |
| `register.go:120` | `password` | `users.password_too_short` | "password must be at least 12 characters" |
| `register.go:124` | `given_name` | `users.given_name_required` | "given_name is required" |
| `register.go:128` | `family_name` | `users.family_name_required` | "family_name is required" |
| `anonymous.go:31` | `device_id` | `users.device_id_required` | "device_id is required" |
| `reset.go:104` | `password` | `users.password_too_short` | "password must be at least 12 characters" |

`server.ErrorWithDetails`'s signature is `func ErrorWithDetails(w http.ResponseWriter, status int, code string, message string, details []apiresp.FieldError)` (`api/internal/server/respond.go`) — it already builds the identical `apiresp.Envelope`/`apiresp.ErrorBody` shape `server.Error` does, just with a populated `Details` field. You will need to add the import
`"github.com/moduleforge/core-api/apiresp"` to each file where you introduce an `apiresp.FieldError`
literal (none of these five files import it today).

Top-level `message` stays the existing site-specific wording (e.g. "email is required") — do **not**
replace it with the design doc's generic "One or more fields are invalid." illustrative example; that
example is illustrative only (see the prior plan's follow-up `WGZV` on this exact point), and keeping
the specific per-site message preserves today's client-visible behavior for the top-level `message`
while the `details[]` entry carries the machine-readable field code.

### 3. `email_taken` → `conflict` (409) + `details[]`

`register.go:203` currently reads:

```go
server.Error(w, http.StatusConflict, "email_taken", "an account with that email already exists")
```

Replace it with the manual-envelope pattern `writeServiceError` in
`api/internal/handlers/user_accounts.go` establishes for exactly this case (`apiresp` has no public
`Conflict(...)` constructor yet — tracked separately as follow-up `ZVum`, cross-repo in `mod-core`, out
of scope here). Concretely, use `server.ErrorWithDetails` (which is the same
`apiresp.WriteJSON`/`Envelope`/`ErrorBody` construction `writeServiceError` hand-rolls, already
available as a thin wrapper in this package's own `server` import):

```go
server.ErrorWithDetails(w, http.StatusConflict, "conflict",
    "an account with that email already exists",
    []apiresp.FieldError{
        {Field: "email", Code: "users.email_taken", Message: "email is already registered"},
    },
)
```

Keep the top-level message as the existing site-specific wording ("an account with that email already
exists"); use the established detail message ("email is already registered", matching
`user_accounts.go` and `apps.go`'s own `users.email_taken` usage) for the `details[]` entry.

### 4. Do not add `details[]` to the generic `unauthorized`→`unauthenticated` sites

Unlike the prior plan's Phase 2 (`identities.go`/`self.go`), which added a `users.bad_credentials`
field detail bound to `current_password` for its change-password flow, the `unauthorized` sites in this
task's scope are generic login/code/token failures with no single distinguishing field — the source
literals here say `"unauthorized"`, not an ad-hoc `"bad_credentials"` code, and the design doc's
mapping table only prescribes a detail code for the latter. Do a plain top-level code swap
(`unauthorized` → `unauthenticated`) with no `details[]` addition. This also matters for the
anti-enumeration design of these endpoints: `login.go`, `emailcode.go`, and `reset.go` deliberately use
one generic message ("invalid email or password" / "invalid or expired code" / "invalid or expired
reset token") for both the "identity not found" and "credential wrong" cases specifically so a caller
cannot distinguish the two — do **not** split these into a `not_found` case and an `unauthenticated`
case, and do not add a `details[]` entry that would leak which sub-case occurred. This is the
"`identity_not_found`-style codes → `not_found` only when self/user-scoped" caution from the task
brief: verified per-site here, and the correct call in every one of these six sites is *not* to apply
it, because these are anti-enumeration paths, not self-scoped lookups.

### 5. Update test assertions

Update the matching `_test.go` files' expected-code assertions (not just status) to match the new
codes:

- `guards_test.go`'s `TestLogin_AnonymousAccount` table: all three `wantCode: "bad_request"` →
  `wantCode: "invalid_input"`.
- `anonymous_test.go`'s `TestAnonymous` table: the two `wantCode: "validation_error"` entries →
  `wantCode: "invalid_input"`; the one `wantCode: "bad_request"` entry → `wantCode: "invalid_input"`.
  Leave the `wantCode: "internal_error"` entry untouched.

Grep the whole `api/internal/handlers/auth/` package for `"bad_request"`, `"unauthorized"`,
`"validation_error"`, and `"email_taken"` after your edits to confirm no residual literal remains in
`register.go`, `login.go`, `emailcode.go`, `anonymous.go`, `reset.go`, `guards_test.go`, or
`anonymous_test.go` (there should be zero matches in these seven files; `oidc.go`'s sibling task and
`oidc_test.go`/`oidc_linkmode_test.go` are out of this task's scope — do not touch them).

## Validation

- `make build.api` (or `cd api && go build ./...`) succeeds.
- `cd api && go vet ./...` is clean for the touched files.
- `make test.unit` (or `cd api && go test ./...`) passes, including the updated
  `TestLogin_AnonymousAccount` and `TestAnonymous` cases.
- `grep -rn '"bad_request"\|"unauthorized"\|"validation_error"\|"email_taken"' api/internal/handlers/auth/register.go api/internal/handlers/auth/login.go api/internal/handlers/auth/emailcode.go api/internal/handlers/auth/anonymous.go api/internal/handlers/auth/reset.go api/internal/handlers/auth/guards_test.go api/internal/handlers/auth/anonymous_test.go` returns no matches.
- Manually confirm (read the diff) that every `server.Error(...)` call form is preserved as-is (no
  site was converted to `apiresp.WriteError(w, r, err)`), per this plan's explicit scope boundary.
- Manually confirm the two anti-enumeration flows (`login.go`'s not-found-vs-bad-password branch,
  `emailcode.go`'s email-lookup-miss branch) still return the exact same generic message/status/code
  for both the "user not found" and "credential wrong" sub-cases — this task must not introduce a
  behavioral or timing difference between those two sub-cases (security-review lens; this endpoint
  group is `mod-users`' highest-traffic unauthenticated surface).
- `make lint` (read-only) — note any pre-existing/environmental failures unrelated to this diff rather
  than treating them as blocking (the prior plan's tasks in this same package hit this; see
  `plan/followups.yaml` history if it recurs).

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
  — canonical reserved-code mapping table (see "Module-specific extension codes") and worked examples.
- `api/internal/handlers/user_accounts.go`'s `writeServiceError` — reference implementation for the
  conflict-envelope pattern used in Requirement 3.
- `api/internal/service/user_accounts.go`'s `Create`/`CreateAnonymousUser` — source of the established
  `users.<rule>` detail codes reused in Requirement 2 (do not rename or duplicate these).
- `api/internal/server/respond.go` — `server.Error`/`server.ErrorWithDetails` signatures.
- `plan/followups.yaml` entries `biPE` (this scope gap), `eiF8` (the 5 deferred flat-envelope sites —
  do not touch), `8iRl` (the deferred `apiresp.WriteError` centralization — do not do it here), `ZVum`
  (why there's no `apiresp.Conflict()` constructor), `9EBV` (the go.work worktree fix).
- Prior plan's Phase 2 task outcomes (`plan/plan-summary-users-apiresp-migration.md`,
  `plan/summary.md`) — worked precedent for this exact kind of migration in
  `apps.go`/`oidc_providers.go`/`oidc_config.go` and `identities.go`/`self.go`/`assume.go`/
  `user_accounts.go`.

## Checkpoint hints

- After `register.go` (the most complex file: 6 sites spanning all three mapping categories).
- After `login.go` + `emailcode.go` + `reset.go` (the mechanical `bad_request`/`unauthorized` swaps,
  plus `reset.go`'s one `validation_error` site).
- After `anonymous.go`.
- After the two test-file updates (`guards_test.go`, `anonymous_test.go`).
