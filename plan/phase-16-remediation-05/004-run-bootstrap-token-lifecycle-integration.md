# Run Bootstrap Token Lifecycle Integration Test And Confirm RefreshState Wiring

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `PlQx` (bug, priority:high) and `hyek` (documentation, priority:low).

`PlQx`: the TestBootstrap_TokenLifecycle integration subtest (setup-token lifecycle, decision U9, a plan success criterion) compiled but was never executed, so phase 15's bootstrap change is unproven.

`hyek`: an OIDCConfigHandler whose cache was never populated rejects every token, so every construction site must call `onboarding.RefreshState` and the constructor godoc must say so.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. Start a throwaway postgres:16 container with a users superuser, bound to a free 127.0.0.1 port; point AUTHZ_DEV_PG_HOST/AUTHZ_DEV_PG_PORT at it (never a shared database; do not set AUTHZ_DEV_PG_CONFIRM_SHARED); remove the container afterwards, also on failure.
2. Run TestBootstrap_TokenLifecycle (bootstrap_integration_test.go, -tags integration) and confirm it actually executes (not skipped by TestMain); fix any failure it exposes in code or test, staying within the bootstrap/setup-token surface.
3. Confirm every construction of the OIDC config/onboarding handler (api/cmd/server/main.go, the test harness, unit tests) calls RefreshState at startup; fix any that do not, and ensure the handler's constructor godoc states that RefreshState must be called before serving.
4. Report the exact commands and pass/fail output.

## Validation

1. go test -tags integration -run TestBootstrap_TokenLifecycle -v shows the subtests RUN and PASS (not SKIP).
2. The full integration suite passes against the same container; make test.unit passes.
3. docker ps -a shows the throwaway container removed.

## References

- Finding `PlQx` in this plan's plan/findings.yaml (type:bug, priority:high): the TestBootstrap_TokenLifecycle integration subtest compiled but was never run, so the phase 15 bootstrap change is unverified. This task runs it and fixes what it exposes.
- Finding `hyek` in this plan's plan/findings.yaml (type:documentation, priority:low): the OIDC config handler behavioral contract (an unpopulated cache rejects every token) requires RefreshState at every construction site, which api/cmd/server/main.go:168 does; this task confirms the other sites and records the requirement in the constructor godoc.
