# OIDC State Cookie Path

## Purpose and scope

How the mod-users API chooses the `Path` attribute of the `oidc_state` cookie, and how to set `USERS_COOKIE_PATH` when a reverse proxy strips the public mount prefix before requests reach the API.

## Default behavior

When `USERS_COOKIE_PATH` is unset, the cookie `Path` is derived from the request URL: everything before `/v1/auth/oidc/` is kept as the prefix, and the result ends with `/v1/auth/oidc/`. A request without that marker gets `/v1/auth/oidc/`. This works when the API sees the full public path.

## When to set USERS_COOKIE_PATH

If a proxy strips the mount prefix, the API sees `/v1/auth/oidc/...` while the browser is at `/manage/v1/auth/oidc/...`. The derived `Path` (`/v1/auth/oidc/`) does not match the browser's URL, the browser never returns the cookie, and the OIDC callback fails with HTTP 400 `missing state cookie`.

Set `USERS_COOKIE_PATH` to the public base path. The cookie `Path` becomes the base plus `/v1/auth/oidc/`, for example `/manage/v1/auth/oidc/`. The same value is used when the cookie is set (OIDC start, identity link start) and when it is cleared (callback), so the clear always matches.

The same value prefixes the OIDC provider test redirect: after a provider test, the browser is sent to `<USERS_COOKIE_PATH>/oidc-config` rather than `/oidc-config`. Only the validated configured value is used; `X-Forwarded-Prefix` is never trusted.

Caddy example, where the app is served under `/manage` and the prefix is stripped:

```caddyfile
example.com {
    handle_path /manage/* {
        reverse_proxy mod-users:8080
    }
}
```

```sh
USERS_COOKIE_PATH=/manage
AUTH_OAUTH_REDIRECT_BASE_URL=https://example.com/manage
```

## Validation

`USERS_COOKIE_PATH` is validated at startup; an invalid value makes `config.Load` fail with an error naming `USERS_COOKIE_PATH`. A valid value:

- starts with `/` and has at least one segment (`/` alone is rejected; leave the variable unset for a root mount);
- has no trailing slash and no empty segments (`/manage/` and `/a//b` are rejected);
- has no `.` or `..` segments;
- uses only letters, digits, `-`, `.`, `_`, `~` within segments, so whitespace, control characters (CR/LF), `;`, `,`, `%`, `?`, `#`, and non-ASCII are rejected.
