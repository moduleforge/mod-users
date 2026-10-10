# 002 Public-Key Verifier Mode And Rotation

## Purpose and scope
Let `NewVerifier` be built with only an Ed25519 public key (no signing secret), with multi-key `kid` rotation. Backward compatible.

## Requirements
1. Keep `NewVerifier(ctx, issuerURL, clientID, jwtSecret, localIssuer)` in `api/internal/auth/jwt.go` and `api/auth/auth.go` working. Add an option-based constructor (e.g. `NewVerifierWithOptions` / `WithEd25519PublicKeys(map[kid]ed25519.PublicKey)`), re-exported from `api/auth`.
2. Config for verify-only services: `JWT_VERIFY_PUBLIC_KEYS` (kid→PEM set; single key without kid also allowed). A verify-only process needs no JWT_SECRET; wire `api/cmd/server/main.go` accordingly.
3. `verifyLocal` accepts exactly the configured algs: pins `EdDSA` for public-key mode, `HS256` for secret mode, both if both configured; selects key by `kid`; rejects unknown kid.
4. Rotation: multiple public keys valid concurrently; removing a kid rejects its tokens.

## Validation
- Unit tests: verify with public key only; unknown kid rejected; rotation with two kids; HS256 path unchanged.
- `cd api && go test ./...`.

## Status
- Outcome: succeeded (2026-10-10).
- Added `NewVerifierWithOptions` with `WithOIDC`, `WithHS256Secret`, `WithLocalIssuer`, `WithEd25519PublicKeys`, `WithEd25519PublicKey`, and `NewLocalVerifier(LocalAuthConfig)`; re-exported from `api/auth/auth.go`. `NewVerifier` signature unchanged in both copies.
- `verifyLocal` pins alg by configured key material (HS256 needs a non-empty secret, EdDSA needs public keys; `none`/other algs rejected), selects EdDSA key by `kid`, rejects unknown kid.
- `JWT_VERIFY_PUBLIC_KEYS` (single PKIX PEM, or JSON kid-to-PEM map) parsed in `api/internal/config/jwt_signing.go` into `LocalAuthConfig.JWTVerifyPublicKeys`.
- `api/cmd/server/main.go` now uses `NewLocalVerifier`: under `JWT_ALG=EdDSA` it accepts only EdDSA (signing key's public half plus verify keys); HS256 otherwise.
- Validation: `cd api && go vet ./... && go test ./...` passed (log: `.flow/validation-logs/02-go-test.log`).
