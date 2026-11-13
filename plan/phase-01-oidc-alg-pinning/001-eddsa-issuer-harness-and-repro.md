# Ed25519 Test Issuer Harness and Baseline Reproduction

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Build a reusable in-package test issuer that mimics MFManager's IdP (Ed25519 key, OKP JWKS, discovery advertising `["EdDSA"]`, tokens with `typ: aud+jwt` and a `kid`), and use it to reproduce the reported failure against the current, unmodified verifier. This establishes what actually fails (the request's root cause is doubtful; see [verifier findings](../notes/oidc-verifier-findings.md)). Test-only changes in `api/internal/auth`.

## Requirements

- Add a test helper file (for example `api/internal/auth/eddsa_issuer_test.go`) with an `httptest` issuer: configurable discovery `id_token_signing_alg_values_supported`, JWKS built from Ed25519 keys as OKP (`kty: OKP, crv: Ed25519, x, kid, use: sig`), ability to publish several keys, and a token minting function accepting header `alg`/`typ`/`kid` and arbitrary claims (iss, sub, aud, email, email_verified, iat, exp, optional nonce). Mint with the existing helpers' conventions (`jwt_verify_keys_test.go` has `eddsaKey`, `signEdDSA`, `signHS256`; `oauth_test.go` has the RS256 fake IdP in `TestOAuth_EndToEnd`).
- Write `TestVerifier_OIDC_EdDSAAudienceToken` against `NewVerifierWithOptions(ctx, WithOIDC(issuer.URL, clientID))` with an `aud+jwt`/EdDSA token and report the result in your task report: pass or fail, and on fail the exact underlying go-oidc error (temporarily log `oidcVerifier.Verify`'s error in the test by calling the oidc verifier directly via `v.oidcVerifier.Verify`).
- Write the equivalent through `initProvider` (the login path) calling `state.Verifier.Verify` directly.
- Also record, in the report, whether the real `OAuth.Exchange` with an ID token lacking a `nonce` claim fails as "nonce mismatch" (do not change behaviour).
- If the baseline test passes on current code keep it as a permanent regression test; if it fails, mark it with the failing reason in a comment, keep it failing only until the next task lands (do not skip it), and halt-and-report if the cause is not algorithm related.

## Validation

- `cd api && go test ./internal/auth/... -run 'EdDSAAudience' -count=1` runs and the outcome is reported.
- `go vet ./internal/auth/` clean; no production files modified.
- The helper does not import any non-test package from outside the module beyond what `go.mod` already has (go-jose may be used only if already a direct dependency; otherwise hand-build the JWS with `crypto/ed25519` and base64url as the existing helpers do).

## Assumptions

go-oidc v3.18.0 already derives algorithms from discovery; the expected baseline outcome is therefore a pass for the facade verifier.

## References

- `api/internal/auth/jwt.go`, `api/internal/auth/oauth.go` (`initProvider`, `Exchange`), `api/internal/auth/jwt_tls_test.go` (`newOIDCTestServer`), `api/internal/auth/oauth_test.go`.

## Status

- Outcome: succeeded (2026-11-13).
- Baseline: EdDSA `aud+jwt` token with kid from an Ed25519/OKP issuer advertising `["EdDSA"]` is ACCEPTED by current code on both the facade path (`v.oidcVerifier.Verify` and `Verifier.Verify`) and the login path (`initProvider` verifier). Kept as permanent regression tests.
- `OAuth.Exchange` with an ID token lacking `nonce` fails with "oauth: nonce mismatch" (signature verifies first).
- Validation: `go test ./internal/auth/... -run EdDSAAudience` passes; `go vet ./internal/auth/` clean; no production files modified; no new dependencies.
- Files: `api/internal/auth/eddsa_issuer_test.go`.
