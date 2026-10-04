# Sender investigation

## Purpose and scope

Records what was found in `api/` about the email sender before planning HI9B.

## Findings

- `api/internal/email/sender.go`: `Sender` interface, `SMTPSender` (host, port, from, auth), `NoOpSender`. No tests exist in the package.
- `SMTPSender.Send` ignores ctx and calls `smtp.SendMail`, which dials with no timeout and has no cancellation hook. Errors are wrapped `smtp: send to %s: %w` (recipient embedded). Underlying `*textproto.Error` text from the relay often echoes the address too (e.g. "550 5.1.1 <addr>: Recipient address rejected"), so merely changing the wrap is insufficient.
- Construction site: `api/cmd/server/main.go` (~line 377) `email.NewSMTPSender(host, port, from, user, pass)` when `cfg.SMTP.Host != ""`, else `email.NoOpSender{}`.
- Consumers declare their own identical interface rather than importing `email.Sender`: `api/internal/handlers/identities.go:57`, `api/internal/handlers/auth/register.go:25`. Test fakes: `api/internal/handlers/auth/guards_test.go` (recordingSender), `api/internal/handlers/identities_stepup_test.go` (fakeSender). Call sites: identities.go:560, auth/emailcode.go:116, auth/reset.go:84. Changing `Send` would break all of these, hence additive design.
- The flow-accounts composing app (outside this repo) type-asserts or wraps; it can use `email.MessageSender` via type assertion on the `Sender` it holds.
- No docs mention the sender (grep of docs/, README.md, AGENTS.md, api/**/*.md).
- `smtp.PlainAuth` refuses to send credentials over a non-TLS, non-localhost connection; manual client flow must keep STARTTLS-when-advertised behavior that `smtp.SendMail` provides.
