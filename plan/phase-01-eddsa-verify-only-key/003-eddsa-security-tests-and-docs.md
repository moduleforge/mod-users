# 003 Alg-Confusion Tests And Docs

## Purpose and scope
Lock in the security properties of tasks 001-002 and document the config.

## Requirements
1. Tests: token signed with the wrong Ed25519 key rejected; `alg=none` rejected; HS256 token signed using the public key bytes as the secret rejected in public-key-only mode; EdDSA token rejected when verifier is HS256-only; expired / wrong-issuer tokens still rejected.
2. Docs (`docs/mod-users-spec.md`, `docs/architecture.md` as applicable, README config table): EdDSA is the chosen asymmetric alg, config names, key generation example, rotation procedure, verify-only deployment, flow-accounts adoption notes.
3. Add a followup/plan note per repo conventions if anything is deferred.

## Validation
- `cd api && go test ./...` passes including new negative tests.
- Docs link-check: every new doc is reachable from README link chain.

## Status
- Outcome: succeeded (2026-10-10).
- Validation: `cd api && go build/vet/test ./...` passed (log: .flow/validation-logs/01-go-test.log). New doc `docs/jwt-eddsa.md` is linked from README.md (and from architecture.md and mod-users-spec.md).
- Tests added (extending task 002's alg-confusion coverage): `api/internal/auth/jwt_alg_confusion_test.go` (public key as HMAC secret in raw/DER/PEM/base64/hex forms, mixed-mode wrong key/expiry/issuer, non-string kid).
- Docs: `docs/jwt-eddsa.md` (new), `README.md` config table, `docs/architecture.md`, `docs/mod-users-spec.md`, `.env.example` (four new vars).
- Nothing deferred.
