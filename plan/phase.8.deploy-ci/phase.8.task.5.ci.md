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
- Open a PR; CI runs all jobs in < 10 minutes total.
- Forced schema drift (manual edit) makes `migrate-check` fail.
- Forced API spec drift makes `contract-check` fail.

## Notes
- Use `act` for local CI dry-runs.
- Flaky-test policy: tests that fail intermittently get auto-retried once with annotation; document the policy in CONTRIBUTING.md.
