# Migrate mod-users to a single .flow/ ignore entry

## Purpose and scope

This is wave 2 (wave-2-migration) of the wave plan `flow-ignore-canonicalization`, plan-group `migrate-moduleforge`. Flow now treats `.flow/` as fully git-ignored; this plan migrates the single project `mod-users` accordingly. One project, one task.

## Overview

Replace every flow-related ignore line in `mod-users`'s `.gitignore` (inventory: 31:# flow stuff; 33:.flow/*; 34:!.flow/plans; 35:!.flow/project-analysis.json; 36:!.flow/what-next-cache.json) with one `.flow/` entry, and untrack any tracked `.flow` content with `git rm -r --cached .flow` (inventory: 6: .flow/binding.md .flow/project-analysis.json .flow/tasks/main.json .flow/tasks/phase-01-task-02-sqlc-queries.json .flow/tasks/phase-03-task-02-anon-endpoint.json .flow/what-next-cache.json). Files stay on disk. One commit touches only `.gitignore` and the `.flow` untracking.

## Phases

| Phase | Slug | Task | Tier |
|---|---|---|---|
| 18 | flow-ignore-migrate | migrate-gitignore | sonnet-low |

## Risks

The main checkout is clean per the inventory, so close-out merge is not expected to be blocked by uncommitted tracked changes (re-check at close-out).
