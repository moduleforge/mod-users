# SSH Key Label Hardening

## Purpose and scope

Resolve followup `GrC7`. `NormalizeLabel` (`api/internal/sshkey/label.go`) bounds only label length, not content. A caller-supplied label containing NUL reaches Postgres and fails the INSERT with a `500` instead of a `400`. Other control characters and Unicode bidi overrides are stored, then echoed in API responses and audit snapshots. The `SSHKeysPanel` this plan adds (task `003`) renders labels, which turns stored bidi overrides into a real display-spoofing vector.

Scope: `api/internal/sshkey/` (`label.go`, `errors.go`, tests), `api/internal/service/ssh_keys.go` (detail-code mapping) and its test, and the label-related descriptions in `api/openapi.yaml`. Go and OpenAPI only. No GUI, model, migration, or prose-doc change: `docs/mod-users-spec.md` is updated by the phase-2 doc task. No standard skill covers this; implement directly.

## Requirements

1. In `api/internal/sshkey/errors.go`, add the sentinel `ErrLabelInvalidChars`. Its doc comment states that it maps to detail code `users.ssh_key_label_invalid`.
2. In `NormalizeLabel`, define a disallowed rune as any rune where `unicode.IsControl(r)` is true, or any bidi formatting character U+202A through U+202E or U+2066 through U+2069. Put the predicate in one small, unexported, unit-tested helper.
   - **Caller-supplied label (`requested != nil`):** keep the existing raw-byte bound as the first check. Reject a label containing any disallowed rune with `ErrLabelInvalidChars`, wrapped with `fmt.Errorf` the same way the existing errors are. Leading and trailing whitespace that `strings.TrimSpace` already removes, including a trailing `\n` or `\t`, must keep working exactly as today. A label like `"  my key\n"` still normalizes to `"my key"`, and only control or bidi characters left after trimming are rejected. Keep the existing length rule and its order relative to trimming.
   - **Comment-derived default (`requested == nil`):** strip disallowed runes instead of rejecting, then trim and truncate to 100 runes as today. A key whose comment contains them still registers.
   - Update the `NormalizeLabel` doc comment to describe both rules.
3. In `api/internal/service/ssh_keys.go`, extend `mapSSHKeyLabelError` so that `ErrLabelInvalidChars` maps to `apiresp.InvalidInput(apiresp.FieldError{Field: "label", Code: "users.ssh_key_label_invalid", Message: ...})`, following the existing `users.ssh_key_label_too_long` case. Any other error still wraps as today.
4. In `api/openapi.yaml`, add the new `400` / `users.ssh_key_label_invalid` outcome to the description text of both `POST /v1/self/ssh-keys` and `POST /v1/user-accounts/{uuid}/ssh-keys`, next to the existing `users.ssh_key_label_too_long` sentence. Add a sentence to the `SSHKeyCreate.label` and `SSHKey.label` descriptions saying that control and bidi-formatting characters are rejected in a supplied label and stripped from a comment-derived default.
5. Tests:
   - `api/internal/sshkey/label_test.go`: table-driven cases for a supplied label containing NUL, an embedded newline (`"a\nb"`), an embedded tab, DEL (U+007F), U+202E, and U+2066, each expecting `ErrLabelInvalidChars`; a supplied label with only surrounding whitespace (`"  my key\n"`), still valid; a comment containing U+202E and NUL, stripped, with no error; and a comment that becomes empty after stripping, giving an empty label.
   - `api/internal/service/ssh_keys_test.go`: one row in `TestSSHKeyService_Register_ParseAndLabelErrorsMapToDetailCodes` for a NUL-bearing label, expecting field `label` and code `users.ssh_key_label_invalid`.
   - All existing tests pass unchanged.
6. Check whether the `authorized_keys` comment can carry a NUL or other control character through `sshkey.Parse` (`api/internal/sshkey/sshkey.go`) at all. Record the answer in the task report. The strip rule applies regardless; this only tells reviewers whether the comment path was reachable.

## Validation

- `cd api && go test ./internal/sshkey/... ./internal/service/...` passes. Run `make preflight` first in a task worktree, so `scripts/link-siblings.sh` plants the sibling-module symlinks (`AGENTS.md`, "Working in worktrees").
- `make test.unit` (or at least `make build.api` plus the `api` unit tests) passes.
- `grep -rn "users.ssh_key_label_invalid" api/` hits exactly `api/internal/service/ssh_keys.go`, its test, `api/internal/sshkey/errors.go` (comment), and `api/openapi.yaml` (both register endpoints).
- `git diff --stat`, excluding `plan/`, touches only `api/internal/sshkey/*`, `api/internal/service/ssh_keys.go`, `api/internal/service/ssh_keys_test.go`, and `api/openapi.yaml`.
- `api/openapi.yaml` still parses as YAML: `python3 -c "import yaml,sys; yaml.safe_load(open('api/openapi.yaml'))"` or an equivalent check.

## Assumptions

- Existing stored labels are not rewritten; there is no data migration. Task `003` renders labels inside `<bdi>` to cover legacy rows.
- The detail-code name `users.ssh_key_label_invalid` is fixed by this plan (overview) and task `003` displays it. Do not rename it.

## References

- Followup `GrC7` (project `plan/followups.yaml`), the source of the policy.
- [The design note's label hardening section](../notes/ssh-keys-panel-design.md#label-hardening-grc7).
- `api/internal/sshkey/label.go`, `api/internal/sshkey/errors.go`, `api/internal/service/ssh_keys.go` (`mapSSHKeyLabelError`, `mapSSHKeyParseError`).
- `docs/mod-users-spec.md`, Security requirements, "Algorithm policy" bullet (updated later by the phase-2 doc task, not here).

## Status

- Outcome: succeeded (2026-10-04).
- Validation: `go test ./internal/sshkey/... ./internal/service/...` passed; `make build.api` passed; api unit tests in `make test.unit` passed (the gui half of `make test.unit` fails on a sibling-environment issue: `react` missing from `mod-core/gui/node_modules`); grep hits only the expected files; `openapi.yaml` parses; diff limited to the scoped files.
- Requirement 6 answer: yes, the comment path is reachable for NUL and bidi characters. `sshkey.Parse` returns them intact in `Comment` (`ssh.ParseAuthorizedKey` does not filter them; covered by `TestParse_CommentCanCarryControlCharacters`). A newline cannot reach the comment: it splits the input into a second line and `Parse` refuses it with `ErrInvalid`. CR and tab were not separately probed.
- Files: `api/internal/sshkey/{label.go,errors.go,label_test.go}`, `api/internal/service/{ssh_keys.go,ssh_keys_test.go}`, `api/openapi.yaml`.
