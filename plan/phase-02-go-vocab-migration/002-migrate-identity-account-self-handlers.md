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
