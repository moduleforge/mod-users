# deploy/k8s

Kubernetes deployment manifests (Kustomize). `base/` holds the draft Deployment, Service and ConfigMap; the Deployment reads secrets (`JWT_SIGNING_KEY`, `JWT_SECRET` and similar) from a `users-api-secrets` Secret that the operator creates.
