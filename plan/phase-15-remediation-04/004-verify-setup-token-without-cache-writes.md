# Verify The Setup Token From A Local Fresh Read Without Rewriting The Shared Cache

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `77AP` (security, priority medium), `HOWM` (optimization, priority low) and `ICke` (documentation, priority low). `verifySetupToken` currently calls `RefreshState` for every request that carries a non-empty setup token, which lets unauthenticated clients force database reads and write-lock acquisitions, and can let a stale refresh overwrite the shared cache. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. `verifySetupToken` in `api/internal/handlers/oidc_config.go` reads the token hash and the confirmed state through a local fresh read (`GetOIDCConfig` plus the boot-state computation) into local variables, and decides from those values. The verify path does not call `RefreshState`.
2. The verify path does not write the process-wide cache (`cachedDB`, `cachedBoot`, `bootstrapped`) and does not re-read the shared cache after its own read. Cache refreshes remain on state-changing paths only.
3. Before any database read, the verify path short-circuits on the cached state: an empty token is rejected without a read, and a request is rejected without a read when the cached hash is already invalid or the cached state is confirmed.
4. The setup-token lifecycle is documented to match the code. The `AGENTS.md` first-run paragraph and the API-layer exceptions bullet in `docs/architecture.md` state that after OIDC is confirmed the OIDC configuration routes require an operator admin session even while the bootstrap is pending, and that `POST /v1/bootstrap` alone keeps the setup token live while the bootstrap is pending.

## Validation

1. Unit tests for setup-token verification cover: an empty token, an invalid token, a valid token while OIDC is unconfirmed, and a valid token while OIDC is confirmed (refused).
2. An integration test: an OIDC configuration route called with the setup token after OIDC is confirmed is refused unless an operator admin session is present.
3. A code search confirms that no `RefreshState` call is reachable from the `verifySetupToken` path.
4. `make build.api` and `make test.unit` pass.

## References

- Finding `77AP` in this plan's `plan/findings.yaml` (type:security, priority:medium): `verifySetupToken` calls `RefreshState` on every request with a non-empty token, so junk tokens force several queries and a write lock per request, and a concurrent refresh can overwrite a newer cache snapshot.
- Finding `HOWM` in this plan's `plan/findings.yaml` (type:optimization, priority:low): `verifySetupToken` runs a database read on each request carrying a non-empty token; the recommendation is to keep the freshness check but drop the shared-cache write and short-circuit on cached state.
- Finding `ICke` in this plan's `plan/findings.yaml` (type:documentation, priority:low): the setup-token lifecycle changed for the OIDC routes, but the `AGENTS.md` first-run paragraph and `docs/architecture.md` still describe the token as valid until both steps are done.
