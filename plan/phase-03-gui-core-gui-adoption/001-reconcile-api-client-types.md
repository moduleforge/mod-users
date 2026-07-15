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
