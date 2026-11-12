# OIDC EdDSA Audience Token Acceptance

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Make mod-users' OIDC verification, both the facade verifier consumers build with `auth.NewVerifierWithOptions(WithOIDC(...))` and the browser login providers built in `initProvider`, demonstrably and explicitly accept EdDSA (Ed25519/OKP JWKS) ID tokens from a provider whose discovery advertises EdDSA, such as MFManager's IdP, while keeping algorithm-confusion protection, issuer, audience, expiry and nonce checks, and `verifyLocal` semantics unchanged. In scope: `api/internal/auth`, `api/auth` facade, the config for an operator algorithm allowlist, tests, and docs. Out of scope: any change in other repositories.

## Current status

Planned, not started. The plan has three phases; every task is sized for one agent.

## Overview

### Clarified request

- Accept tokens with JWS header `alg: EdDSA` (with `kid`) and `typ: aud+jwt`, claims `iss`, `sub`, `aud` (the app's client id), `email`, `email_verified`, five-minute lifetime, signed by an Ed25519 key published as an OKP JWKS, from an issuer whose discovery lists `id_token_signing_alg_values_supported: ["EdDSA"]`.
- Must not change: `verifyLocal`/`verifyLocalSignature`/`verifyCarrier` behaviour, the bearer path rejecting audience tokens, issuer/audience/expiry/nonce checks, TLS verification defaults.
- Success: new and existing `api/internal/auth` tests pass (`make test.unit`); EdDSA acceptance and every negative case are covered; the integration suite passes against an ephemeral `postgres:16`.

### Key finding that shapes the plan

The request's stated root cause (go-oidc accepts only RS256 and ignores discovery) does not hold for the pinned go-oidc v3.18.0, which already derives the algorithm list from discovery, and go-jose v4.1.4 already handles OKP/Ed25519. See [verifier findings](./notes/oidc-verifier-findings.md). Consequently:

- Phase 1 starts by reproducing the failure with a real Ed25519 issuer, and the reproduction result is recorded before the pinning change. No dependency bump is planned.
- The change that stays regardless is explicit pinning, so the accepted algorithms no longer depend on library defaults.
- If the reproduction fails for a reason other than algorithm (for example the nonce, see the note), the implementer reports that as a blocker/finding instead of widening scope.

### Decisions recorded

1. **Algorithm set.** The accepted algorithms are the discovery document's `id_token_signing_alg_values_supported` intersected with a fixed asymmetric allowlist (RS256/384/512, PS256/384/512, ES256/384/512, EdDSA), further intersected with the operator allowlist when one is configured. `none` and every `HS*` are never accepted, even if advertised. Discovery listing nothing usable falls back to RS256 only (the OIDC default). An empty intersection fails provider/verifier construction with a clear error rather than silently accepting anything.
2. **RS256 stays allowed** by default so existing providers keep working (regression-tested).
3. **Operator restriction.** A Go option `WithOIDCSigningAlgs(algs ...string)` on the verifier (exposed on the `api/auth` facade) and a comma-separated env var `OIDC_ALLOWED_SIGNING_ALGS` for login providers (parsed in config, empty means default allowlist, unknown/forbidden names are a startup config error reported with the other config problems).
4. **typ.** Not required. When present it is accepted only if it equals `JWT` or `aud+jwt` (case-insensitive, per RFC 7515); any other value (for example `at+jwt`, `step-up`, `logout+jwt`) is rejected. This is checked before signature-independent use of the claims, and the shared helper also replaces the exact-case comparison in `carrier.go` so one function defines typ comparison. Rationale: provider ID tokens usually carry `JWT` or no typ; blocking other explicit token types removes cross-token confusion at no interoperability cost known today. The user may override (see requests below).
5. **Key selection.** go-oidc selects JWKS keys by `kid` and rejects a key whose type does not match the pinned algorithm (go-jose verify fails); the tests assert the type-mismatch case rather than adding code.
6. **No dependency bump**; go-oidc v3.18.0 and go-jose v4.1.4 suffice.

### Phases

1. **OIDC Verification Algorithm Pinning and EdDSA Acceptance** (security review emphasis: algorithm handling). Tasks: Ed25519 test issuer harness and baseline reproduction; pin signing algorithms and typ policy in both verification paths; algorithm-confusion and negative tests.
2. **EdDSA OIDC Login End-to-End Coverage**. One task: EdDSA provider driven through the real `Exchange` and the HTTP callback/handoff, with a Postgres-backed integration test.
3. **Documentation Updates**. Architecture/spec docs, and the consumer upgrade notes (this repo's changelog convention), README config reference and OIDC troubleshooting.

### Security review

The server-side `security_review: required` stamp on a phase is set only by a linked type:security finding and cannot be set by the planner. The phase 1 task documents therefore state the review emphasis explicitly; the manager should run the security review at the phase 1 gate (algorithm allowlist, HS*/none exclusion, kid and key-type handling, typ policy, error messages that never echo token content).

### Consumption and out-of-scope follow-on steps (other repositories)

- app-mfmanager pins mod-users in `versions.lock.yaml` (currently e080e3b1); after this plan merges, bump with `make pins.update`.
- app-mftodo pins an older mod-users (eb41f08) in its `versions.lock.yaml` and calls the removed `auth.NewVerifier(ctx, issuer, clientID, "", "")`; adopting the fix needs a pin bump and a move to `auth.NewVerifierWithOptions(ctx, auth.WithOIDC(issuer, clientID))`. Its own login provider config (id `mfidp`, `jit_mode none`) then needs the nonce echoed by MFManager's token endpoint (see the note).
- None of this is planned here.

### Related existing followups

Touched: dvet (typ comparison). Stale and left alone: UJev, 8chg (TLS verify already fixed in code). Left alone: ASqb and the unrelated OIDC UI/handler followups. Details in the note.

### Validation constraints

`make test.unit` must pass; no new scripts or Makefile targets; never use the shared Postgres on port 5433, only ephemeral `postgres:16` containers with unique names (AGENTS.md recipe); the pinned-sibling build caveat applies (inside the worktree `make preflight`/`link-siblings.sh` supplies the sibling replaces).
