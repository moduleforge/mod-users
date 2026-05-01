# users-module — next steps

All 9 planned phases (foundation → data model → API core → local auth → user mgmt → multi-tenancy → GUI → deploy/CI → Google SSO) have been implemented. Items below are pending manual verification or deferred work that surfaced during implementation. Original phase reports were in `plan/` (now removed); this file is the forward-looking residue.

Related docs:
- `docs/oidc-troubleshooting.md` — step-by-step checklist for IdP-side login rejection (Microsoft signInAudience matrix, redirect URI exact match, stale `oidc_providers` overrides, etc.).

## Pending manual verification (needs live stack / cloud access)

- **docker-compose + Authelia end-to-end smoke** — Phase 1.3 wired it, but a live Docker test is still pending a restart. Also see the Phase 9 smoke notes: on first boot the API may lose a startup race against Authelia's TLS bootstrap and crash once before `restart: unless-stopped` recovers it (missing `depends_on: authelia: condition: service_healthy` on the API service).
- **Cloud Run / k8s manifests** — shipped as drafts; no cluster or cloud account was available for validation. Needs a real environment before relying on them.
- **ko image signing** — `cosign` was not installed; image builds work but nothing is signed.
- **Real Google / Microsoft OIDC clients** — Google and Microsoft entries in config are templated; neither has an actual client registered with the provider. `.env.example` documents the Google Cloud Console setup steps. Microsoft code path exists (`AUTH_PROVIDER_MICROSOFT_*`) but no Azure AD app registration yet (optional per CLAUDE.md).

## Known open items (code-level, deferred)

- **No DB-backed integration test harness.** All existing tests use hand-rolled in-memory fakes. Adding handler-level integration tests (register → login → /v1/self) against a real Postgres would catch whole classes of issues the unit tests can't. Also blocks the tags-module integration test (see tags-module/next-steps.md).
- **GUI test harness missing.** No vitest/jest scaffolding in `gui/`. Phase 9.2 code has no unit tests as a result. A standalone testing-infra phase is warranted.
- **Multi-tenancy middleware.** `WithAppContext` (X-App header resolution) is partially implemented in the resolver. Full per-request app scoping needs refinement once real tenant scenarios are exercised.
- **Email-code hashing.** Uses bcrypt rather than the SHA-256+salt originally specified. Both are fine for v1; noted for consistency review.
- **Account-merging (`MERGE-V2`).** Merging duplicate OIDC+local accounts by verified email is not implemented. Out of scope for Phase 4 / 9.
- **Session storage.** Still localStorage + Bearer. Cookie-based sessions are a future consideration.
- **`refreshUser` 401 handling.** The auth-context's `refreshUser` still routes through the shared `request()` helper and triggers a hard redirect on 401; its local `logout()` call becomes redundant. Same category as the return-page bug fixed in Phase 9.2, just not touched.
- **`gofmt -l` offenders on `main`** (pre-date Phase 9): `claims_test.go`, `local_jwt.go`, `password.go`, `principal.go`, `resolver.go`, `handlers/auditlog.go`, `handlers/self.go`. Worth a one-commit cleanup.
