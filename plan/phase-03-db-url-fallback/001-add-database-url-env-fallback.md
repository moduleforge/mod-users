# Add Database Url Env Fallback

## Purpose and scope

Give `api/internal/config/config.go`'s `Load()` a preference-with-fallback
for the Postgres connection-string env var: read `MFAPP_DATABASE_URL` when
set, else `DB_URL` — **both supported permanently**, not as a deprecation
window (see `plan/overview.md`'s "Deferred and flagged" for why). This is
the resolved value every other part of the module (the pool `localdb.New`
builds later, and Phase 4's JWT-secret bootstrap) consumes as `cfg.DB.URL`.

No standard skill covers this; it is a small, self-contained change. Follow
the [`## Procedure`](#procedure) below.

## Requirements

1. **New resolver function** in `api/internal/config/config.go` (or a small
   new file in the same package, implementer's choice — the change is small
   enough that either is fine):

   ```go
   // resolveDBURL returns MFAPP_DATABASE_URL when set, else DB_URL. Both
   // names are supported permanently: MFAPP_DATABASE_URL is the name the
   // app-mfmanager catalog-deploy engine injects into the apps it manages;
   // DB_URL remains the name every self-hosted or non-catalog deployment
   // sets directly (including app-mfmanager's own bootstrap connection —
   // see plan/overview.md's "Key research findings"). This is not a
   // rename-in-progress: neither name is scheduled for removal.
   func resolveDBURL() string {
       if v := os.Getenv("MFAPP_DATABASE_URL"); v != "" {
           return v
       }
       if v := os.Getenv("DB_URL"); v != "" {
           slog.Debug("config: using DB_URL (MFAPP_DATABASE_URL not set)")
           return v
       }
       return ""
   }
   ```

   The `slog.Debug` line is informational only — deliberately not phrased
   as a deprecation warning (per the design note above), and at `Debug`
   level so it does not add noise to normal operator-facing logs. `slog`
   is already imported by this file (used by the SMTP-not-configured
   warning).

2. **Use it in `Load()`.** Change:

   ```go
   URL: os.Getenv("DB_URL"),
   ```

   to:

   ```go
   URL: resolveDBURL(),
   ```

   inside the `DBConfig` literal in `Load()`.

3. **Update `validate()`'s required-field label.** Change the `required`
   check-list entry:

   ```go
   {"DB_URL", cfg.DB.URL},
   ```

   to:

   ```go
   {"MFAPP_DATABASE_URL / DB_URL", cfg.DB.URL},
   ```

   This label still contains the substring `"DB_URL"`, so the existing
   `TestLoad` subtest `"missing required fields produces aggregated
   error"` (which asserts `strings.Contains(err.Error(), "DB_URL")`)
   continues to pass unmodified — confirm this rather than editing that
   test.

4. **Update doc comments.** `DBConfig.URL`'s field comment and `Load()`'s
   package-level doc comment should mention the new precedence
   (`MFAPP_DATABASE_URL` preferred, `DB_URL` fallback, both permanent).

5. **Update `.env.example`.** Its current `DB_URL` section
   (`# API reads DB_URL directly:` / `DB_URL=postgresql://...`) should
   gain a comment describing the `MFAPP_DATABASE_URL` alternative and the
   precedence, without removing the working `DB_URL=` default local-dev
   line (this repo's own `deploy/local/docker-compose.yml` still sets
   `DB_URL` for its reference stack, unchanged by this task).

## Validation

- `cd api && go build ./...` succeeds.
- `cd api && make lint` (go vet + gofmt check) passes.
- `cd api && go test ./internal/config/...` passes — every existing
  subtest in `config_test.go` continues to pass unmodified, **including**
  `"missing required fields produces aggregated error"` (per Requirement 3
  above — do not edit that test to make it pass; if it needs editing,
  the label change was done wrong).
- New unit tests added to `config_test.go` (or a new
  `config_test.go`-adjacent file) cover:
  - `MFAPP_DATABASE_URL` set (and `DB_URL` also set, to a different value)
    → `cfg.DB.URL` equals the `MFAPP_DATABASE_URL` value.
  - Only `DB_URL` set → `cfg.DB.URL` equals the `DB_URL` value (the
    fallback path).
  - Neither set → `Load()` errors, and the error mentions both
    `MFAPP_DATABASE_URL` and `DB_URL` (or at minimum the combined label
    from Requirement 3) — extends, rather than duplicates, the existing
    "missing required fields" subtest's assertion.
- `grep -n "resolveDBURL\|MFAPP_DATABASE_URL" api/internal/config/config.go .env.example` shows the new symbol/name in both files.

## Metadata

architectural_impact: false

## Assumptions

- `DB_URL` is never removed by this task or any task this plan currently
  plans — see `plan/overview.md`'s "Deferred and flagged" for the
  reasoning. Do not add a removal TODO, sunset date, or deprecation
  warning language anywhere in this task's changes.
- This task does not touch `moduleforge.module.yaml`, `deploy/`, or any
  file outside `api/internal/config/`, `api/config/` (facade, if its own
  doc comment needs a matching update), and `.env.example` — the pool
  construction (`api/internal/db/pool.go`, `api/localdb/pool.go`) already
  reads `cfg.DB.URL` (the resolved field), not the raw env var, so it
  needs no change.

## References

- `api/internal/config/config.go` — `Load()`, `validate()`, `DBConfig` —
  the file this task modifies. Read in full before starting (already read
  during planning).
- `api/internal/config/config_test.go` — existing `TestLoad` subtests,
  especially `"all required fields set succeeds..."` and `"missing
  required fields produces aggregated error"` — both must keep passing
  unmodified.
- `plan/overview.md`'s "Key research findings" section — the concrete
  evidence (`app-mfdemo`, `app-mfmanager`) behind the permanent-dual-support
  decision this task implements.
- `.env.example` (this repo's own, at the project root) — the file to
  update per Requirement 5.

## Procedure

1. Implement Requirements 1-3 in `api/internal/config/config.go`.
2. Update doc comments (Requirement 4).
3. Update `.env.example` (Requirement 5).
4. Add the new unit tests; run the full `internal/config` test suite and
   confirm every existing subtest still passes unmodified.
5. Run the Validation commands; fix and re-run until green.
6. Commit all changed files together as this task's change.
