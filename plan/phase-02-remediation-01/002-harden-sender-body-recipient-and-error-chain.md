# Stuff Bare-CR Dots, Validate To as a Bare Address, Redact the Error Chain, and Cover Untested Branches

## Purpose and scope

Remediates findings L7ky, GS7Y, Zr31, and KKhL (in this plan's `plan/findings.yaml`) in `api/internal/email/sender.go` and its tests. The work targets the plan branch `plan/smtp-sender-replyto-timeout` and lands through the ordinary per-task loop. Edits are confined to `api/internal/email/`.

## Requirements

1. L7ky: before the payload is written, normalize TextBody line endings (CRLF to LF, then any remaining bare CR to LF) so `net/textproto` dot-stuffing and CRLF conversion apply uniformly. No bare CR reaches the wire.
2. GS7Y: in the validate stage, reject a To that `net/mail.ParseAddress` cannot parse, or whose parsed `.Address` differs from To (bare addr-spec only: no display name, no angle brackets, no comma list). Return the address-free error `smtp: validate: invalid recipient address` and make no connection.
3. Zr31: in `fail()`'s default branch, set `cause` only when the unredacted `err.Error()` contains none of `t.secrets`; otherwise leave `cause` nil. Context and deadline causes are unchanged. Update the `sendError` doc comment to say the Unwrap chain is address-free.
4. KKhL: extend the in-process fake server and add tests for STARTTLS handshake failure (the `smtp: starttls:` error has no address), AUTH configured but not advertised (`smtp: auth: server does not support AUTH`), AUTH 535 (error shows code 535 and not the server text), and the redaction branch (an error carrying an address yields neither address text in `Error()` nor an unredacted Unwrap chain). An RCPT 550 racing ctx cancellation test may be added only if it can synchronize deterministically (no sleeps).
5. Add tests for L7ky (a body containing `\r.\r\n` reaches the DATA payload dot-stuffed with no bare CR) and GS7Y (`a@x> NOTIFY=NEVER <b`, `Name <a@x>` and `a@x, b@y` are rejected with no connection made).
6. Do not change `Send`/`SendMessage`/`NewSMTPSender` signatures, and do not touch `api/handlers`.

## Validation

1. `cd api && go test -race ./internal/email/... -count=20` passes with no flakes.
2. `make build.api` and `make test` succeed; `make lint.api` is clean.
3. `grep -n 'send to' api/internal/email/sender.go` finds nothing; `git diff --stat` shows only `api/internal/email/` files.

## References

- Findings L7ky, GS7Y, Zr31, KKhL, in this plan's `plan/findings.yaml`.
- `api/internal/email/sender.go`, `api/internal/email/sender_test.go`.
