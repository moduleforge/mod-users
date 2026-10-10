# Sibling checkout-deps.sh audit

Read-only audit of every `scripts/checkout-deps.sh` under `/Users/zane/playground/moduleforge/*` and `/Users/zane/playground/liquid-labs/*`. Reference: this repo's hardened `scripts/checkout-deps.sh` (token via `GIT_CONFIG_COUNT` env, host/owner validation, `MODULEFORGE_TOKEN_ALLOWED_HOSTS` allowlist, `http.followRedirects=false`). Findings are by static inspection of file contents; no token values were read or recorded.

| Repo | Path | extraheader on cmdline? | Host/owner validation? | Token-host allowlist? | Redirect hardening? | Needs fix |
|---|---|---|---|---|---|---|
| mod-users (this repo, main checkout) | scripts/checkout-deps.sh | yes (`-c http.extraheader=`) | partial (override host charset only; no owner/top-level shape check) | no | no | superseded by the hardened script on this plan's branch; resolved on merge |
| app-mfmanager | scripts/checkout-deps.sh | yes | no | no | no | YES (all four) |
| app-mfgit | scripts/checkout-deps.sh | yes | partial (override host charset only) | no | no | YES (all four) |
| app-mftodo | scripts/checkout-deps.sh | yes | partial (override host charset only) | no | no | YES (all four) |
| flow-accounts (liquid-labs) | scripts/checkout-deps.sh | no (GIT_CONFIG env) | yes (host and owner regex) | yes | yes | no (no `overrides` support, so nothing further to harden) |

## Notes

- app-mfgit, app-mftodo and mod-users main checkout share an identical script (same content hash); app-mfmanager is a distinct older variant.
- Copies under `*/worktrees/*` that are byte-identical to their repo's main checkout were skipped (app-mfmanager, app-mfgit, flow-accounts worktrees).
- Worktree copies that differ from the main checkout: mod-users plan worktrees (the hardened work under way here), mod-users `externally-managed-sign-in-sta`, app-mftodo `mftodo-managed-request-path-fi` and `release-mftodo-0-1-2`, app-mfgit `api-completeness-gaps`. All of the non-mod-users-plan ones still pass the token via `-c http.extraheader` on the command line and have no allowlist or redirect hardening, so they add no new fix beyond their repo's finding. An additional app-mftodo worktree (`task/mod-users`) matches the unhardened mod-users variant.
