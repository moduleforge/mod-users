# Add HKDF Derivation And Key Resolution For State And Step-Up

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Add, in `api/internal/auth`, the primitives that produce the OAuth-state and step-up HMAC keys: an HKDF-SHA256 derivation from the Ed25519 signing key with distinct versioned labels, and resolver functions that apply the precedence rule (explicit `JWT_SECRET` wins; otherwise derive under EdDSA). No config or wiring changes here; those are task 002. Part of the work that closes followup `GlTi`.

New code goes in a new file (for example `api/internal/auth/derived_keys.go`) with tests in `derived_keys_test.go`. No existing exported signature changes.

## Requirements

1. Constants for the labels: `mod-users/v1/oauth-state` and `mod-users/v1/step-up`, with a doc comment explaining the version segment and that labels are non-secret domain-separation contexts.
2. A derivation function (unexported or internal-exported) taking an `ed25519.PrivateKey` and a label, returning a 32-byte `[]byte`. Use stdlib `crypto/hkdf` with `sha256.New` (`go 1.26.2` in `api/go.mod`; do not add a dependency). IKM is `key.Seed()`; nil salt; `info` is the label. Reject a key whose length is not `ed25519.PrivateKeySize`. Zero the seed copy and any intermediate buffers it controls before returning (best effort; document the Go limits).
3. Two resolvers taking `config.LocalAuthConfig` and returning `([]byte, error)`:
   - `OAuthStateKey(lc)` and `StepUpKey(lc)` (names may be adjusted; keep them symmetric).
   - If `lc.JWTSecret != ""`: return `[]byte(lc.JWTSecret)` unchanged (explicit secret wins; also the whole HS256 path).
   - Else if `lc.JWTAlg == config.JWTAlgEdDSA` and a valid signing key is present: return the derived key for that label.
   - Else: return an error (fixed message, no key material) so callers can fail startup. Never return an empty key with nil error.
4. The step-up key is used by `IssueStepUpToken`/`VerifyStepUpToken`, whose signatures and empty-secret guards (`ErrStepUpEmptySecret`, `ErrStepUpRequired`) stay unchanged. Do not alter `StateSigner` either.
5. Derived keys and the signing key must never appear in logs, error strings, or `String()`/`%v` output of any new type.
6. `kid` is deliberately not part of the derivation (see plan overview, rotation); put that rationale in a code comment.

## Validation

Add tests in `api/internal/auth/derived_keys_test.go` covering:

- Determinism: same key and label give the same 32 bytes; a different signing key gives a different output.
- Label separation: state and step-up derivations differ for the same key.
- Cross-rejection: a token produced by `StateSigner.Sign` with the derived state key fails `Verify` under the derived step-up key, and a step-up token from `IssueStepUpToken` with the derived step-up key fails `VerifyStepUpToken` with the derived state key (`ErrStepUpRequired`).
- Precedence: with `JWTAlg=EdDSA`, a signing key and a non-empty `JWTSecret`, both resolvers return exactly the secret bytes; with `JWTSecret` empty they return derived keys.
- HS256 unchanged: `JWTAlg` empty or `HS256` with a secret returns the secret; with an empty secret returns an error (no derivation under HS256, even if a signing key were set).
- Empty-secret guards: resolver error path never yields empty key; `IssueStepUpToken` with empty secret still returns `ErrStepUpEmptySecret`.
- Invalid or wrong-length signing key yields an error; errors contain no key bytes.
- A known-answer test pinning the HKDF output for a fixed seed and each label, so an accidental label or version change is caught.

Run from `/Users/zane/playground/moduleforge/mod-users/worktrees/plan/catalog-ent-and-derived-keys`: `make test.unit` (or `cd api && go test ./internal/auth/...`), `make lint`, and `go vet ./...` in `api/`. All must pass.

## Metadata

architectural_impact: false

## Assumptions

- The implementing worktree is a task branch off the plan branch; `model/db/` is present (see `.claude/CLAUDE.md` gotcha about `make clean.build`).
- Go toolchain is 1.26+, so `crypto/hkdf` is available.

## References

- Followup `GlTi` (read via `followups_list`): the open question this plan resolves.
- `api/internal/auth/signer.go`, `api/internal/auth/stepup.go`, `api/internal/auth/oauth_state.go`, `api/internal/config/jwt_signing.go`.
- `docs/architecture.md` D17 (amended in task 002).

## Status

- Outcome: succeeded (2026-10-11).
- Added `api/internal/auth/derived_keys.go` (labels, `deriveKey`, `OAuthStateKey`, `StepUpKey`) and `api/internal/auth/derived_keys_test.go`.
- Validation: `go test ./internal/auth/...` passed; `go vet ./...` passed; known-answer vectors cross-checked against an independent RFC 5869 computation. `make lint` fails in `api/scripts/check-server-error-literals.sh` on pre-existing `api/internal/handlers/identities.go:318`, outside this task's change set; `golangci-lint` reports no issues in the new files.
