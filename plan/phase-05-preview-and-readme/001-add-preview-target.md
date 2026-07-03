# Add Preview Target

## Purpose and scope

Adopt the sibling `make preview` convention by adding a module-level `preview` target to `mod-users/Makefile` that runs the Ladle component workbench for `gui/` (port 61002). Mirrors `mod-core/Makefile`'s `preview` target. Touches only the root `mod-users/Makefile` — fully independent of every other phase.

The cross-repo aggregate-`README.md` correction is **NOT part of this task** (see `## References`) — it is flagged for the manager because the aggregate README is in a separate git repository outside `project_root`.

No standard skill; follow the `## Requirements`.

## Requirements

1. **Add a `preview` target to the root `mod-users/Makefile`.** Place it in the "Dev orchestration" section (near `dev.start`/`dev.restart`). Delegate to the existing `gui/` Makefile `dev.start` target (which runs `preflight` then `bun run dev` = `ladle serve --port 61002`), so deps are installed if needed and the port stays defined in one place:
   ```make
   .PHONY: preview
   preview: ## Run the Ladle component workbench for gui/ (http://localhost:61002)
   	@echo "==> preview: users-module/gui — Ladle on http://localhost:61002"
   	@$(MAKE) -C gui dev.start
   ```
   Match the surrounding file's tab-indentation and `## ` help-comment style so it is picked up by the self-documenting `help` target.

2. Do **not** duplicate the Ladle port literal in a way that diverges from `gui/Makefile` (which owns `--port 61002`). Delegating to `dev.start` keeps a single source of truth; the `61002` in the echo/help text is cosmetic.

## Validation

- `make preview` appears in `make help` output and, when run, starts Ladle for `gui/` on http://localhost:61002 (foreground; Ctrl-C to stop).
- `grep -n 'preview' Makefile` shows the new target; `make -n preview` (dry run) shows it delegating to `$(MAKE) -C gui dev.start`.
- No other Makefile targets are altered; `make help` still lists the existing canonical targets.

## Assumptions

- `mod-users/Makefile` currently has no `preview` target (verified) and `gui/`'s Ladle already runs on 61002 via `gui/Makefile` `dev.start`.
- Adding `preview` is a low-cost convention alignment treated as in scope (an applied assumption the user may veto).

## References

- [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md) — scope decision 3 (`make preview` convention) and the cross-repo README concern.
- Sibling reference (read-only): `mod-core/Makefile` `preview` target (the model).
- **Cross-repo, manager-handled, NOT this task:** the aggregate `/Users/zane/playground/moduleforge/README.md` module-table cell (`mod-users … yes (Next.js)` → `yes (tsup lib)`) and the "GUI workbench" row (add `mod-users/`) live in a separate git repo outside `project_root`; a mod-users task branch cannot carry that edit. Flagged for the manager.

## Checkpoint hints

- Single-file change; no checkpoints needed.

## Status

- **Outcome:** succeeded. Date: 2026-07-03.
- Implemented in worktree `worktree/phase-05-task-01-add-preview-target`; final commit captured in the task agent's structured report.
- `Makefile`: added a `.PHONY: preview` target (in the "Dev orchestration" section, immediately after `dev.restart`) delegating to `$(MAKE) -C gui dev.start`, exactly matching the block specified in `## Requirements` item 1. No other targets were touched.
- Validation: `grep -n 'preview' Makefile` shows the new target; `make -n preview` dry-run shows delegation to `gui dev.start`; `make help` lists `preview` among the canonical targets with its help text, all pre-existing targets unchanged. Ran `make preview` live — Ladle started, `curl http://localhost:61002` returned HTTP 200, then the process was stopped (Ctrl-C equivalent) to leave the worktree clean.
- No assumptions from `## Assumptions` were overridden; both held as stated (no pre-existing `preview` target; `gui/Makefile`'s `dev.start` already runs Ladle on 61002).
- **Flagged for manager (per `## References`, not part of this task):** the cross-repo aggregate `/Users/zane/playground/moduleforge/README.md` module-table cell and "GUI workbench" row updates remain outstanding in the separate aggregate repo.
