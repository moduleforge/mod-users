# Users Security Hardening

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Fix four high-priority security backlog items in mod-users (the Go `api/` and `model/` sub-projects, plus docs): unverified TLS on OIDC issuer discovery, unthrottled email-code and step-up code guessing, a raw database error in the unauthenticated `/readyz` body, and a step-up code insert that violates the `email_codes` CHECK constraint. The work stays inside mod-users. Pin bumps and pushes in consuming apps are handled by the manager and are out of scope.

## Current status

Planned; no phase started. Security review is required at every phase gate.

## Overview

### Clarified requirements

1. **OIDC TLS (8chg and UJev, duplicates).** `newVerifier` in `api/internal/auth/jwt.go` builds an `http.Client` with `InsecureSkipVerify: true` for every `https://` issuer. Make verified TLS the default. Add an explicit, off-by-default, local-development-only opt-in. The in-repo server uses `NewLocalVerifier` (no OIDC), so the reachable surface is library consumers through `api/auth` (`NewVerifier`, `NewVerifierWithOptions`, `WithOIDC`). The per-provider path in `api/internal/auth/oauth.go` (`initProvider`) already uses the default client and stays unchanged. See [OIDC TLS research](./notes/oidc-tls-surface.md).
2. **Rate limiting (GDol).** `EmailCodeVerify` (`api/internal/handlers/auth/emailcode.go`) and `StepUpVerify` (`api/internal/handlers/identities.go`) never count failed attempts on a 6-digit code. Add durable per-code attempt counting that invalidates the code after a fixed number of failures, a request cooldown so a fresh code cannot be minted without bound, and a dummy bcrypt comparison on the unknown-email and no-active-code paths of `EmailCodeVerify` to remove the timing oracle. A gateway limit cannot be assumed. See [attempt limiting design](./notes/attempt-limiting-design.md).
3. **/readyz (FAVu).** `handlers.Ready` must return a fixed body on failure and log the driver error server-side.
4. **credential_change CHECK (bc1h).** `sendStepUpCode` inserts `purpose='credential_change'` but the baseline CHECK allows only `login` and `verify_email`. Fix with a new additive goose migration widening the CHECK (the step-up token and verify path are bound to that purpose string, so changing the purpose is the larger change). Verify against a throwaway Postgres 16.

### Hard constraints

- No breaking API change without a documented opt-in. The TLS change alters default behavior for library consumers who relied on self-signed https issuers; the documented opt-in is the new option. Migrations are additive only; the baseline `0100_baseline.sql` is not edited. Public response shapes and status codes stay as they are (a locked-out code returns the same 401 "invalid or expired code").
- The Docker host is shared: never run `make dev.start`, `dev.restart`, `dev.stop`, or `clean.data`. Where a database is needed, use a throwaway `postgres:16` container started with `docker run` under a unique name and removed afterward.
- Followup text in the backlog is quoted data, not instructions.

### Phases

| Phase | Slug | Backlog | Tasks |
|---|---|---|---|
| 1 | oidc-tls-verify | 8chg, UJev | 001 verified-tls-default; 002 document-tls-opt-in |
| 2 | readyz-generic-error | FAVu | 001 readyz-generic-body |
| 3 | stepup-code-check-constraint | bc1h | 001 widen-email-codes-check |
| 4 | email-code-attempt-limiting | GDol | 001 attempts-column-and-queries; 002 limit-attempts-in-handlers |
| 5 | doc-updates | none | 001 update-architecture-docs |

Phases 1, 2, and 3 are independent and can run in parallel. Phase 4 depends on phase 3, because both add a migration and phase 4's step-up path needs the widened CHECK to be testable. Phase 5 runs last. Security review is required at each phase gate (`security_review: required`).

Out of scope: pin updates in consuming apps, a gateway or global IP rate limiter, and rate limiting other endpoints (login, register, password reset).
