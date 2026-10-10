# 002 Audit Sibling checkout-deps Copies

## Purpose and scope
Read-only audit: list which other ModuleForge repos' copies of `checkout-deps.sh` still need the gsl4 fix. Known copies: `moduleforge/app-mfgit`, `app-mfmanager`, `app-mftodo` (search all of `/Users/zane/playground/moduleforge/*` for others).

## Requirements
1. For each copy, check for `http.extraheader` via `-c`, host/owner validation, allowlist, redirect hardening.
2. Write findings to `plan/notes/sibling-checkout-deps-audit.md` (table: repo, path, extraheader-on-cmdline?, validation?, allowlist?, needs fix).
3. Do not modify other repos; add one followup per repo needing the fix.

## Validation
- Notes file exists and covers every found copy; followups added.

## Status

- Outcome: succeeded (2026-10-10).
- Audit table written to `plan/notes/sibling-checkout-deps-audit.md`. app-mfmanager, app-mfgit and app-mftodo need the full hardening (3 findings filed); flow-accounts is already hardened; mod-users main checkout is superseded by this plan's script.
