# CORS Configuration

## Purpose and scope

How the mod-users API decides which browser origins may call it cross-origin, the `CORS_ORIGINS` and `DEPLOY_MODE` settings that control it, and what changed for deployments that relied on the old implicit wildcard.

## Policy

CORS is default-deny. The API emits CORS headers only for origins you list explicitly.

| `DEPLOY_MODE` | `CORS_ORIGINS` | Result |
|---|---|---|
| any | explicit list (`https://app.example.com,https://admin.example.com`) | A matching `Origin` is echoed in `Access-Control-Allow-Origin` with `Access-Control-Allow-Credentials: true`. A non-matching origin receives no CORS headers. |
| `serverless`, `k8s`, `container-host` | unset or empty | No CORS headers: cross-origin browser access is blocked. |
| `local` | unset or empty | Dev convenience: `Access-Control-Allow-Origin: *`, never with credentials. |
| `local` | `*` | Same as the row above. |
| `serverless`, `k8s`, `container-host` | any entry contains `*` (`*`, `https://*`, `http*`, `https://*.example.com`) | Startup fails (`config.Load` returns an error naming `CORS_ORIGINS`). |

Entries are bare `scheme://host[:port]` origins: no trailing slash and no path, matched case-insensitively. Subdomain wildcard patterns are not supported; the underlying CORS library treats any entry containing `*` as a prefix/suffix pattern, so every such entry is rejected outside local mode (and `corsOptions` fails closed if a hand-built configuration reaches it).

The wildcard is never combined with credentials. Every mode except `local` counts as production-like for this guard.

## Behavior change for existing deployments

Earlier versions set credentials unconditionally and the underlying CORS library treated an empty origin list as allow-all, so an unset `CORS_ORIGINS` produced `Access-Control-Allow-Origin: *` together with credentials in every `DEPLOY_MODE`.

- A non-local deployment that left `CORS_ORIGINS` unset (relying on that implicit wildcard) now serves no CORS headers, and its browser clients will start failing cross-origin calls. Fix: set `CORS_ORIGINS` to the exact origins of your front ends.
- A non-local deployment that set `CORS_ORIGINS=*` (or any entry containing `*`) now fails at startup. Replace it with an explicit list.
- A local-mode deployment is unaffected apart from the wildcard no longer carrying credentials.

Adoption note for flow-accounts and other consuming applications: audit the `CORS_ORIGINS` value in each non-local environment before upgrading.
