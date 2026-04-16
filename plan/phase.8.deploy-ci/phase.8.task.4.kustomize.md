# Phase 8, Task 4 — Kustomize base + CNPG

## Acceptance
- `deploy/k8s/base/` contains:
  - `kustomization.yaml`
  - `api-deployment.yaml`, `api-service.yaml`
  - `gui-deployment.yaml`, `gui-service.yaml`
  - `ingress.yaml` (annotated for nginx + cert-manager)
  - `cnpg-cluster.yaml` (CloudNativePG `Cluster` with 1 instance for the example, 3 for prod overlay)
  - `migrations-job.yaml` (Atlas migrate job, run pre-deploy via Argo sync wave or kubectl apply order)
  - `configmap.yaml`, `secret.yaml.example`
- `deploy/k8s/overlays/example/` overlays setting hostnames, replica counts, and `DB_POOL_MAX_CONNS=20`.
- README documents prerequisites (CNPG operator installed, cert-manager, ingress controller).

## How to verify
- **[local]** `kustomize build deploy/k8s/overlays/example` produces clean YAML.
- **[local]** `kubectl apply --dry-run=client -k deploy/k8s/overlays/example` validates against installed CRDs (skips CNPG types if operator absent — note in README).
- **[local, optional]** If `kind` is available: spin a cluster, install CNPG operator, apply overlay, verify `/healthz`.
- **[draft-only — defer until cluster access]** Full round-trip with ingress + cert-manager + Authelia in-cluster.

## Notes
- This task ships as documented draft.
- We don't ship an Authelia chart; the README points to upstream chart with the values needed to register `users-api` as a client.
- Document any CRDs the manifests assume (CNPG `Cluster`, cert-manager `Certificate`) so reviewers can install them in their target cluster.
