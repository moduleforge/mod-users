# Phase 8, Task 5 — GitHub Actions CI

## Acceptance
`.github/workflows/ci.yml`:
- Triggered on PRs and push to `main`.
- Jobs (parallel where possible):
  - `lint` — go vet, golangci-lint, eslint, prettier.
  - `test-unit` — `make test.unit` for api and gui.
  - `test-integration` — spins up Postgres service container + Authelia + mailhog; runs `make test.integration`.
  - `migrate-check` — `make model.verify` + asserts `atlas migrate diff` reports no drift.
  - `contract-check` — runs `oapi-codegen` against `api/openapi.yaml` and verifies committed `api-client/` matches.
  - `build-images` — only on push to `main`; builds and signs images, attaches SBOM.
- All jobs cache go modules and pnpm store.
- Status checks REQUIRED on the `main` branch.

## How to verify
- **[local]** `actionlint .github/workflows/*.yml` passes.
- **[local]** `act -j lint` and `act -j test-unit` execute successfully (requires `act` installed; document in CONTRIBUTING.md).
- **[local]** Forced schema drift makes `make model.verify` fail (the same logic the `migrate-check` job runs).
- **[local]** Forced API spec drift makes `make api.client.gen && git diff --exit-code` fail.
- **[draft-only — defer until repo is on GitHub with secrets]** Open a real PR; CI runs all jobs in < 10 minutes total. Image push + cosign signing requires registry credentials in GitHub secrets.

## Notes
- The `build-images` job is fully written but will only succeed once a registry secret is configured. Job is gated on `github.event_name == 'push' && github.ref == 'refs/heads/main'` so PRs don't try to push.
- Use `act` for local CI dry-runs.
- Flaky-test policy: tests that fail intermittently get auto-retried once with annotation; document the policy in CONTRIBUTING.md.
