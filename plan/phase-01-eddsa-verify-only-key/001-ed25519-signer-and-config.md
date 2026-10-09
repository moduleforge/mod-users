# 001 Ed25519 Signer, Key Config And kid

## Purpose and scope
Let the login signer sign local tokens with an Ed25519 private key (alg `EdDSA`) while HS256 keeps working. Covers signer, config, and `kid` header. Verifier is task 002.

## Requirements
1. Add config (names documented in task 003): `JWT_SIGNING_KEY` (PKCS#8 PEM or base64 seed, pick one and document), `JWT_SIGNING_KID`, `JWT_ALG` (`HS256` default | `EdDSA`). Validate at startup: EdDSA requires a key; HS256 requires JWT_SECRET (existing bootstrap in `jwtsecret_bootstrap.go` unchanged when HS256).
2. Refactor the three `SigningMethodHS256` sites in `api/internal/auth/local_jwt.go` behind one signer abstraction selecting method/key by config; set the `kid` header when configured.
3. Default behaviour (no new config) is byte-for-byte unchanged.
4. Do not log key material.

## Validation
- Unit tests: EdDSA-signed token has `alg=EdDSA` and `kid`; HS256 default unchanged; bad/missing key fails config load.
- `cd api && go test ./...` and `make lint` (note known pre-existing lint.model failure).
