# Plan: SMTP Sender Reply-To, Bounded Send, Address-Free Errors

## Purpose and scope

Harden `SMTPSender` in `api/internal/email/sender.go` (followup HI9B, origin flow-accounts followup Jpla) so a composing app's notices hook can drop its workarounds. Three changes: (1) Reply-To support; (2) honor context deadline/cancellation with a bounded dial; (3) no recipient address in error text.

In scope: only files under `api/` (hard constraint). Out of scope: changing the three consumer-side `Sender` interfaces' method set, config/env additions (SMTP_* vars), retry/queueing, HTML or multi-recipient mail, and edits to flow-accounts.

## Current status

Planned; no implementation started. Single phase, single task.

## Overview

Investigation ([findings](./notes/sender-investigation.md)): `email.Sender` (`Send(ctx, to, subject, textBody)`) is re-declared as a local interface in `api/internal/handlers/identities.go`, `api/internal/handlers/auth/register.go`, and test fakes in `handlers/auth/guards_test.go` and `handlers/identities_stepup_test.go`. Changing `Send`'s signature would break all of them, so the plan keeps `Send` unchanged and adds an additive, optional capability.

Success criteria:

- `Send` signature unchanged; all existing callers/fakes compile untouched.
- New `email.Message{To, Subject, TextBody, ReplyTo}` and `email.MessageSender` interface (`SendMessage(ctx, Message) error`), implemented by `SMTPSender` and `NoOpSender`; `Send` delegates to `SendMessage` with empty ReplyTo.
- `NewSMTPSender` stays source-compatible (variadic functional options added: dial timeout, overall send timeout, with sane defaults).
- Send uses a context-aware dial and an overall deadline (min of ctx deadline and send timeout); ctx cancellation aborts a hung relay; `errors.Is(err, context.DeadlineExceeded/Canceled)` works.
- Errors never contain the recipient, sender, or Reply-To address.
- Header-injection guard: CR/LF in To/Subject/ReplyTo is rejected.

Phase `smtp-sender-hardening` task list:

1. `001-harden-smtp-sender` (sonnet-med) — implement all three changes plus unit tests in `api/internal/email/`.

No parallelism (single task). No `doc-updates` phase: no docs under `docs/`, README, or AGENTS.md mention the sender, the package is `internal/`, and the change is additive with no spec-defined behavior change.
