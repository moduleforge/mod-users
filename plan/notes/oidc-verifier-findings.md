# OIDC Verifier Findings

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Records what the code and the vendored libraries actually do today, checked against the request's FINDING about EdDSA owner-login tokens. All paths are relative to `api/` of the project root.

## Two verification paths

- `internal/auth/jwt.go` `newVerifier` builds `provider.Verifier(&oidc.Config{ClientID: clientID})` for the facade `auth.NewVerifierWithOptions(WithOIDC(...))` (the path consumers such as app-mftodo use). `Verifier.Verify` tries `verifyLocal` first (EdDSA, only when `iss == localIssuer` and local keys are configured), then falls through to the OIDC verifier. Every OIDC failure is swallowed into the fixed error `auth: token verification failed`.
- `internal/auth/oauth.go` `initProvider` builds a second, separate `provider.Verifier(&oidc.Config{ClientID, SkipIssuerCheck})` per login provider; `Exchange` (line ~534) calls `Verify`, then enforces the Microsoft issuer pattern, then `idToken.Nonce == payload.Nonce`, then maps claims. The request's "OIDC login path" is this one; it must get the same fix.

## The finding is partly wrong for the pinned library versions

- `go.mod` pins go-oidc v3.18.0 and go-jose v4.1.4 (indirect). go-oidc v3.18.0 `Provider.Verifier` (`oidc/verify.go` `newVerifier`) copies the discovery document's `id_token_signing_alg_values_supported`, filtered to its `supportedAlgorithms` map (RS*, PS*, ES*, EdDSA; HS* and none are dropped at discovery), into `SupportedSigningAlgs` when the config leaves it empty. RS256 is only the fallback when discovery lists no supported algorithm. So a provider advertising `["EdDSA"]` is already verified as EdDSA. The commit the consumers pin (eb41f08) has the same call and the same library versions.
- go-oidc `RemoteKeySet` / go-jose v4.1.4 parse OKP/Ed25519 JWK (`jwk.go`, `case "OKP"`). No dependency bump is needed for EdDSA/OKP.
- go-oidc and go-jose do not read or check the JWS `typ` header.
- Conclusion: the plan cannot assume the stated root cause. The first task reproduces the failure against current code with a real Ed25519 issuer. Whatever the outcome, the pinning work stays valid: it makes the accepted algorithm set explicit, operator-restrictable, and independent of library defaults, rather than implicit.

## Other candidate causes to rule out in the reproduction (not verified)

- Nonce: `Exchange` rejects an ID token whose `nonce` differs from the state's nonce. The request lists the minted claims (iss, sub, aud, email, email_verified, 5-minute lifetime) with no `nonce`; `IssueAudienceToken` takes only `extra` claims. If MFManager's token endpoint does not echo the authorization request's nonce, login fails with "nonce mismatch" regardless of algorithm. Nonce enforcement stays (the request says keep it); this is a consumer-side follow-on.
- Consumer API drift: `auth.NewVerifier(ctx, issuer, clientID, secret, localIssuer)` as called by app-mftodo exists at pinned commit eb41f08 but was removed at HEAD (only `NewVerifierWithOptions` remains). A pin bump forces the consumer to `NewVerifierWithOptions(ctx, WithOIDC(issuer, clientID))`.
- Claim mapping for provider id `mfidp` (claim style, `email_verified`, `jit_mode none` needing a pre-linked (issuer, sub) credential).

## typ handling

- `typ: aud+jwt` is set by `IssueAudienceToken` (`internal/auth/carrier.go`, `audienceTokenTyp`); the bearer path rejects it via `rawTokenTyp`, exact-case compare (open followup dvet suggests `strings.EqualFold`).
- OIDC-verified tokens currently ignore `typ`.

## Related followups (project store)

- UJev and 8chg (OIDC verifier skipped TLS verification): already fixed in code (`WithInsecureSkipTLSVerify` opt-in, `jwt_tls_test.go`); the followup entries look stale. Left alone; mentioned to the manager.
- ASqb (opt-in could be restricted to dev hosts): unrelated, left alone.
- dvet (typ EqualFold in carrier.go): touched. The new typ helper is shared and the carrier comparison moves onto it, which resolves dvet; the manager decides whether to close it.
- 4 other OIDC followups (config UI nits, confirm drops unknown ids, link-insert race, unlink 409 ordering) are unrelated and left alone.

## Consumption of mod-users

- app-mfmanager `versions.lock.yaml` pins mod-users e080e3b1; app-mftodo `versions.lock.yaml` pins eb41f08; both consume it through Go `replace` of a pinned checkout (`make pins.update` in app-mfmanager), not a registry. No CHANGELOG file exists in mod-users; the convention is `docs/consumer-upgrade-notes.md`.
