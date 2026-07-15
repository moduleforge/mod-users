# Migrate Apps Oidc Handlers

## Purpose and scope

Migrate the ad-hoc top-level `error.code` string literals in the apps/OIDC handler group onto the
reserved core vocabulary, moving finer distinctions into namespaced `details[].code` per the design
doc's mapping table. Scope is the nested-envelope `server.Error(...)` call sites in three files:

- `api/internal/handlers/apps.go`
- `api/internal/handlers/oidc_providers.go`
- `api/internal/handlers/oidc_config.go`

This group is homogeneous — only `bad_request` and `validation_error` codes appear here. Runs after
Phase 1 (`apiresp` foundation) has landed. **Parallel-eligible** with Phase 2 task 002 (disjoint
files).

No standard skill covers this; see `## Procedure`.

## Requirements

Apply the design doc's [module-specific extension codes] mapping to every `server.Error(...)` literal
in the three files:

1. **`bad_request` → `invalid_input`** (top-level). ~15 sites across the three files (dominant in
   `apps.go` and `oidc_providers.go`). The top-level reserved code becomes `invalid_input`; the HTTP
   status stays `400`. No `details` unless a specific field distinction exists.

2. **`validation_error` → `invalid_input`** + per-field `details[]`. ~3 sites in `apps.go`
   (lines ~60, 64, 300). The top-level code becomes `invalid_input`; the specific validation
   distinction moves into `details[]` as `{field: "<input>", code: "users.<rule>", message: …}`.
   Choose a `users.<rule>` detail code that names the failed rule (e.g. `users.name_required`,
   `users.name_too_long`) based on the message at each site. Where the failure is not naturally
   field-bound, a single `details[]` entry with an appropriate `users.<rule>` code and best-effort
   `field` is acceptable.

3. **Leave already-reserved codes unchanged.** `internal_error`, `not_found`, `forbidden`,
   `conflict`, `invalid_input` already conform — do not alter their top-level code. (If Phase 1 chose
   to migrate `server.Error` callers directly onto `apiresp`, follow that same call form here for
   consistency; otherwise keep using `server.Error` with the corrected code string.)

4. **Update the matching tests.** `apps` tests (if present), `oidc_providers_test.go`, and
   `oidc_config_test.go` assert on the old codes — update every assertion to the new top-level code
   and, where applicable, to assert the `details[].code`.

Do **not** touch the flat-envelope sites listed in the plan overview's "Open scope question" (none are
in these three files, but do not introduce new ones).

## Validation

- `cd api && go build ./...` and `make test.unit` pass.
- `make lint` passes.
- `grep -rn '"bad_request"' api/internal/handlers/apps.go api/internal/handlers/oidc_providers.go api/internal/handlers/oidc_config.go`
  returns **no** non-test matches.
- `grep -rn '"validation_error"' api/internal/handlers/apps.go` returns **no** non-test matches.
- Every migrated `validation_error` site now emits top-level `invalid_input` with a `users.<rule>`
  `details[]` entry.
- Test assertions in the three files' `_test.go` counterparts reference only reserved top-level codes
  (plus `users.*` detail codes).

## Metadata

architectural_impact: true

## Assumptions

- **Wave 0 is merged** and Phase 1 (`adopt-apiresp-sentinels-and-writer`) has landed, so the
  `apiresp`-backed writer/vocabulary is in place.
- The `bad_request` and `validation_error` string counts are approximate (research counted ~8
  `bad_request` + ~3 `validation_error` in `apps.go`, ~6 `bad_request` in `oidc_providers.go`, ~1
  `bad_request` in `oidc_config.go`); re-grep at task start for the authoritative set.

## References

- `docs/mf-standards/architecture/api-response-design.md` — "Reserved core codes" and
  "Module-specific extension codes" mapping table.
- Phase 1 task `001-adopt-apiresp-sentinels-and-writer.md` — establishes the writer/vocabulary this
  task migrates call sites onto.

## Procedure

1. Grep the three files for `server.Error(` and enumerate the literal codes.
2. Replace `bad_request` → `invalid_input` at each site.
3. Convert each `validation_error` site to `invalid_input` + a `users.<rule>` `details[]` entry.
4. Update the corresponding `_test.go` assertions.
5. Build, test, lint.

## Checkpoint hints

- After `apps.go` + its tests.
- After `oidc_providers.go` + `oidc_config.go` + their tests.
