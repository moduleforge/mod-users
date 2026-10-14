# Document TLS Opt-In

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Document the changed default and the new opt-in so library consumers can migrate. Scope: `docs/consumer-upgrade-notes.md`, `docs/oidc-troubleshooting.md`, `docs/mod-users-spec.md` (security requirements), and `.env.example`/README only if they mention OIDC TLS. Depends on task 001.

## Requirements

1. Add an entry to `docs/consumer-upgrade-notes.md` (follow its existing format): OIDC issuer discovery now verifies TLS by default; consumers that relied on a self-signed https issuer in local development must use `NewVerifierWithOptions` with `WithOIDC` and the new opt-in option (name from task 001). State that the option is for local development only and logs a warning.
2. Add a self-signed-certificate troubleshooting entry to `docs/oidc-troubleshooting.md` (symptom: discovery error `x509: certificate signed by unknown authority`; remedies: trust the CA via the system store or `SSL_CERT_FILE`, or the dev-only option).
3. Add one sentence to the spec's security requirements stating issuer discovery and JWKS fetches verify TLS.
4. Do not edit plan docs from non-plan docs, and do not link into `plan/`.

## Validation

- Each named doc contains the new text; grep for the option name matches the code.
- The upgrade note names the old behavior, the new default, and the opt-in.
- Markdown style per repo conventions; no links into `plan/`.

## Metadata

architectural_impact: false

## Status

succeeded (2026-10-14). Added entries to `docs/consumer-upgrade-notes.md`, `docs/oidc-troubleshooting.md`, and `docs/mod-users-spec.md`; option name `WithInsecureSkipTLSVerify` confirmed against `api/auth/auth.go`. README and `.env.example` do not mention OIDC TLS. Greps pass; no links into `plan/`.
