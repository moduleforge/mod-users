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
