# Consumer upgrade notes

## Purpose and scope

For people who compose, embed or verify tokens from this module (the first consumer is flow-accounts). One section per area, each naming the exact public API, configuration or behaviour change and pointing to the detailed doc rather than repeating it. It covers the EdDSA verify-only key, token hardening, CORS, the cookie base path, the GUI, and `scripts/checkout-deps.sh`, and ends with known limits. Design rationale lives under [Key decisions](./architecture.md#key-decisions) (D14 to D19).

## EdDSA verify-only key

Details: [jwt-eddsa.md](./jwt-eddsa.md).

Configuration (all read by `config.Load`):

| Variable | Meaning |
|---|---|
| `JWT_ALG` | `HS256` (default) or `EdDSA`; any other value fails startup |
| `JWT_SIGNING_KEY` | Ed25519 private key, PKCS#8 PEM; required when `JWT_ALG=EdDSA` |
| `JWT_SIGNING_KID` | optional `kid` header for issued tokens |
| `JWT_VERIFY_PUBLIC_KEYS` | extra accepted public keys: one PKIX PEM, or a JSON `kid` to PEM map |

Public Go API, package `github.com/moduleforge/mod-users/api/auth`:

- `auth.NewVerifierWithOptions(ctx, opts...)` with the options `WithOIDC`, `WithHS256Secret`, `WithLocalIssuer`, `WithEd25519PublicKey` and `WithEd25519PublicKeys`. A verify-only service passes only `WithLocalIssuer` plus public keys and holds nothing that can mint tokens.
- `auth.NewLocalVerifier(ctx, cfg.LocalAuth)`: the verifier matching the local-auth config. Under `JWT_ALG=EdDSA` it accepts only EdDSA (the signing key's public half plus `JWT_VERIFY_PUBLIC_KEYS`); under HS256 it accepts the `JWT_SECRET` plus any configured public keys.
- `auth.NewSigner(cfg.LocalAuth)`: the signer selected by `JWT_ALG`.
- `auth.OAuthStateKey(cfg.LocalAuth)` and `auth.StepUpKey(cfg.LocalAuth)` (additive): resolve the OAuth state and step-up HMAC keys, returning the explicit `JWT_SECRET` when set and otherwise, under EdDSA, a key derived from the signing key. An app that builds the identities or SSH-key handlers itself passes `string(key)` from `auth.StepUpKey` as their `jwtSecret` argument (constructor signatures are unchanged) and should fail startup on error. The returned keys are secret: never log them.

`auth.NewVerifier` keeps its signature but changed behaviour:

- An empty `jwtSecret` now yields a verifier that accepts no HS256 tokens (previously an empty HMAC key was usable).
- A non-empty `jwtSecret` with an empty `localIssuer` is refused with an error.

`JWT_SECRET` is required only under HS256, where it is the signing secret. When the environment variable is unset and a database URL is resolved, `config.Load` does not fail; it reads the secret from the `auth_jwt_secrets` table, or generates 32 random bytes (64 hex characters) and persists them on first boot. This runs over a dedicated short-lived connection (10 second timeout) under a Postgres advisory lock, and creates the table if it does not exist yet. An explicit `JWT_SECRET` always wins; a persisted value shorter than 64 characters fails startup rather than being regenerated; an unreachable database fails `config.Load`.

Under `JWT_ALG=EdDSA`, `JWT_SECRET` is optional and the database bootstrap never runs. When it is unset, the OAuth state and step-up HMAC keys are derived from `JWT_SIGNING_KEY` with HKDF-SHA256 under the labels `mod-users/v1/oauth-state` and `mod-users/v1/step-up`; an explicit `JWT_SECRET` wins and is used for both. It is never accepted as a session-token verification key. Consequences for upgrades: a deployment that relied on the bootstrapped secret under EdDSA and leaves the variable unset sees in-flight state and step-up tokens fail once at the switch (users retry); the `auth_jwt_secrets` row is left untouched and unused; and swapping `JWT_SIGNING_KEY` later invalidates in-flight state and step-up tokens unless an explicit `JWT_SECRET` is set. Details: [jwt-eddsa.md](./jwt-eddsa.md#derived-state-and-step-up-keys) and D19. A verify-only service should not run `config.Load`: build the verifier directly with the options above.

## Token hardening

Details: [jwt-eddsa.md](./jwt-eddsa.md#signer-wiring-for-composing-apps).

- Step-up tokens (JWS header `typ: step-up`) are rejected by the bearer verifier, whatever the issuer configuration. `VerifyStepUpToken` is unchanged: it still requires the exact header and the `credential_change` purpose.
- `IssueStepUpToken` returns `ErrStepUpEmptySecret` for an empty secret; `VerifyStepUpToken` returns `ErrStepUpRequired` for an empty secret.
- An app that composes the handlers itself must build the signer with `auth.NewSigner(cfg.LocalAuth)` and use the signer-requiring constructors `authhandlers.NewWithSigner` and `handlers.NewAssumeHandlerWithSigner` (a nil signer is an error). The OIDC handler builds its signer once at construction; check `oidcHandler.SignerErr()` after construction to fail startup on a bad config.
- Residual: the legacy constructors `authhandlers.New` and `handlers.NewAssumeHandler` are unchanged and cannot see `JWT_ALG`. A handler built with one of them and given no `SetSigner` call falls back to HS256 keyed by the secret it was constructed with (an empty secret fails). Under `JWT_ALG=EdDSA` that mints HS256 tokens the EdDSA verifier will reject, so composing apps should migrate to the `WithSigner` constructors.

## CORS

Details: [cors.md](./cors.md).

- The default changed to deny. Outside `DEPLOY_MODE=local`, an unset or empty `CORS_ORIGINS` emits no CORS headers. Previously an unset value produced `Access-Control-Allow-Origin: *` together with credentials.
- Credentials (`Access-Control-Allow-Credentials: true`) are sent only with an explicit allow-list; the wildcard is never combined with credentials.
- Outside local mode, `*` and any entry containing `*` (for example `https://*.example.com`) is rejected at startup by `config.Load`, with an error naming `CORS_ORIGINS`. `server.New` also fails closed if a hand-built config reaches it.
- Deploy manifests: `deploy/k8s/base/configmap.yaml` and `deploy/cloudrun/service.yaml` ship `CORS_ORIGINS: https://app.example.com`, a placeholder you must replace with your real front-end origins.
- Local-mode caveat: with `DEPLOY_MODE=local`, a `*` pattern entry other than the bare token (for example `https://*.example.com`) is still passed to the CORS library as a pattern and reflected with credentials. Only the bare `*` (or an empty value) gets the credential-free wildcard. Do not use pattern entries, even locally, against anything you care about.

## Cookie base path

Details: [cookie-path.md](./cookie-path.md).

- New `USERS_COOKIE_PATH`: the public base path (for example `/manage`) when a proxy strips the mount prefix. The `oidc_state` cookie `Path` becomes the base plus `/v1/auth/oidc/`; unset keeps the request-derived behaviour. It is validated at startup.
- Breaking facade change: `handlers.NewIdentitiesHandler` gained a trailing `cookiePath string` parameter. Pass `""` for the old behaviour. The manifest entry in `moduleforge.module.yaml` passes `field:cfg.Auth.CookiePath`.
- The OIDC provider-test redirect now keeps the prefix: with `USERS_COOKIE_PATH` set, the browser lands on `<prefix>/oidc-config` instead of `/oidc-config`. Only the validated configured value is used; `X-Forwarded-Prefix` is deliberately never trusted.

## GUI

Details: [gui/README.md](../gui/README.md).

- `AuthPage`'s `onAuthenticated` now receives `(returnPath, mode)`, where `mode` is `'login'` or `'register'` (type `AuthMode`). Callbacks declaring fewer parameters keep working.
- New `email_verified_at` field: an RFC 3339 timestamp, or `null` while the email is unverified. The key is snake_case on the wire; in the GUI it is `UserAccountSelf.email_verified_at` and `useAuth().emailVerifiedAt`.
- Payloads that carry it: `GET`/`PUT /v1/self`; `POST /v1/auth/login`; `POST /v1/auth/email-code/verify`; `POST /v1/auth/anonymous` (always an explicit `null`); and the admin assume-identity response. Register chains login, so it is covered. Use it right after sign-in without a follow-up `refreshUser()`.
- OpenAPI (`api/openapi.yaml`): new schemas `UserSelf` (used by `/v1/self`) and `LoginResponse` (login, email-code verify, assume); `email_verified_at` is no longer on the admin `UserDetail`; `AnonAuthToken.user` documents the null.
- Workspace and lockfile: `gui/` has no lockfile of its own. The single committed lockfile is the repo-root `bun.lock`, covering the root Bun workspace (`gui/` plus the sibling `mod-core/gui`); build `mod-core/gui` first.

## scripts/checkout-deps.sh

Details: the header comment and `--help` of [scripts/checkout-deps.sh](../scripts/checkout-deps.sh); tests in `scripts/test-checkout-deps.sh` (run by `make test.unit`).

- The token (`MODULEFORGE_READ_TOKEN`) is read from the environment only. It travels in git's environment config (`GIT_CONFIG_*`), never in argv, the clone URL or `.git/config`, and is never echoed even under xtrace.
- `MODULEFORGE_TOKEN_ALLOWED_HOSTS` (space- or comma-separated hostnames, default `github.com`) lists the hosts that may receive the token; a lockfile host outside the list is refused (exit 3) before any network call. A self-hosted forge must be added explicitly. Not consulted without a token.
- Lockfile values (host, owner, repo, pin keys, override fields) are shape-validated when read, with fixed error messages that never echo the value.
- The auth header is scoped to the fetch target's host and `http.followRedirects` is off, so the token cannot follow a redirect to another host.
- Requirements: bash 4 or newer (the script uses `${var,,}` lowercasing; it has no explicit version check) and git 2.31 or newer for the `GIT_CONFIG_COUNT` environment config.
- The sibling repos `app-mfgit`, `app-mftodo` and `app-mfmanager` carry their own ported copies of this script, and `flow-accounts` has its own. They are not generated from this one: a future fix here must be mirrored into each copy by hand.

## OIDC issuer TLS verification

- Previously, OIDC issuer discovery over `https://` skipped TLS certificate verification. It now verifies the issuer certificate by default, and JWKS fetches follow the same rule.
- A consumer that relied on a self-signed `https://` issuer in local development must build the verifier with `NewVerifierWithOptions`, passing `WithOIDC` and the new opt-in option `WithInsecureSkipTLSVerify()`.
- `WithInsecureSkipTLSVerify` is for local development only. It logs a warning (`TLS verification is disabled for OIDC issuer; local development only`) naming the issuer host. Prefer trusting the CA instead; see [OIDC troubleshooting](./oidc-troubleshooting.md#self-signed-issuer-certificate).

## Known limits and open items

- Local-mode CORS pattern reflection: see the caveat under [CORS](#cors). Pattern entries are not rejected in `DEPLOY_MODE=local` and are still reflected with credentials.
- `LoginResponse.user` is typed wider than the payload: the GUI types it as `UserAccountSelf`, but the login, email-code and assume payloads carry only `uuid`, `email` and `email_verified_at` (anonymous sign-in carries `uuid`, `is_anonymous` and `email_verified_at`). Call `refreshUser()` when the full profile is needed.
