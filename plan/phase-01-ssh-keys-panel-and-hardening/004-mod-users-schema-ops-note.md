# Mod Users Schema Ops Note

## Purpose and scope

Resolve followup `AWmf`. `mod_users.ssh_public_keys` (migration `0103`) is the ecosystem's first real table in a `mod_*` schema. The migration issues no `GRANT`, by design. The followup asks that `docs/architecture.md` state what a composing host's runtime database role needs on `mod_users`, and that backup, reset, or test tooling scoped to `public` misses the table. Scope: `docs/architecture.md`, and `docs/architecture/ssh-keys.md` only if needed for consistency. Docs only; no code. No standard skill covers this; edit directly.

## Requirements

1. **Re-run the tooling audit** that [the design note's `AWmf` section](../notes/ssh-keys-panel-design.md#mod_users-schema-operations-note-awmf) records. Search mod-users' `scripts/`, the root and sub-project `Makefile`s, `deploy/`, and Go test helpers for anything that enumerates, dumps, truncates, resets, or grants on tables by schema: for example `information_schema`, `pg_tables`, `pg_namespace`, `pg_dump -n`, `TRUNCATE`, `DROP SCHEMA`, `search_path`, `GRANT`, or a literal `'public'`. Confirm whether any of it assumes `public` only. Report each hit and its verdict in the task report. If any mod-users tooling does assume `public` only, do not fix it in this task: file a finding with `followups_add` (`plan_slug: mfgit-upstream-modules`, `phase_slug: ssh-keys-panel-and-hardening`) and describe it in the doc.
2. **State the requirement directly in `docs/architecture.md`'s Data model section.** Today the "Schema placement is mixed by design" paragraph only points at D1. Keep that pointer, and add a short paragraph or bullets saying the following:
   - The migration issues no `GRANT`. A composing host's provisioning owns privileges on `mod_users`.
   - A host that only **resolves** SSH keys (consumes `sshKeyResolver`) needs `USAGE` on schema `mod_users` and `SELECT` on `mod_users.ssh_public_keys`, plus read access to the `public` tables the resolver reads: `public.user_accounts` in the `ResolveActiveSSHPublicKey` join, and `public.entities` through mod-core's `GetEntityByID`, which checks for an archived holder.
   - A host that **serves the register and revoke routes** (`/v1/self/ssh-keys`, `/v1/user-accounts/{uuid}/ssh-keys`) additionally needs `INSERT` and `UPDATE` on `mod_users.ssh_public_keys`. Revoke archives the row with an `UPDATE`; it never deletes.
   - Without those privileges, resolution fails with a database permission error, not with the "unknown key" sentinel. A host that lacks write privileges should keep the write routes unreachable rather than let them return a `500`.
   - Backup, restore, reset, truncate, and test-fixture tooling that enumerates this module's tables by scanning `public` will miss `mod_users.ssh_public_keys`. A short checklist for composing hosts covers `pg_dump` schema filters, reset or truncate lists, `search_path` assumptions, and test-database teardown.
   - mod-users' own tooling result from requirement 1, for example: "mod-users' own repository tooling was audited on <date> and does not enumerate tables by schema."

   Verify each privilege claim against `model/queries/ssh_public_keys.sql` and `api/internal/service/ssh_key_resolver.go` before writing it. Do not describe how any specific host (MFManager, app-mfgit) implements its grants: that belongs to those projects, and in this wave program it lands in other plan-groups.
3. Update `docs/architecture.md`'s Key decisions **D1** bullet so it names the operational requirement in a phrase, not only by pointer.
4. Read `docs/architecture/ssh-keys.md` D1's "Operational consequence" paragraph. If it conflicts with the read/write split above, for example because it says only "the usual table privileges", align it in one or two sentences. Otherwise leave it unchanged and say so in the task report.
5. Follow the markdown standards: sentence-case headings, inline links rather than "see"-style pointers, and no frontmatter.

## Validation

- `grep -n "USAGE" docs/architecture.md` hits the new Data model text.
- `grep -n "mod_users" docs/architecture.md` shows the read-only and write privilege sets and the `public`-only tooling warning.
- Every privilege named in the new text maps to a statement in `model/queries/ssh_public_keys.sql` or `api/internal/service/ssh_key_resolver.go`. List the mapping in the task report.
- `git diff --stat`, excluding `plan/`, touches only `docs/architecture.md` and, optionally, `docs/architecture/ssh-keys.md`.
- Every link in the new text resolves to an existing file or anchor. Check by hand, or with the `link` tool against `docs/architecture.md`.

## Assumptions

- The planning-time audit found no `public`-only tooling in mod-users. The task re-confirms it.
- The phase-2 doc-updates task also edits `docs/architecture.md` (GUI component library section), after this task. Keep this task's edits to the Data model and Key decisions sections to avoid overlap.

## References

- Followup `AWmf` (project `plan/followups.yaml`).
- [The design note's `AWmf` section](../notes/ssh-keys-panel-design.md#mod_users-schema-operations-note-awmf).
- `docs/architecture.md` (Data model, Key decisions D1), `docs/architecture/ssh-keys.md` (D1), `model/migrations/sql/0103_ssh_public_keys.sql`, `model/queries/ssh_public_keys.sql`, `api/internal/service/ssh_key_resolver.go`.
- `/Users/zane/playground/moduleforge/.flow/app-mfgit-managed-mode-r1-schema-review.md`, "Operational tooling blind spots" row (read-only context).

## Status

Succeeded, 2026-10-04. Edited [docs/architecture.md](../../docs/architecture.md) (Data model, Key decisions D1) and aligned the "Operational consequence" paragraph in [docs/architecture/ssh-keys.md](../../docs/architecture/ssh-keys.md). Audit found no `public`-only tooling in mod-users; validation checks passed.
