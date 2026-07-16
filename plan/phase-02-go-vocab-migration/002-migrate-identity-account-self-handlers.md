# Migrate Identity Account Self Handlers

## Purpose and scope

Migrate the ad-hoc top-level `error.code` string literals in the identity/account/self handler group
onto the reserved core vocabulary, moving finer distinctions into namespaced `details[].code` per the
design doc's mapping table. This group carries the plan's most interesting cases (`bad_credentials`,
`identity_not_found`, `unauthorized`). Scope is the nested-envelope `server.Error(...)` call sites in:

- `api/internal/handlers/identities.go`
- `api/internal/handlers/user_accounts.go` — **literal `server.Error` sites only** (its
  `writeServiceError` helper is migrated in Phase 1; do not re-touch it).
- `api/internal/handlers/self.go`
- `api/internal/handlers/assume.go`

Runs after Phase 1 has landed. **Parallel-eligible** with Phase 2 task 001 (disjoint files).

No standard skill covers this; see `## Procedure`.

## Requirements

Apply the design doc's mapping to every relevant `server.Error(...)` literal:

1. **`bad_request` → `invalid_input`** (top-level; status stays `400`). ~11 sites across the four
   files (e.g. `user_accounts.go` decode/`parseUUIDParam` sites, `self.go`, `assume.go`,
   `identities.go` JSON-body sites).

2. **`validation_error` → `invalid_input`** + per-field `users.<rule>` `details[]`. E.g.
   `identities.go:348` (password length) and `user_accounts.go:51` (in `writeServiceError` — **Phase 1
   owns that one**; skip it here). For `identities.go:348`, emit `invalid_input` +
   `{field: "new_password", code: "users.password_too_short", message: …}`.

3. **`unauthorized` → `unauthenticated`** (top-level; status stays `401`). Sites in `identities.go`,
   `oidc`-adjacent, and `user_accounts.go` literal paths. (The `writeServiceError`/`writeAuthzError`
   `unauthorized` occurrences are Phase 1; only migrate the direct literals here.)

4. **`bad_credentials` → `unauthenticated`** + `details[]`. `identities.go:368` and `:378` (change
   password: current_password required / incorrect). Top-level `unauthenticated` (401) +
   `{code: "users.bad_credentials", message: …}` detail (no bound form field, or `field:
   "current_password"`).

5. **`identity_not_found` → `not_found`** + `details[]`. `identities.go:298` (OIDC-identity unlink).
   **Decision (recorded here):** map to top-level **`not_found`** (404) +
   `{code: "users.identity_not_found", message: …}`, **not** the masked `forbidden`. Rationale: the
   design doc's masking-by-default governs the `EntityResolver`'s UUID→ID resolution step; this site
   is **not** `EntityResolver`-mediated — `errIdentityNotFound` is returned only when a delete scoped
   to the caller's own `UserAccountID` (see `identities.go` ~line 274–283) affects zero rows, i.e. the
   caller's *own* identity UUID is absent. There is no cross-user existence leak (a caller can only
   ever probe their own identities), so masking does not apply and a plain 404 is correct. If a
   reviewer disagrees, the fallback is top-level `forbidden` — flag rather than silently switch.

6. **Leave already-reserved codes unchanged** (`internal_error`, `not_found`, `forbidden`).

7. **Update the matching tests.** `identities_test.go`, `identities_stepup_test.go`, and the literal
   assertions in `user_accounts_authz_test.go` reference old codes — update to the new top-level codes
   and assert `details[].code` where applicable. Coordinate with Phase 1's edits to
   `user_accounts_authz_test.go` (Phase 1 owns the sentinel-path assertions; this task owns the
   literal-site assertions) — since Phase 1 lands first, rebase onto its state.

**Do NOT migrate the flat-envelope sites** `writeStepUpRequired` (`identities.go:624`,
`step_up_required` + `challenge_path`) or `writeLastIdentityError` (`identities.go:644`,
`last_identity`) — these are the deferred flat-envelope sites from the plan overview's "Open scope
question" and are out of scope pending a manager decision. Leave them untouched.

## Validation

- `cd api && go build ./...` and `make test.unit` pass.
- `make lint` passes.
- `grep -rn '"bad_request"\|"validation_error"\|"unauthorized"\|"bad_credentials"\|"identity_not_found"' api/internal/handlers/identities.go api/internal/handlers/self.go api/internal/handlers/assume.go`
  returns **no** non-test matches; for `user_accounts.go`, no non-test matches **outside**
  `writeServiceError` (Phase 1's helper).
- `grep -n '"step_up_required"\|"last_identity"' api/internal/handlers/identities.go` still returns the
  two flat-envelope helper sites **unchanged** (confirming they were correctly left out of scope).
- The `identity_not_found` site emits top-level `not_found` + `details[].code == "users.identity_not_found"`.
- The two `bad_credentials` sites emit top-level `unauthenticated` + `details[].code == "users.bad_credentials"`.
- Updated assertions in `identities_test.go`, `identities_stepup_test.go`, `user_accounts_authz_test.go`
  reference only reserved top-level codes plus `users.*` detail codes.

## Metadata

architectural_impact: true

## Assumptions

- **Wave 0 is merged** and Phase 1 (`adopt-apiresp-sentinels-and-writer`) has landed.
- String counts are approximate (research: `identities.go` ~6 `bad_request`, 2 `bad_credentials`, 1
  `identity_not_found`, 2 `unauthorized`, 1 `validation_error`; `user_accounts.go` ~3 literal
  `bad_request`; `self.go` 1 `bad_request`; `assume.go` 1 `bad_request`); re-grep at task start.
- The `identity_not_found` → `not_found` decision (item 5) is recorded as this task's default; a
  reviewer may override to `forbidden` but should do so explicitly.

## References

- `docs/mf-standards/architecture/api-response-design.md` — "Module-specific extension codes" mapping
  table; "Existence masking (`not_found` vs `forbidden`)" (informs item 5); "401 vs 403".
- Phase 1 task `001-adopt-apiresp-sentinels-and-writer.md`.
- Plan `overview.md` "Open scope question" — the flat-envelope sites this task must leave untouched.

## Procedure

1. Re-grep the four files for `server.Error(` literals; exclude `writeServiceError` in
   `user_accounts.go` (Phase 1) and the two flat-envelope helpers in `identities.go` (deferred).
2. Apply mappings (items 1–6).
3. Update the corresponding `_test.go` assertions (rebased onto Phase 1's `user_accounts_authz_test.go`
   edits).
4. Build, test, lint.

## Checkpoint hints

- After `identities.go` (the interesting cases) + `identities_test.go`/`identities_stepup_test.go`.
- After `user_accounts.go` literals + `self.go` + `assume.go` + `user_accounts_authz_test.go`.

## Status

**Implementation outcome:** succeeded

**Date:** 2026-07-16

**Implementation summary:**

Re-grepped the four in-scope files at task start and found the literal-site counts matched the
task doc's approximate research (17 total matches: 11 `bad_request`, 1 `validation_error`,
2 `bad_credentials`, 1 `identity_not_found`, 2 `unauthorized`), all inside `identities.go`, `self.go`,
`assume.go`, and `user_accounts.go`'s three literal sites (none inside `writeServiceError`, which
Phase 1 already migrated and which this task left untouched).

Applied the design doc's mapping table to every literal `server.Error(...)` call site:

1. **`bad_request` → `invalid_input`** (11 sites: `identities.go` StartLink/Unlink/SetPassword
   decode+validation/StepUpVerify decode+validation, `self.go` Put decode, `assume.go` Assume UUID
   parse, `user_accounts.go` Create decode/Update decode/`parseUUIDParam`). Straightforward top-level
   rename; status unchanged (400).
2. **`validation_error` → `invalid_input`** + `details[]` at `identities.go` SetPassword (password
   length check): now emits `invalid_input` + `{field: "new_password", code:
   "users.password_too_short", message: "password must be at least 12 characters"}`, via the
   existing `server.ErrorWithDetails` helper (already migrated onto `apiresp` envelope construction
   by Phase 1).
3. **`unauthorized` → `unauthenticated`** at the two `identities.go` `StepUpVerify` sites (no active
   code / wrong code). Top-level rename only; status unchanged (401).
4. **`bad_credentials` → `unauthenticated`** + `details[]` at `identities.go` SetPassword's two
   current-password sites (missing / incorrect). Both now emit `unauthenticated` +
   `{field: "current_password", code: "users.bad_credentials", message: …}`. **Decision:** used the
   `field: "current_password"` option from the task doc's "no bound form field, or field:
   'current_password'" alternative — both sites concern the same input, so binding the field is more
   useful to a GUI consumer than an unbound detail.
5. **`identity_not_found` → `not_found`** + `details[]` at `identities.go` Unlink (OIDC-identity
   unlink not-found path): now emits top-level `not_found` (404) +
   `{code: "users.identity_not_found", message: "identity not found"}`, per the task doc's recorded
   decision (this delete is scoped to the caller's own `UserAccountID`, not `EntityResolver`-mediated,
   so masking does not apply). Left a code comment at the call site recording the rationale for a
   future reader.
6. **Flat-envelope sites left untouched**, confirmed unchanged post-edit: `writeStepUpRequired`
   (`identities.go`, `step_up_required` + `challenge_path`) and `writeLastIdentityError`
   (`identities.go`, `last_identity`).

**Tests updated** (Requirement 7):
- `identities_test.go` — the `setPasswordPreCheckHandler` test double (a local, non-JSON
  `http.Error`-based mirror of `SetPassword`'s pre-transaction validation, used because the real
  transactional path can't run against the fakes) had its descriptive string literals updated from
  `bad_request:`/`validation_error:`/`bad_credentials:` to `invalid_input:`/`unauthenticated:` (with
  `users.bad_credentials`/`users.password_too_short` noted parenthetically) so they no longer
  reference retired top-level codes. These strings were never asserted on by the test (only HTTP
  status is checked) so this is a clarity-only change, not a behavior-relevant one; the sub-test name
  at line ~497 was also updated from "validation_error" to "invalid_input".
- `identities_stepup_test.go` — the `stepUpVerifyTestHandler` test double's two
  `http.Error(w, "unauthorized", 401)` calls updated to `"unauthenticated"` for the same reason
  (descriptive only; not asserted).
- `user_accounts_authz_test.go` — the `shim` struct is documented as "byte-for-byte identical to
  UserAccountsHandler" for handler-level testing without a concrete-typed service. Its 5
  `server_Error(w, http.StatusBadRequest, "bad_request", …)` calls (Create decode, Get/Delete/
  GrantAdmin/RevokeAdmin UUID-parse) were updated to `"invalid_input"` to keep the shim's mirrored
  behavior accurate, matching the same change applied to the real `user_accounts.go` sites. No
  `TestShim_*` test asserts on the JSON `error.code` value for these paths (only HTTP status, which
  is unchanged), so no test assertions needed to change beyond the shim's own literal. Phase 1's
  `TestWriteServiceError_Mapping` / `TestWriteAuthzError_Mapping` sentinel-path tests (owned by Phase
  1) were left untouched, per the task doc's file-ownership split.

**Decisions made:**
- `identity_not_found` → top-level `not_found` (not `forbidden`) per the task doc's recorded default;
  not overridden — the delete-scoped-to-own-account reasoning holds up on inspection of
  `identities.go`'s `Unlink` transaction (the `DeleteOIDCIdentityByUUID` query is
  `AND user_account_id = $2`-scoped).
- `bad_credentials` details bind `field: "current_password"` on both SetPassword sites (task doc
  offered this as one of two acceptable options).
- Updated three test files' non-asserted descriptive string literals (old top-level codes embedded in
  test-double error messages) for internal consistency, even though no test assertion depended on
  them — treated as in-scope under Requirement 7's "update the matching tests" / "reference old
  codes" language, and as same-diff self-fixes confined to files the task doc already names.

**Validation:**
- `cd api && go build ./...` — exits 0.
- `cd api && go vet ./...` — exits 0.
- `cd api && go test ./...` — all packages pass (including `internal/handlers`).
- `make test.unit` (repo-wide: model/api/gui) — passes (`model`: no unit tests, generated code;
  `api`: `go test ./...` passes; `gui`: no unit tests configured yet).
- `make lint` / `make lint.api` — **environmental failures, not caused by this task's diff**:
  `lint.model`'s `shadow-db-lint` step cannot reach an ephemeral Postgres container in this sandbox
  (`dial tcp 172.17.0.2:5432: connect: operation timed out`) — `model/` is untouched by this task,
  same failure mode Phase 1 already flagged for the manager. `lint.api` fails only on
  `gofmt -l api/config/config.go` (pre-existing misalignment, confirmed present on this task's
  starting `HEAD` via `git log`, and in a file this task never touches) — also already flagged by
  Phase 1. `go vet ./...` (the other half of `lint.api`) passes with no output, and
  `gofmt -l` on every file this task touched (`internal/handlers/{identities,self,assume,
  user_accounts,identities_test,identities_stepup_test,user_accounts_authz_test}.go`) is empty.
- Manual: added a throwaway unit test (not committed) directly exercising `server.ErrorWithDetails`
  with the exact arguments used at the `identity_not_found` and `bad_credentials` call sites, and
  confirmed the encoded JSON matches the task doc's required shape exactly:
  `{"error":{"code":"not_found","message":"identity not found","details":[{"field":"",
  "code":"users.identity_not_found","message":"identity not found"}]}}` and
  `{"error":{"code":"unauthenticated","message":"authentication is required","details":[
  {"field":"current_password","code":"users.bad_credentials","message":"current password is
  incorrect"}]}}`.
- `grep -rn '"bad_request"\|"validation_error"\|"unauthorized"\|"bad_credentials"\|
  "identity_not_found"' api/internal/handlers/identities.go api/internal/handlers/self.go
  api/internal/handlers/assume.go api/internal/handlers/user_accounts.go` — no matches (exit 1) in
  any of the four files.
- `grep -n '"step_up_required"\|"last_identity"' api/internal/handlers/identities.go` — still returns
  the two unchanged flat-envelope helper sites.

**Files touched:**
- `api/internal/handlers/identities.go`
- `api/internal/handlers/self.go`
- `api/internal/handlers/assume.go`
- `api/internal/handlers/user_accounts.go`
- `api/internal/handlers/identities_test.go`
- `api/internal/handlers/identities_stepup_test.go`
- `api/internal/handlers/user_accounts_authz_test.go`

**Flagged for manager:**
- `make lint` / `make lint.api` fail in this sandbox for the same two environmental/pre-existing
  reasons Phase 1 already flagged: `lint.model`'s ephemeral-Postgres Docker networking timeout
  (unrelated to `model/`, which this task never touches), and `api/config/config.go`'s pre-existing
  `gofmt` misalignment (confirmed present on this task's starting `HEAD`, in a file this task never
  touches). Not fixed here — fixing `config.go` would require touching a file outside this task's
  diff, so it doesn't qualify for the same-diff self-fix carve-out. Same underlying issues Phase 1
  flagged; still unresolved as of this task.
- Three test files' non-JSON-asserted descriptive string literals were updated for internal
  consistency (see Decisions made) even though no test behavior depended on them — flagging in case
  the manager prefers a narrower diff that leaves test-double message strings untouched when nothing
  asserts on them.
