# Close checkout-deps Test Gaps

## Purpose and scope
Implements backlog item nJel (project followup). `scripts/test-checkout-deps.sh` (58 cases) does not protect the `set +x` xtrace safeguards around token dereferences in `scripts/checkout-deps.sh`, and lacks several host edge cases. Work targets the plan branch `plan/flow-accounts-upstream`.

## Requirements
1. Add a case that runs the script under `bash -x` (and a second under `SHELLOPTS=xtrace`) with a fake token set and asserts that neither the token nor its base64 form appears in the combined stdout and stderr. Use the existing `no_leak` helper and the existing randomly generated fake token.
2. Add cases for: the allowlist re-check inside `git_fetch_auth` (reached with a host that passed lockfile read but is not allowlisted, e.g. via an override host set to a non-allowlisted host where the lockfile-time check is bypassed or by invoking the function path the test harness can reach; if the re-check is truly unreachable from the CLI, say so in the task Status and test the closest reachable path); a trailing-dot host (`github.com.`); a host-suffix lookalike (`github.com.evil.example`); a CRLF-terminated lockfile; and positive case-insensitivity (`GitHub.com` as lockfile host with a token and default allowlist).
3. While there, apply the small robustness fixes that the new tests exercise and that live in the same script, if (and only if) a new test exposes the need: iterate `MODULEFORGE_TOKEN_ALLOWED_HOSTS` without globbing (`set -f` locally or `read -ra`). Do not change behavior otherwise.
4. Record in the test file header that the ps/argv check is a single snapshot taken inside the stub.

## Validation
1. `bash scripts/test-checkout-deps.sh` passes with the new cases; `shellcheck scripts/checkout-deps.sh scripts/test-checkout-deps.sh` is clean (report if shellcheck is unavailable).
2. Negative control: temporarily remove one `set +x` guard around a token dereference in a scratch copy of the script and show the new xtrace case fails; do not commit that change.
3. `make test.unit.scripts` passes.

## References
- Backlog item nJel in the project's `plan/followups.yaml`.
