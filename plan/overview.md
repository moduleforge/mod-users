# Catalog Ent And Derived Keys (mod-users slice)

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This is the mod-users slice of a two-repo federated plan (the mod-tokens slice is planned separately and has no dependency on this one). It makes `JWT_SECRET` optional under `JWT_ALG=EdDSA` by deriving the OAuth-state and step-up HMAC keys from the Ed25519 signing key with HKDF (SHA-256). It closes followup `GlTi`.

In scope: `api/internal/auth` (new derivation and key-resolution helpers, `NewOAuth`), `api/internal/config` (`Load`/`validate`, `LocalAuthConfig`), `api/cmd/server/main.go` wiring, tests, and the docs `docs/jwt-eddsa.md`, `docs/consumer-upgrade-notes.md`, `docs/architecture.md` (new decision D19, amending D17), `README.md`, `.env.example`.

Out of scope: session-token signing/verification (unchanged), the `StateSigner`/`IssueStepUpToken`/`VerifyStepUpToken` signatures (unchanged), HS256 behavior (unchanged), per-token revocation, and the mod-tokens repo.

## Current status

Planned; no implementation started. Phase 3 (`derived-hmac-keys`) holds two sequential tasks. Phase numbering starts at 3 per the federated run.

## Overview

### Clarified request

- Under `JWT_ALG=EdDSA`, when `JWT_SECRET` is unset, `config.Load` must not bootstrap a secret from the database and must not report `JWT_SECRET` as a missing field. The OAuth-state key and the step-up key are derived from the Ed25519 signing key instead.
- Under `JWT_ALG=HS256` (default), nothing changes: `JWT_SECRET` is required, and is DB-bootstrapped when unset.
- Under EdDSA with `JWT_SECRET` explicitly set: the explicit secret wins for both state and step-up, byte-for-byte as today, so in-flight tokens survive an upgrade. Derivation applies only when it is unset.
- The empty-secret guards stay: `ErrStepUpEmptySecret`, `ErrStepUpRequired` on empty verify secret, `NewStateSigner` empty-key error, and `NewOAuth` fails with a clear error if no key can be resolved.
- Backward-compatible APIs: no existing exported signature changes; new helpers are additive.
- Derived keys are never logged or exposed and intermediates are zeroized where practical.

### Design decisions

- **Precedence.** Explicit non-empty `JWT_SECRET` (env) wins. Otherwise, under EdDSA, derive. Because `config.Load` leaves `LocalAuth.JWTSecret` empty under EdDSA when the env var is unset (no DB bootstrap), "JWTSecret non-empty" is the signal for "explicit" under EdDSA. Under HS256 `JWTSecret` is always populated (env or bootstrap) and is used as today.
- **Derivation.** `crypto/hkdf` (Go 1.26 stdlib; `api/go.mod` is `go 1.26.2`) with SHA-256. IKM is the Ed25519 private-key seed (`key.Seed()`, 32 bytes), not the full 64-byte private key and never the public key. Fixed non-secret salt is unnecessary; use a nil salt. `info` is a versioned, per-use label: `mod-users/v1/oauth-state` and `mod-users/v1/step-up`. Output length 32 bytes. The version segment lets a future change roll keys deliberately.
- **Domain separation.** Distinct labels give independent keys, so an OAuth state token never verifies as a step-up token or the reverse. (Existing structural separation, such as the step-up `typ` header, remains as defense in depth.) The derived keys are also unrelated to the Ed25519 signing operation itself.
- **Rotation (`kid`).** Derivation uses the active signing key only; `kid` is deliberately not mixed into the info label (a kid-only relabel would needlessly invalidate tokens, and `kid` is optional). When the signing key rotates, derived state/step-up keys change and in-flight state (OAuth login round-trip) and step-up (`StepUpTTL` = 5 minutes) tokens fail verification; the user simply restarts the login or step-up. Accepting a previous derived key is not feasible, since only the active private key is configured (previous keys are public-only in `JWT_VERIFY_PUBLIC_KEYS`). The rotation window is therefore bounded by the longest state/step-up lifetime, and is documented. Operators who need seamless continuity across rotation set an explicit `JWT_SECRET`.
- **Upgrade note.** A deployment already running EdDSA with a DB-bootstrapped secret that now omits it switches to derived keys on upgrade: in-flight state/step-up tokens at the moment of that deploy fail once. An existing `auth_jwt_secrets` row is left untouched and unused (not deleted). Setting `JWT_SECRET` explicitly (to the persisted value) avoids even that.
- **Zeroization.** Derivation helper works on `[]byte`, zeroes the seed copy and intermediate buffers it controls. Go strings cannot be zeroed, and the existing handler constructors take `jwtSecret string`; keep those signatures and pass the derived bytes through `string(...)`, documenting that residual.

### Architectural-implications check

Applied the four-item checklist: no public API or component boundary signature changes (additive helpers only; config accepts a previously-rejected state); no new subsystem; `docs/mod-users-spec.md` does not define `JWT_SECRET` behavior (grep-verified), so no spec-defined behavior changes; no tracked state added or removed. No `doc-updates` phase is therefore registered. The affected durable docs (D17 amendment, new D19, jwt-eddsa.md, consumer-upgrade-notes.md) are folded into task 002. Task 002 is flagged `architectural_impact: true` because it reshapes the cross-cutting auth/config layer so `phase-review` still runs conformance. The manager may override and add a `doc-updates` phase if it reads the checklist differently.

### Task list

Phase 3 `derived-hmac-keys` (sequential: 002 depends on 001):

1. [001-hkdf-key-derivation](./phase-03-derived-hmac-keys/001-hkdf-key-derivation.md): HKDF derivation helpers and key-resolution functions plus unit tests (`sonnet-med`).
2. [002-optional-jwt-secret-wiring-and-docs](./phase-03-derived-hmac-keys/002-optional-jwt-secret-wiring-and-docs.md): config changes, `NewOAuth` and `main.go` wiring, config/wiring tests, and docs; references followup `GlTi` (`sonnet-med`).

### Followup closure

Both task documents reference followup `GlTi`; task 002 completes it. The manager closes `GlTi` with `followups_remove` after the plan merges.
