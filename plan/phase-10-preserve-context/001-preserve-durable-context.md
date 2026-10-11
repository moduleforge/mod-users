# Preserve Durable Plan Context In Docs

## Purpose and scope
This plan's working documents (`plan/overview.md`, `plan/notes/`, the phase task documents with their Status sections, the closed-findings log in `plan/findings.yaml`) are deleted when the plan is finalized. Move whatever is worth keeping into permanent project docs now, so the plan can close out without losing context. The plan's code is already merged on the plan branch; this task is documentation only.

## Requirements
1. Inventory the plan context: read `plan/overview.md`, everything under `plan/notes/`, each task document's Status section under `plan/phase-*/`, and the closed log in `plan/findings.yaml`. For each item decide, and record in this task's Status: **promoted** (to which durable doc), **already captured** (where), or **discarded** (why: purely procedural, superseded, or obsolete, for example the sibling-repo audit whose findings are now fixed in those repos).
2. Create `docs/consumer-upgrade-notes.md`, a permanent doc for people who compose, embed or verify tokens from this module (the first consumer is flow-accounts). One section per area, each naming the exact public API/config/behaviour change and linking to the detailed doc rather than repeating it:
   - EdDSA verify-only key: `JWT_ALG`, `JWT_SIGNING_KEY`, `JWT_SIGNING_KID`, `JWT_VERIFY_PUBLIC_KEYS`; `auth.NewVerifierWithOptions` and its options, `auth.NewLocalVerifier`, `auth.NewSigner`; `NewVerifier` behaviour changes (an empty secret accepts no HS256 tokens; a secret with an empty issuer is refused); `JWT_SECRET` is still required (OAuth state and step-up tokens), and how it is bootstrapped (state the real behaviour from `api/internal/config/jwtsecret_bootstrap.go`).
   - Token hardening: step-up tokens are rejected by the bearer verifier by `typ`; step-up issue/verify refuse an empty secret; handler constructors `authhandlers.NewWithSigner` / `handlers.NewAssumeHandlerWithSigner` and the residual (legacy constructors fall back to HS256).
   - CORS: the new default (no CORS headers unless an explicit allow-list), credentials only with an explicit list, `*` and `*`-patterns rejected outside local mode, the deploy-manifest placeholders, and the local-mode caveat that a `*` pattern entry is still reflected with credentials.
   - Cookie base path: `USERS_COOKIE_PATH`, the breaking facade change (`handlers.NewIdentitiesHandler` gained a trailing `cookiePath string`; pass `""` for the old behaviour) and the OIDC test-redirect prefix.
   - GUI: `AuthPage` `onAuthenticated(returnPath, mode)`; the new `email_verified_at` field (snake_case on the wire; `emailVerifiedAt` in `useAuth()`), which payloads carry it (`/v1/self`, login, email-code verify, anonymous as explicit null, admin assume), and the OpenAPI schemas `UserSelf` and `LoginResponse`; the workspace/lockfile note.
   - `scripts/checkout-deps.sh`: the environment-only token, `MODULEFORGE_TOKEN_ALLOWED_HOSTS`, validation, redirects off, its bash 4 and git 2.31 requirements, and that the sibling repos (`app-mfgit`, `app-mftodo`, `app-mfmanager`) carry their own ported copies, so a future fix must be mirrored.
   - A short "Known limits and open items" list: local-mode CORS pattern reflection, `LoginResponse.user` typing wider than the payload, and the question of making `JWT_SECRET` optional.
   Verify every name, env var and behaviour against the code (grep, and read the handlers); list only things that exist.
3. Capture design rationale that is not already in `docs/architecture.md` or a decision record: why verifier algorithms are pinned per key (no alg confusion), why `kid` rotation uses a map, why `email_verified_at` is snake_case and always present, why EdDSA keeps `JWT_SECRET`, and why CORS fails closed. If the repo keeps decision records (look for `docs/architecture/decisions/` or similar), add concise records following the existing numbering and format; otherwise add a "Decisions" section to `docs/architecture.md`. Do not duplicate text that already exists.
4. Link everything new from `README.md` (or a doc already reachable from it) so the link chain stays intact. Never link from a non-plan doc into `plan/`; do not delete or edit anything under `plan/` except this task document's Status section.

## Validation
1. Every API name, env var and file path in the new docs exists (grep; list the checks run in the Status).
2. All new and edited docs are reachable from `README.md`; internal links and anchors resolve (check by hand or with the repo's link checker if one exists).
3. The Status section lists, for every inventoried item, promoted / already captured / discarded.
4. No code changes; `git diff` shows docs and this task document only.

## References
- `plan/overview.md` and `plan/notes/` in this plan worktree.
- Existing docs: `docs/jwt-eddsa.md`, `docs/cors.md`, `docs/cookie-path.md`, `docs/architecture.md`, `docs/mod-users-spec.md`.
