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
- `kubectl apply -k deploy/k8s/overlays/example` against a kind cluster brings the stack up.
- `/healthz` reachable via the ingress host.
- Login flow round-trips against an in-cluster Authelia (or external OIDC).

## Notes
- We don't ship an Authelia chart; the README points to upstream chart with the values needed to register `users-api` as a client.
