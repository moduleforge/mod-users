# EdDSA and Algorithm-Confusion Verification Tests

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Complete test coverage for the algorithm and typ behaviour delivered by the previous task, using the Ed25519 issuer harness from the first task. Test-only changes in `api/internal/auth` (new files such as `jwt_oidc_eddsa_test.go`, `oidc_algs_test.go`). **Security review emphasis: these tests are the evidence for the algorithm-handling review.**

## Requirements

Positive (through `NewVerifierWithOptions(WithOIDC)` and `initProvider`):
- EdDSA `aud+jwt` token, discovery `["EdDSA"]`, correct iss/aud/exp: accepted, claims returned (sub, email, email_verified).
- Same token with `typ: JWT` and with no typ, and with typ `AUD+JWT`: accepted.
- Key rotation: JWKS with two OKP keys, token selects by `kid`; unknown `kid` rejected.
- RS256 regression: RS256 issuer with discovery `["RS256"]` accepted; discovery listing both RS256 and EdDSA accepts either; discovery listing nothing falls back to RS256 and rejects EdDSA.
- Operator restriction: `WithOIDCSigningAlgs("EdDSA")` rejects an RS256 token from a dual-alg issuer and accepts EdDSA; empty intersection fails construction with a clear error.

Negative (each asserts rejection, and that the error text does not contain token content):
- `alg: none` (with an advertised `none` in discovery too), HS256 keyed with the Ed25519 public key bytes and common encodings (reuse the variants in `jwt_alg_confusion_test.go`), HS256 even when discovery advertises `HS256`.
- Algorithm not in discovery (RS256 token, discovery `["EdDSA"]`) and the reverse (EdDSA token, discovery `["RS256"]`).
- Key-type mismatch: JWKS entry is RSA while the token is EdDSA with the same `kid`, and the reverse.
- Wrong `aud`, wrong `iss`, expired, not-yet-valid if go-oidc enforces it, wrong signing key with the right `kid`, tampered payload.
- typ mismatch: `at+jwt`, `step-up`, `logout+jwt` rejected; `verifyCarrier` still rejects `aud+jwt` as a bearer token and now also `Aud+JWT`.
- Local path unchanged: a local-issuer EdDSA token still verifies through `verifyLocal`; an MFManager-style token with `iss != localIssuer` falls through to OIDC rather than being accepted locally.
- Unit tests of `resolveOIDCSigningAlgs` (table-driven) and of the `OIDC_ALLOWED_SIGNING_ALGS` parser in config.

## Validation

- `cd api && go test ./internal/auth/... ./internal/config/... -count=1` and `make test.unit` pass.
- Mutation check: temporarily removing the `SupportedSigningAlgs` assignment or the typ check makes at least the corresponding tests fail (report the result; revert the mutation).

## Assumptions

Depends on the harness task and on the algorithm pinning task being merged.
