# Phase — Preview Target & README Alignment

## Goals

Adopt the sibling `make preview` convention for the Ladle component workbench and correct the stale aggregate-README module-table entry for mod-users. Independent of other phases. Note the README correction crosses the repo boundary and is largely a manager-handled item (see below).

## Inputs

- `mod-core/Makefile` `preview` target (module-level; wraps `bun run dev` = Ladle) as the model.
- `mod-users/Makefile` (top-level; currently no `preview` target; gui Ladle runs on 61002).
- Root aggregate `/Users/zane/playground/moduleforge/README.md` module table (line 71: `mod-users … yes (Next.js)`) and workbench row (line 114).

## Outputs

- A `preview` target added to `mod-users/Makefile` mirroring `mod-core` (installs gui deps if needed, runs Ladle on 61002 via `make -C gui dev.start`).
- **Cross-repo (manager-handled, not a mod-users task):** root README module-table `mod-users` GUI cell corrected `Next.js` → `tsup lib`; workbench row extended to include `mod-users/`. Flagged for the manager because the aggregate README is in a separate git repo outside `project_root`; a mod-users task branch cannot carry it. Recorded so it is not lost.
