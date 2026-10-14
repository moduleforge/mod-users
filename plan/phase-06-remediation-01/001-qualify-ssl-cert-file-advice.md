# Qualify SSL_CERT_FILE Advice For macOS In OIDC Troubleshooting

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Remediate finding `eyu1` (recorded in this plan's plan/findings.yaml): the self-signed issuer certificate remedy in the OIDC troubleshooting doc tells developers to point `SSL_CERT_FILE` at a PEM bundle without qualifying the platform. On macOS, Go's certificate verification does not honor `SSL_CERT_FILE`, so the advice does not work there. This task corrects that remedy so macOS developers are told to trust the issuer CA through the macOS keychain.

Scope: `docs/oidc-troubleshooting.md` only. The work targets the plan branch plan/users-security-hardening and lands through the ordinary per-task loop.

## Requirements

1. In the "Self-signed issuer certificate" section of `docs/oidc-troubleshooting.md`, state that `SSL_CERT_FILE` applies on Linux and BSD, not macOS.
2. In the same section, tell macOS developers to add the issuer CA to the macOS keychain (system trust) instead.
3. Keep the dev-only `WithInsecureSkipTLSVerify()` option presented as the last resort for local development only.

## Validation

1. Read `docs/oidc-troubleshooting.md` "Self-signed issuer certificate" section and confirm the platform qualification and keychain guidance are present.
2. Confirm no links into `plan/` were added and that the markdown style matches the surrounding entries.

## References

- Finding `eyu1` in this plan's plan/findings.yaml (type:security).
