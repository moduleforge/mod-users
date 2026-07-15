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
