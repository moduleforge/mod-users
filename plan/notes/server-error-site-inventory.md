# Literal `server.Error` site inventory (verified against current source)

Verified against `api/internal/handlers/` on branch `plan/centralize-server-error`
(commit state as of 2026-07-17). The followup `8iRl` estimated "~28" sites; the
**actual count is 96** literal `server.Error(...)` / `server.ErrorWithDetails(...)`
call sites across the seven target files. The count is stale, not the file list.

Per-file totals: `apps.go` 27 · `oidc_providers.go` 20 · `oidc_config.go` 13 ·
`identities.go` 23 · `self.go` 6 · `assume.go` 3 · `user_accounts.go` 4.

## What "centralize" means here (and what it changes on the wire)

`server.Error(w, status, code, message)` and `server.ErrorWithDetails(...)` are thin
wrappers (`api/internal/server/respond.go`) that hand-write an `apiresp.Envelope`
with a **caller-supplied top-level `message`**. The design end-state
(`docs/mf-standards/architecture/api-response-design.md`, "Go-layer ownership") is
`apiresp.WriteError(w, r, err)`, which derives `status` + `code` from sentinel
classification (`errors.Is`) and sets the top-level `message` from `apiresp`'s
**unexported `publicMessage(code)`** — a fixed, generic, per-code string.

Consequence, load-bearing: collapsing a literal site onto `apiresp.WriteError`
**replaces the site's bespoke top-level `message` with the generic per-code message**:

| code | generic `publicMessage` the client will receive |
|---|---|
| `invalid_input` | "one or more fields are invalid" |
| `not_found` | "the requested resource was not found" |
| `unauthenticated` | "authentication is required" |
| `forbidden` | "you do not have access to this resource" |
| `conflict` | "the request conflicts with the current state" |
| `internal_error` | "an internal error occurred" (bespoke text is **logged**, not returned) |

This does **not** break the machine-readable client contract: `error.code`,
HTTP `status`, and `error.details[]` (the fields the GUI/`ApiRequestError` branch on)
are all preserved. Only human-readable **display text** genericizes. Handler tests
assert on `rr.Code` (status) and `error.code`/`details`, not on top-level message
text (spot-checked `oidc_providers_test.go`, `user_accounts_authz_test.go`), so test
breakage is minimal.

For `internal_error` (500) this is a strict improvement and exactly what the design
mandates (generic body message, specific detail logged server-side; `WriteError`
already logs all 5xx via `logServerError` with request context).

## Site categories

### Category 1 — `internal_error` 500 (clean collapse; message → logged)
Replace `server.Error(w, 500, "internal_error", "<msg>")` with
`apiresp.WriteError(w, r, err)` where `err` is the in-scope underlying error, wrapped
with the op label so the single server-side log line stays useful, e.g.
`apiresp.WriteError(w, r, fmt.Errorf("apps.Create: %w", err))`. Where no error value
is in scope (a static "failed to X"), construct one: `errors.New("<msg>")`.
Any **immediately-preceding `slog.ErrorContext` that logs only this same failure**
becomes redundant (WriteError logs all 5xx) — remove it, folding any distinct
structured context (`"id", id`, op label) into the wrapped error instead.

### Category 2 — 4xx sentinel, no details (message genericizes)
Replace with `apiresp.WriteError(w, r, <sentinel>)` using the matching sentinel:
`apiresp.ErrInvalidInput` / `ErrNotFound` / `ErrUnauthenticated` / `ErrForbidden`.
The bespoke top-level message is dropped in favour of the generic one. Do **not**
invent new `details[]` codes to rescue messages in this plan (see "Deferred").

### Category 3 — `invalid_input` WITH details (fully supported)
Replace `server.ErrorWithDetails(w, 400, "invalid_input", msg, details)` with
`apiresp.WriteError(w, r, apiresp.InvalidInput(details...))`. `InvalidInput` is the
one public detail-carrying constructor; it round-trips the exact `FieldError` set.
Top-level message genericizes; `details[]` (the field-bound specificity) is preserved
byte-for-byte.

### Category 4 — CARVE-OUT: detail/message that `apiresp` has no public constructor for
`apiresp` exposes **only** `InvalidInput(...)` as a public detail-carrying
constructor (followup `ZVum`). Any non-`invalid_input` site that must carry a
`details[]` entry — or a `conflict` whose actionable message must survive — cannot be
expressed through `apiresp.WriteError`. These stay as `server.Error`/
`server.ErrorWithDetails` with a **documented justification comment** referencing
`ZVum` and the constructor gap, mirroring the existing `writeServiceError` /
`svc.ErrEmailTaken` precedent (`handlers/user_accounts.go:56`). This broadens `ZVum`
(originally a conflict-only observation) to `not_found`+details and
`unauthenticated`+details as well.

The four carve-out sites:
- `oidc_providers.go:218` — `conflict` "provider id already exists; use PUT to update".
  Actionable guidance; preserve (do not genericize). ZVum gap.
- `identities.go:305` — `not_found` + `users.identity_not_found` detail.
- `identities.go:379` — `unauthenticated` + `users.bad_credentials` (current_password required).
- `identities.go:391` — `unauthenticated` + `users.bad_credentials` (current password incorrect).

## Per-file site list (line : category)

### apps.go (27) — 0 carve-outs
- 55 C2 invalid_input "invalid JSON body"
- 61 C3 invalid_input + slug_required
- 67 C3 invalid_input + name_required
- 101 C1 500 "failed to create app"
- 132 C1 500 "failed to list apps"
- 179 C2 invalid_input "invalid JSON body"
- 227 C1 500 "failed to update app"
- 268 C1 500 "failed to archive app"
- 301 C2 invalid_input "invalid JSON body"
- 305 C3 invalid_input + user_uuid_required
- 313 C2 invalid_input "invalid user_uuid"
- 319 C2 not_found "user account not found"
- 324 C1 500 "failed to load user account"
- 339 C1 500 "failed to assign user account to app"
- 366 C1 500 "failed to list app user accounts"
- 396 C2 invalid_input "invalid user uuid"
- 402 C2 not_found "user account not found"
- 407 C1 500 "failed to load user account"
- 416 C1 500 "failed to remove user account from app"
- 443 C2 invalid_input "invalid user uuid"
- 449 C2 not_found "user account not found"
- 454 C1 500 "failed to load user account"
- 460 C2 invalid_input "invalid JSON body"
- 473 C1 500 "failed to update roles"
- 489 C2 invalid_input "invalid uuid"
- 494 C2 not_found "app not found"
- 499 C1 500 "failed to load app"

### oidc_providers.go (20) — carve-out: 218
- 133 C2 not_found "unknown provider id"
- 139 C1 500 "failed to load provider"
- 143 C2 not_found "provider not found"
- 164 C2 invalid_input "invalid JSON body"
- 172 C2 invalid_input "invalid provider id format"
- 190 C2 invalid_input "invalid JSON body"
- 199 C2 invalid_input "id is required"
- 207 C2 invalid_input "id must be 2-32 chars, ..." (useful hint — see Deferred)
- 218 **C4 conflict** "provider id already exists; use PUT to update"
- 222 C1 500 "failed to check provider"
- 239 C2 invalid_input "invalid provider id format"
- 245 C1 500 "failed to delete provider"
- 251 C1 500 "failed to reload providers"
- 273 C1 500 "failed to load provider"
- 299 C1 500 "failed to persist provider"
- 305 C1 500 "failed to reload providers"
- 313 C1 500 "persisted but failed to respond"
- 318 C1 500 "provider disappeared after write"
- 407 C1 500 "failed to authorize request"
- 411 C2 unauthenticated "admin session or setup token required"

### oidc_config.go (13) — 0 carve-outs
- 188 C1 500 "failed to load provider state"
- 234 C2 forbidden "setup token endpoint is loopback-only" (useful hint — see Deferred)
- 242 C2 not_found "setup already confirmed"
- 250 C2 not_found "no active setup token"
- 287 C2 invalid_input "invalid JSON body"
- 294 C1 500 "failed to authorize request"
- 298 C2 unauthenticated "setup token or admin session required"
- 326 C1 500 "failed to persist configuration"
- 333 C1 500 "failed to persist configuration"
- 344 C1 500 "failed to reload providers"
- 349 C1 500 "failed to reload providers"
- 356 C1 500 "failed to recompute state"
- 434 C1 500 "failed to load saved config"

### identities.go (23) — carve-outs: 305, 379, 391
- 149 C1 500 "failed to load credentials"
- 157 C1 500 "failed to load identities"
- 216 C2 not_found "unknown provider"
- 220 C2 invalid_input err.Error() (dynamic text; genericizes — currently surfaces raw err)
- 248 C2 invalid_input "invalid identity UUID"
- 305 **C4 not_found** + users.identity_not_found
- 311 C1 500 "failed to unlink identity"
- 348 C2 invalid_input "invalid JSON body"
- 353 C2 invalid_input "new_password is required"
- 357 C3 invalid_input + password_too_short
- 370 C1 500 "failed to load credentials"
- 379 **C4 unauthenticated** + users.bad_credentials (current_password required)
- 387 C1 500 "failed to verify current password"
- 391 **C4 unauthenticated** + users.bad_credentials (current password incorrect)
- 401 C1 500 "failed to process password"
- 418 C1 500 "failed to save password"
- 472 C1 500 "failed to remove password"
- 573 C2 invalid_input "invalid JSON body"
- 577 C2 invalid_input "code is required"
- 586 C2 unauthenticated "invalid or expired code"
- 591 C1 500 "failed to look up code"
- 596 C2 unauthenticated "invalid or expired code"
- 608 C1 500 "failed to issue step-up token"

### self.go (6) — 0 carve-outs
- 35 C1 500 "failed to load user account"
- 41 C1 500 "failed to load entity"
- 61 C2 invalid_input "invalid JSON body"
- 67 C1 500 "failed to load user account"
- 75 C1 500 "failed to resolve entity"
- 93 C1 500 "failed to reload entity"

### assume.go (3) — 0 carve-outs
- 44 C2 invalid_input "invalid uuid"
- 51 C2 not_found "user account not found"
- 62 C1 500 "failed to issue token"

### user_accounts.go (4) — 0 carve-outs
- 84 C2 invalid_input "invalid JSON body"
- 160 C2 not_found "user account not found"
- 195 C2 invalid_input "invalid JSON body"
- 270 C2 invalid_input "invalid uuid"

## Explicitly OUT of scope (do not touch)

- `writeStepUpRequired` / `writeLastIdentityError` (flat-envelope helpers) and the
  sites that call them — followup `eiF8` (`identities.go` step_up_required/last_identity).
- `api/internal/auth/require_verified.go`, `require_confirmed.go` — followup `eiF8`.
- `api/internal/handlers/auth/*.go` (`register.go`, `login.go`, `emailcode.go`,
  `anonymous.go`, `oidc.go`, `reset.go`) — the separately-tracked `biPE` scope gap;
  not part of `8iRl`'s seven files.
- The shared helpers `writeServiceError`, `writeAuthzError`, `writeCoreServiceErr`
  (already collapsed onto `apiresp.WriteError` in Phase 1) and `server/respond.go` —
  keep them; this plan only migrates the literal call sites that still bypass them.
- `mod-core/api/apiresp` itself — cross-repo, off limits (`ZVum`).

## Deferred (flag to manager; NOT done in this plan)

Genericizing drops a few genuinely useful, actionable top-level messages. The
design's mechanism to keep such specificity is a namespaced `details[].code`, but
adding new detail codes is a per-site UX judgment and (for non-`invalid_input`
codes) is blocked by the `ZVum` constructor gap — both beyond a mechanical collapse.
Recommend a separate follow-up (or a manager decision to keep the message) for:
`oidc_providers.go:207` (id format rule) and `oidc_config.go:234`
(loopback-only) — plus the `oidc_providers.go:218` conflict hint, preserved here as a
carve-out. The plain "X is required" hints (`199`, `353`, `577`) genericize.
</content>
