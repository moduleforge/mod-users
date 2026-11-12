# Pin OIDC Signing Algorithms and Enforce typ Policy

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Security-sensitive change. Make the accepted OIDC signature algorithms explicit in both OIDC verification paths instead of relying on go-oidc defaults, add the operator restriction, and enforce the typ policy. Files: `api/internal/auth/jwt.go`, `api/internal/auth/oauth.go` (`initProvider`), `api/internal/auth/carrier.go` (typ comparison only), `api/auth/auth.go` (facade option), `api/internal/config` (env var). **Security review emphasis: algorithm handling.** Follow the decisions in [overview](../overview.md) (Decisions recorded 1 to 5).

## Requirements

- New internal helper (new file, e.g. `api/internal/auth/oidc_algs.go`): `resolveOIDCSigningAlgs(discovered, operatorAllow []string) ([]string, error)` implementing decision 1: fixed allowlist {RS256, RS384, RS512, PS256, PS384, PS512, ES256, ES384, ES512, EdDSA}; intersect with discovery (read from `provider.Claims` into a struct with `id_token_signing_alg_values_supported`); if discovery lists none from the allowlist, fall back to RS256 only; intersect with `operatorAllow` when non-empty; empty result is an error naming the issuer and never echoing token data; `none`, `HS*` and unknown names never survive. Deterministic output order.
- Set `oidc.Config.SupportedSigningAlgs` from the helper in `newVerifier` (jwt.go) and `initProvider` (oauth.go); a resolve error fails construction (for `initProvider`: captured in `state.Err`, `InitOK=false`, like a discovery error).
- Verifier option `WithOIDCSigningAlgs(algs ...string)` in jwt.go (validated at option time: reject `none`, `HS*`, unknown names), re-exported from `api/auth/auth.go` with a doc comment; update `api/auth/facade_test.go` signature assertions as that file's pattern requires.
- Config: `OIDC_ALLOWED_SIGNING_ALGS` (comma separated, optional) parsed in `api/internal/config` with the same validation, reported with the other config problems at startup, carried to `initProvider` through `cfg`; add `.env.example` entry if that file lists OIDC settings.
- typ policy (decision 4): add `checkOIDCTokenTyp(rawToken string) error` that reads the JWS protected header (reuse `rawTokenTyp`), accepts absent, `JWT` and `aud+jwt` case-insensitively and rejects any other value with a fixed error string. Call it before `oidcVerifier.Verify` in `Verifier.Verify` and before `Verifier.Verify` in `Exchange`. Replace the exact-case comparison in `carrier.go` (`rawTokenTyp(rawToken) == audienceTokenTyp`) with `strings.EqualFold` so one definition of typ equality exists.
- Do not touch `verifyLocal`, `verifyLocalSignature`, `verifyCarrier` semantics, issuer/audience/expiry/nonce checks, or TLS handling. Local-first, OIDC-fallback ordering in `Verify` is unchanged. Log nothing token-derived.
- Update the `Verifier` doc comment (jwt.go line ~30, "OIDC (RS256 via JWKS...") to describe the pinned set.

## Validation

- `cd api && go build ./... && go vet ./... && go test ./internal/auth/... ./internal/config/... ./auth/... -count=1` pass (run `make preflight` first inside the worktree for sibling resolution).
- `make test.unit` passes.
- grep confirms no remaining `oidc.Config{ClientID` construction without `SupportedSigningAlgs` under `api/` (non-test).
- The Task 001 baseline tests still pass; RS256 regression tests (`TestOAuth_EndToEnd`, `jwt_test.go`) pass unchanged.

## Metadata

architectural_impact: true

## Assumptions

Task 001's findings are available in its report; if the baseline showed a non-algorithm failure, address only what the report says is in this repo and flag the rest.

## Checkpoint hints

- After the algorithm helper and unit tests of it.
- After wiring both verification paths.
- After the config env var and facade option.
- After the typ check and carrier change.
