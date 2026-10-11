# Plan Summary: flow-accounts-upstream

## What was planned and why

_No `## Purpose and scope` or `## Overview` section found in `plan/overview.md`._

## What shipped

### Phase 01 — Verify-only EdDSA key for NewVerifier - 4eZf

1. **Ed25519 Signer, Key Config And kid** (`001-ed25519-signer-and-config.md`, tier `sonnet-high`) — Added JWT_ALG, JWT_SIGNING_KEY (PKCS#8 PEM Ed25519, literal \n accepted) and JWT_SIGNING_KID config validated in Load(); local JWT issuance via single Signer (HS256/EdDSA, kid when configured); default HS256 header unchanged, old Issue*JWT signatures kept as wrappers; handlers/main.go wired via SetSigner; public auth.Signer alias + NewSigner added in api/auth/auth.go. JWT_SECRET stays required under EdDSA (OAuth state/step-up).
   Commit `5e87806`, merged at `728f98a058d937034637d3170037c978535ad4bc`.

2. **Public-Key Verifier Mode And Rotation** (`002-public-key-verifier-mode.md`, tier `sonnet-high`) — Verifier supports public-key-only mode with per-kid rotation and per-key alg pinning via NewVerifierWithOptions (WithOIDC, WithHS256Secret, WithLocalIssuer, WithEd25519PublicKey(s)) re-exported from api/auth; NewVerifier signature unchanged. JWT_VERIFY_PUBLIC_KEYS (PKIX PEM or JSON kid->PEM) validated in config. main.go uses auth.NewLocalVerifier and accepts EdDSA tokens under JWT_ALG=EdDSA. Behavior change: legacy NewVerifier with empty jwtSecret now accepts no HS256 tokens (previously forgeable). Docs for JWT_ALG/JWT_SIGNING_*/JWT_VERIFY_PUBLIC_KEYS still pending (task 003).
   Commit `134ea2e`, merged at `6392d3e00eda38c7b66d8c461d5ea16223314c5f`.

3. **Alg-Confusion Tests And Docs** (`003-eddsa-security-tests-and-docs.md`, tier `sonnet-med`) — Added api/internal/auth/jwt_alg_confusion_test.go (public key as HMAC secret across encodings, mixed-mode, non-string kid) and docs/jwt-eddsa.md covering config, PEM/\n escape, key generation, rotation, verify-only deployment and NewVerifier empty-secret behavior change; README config table, architecture/spec pointers, .env.example entries for JWT_ALG/JWT_SIGNING_KEY/JWT_SIGNING_KID/JWT_VERIFY_PUBLIC_KEYS. No code behavior change.
   Commit `9d38b88`, merged at `4a0593a9b25a4336348d9ef4f6a9d2a15b094aee`.

### Phase 02 — CORS safe default - 5K4Q

1. **CORS Default Deny And Startup Guard** (`001-cors-default-deny.md`, tier `sonnet-med`) — CORS default-deny: empty CORS_ORIGINS outside local mode installs no CORS middleware; AllowCredentials only with explicit allow-list; config.Load fails when CORS_ORIGINS contains * and DEPLOY_MODE is not local (serverless, k8s, container-host count as production-like); local mode keeps permissive * without credentials; server.New fails closed on hand-built wildcard config. docs/cors.md added with behavior-change and flow-accounts adoption notes, linked from README; spec and .env.example updated.
   Commit `7afae2a`, merged at `79506db6750cc5c1069e5fd85020860b12ccba0d`.

### Phase 03 — Cookie Path behind a strip-prefix proxy - E75T and hDB7

1. **Configured Cookie Base Path** (`001-configured-cookie-base-path.md`, tier `sonnet-med`) — USERS_COOKIE_PATH (strict allowlist-validated, rejected at startup if invalid) sets the oidc_state cookie Path to base + /v1/auth/oidc/ at every set and clear site (shared newStateCookie); unset keeps request-derived behavior; X-Forwarded-Prefix deliberately not trusted. BREAKING for direct callers: facade handlers.NewIdentitiesHandler gained trailing cookiePath string arg (pass empty string for old behavior); moduleforge.module.yaml passes field:cfg.Auth.CookiePath. docs/cookie-path.md with Caddy strip-prefix example, README, .env.example. Resolves followup E75T; hDB7 (redirectToTestResult path overwrite) NOT addressed, left for task 3.002.
   Commit `928c87b`, merged at `fabe260e16770d7c223aab46390685c7d580731e`.

2. **OIDC Test Redirect Keeps Mount Prefix** (`002-oidc-test-redirect-prefix.md`, tier `sonnet-med`) — redirectToTestResult now prepends the validated configured cookie base path (OIDCHandler.testResultPath) to /oidc-config, clearing RawPath; unchanged when unset; X-Forwarded-Prefix ignored; other redirect builders audited, no change needed. Resolves followup hDB7. Table test added.
   Commit `ee8360b`, merged at `0199d0c5b34ed5023ba2046083abab82483fbd0a`.

### Phase 04 — users-gui improvements - 5F38, dnL0, lJE0

1. **AuthPage Passes Mode To Callback** (`001-authpage-mode-callback.md`, tier `sonnet-low`) — AuthPage onAuthenticated is now (returnPath: string | null, mode: AuthMode) with mode 'login' or 'register'; one-arg callbacks still type-check; tests, Ladle story and gui/README updated; gui bun test (225 pass), typecheck and build passed with core-gui resolved.
   Commit `aab79f4`, merged at `007e46e26c5fdd336dbf94e103f5eefa6590b222`.

2. **Expose emailVerifiedAt On Self** (`002-self-email-verified-at.md`, tier `sonnet-med`) — Added nullable email_verified_at (snake_case wire name, matching neighbouring /v1/self fields; always emitted, explicit null when unverified) to the /v1/self response (api/internal/handlers/self.go, OpenAPI UserDetail), to GUI UserAccountSelf as email_verified_at: string | null, and exposed via useAuth() as camelCase emailVerifiedAt (null when signed out). Go and GUI tests for verified/unverified; go test, bun test (225 pass) and typecheck passed. Resolves followup QCRD. Wire name differs from the requested camelCase emailVerifiedAt; adoption note needed. Observations outside scope: buildSelfResponse does not emit is_admin though the GUI type declares it; OpenAPI UserSummary has a stale email_verified boolean.
   Commit `0a4f032`, merged at `0c60257a6afdb2bad9235fb3ef90f784bcbf0057`.

3. **Commit gui bun.lock And Document core-gui Link** (`003-gui-lockfile-and-link.md`, tier `sonnet-med`) — Task premise was stale: root package.json is already a bun workspace (gui, ../mod-core/gui) with a committed root bun.lock containing workspace:../mod-core/gui; gui/yalc.lock is gitignored and absent; .gitignore does not exclude bun.lock. No gui/bun.lock added (would conflict with workspace root lockfile). Added a Lockfile paragraph to gui/README.md; link documented in AGENTS.md and gui/README.md. Verified in a clean clone with sibling mod-core laid out side by side: bun install --frozen-lockfile, typecheck and build pass. Caveat: the workspace path ../mod-core/gui requires mod-core as a sibling of mod-users, so it does not resolve inside nested Flow task worktrees.
   Commit `2ebfb12`, merged at `c908d61b0b8cee229232687fd413e939ecba7f13`.

### Phase 05 — checkout-deps token handling - gsl4

1. **Port checkout-deps Token Hardening** (`001-port-checkout-deps-hardening.md`, tier `sonnet-high`) — Ported token hardening into scripts/checkout-deps.sh: token via GIT_CONFIG_COUNT/KEY/VALUE env scoped to http.https://<host>/.extraheader with http.followRedirects=false (not in argv), strict host/owner shape validation (also override host/owner/repo and pin keys, rejecting . .. and leading -), MODULEFORGE_TOKEN_ALLOWED_HOSTS allowlist (default github.com) checked at lockfile read time and in git_fetch_auth, token never echoed. New scripts/test-checkout-deps.sh (58 cases, stub git, ps/argv absence check with positive control; negative control vs old script gave 51 FAIL), wired as make test.unit.scripts under test.unit; shellcheck clean. Caveats: header says the script is byte-identical with copies in other repos (now diverged until synced, task 5.002 audits); error 'unrecognized line in overrides block' still echoes a raw lockfile line to stderr (not a token leak, out of scope).
   Commit `e6f9a41`, merged at `7c6b29560c42bc98af922d84a49e63bf72af27b2`.

2. **Audit Sibling checkout-deps Copies** (`002-audit-sibling-checkout-deps.md`, tier `sonnet-low`) — Audit written to plan/notes/sibling-checkout-deps-audit.md. app-mfmanager, app-mfgit and app-mftodo copies of checkout-deps.sh are unhardened (token on argv via -c http.extraheader for mfmanager; no host/owner validation, allowlist or redirect hardening); mod-users main copy superseded by this plan; flow-accounts already hardened (no overrides support). Findings 5eQ7 (app-mfmanager), dUlk (app-mfgit), ll8e (app-mftodo) filed, type:security priority:high. Static audit only; no token values read.
   Commit `35c40ff`, merged at `3990fa0c5cf4cbf61db99ae9e1571a560047eee7`.

### Phase 06 — Remediation Round 1

1. **Signer Wiring Guard And Key Revocation Docs** (`001-signer-wiring-and-key-revocation-docs.md`, tier `sonnet-med`) — Added signer-requiring handler constructors (authhandlers.NewWithSigner, handlers.NewAssumeHandlerWithSigner; public constructors keep signatures), NewHS256Signer now returns error and rejects empty secret, no-signer fallback fails closed on empty secret, OIDC signer built once with startup check (SignerErr) in main. docs/jwt-eddsa.md gained signer-wiring and revocation sections. Residual: legacy-constructed handler with non-empty secret and no SetSigner still falls back to HS256 (handlers do not see JWT_ALG); documented.
   Commit `cba3f9a`, merged at `d83918010bd2777e6053f57e4b318b2536b77ccd`.

2. **Reject Step-Up Tokens As Bearer Tokens** (`002-reject-step-up-as-bearer.md`, tier `sonnet-med`) — verifyLocal rejects tokens with header typ step-up (case-insensitive) before signature check; internal NewVerifier now errors when jwtSecret non-empty and localIssuer empty (signature unchanged; no in-repo caller affected); VerifyStepUpToken still requires exact typ and credential_change purpose. Tests added; full Go suite green.
   Commit `24bbc48`, merged at `bc988371945c38fa02381ca6a2a6c74c647632d8`.

### Phase 07 — Remediation Round 2

1. **Step-Up Tokens Refuse An Empty Secret** (`001-step-up-empty-secret-guard.md`, tier `sonnet-med`) — IssueStepUpToken returns new sentinel ErrStepUpEmptySecret on empty secret; VerifyStepUpToken returns existing ErrStepUpRequired on empty secret; header and purpose checks unchanged; tests for empty-secret issuing and empty-key forgery added; full Go suite green.
   Commit `515df63`, merged at `faa7443bd07198b02062846c63d28cc81fae1026`.

### Phase 08 — Remediation Round 3

1. **Reject CORS Wildcard Patterns And Fix Manifests** (`001-reject-cors-wildcard-patterns.md`, tier `sonnet-med`) — config.Load rejects any CORS_ORIGINS entry containing * outside local mode (error names CORS_ORIGINS and mode, no entry echo); corsOptions fails closed (no middleware, no credentials) for pattern entries; local mode unchanged; k8s configmap uses placeholder origin, cloudrun service.yaml gets CORS_ORIGINS entry with comment; docs/cors.md and README updated. Tests added; full Go suite green.
   Commit `c4c3261`, merged at `cbde94e584977b4fa7f73eb4bd74458fb371d3d1`.

### Phase 09 — Backlog Fixes jKvl And nJel

1. **Email Verified State Correct After Login** (`001-email-verified-after-login.md`, tier `sonnet-med`) — Server-side fix: login, email-code verify, anonymous (explicit null) and admin assume payloads now carry email_verified_at with /v1/self semantics (shared sessionUser and emailVerifiedAtValue helpers), so useAuth().emailVerifiedAt is correct right after login(), register() and email-code sign-in. OpenAPI: removed email_verified_at from shared UserDetail, added UserSelf (required nullable email_verified_at) for GET/PUT /v1/self and LoginResponse for login/email-code/assume. Go and GUI tests (230 pass) and typecheck pass. Finding mJdk filed: LoginResponse.user still typed UserAccountSelf though sign-in payloads send only uuid, email, email_verified_at (pre-existing).
   Commit `29260d1`, merged at `483d43de2d21d3cbaf09473a4b24e25c85d9c22d`.

2. **Close checkout-deps Test Gaps** (`002-checkout-deps-test-gaps.md`, tier `sonnet-med`) — Added 11 test cases to scripts/test-checkout-deps.sh (69 PASS): bash -x and SHELLOPTS=xtrace for --verify --remote and --pinned (with trace-present positive controls), trailing-dot host, lookalike host, CRLF lockfile, GitHub.com case-insensitivity, glob allowlist case, direct git_fetch_auth allowlist re-check via function extraction (re-check unreachable from CLI). Fixed token_host_allowed to run under local set -f (restoring caller setting). Negative control (replacing set +x in git_fetch_auth) made the --pinned xtrace cases fail. --verify --remote xtrace cases cannot detect a removed guard (output discarded); only --pinned cases do. shellcheck clean. The set -f change must be mirrored in sibling copies (app-mftodo and app-mfgit use read -a instead; app-mfmanager in progress).
   Commit `4bd299e`, merged at `4644e5cdf7ccc17edbcde213a0ddb2f27c3d86ee`.

### Phase 10 — Preserve Durable Context

1. **Preserve Durable Plan Context In Docs** (`001-preserve-durable-context.md`, tier `sonnet-high`) — Wrote docs/consumer-upgrade-notes.md (EdDSA/verifier API and config, token hardening, CORS, cookie base path and breaking NewIdentitiesHandler cookiePath, GUI and email_verified_at wire name, checkout-deps script and sibling copies, known limits); added Key decisions D13-D17 to docs/architecture.md (repo keeps decisions there, no decisions dir); linked from README; small known-limit additions to cors.md and cookie-path.md. Every name and behavior verified against code; Status table has a disposition for all plan items (overview, notes, 18 task statuses, closed findings). Docs only. Observations: docs/jwt-eddsa.md says JWT_SECRET 'required' without the DB bootstrap mention (not wrong, covered in new notes); scripts/checkout-deps.sh header has stale 'two carrier-repo copies' and plan/notes reference.
   Commit `1a40140`, merged at `9abb3ecc1f583cac24c6aa655df50bf4782ee336`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Findings

- **`GlTi`** — **config.Load()/validate() in mod-users still r** — promoted — ref: `GlTi` — 2026-10-10

- **`QI2I`** — **Handler signer injection is opt-in only** — fixed — ref: `phase-06-remediation-01/001-signer-wiring-and-key-revocation-docs.md` — 2026-10-10

- **`RmRX`** — **Document JWT_VERIFY_PUBLIC_KEYS revocation** — fixed — ref: `phase-06-remediation-01/001-signer-wiring-and-key-revocation-docs.md` — 2026-10-10

- **`nIyL`** — **Step-up tokens share JWT_SECRET with bearer** — fixed — ref: `phase-06-remediation-01/002-reject-step-up-as-bearer.md` — 2026-10-10

- **`SUyS`** — **Step-up tokens accept an empty secret** — fixed — ref: `phase-07-remediation-02/001-step-up-empty-secret-guard.md` — 2026-10-10

- **`BODx`** — **CORS docs still say only bare * errors** — promoted — ref: `BODx` — 2026-10-10

- **`dnKQ`** — **Local CORS patterns reflect with credentials** — promoted — ref: `dnKQ` — 2026-10-10

- **`OO6Y`** — **CORS wildcard guard misses pattern entries** — fixed — ref: `phase-08-remediation-03/001-reject-cors-wildcard-patterns.md` — 2026-10-10

- **`D6o7`** — **Deploy manifests silently lose CORS** — fixed — ref: `phase-08-remediation-03/001-reject-cors-wildcard-patterns.md` — 2026-10-10

- **`7ok7`** — **Cookie path docs omit test-redirect effect** — promoted — ref: `7ok7` — 2026-10-10

- **`rzI8`** — **Cookie base path has no length cap** — dismissed — 2026-10-10 — reason: Reviewer rated it a suggestion: the value is operator-set at startup, not attacker-controlled, and not exploitable; an oversized value only makes browsers drop the cookie, which the operator sees immediately. Remediation budget is exhausted and it is not worth a backlog item.

- **`jKvl`** — **emailVerifiedAt reads null after login** — promoted — ref: `jKvl` — 2026-10-10

- **`xuSi`** — **checkout-deps strictness edge cases** — promoted — ref: `xuSi` — 2026-10-10

- **`5eQ7`** — **app-mfmanager checkout-deps.sh unhardened** — promoted — ref: `5eQ7` — 2026-10-10

- **`dUlk`** — **app-mfgit checkout-deps.sh unhardened** — promoted — ref: `dUlk` — 2026-10-10

- **`ll8e`** — **app-mftodo checkout-deps.sh unhardened** — promoted — ref: `ll8e` — 2026-10-10

- **`nJel`** — **checkout-deps tests miss xtrace/host cases** — promoted — ref: `nJel` — 2026-10-10

- **`mJdk`** — **LoginResponse.user type is wider than payload** — promoted — ref: `mJdk` — 2026-10-11

- **`66qf`** — **Assume test gap; LoginResponse typing** — promoted — ref: `66qf` — 2026-10-11

## Remediation

- Rounds used: 3 (resolved max_rounds: 4).
- Remediation tasks added: 4 (resolved max_tasks_per_remediation_round per round: 25).
- Remediation phases:
  - `remediation-01` — 2 task(s)
  - `remediation-02` — 1 task(s)
  - `remediation-03` — 1 task(s)
- Security review required: yes (at least one remediation phase carries `security_review: required`).

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — Verify-only EdDSA key for NewVerifier - 4eZf

- [x] [001-ed25519-signer-and-config.md](./phase-01-eddsa-verify-only-key/001-ed25519-signer-and-config.md) — tier `sonnet-high` · branch `plan/flow-accounts-upstream-01-001` · commit `5e87806` · merge `728f98a058d937034637d3170037c978535ad4bc`
- [x] [002-public-key-verifier-mode.md](./phase-01-eddsa-verify-only-key/002-public-key-verifier-mode.md) — tier `sonnet-high` · branch `plan/flow-accounts-upstream-01-002` · commit `134ea2e` · merge `6392d3e00eda38c7b66d8c461d5ea16223314c5f`
- [x] [003-eddsa-security-tests-and-docs.md](./phase-01-eddsa-verify-only-key/003-eddsa-security-tests-and-docs.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-01-003` · commit `9d38b88` · merge `4a0593a9b25a4336348d9ef4f6a9d2a15b094aee`

### Phase 02 — CORS safe default - 5K4Q

- [x] [001-cors-default-deny.md](./phase-02-cors-safe-default/001-cors-default-deny.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-02-001` · commit `7afae2a` · merge `79506db6750cc5c1069e5fd85020860b12ccba0d`

### Phase 03 — Cookie Path behind a strip-prefix proxy - E75T and hDB7

- [x] [001-configured-cookie-base-path.md](./phase-03-cookie-base-path/001-configured-cookie-base-path.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-03-001` · commit `928c87b` · merge `fabe260e16770d7c223aab46390685c7d580731e`
- [x] [002-oidc-test-redirect-prefix.md](./phase-03-cookie-base-path/002-oidc-test-redirect-prefix.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-03-002` · commit `ee8360b` · merge `0199d0c5b34ed5023ba2046083abab82483fbd0a`

### Phase 04 — users-gui improvements - 5F38, dnL0, lJE0

- [x] [001-authpage-mode-callback.md](./phase-04-users-gui-improvements/001-authpage-mode-callback.md) — tier `sonnet-low` · branch `plan/flow-accounts-upstream-04-001` · commit `aab79f4` · merge `007e46e26c5fdd336dbf94e103f5eefa6590b222`
- [x] [002-self-email-verified-at.md](./phase-04-users-gui-improvements/002-self-email-verified-at.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-04-002` · commit `0a4f032` · merge `0c60257a6afdb2bad9235fb3ef90f784bcbf0057`
- [x] [003-gui-lockfile-and-link.md](./phase-04-users-gui-improvements/003-gui-lockfile-and-link.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-04-003` · commit `2ebfb12` · merge `c908d61b0b8cee229232687fd413e939ecba7f13`

### Phase 05 — checkout-deps token handling - gsl4

- [x] [001-port-checkout-deps-hardening.md](./phase-05-checkout-deps-token-handling/001-port-checkout-deps-hardening.md) — tier `sonnet-high` · branch `plan/flow-accounts-upstream-05-001` · commit `e6f9a41` · merge `7c6b29560c42bc98af922d84a49e63bf72af27b2`
- [x] [002-audit-sibling-checkout-deps.md](./phase-05-checkout-deps-token-handling/002-audit-sibling-checkout-deps.md) — tier `sonnet-low` · branch `plan/flow-accounts-upstream-05-002` · commit `35c40ff` · merge `3990fa0c5cf4cbf61db99ae9e1571a560047eee7`

### Phase 06 — Remediation Round 1

- [x] [001-signer-wiring-and-key-revocation-docs.md](./phase-06-remediation-01/001-signer-wiring-and-key-revocation-docs.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-06-001` · commit `cba3f9a` · merge `d83918010bd2777e6053f57e4b318b2536b77ccd`
- [x] [002-reject-step-up-as-bearer.md](./phase-06-remediation-01/002-reject-step-up-as-bearer.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-06-002` · commit `24bbc48` · merge `bc988371945c38fa02381ca6a2a6c74c647632d8`

### Phase 07 — Remediation Round 2

- [x] [001-step-up-empty-secret-guard.md](./phase-07-remediation-02/001-step-up-empty-secret-guard.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-07-001` · commit `515df63` · merge `faa7443bd07198b02062846c63d28cc81fae1026`

### Phase 08 — Remediation Round 3

- [x] [001-reject-cors-wildcard-patterns.md](./phase-08-remediation-03/001-reject-cors-wildcard-patterns.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-08-001` · commit `c4c3261` · merge `cbde94e584977b4fa7f73eb4bd74458fb371d3d1`

### Phase 09 — Backlog Fixes jKvl And nJel

- [x] [001-email-verified-after-login.md](./phase-09-backlog-fixes/001-email-verified-after-login.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-09-001` · commit `29260d1` · merge `483d43de2d21d3cbaf09473a4b24e25c85d9c22d`
- [x] [002-checkout-deps-test-gaps.md](./phase-09-backlog-fixes/002-checkout-deps-test-gaps.md) — tier `sonnet-med` · branch `plan/flow-accounts-upstream-09-002` · commit `4bd299e` · merge `4644e5cdf7ccc17edbcde213a0ddb2f27c3d86ee`

### Phase 10 — Preserve Durable Context

- [x] [001-preserve-durable-context.md](./phase-10-preserve-context/001-preserve-durable-context.md) — tier `sonnet-high` · branch `plan/flow-accounts-upstream-10-001` · commit `1a40140` · merge `9abb3ecc1f583cac24c6aa655df50bf4782ee336`
