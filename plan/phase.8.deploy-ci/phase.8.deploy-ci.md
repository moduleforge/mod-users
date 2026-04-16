# Phase 8 — Deploy + CI

## Goal
Same binary, three deploy modes. CI gates merges with lint, unit + integration tests, migration validity, image build, and OpenAPI contract verification.

## Execution scope for v1 (this delivery)

The implementer (Claude in execution mode) has **docker access only** — no AWS, no GCP, no live k8s cluster. Tasks fall into two buckets:

**Locally executable + verified** (must work end-to-end on this delivery):
- 8.1 docker-compose dev loop
- 8.2 image build (local docker; ko build to local daemon; cosign/SBOM commands wired but not pushed)
- 8.6 OpenAPI contract + codegen
- 8.5 GitHub Actions YAML written + lint-validated (`actionlint`); jobs that need a real runner with cloud secrets are documented as such

**Drafted in full, not live-tested**:
- 8.3 Cloud Run manifests + README — drafted to a deployable state; verification step says "review only — apply when GCP access available"
- 8.4 Kustomize base + overlays — drafted; if a kind cluster happens to be available, optional smoke-test, otherwise `kustomize build` is the only verification

Each task file's "How to verify" section is annotated with `[local]` / `[draft-only]` to make this split explicit. The plan is structured so phases 1–7 plus 8.1, 8.2, 8.6 deliver a fully working local stack; cloud/k8s deploy artifacts ship as documented drafts ready for first-pass review when access is granted.

## Tasks
- 8.1 docker-compose dev loop validated end-to-end (login through admin actions) — **local**
- 8.2 ko image build + cosign keyless signing + syft SBOM — **local build, no push**
- 8.3 Cloud Run example (env-only differences from local) — **draft only**
- 8.4 Kustomize base (CloudNativePG, ingress, OIDC config) — **draft only**
- 8.5 GitHub Actions: lint, test, migrate-check, image build, contract test — **YAML drafted + lint-validated**
- 8.6 OpenAPI contract + codegen verification — **local**

## Hard rules
- Same image runs in all three modes; differences live in env vars and orchestration only.
- Kustomize uses CloudNativePG (CNPG) for the database; the manifest does not bake in cloud-specific storage classes — overlays do.
- Cloud Run example uses Cloud SQL via Cloud SQL Proxy sidecar OR direct pgx connection with IAM auth — pick connection-string approach for v1 simplicity, document the trade-off.
- `MaxConns` defaults: serverless overlay sets `DB_POOL_MAX_CONNS=4`; k8s overlay sets `=20`.
- All container images signed with cosign keyless (Sigstore); SBOM attached.
- CI's `migrate-check` runs `atlas migrate validate` against the shadow DB and asserts the schema diff against the latest migration is empty.
- OpenAPI spec lives in `api/openapi.yaml`; CI runs `oapi-codegen` and asserts the generated client compiles AND matches the committed `api-client/`.
