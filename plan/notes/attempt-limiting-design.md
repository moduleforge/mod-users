# Attempt Limiting Design

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Design for the email-code and step-up rate limiting, so the schema task and the handler task agree on names and semantics.

## Findings

- `email_codes` (baseline) has no attempts column. `GetActiveEmailCode` returns the newest unconsumed, unexpired code for (user, purpose). `ConsumeEmailCode` sets `consumed_at`.
- `EmailCodeVerify` skips bcrypt for an unknown email and when no active code exists, while `Login` uses a dummy hash. That is the timing oracle in the backlog item.
- `EmailCodeRequest` and `StepUpRequest` mint a new code on every call, so per-code attempt limits alone could be reset by requesting new codes.
- Handler tests use in-memory fake query implementations (for example `q.emailCodes` in `identities_stepup_test.go`); adding methods to the sqlc `Querier` interface requires updating those fakes.
- The server must not trust X-Forwarded-For (see the comment in `api/internal/server/server.go`), so the limit is keyed by account and code, not client IP.

## Design

- Migration `0102`: `ALTER TABLE email_codes ADD COLUMN failed_attempts INT NOT NULL DEFAULT 0`. Down migration drops the column.
- New sqlc query `ClaimEmailCodeAttempt :one`: `UPDATE email_codes SET failed_attempts = failed_attempts + 1, consumed_at = CASE WHEN failed_attempts + 1 >= $2 THEN now() ELSE consumed_at END WHERE id = $1 AND consumed_at IS NULL AND failed_attempts < $2 RETURNING ...`. Claim an attempt before comparing the code, so concurrent guesses cannot exceed the bound. No row returned means the code is exhausted: respond with the same 401 as a wrong code.
- On a correct code the code is consumed as today.
- Constant `maxEmailCodeAttempts = 5`, shared by both handlers (one package-level constant in a location both packages can import, or two constants with a comment and a test asserting equality; implementer chooses).
- Request cooldown: the function `create_email_code_with_cooldown` (called via sqlc query `CreateEmailCodeWithCooldown`) atomically skips minting when any code for the same (user, purpose), in any state (live, exhausted, or consumed), was created within the last 60 seconds. Any-state semantics keep an exhausted or consumed code from being bypassed by requesting a fresh one. The request endpoints still return 204 after the constant ~200 ms, so the cooldown is not observable.
- Timing: in `EmailCodeVerify`, run `bcrypt.CompareHashAndPassword` against a package-level dummy hash (generated once at init at `bcrypt.DefaultCost`) for the unknown-email and no-active-code paths, then return the same 401.
