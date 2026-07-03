# Phase — GUI Build Alignment

## Goals

Bring `mod-users/gui`'s build tooling into byte-for-shape alignment with the sibling tsup component libraries so `@moduleforge/users-gui` builds and packages identically. This is the foundational phase: it establishes the correct build outputs that the CI phase validates and that `app-mfdemo` consumes. Comes first because every other packaging claim (CI green, consumer styling) depends on the build producing the sibling-shaped `dist/`.

## Inputs

- Current `gui/package.json`, `gui/tsup.config.ts`, `gui/.ladle/*`, `gui/src/styles.css`.
- Sibling references (read-only): `mod-contacts/gui` (the sibling that also peer-depends on `@moduleforge/core-gui`) for `tsup.config.ts` external list and package.json dependency shape; `mod-core/gui` for `vite.config.ts` and `.ladle/styles.css` convention.
- Findings in [../notes/sibling-build-mechanism.md](../notes/sibling-build-mechanism.md) and [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md).
- The dev-time yalc link (`.yalc/@moduleforge/core-gui`) must be present locally to build/typecheck (per AGENTS.md).

## Outputs

- `gui/vite.config.ts` (new) identical to siblings — for Ladle.
- `gui/tsup.config.ts` rewritten to a single plain config: `entry: ['src/index.ts']`, `format: ['cjs','esm']`, `dts: true`, `sourcemap: true`, `clean: true`, `external: ['react','react-dom','@moduleforge/core-gui']`.
- `gui/package.json`: `build` script becomes plain `tsup`; `build:css` script removed; `@moduleforge/core-gui` removed from `dependencies` (retained as optional peer); devDeps aligned to siblings where safe. The vestigial `"./styles.css": "./dist/index.css"` export is **kept as-is** for byte-for-shape parity with the siblings (all four siblings carry this dead export); no build step will populate `dist/index.css`.
- Ladle Tailwind entry relocated to `gui/.ladle/styles.css` with `.ladle/components.tsx` importing `./styles.css` (matching siblings); `src/styles.css` disposition documented.
- `gui/Makefile` header/build/clean comments updated (no more "CSS via tailwindcss-cli").
- **Success criterion (build shape):** a plain-tsup build producing **JS + DTS only** — `dist/{index.js,index.mjs,index.d.ts,index.d.mts,*.map}` and **no** real CSS content in `dist/` (the `./styles.css` export path is retained vestigially for sibling shape-parity, but no build step generates CSS inside the library). Fresh `bun install` succeeds without a pre-populated `.yalc/`; `app-mfdemo` verified to still render styled components via its own Tailwind `@source` scan of `dist/`.
