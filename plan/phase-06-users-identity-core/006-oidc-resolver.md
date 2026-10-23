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

## Status

Outcome: succeeded (2026-10-23).

Validation: `go build ./internal/auth/... ./internal/config/... ./internal/handlers/auth/...` passes; the auto-link grep and the `access_token`/`#token=` grep over `api/internal/handlers/auth/oidc.go` both return nothing; `go test ./internal/auth/... ./internal/config/...` passes. The `handlers/auth` and `handlers` test packages do not compile (stale pre-rewrite tests, rewritten by task 010); behavior tests are task 010.

Changed: `api/internal/auth/resolver.go` (new: decision table, `ProviderPolicy`, redirect error codes), `api/internal/auth/oauth.go`, `oauth_state.go`, `oauth_claims.go`, `oauth_claims_helpers.go`, `api/internal/handlers/auth/oidc.go` (fence removed, rewritten), `api/internal/handlers/auth/oidc_flows.go` (new: sign-in, JIT, link, step-up, authenticated starts), `api/internal/handlers/oidc_providers.go`, `api/internal/config/provider_merge.go`, `providers.go`, plus unit tests for the resolver, state flows, claim enrichment and provider policy.

Decisions and notes:

- Redirects use the URL fragment throughout: `#handoff=<code>`, `#error=<code>`, `#linked=1`, `#stepped_up=1`, each with `&return=<path>` when a return path was given. The provider's own `error` parameter is mapped to `authentication_failed` (its text is untrusted). Link and step-up success carry no token; after step-up the tab takes a fresh access token from `POST /v1/session/switch`, so `TokenForSessionInTx` is not used here.
- Extra redirect code `identity_in_use` (kept from the old link flow) for a link whose external identity belongs to another principal; the four codes of the API surface are otherwise unchanged.
- JIT `byo` also requires `platform_settings.allow_byo_identity` (refusal `no_account`) and a syntactically valid IdP email (`missing_email`); the identifier is verified only when the IdP asserted `email_verified`. Names come from `given_name`/`family_name` (or `name`), falling back to the email local part and `-`.
- `managed` JIT is a trusted local implementation (entity, principal, `users` row, active `idp_jit` party profile, party-confined `oidc` credential) because `CreateManagedUserInTx` authorizes an acting profile that does not exist yet; its `create user` observation runs as the new profile. No `create profile` observation is written (not in the requirement 8 list).
- `NO_OIDC_ACCOUNTS` caps a `byo` provider to `none` at resolution time (`OAuth.PolicyFor`), so an enable-only DB row cannot re-open JIT; a `managed` provider is unaffected.
- IdP `acr` is parsed only when it is a bare 1, 2 or 3, and passed as the asserted factor acr only when the provider has `trust_acr` (the database applies the same rule).
- Link start enforces `AUTH_SELF_SERVICE_MAX_AUTH_AGE` (409 `users.step_up_required`); link and step-up are refused for impersonation sessions, and link for service accounts, managed users and guests. Link does not confine the new credential to a party-bound provider's party.
- `OAuth.LinkAuthorizeURL` is replaced by `SessionAuthorizeURL`; `ListProviders` moved out of the OIDC handler (the `/v1/auth` `Handler.Providers` serves it).
- The managed JIT path and the link/step-up callbacks were not exercised against a live database.
