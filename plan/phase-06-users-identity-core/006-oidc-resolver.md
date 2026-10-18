# OIDC Resolver

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Rewrite the OIDC resolver and orchestrator on credentials and sessions: login by known
`(issuer, subject)`, link and step-up only from an authenticated session, JIT by the provider's
`jit_mode`, never link by email, and return through a one-time handoff code instead of a token in
the redirect.

Scope: `api/internal/auth/resolver.go`, `oauth.go`, `oauth_state.go`, the internal claim mappers
moved by task 003, `api/internal/handlers/auth/oidc.go`, `api/internal/handlers/oidc_providers.go`,
`api/internal/config/provider_merge.go`, `api/internal/config/providers.go`.

Suggested role: `plugins/flow/roles/developer-go.md`.

Dependencies within the phase: hard on 003, 004 and 005 (session creation and the
`login_handoff` challenge). Parallel-eligible with task 007. Task 008 mounts its routes.

`api/` as a whole stays uncompilable until task 008; this task makes its own packages build.

## Requirements

1. **Decision table.** Known active `cred_oidc (issuer, subject)`: log in (session with the `oidc`
   factor; IdP `acr` counted only when the provider has `trust_acr`). Unknown identity: JIT by
   `jit_mode`: `byo` creates user, person, personal profile, email identifier and `oidc` credential
   (task 004 primitives); `managed` creates a managed user of the provider's `party_id`, an active
   `idp_jit` party profile and an `oidc` credential confined to the party; `none` refuses
   (`no_account`). Env-only providers act as `party_id NULL`, `jit_mode 'byo'` (`'none'` with
   `NO_OIDC_ACCOUNTS`), `trust_acr false`. Every `oidc` credential this task creates (JIT and
   link) goes through task 004's trusted `CredentialService.LinkOIDCInTx` (decision U24), which
   is also app-mfmanager's pre-provisioning path. The authenticated-session check of requirement 3
   stays in this task, because `LinkOIDCInTx` itself does no authorization.
2. **Never link by email.** An unknown identity whose email matches an existing identifier yields
   `account_exists_sign_in_and_link`; never link to managed or service principals.
3. **Link and step-up modes** only from an authenticated session: the OAuth state carries the
   session uuid; the callback verifies the session is live and its principal matches. Link adds an
   `oidc` credential; step-up adds the factor and raises `acr`/`auth_time` (U8). Provide the handler
   methods for `POST /v1/me/credentials/oidc/{provider}/start` and
   `POST /v1/session/step-up/oidc/{provider}/start` (each returning `authorize_url`); task 008 mounts
   them under the `/v1/me` and `/v1/session` route groups.
4. **Handoff (U7).** The callback creates the session and a `login_handoff` challenge and redirects
   with `#handoff=<code>` or `#error=<code>` (`account_exists_sign_in_and_link`, `no_account`,
   `authentication_failed`, `missing_email`); no token or refresh token in the redirect.
   `GET /v1/auth/oidc/{provider}/start` and `GET /v1/auth/oidc/{provider}/callback` keep their paths.
5. The `oidc_state` CSRF cookie and its cookie-path behavior are unchanged; it is the only cookie.
6. **Providers.** `oidc_providers` CRUD and the provider merge carry `party_uuid`, `jit_mode`,
   `trust_acr` (`managed` requires a party). The OIDC-config ambient admin check stays as is until
   phase `parties-profiles-and-bootstrap` (U9).
7. Delete the email auto-link code paths and their configuration, if any.
8. **Audit events (decision U21).** Use only the ops of
   [API surface section 10.1](../notes/mod-users-api-surface.md):
   - `create` with resource `user` for JIT users;
   - `create` with resource `credential` for every new `oidc` credential;
   - `login` with resource `session` for the callback's new session;
   - `update` with resource `session` for an OIDC step-up.

   Today's resolver emits `create`/`update` on `user_account` and `auth_oidc_identity`. Those
   resources disappear. The callback runs before any `AuthContext` exists, so install the
   unauthenticated-flow `AuthContext` of section 10.1 for its observations.

## Validation

- `cd api && go build ./internal/auth/... ./internal/config/... ./internal/handlers/auth/...` passes.
- `grep -rniE 'link.*by.*email|autolink|auto_link' api/internal/auth api/internal/handlers/auth --include='*.go' | grep -v _test.go`
  shows no surviving auto-link path.
- `grep -rn 'access_token\|#token=' api/internal/handlers/auth/oidc.go` shows no token placed in a
  redirect.
- Behavior tests (no-email-link, JIT modes, link only when authenticated, handoff single use) are
  task 010.

## References

- [API surface sections 2.4, 2.6, 7](../notes/mod-users-api-surface.md).
- [Decisions U7, U8, U21, U24](../notes/decisions-and-resolutions.md).
- [Work packages, package 6](../notes/work-packages.md).
- `docs/oidc-troubleshooting.md` — current behavior (reviewed in the doc-updates phase).

## Checkpoint hints

- After the resolver decision table.
- After link, step-up and handoff in the callback handler.
- After provider columns in the providers handler and merge.
