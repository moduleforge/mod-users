# OIDC TLS Surface

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Records what the investigation found about where unverified TLS is reachable, so the implementing task does not re-derive it.

## Findings

- `api/internal/auth/jwt.go` `newVerifier` wraps the context with `oidc.ClientContext` using a client with `InsecureSkipVerify: true` whenever the issuer starts with `https://`. `verifierConfig` is populated by `VerifierOption` values; `WithOIDC(issuerURL, clientID)` sets the issuer.
- Public entry points in `api/auth/auth.go` re-export `NewVerifier`, `NewVerifierWithOptions`, and `WithOIDC`. Consumers (app-mfmanager, app-mftodo via `ResolveMFIDPVerifier`) call these.
- `NewVerifier` has a fixed positional signature and cannot take the opt-in. After the change it always verifies TLS. A consumer needing a self-signed dev issuer moves to `NewVerifierWithOptions` with the new option.
- The server binary (`api/cmd/server/main.go`) uses `NewLocalVerifier` and does not do OIDC discovery through this path.
- `api/internal/auth/oauth.go` `initProvider` calls `oidc.NewProvider` with the default client: already verified, no change.
- Local dev Authelia is reached as host `authelia` (via /etc/hosts), so a loopback-only restriction on the opt-in would break the documented dev stack. The opt-in is therefore an explicit option, not a hostname allowlist, and logs a warning when used.

## Design decision

Add `WithInsecureSkipTLSVerify()` (exact name is the implementer's call, kept clear and prefixed `Insecure`) in `api/internal/auth/jwt.go`, re-exported from `api/auth/auth.go`. Default off. `newVerifier` installs the skip-verify client only when the option is set and the issuer is `https://`, and logs a `slog.Warn`. Otherwise the default transport is used (no custom client).
