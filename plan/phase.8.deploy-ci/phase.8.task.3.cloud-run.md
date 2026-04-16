# Phase 8, Task 3 — Cloud Run example

## Acceptance
- `deploy/serverless/cloud-run/` contains:
  - `service.yaml` — Knative service spec for api (and a separate one for gui).
  - `env.example` — required env vars including `DEPLOY_MODE=serverless`, `DB_POOL_MAX_CONNS=4`, `OIDC_*`, `JWT_SECRET`, `SMTP_*`.
  - `README.md` — step-by-step `gcloud run deploy --image …` with notes on Cloud SQL connection (Cloud SQL connector via env-injected DSN).
  - `migrations.job.yaml` — a Cloud Run Job that runs Atlas migrate before deploys.
- Demonstrated working: deploy in a sandbox project, `/healthz` returns 200, login flow round-trips against a managed OIDC (use Google for the demo).

## How to verify
- README walkthrough produces a working deployment in < 15 minutes from a clean GCP project.
- Concurrency=80, single instance under load shows pool MaxConns=4 in pgx debug log; multi-instance scaling does not exhaust Postgres `max_connections`.

## Notes
- AWS App Runner and Fly.io variants are notes-only in v1; Cloud Run is the canonical example.
