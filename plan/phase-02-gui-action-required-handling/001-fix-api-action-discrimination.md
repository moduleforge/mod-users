# Fix Api Action Discrimination

## Purpose and scope

Fix the error/action discrimination bug in `gui/src/lib/api.ts`'s `request()` helper and add
first-class support for the action-required response kind defined by the finalized contract
(`docs/mf-standards/architecture/api-response-design.md`). Deliverables: local `ApiAction` /
`ApiActionResponse` wire types, a dedicated `ApiActionRequiredError` class, and a fix to
`request()`'s non-2xx handling so a top-level `action` member short-circuits error classification.
Scope is limited to `gui/src/lib/api.ts` — this task does not touch `gui/src/lib/auth-context.tsx`
(a separate, dependent task) and does not build any new navigation UI screens. No dedicated skill
covers this change; implement directly per the requirements below, matching the design doc's TS
sketch verbatim in shape.

## Requirements

1. Define local `ApiAction` / `ApiActionResponse` wire types in `gui/src/lib/api.ts`, mirroring the
   design doc's "Action-required: navigate, don't alarm" TS sketch:
   - `ApiAction`: `{ code: string; message: string; path: string; data?: Record<string, unknown> }`
   - `ApiActionResponse`: `{ action: ApiAction }`
   These are **not** imported from `@moduleforge/core-gui` — core-gui does not export them yet (Wave
   0 was Go-only). Add a code comment flagging them as a future `@moduleforge/core-gui` promotion
   candidate, following the same originate-locally-then-promote precedent the `ApiError` /
   `ApiErrorResponse` types went through (see the file's existing top-of-file comment block).
2. Define a dedicated `ApiActionRequiredError` class (extends `Error`, parallel to the existing
   `ApiRequestError` import), carrying `code`, `message`, `path`, and optional `data`. Consider also
   carrying the HTTP `status` for symmetry with `ApiRequestError`, unless that meaningfully
   complicates the shape — use judgment; `code`/`message`/`path`/`data` are the fields the design
   doc's decision D8 requires.
3. Fix `request()`'s non-2xx handling (the existing 401 special-case above it is unaffected and must
   not change): parse the response body once, then inspect the **top-level member** before
   classifying:
   - If the body has a top-level `action` member (matches `ApiActionResponse`), construct and throw
     `ApiActionRequiredError` from it. This is a short-circuit: a 403/409/503 body carrying `action`
     MUST NEVER be thrown as `ApiRequestError` and MUST NEVER reach the existing error-body branch.
   - Otherwise, fall through to the existing error-body handling (today's `errorBody.error` branch),
     unchanged in behavior for genuine error bodies.
   - This also resolves the latent bug the phase description calls out: the current code assumes a
     truthy `error` member is always an object (`errorBody.error.code`), which breaks silently for
     any endpoint emitting a flat string `error` (as the three deferred mod-users backend sites did
     before their Phase 1 migration). Correcting the discrimination order removes this failure mode
     for the sites that now emit `action` instead of a flat `error` string.
4. Add a defense-in-depth same-origin-relative guard on `action.path` before it is attached to the
   thrown error: reject (and fall back to a safe default route, e.g. `/`) any path that is not a
   single leading `/` not followed by another `/` or a backslash — i.e. reject empty strings,
   absolute URLs with a scheme, `//host/...`-style protocol-relative authorities, and
   `/\host/...`-style backslash tricks some browsers normalize as protocol-relative. This mirrors the
   server-side guard the design doc describes (`WriteActionRequired` panics on a non-relative path);
   the client must not trust the server response unconditionally.
5. Do not change the public signature or calling convention of `request<T>()`, nor of the existing
   `auth`/`self`/`userAccounts`/`audit`/`apps` client methods — this task changes only what
   `request()` throws for a non-2xx body carrying `action`.
6. Export the new `ApiAction`, `ApiActionResponse`, and `ApiActionRequiredError` symbols so
   `auth-context.tsx` (the next task) and any external consumer can import them.
7. Record a follow-up (via the `followups_add` flow-mcp tool, `type:enhancement`) noting that
   `ApiAction`/`ApiActionResponse`/`ApiActionRequiredError` in `gui/src/lib/api.ts` are strong
   candidates for future promotion to `@moduleforge/core-gui`, parallel to how `ApiError` /
   `ApiErrorResponse` / `ApiRequestError` were promoted. Reference `gui/src/lib/api.ts` by path in the
   follow-up text; do not reference plan/phase/task names (they will not exist once this plan closes).

## Validation

- `cd gui && bun run typecheck` — requires the yalc `@moduleforge/core-gui` link per `AGENTS.md`
  First-time setup step 4. If the link is not already present in this worktree, set it up first
  rather than skipping typecheck.
- `make lint.gui` (or the equivalent `gui/package.json` lint script) — read-only lint pass.
- `make build.gui` to confirm the module still builds.
- Verify the current gui/ test-infrastructure state before attempting `bun test`: follow-up `KXNZ`
  recorded (2026-07-16) that `gui/` has no test runner set up at all (no test files, no
  `bunfig.toml`, no bun-types). Confirm whether that is still true in this worktree.
  - If a test runner now exists, add unit tests for the new `request()` discrimination logic: a
    403/409/503 body with a top-level `action` throws `ApiActionRequiredError` with the right
    fields; a 403/409 body with a top-level `error` object still throws `ApiRequestError` exactly as
    before (regression check); the same-origin-relative guard rejects the documented attack shapes
    and falls back to the safe default.
  - If no test runner exists, do not stand one up as part of this task — that is out of scope and
    already tracked by `KXNZ`. Rely on typecheck/lint/build for validation, and do not add a
    duplicate follow-up for the same gap.
- Manual check: grep `gui/src/lib/api.ts` to confirm `ApiActionRequiredError`, `ApiAction`, and
  `ApiActionResponse` are all exported.
- Confirm the follow-up from Requirement 7 was recorded exactly once (`followups_list`, or inspect
  `plan/followups.yaml` in this worktree), tagged `type:enhancement`, with no plan/phase/task-name
  references in its text.

## Metadata

architectural_impact: false

## Assumptions

- The finalized contract (`docs/mf-standards/architecture/api-response-design.md`, "Action-required
  responses" and "Action-required: navigate, don't alarm" sections) is authoritative for field
  shapes and the discrimination rule; do not re-derive it from the Go side, and do not wait on
  Phase 1 (`go-action-required-migration`) — this task depends only on the finalized wire contract,
  not on that phase's build output (the two phases are parallel-eligible).
- `@moduleforge/core-gui`'s exported `ApiError`/`ApiErrorResponse`/`FieldErrorData`/`ApiRequestError`
  types and the existing re-export comment block at the top of `gui/src/lib/api.ts` are unaffected by
  this task; the new action types/class are defined locally (not imported), per decision D9 recorded
  in `plan/notes/action-path-values-and-decisions.md`.

## References

- `docs/mf-standards/architecture/api-response-design.md` — "Action-required responses",
  "Action-code vocabulary", "Action-required status set", and "Action-required: navigate, don't
  alarm" sections; the canonical field shapes and discrimination rule.
- `plan/notes/action-path-values-and-decisions.md` — D1, D8, D9 (client error-class decision,
  local-types decision) and the resolved `action.path` values (`/verify-email`, `/step-up`,
  `/oidc-config`).
- `gui/src/lib/api.ts` — the file this task modifies; the existing `request()` helper and the
  top-of-file comment describing the core-gui type re-export convention.
- `AGENTS.md` First-time setup step 4 — the yalc link required for `gui/` typecheck/build.
