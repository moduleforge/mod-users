# Wire Risk

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This note is the plan's running list of wire-visible changes (common rule 4). Each implementing task appends or corrects the entries for what it actually changed, under [Recorded by tasks](#recorded-by-tasks). Task 5.2 copies the final list into the project docs (`api/openapi.yaml` where the path exists, `README.md`, `AGENTS.md`, `docs/mod-users-spec.md`, `docs/consumer-upgrade-notes.md`) and names the downstream clients. The planned list below is the planner's expectation, not a record. Decision ids (Dn) refer to the [handler inventory](./handler-inventory.md#planning-decisions).

## Planned changes

The HTTP clients are `@moduleforge/users-gui`, the app GUIs and servers that compose mod-users, and external clients of `/v1/auth/*`.

1. **401 before 400 on direct mounts (D1).** An unauthenticated request with a malformed body or UUID on an authenticated route gets 401 with code `unauthenticated`. Composed apps already run mod-users' `requireAuth` first, so production clients keep seeing 401 `unauthorized` there. The new behavior only shows on direct mounts and in tests.
2. **User accounts mask misses (D2).** A missing account on `GET`/`PUT`/`DELETE /user-accounts/{uuid}` and `POST .../assume` gives 403 to every caller, admins included. It was 404. `POST .../assume` with no actor on the context gives 401 (was 404). The ssh-key operator routes keep their masked 403.
3. **Stricter decoding everywhere (D6, D7).**
   - Unknown JSON fields are rejected with 400 on every body route, including the public `/v1/auth/*` routes. They were silently ignored.
   - Trailing data after the JSON value is rejected.
   - Bodies over 1 MiB get 400 with detail `body_too_large`; the ssh-key register cap stays at 32 KiB, now with `body_too_large`.
   - Decode and path-UUID 400s gain `details` (`invalid_json_body`, `invalid_uuid`, field errors). The top-level code stays `invalid_input`.
   - Literal "invalid JSON body" messages become apiresp's fixed message.
4. **`POST /user-accounts` (D4).** A malformed body, or missing email, given_name or family_name, or a short password, now gives 400 to any authenticated caller. Before, a caller without create permission got 403.
5. **`PUT /user-accounts/{uuid}` (D4).** A present-but-blank `email`, `given_name` or `family_name` gives 400 (was stored). A duplicate email gives 409 `users.email_taken` (was 500; planned, task 4.1).
6. **`PUT /self` (D5).** A present-but-blank `given_name` or `family_name` gives 400 with `natural_persons.given_name_required` or `natural_persons.family_name_required` (was stored blank). App GUIs pairing mod-core's `ProfileEditor` with this route should show the inline error.
7. **Apps membership (D3).** A bad path UUID, malformed body or missing or invalid `user_uuid` gives 400 before the 403. A non-admin with a bad request now sees 400, where before they saw 403. An unknown app for an authorized admin stays 404. `AllowNotFound("app")` is no longer set on the shared entity resolver. Anything else that uses plain `Resolve` with slug `app` on that resolver gets 403 instead of 404, which matches mod-core alone. A body `user_uuid` that names no account gives 422 (was 404; planned, task 4.1). `gui/src/lib/api.ts` `apps.addMember` sending `role` would be rejected as an unknown field; task 5.1 aligns it to `roles`.
8. **Step-up after shape checks (D9).** On the identities and ssh-key credential routes, a malformed body or UUID gives 400 even when no step-up token is present (was 409 `users.step_up_required`). A malformed request no longer consumes a step-up token.
9. **SSH key parsing before authorization (D10).** A malformed `public_key` or label gives 400 to a caller who may not update the account (was 403).
10. **OIDC config (D7, D8).**
    - Admin-bearer or `X-Setup-Token` callers with a malformed body get 400 after authentication. Body-token callers with a malformed body still get 400 first, a documented exception.
    - Provider bodies are capped at 1 MiB, unknown keys are rejected, and trailing data is rejected.
    - The 409 on an existing provider id keeps its status and code and gains detail `users.oidc_provider_exists`.
11. **Public auth literals (D12).**
    - 500 `internal_error` messages become generic.
    - Required-field 400s keep their status and code and may gain field details (new detail codes are listed by task 3.7).
    - `GET /auth/oidc/{provider}/start` no longer echoes the internal error text in its 400 message.
    - Credential-failure 401 messages and the middleware's `unauthorized` 401s are unchanged.

In-process callers:

12. `UserAccountService.Get`/`Update`/`Delete`/`Assume` return `ErrForbidden` on a miss (was `ErrNotFound`). `Assume` with no actor returns `ErrUnauthenticated`.
13. Consumer authorizers (mod-users `Authorizer`, test fakes) receive an extra `Authorize` call with target `0` on each masked miss. It must be denied quietly (common rule 5), and audit logs show one such call per masked miss.

## Recorded by tasks

Tasks append dated entries here: route, old behavior, new behavior, and the client that may notice.

- 2026-10-11, mount-require-auth (1.2): the 8 authenticated registrars (`RegisterAccountRoutes`, `RegisterSelfGetRoute`, `RegisterSelfPutRoute`, `RegisterSelfIdentitiesReadRoute`, `RegisterSelfIdentitiesWriteRoutes`, `RegisterSelfSSHKeysReadRoute`, `RegisterSelfSSHKeysWriteRoutes`, `RegisterUserAccountSSHKeyRoutes`) now mount mod-core `authz.RequireAuth` inside an `r.Group`. Old: 401 depended on the composer mounting a token-verifying `requireAuth`; a direct mount with no actor reached the handler (and a malformed body or UUID got 400). New: a request with no effective actor gets 401 code `unauthenticated` before any handler code. Production is unchanged (the module's `requireAuth` runs first and answers 401 code `unauthorized`); only direct mounts and tests, such as `app-mfmanager` `svcconfine_integration_test.go`, can notice. A sudo-only identity passes the gate.
- 2026-10-11, user-account-masked-miss (2.1), planned items 2, 12 and 13: `GET`/`PUT`/`DELETE /v1/user-accounts/{uuid}` and `POST /v1/user-accounts/{uuid}/assume` now return 403 (`forbidden`, identical envelope) for a UUID that names no account, for every caller, admins included. Old: 404 `not_found`. `UserAccountService.Get`/`Update`/`Delete`/`Assume` return `apiresp.ErrForbidden` on a miss (was `coreservice.ErrNotFound`); `Assume` with no effective actor returns `apiresp.ErrUnauthenticated` (was `ErrNotFound`, so 404). `Assume` now reads the sudo-aware effective actor (`opctx.EffectiveActorEntityID`) for the admin lookup, so a sudo-only identity is no longer rejected there, and the admin account lookup runs after authorization. The ssh-key routes (`SSHKeyService.loadAuthorizedAccount`) share the same helper: a missing account or missing account-holder entity is a masked 403 as before, and a key miss on revoke stays a masked 403 for every caller. Side effects: the ssh-key service now requires an authenticated context (`ErrUnauthenticated` otherwise), and it authorizes before the archived-holder check (an archived holder still gives 403). Consumer authorizers receive one extra `Authorize(op, &0)` call per masked miss and must deny it quietly. Clients that may notice: `@moduleforge/users-gui` admin user-detail pages and any app GUI or server that treated 404 on these routes as "no such user".
- 2026-10-11, decode-wrapper-and-callers (3.2), planned items 3 and 4: `server.Decode` is now a wrapper over `apiresp.DecodeJSON` and all 16 body decodes use it. Every body route, including the public `/v1/auth/*` routes (login, register, anonymous, email-code request and verify, password-reset request and confirm), now rejects unknown JSON fields, trailing data after the first JSON value, and an empty body with 400 `invalid_input` and detail `invalid_json_body`. A body over 1 MiB gives 400 `invalid_input` with detail `body_too_large`; `POST` ssh-key register keeps its 32 KiB cap, now through `apiresp.WithMaxBytes` and with the same `body_too_large` detail (it was a bare 400). The literal "invalid JSON body" messages on `/v1/auth/*` become apiresp's fixed messages ("request body must be valid JSON" and siblings); the status and top-level code are unchanged. `POST /v1/user-accounts` no longer authorizes on decode failure: a malformed body is 400 for any authenticated caller (was 403 for a caller without create permission). `UserAccountService.AuthorizeCreate` stays (still used by `Create` and its tests). Client audit: the `@moduleforge/users-gui` request bodies and every app caller found (app-mfmanager scripts, onboard authclient and GUI, app-mfdemo, app-mfgit, app-mftodo) send only fields the server structs declare, with these exceptions. (a) `auth.resetPassword` in `@moduleforge/users-gui` sends `new_password` but the server reads `password`; before, the field was silently dropped and every GUI reset got 400 `users.password_too_short`; now it gets 400 `invalid_json_body` (unknown field). The server does not accept `new_password`; the GUI field is fixed by task 5.1. (b) `apps.addMember` sends `role` (server reads `roles`) and `apps.create`/`update` target routes this module does not serve; task 5.1 aligns them, and they have no callers today.
