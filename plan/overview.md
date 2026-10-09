# Plan: flow-accounts-upstream

Implements five upstream requests from flow-accounts (the first production consumer of mod-users via ModuleForge/mfgen). Source: `upstream-prompts.md` (mod-users section). Base: origin main e107d08.

## Goals
1. Verify-only key for `NewVerifier` (flow-accounts 4eZf) — **EdDSA (Ed25519)** asymmetric mode; HS256 keeps working.
2. CORS safe default (5K4Q).
3. Cookie Path behind a strip-prefix proxy (E75T, hDB7; existing followups of the same ids).
4. users-gui improvements (5F38, dnL0, lJE0; related existing followup QCRD).
5. checkout-deps token handling (gsl4), plus audit of sibling repos' copies.

## Constraints
- Task branches, tests and docs per item; public API backward compatible (new options/config only; existing `auth.NewVerifier` signature in `api/auth/auth.go` keeps working — add an options-based variant rather than changing it).
- No production keys; test keys are generated in-test.
- Phases are ordered 1-5 as requested but are independent of one another except where noted; each can be reviewed/merged on its own.

## Phases
| # | Phase | Tasks |
|---|-------|-------|
| 1 | eddsa-verify-only-key | 001 signer+config+kid, 002 verifier public-key mode + rotation, 003 alg-confusion tests + docs |
| 2 | cors-safe-default | 001 default-deny + startup guard + tests + docs |
| 3 | cookie-base-path | 001 configured base path, 002 oidc test-redirect prefix |
| 4 | users-gui-improvements | 001 AuthPage mode, 002 emailVerifiedAt, 003 bun.lock + core-gui link |
| 5 | checkout-deps-token-handling | 001 port hardening, 002 audit sibling copies (report only) |

Dependencies: 1.002 after 1.001; 1.003 after 1.002; 3.002 after 3.001; 5.002 after 5.001. All else independent.

## Code anchors (verified at planning time)
- Verifier: `api/internal/auth/jwt.go` (`NewVerifier(ctx, issuerURL, clientID, jwtSecret, localIssuer)`, `verifyLocal` HS256), re-exported in `api/auth/auth.go`; called from `api/cmd/server/main.go:86`.
- Signers: `api/internal/auth/local_jwt.go` (three `SigningMethodHS256` call sites); config in `api/internal/config/config.go` (JWT_SECRET, `LocalAuth`).
- CORS: `api/internal/server/server.go` (`AllowCredentials: true`, `parseOrigins(cfg.Server.CORSOrigins)`).
- Cookie path: `api/internal/auth/oidc_cookie_path.go` (`OIDCStateCookiePath(r)`); cookies set in `api/internal/handlers/auth/oidc.go` (~507) and `api/internal/handlers/identities.go` (~739); test-redirect `u.Path = "/oidc-config"` at `oidc.go:~334`.
- GUI: `gui/src/components/auth-page.tsx`, `gui/yalc.lock`, `gui/package.json`.
- Script: `scripts/checkout-deps.sh` (783 lines; token at ~270, `TOKEN_SET` ~730). Reference fix: `/Users/zane/playground/liquid-labs/flow-accounts/scripts/checkout-deps.sh` and `scripts/test-checkout-deps.sh`.

## Report-back deliverable (final task of the plan run)
Commits, new public API / config names, and changes flow-accounts must make on adoption.
