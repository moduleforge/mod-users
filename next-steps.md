# users-module — next steps

All 9 planned phases (foundation → data model → API core → local auth → user mgmt → multi-tenancy → GUI → deploy/CI → Google SSO) have been implemented. Items below are pending manual verification or deferred work that surfaced during implementation. Original phase reports were in `plan/` (now removed); this file is the forward-looking residue.

The assume/login audit gap identified in the post-Phase-5 review was closed on 2026-05-08 (see commits f3214b9, 3b4baa7, 71a8e34 on gui-library-split).

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
- **Account-merging (`MERGE-V2`).** ✅ Implemented 2026-05-10–11 (commits 7d42ddd → dd01ccb on users-module; core-module 1f80162 added `txhelper.RunSerializable`). `auth_oidc_identities` table, multi-identity resolver with 5-branch decision table, verification gating middleware (`RequireVerifiedEmail`), and `/v1/self/identities` + `/v1/self/credential/password` self-service endpoints. Configurable step-up via `AUTH_REQUIRE_STEP_UP`. Review pass (dev/architect/code-reviewer) and resulting B1 + F1–F7 fixes landed.

## MERGE-V2 follow-up work (deferred from final review)

- **MERGE-V2 GUI (Phase 5).** Two pieces. (a) A "verify your email" landing/banner triggered by `/v1/self` returning `email_verified=false` or any /v1 call returning 403 `email_unverified`. (b) A settings page consuming `GET /v1/self/identities`, with "Add Google / Microsoft / ..." via `POST /v1/self/identities/oidc/{provider}/start`, "Set / change password" via `POST /v1/self/credential/password`, "Unlink" via `DELETE /v1/self/identities/{uuid}`, and step-up integration when `AUTH_REQUIRE_STEP_UP=true` (request code, submit code, retry with `X-Step-Up-Token`).
- **Move identity ops to a service layer + adopt `txhelper.Run` end-to-end.** Architect's central finding from the MERGE-V2 review. The resolver (3 naked `pool.Begin` calls) and `IdentitiesHandler` (handler-resident tx code + business logic) should become `IdentityService` with `*InTx` variants. Required prep work before MERGE-V3's explicit cross-account merge, which needs to compose `IdentityService` with `UserAccountService` in a single transaction.
- **Centralize `safeObserve` / `safeObserveAfterCommit` helpers.** Currently copy-pasted across `resolver.go`, `identities.go`, `oidc.go`. Symptom of the service-layer gap above.
- **Deprecate the no-deps `NewIdentitiesHandler` constructor** in favor of `NewIdentitiesHandlerWithDeps`. The no-deps form allocates its own consumed-token `sync.Map` and risks orphaning the step-up janitor if it ever reaches production wiring.
- **Document the informal resource-slug namespace.** `auth_oidc_identity` and `auth_local` are not entity types (no `Entity.Resource()` value); observers fire with these as the `resource` string but no entity registry lookup will resolve them. A short doc block at the call sites would prevent future audit-correlation confusion.
- **Step-up replay window after restart.** The single-use `sync.Map` cache is process-local; a restart leaves a replay window up to the 5-min `StepUpTTL`. Fine for single-process today; a DB-backed consumed-`jti` store is needed before multi-replica or before deploys during peak traffic.
- **`X-Forwarded-Proto` trusted unconditionally** for the Secure cookie flag (`oidc_state` cookie). Document the reverse-proxy assumption; if the API is ever exposed without a proxy that strips inbound `X-Forwarded-Proto`, this is a downgrade vector.
- **`emailcode.go` uses `== pgx.ErrNoRows`** (3 sites). Inconsistent with the rest of the new code which uses `errors.Is`; cosmetic.
- **DB-backed integration test harness** — the final review uncovered the `audit_log.op` CHECK constraint violation (B1) only via cross-module synthesis. A real-DB integration suite would have caught it directly. Same blocker as for the tags-module integration tests (already noted above).
- **Session storage.** Still localStorage + Bearer. Cookie-based sessions are a future consideration.
- **`refreshUser` 401 handling.** The auth-context's `refreshUser` still routes through the shared `request()` helper and triggers a hard redirect on 401; its local `logout()` call becomes redundant. Same category as the return-page bug fixed in Phase 9.2, just not touched.

## Cross-cutting framework — deferred from Phase 5 review

- **`apps` events have nil `target_entity_id`** — documented in `api/internal/handlers/apps.go`. Apps are not core entities; `audit_log.target_entity_id` is NULL for app events, so `AuditService.ListByEntity` never returns them. Future fix: register apps as entities, or add `app_uuid`/`subject_uuid` to `audit_log`.
