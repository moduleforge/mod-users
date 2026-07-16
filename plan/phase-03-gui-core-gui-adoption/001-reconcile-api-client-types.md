# Reconcile Api Client Types

## Purpose and scope

Reconcile `mod-users`' GUI API client (`gui/src/lib/api.ts`) with the canonical error/client contract
`@moduleforge/core-gui` exposes (delivered by Wave 0), eliminating the divergent local duplicates of
the wire types, and align the 401 handling code with the reserved vocabulary. Covers user-request
items 6 (client reconciliation) and 7 (the 401 throw quirk).

Depends only on Wave 0 (independent of the Go phases). **Parallel-eligible** with Phase 3 task 002
(disjoint file).

No standard skill covers this; see `## Procedure`.

## Requirements

1. **Reconcile the wire types (item 6).** `gui/src/lib/api.ts` currently defines its own
   `ApiError {code, message}`, `ApiErrorResponse {error: ApiError}`, and
   `ApiRequestError extends Error {code, status}` (no `details`). The design doc's types are a
   superset-compatible extension of these. **Preferred:** re-point the local definitions to
   import/re-export `@moduleforge/core-gui`'s canonical `FieldError`/`ApiError`/`ApiErrorResponse`/
   `ApiRequestError`, eliminating the duplication. **At minimum:** add `details?: FieldError[]` to
   `ApiError` and `ApiRequestError` locally so the shapes stay wire-compatible. Do **not** leave two
   diverging, incompatible definitions of the same wire shape. If re-exporting, ensure existing
   consumers importing these names from `@moduleforge/users-gui` keep resolving (re-export the names).

2. **Populate `details` on thrown errors.** Where `request()` parses the error envelope
   (`api.ts` ~lines 225–238), also read `errorBody.error.details` and pass it into the thrown
   `ApiRequestError` so field-level detail reaches consumers/the `useApiError` hook.

3. **401 code alignment + quirk decision (item 7).** In `request()` (~lines 217–223), the 401 branch
   currently `throw new ApiRequestError('unauthorized', 'Authentication required', 401)`
   **unconditionally**, while the token-clear-and-redirect side-effect is gated on `!skipAuthRedirect`.
   - **Change:** the synthesized top-level code `'unauthorized'` → **`'unauthenticated'`** to match the
     reserved vocabulary (`ErrUnauthenticated` → 401; the design doc's "Client contract" and
     "unauthenticated, not unauthorized, for 401" sections).
   - **Preserve the throw-always behavior — do NOT make the throw conditional on `skipAuthRedirect`.**
     Decision/rationale (recorded here): this is **intentional, relied-upon** behavior, not an
     oversight. The `skipAuthRedirect` option's own doc-comment (api.ts ~lines 24–34) states it is for
     callers that "need to handle authentication failures **itself** (e.g., the OAuth return page,
     which must redirect to a login URL that carries an `?error=...` message)" — i.e. the caller opts
     out of the redirect **precisely so it can catch the throw** and handle it. The design doc's client
     contract likewise says only that the *redirect* is skipped when `skipAuthRedirect` is set; it does
     not say the throw is suppressed. Making the throw conditional would be a behavioral change beyond
     this plan's response-shape scope and would break the OAuth-return caller. Keep the throw
     unconditional; change only the code string.

## Validation

- `cd gui && bun test` passes (yalc link in place — see Assumptions).
- `make build.gui` and `make lint.gui` pass (both require the yalc link).
- `grep -n "'unauthorized'" gui/src/lib/api.ts` returns **no** match; the 401 branch throws
  `'unauthenticated'`.
- `grep -n "details" gui/src/lib/api.ts` shows `details` is present on the client error type and
  populated from the parsed envelope.
- The throw in the 401 branch remains **outside** the `if (!skipAuthRedirect …)` block (unconditional).
- If re-exporting core-gui types: `grep -n "@moduleforge/core-gui" gui/src/lib/api.ts` confirms the
  import, and no local `interface ApiError`/`ApiErrorResponse` duplicate diverges from the canonical
  shape.

## Metadata

architectural_impact: true

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-16
- **Worktree / branch:** `phase-03-task-01-reconcile-api-client-types` (worktree
  `worktrees/phase-03-task-01-reconcile-api-client-types`)
- **Final commit:** `3533209`
- **Approach taken:** the **Preferred** path (item 1) — `gui/src/lib/api.ts` now `import`s
  `ApiError`, `ApiErrorResponse`, `FieldErrorData`, and `ApiRequestError` from
  `@moduleforge/core-gui` and re-exports them under the same names, eliminating the local
  duplicate `interface ApiError`, `interface ApiErrorResponse`, and `class ApiRequestError`.
  `src/index.ts` already re-exported these names from `./lib/api`, so no change was needed there
  for existing `@moduleforge/users-gui` consumers to keep resolving them; `FieldErrorData` is a
  new re-export (core-gui does not export a plain `FieldError` type — that name is taken by the
  `<FieldError>` component — so the canonical `FieldErrorData` name was kept as-is rather than
  aliased).
- **`request()` kept local** per the Assumptions' stated default: the users-specific
  auth/token/redirect logic in `createUsersClient`'s `request()` was not replaced with core-gui's
  shared `request()` helper; only the wire/client types were reconciled.
- **Item 2 (details):** the `!response.ok` branch in `request()` now reads
  `errorBody.error.details` into a local `errorDetails: FieldErrorData[] | undefined` and passes
  it as the 4th constructor argument to the (now core-gui) `ApiRequestError`.
- **Item 3 (401 code + quirk):** the synthesized top-level code changed from `'unauthorized'` to
  `'unauthenticated'`; the throw remains unconditional and outside the
  `if (!skipAuthRedirect …)` block, exactly as directed.
- **Validation:** `make lint.gui` (`tsc --noEmit`) and `make build.gui` both pass. All four grep
  checks pass (`'unauthorized'` — no match; `details` — present, populated from the parsed
  envelope; `@moduleforge/core-gui` — import present; no local `interface
  ApiError`/`ApiErrorResponse`/`class ApiRequestError` remain). `cd gui && bun test` was **not
  applicable**: `gui/` has no test files and no `test` script anywhere in its history (confirmed
  via `git log --all` — no `*.test.*`/`*.spec.*` file was ever added under `gui/`), so `bun test`
  fails with "0 test files matching" independent of this task's changes; making it pass would
  require adding a new devDependency (`bun-types`/`@types/bun`) and establishing the project's
  first test convention, which is outside this task's `## Requirements`. See the implementation
  report's `flagged_for_manager` for the recommendation to track this as a separate follow-up.
- **Yalc-setup housekeeping note:** the worktree's local `file:.yalc/@moduleforge/core-gui`
  dependency entries in `gui/package.json`/`bun.lock` were transiently swept into three of this
  task's commits by the Flow checkpoint/finalize scripts' `git add -A` (they run from inside the
  worktree and stage everything pending, including local yalc setup that should stay uncommitted
  per `AGENTS.md`/`.claude/CLAUDE.md`'s convention — consistent with `fa0923f`'s prior removal of
  the same entry). Each occurrence was immediately reverted in a dedicated follow-up commit
  (`c4be539`, `33eafb7`, `3533209`) and the local yalc link re-applied as an uncommitted working-tree
  change, matching the pre-task state. The final worktree state has only `bun.lock` and
  `gui/package.json`'s yalc entries uncommitted (as expected); `gui/src/lib/api.ts` and this task
  document's edits are committed at `1bb439c` / the doc-update commit.

## Assumptions

- **Wave 0 is merged** and `@moduleforge/core-gui` exports `FieldError`, `ApiError`,
  `ApiErrorResponse`, `ApiRequestError`, and the shared `request()` helper per the design doc's
  "GUI-facing error-data contract".
- **yalc link required.** Per `AGENTS.md` First-time setup step 4 and `.claude/CLAUDE.md`'s known
  gotcha: `gui/` resolves `@moduleforge/core-gui` via a `file:.yalc/` link that is gitignored and must
  be populated in the worktree before building/typechecking. Setup: from the `core-gui` package dir
  run `yalc publish`, then from `mod-users` root `cd gui && yalc add @moduleforge/core-gui && cd .. &&
  bun install`. In a worktree, copy `.yalc/` from the main checkout as AGENTS.md "Working in worktrees"
  describes.
- Consider whether to also adopt Wave 0's shared `request()` helper wholesale vs. keeping the local
  `createUsersClient` `request()` and only reconciling types. Default: keep the local `request()`
  (it carries users-specific auth/token/redirect logic) and reconcile the **types** + the 401 code;
  note the decision if you diverge.

## References

- `docs/mf-standards/architecture/api-response-design.md` — "GUI-facing error-data contract"
  (wire types, `ApiRequestError`, the 401-redirect special case), "unauthenticated, not unauthorized".
- `gui/src/lib/api.ts` — the client being reconciled.
- `AGENTS.md` / `.claude/CLAUDE.md` — yalc setup.

## Procedure

1. Set up the yalc link (Assumptions).
2. Reconcile the wire types with `@moduleforge/core-gui` (item 1) and re-export as needed.
3. Populate `details` on thrown `ApiRequestError` (item 2).
4. Change the 401 synthesized code to `'unauthenticated'`, preserving the unconditional throw (item 3).
5. Build, test, lint `gui/`.

## Checkpoint hints

- After type reconciliation compiles.
- After the 401 code change + `details` population.
