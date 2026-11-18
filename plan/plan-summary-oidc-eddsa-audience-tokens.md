# Plan Summary: oidc-eddsa-audience-tokens

## What was planned and why

Make mod-users' OIDC verification, both the facade verifier consumers build with `auth.NewVerifierWithOptions(WithOIDC(...))` and the browser login providers built in `initProvider`, demonstrably and explicitly accept EdDSA (Ed25519/OKP JWKS) ID tokens from a provider whose discovery advertises EdDSA, such as MFManager's IdP, while keeping algorithm-confusion protection, issuer, audience, expiry and nonce checks, and `verifyLocal` semantics unchanged. In scope: `api/internal/auth`, `api/auth` facade, the config for an operator algorithm allowlist, tests, and docs. Out of scope: any change in other repositories.

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

## What shipped

### Phase 01 — OIDC Verification Algorithm Pinning and EdDSA Acceptance

1. **Ed25519 Test Issuer Harness and Baseline Reproduction** (`001-eddsa-issuer-harness-and-repro.md`, tier `sonnet-med`) — Built a reusable Ed25519/OKP test issuer harness. The baseline shows go-oidc already accepts EdDSA aud+jwt tokens on both verifier paths, so the remaining plan is hardening plus tests. A missing nonce claim yields nonce mismatch in Exchange.
   Commit `200e9daf`, merged at `64936cbea9a4501c7a02406b0b759a48f089914f`.

2. **Pin OIDC Signing Algorithms and Enforce typ Policy** (`002-pin-oidc-signing-algs.md`, tier `sonnet-high`) — Pinned the OIDC signing algorithms explicitly in both verification paths (discovery intersected with a fixed allowlist, RS256 fallback, optional operator restriction via WithOIDCSigningAlgs and OIDC_ALLOWED_SIGNING_ALGS). Enforced the typ policy before OIDC verification in Verify and Exchange. Unified the typ comparison in carrier.go with EqualFold. Covered with table-driven and issuer-harness tests; all validation green.
   Commit `0c7faac6`, merged at `c48f4b85465ba0459f497c539b70768cb7e19bd6`.

3. **EdDSA and Algorithm-Confusion Verification Tests** (`003-alg-confusion-negative-tests.md`, tier `sonnet-med`) — Added a test-only file covering the EdDSA and algorithm-confusion matrix through NewVerifierWithOptions and initProvider, reusing the harness. Both mutation checks fail the expected tests and were reverted.
   Commit `0b6fbb01`, merged at `950975d06b0664b65834dc705cf284f4853021b0`.

### Phase 02 — EdDSA OIDC Login End-to-End Coverage

1. **EdDSA OIDC Login Flow Tests Including Postgres Integration** (`001-eddsa-login-flow-tests.md`, tier `sonnet-high`) — _No digest recorded for **EdDSA OIDC Login Flow Tests Including Postgres Integration** — its outcome is in `plan/phase-02-oidc-eddsa-login-e2e/001-eddsa-login-flow-tests.md`._
   Commit `fb537df4`, merged at `8873a7bfc2350d64f0475701a7c0e690edc4b025`.

### Phase 03 — Documentation Updates

1. **Update Architecture Docs** (`001-update-architecture-docs.md`, tier `sonnet-high`) — Doc-only update across four docs describing OIDC algorithm pinning, the typ policy, the operator restriction, and the generic claim-style limitation; no code touched.
   Commit `0e8c8772`, merged at `72660c94f4f3f4f6ce2bf277fdb9cd99b33c13e5`.

2. **Consumer Upgrade Notes, Config Reference and Troubleshooting** (`002-consumer-notes-and-config-docs.md`, tier `sonnet-med`) — Doc-only change across README.md, docs/consumer-upgrade-notes.md and docs/oidc-troubleshooting.md. It documents the delivered algorithm pinning, the typ policy, the removed constructor and the nonce echo requirement, and records the generic claim_style limitation. A Status note is appended to the task document.
   Commit `54cd25cc`, merged at `d8043e4c25eef1762d76002302209c5480994c28`.

### Phase 04 — Remediation Round 1

1. **Fail Closed On Non-String Or Undecodable OIDC Token Typ** (`001-fail-closed-oidc-typ.md`, tier `sonnet-med`) — The OIDC typ check now fails closed on undecodable 3-segment headers and on non-string typ (array, number, object, bool, null). Absent, JWT and aud+jwt (case-insensitive) still accepted; carrier path untouched. New tests cover the check function, Verifier.Verify and OAuth.Exchange.
   Commit `8eac3549`, merged at `20c7c28eee09c31ff208cf515acc5812c1c193c4`.

### Phase 05 — Remediation Round 2

1. **Reject Non-Compact OIDC Tokens In The Typ Check And Make Carrier Typ Parsing Strict** (`001-oidc-typ-compact-only-and-carrier-strict.md`, tier `sonnet-high`) — _No digest recorded for **Reject Non-Compact OIDC Tokens In The Typ Check And Make Carrier Typ Parsing Strict** — its outcome is in `plan/phase-05-remediation-02/001-oidc-typ-compact-only-and-carrier-strict.md`._
   Commit `af45c635`, merged at `004907d74d91431f9d527384d4eeb7765a920e9d`.

### Phase 06 — Remediation Round 3

1. **Correct OIDC typ Helper, Empty typ and RS256 Default Wording in Docs** (`001-correct-oidc-typ-and-alg-doc-wording.md`, tier `sonnet-med`) — Empty typ treated as absent and RS256 wording corrected across OIDC docs; typ helper described as two separate checks sharing aud+jwt constant; comment-only edit in oidc_algs.go; make test.unit 458 pass.
   Commit `5ae09eaf`, merged at `aa7f6e3`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Findings

- **`xByR`** — **BASELINE: EdDSA is already accepted by the cu** — dismissed — 2026-11-13 — reason: Not actionable: this records the baseline reproduction outcome (EdDSA
  already accepted by the unmodified verifier on both the facade and the
  initProvider login paths), confirmed in the 001 task doc's Status
  section. It asks for no change. The plan overview ("Key finding that
  shapes the plan") already expects this and continues as explicit
  pinning plus tests. The durable statement that EdDSA/OKP providers are
  accepted is already part of future task 3/002 (consumer upgrade notes)
  and task 3/001 (architecture docs). Reclassified from the default
  ambiguity stamp because no decision is pending.

- **`PED5`** — **Nonce finding: OAuth.Exchange with an EdDSA I** — promoted — ref: `PED5` — 2026-11-13

- **`ByMy`** — **Dangling forward link: ./docs/mf-standards/b…** — dismissed — 2026-11-13 — reason: Not actionable (false positive): the link target exists. docs/mf-standards
  is a git submodule (.gitmodules; gitlink 2854009c at HEAD). It is not
  initialized in this plan worktree (`git submodule status` shows a leading
  "-"), so the review lens saw an empty directory. At the pinned commit
  2854009c, building-applications.md exists and has "## First-time setup"
  (line 180), so README.md:33's link and anchor resolve. AGENTS.md:30
  already documents the required `git submodule update --init
  docs/mf-standards`. No document change is needed.

- **`dLlw`** — **Dangling forward link: ./mf-standards/buildi…** — dismissed — 2026-11-13 — reason: Not actionable (false positive, same cause as ByMy): the docs/mf-standards
  submodule is uninitialized in this worktree. At pinned commit 2854009c,
  building-applications.md exists with the #first-time-setup anchor
  (line 180), so docs/mod-users-spec.md:161 resolves once the submodule is
  initialized.

- **`BtjV`** — **Dangling forward link: ./mf-standards/archit…** — dismissed — 2026-11-13 — reason: Not actionable (false positive): mf-standards/architecture.md exists at
  the pinned submodule commit 2854009c (verified with git cat-file). The
  link at docs/mod-users-spec.md:7 dangles only because the submodule is
  uninitialized in this worktree.

- **`ecIT`** — **Dangling forward link: ./mf-standards/archit…** — dismissed — 2026-11-13 — reason: Not actionable (false positive): mf-standards/architecture/api-response-design.md
  exists at the pinned submodule commit 2854009c. The link at
  docs/mod-users-spec.md:218 dangles only because the submodule is
  uninitialized in this worktree.

- **`rdfI`** — **Dangling forward link: ./mf-standards/archit…** — dismissed — 2026-11-13 — reason: Not actionable (false positive): mf-standards/architecture/authorization-design.md
  exists at the pinned submodule commit 2854009c. The link at
  docs/mod-users-spec.md:381 dangles only because the submodule is
  uninitialized in this worktree.

- **`5R2E`** — **Dangling forward link: ./mf-standards/archit…** — dismissed — 2026-11-13 — reason: Not actionable (false positive): mf-standards/architecture.md exists at
  the pinned submodule commit 2854009c. The link at docs/architecture.md:5
  dangles only because the submodule is uninitialized in this worktree.

- **`GqoZ`** — **Dangling forward link: ./mf-standards/archit…** — dismissed — 2026-11-13 — reason: Not actionable (false positive): mf-standards/architecture/db-considerations.md
  exists at the pinned submodule commit 2854009c. The link at
  docs/architecture.md:257 dangles only because the submodule is
  uninitialized in this worktree.

- **`xokw`** — **Dangling forward link: ../docs/mf-standards/…** — dismissed — 2026-11-13 — reason: Not actionable (false positive): docs/mf-standards/architecture/db-considerations.md
  exists at the pinned submodule commit 2854009c. The link at
  model/README.md:6 dangles only because the submodule is uninitialized in
  this worktree.

- **`FnMz`** — **Dangling forward link: ./docs/mf-standards/b…** — dismissed — 2026-11-13 — reason: Not actionable (false positive): at the pinned submodule commit 2854009c,
  building-common.md exists and has "## Building inside a task worktree"
  (line 21), so AGENTS.md:178's link and anchor resolve. The link dangles
  only because the submodule is uninitialized in this worktree.

- **`L3ZO`** — **Dangling forward link: ./docs/mf-standards/a…** — dismissed — 2026-11-13 — reason: Not actionable (false positive): mf-standards/architecture/http-error-precedence.md
  exists at the pinned submodule commit 2854009c. The link at AGENTS.md:229
  dangles only because the submodule is uninitialized in this worktree.

- **`dtB2`** — **checkOIDCTokenTyp relies on rawTokenTyp, whi…** — dismissed — 2026-11-13 — reason: Duplicate of GCnC: the same defect in the same place. checkOIDCTokenTyp
  (api/internal/auth/oidc_algs.go:84-90) treats rawTokenTyp's "" result as
  an absent typ. rawTokenTyp (api/internal/auth/carrier.go:381-397) returns
  "" for a non-string typ or an undecodable header, so the typ policy fails
  open. GCnC is the more complete write-up and carries the security
  classification. It is not dismissed and is fixed in the
  fail-closed-oidc-typ spec.

- **`iHTc`** — **Project doc api/server/CLAUDE.md is not reac…** — promoted — ref: `iHTc` — 2026-11-13

- **`vrvt`** — **Exchange wraps go-oidc's verification error…** — promoted — ref: `vrvt` — 2026-11-13

- **`GCnC`** — **checkOIDCTokenTyp relies on rawTokenTyp (api…** — dismissed — 2026-11-13 — reason: Already fixed. api/internal/auth/oidc_algs.go:88-114 (checkOIDCTokenTyp) now decodes the
  header itself with Typ as json.RawMessage, returns errOIDCTokenTyp when a 3-segment header
  fails base64url or JSON decoding (lines 93-103), and rejects a present typ that is null or
  not a string (lines 107-109); absent, "JWT" and "aud+jwt" remain accepted. Tests confirm it:
  api/internal/auth/oidc_algs_test.go:293-325 (TestCheckOIDCTokenTyp_FailsClosed: array,
  number, object, bool and null typ plus undecodable headers), :328
  (TestVerifier_OIDC_RejectsNonStringTyp) and :348 (TestOAuth_Exchange_RejectsNonStringTyp).
  The carrier-path part of the original text is tracked separately as bPTZ; the
  non-3-segment bypass is tracked as JxLt.

- **`28P8`** — **Go json matches the member name case-insensi…** — dismissed — 2026-11-13 — reason: Not actionable. The item describes no defect: it observes that Go's encoding/json matches
  "TYP" case-insensitively and that an empty-string typ is accepted, states that neither is
  a defect against the plan's stated typ policy, and recommends no action. Case-insensitive
  member matching can only make the check stricter (a "TYP" key carrying a disallowed value
  is rejected), and an empty typ is treated like an absent typ, which decision 4 accepts. No
  change is called for.

- **`JxLt`** — **checkOIDCTokenTyp returns nil for any input…** — fixed — ref: `phase-05-remediation-02/001-oidc-typ-compact-only-and-carrier-strict.md` — 2026-11-13

- **`bPTZ`** — **rawTokenTyp (used by verifyCarrier to reject…** — fixed — ref: `phase-05-remediation-02/001-oidc-typ-compact-only-and-carrier-strict.md` — 2026-11-13

- **`FNE2`** — **Generic claim style unusable in initProvider** — promoted — ref: `FNE2` — 2026-11-13

- **`JYWh`** — **api/server/CLAUDE.md unreachable from README** — promoted — ref: `JYWh` — 2026-11-13

- **`JdIM`** — **States 'The local carrier verifier uses the…** — dismissed — 2026-11-13 — reason: Duplicate of JOmI. It is the same defect at the same place: the claim at
  docs/architecture.md:192 that "the local carrier verifier uses the same
  typ check helper". JOmI names this location and also the matching
  sentence in sessions-and-tokens.md, and JOmI is dispositioned fix. I
  confirmed the defect is real. The carrier path uses rawTokenTyp
  (api/internal/auth/carrier.go:331,386) and the OIDC paths use
  checkOIDCTokenTyp (api/internal/auth/oidc_algs.go:89, called from
  oauth.go:541 and jwt.go:246).

- **`G8gV`** — **Says the typ header is checked on external O…** — dismissed — 2026-11-13 — reason: Duplicate of JOmI. It is the same defect: the phrase "by the same shared
  helper" at docs/architecture/sessions-and-tokens.md:92. JOmI explicitly
  names this sentence as a second occurrence, and JOmI is dispositioned
  fix. The defect is confirmed in code: two separate helpers exist,
  rawTokenTyp and checkOIDCTokenTyp.

- **`xwrM`** — **The statement that RS256 is accepted by defa…** — fixed — ref: `phase-06-remediation-03/001-correct-oidc-typ-and-alg-doc-wording.md` — 2026-11-13

- **`JOmI`** — **The doc says the local carrier verifier uses…** — fixed — ref: `phase-06-remediation-03/001-correct-oidc-typ-and-alg-doc-wording.md` — 2026-11-13

- **`a3im`** — **checkOIDCTokenTyp also accepts an empty-stri…** — fixed — ref: `phase-06-remediation-03/001-correct-oidc-typ-and-alg-doc-wording.md` — 2026-11-13

- **`YqrL`** — **Unreachable plan-summary docs, dangling links** — promoted — ref: `YqrL` — 2026-11-13

## Remediation

- Rounds used: 3 across all gates (resolved max_rounds: 4 per gate).
- Remediation tasks added: 3 (resolved max_tasks_per_remediation_round per round: 25).
- Remediation phases:
  - `remediation-01` — 1 task(s)
  - `remediation-02` — 1 task(s)
  - `remediation-03` — 1 task(s)
- Security review required: yes (at least one remediation phase carries `security_review: required`).

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — OIDC Verification Algorithm Pinning and EdDSA Acceptance

- [x] [001-eddsa-issuer-harness-and-repro.md](./phase-01-oidc-alg-pinning/001-eddsa-issuer-harness-and-repro.md) — tier `sonnet-med` · branch `plan/oidc-eddsa-audience-tokens-01-001` · commit `200e9daf` · merge `64936cbea9a4501c7a02406b0b759a48f089914f`
- [x] [002-pin-oidc-signing-algs.md](./phase-01-oidc-alg-pinning/002-pin-oidc-signing-algs.md) — tier `sonnet-high` · branch `plan/oidc-eddsa-audience-tokens-01-002` · commit `0c7faac6` · merge `c48f4b85465ba0459f497c539b70768cb7e19bd6`
- [x] [003-alg-confusion-negative-tests.md](./phase-01-oidc-alg-pinning/003-alg-confusion-negative-tests.md) — tier `sonnet-med` · branch `plan/oidc-eddsa-audience-tokens-01-003` · commit `0b6fbb01` · merge `950975d06b0664b65834dc705cf284f4853021b0`

### Phase 02 — EdDSA OIDC Login End-to-End Coverage

- [x] [001-eddsa-login-flow-tests.md](./phase-02-oidc-eddsa-login-e2e/001-eddsa-login-flow-tests.md) — tier `sonnet-high` · branch `plan/oidc-eddsa-audience-tokens-02-001` · commit `fb537df4` · merge `8873a7bfc2350d64f0475701a7c0e690edc4b025`

### Phase 03 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-03-doc-updates/001-update-architecture-docs.md) — tier `sonnet-high` · branch `plan/oidc-eddsa-audience-tokens-03-001` · commit `0e8c8772` · merge `72660c94f4f3f4f6ce2bf277fdb9cd99b33c13e5`
- [x] [002-consumer-notes-and-config-docs.md](./phase-03-doc-updates/002-consumer-notes-and-config-docs.md) — tier `sonnet-med` · branch `plan/oidc-eddsa-audience-tokens-03-002` · commit `54cd25cc` · merge `d8043e4c25eef1762d76002302209c5480994c28`

### Phase 04 — Remediation Round 1

- [x] [001-fail-closed-oidc-typ.md](./phase-04-remediation-01/001-fail-closed-oidc-typ.md) — tier `sonnet-med` · branch `plan/oidc-eddsa-audience-tokens-04-001` · commit `8eac3549` · merge `20c7c28eee09c31ff208cf515acc5812c1c193c4`

### Phase 05 — Remediation Round 2

- [x] [001-oidc-typ-compact-only-and-carrier-strict.md](./phase-05-remediation-02/001-oidc-typ-compact-only-and-carrier-strict.md) — tier `sonnet-high` · branch `plan/oidc-eddsa-audience-tokens-05-001` · commit `af45c635` · merge `004907d74d91431f9d527384d4eeb7765a920e9d`

### Phase 06 — Remediation Round 3

- [x] [001-correct-oidc-typ-and-alg-doc-wording.md](./phase-06-remediation-03/001-correct-oidc-typ-and-alg-doc-wording.md) — tier `sonnet-med` · branch `plan/oidc-eddsa-audience-tokens-06-001` · commit `5ae09eaf` · merge `aa7f6e3`
