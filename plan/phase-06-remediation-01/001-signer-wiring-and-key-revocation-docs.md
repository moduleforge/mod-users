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

## Status
- Outcome: succeeded (2026-10-10).
- Validation: `cd api && go build ./... && go vet ./... && go test ./...` passed; new tests added; docs/jwt-eddsa.md linked from README.
- Changes: `NewHS256Signer` now returns `(*Signer, error)` and rejects an empty secret (internal API; `IssueLocalJWT`/`IssueAnonymousJWT`/`IssueAssumeJWT` propagate the error); new `NewWithSigner` / `NewAssumeHandlerWithSigner` constructors (internal and public facades) reject a nil signer, existing constructors unchanged; no-signer fallback fails on empty secret; `OIDCHandler` builds its signer once at construction (`SignerErr()`); `cmd/server/main.go` uses the new constructors; docs/jwt-eddsa.md gained "Signer wiring" and "Revocation" sections.
- Residual: a legacy-constructed handler with a non-empty secret and no `SetSigner` still falls back to HS256 (handlers do not see `JWT_ALG`); docs direct composing apps to the signer-requiring constructors.
