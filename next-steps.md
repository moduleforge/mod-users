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

## Cross-cutting framework — deferred from Phase 5 review

- **P1: Reinstate `assume` and `login` audit events** (architect H1 / code-reviewer H3). The `audit_log` CHECK constraint accepts both ops; the original users-module-internal audit (now deleted) wrote `op=login`. After Phase 4.1, neither operation produces an audit row.
  - `api/internal/handlers/assume.go` has no `Authorize` call and no `Observe` call. Wire `Authorizer.Authorize(ctx, "assume", target)` and emit an `Observe(..., "assume", "user_account", ...)` for the assume event. (The act of impersonation is itself the auditable change.)
  - `api/internal/handlers/auth/oidc.go:120` has a stale comment claiming it writes an audit row; remove the stale comment and either: (a) populate `opctx.WithActor(ctx, resolvedEntityID)` after the user is resolved and call the regular ObserverGroup path, or (b) add a dedicated login-audit helper that uses the actor's just-resolved entity ID.
  - Tracking source: `plan/report.users-audit-gap.md` enumerates which exact mutations should emit which ops/resources.
- **`apps` events have nil `target_entity_id`** (architect M2). Apps are not core entities; `audit_log.target_entity_id` is NULL for app events, so `ListByEntity` queries never return them. Either (a) register apps in core's `entities` table, or (b) add an `app_uuid` (or generic `subject_uuid`) column to `audit_log`. Document the limitation in `apps.go` for now.
- **`actor coreservice.Principal` parameter cleanup** — handlers still construct `Principal` and pass to service calls for inline ownership checks. Removing requires either an `IsAdmin` opctx key or moving ownership checks into the Authorizer. Defer until pressing.
- **`UserAccountsHandler.Update` builds `after` snapshot twice** (code-reviewer L4). Extract to a helper.
- **sqlc regenerate verification for `GetUserAccountByAccountHolder`** — query was manually added to `model/db/` during Phase 4.1 because sqlc wasn't available in the worktree. Re-run `sqlc generate` to confirm canonical output matches.

