# 001 Port checkout-deps Token Hardening

## Purpose and scope
Flow-accounts gsl4: `scripts/checkout-deps.sh` passes `MODULEFORGE_READ_TOKEN` as `git -c http.extraheader=...` (visible in ps//proc/cmdline) and does not validate `host:`/`owner:` or allowlist the token host. Port the flow-accounts fix. The prompt's worktree path is stale; use `/Users/zane/playground/liquid-labs/flow-accounts/scripts/checkout-deps.sh` and `scripts/test-checkout-deps.sh` (33 cases) as reference.

## Requirements
1. Validate host/owner shape read from `versions.lock.yaml`.
2. Token-host allowlist `MODULEFORGE_TOKEN_ALLOWED_HOSTS` (default `github.com`); refuse to send the token elsewhere.
3. Pass the header via `GIT_CONFIG_COUNT=1`, `GIT_CONFIG_KEY_0=http.https://<host>/.extraheader`, `GIT_CONFIG_VALUE_0=...` environment; set `http.followRedirects=false`; never echo the token.
4. Port `test-checkout-deps.sh`, wire into CI/Makefile as the repo does for other scripts.
5. Update script usage/header comments and docs.

## Validation
- `bash scripts/test-checkout-deps.sh` passes; shellcheck clean if used in repo.
- Manual: run with a fake token and confirm it is absent from `ps`-visible argv (`bash -x` not used).

## Status
- Outcome: succeeded (2026-10-10).
- Validation: `bash scripts/test-checkout-deps.sh` passes (58 cases, incl. a process-table argv check with a positive control); `shellcheck` clean on `scripts/checkout-deps.sh` and `scripts/test-checkout-deps.sh`; the test fails 51 cases against the pre-change script (negative control).
- Changed: `scripts/checkout-deps.sh`, `scripts/test-checkout-deps.sh` (new), `Makefile` (`test.unit.scripts`, part of `test.unit`, so CI's `make test.unit` runs it), `docs/project-structure.md`.
- Decisions: this repo's `overrides` block means hosts vary per key, so the allowlist is enforced for the top-level host and every override host at read time and again in `git_fetch_auth`, and the header is scoped to the fetch target's host (not just `LF_HOST`). Override host/owner/repo and pin keys got the same strict shape checks (rejecting `.`/`..`, leading `-`, `..` in hosts).
