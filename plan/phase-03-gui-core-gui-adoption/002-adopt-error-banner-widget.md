# Adopt Error Banner Widget

## Purpose and scope

Remove the cross-module GUI coupling in `gui/src/components/error-message.tsx`, which currently
imports `@moduleforge/core-gui`'s low-level `Alert`/`AlertDescription` UI primitives directly — in
tension with the ecosystem's "no module GUI imports another module's low-level GUI primitive" rule.
Wave 0 promotes a proper `<ErrorBanner>` widget into `@moduleforge/core-gui` as the sanctioned
promotion target. Covers user-request item 5.

Depends only on Wave 0 (independent of the Go phases). **Parallel-eligible** with Phase 3 task 001
(disjoint file).

No standard skill covers this; see `## Procedure`.

## Requirements

1. **Switch `ErrorMessage` to consume `<ErrorBanner>`.** Replace the direct
   `import { Alert, AlertDescription } from '@moduleforge/core-gui'` and the local `Alert`/
   `AlertDescription`/`AlertCircle` markup with `@moduleforge/core-gui`'s `<ErrorBanner>` widget.
   Preserve the current `ErrorMessage({ message }: { message: string | null })` behavior (renders
   nothing when `message` is null; otherwise renders a destructive banner with the message).
   **Alternative (implementer's judgment):** retire `ErrorMessage` entirely and switch its call sites
   to use `<ErrorBanner>` directly. Either is acceptable, **but the direct `Alert`/`AlertDescription`
   import must be removed.**

2. **Update call sites if retiring `ErrorMessage`.** If you retire the component, update every importer
   of `ErrorMessage` to use `<ErrorBanner>` (grep for `ErrorMessage` across `gui/src`). If you keep
   `ErrorMessage` as a thin wrapper, no call-site changes are needed.

## Validation

- `grep -rn "Alert\|AlertDescription" gui/src/components/error-message.tsx` returns **no** direct
  low-level Alert import (the file uses `<ErrorBanner>` instead), or the file is deleted.
- If `ErrorMessage` is retired: `grep -rn "ErrorMessage" gui/src` returns no unresolved references
  (all call sites migrated), and the export is removed from the package barrel if applicable.
- `make build.gui` and `make lint.gui` pass (both require the yalc link).
- `cd gui && bun test` passes.
- Visual/behavioral parity: a non-null `message` still renders a destructive banner; a null `message`
  renders nothing.

## Metadata

architectural_impact: true

## Assumptions

- **Wave 0 is merged** and `@moduleforge/core-gui` exports `<ErrorBanner>` (the promotion of
  `mod-users`' `ErrorMessage`, wrapping the shared `Alert` `destructive` variant) per the design doc's
  "Widget set implied" section.
- **yalc link required** (same setup as Phase 3 task 001): `gui/` resolves `@moduleforge/core-gui` via
  a gitignored `file:.yalc/` link that must be populated in the worktree before building/typechecking
  (`AGENTS.md` First-time setup step 4 / "Working in worktrees"; `.claude/CLAUDE.md` known gotcha).

## References

- `docs/mf-standards/architecture/api-response-design.md` — "GUI-facing error-data contract" and
  "Widget set implied" (`<ErrorBanner>` as the `mod-core/gui` promotion of `ErrorMessage`).
- `gui/src/components/error-message.tsx` — the component being migrated.
- `AGENTS.md` / `.claude/CLAUDE.md` — yalc setup.

## Procedure

1. Set up the yalc link.
2. Replace the direct `Alert`/`AlertDescription` usage with `<ErrorBanner>` (or retire `ErrorMessage`
   and migrate call sites).
3. Build, test, lint `gui/`.

## Status

- **Outcome:** succeeded (2026-07-16).
- Chose the thin-wrapper alternative from Requirement 1: `ErrorMessage` now delegates to
  `@moduleforge/core-gui`'s `<ErrorBanner>` (`error={message}`, `ErrorBannerData` accepts a plain
  `string`), so no call sites needed migration (Requirement 2 not triggered).
- Direct `Alert`/`AlertDescription`/`AlertCircle` import and markup removed from
  `gui/src/components/error-message.tsx`.
- Validation: `grep -rn "Alert\|AlertDescription" gui/src/components/error-message.tsx` — no match
  (passed). `grep -rn "ErrorMessage" gui/src` — all references still resolve to the retained wrapper
  and its call sites (passed; retirement branch not applicable). `make build.gui` — passed. `make
  lint.gui` (`tsc --noEmit`) — passed. Visual/behavioral parity (null → renders nothing, non-null →
  destructive banner with message) — preserved by construction via `<ErrorBanner error={message} />`
  and confirmed by reading `ErrorBanner`'s documented contract in
  `gui/.yalc/@moduleforge/core-gui/dist/index.d.ts`.
- `cd gui && bun test` — **not-applicable**, not passed. Confirmed by diffing against the unmodified
  worktree/main checkout that the entire `gui/` package has zero test files and zero test
  infrastructure (no `bunfig.toml`, no `bun-types`/`@types/bun`, no Testing Library) — `bun test`
  exits 1 with "0 test files matching" both before and after this task's change, independent of
  anything this task touched. A first-pass fix (adding a minimal `error-message.test.tsx` using
  `bun:test` + `react-dom/server`) typechecked-failed on the missing `bun:test` ambient types, which
  would require adding a new devDependency (`bun-types`) — a repo-wide test-infra bootstrap outside
  this task's `## Requirements`/`## Procedure`. Reverted that test file rather than expand scope
  unilaterally; flagged for the manager below.
- Assumptions applied: both `## Assumptions` entries (Wave 0 merged with `<ErrorBanner>` exported;
  yalc link required) held as stated.
- Flagged for manager: `gui/` (and apparently the whole repo, per a scan for `bun:test`/`bun-types`
  usage) has no test runner set up yet, so the task doc's `cd gui && bun test` validation bullet is
  currently unsatisfiable for any `gui/` task without first standing up test infrastructure
  (`bun-types` devDependency at minimum). Recommend either a follow-up task to establish minimal
  `gui/` test infra, or amending this validation template bullet until that infra exists.
