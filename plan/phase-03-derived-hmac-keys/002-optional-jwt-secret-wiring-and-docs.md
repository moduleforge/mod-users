# Make JWT_SECRET Optional Under EdDSA, Wire Derived Keys, And Document

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Using the resolvers from task 001 (`001-hkdf-key-derivation.md`, in this phase), make `config.Load` stop requiring and DB-bootstrapping `JWT_SECRET` under `JWT_ALG=EdDSA` when it is unset, wire the derived keys into the OAuth state signer and the step-up handlers, and update the durable docs. This task closes followup `GlTi` (the manager removes it after the plan merges). It depends on task 001.

## Requirements

Code:

1. `api/internal/config/config.go`:
   - Keep the DB bootstrap block (`cfg.LocalAuth.JWTSecret == "" && cfg.DB.URL != ""`) but skip it when `cfg.LocalAuth.JWTAlg == JWTAlgEdDSA`. `loadJWTSigning` already runs before this point; make sure alg is populated first (check ordering in `Load`).
   - In `validate`, drop `JWT_SECRET` from the required list when the alg is EdDSA. Keep it required under HS256. EdDSA still requires `JWT_SIGNING_KEY` (existing check).
   - An explicitly set `JWT_SECRET` under EdDSA is kept as-is (no length policy change) and wins per the plan overview precedence.
   - Update the `LocalAuthConfig.JWTSecret`/`JWTAlg` doc comments and the `Load` doc comment (they currently say it stays required under EdDSA). Document that under EdDSA an empty `JWTSecret` means "derive".
2. `api/internal/auth/oauth.go` `NewOAuth`: replace the `JWTSecret == ""` hard failure with the task 001 state-key resolver; fail with a clear error (no key material) only when no key can be resolved. Update the comment at the top of `NewOAuth` (missing state-signer key) and the `oauth_state.go` comment about shared key material.
3. `api/cmd/server/main.go` (lines ~511 and ~522) and any other `cfg.LocalAuth.JWTSecret` consumers of step-up (`identities.go` deps, `ssh_keys.go`): pass the task 001 step-up key (as `string(key)` to keep existing `jwtSecret string` signatures; handler constructors and `IdentitiesDeps.JWTSecret` keep their types). Fail startup with a clear error if resolution fails. Do not change exported signatures in `api/auth/auth.go`; additive facade wrappers for the resolvers are optional and only if composing apps need them (they do build step-up handlers, so add them, documented, as an additive change).
4. Step-up behavior when `RequireStepUpForCredentialChange` is false and no key resolves: do not regress existing behavior; under HS256 the secret is always set, under EdDSA a key always resolves, so this case should not arise. Confirm and note it in the report.
5. HS256 path and the existing `NewSigner`, `NewLocalVerifier`, `NewHS256Signer`, `NewStateSigner`, `IssueStepUpToken`, `VerifyStepUpToken` signatures and behavior are unchanged. `ErrStepUpEmptySecret` remains.
6. Never log or expose derived keys or the signing key (including in error text and `slog` fields).

Docs (folded in; no separate doc-updates phase):

7. `docs/architecture.md`: add **D19** (D18 is the highest existing number; verify before writing) in Key decisions, after D18: "EdDSA derives the state and step-up keys when `JWT_SECRET` is unset". State the precedence, the HKDF labels and v1 versioning, the rotation window, and the no-DB-bootstrap consequence. Amend D17 (it currently says the secret stays required and that optionality is open) to point to D19 and say the secret is required only under HS256. Update the "Configuration and secrets bootstrap" section: state that the DB bootstrap applies to HS256 only (it never runs under EdDSA), and fix the sentence "`JWT_SECRET` remains required for OAuth state and step-up tokens" in "Local token algorithm".
8. `docs/jwt-eddsa.md`: update the `JWT_SECRET` row in the configuration table (required under HS256; optional under EdDSA, derived when unset, explicit value wins); add a section on derived keys covering derivation, labels, precedence, rotation behavior (state and step-up tokens in flight at a signing-key swap fail and must be retried; window equals the longest state/step-up lifetime, step-up is 5 minutes; set an explicit `JWT_SECRET` for continuity), upgrade note for deployments that relied on the DB-bootstrapped secret under EdDSA (one-time failure of in-flight tokens; the `auth_jwt_secrets` row is left untouched and unused), and the verify-only bullet that says `config.Load` still needs `JWT_SECRET` (now false under EdDSA). Update the "Rotation procedure" with a short note on derived keys.
9. `docs/consumer-upgrade-notes.md`: replace the "`JWT_SECRET` is still required in every mode" paragraph and the "Known limits and open items" bullet (the open question is now resolved), add the new derived-key behavior and any new facade functions, and extend the Purpose line's D14 to D18 range to D19.
10. `README.md` (the `JWT_SECRET` env row, ~line 43), `.env.example`, and comments in `deploy/` manifests that call the secret required: adjust wording to "required for HS256; optional under EdDSA". Keep changes to wording only.

## Validation

Tests (extend existing files; update any that assert `JWT_SECRET` is required under EdDSA, e.g. in `api/internal/config/config_test.go` and `jwt_signing_test.go`):

- Config: EdDSA + signing key + no `JWT_SECRET` + a DB URL: `Load` succeeds, `LocalAuth.JWTSecret == ""`, and the bootstrap is never invoked (assert via a seam or by pointing the DB URL at an unreachable address and confirming `Load` still succeeds without a DB error; a bootstrap attempt would fail `Load`). Respect the existing seams in `jwtsecret_bootstrap_test.go`/`jwtsecret_bootstrap_integration_test.go`.
- Config: EdDSA + explicit `JWT_SECRET`: kept unchanged. HS256 + no secret + DB URL: still bootstraps (existing test remains green); HS256 + no secret + no DB URL: still reports `JWT_SECRET` missing.
- OAuth: `NewOAuth` with an EdDSA config and no secret succeeds and its `StateSigner` signs/verifies; with an explicit secret it uses the secret (verify a token signed by `NewStateSigner([]byte(secret))` is accepted). State token signed under the derived key is rejected when presented to the step-up verifier, and vice versa (cross-rejection at the wiring level).
- Step-up through `checkStepUp`/handler wiring with a derived key issues and verifies a token; explicit-secret case unchanged.
- Empty-secret guards still fire (`ErrStepUpEmptySecret`, `ErrStepUpRequired`).
- No leakage: tests or a grep confirm no new log line or error includes key bytes.

Commands, from `/Users/zane/playground/moduleforge/mod-users/worktrees/plan/catalog-ent-and-derived-keys`: `make test.unit`, `make lint`, `make build.api`. All pass. Then grep checks: `grep -rn "remains required\|still required in every mode\|JWT_SECRET.*required" docs README.md .env.example deploy` returns only statements correctly scoped to HS256; `grep -n "D19" docs/architecture.md docs/consumer-upgrade-notes.md` shows the new decision and the updated range; D17 no longer claims optionality is open. Docs follow the repo's markdown style (see `docs/mf-standards` if present and `AGENTS.md`).

## Metadata

architectural_impact: true

## Assumptions

- Task 001 has landed: the resolvers and `derived_keys.go` exist.
- `docs/mod-users-spec.md` has no `JWT_SECRET` text (grep-verified at planning time); re-check and update only if that has changed.
- This task does not push; the manager handles the merge and then closes followup `GlTi`.

## References

- Followup `GlTi`: closed by this task after merge.
- `docs/architecture.md` D14 to D18 and the "Configuration and secrets bootstrap" section.
- `api/internal/config/config.go` (`Load`, `validate`, `LocalAuthConfig`), `api/internal/config/jwtsecret_bootstrap.go`, `api/internal/auth/oauth.go`, `api/cmd/server/main.go`, `api/internal/handlers/identities.go`, `api/internal/handlers/ssh_keys.go`, `api/auth/auth.go`.

## Checkpoint hints

- After the `config.go` change and its tests.
- After `NewOAuth` and `main.go` wiring plus tests.
- After `docs/architecture.md` (D17 amendment and D19).
- After `docs/jwt-eddsa.md` and `docs/consumer-upgrade-notes.md`.
- After README, `.env.example`, and deploy comment edits.
