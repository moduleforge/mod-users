# Signer Wiring Guard And Key Revocation Docs

## Purpose and scope
Remediates plan findings QI2I and RmRX in this plan's `plan/findings.yaml`. Work targets the plan branch `plan/flow-accounts-upstream` and lands through the ordinary per-task loop.

## Requirements
1. Make a handler built without `SetSigner` safe under `JWT_ALG=EdDSA`: the auth handler (`api/internal/handlers/auth/register.go`) and `AssumeHandler` (`api/internal/handlers/assume.go`) must not silently mint HS256 tokens when the configured algorithm is EdDSA. Prefer a constructor variant taking a `Signer` (public constructors keep their existing signatures) and/or a clear failure on first use when no signer is set and EdDSA is configured.
2. Reject an empty secret in `NewHS256Signer` (`api/internal/auth/signer.go`), mirroring `WithHS256Secret`.
3. Build the OIDC callback's `Signer` once at handler construction (`api/internal/handlers/auth/oidc.go`) instead of per request, so a bad config fails at startup.
4. In `docs/jwt-eddsa.md`, document the required signer wiring for composing apps (`SetSigner(auth.NewSigner(cfg.LocalAuth))` or the new constructor variant).
5. In `docs/jwt-eddsa.md`, state that removing a key from `JWT_VERIFY_PUBLIC_KEYS` is what revokes it, including after reverting to HS256, and reconcile the apparent contradiction between the rotation step 4 text and the "not revocable by key removal alone before exp" sentence.

## Validation
1. `cd api && go build ./... && go vet ./... && go test ./...` passes.
2. New tests cover: handler without signer under EdDSA config fails (or uses the injected signer), empty-secret `NewHS256Signer` is rejected, OIDC signer built once.
3. `docs/jwt-eddsa.md` is reachable from README and the new text is present.

## References
- Finding QI2I in this plan's `plan/findings.yaml`.
- Finding RmRX in this plan's `plan/findings.yaml`.
