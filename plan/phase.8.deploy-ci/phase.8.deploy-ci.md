# Phase 8 — Deploy + CI

## Goal
Same binary, three deploy modes. CI gates merges with lint, unit + integration tests, migration validity, image build, and OpenAPI contract verification.

## Tasks
- 8.1 docker-compose dev loop validated end-to-end (login through admin actions)
- 8.2 ko image build + cosign keyless signing + syft SBOM
- 8.3 Cloud Run example (env-only differences from local)
- 8.4 Kustomize base (CloudNativePG, ingress, OIDC config)
- 8.5 GitHub Actions: lint, test, migrate-check, image build, contract test
- 8.6 OpenAPI contract + codegen verification

## Hard rules
- Same image runs in all three modes; differences live in env vars and orchestration only.
- Kustomize uses CloudNativePG (CNPG) for the database; the manifest does not bake in cloud-specific storage classes — overlays do.
- Cloud Run example uses Cloud SQL via Cloud SQL Proxy sidecar OR direct pgx connection with IAM auth — pick connection-string approach for v1 simplicity, document the trade-off.
- `MaxConns` defaults: serverless overlay sets `DB_POOL_MAX_CONNS=4`; k8s overlay sets `=20`.
- All container images signed with cosign keyless (Sigstore); SBOM attached.
- CI's `migrate-check` runs `atlas migrate validate` against the shadow DB and asserts the schema diff against the latest migration is empty.
- OpenAPI spec lives in `api/openapi.yaml`; CI runs `oapi-codegen` and asserts the generated client compiles AND matches the committed `api-client/`.
