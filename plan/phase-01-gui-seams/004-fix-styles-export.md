# Remove the Dangling ./styles.css Package Export

## Purpose and scope

`gui/package.json` exports `./styles.css` pointing at `./dist/index.css`, which the `tsup`-only build never produces. Per [`../notes/seam-design.md`](../notes/seam-design.md) (Styles export) remove the export instead of building a stylesheet: this library's documented model is that consumers `@source`-scan `dist/`, and the workbench stylesheet (`gui/.ladle/styles.css`) contains a `body`/`@layer base` rule and a full token duplicate that would clash with core-gui's CSS. Scope: `gui/package.json` only (plus the one-line doc sentence below). Parallel-eligible with the other phase-1 tasks.

## Requirements

1. Remove the `"./styles.css": "./dist/index.css"` entry from `gui/package.json`'s `exports` (keep the `.` entry and valid JSON).
2. Confirm nothing in this repo imports `@moduleforge/users-gui/styles.css` (`grep -rn "users-gui/styles" .` excluding `node_modules`); if a hit exists, halt and report instead of editing it.
3. Do not add a CSS build step, and do not touch `.ladle/styles.css`.
4. Docs wording is owned by the phase-2 guide and phase-3 architecture task; no doc edit here.

## Validation

- `node -e "JSON.parse(require('fs').readFileSync('gui/package.json','utf8'))"` succeeds and `grep -n styles gui/package.json` finds no `./styles.css` export.
- `cd gui && bun run build` succeeds (needs `mod-core/gui` built) and `ls gui/dist` still contains `index.mjs`, `index.js`, `index.d.ts`.
- `git diff --stat` shows only `gui/package.json`.

## Metadata

architectural_impact: false

## References

- [`../notes/seam-design.md`](../notes/seam-design.md), `docs/architecture.md` (line stating the build emits no CSS)

## Status

- Outcome: succeeded (2026-10-04)
- Removed the `./styles.css` export from `gui/package.json`; no source imports `users-gui/styles` (only plan notes mention it).
- Validation: package.json parses and has no styles export; `bun run build` succeeds and `gui/dist` contains `index.mjs`, `index.js`, `index.d.ts`; diff touches only `gui/package.json`.
