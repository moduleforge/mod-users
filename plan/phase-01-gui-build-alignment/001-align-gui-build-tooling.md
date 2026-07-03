# Align Gui Build Tooling

## Purpose and scope

Bring `mod-users/gui`'s build tooling into shape-alignment with the canonical sibling tsup component libraries (`mod-core`, `mod-contacts`, `mod-tags`, `mod-tasks`) so `@moduleforge/users-gui` builds and packages the same way. This is a packaging/tooling change only — **do not touch component behavior** (`src/components/*`, `src/lib/*`, `src/index.ts` public surface).

No standard skill covers this; follow the `## Procedure` below. The authoritative findings are in [../notes/sibling-build-mechanism.md](../notes/sibling-build-mechanism.md) and [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md).

## Requirements

1. **Add `gui/vite.config.ts`** identical to the siblings (consumed by Ladle, which is Vite-based — not by the tsup library build):
   ```ts
   import { defineConfig } from 'vite';
   import tailwind from '@tailwindcss/vite';

   export default defineConfig({
     plugins: [tailwind()],
   });
   ```

2. **Rewrite `gui/tsup.config.ts`** to a single plain config matching `mod-contacts/gui` (the sibling that also peer-depends on `@moduleforge/core-gui`) — drop the second CSS entry entirely:
   ```ts
   import { defineConfig } from 'tsup';

   export default defineConfig({
     entry: ['src/index.ts'],
     format: ['cjs', 'esm'],
     dts: true,
     sourcemap: true,
     clean: true,
     external: ['react', 'react-dom', '@moduleforge/core-gui'],
   });
   ```
   `@moduleforge/core-gui` must be in `external` because `src` imports it (sidebar-nav, ui/dialog, error-message).

3. **Edit `gui/package.json`:**
   - `scripts.build`: change `"tsup && bun run build:css"` → `"tsup"`.
   - Remove the `scripts.build:css` line entirely.
   - Remove `@moduleforge/core-gui` from `dependencies` (the committed `"file:.yalc/@moduleforge/core-gui"` entry). **Keep** it as the existing optional `peerDependencies` + `peerDependenciesMeta` entry — do not touch those. This is what makes fresh `bun install`/CI work (matches `mod-contacts/gui`, which declares core-gui only as an optional peer).
   - **Keep** the vestigial `"./styles.css": "./dist/index.css"` export exactly as-is (all four siblings carry this dead export; keeping it preserves shape-parity even though nothing will populate `dist/index.css`).
   - devDeps: leave as-is except where a dep is provably unused. `@types/node` and `shadcn` are present in mod-users but absent in `mod-contacts`; only remove one if a grep confirms it is not referenced (e.g. no `shadcn` CLI invocation in any script/Makefile, no `@types/node` type usage). **When in doubt, keep it** — a spurious devDep is harmless; a removed-but-needed one breaks the build.

4. **Relocate the Ladle Tailwind entry stylesheet** to the sibling convention:
   - Create `gui/.ladle/styles.css` containing the current contents of `gui/src/styles.css` (the `@import "tailwindcss"` / `@source "../src"` / `@theme` / `:root` / `.dark` / `@layer base` block). The `@source "../src"` line resolves identically from `.ladle/` since both `.ladle/` and `src/` sit under `gui/`.
   - Change `gui/.ladle/components.tsx` import from `'../src/styles.css'` to `'./styles.css'` (matching `mod-core/gui/.ladle/components.tsx`).
   - Remove `gui/src/styles.css` (no sibling has a `src/styles.css`, and after tsup drops the CSS entry nothing imports it from `src`). Confirm via grep that no `src` file imports `styles.css` before deleting.

5. **Update `gui/Makefile` comments** so they no longer describe a CSS build:
   - Line 4 header: `# Build: tsup (JS/TS) + @tailwindcss/cli (CSS). Preview: ladle on port 61002.` → `# Build: tsup (JS/TS + DTS). Preview: ladle on port 61002.`
   - The `build` target's `## ...` help text (line 69): `## Build the component library (tsup JS/TS + CSS via tailwindcss-cli)` → `## Build the component library (tsup: JS/TS + DTS)`.
   - Leave the `clean` target's `rm -rf dist .next build` as-is or drop `.next` (a stale-artifact reference); dropping `.next` is optional and coordinated with Phase 02, which removes the stray `.next/` dir — safe to leave for Phase 02.

6. **Clear the now-stale yalc known-issue** created by removing the `file:` dep:
   - `AGENTS.md` "Known issues and follow-up items": remove the **yalc dep** bullet (`next-steps id: 3RgF` — "fresh checkouts and CI will fail `bun install` without `.yalc/`"), since the `file:` dependency is gone and fresh `bun install` now succeeds. Leave the CI-pnpm bullet (`r86L`) — that is Phase 04's.
   - `.claude/CLAUDE.md` "Known gotchas": update the **yalc link** bullet to reflect the new reality — fresh `bun install` no longer fails, but building/typechecking `gui/` still needs `@moduleforge/core-gui` resolved via the yalc `yalc add` step from AGENTS.md (the peer import in `src` still must resolve). Do **not** delete the yalc setup step in AGENTS.md First-time setup (step 4) — it remains required for builds.

## Validation

- `gui/vite.config.ts` exists and matches the sibling content.
- `gui/tsup.config.ts` is a single `defineConfig({...})` (not an array) with `external` including `@moduleforge/core-gui`; no CSS entry remains.
- `gui/package.json`: `grep -n 'build:css\|file:.yalc' gui/package.json` returns nothing; `"build": "tsup"`; `@moduleforge/core-gui` still present under `peerDependencies`/`peerDependenciesMeta`; `"./styles.css"` export still present.
- `gui/.ladle/styles.css` exists; `gui/.ladle/components.tsx` imports `'./styles.css'`; `gui/src/styles.css` is gone and `grep -rn "styles.css" gui/src` returns nothing.
- With `.yalc/@moduleforge/core-gui` linked locally (per AGENTS.md), `make build.gui` succeeds and `gui/dist/` contains `index.js`, `index.mjs`, `index.d.ts`, `index.d.mts`, and `.map` files, and **no** `dist/index.css` with real content (parity with `mod-core/gui/dist/`).
- **Fresh-install check:** in a clean checkout of this branch (no `.yalc/` populated, `@moduleforge/core-gui` not linked), `bun install` completes without error (the `file:` path no longer breaks resolution).
- **Consumer regression check:** `app-mfdemo` still renders styled `@moduleforge/users-gui` components — its `src/app/globals.css` `@source "../../node_modules/@moduleforge/users-gui/dist"` scans the built JS, which still carries the cva/className literals. Rebuild `users-gui`, and confirm app-mfdemo's Tailwind build still emits the component classes (no missing styles).
- `AGENTS.md` no longer contains the 3RgF "fresh checkouts ... fail `bun install` without `.yalc/`" bullet; `.claude/CLAUDE.md` yalc gotcha reflects install-works / build-still-needs-yalc.
- `make lint.gui` (tsc `--noEmit`) passes.

## Metadata

architectural_impact: true

## Assumptions

- The local dev environment has `@moduleforge/core-gui` linked via yalc so `make build.gui` and `make lint.gui` can resolve the peer import (per AGENTS.md First-time setup step 4). Fresh CI does not build against a populated `.yalc/`; the fresh-install check above deliberately excludes it.
- `app-mfdemo` lives at the aggregate level (outside `project_root`, read-only). The consumer regression check is a verification, not an edit to app-mfdemo.

## References

- [../notes/sibling-build-mechanism.md](../notes/sibling-build-mechanism.md) — why siblings emit no `dist/index.css`, what `vite.config.ts` is for, how consumers get CSS via `@source`, and the `external`/`.ladle/styles.css` conventions.
- [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md) — scope decision 2 (yalc/core-gui dependency inconsistency).
- Sibling references (read-only): `mod-contacts/gui/tsup.config.ts` and `mod-contacts/gui/package.json` (core-gui peer shape + external list); `mod-core/gui/vite.config.ts` and `mod-core/gui/.ladle/{components.tsx,styles.css}` (Ladle convention).

## Checkpoint hints

- After adding `vite.config.ts` and rewriting `tsup.config.ts`.
- After the `package.json` script/dependency edits.
- After relocating the Ladle stylesheet and removing `src/styles.css`.
- After the `gui/Makefile` comment + AGENTS.md/.claude known-issue edits.
