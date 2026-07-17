# Centralize literal `server.Error` sites onto `apiresp.WriteError`

## Purpose and scope

Complete the `mod-users` half of the API-response centralization goal by collapsing
the remaining literal `server.Error(status, "code", message)` /
`server.ErrorWithDetails(...)` call sites in `api/internal/handlers/` onto
sentinel-driven `apiresp.WriteError(w, r, err)` calls, so the status/code/envelope
decision lives in one place (the shared `mod-core/api/apiresp` package) rather than
being duplicated as literals at each handler site. This closes followup **`8iRl`**
("Literal server.Error sites not centralized", tag `go-vocab-migration`) — the piece
the prior `users-apiresp-migration` plan (phases 1–2) deliberately deferred.

This is a **mechanical-but-careful refactor of the pattern already established** in
that plan's Phase 1 (which collapsed the three sentinel-classifying helpers —
`writeServiceError`, `writeAuthzError`, `writeCoreServiceErr` — onto
`apiresp.WriteError`). It is **not** new architecture: the vocabulary at every site is
already correct (Phase 2 migrated the codes); what remains is routing each site through
the single classification point instead of re-stating status+code inline.

### In scope

The literal `server.Error`/`server.ErrorWithDetails` sites in exactly these seven
files (96 sites total — verified against source; the followup's "~28" estimate is
stale, the file list is not): `apps.go`, `oidc_providers.go`, `oidc_config.go`,
`identities.go`, `self.go`, `assume.go`, `user_accounts.go`.

The full per-site categorized inventory, the transform rules per category, and the
exact carve-out sites are in the
[server.Error site inventory](./notes/server-error-site-inventory.md).

### Out of scope (do not touch)

- `mod-core/api/apiresp` itself (cross-repo — followup `ZVum`).
- The GUI / TypeScript side.
- The flat-envelope sites deferred by followup `eiF8`: `writeStepUpRequired` /
  `writeLastIdentityError` and their call sites, `require_verified.go`,
  `require_confirmed.go`.
- `api/internal/handlers/auth/*.go` — the separately-tracked `biPE` scope gap.
- The Phase-1 shared helpers and `server/respond.go` (kept as-is).

## Current status

Planning complete. Single phase, four parallel-eligible implementation tasks split by
handler file to keep each a coherent, reviewable unit with no shared-file contention.
Ready for dispatch.

## Overview

### The behavior change this migration makes (load-bearing — flagged)

`apiresp.WriteError` sets the top-level `error.message` from `apiresp`'s fixed,
generic, per-code `publicMessage` (e.g. `invalid_input` → "one or more fields are
invalid"; `internal_error` → "an internal error occurred", with the specific text
**logged** server-side, not returned). So collapsing a literal site **replaces its
bespoke top-level message with the generic per-code message.**

This is exactly what the design doc's "Go-layer ownership" section mandates
(`Message: publicMessage(err, code)`), and it **does not break the machine-readable
client contract**: `error.code`, HTTP `status`, and `error.details[]` — the fields the
GUI's `ApiRequestError` branches on — are all preserved. Only human-readable display
text genericizes. Handler tests assert on status codes and `error.code`/`details`, not
top-level message strings, so test breakage is minimal.

The migration **preserves all existing `details[]`** (via `apiresp.InvalidInput(...)`)
and does **not** invent new detail codes to rescue dropped messages — that would be a
per-site UX judgment beyond a mechanical collapse. A short list of genuinely useful
messages that genericize (an OIDC id-format hint, a loopback-only hint) is flagged for
a possible follow-up in the inventory note's "Deferred" section.

### The carve-out (constraint from followup `ZVum`)

`apiresp` exposes only `InvalidInput(...)` as a public detail-carrying constructor.
Any non-`invalid_input` site that must carry a `details[]` entry — or a `conflict`
whose actionable message must survive — cannot be expressed through
`apiresp.WriteError`. **Four sites** stay as literal `server.Error`/
`server.ErrorWithDetails` with a documented justification comment mirroring the
existing `writeServiceError`/`svc.ErrEmailTaken` precedent: `oidc_providers.go:218`
(conflict), `identities.go:305` (not_found + detail), `identities.go:379` and `:391`
(unauthenticated + detail). This broadens `ZVum` (originally conflict-only) to the
not_found+details and unauthenticated+details cases too — noted for the fast-follow on
`mod-core`.

### Success criteria

- Every in-scope literal `server.Error`/`server.ErrorWithDetails` site except the four
  documented carve-outs is replaced by `apiresp.WriteError(w, r, err)` with the
  matching sentinel (Category-3 detail sites via `apiresp.InvalidInput(...)`).
- HTTP status codes and machine-readable `error.code`/`details` are unchanged at every
  migrated site; only top-level message text genericizes.
- The four carve-out sites remain, each with a justification comment referencing `ZVum`
  and the apiresp constructor gap.
- Redundant preceding `slog.ErrorContext` calls at 500 sites are removed (WriteError
  logs all 5xx with request context); the op label / distinct structured context is
  folded into the error passed to `WriteError`.
- `make build.api` and `make test.unit` pass; any test asserting an old bespoke
  message is updated to the generic message (codes/status assertions are unchanged).

### Phase and tasks

**Phase 01 — Centralize server.Error sites** (four parallel-eligible tasks, split by
handler file; no two tasks touch the same file):

1. `migrate-apps-handler` — `apps.go` (27 sites; 0 carve-outs).
2. `migrate-oidc-handlers` — `oidc_providers.go` + `oidc_config.go` (33 sites;
   1 carve-out at `oidc_providers.go:218`).
3. `migrate-identities-handler` — `identities.go` (23 sites; 3 carve-outs at 305/379/391).
4. `migrate-self-assume-account-handlers` — `self.go` + `assume.go` +
   `user_accounts.go` (13 sites; 0 carve-outs).
</content>
