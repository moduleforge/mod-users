# Harden SMTPSender: Reply-To, Context-Bounded Send, Address-Free Errors

## Purpose and scope

Implement followup HI9B in `api/internal/email/sender.go` and add tests in `api/internal/email/sender_test.go`. Edit only files under `api/`. Do not change the `Send` method signature on `Sender`, and do not touch the consumer-side interfaces or fakes in `api/internal/handlers/`. Background: [sender investigation](../notes/sender-investigation.md).

## Requirements

1. **Additive API.** Add `type Message struct { To, Subject, TextBody, ReplyTo string }` and `type MessageSender interface { SendMessage(ctx context.Context, msg Message) error }`. Implement `SendMessage` on `*SMTPSender` and on `NoOpSender` (value receiver, returns nil). `(*SMTPSender).Send(ctx, to, subject, body)` becomes a thin wrapper calling `SendMessage` with empty `ReplyTo`. Leave `Sender` unchanged. Add compile-time assertions (`var _ Sender = ...`, `var _ MessageSender = ...`).
2. **Reply-To.** When `ReplyTo != ""`, emit a `Reply-To: <value>` header; omit the header when empty. Validate with `net/mail.ParseAddress`; on failure return an address-free error (see 5). From stays fixed at construction. The envelope sender remains `s.from`.
3. **Options, backward compatible.** Change `NewSMTPSender` to `NewSMTPSender(host string, port int, from, user, pass string, opts ...Option)`; existing call in `api/cmd/server/main.go` must compile unchanged. Provide `type Option func(*SMTPSender)`, `WithDialTimeout(d time.Duration)` (default 10s) and `WithSendTimeout(d time.Duration)` (overall cap for the whole transaction, default 30s). Non-positive values leave the default.
4. **Context-bounded send.** Replace `smtp.SendMail` with an explicit flow: `net.Dialer{Timeout: dialTimeout}.DialContext(ctx, "tcp", addr)`; derive the effective deadline as the earlier of ctx's deadline and now+sendTimeout and apply it with `conn.SetDeadline`; also close the connection promptly on `ctx.Done()` (small watcher goroutine that is always cleaned up, no goroutine leak). Then `smtp.NewClient`, `Hello` as needed, STARTTLS when the server advertises it (with `tls.Config{ServerName: host}`, matching `smtp.SendMail` behavior), `Auth` when `s.auth != nil`, `Mail(s.from)`, `Rcpt(to)`, `Data` write, `Quit`. When ctx is done, return an error for which `errors.Is(err, ctx.Err())` holds (map the post-close I/O error to `ctx.Err()`).
5. **Address-free errors.** Remove the recipient from error text; format as `smtp: <stage>: ...` where stage is one of dial, starttls, auth, mail, rcpt, data, quit, validate. Relay replies commonly echo addresses (e.g. `550 5.1.1 <user@x>: Recipient address rejected`), so for `*textproto.Error` report only the numeric code and enhanced-status-free generic text (for example `smtp: rcpt: server replied 550`), not the server's message. Network errors may keep the relay host:port (not a recipient) but must be checked to not include To/ReplyTo/From. Never include To, ReplyTo, or From in any returned error.
6. **Header-injection guard.** Reject (stage `validate`) any `To`, `Subject`, or `ReplyTo` containing `\r` or `\n`. Keep the existing message format otherwise (To, From, Subject, MIME-Version, Content-Type, blank line, body), adding Reply-To when set.
7. **Tests** (`api/internal/email/sender_test.go`, standard library only, in-process fake SMTP server on `127.0.0.1:0` listening via `net.Listen`; no network, no Postgres):
   - Reply-To header present in the DATA payload when set, absent when empty; `Send` produces no Reply-To.
   - Hung relay (server accepts then never speaks): `SendMessage` with a short ctx deadline returns within a bounded time with `errors.Is(err, context.DeadlineExceeded)`; likewise with ctx cancelled mid-send (`context.Canceled`). Also a `WithSendTimeout` cap test with a background ctx.
   - Dial to a closed port returns promptly with an address-free error.
   - Relay rejecting RCPT with a message echoing the recipient: returned error text contains neither the recipient address nor the server message; contains the code.
   - Invalid Reply-To and CR/LF injection are rejected without any connection being made.
   - `NoOpSender` implements both interfaces.
   Use short timeouts (hundreds of ms) and `t.Parallel()` only where safe; no sleeps-as-synchronization flakiness.
8. Update the doc comments on `Sender`, `SMTPSender`, and the new symbols (what ctx controls, that errors are address-free). No other docs need changes.

## Validation

Run from the plan's task worktree per AGENTS.md; write command output to a log under `<project_root>/.flow/validation-logs/` and report only the path and a bounded tail.

- `make build.api` succeeds (confirms `main.go` and all existing consumers compile unchanged).
- `make test` (unit tests, includes `api/`) passes; `cd api && go test -race ./internal/email/...` passes, repeated with `-count=20` to confirm the timeout tests are not flaky.
- `make lint` is clean.
- `grep -n 'send to' api/internal/email/sender.go` finds no remaining recipient-embedding error format.
- `git diff --stat` shows changes only under `api/` and `api/internal/handlers/` files are untouched.

## Metadata

architectural_impact: false

## Assumptions

- `api/internal/email` is an internal package; the only in-repo constructor call is `api/cmd/server/main.go`, which needs no edit. Wiring Reply-To into real flows (verification/reset emails) is out of scope; the composing app uses `MessageSender` by type assertion.
- Defaults of 10s dial and 30s total are acceptable; no new env/config knobs.
