# Verified TLS Default

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Backlog items 8chg and UJev. Make OIDC issuer discovery and JWKS fetching verify TLS certificates by default, and add an explicit, off-by-default, local-development-only opt-in to skip verification. Scope: `api/internal/auth/jwt.go`, `api/auth/auth.go`, and tests. Background in [OIDC TLS research](../notes/oidc-tls-surface.md).

## Requirements

1. In `newVerifier` (`api/internal/auth/jwt.go`), remove the unconditional `InsecureSkipVerify` client. By default, call `oidc.NewProvider` with the unmodified context so the default verified transport is used.
2. Add a `VerifierOption` (suggested name `WithInsecureSkipTLSVerify`) that sets a new `verifierConfig` field. Only when it is set and the issuer starts with `https://` install the skip-verify client; log a `slog.Warn` stating TLS verification is disabled for the issuer (log the issuer host only). The doc comment must say local development only.
3. Re-export the option from `api/auth/auth.go` next to `WithOIDC`.
4. `NewVerifier` (positional signature) always verifies TLS; do not change its signature.
5. Do not touch `initProvider` in `oauth.go`.
6. Tests (use `httptest.NewTLSServer` serving a minimal OIDC discovery document at `/.well-known/openid-configuration` and an empty JWKS; its certificate is untrusted by the system pool):
   - Default: `NewVerifierWithOptions(ctx, WithOIDC(srv.URL, "cid"))` returns an error mentioning discovery.
   - `NewVerifier(ctx, srv.URL, "cid", "", "")` also fails.
   - With the opt-in option, construction succeeds.
   - The opt-in has no effect on an `http://` issuer (no panic, no warning required).

## Validation

- `cd api && go test ./internal/auth/... ./auth/...` passes, including the new tests.
- `cd api && go vet ./...` and `make lint` pass.
- `grep -rn InsecureSkipVerify api --include=*.go` shows it only inside the opt-in branch (and tests).
- The untrusted-certificate-fails-by-default test would fail against the old code (confirm by reasoning or a temporary revert).

## Metadata

architectural_impact: true

## Checkpoint hints

- After the internal option and `newVerifier` change
- After the public re-export
- After the tests
