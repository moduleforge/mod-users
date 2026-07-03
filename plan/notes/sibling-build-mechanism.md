# Sibling GUI build mechanism — findings

## Purpose and scope

Records how the canonical sibling GUI libraries (`mod-core`, `mod-contacts`, `mod-tags`, `mod-tasks`) actually build and package, so the mod-users alignment tasks replicate the *real* convention rather than an assumed one. Investigated by reading each sibling's `tsup.config.ts`, `vite.config.ts`, `package.json`, `src/index.ts`, `.ladle/`, and `dist/` on disk.

## Headline correction to the manager's premise

The dispatch premise stated the siblings "still emit `dist/index.css`" from a plain `tsup` build and asked to "investigate precisely how." **They do not.** No sibling emits `dist/index.css`:

- `mod-core/gui/dist/` contains only `index.js`, `index.mjs`, `index.d.ts`, `index.d.mts`, and `.map` files — **no `index.css`**.
- `mod-contacts/gui` and `mod-tasks/gui` have no `dist/` at all at rest; `mod-tags/gui/dist/` likewise has no CSS.
- None of the four sibling `src/index.ts` files import a CSS file, and none has a `src/styles.css`.
- Their `tsup.config.ts` has a single `entry: ['src/index.ts']` with no CSS entry and no postcss/esbuild-CSS plugin.

Yet **all four** still declare `"./styles.css": "./dist/index.css"` in their `exports` map. That export points at a file the build never produces — it is **vestigial/dead across every sibling**. Nothing imports it (see consumer section).

Conclusion: there is no clever tsup→CSS mechanism to replicate. The correct sibling convention is **tsup emits JS + DTS only; no CSS is bundled into `dist/`.**

## What `vite.config.ts` is actually for

Every sibling `vite.config.ts` is identical:

```ts
import { defineConfig } from 'vite';
import tailwind from '@tailwindcss/vite';
export default defineConfig({ plugins: [tailwind()] });
```

It is **not** used by the library (`tsup`) build. It is consumed by **Ladle**, which is Vite-based: the `dev`/`preview:build` scripts (`ladle serve` / `ladle build`) pick up `vite.config.ts` so `@tailwindcss/vite` processes Tailwind v4 in the component workbench. The Tailwind entry stylesheet for the workbench lives at `.ladle/styles.css` (git-tracked in `mod-core`), imported by `.ladle/components.tsx` as `./styles.css`.

## How consumers get CSS (the real proof point)

`app-mfdemo` (aggregate-level Next.js app) does **not** import any `@moduleforge/*-gui/styles.css`. Its `src/app/globals.css` uses Tailwind v4 `@source` scanning of the libraries' built JS:

```css
@import "tailwindcss";
@import "tw-animate-css";
@import "shadcn/tailwind.css";
@source "../../node_modules/@moduleforge/core-gui/dist";
@source "../../node_modules/@moduleforge/users-gui/dist";
```

The **consuming app's** Tailwind build emits the CSS for the classes used inside the libraries' components (including `cva()` strings). The library ships no stylesheet. This is the canonical Tailwind v4 pattern and is why the `"./styles.css"` export is unused.

Note: `@source ".../@moduleforge/users-gui/dist"` scans the *built* JS in `dist/`, so mod-users' `dist/index.js`/`index.mjs` must contain the class strings (they do — cva/className literals survive tsup bundling). This already works today; dropping mod-users' separate CSS build does **not** break app-mfdemo.

## Implications for mod-users alignment

1. **Remove** the bespoke `build:css` script and the second (CSS) tsup entry. `build` becomes plain `tsup` (matches all siblings).
2. **Add** `vite.config.ts` identical to siblings (for Ladle).
3. mod-users uniquely has `src/styles.css` (with `@source "../src"`) imported by `.ladle/components.tsx` as `../src/styles.css`. Siblings instead keep this at `.ladle/styles.css` imported as `./styles.css`. Relocating to `.ladle/styles.css` matches the convention; the `@source "../src"` line resolves identically from `.ladle/` (both `.ladle/` and `src/` sit under `gui/`).
4. The vestigial `"./styles.css": "./dist/index.css"` export: siblings keep it (dead). To be byte-for-byte "identical in shape," keep it; it is harmless. (Removing it would make mod-users *more* correct but *less* identical — a minor call flagged in the scope note.)
5. `tsup.config.ts` `external`: mod-contacts (the sibling that also peer-depends on `@moduleforge/core-gui`) lists `external: ['react','react-dom','@moduleforge/core-gui']`. mod-users imports `@moduleforge/core-gui` in `src` (sidebar-nav, ui/dialog, error-message), so its external list should match mod-contacts and add `@moduleforge/core-gui`.

## Ladle port

Siblings run Ladle on distinct ports (mod-core 61000, mod-contacts/mod-users 61002). mod-users already uses `ladle serve --port 61002`. Module-level `make preview` in mod-core wraps `bun run dev` (Ladle); mod-users' top-level Makefile has **no** `preview` target (siblings' `make preview` lives in the *module-level* Makefile, not `gui/Makefile`).
