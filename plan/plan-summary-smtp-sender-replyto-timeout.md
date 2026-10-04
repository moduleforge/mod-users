# Plan Summary: smtp-sender-replyto-timeout

## What was planned and why

Harden `SMTPSender` in `api/internal/email/sender.go` (followup HI9B, origin flow-accounts followup Jpla) so a composing app's notices hook can drop its workarounds. Three changes: (1) Reply-To support; (2) honor context deadline/cancellation with a bounded dial; (3) no recipient address in error text.

In scope: only files under `api/` (hard constraint). Out of scope: changing the three consumer-side `Sender` interfaces' method set, config/env additions (SMTP_* vars), retry/queueing, HTML or multi-recipient mail, and edits to flow-accounts.

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

## What shipped

### Phase 01 — SMTP Sender Reply-To, Bounded Dial, and Address-Free Errors

1. **Harden SMTPSender: Reply-To, Context-Bounded Send, Address-Free Errors** (`001-harden-smtp-sender.md`, tier `sonnet-med`) — Replaced smtp.SendMail with an explicit context-bounded SMTP flow (DialContext, conn deadline, watcher goroutine that closes the connection on ctx.Done and is always joined). Added Message, MessageSender, Options, Reply-To and CR/LF validation, and address-free stage errors. Tests cover everything the task listed and are stable under -race -count=20.
   Commit `80afea3`, merged at `1de14a3076d6d9189a6c7c8b2340aea828201ade`.

### Phase 02 — Remediation Round 1

1. **Expose Message, MessageSender and Sender Options in the Public Email Facade** (`001-expose-sender-api-in-public-facade.md`, tier `haiku-med`) — Exposed the new sender API in the public api/email facade by adding type aliases for Message, MessageSender, and Option, re-exporting WithDialTimeout and WithSendTimeout, and updating NewSMTPSender to accept variadic opts. Backward compatible, scoped to api/email.
   Commit `4c74bfc`, merged at `335361f633cb23626b2308a91bd9a934ea0aeace`.

2. **Stuff Bare-CR Dots, Validate To as a Bare Address, Redact the Error Chain, and Cover Untested Branches** (`002-harden-sender-body-recipient-and-error-chain.md`, tier `sonnet-med`) — Hardened the SMTP sender against bare-CR dot-stuffing bypass, non-bare-address recipients, and address leakage through the Unwrap chain. Added tests for each, plus the previously uncovered STARTTLS, AUTH and redaction branches.
   Commit `e1c0127`, merged at `a2c0a0615451463589b469e38ea13feaf21e4970`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Findings

- **`Vdvt`** — **Root \`make lint\` fails in lint.model (goose m** — promoted — ref: `Vdvt` — 2026-10-04

- **`XFjp`** — **Public api/email facade lacks new sender API** — fixed — ref: `phase-02-remediation-01/001-expose-sender-api-in-public-facade.md` — 2026-10-04

- **`L7ky`** — **Body bare-CR dot not stuffed (SMTP smuggling)** — fixed — ref: `phase-02-remediation-01/002-harden-sender-body-recipient-and-error-chain.md` — 2026-10-04

- **`GS7Y`** — **To address only CR/LF-checked, not parsed** — fixed — ref: `phase-02-remediation-01/002-harden-sender-body-recipient-and-error-chain.md` — 2026-10-04

- **`Zr31`** — **sendError.Unwrap exposes unredacted cause** — fixed — ref: `phase-02-remediation-01/002-harden-sender-body-recipient-and-error-chain.md` — 2026-10-04

- **`KKhL`** — **Untested STARTTLS/AUTH/redaction branches** — fixed — ref: `phase-02-remediation-01/002-harden-sender-body-recipient-and-error-chain.md` — 2026-10-04

- **`QgEw`** — **Cause suppression checks top-level text only** — promoted — ref: `QgEw` — 2026-10-04

- **`1f4R`** — **From header not CR/LF-validated** — promoted — ref: `1f4R` — 2026-10-04

- **`Fsqn`** — **To bare-address rule undocumented** — promoted — ref: `Fsqn` — 2026-10-04

- **`ECRB`** — **Facade tests are smoke-only** — promoted — ref: `ECRB` — 2026-10-04

- **`ZZA5`** — **Strict To equality rejects quoted local parts** — promoted — ref: `ZZA5` — 2026-10-04

## Remediation

- Rounds used: 1 (resolved max_rounds: 3).
- Remediation tasks added: 2 (resolved max_added_tasks: 10).
- Remediation phases:
  - `remediation-01` — 2 task(s)
- Security review required: yes (at least one remediation phase carries `security_review: required`).

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — SMTP Sender Reply-To, Bounded Dial, and Address-Free Errors

- [x] [001-harden-smtp-sender.md](./phase-01-smtp-sender-hardening/001-harden-smtp-sender.md) — tier `sonnet-med` · branch `plan/smtp-sender-replyto-timeout-01-001` · commit `80afea3` · merge `1de14a3076d6d9189a6c7c8b2340aea828201ade`

### Phase 02 — Remediation Round 1

- [x] [001-expose-sender-api-in-public-facade.md](./phase-02-remediation-01/001-expose-sender-api-in-public-facade.md) — tier `haiku-med` · branch `plan/smtp-sender-replyto-timeout-02-001` · commit `4c74bfc` · merge `335361f633cb23626b2308a91bd9a934ea0aeace`
- [x] [002-harden-sender-body-recipient-and-error-chain.md](./phase-02-remediation-01/002-harden-sender-body-recipient-and-error-chain.md) — tier `sonnet-med` · branch `plan/smtp-sender-replyto-timeout-02-002` · commit `e1c0127` · merge `a2c0a0615451463589b469e38ea13feaf21e4970`
