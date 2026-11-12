# Consumer Upgrade Notes, Config Reference and Troubleshooting

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Document the change for consumers and operators. This repo has no CHANGELOG file; `docs/consumer-upgrade-notes.md` is the change-log convention (one section per change, e.g. "OIDC issuer TLS verification"). Docs only.

## Requirements

- `docs/consumer-upgrade-notes.md`: add a section (after "OIDC issuer TLS verification") covering: EdDSA/OKP providers accepted; algorithm set is discovery intersected with the allowlist, `none`/`HS*` never; RS256 unchanged; `WithOIDCSigningAlgs` and `OIDC_ALLOWED_SIGNING_ALGS` for restriction; typ policy (absent, `JWT`, `aud+jwt` accepted, others rejected) and that the bearer-path audience-token rejection is now case-insensitive; the removed `auth.NewVerifier(ctx, issuer, clientID, secret, localIssuer)` constructor and its replacement `NewVerifierWithOptions(ctx, WithOIDC(issuer, clientID))` for consumers moving to this version; nonce must be echoed by the provider for browser login.
- README configuration reference (`README.md`, the configuration table): add `OIDC_ALLOWED_SIGNING_ALGS`; `.env.example` if not done earlier.
- `docs/oidc-troubleshooting.md`: add the failure modes (algorithm not advertised/allowed, typ rejected, JWKS key type mismatch, nonce mismatch) under the refusals section, noting verify errors never echo token content.

## Validation

- Edited docs render, anchors used by other docs still exist (grep for `consumer-upgrade-notes.md#` references).
- Env var name and option name match the code exactly (grep `OIDC_ALLOWED_SIGNING_ALGS`, `WithOIDCSigningAlgs`).
- No links into `plan/`.

## Assumptions

Phase 1 is merged so names are final.
