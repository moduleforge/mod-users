# Reject CORS Wildcard Patterns And Fix Manifests

## Purpose and scope
Remediates plan findings OO6Y and D6o7 in this plan's `plan/findings.yaml`. Work targets the plan branch `plan/flow-accounts-upstream` and lands through the ordinary per-task loop.

## Requirements
1. Outside `DEPLOY_MODE=local`, `config.Load` (`api/internal/config/config.go`) must reject any `CORS_ORIGINS` entry containing `*`, not only the exact token `*`; the error names `CORS_ORIGINS` and never echoes more than the offending entry shape.
2. `corsOptions` in `api/internal/server/server.go` must fail closed (no CORS middleware, no credentials) when, outside local mode, any entry contains `*`, so a hand-built Config cannot reach the go-chi/cors prefix/suffix pattern matching with credentials. Subdomain patterns are not supported; do not add them.
3. Local mode behavior (permissive `*` without credentials) is unchanged.
4. `deploy/k8s/base/configmap.yaml`: replace the empty `CORS_ORIGINS` with a placeholder origin and an explanatory comment (consistent with `AUTH_FRONTEND_RETURN_URL`). `deploy/cloudrun/service.yaml`: add a `CORS_ORIGINS` entry or comment explaining that default-deny applies.
5. `docs/cors.md` and the README CORS text: state that patterns containing `*` are rejected outside local mode, that entries are bare `scheme://host[:port]` with no trailing slash or path and are matched case-insensitively; add the flow-accounts adoption note if missing.

## Validation
1. `cd api && go build ./... && go vet ./... && go test ./...` passes.
2. New tests: `https://*`, `http*` and `https://*.example.com` rejected by `config.Load` outside local mode; `corsOptions` fails closed for the same entries; exact origins still work with credentials; local mode unchanged.
3. `docs/cors.md` remains reachable from README.

## References
- Finding OO6Y in this plan's `plan/findings.yaml`.
- Finding D6o7 in this plan's `plan/findings.yaml`.
