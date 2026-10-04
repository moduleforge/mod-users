# Reject All Unicode Bidi Control Characters in SSH Key Labels

## Purpose and scope

This remediation task closes finding `sQRd` from the phase-01 gate's security-lens review. It targets the plan branch `plan/mfgit-upstream-modules` and lands through the ordinary per-task loop (its own worktree, merged back into the plan branch).

`api/internal/sshkey/label.go`'s `isDisallowedLabelRune` rejects every Unicode control character plus the bidi override/embedding (U+202A-U+202E) and isolate (U+2066-U+2069) characters, but not the remaining Unicode `Bidi_Control` code points: U+061C (ARABIC LETTER MARK), U+200E (LEFT-TO-RIGHT MARK), and U+200F (RIGHT-TO-LEFT MARK). The plan overview's stated goal ("reject control and bidi-formatting characters") and the `api/openapi.yaml` text this plan added ("Control and bidi-formatting characters are ... rejected") both claim the whole bidi-formatting class, so this is a gap against the plan's own stated scope and documentation, not a deliberate exclusion.

Zero-width characters (U+200B ZWSP, U+200C ZWNJ, U+200D ZWJ, U+2060 WORD JOINER) are explicitly **out of scope** for this task: they are not Unicode bidi controls, and rejecting ZWJ/ZWNJ would refuse legitimate labels (emoji sequences, Persian and Indic scripts).

## Requirements

1. Extend `isDisallowedLabelRune` in `api/internal/sshkey/label.go` so it also returns `true` for U+061C, U+200E, and U+200F, in addition to the existing `unicode.IsControl` check and the U+202A-U+202E / U+2066-U+2069 ranges. Update its doc comment to state it now covers Unicode's full `Bidi_Control` set.
2. Keep zero-width characters (U+200B, U+200C, U+200D, U+2060) allowed — do not add them to the rejection set.
3. A supplied label containing one of the three new runes is rejected with `ErrLabelInvalidChars` (`users.ssh_key_label_invalid`), exactly as the existing control/bidi runes are. A comment-derived default label has the new runes stripped, exactly as the existing ones are.
4. Change no API shape or detail code. `api/openapi.yaml`'s existing "control and bidi-formatting characters" wording stays accurate and needs no edit.

## Validation

1. Add table-driven rows to `api/internal/sshkey/label_test.go`: a supplied label containing U+200E, a supplied label containing U+200F, and a supplied label containing U+061C each return `ErrLabelInvalidChars`; a comment-derived default containing any of these three is stripped with no error; a supplied label containing U+200D (ZWJ) is still accepted (no regression on the explicitly-out-of-scope zero-width characters).
2. Add direct unit-test rows for `isDisallowedLabelRune` covering each of the three new code points, plus boundary runes U+200D (must stay allowed) and U+2010 (HYPHEN, must stay allowed).
3. `cd api && go test ./internal/sshkey/... ./internal/service/...` passes (run `make preflight` first in the task worktree); every existing test still passes unchanged.
4. `git diff --stat`, excluding `plan/`, touches only `api/internal/sshkey/label.go` and `api/internal/sshkey/label_test.go`.

## References

- Finding `sQRd` in this plan's `plan/findings.yaml`.

## Status

- **Outcome:** succeeded
- **Date:** 2026-10-04
- **Summary:** Extended `isDisallowedLabelRune` in `api/internal/sshkey/label.go` to reject U+061C (ARABIC LETTER MARK), U+200E (LEFT-TO-RIGHT MARK), and U+200F (RIGHT-TO-LEFT MARK), completing coverage of Unicode's `Bidi_Control` set alongside the existing control-character and U+202A-U+202E/U+2066-U+2069 checks. Zero-width characters (U+200B/U+200C/U+200D/U+2060) remain allowed. Doc comment updated to state full `Bidi_Control` coverage.
- **Validation:** `cd api && go test ./internal/sshkey/... ./internal/service/...` passed (after `make preflight`); added table-driven rows to `label_test.go` for the three new runes (rejected when supplied, stripped from comment-derived defaults) plus a regression row confirming U+200D (ZWJ) is still accepted; added `isDisallowedLabelRune` rows for U+061C, U+200E, U+200F (true) and boundary runes U+200D, U+2010 (false). `git diff --stat` (excluding `plan/`) touches only `api/internal/sshkey/label.go` and `api/internal/sshkey/label_test.go`.
- **Affected source files:** `api/internal/sshkey/label.go`, `api/internal/sshkey/label_test.go`.
- **Security review (inline, per `review_focus`):** no findings — the change is a pure input-boundary validation hardening, matches Unicode's `Bidi_Control` set exactly, introduces no new sinks, and does not weaken any existing check.
