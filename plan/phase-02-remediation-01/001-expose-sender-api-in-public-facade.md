# Expose Message, MessageSender and Sender Options in the Public Email Facade

## Purpose and scope

Remediates finding XFjp (in this plan's `plan/findings.yaml`): the public `api/email` facade does not expose the new sender API, so external consumers cannot set timeouts or use Reply-To. The work targets the plan branch `plan/smtp-sender-replyto-timeout` and lands through the ordinary per-task loop. Edits are confined to `api/email/`.

## Requirements

1. `api/email` adds type aliases `Message`, `MessageSender`, and `Option` for the `internal/email` types.
2. `api/email` re-exports `WithDialTimeout` and `WithSendTimeout`, with the same behavior as the internal functions.
3. The facade's `NewSMTPSender` becomes `NewSMTPSender(host string, port int, from, user, pass string, opts ...Option)` and forwards `opts`. Existing callers stay source-compatible.
4. Update the package and symbol doc comments to say what is re-exported. Do not edit `api/internal/email` or `api/handlers`.

## Validation

1. `make build.api` succeeds; `api/handlers/authhandlers` and `api/cmd/server` compile unchanged.
2. A test in `api/email` asserts `var _ email.MessageSender = (*email.SMTPSender)(nil)` holds, and that `NewSMTPSender(..., email.WithDialTimeout(d), email.WithSendTimeout(d))` compiles and returns non-nil.
3. `cd api && go test ./email/...` passes; `make lint.api` is clean; `git diff --stat` shows only `api/email/` files.

## References

- Finding XFjp, in this plan's `plan/findings.yaml`.
- `api/email/email.go`, `api/internal/email/sender.go`.

## Status

**Outcome:** succeeded

**Date:** 2026-10-04

**Implementation summary:** Exposed the sender API in the public `api/email` facade by adding type aliases for `Message`, `MessageSender`, and `Option`, and re-exporting `WithDialTimeout` and `WithSendTimeout`. Updated `NewSMTPSender` to accept variadic `opts ...Option` parameter and forward it to the internal constructor, maintaining backward compatibility with existing callers.

**Files modified:**
- `api/email/email.go` — added type aliases, re-exported functions, updated NewSMTPSender signature
- `api/email/email_test.go` — created with tests for interface compliance and new signature

**Validation results:**
1. `make build.api` — passed
2. Interface compliance and new signature test — passed
3. `cd api && go test ./email/...` — passed (0 failed, 0 skipped)
4. `make lint.api` — passed (gofmt, go vet clean)
5. `git diff --stat` — only `api/email/` files modified

**Correctness review:** All requirements met. Type aliases and re-exports are simple facades over internal types. NewSMTPSender signature change is backward compatible — existing callers without options still work. No new error paths or edge cases. Tests verify interface compliance and the new signature works as intended.
