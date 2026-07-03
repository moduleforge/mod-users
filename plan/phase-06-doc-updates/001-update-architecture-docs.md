# Update Architecture Docs

## Purpose and scope

Update the architecture, project-structure, and spec documentation to reflect the changes made in this plan session: `mod-users/gui` is realigned to the sibling plain-tsup component-library build (JS + DTS only, no in-library CSS build; Ladle Tailwind entry at `.ladle/styles.css`; `vite.config.ts` for Ladle), and the local dev stack no longer runs a GUI container (the docker-compose `gui` service and `gui/Dockerfile.dev` are removed). Runs last, after the implementation phases have landed. Invoke the `update-architecture-docs` task-procedure at `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.

## Requirements

Reconcile the docs below against the implementation changes so they describe the system as it now is — no residual references to the removed Next.js GUI container, the tsup CSS build, or `src/styles.css`.

### Implementation task documents that surfaced the architectural implications

These implementation task docs (relative to `plan_worktree_path`) carry the architectural changes this doc update must reflect. They will have been completed by the time this phase runs:

- `plan/phase-01-gui-build-alignment/001-align-gui-build-tooling.md` — reshapes the GUI component-library build boundary: plain-tsup (JS + DTS only), no in-library CSS build, `vite.config.ts` added for Ladle, Ladle stylesheet relocated to `.ladle/styles.css`, `src/styles.css` removed, `@moduleforge/core-gui` demoted to optional peer.
- `plan/phase-03-dev-stack-disposition/001-remove-gui-compose-service.md` — removes the `deploy/local/docker-compose.yml` `gui` service and `gui/Dockerfile.dev`, changing the documented local-dev-stack topology (no more port-3000 GUI dev server in `make dev.start`).

### Architecture and spec files to review and update

- **`docs/architecture.md`**
  - "GUI component library" section (~lines 85-91): keep "built with tsup", but update the source-layout sentence that cites `src/styles.css` (base styles) — after Phase 01 the Tailwind entry lives at `gui/.ladle/styles.css` and `src/styles.css` is gone; consumers generate CSS via Tailwind v4 `@source` scanning of the built `dist/` (as `app-mfdemo` does). Ensure nothing implies the library emits a stylesheet.
  - "Local development stack" table (~lines 97-103): the `| GUI dev server | 3000 | Next.js dev server (app-mfdemo) with hot-reload |` row no longer belongs under `make dev.start` (the compose `gui` service is removed and `make dev.start` no longer serves anything on 3000). Remove that row, or clearly relocate the note to say app-mfdemo runs separately (outside `make dev.start`) as the integration/preview app, and that local component preview is `make preview` (Ladle, 61002).
  - Verify the "yalc" cross-cutting-patterns note (~line 126) remains accurate — yalc is still the local link mechanism for the `@moduleforge/core-gui` peer.
- **`docs/project-structure.md`**
  - `gui/` layout block (~lines 60-79): drop the `Dockerfile.dev` line (removed); add `vite.config.ts` (Ladle config) and note the `.ladle/` workbench (with `.ladle/styles.css`); remove or update the `src/styles.css` line (`src/styles.css` no longer exists); refine the `tsup.config.ts` comment (outputs CJS + ESM + .d.ts — no CSS).
  - `deploy/` block (~line 88): the compose-file description `# brings up Postgres, Authelia, Mailpit, API, GUI dev server` — drop "GUI dev server".
- **`docs/mod-users-spec.md`** — review the GUI-packaging paragraph. Confirm it does not describe an in-library CSS build or a Next.js GUI; per investigation, references to "app-mfdemo Next.js project" are correct and stay. Update only if a packaging description is now inaccurate.

role_doc: references/roles/architect-frontend.md

## Validation

- `docs/architecture.md`: no "GUI dev server (3000)" row under the `make dev.start` stack table; the GUI-library section no longer cites `src/styles.css` as a shipped stylesheet and does not imply the library emits CSS; `make preview` (Ladle) is referenced as the local component preview path.
- `docs/project-structure.md`: `gui/` block lists `vite.config.ts` and `.ladle/`, does not list `Dockerfile.dev`, and does not list `src/styles.css`; the `deploy/` compose-file description no longer mentions a GUI dev server.
- `docs/mod-users-spec.md`: GUI-packaging description is accurate (plain-tsup library; no in-library CSS build).
- Cross-file sweep: `grep -rn 'Dockerfile.dev\|GUI dev server\|src/styles.css' docs/` returns no live references (excluding any historical `plan/summary-*.md`).
- All three named files were reviewed; any file that needed no change is explicitly noted as reviewed-and-correct in the task report.

## References

- `plugins/flow/task-procedures/update-architecture-docs/SKILL.md` — the task-procedure to invoke.
- [../notes/sibling-build-mechanism.md](../notes/sibling-build-mechanism.md) and [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md) — the build-mechanism findings and dev-stack decision underlying these doc changes.
- Implementation task docs listed above (the changes to reflect).

## Status

- **Outcome:** succeeded.
- **Date:** 2026-07-03.
- **Task worktree:** `worktree/phase-06-task-01-update-architecture-docs`, commit `dbccabb` ("docs: reconcile architecture and project-structure docs with gui build/dev-stack changes").
- **Files modified:**
  - `docs/architecture.md` — "GUI component library" section: replaced the `src/styles.css` (base styles) claim with a description of the plain-tsup JS+DTS-only build, the Tailwind v4 entry at `gui/.ladle/styles.css` (workbench-only), and consumer CSS generation via `@source` scanning of `dist/`. "Local development stack" table: removed the `GUI dev server | 3000 | Next.js dev server (app-mfdemo)` row (no longer started by `make dev.start`) and added a sentence pointing local component preview at `make preview` (Ladle, 61002) with `app-mfdemo` as the separately-run integration testbed. The yalc cross-cutting-patterns note was reviewed and left unchanged (still accurate).
  - `docs/project-structure.md` — `gui/` layout block: dropped the `Dockerfile.dev` line, added `vite.config.ts` and `.ladle/` (noting `styles.css` as the workbench-only Tailwind entry), refined the `tsup.config.ts` comment to note "no CSS", removed the `src/styles.css` line, and updated the block's lead sentence to state the library ships no bundled CSS. `deploy/` block: dropped "GUI dev server" from the compose-file description comment.
  - `docs/mod-users-spec.md` — reviewed, no edit needed: `grep -n "styles.css|Dockerfile.dev|GUI dev server|CSS|tsup" docs/mod-users-spec.md` returned no matches; its "app-mfdemo Next.js project" references are accurate and unrelated to this delta.
- **Stale sections found and updated:**
  - `{docs/architecture.md, "GUI component library", "removed the src/styles.css / base-styles claim; added the no-in-library-CSS + .ladle/styles.css + @source consumer-CSS description"}`
  - `{docs/architecture.md, "Local development stack", "removed the port-3000 GUI-dev-server row and added a make-preview/app-mfdemo pointer"}`
  - `{docs/project-structure.md, "gui/ — TypeScript/React component library", "dropped Dockerfile.dev, added vite.config.ts and .ladle/, removed src/styles.css, refined tsup.config.ts comment, updated lead sentence"}`
  - `{docs/project-structure.md, "deploy/ — deployment configuration", "dropped \"GUI dev server\" from the compose-file description comment"}`
- **Validation:**
  - Phase 4 (update-architecture-docs) validation: re-read both edited files; no dangling cross-references introduced (all links — `AGENTS.md`, `AGENTS.md#first-time-setup`, `mod-users-spec.md`, `project-structure.md`, `oidc-troubleshooting.md` — point at unchanged, existing targets); no contradicted claims remain within either file or across `architecture.md`/`project-structure.md`/`mod-users-spec.md`.
  - Task-doc `## Validation` checklist: all five items confirmed — `docs/architecture.md` has no GUI-dev-server row and no longer cites `src/styles.css` as shipped, and references `make preview` for local preview; `docs/project-structure.md`'s `gui/` block lists `vite.config.ts`/`.ladle/`, omits `Dockerfile.dev` and `src/styles.css`, and the `deploy/` description no longer mentions a GUI dev server; `docs/mod-users-spec.md`'s GUI-packaging description was confirmed already accurate; `grep -rn 'Dockerfile.dev|GUI dev server|src/styles.css' docs/` (run from the task worktree root) returned no matches; all three named files were reviewed, with `mod-users-spec.md` explicitly noted as reviewed-and-correct (no edit).
- **Assumptions applied:**
  - The task doc's explicit instruction to reference `make preview` in `docs/architecture.md`'s local-dev-stack section was followed even though the Phase 05 task doc (which introduces `make preview`) is not in this task's formal `completed_task_docs` list — the target existed and was verified on disk (`Makefile` line 159-161, port 61002) before citing it, per the task document's own explicit line-level guidance overriding the generic procedure's narrower `completed_task_docs` scoping.
  - Left the `docs/project-structure.md` blockquote ("`gui/` depends on `@moduleforge/core-gui` via a `file:.yalc/` link") unedited: although Phase 01 removed the committed `file:.yalc/...` entry from `package.json` `dependencies` (now an optional peer resolved via yalc at build/lint time), the qualitative yalc-linking mechanism for local dev is unchanged and `AGENTS.md` retains identical phrasing post-Phase-01, so this was treated as still-accurate framing rather than a stale claim requiring an edit.
- **Flagged for manager:**
  - Pre-existing (not introduced by Phase 01/03) terminology inconsistency, out of this task's scope to fix: `docs/project-structure.md`'s `gui/` layout block labels `src/stories/` as "Storybook story files," and `docs/mod-users-spec.md` use case 13 says app-mfdemo is the showcase "in lieu of a dedicated story tool" / "the role Storybook plays in other projects" — but the actual tool is Ladle (which the plan's own Phase 05 gave a `make preview` entry point), not Storybook, and Ladle already existed as a workbench before this plan started per `plan/overview.md`. This predates the completed task docs and would need a scope decision (rename "Storybook" → "Ladle" / reconcile the "no dedicated story tool" framing) beyond what `completed_task_docs` authorizes here.
