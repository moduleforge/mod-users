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
