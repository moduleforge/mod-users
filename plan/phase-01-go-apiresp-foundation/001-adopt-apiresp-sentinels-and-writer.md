# Adopt Apiresp Sentinels And Writer

## Purpose and scope

Foundation for the whole Go migration. Adopt the shared `github.com/moduleforge/core-api/apiresp`
package (delivered by Wave 0) as the single owner of `mod-users`' HTTP error classification and
envelope construction: promote the module's local authz sentinels onto `apiresp`'s canonical
sentinels, and collapse the three sentinel-classifying handler helpers onto `apiresp.WriteError`.

This task establishes the mechanism Phase 2's per-handler vocabulary migration relies on. It does
**not** migrate the ad-hoc string-literal `error.code` call sites — those are Phase 2. It touches only
the sentinel-driven paths.

No standard skill covers this; see `## Procedure`.

## Requirements

1. **Wire the dependency.** Confirm `github.com/moduleforge/core-api/apiresp` resolves in `api/`
   (`api/go.mod` already has `replace github.com/moduleforge/core-api v0.0.0 => ../../mod-core/api`;
   `apiresp` is a subpackage that exists only once Wave 0 is merged). Add the import where used;
   run `go mod tidy` in `api/` if required.

2. **Promote the authz sentinels (design doc "Go-layer ownership" §, item 1).**
   `api/internal/authz/authz.go` currently defines local
   `ErrUnauthenticated = errors.New("authz: no authenticated actor")` and
   `ErrForbidden = errors.New("authz: forbidden")` (lines ~36–42) in an `internal/` package that no
   other module can import. Repoint these onto `apiresp`'s canonical sentinels so `errors.Is` matches
   across modules. Preferred low-blast-radius approach: replace the two `var` declarations with
   aliases —
   ```go
   var ErrUnauthenticated = apiresp.ErrUnauthenticated
   var ErrForbidden       = apiresp.ErrForbidden
   ```
   so the `Authorizer` returns the canonical sentinels and all existing internal references
   (authz.go's own returns, `handlers/errors.go`, `handlers/user_accounts.go`) keep compiling.
   Full removal of the local names in favor of direct `apiresp.*` references is acceptable at the
   implementer's discretion, but the **canonical home must move to `apiresp`** — do not leave a
   parallel independent sentinel.

3. **Collapse the sentinel-classifying helpers onto `apiresp.WriteError`.** Each of these switches on
   sentinels manually and calls `server.Error(...)`; replace the body with `apiresp.WriteError(w, r, err)`
   (note `WriteError` needs the `*http.Request` — thread it through; update signatures and call sites
   accordingly):
   - `api/internal/handlers/errors.go` — `writeAuthzError` (maps `ErrUnauthenticated` → 401,
     else → 403).
   - `api/internal/handlers/user_accounts.go` — `writeServiceError` (maps `ErrUnauthenticated`,
     `ErrForbidden`, `svc.ErrEmailTaken`, `svc.ErrInvalidInput`, default → 500).
   - `api/internal/handlers/self.go` — `writeCoreServiceErr` (maps `coreservice.ErrNotFound`,
     `ErrForbidden`, `ErrInvalidInput`, default → 500).

4. **Service-error → detail mappings (design doc worked example + item 4).** `apiresp.WriteError`
   classifies `apiresp` sentinels via `errors.Is`, but `mod-users`' **service** sentinels
   (`svc.ErrEmailTaken`, `svc.ErrInvalidInput`) are not `apiresp` sentinels. Ensure they land on the
   correct wire shape:
   - `svc.ErrEmailTaken` → top-level **`conflict`** (409) + `details[]`:
     `{field: "email", code: "users.email_taken", message: …}`. Per the design doc this is a
     deliberate plain 409 (create-time uniqueness), **not** a masked 403 — do **not** apply masking
     logic to this path.
   - `svc.ErrInvalidInput` → top-level **`invalid_input`** (400) + per-field `users.<rule>`
     `details[]`.
   Achieve this by having the service errors wrap the corresponding `apiresp` sentinel and carry the
   detail (using whatever detail-carrying constructor Wave 0's `apiresp` exposes — analogous to
   `apiresp.InvalidInput(...)`), so `apiresp.WriteError` produces the envelope. If Wave 0's `apiresp`
   exposes no public detail constructor for `conflict`, fall back to constructing the conflict
   envelope at the `writeServiceError` mapping point using `apiresp`'s writer/`WriteJSON` — but the
   sentinel classification must still come from `apiresp`. Record which mechanism was used.

5. **Decide `server/respond.go`'s fate (design doc item 2).** `api/internal/server/respond.go` today
   emits the nested `{error:{code,message[,details]}}` shape via `JSON`/`Error`/`ErrorWithDetails`
   (`ErrorWithDetails` is currently **unused** anywhere). Either (a) keep these as thin wrappers that
   delegate to `apiresp` internally, or (b) migrate callers directly onto `apiresp`. Implementer's
   choice, **but the underlying sentinel classification and envelope construction must be `apiresp`'s
   single implementation, not a parallel reimplementation.** If keeping `respond.go` as wrappers,
   `JSON` may stay (bare success bodies) but `Error`/`ErrorWithDetails` must route through `apiresp`.
   The many direct `server.Error(w, status, "code", msg)` literal call sites are migrated in Phase 2 —
   leave them for that phase, but ensure whatever `respond.go` shape you choose does not block Phase 2.

6. **Update affected tests.** `api/internal/handlers/user_accounts_authz_test.go` asserts on
   sentinel-path codes produced by `writeServiceError` (e.g. `email_taken`, `unauthorized`). Update
   those assertions to the new wire shape (`email_taken` → top-level `conflict` with
   `details[].code == "users.email_taken"`; `unauthorized` → `unauthenticated`). Do **not** touch
   assertions on literal-code sites owned by Phase 2.

## Validation

- `cd api && go build ./...` succeeds (with Wave 0 merged so `apiresp` resolves).
- `make test.unit` (or `cd api && go test ./...`) passes, including the updated
  `user_accounts_authz_test.go`.
- `make lint` passes.
- `grep -rn 'errors.New("authz:' api/internal/authz/authz.go` — the local sentinels are gone or are
  aliases to `apiresp.*` (no independent `errors.New` sentinel remains as the canonical home).
- `grep -rn 'errors.Is(err, localAuthz.Err' api/internal/handlers/` — remaining references resolve
  against the promoted (`apiresp`-backed) sentinels.
- The three helpers (`writeAuthzError`, `writeServiceError`, `writeCoreServiceErr`) no longer contain a
  hand-written sentinel→`server.Error` switch; they delegate to `apiresp.WriteError`.
- Manual: confirm a `svc.ErrEmailTaken` path returns `409` with body
  `{"error":{"code":"conflict","message":…,"details":[{"field":"email","code":"users.email_taken",…}]}}`.

## Metadata

architectural_impact: true

## Assumptions

- **Wave 0 (`mod-core` `apiresp-error-widgets`) is merged** and
  `github.com/moduleforge/core-api/apiresp` resolves via the existing `replace` directive. If it does
  not resolve, halt — this task cannot proceed.
- Wave 0's `apiresp` exposes `ErrUnauthenticated`, `ErrForbidden`, `ErrNotFound`, `ErrInvalidInput`,
  `ErrConflict`, `WriteJSON`, `WriteError(w, r, err)`, and `InvalidInput(...)` per the design doc's
  "Go-layer ownership" section. The exact detail-attachment API for non-`invalid_input` sentinels
  (e.g. `conflict`) is whatever Wave 0 ships; adapt to it and record the mechanism used.
- This task deliberately leaves the direct `server.Error(w, …, "literal", …)` call sites for Phase 2.

## References

- `docs/mf-standards/architecture/api-response-design.md` — "Error-code vocabulary", "HTTP status
  mapping", "Go-layer ownership" (the `apiresp` package surface and the worked `email_taken` example).
- `api/internal/authz/authz.go` (sentinels), `api/internal/server/respond.go` (writer),
  `api/internal/handlers/errors.go`, `api/internal/handlers/user_accounts.go`,
  `api/internal/handlers/self.go` (the three helpers).

## Procedure

1. Confirm `apiresp` resolves; add the import.
2. Alias/repoint the authz sentinels onto `apiresp` (item 2).
3. Rewrite `writeAuthzError`, `writeServiceError`, `writeCoreServiceErr` to delegate to
   `apiresp.WriteError`, threading `*http.Request` (items 3–4).
4. Ensure `svc.ErrEmailTaken`/`svc.ErrInvalidInput` carry the correct detail codes (item 4).
5. Decide and apply `respond.go`'s shape (item 5).
6. Update `user_accounts_authz_test.go` sentinel-path assertions (item 6).
7. Build, test, lint.

## Checkpoint hints

- After promoting the authz sentinels and confirming `go build` in `authz/`.
- After migrating the three helpers.
- After the `svc.ErrEmailTaken`/`svc.ErrInvalidInput` detail wiring + test updates.

## Status

**Implementation outcome:** succeeded

**Date:** 2026-07-16

**Note on an earlier halt in this same session:** this task initially halted with `missing
prerequisites` because `cd api && go build ./...` could not resolve any sibling-module `replace`
directive (`core-api`, `core-model`, `audit-api`, `audit-model`, `authz-api`, `authz-model`) from
inside the provisioned task worktree — a worktree-nesting-depth artifact of `create-worktree.sh`
(`<repo-root>/worktrees/<branch>/` sits one path segment deeper than the committed relative `replace`
paths assume), documented at `docs/mf-standards/building-common.md` § "Building inside a task
worktree". The manager fixed this directly in the worktree with a worktree-local, gitignored `go.work`
(`go.work`/`go.work.sum`, plus explicit `go work edit -replace` overrides to resolve a
"conflicting replacements" error the plain `go work use` recipe alone did not avoid) and confirmed
`go build`/`go vet` exit 0 before handing the task back. This is an environment-only fix — nothing
about it is committed (both files are covered by the repo's existing `/go.work` / `/go.work.sum`
`.gitignore` entries) — and the actual implementation below proceeded once the worktree could build.

**Implementation summary:**

1. **Wired the dependency.** `github.com/moduleforge/core-api/apiresp` resolves in `api/` once the
   worktree's `go.work` was in place; no `api/go.mod`/`go.sum` change was needed (no `go mod tidy`
   was required — `apiresp` is a subpackage of the already-required `github.com/moduleforge/core-api`
   module).
2. **Promoted the authz sentinels** (`api/internal/authz/authz.go`): `ErrUnauthenticated`/
   `ErrForbidden` are now `var` aliases of `apiresp.ErrUnauthenticated`/`apiresp.ErrForbidden` (not
   independent `errors.New` sentinels). All existing internal references
   (`handlers/errors.go`, `handlers/user_accounts.go`, `handlers/apps.go`, `handlers/assume.go`)
   kept compiling unchanged where they still reference `localAuthz.Err*`/`svc.Err*` by name (those
   names now resolve to the promoted, apiresp-backed values).
3. **Collapsed the three sentinel-classifying helpers onto `apiresp.WriteError`**, threading
   `*http.Request` through each (and through every call site):
   - `handlers/errors.go` — `writeAuthzError(w, r, err)` is now a one-line delegate to
     `apiresp.WriteError(w, r, err)`. Note: the previous hand-written switch mapped *any*
     non-`ErrUnauthenticated` error to 403 (including, in principle, an unclassified `Authorize`-path
     DB error); `apiresp.WriteError` instead maps an unrecognized error to `500 internal_error` — a
     deliberate, requirement-directed behavior change (Requirement 3 explicitly names the current
     "else → 403" behavior and directs the mechanical `apiresp.WriteError` replacement).
   - `handlers/user_accounts.go` — `writeServiceError(w, r, err)` delegates to
     `apiresp.WriteError(w, r, err)` for every case except `svc.ErrEmailTaken`, which is special-cased
     (see point 4) because `apiresp` exposes no public conflict-detail constructor.
   - `handlers/self.go` — `writeCoreServiceErr(w, r, err)` is now a one-line delegate to
     `apiresp.WriteError(w, r, err)`; `coreservice.ErrNotFound`/`ErrForbidden`/`ErrInvalidInput` are
     already aliases of the `apiresp` sentinels on the `mod-core` side (confirmed by reading
     `mod-core/api/service/errors.go`), so no local switch is needed.
4. **Service-error → detail mappings:**
   - `svc.ErrEmailTaken` (`internal/service/user_accounts.go`) now wraps `apiresp.ErrConflict`
     (`fmt.Errorf("%w: email already registered", apiresp.ErrConflict)`) instead of being an
     independent `errors.New`. `apiresp` exposes no public detail-carrying constructor for the
     conflict sentinel (only `InvalidInput`), so per the task's documented fallback, `writeServiceError`
     special-cases `errors.Is(err, svc.ErrEmailTaken)` and constructs the envelope directly via
     `apiresp.WriteJSON`/`apiresp.Envelope`/`apiresp.ErrorBody`/`apiresp.FieldError` — apiresp's own
     types, not a parallel reimplementation — attaching `{field: "email", code: "users.email_taken",
     message: "email is already registered"}`. Sentinel classification (that this is a 409) still
     traces to `apiresp.ErrConflict` via the wrap; `apiresp.WriteError` alone would already map
     `svc.ErrEmailTaken` to 409, just without the field detail.
   - `svc.ErrInvalidInput` is now a direct alias of `apiresp.ErrInvalidInput` (mirroring
     `mod-core/api/service/errors.go`'s existing pattern for the same sentinel). All five call sites
     that used to do `fmt.Errorf("%w: <msg>", ErrInvalidInput)` (`Create`'s email/given_name/
     family_name/password checks, `CreateAnonymousUser`'s device_id check) now build the error via
     `apiresp.InvalidInput(apiresp.FieldError{Field: ..., Code: "users.<rule>", Message: ...})`, e.g.
     `users.email_required`, `users.given_name_required`, `users.family_name_required`,
     `users.password_too_short`, `users.device_id_required`.
5. **`server/respond.go`'s fate:** kept as thin wrappers (option (a) in the task doc). `JSON` is
   unchanged (bare success bodies, per the task doc's explicit allowance). `Error` and
   `ErrorWithDetails` now build the response via `apiresp.WriteJSON`/`apiresp.Envelope`/
   `apiresp.ErrorBody` (`ErrorWithDetails`'s `details` parameter is now typed `[]apiresp.FieldError`
   instead of `any` — it was previously unused anywhere in the codebase, so this is not a breaking
   change) instead of a local `map[string]any` reimplementation, so envelope construction is
   apiresp's single implementation everywhere. The many literal `server.Error(w, status, "code", msg)`
   call sites are deliberately untouched — they compile and behave identically (same wire shape) and
   are Phase 2's job to migrate onto sentinel-driven `apiresp.WriteError` calls.
6. **Updated `user_accounts_authz_test.go`**: `TestWriteServiceError_Mapping` now asserts the wire
   shape via an `*http.Request` + JSON-body decode (status **and** `error.code` **and**, for
   `email_taken`, `error.details[]`) instead of only the HTTP status: `unauthenticated` (was
   `unauthorized`), `forbidden`, `email_taken` → top-level `conflict` with
   `details[0] == {field: "email", code: "users.email_taken", message: "email is already
   registered"}`, `invalid_input`, and an unclassified error → `internal_error`. All `writeServiceError`
   call sites in the file's `shim` test harness were updated to the new 3-arg signature. No assertion
   on a literal-code call site (Phase 2 territory) was touched.

**Decisions made:**
- `writeServiceError`'s `svc.ErrEmailTaken` special case checks `errors.Is(err, svc.ErrEmailTaken)`
  specifically (not the more generic `errors.Is(err, apiresp.ErrConflict)`) — precise to the one
  known conflict source in this handler today, while still tracing back to `apiresp.ErrConflict`
  for classification (satisfying "sentinel classification must still come from apiresp"). If a second,
  differently-detailed conflict source is ever added to this handler, its check will need its own
  branch rather than silently reusing the email detail — noted here for whoever does that.
- Kept `server/respond.go` as thin wrappers (option (a)) rather than migrating the ~14/~15 literal
  `server.Error`/`server.JSON` call sites directly onto `apiresp` — smaller blast radius, and the task
  doc explicitly reserves those call sites for Phase 2.

**Validation:**
- ✓ `cd api && go build ./...` exits 0.
- ✓ `cd api && go vet ./...` exits 0.
- ✓ `make test.unit` (repo-wide: model/api/gui) passes; `cd api && go test -count=1 ./...` passes
  (all packages, including the updated `internal/handlers` and `internal/service`).
- ✗/environmental — `make lint` (repo-wide) fails, but not for any reason introduced by this diff:
  `lint.model` fails because its shadow-db-lint step cannot reach an ephemeral Postgres container in
  this sandbox (`dial tcp 172.17.0.2:5432: connect: operation timed out`) — `model/` is untouched by
  this task. `lint.api` (`go vet` + `gofmt -l .`) fails only because `api/config/config.go` — a file
  this task never touches — has a pre-existing `gofmt` alignment issue, confirmed present on the
  pre-task `HEAD` (`git show HEAD:api/config/config.go | gofmt -l -` flags it) and unrelated to
  apiresp/sentinels. `go vet ./...` alone (the other half of `lint.api`) passes with no output, and
  `gofmt -l` on every file this task touched (`internal/authz/authz.go`,
  `internal/handlers/{apps,assume,errors,self,user_accounts,user_accounts_authz_test}.go`,
  `internal/server/respond.go`, `internal/service/user_accounts.go`) is empty. See flagged items below.
- ✓ `grep -rn 'errors.New("authz:' api/internal/authz/authz.go` — no matches (the local sentinels are
  gone; both are now `apiresp.*` aliases).
- ✓ `grep -rn 'errors.Is(err, localAuthz.Err' api/internal/handlers/` — no matches (the sentinel
  switches that used to compare against `localAuthz.Err*` were replaced with `apiresp.WriteError`
  delegation entirely, so there is nothing left doing a local sentinel comparison).
- ✓ The three helpers (`writeAuthzError`, `writeServiceError`, `writeCoreServiceErr`) no longer
  contain a hand-written sentinel→`server.Error` switch; each delegates to `apiresp.WriteError`
  (`writeServiceError` additionally special-cases `svc.ErrEmailTaken` to attach the conflict detail,
  per Requirement 4's documented fallback).
- ✓ Manual: `TestWriteServiceError_Mapping/email_taken` in `user_accounts_authz_test.go` decodes the
  actual response body and asserts `status == 409`, `error.code == "conflict"`, and
  `error.details == [{"field":"email","code":"users.email_taken","message":"email is already
  registered"}]` — i.e. exactly the body shape the task doc specifies.

**Files touched:**
- `api/internal/authz/authz.go`
- `api/internal/handlers/apps.go`
- `api/internal/handlers/assume.go`
- `api/internal/handlers/errors.go`
- `api/internal/handlers/self.go`
- `api/internal/handlers/user_accounts.go`
- `api/internal/handlers/user_accounts_authz_test.go`
- `api/internal/server/respond.go`
- `api/internal/service/user_accounts.go`

**Flagged for manager:**
- `api/config/config.go` has a pre-existing `gofmt` alignment issue (a `const`/`var`-block column
  misalignment), confirmed present on this task's starting `HEAD`, unrelated to apiresp/sentinels, and
  outside this task's diff — it currently blocks a clean `make lint`/`make lint.api` run. Did not fix
  it myself (would require touching a file outside this task's diff, so it doesn't qualify for the
  same-diff self-fix carve-out); likely a `dispatch-simple-task`-sized `gofmt -w` fix.
- `make lint.model` (`shadow-db-lint`) cannot reach its ephemeral Postgres container in this sandbox
  environment (Docker networking timeout to `172.17.0.2:5432`) — an environment limitation, not a
  `model/` code issue; `model/` is untouched by this task.
- The worktree-nesting/`go.work` build fix the manager applied is worktree-local and gitignored, so it
  does not travel with this branch's commits. Whatever mechanism eventually formalizes the fix
  (`create-worktree.sh` change, a documented per-worktree setup step, etc.) will need to be applied
  again for this task's *own* worktree if it is ever rebuilt from a fresh `git worktree add`, and for
  every other task worktree touching `api/` in this plan.
