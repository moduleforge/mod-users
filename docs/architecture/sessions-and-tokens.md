# Sessions and Tokens

## Purpose and scope

This document records how `mod-users` turns a successful authentication into a revocable session, how that session is carried to the API as a short-lived access token, and how each request is re-checked against the database. It covers the `sessions` row as the unit of revocation, assurance (`acr`, `amr`, `auth_time`), per-request liveness and profile availability, login landing and profile switching, per-tab refresh lineages, step-up, the OIDC handoff code, client-credentials tokens, impersonation sessions and the setup-token bootstrap. It is written for developers who integrate with or change the module, and for operators who need to reason about what a revocation, a policy change or a key rotation does to live traffic.

The model is the one accepted in mod-core's [identity model ADR](https://github.com/moduleforge/mod-core/blob/main/docs/architecture/identity-model.md); this document records mod-users' share of it and the choices the implementation made on top. The data model, the authorization engine and the route list live in [architecture.md](../architecture.md) (see [Data model](../architecture.md#data-model), [API layer](../architecture.md#api-layer) and [Authentication flow](../architecture.md#authentication-flow)); key and configuration handling for the token signer is in [jwt-eddsa.md](../jwt-eddsa.md). SSH key credentials, which authenticate without a session, are in [ssh-keys.md](./ssh-keys.md).

## Table of contents

1. [The session is the revocation unit](#the-session-is-the-revocation-unit)
2. [Assurance: acr, amr and auth_time](#assurance-acr-amr-and-auth_time)
3. [The access token](#the-access-token)
4. [Per-request liveness](#per-request-liveness)
5. [Profile availability](#profile-availability)
6. [Login landing and profile switching](#login-landing-and-profile-switching)
7. [Refresh lineages](#refresh-lineages)
8. [Step-up](#step-up)
9. [The OIDC handoff code](#the-oidc-handoff-code)
10. [Client-credentials tokens](#client-credentials-tokens)
11. [Impersonation sessions](#impersonation-sessions)
12. [Setup-token bootstrap](#setup-token-bootstrap)
13. [Client contract](#client-contract)

## The session is the revocation unit

A sign-in creates one `mod_users.sessions` row for a `user` principal (service accounts never have a session; they authenticate with a non-interactive credential, see [Client-credentials tokens](#client-credentials-tokens)). Everything a caller can do later hangs off that row, so revoking the row revokes all of it at once.

| Column | Meaning |
|---|---|
| `principal_id`, `app_id` | Who signed in, and the app the session was started for (null when none). Neither can change |
| `acr`, `amr`, `auth_time` | The session's assurance, see the next section |
| `created_at`, `last_seen_at`, `expires_at` | The absolute lifetime is `AUTH_SESSION_MAX_AGE` (default 720 hours) from creation |
| `revoked_at`, `revoke_reason` | Set once on revocation. Reasons: `logout`, `admin`, `credential_revoked`, `principal_disabled`, `refresh_reuse`, `impersonation_ended`, `parent_revoked`, `profile_ended` |
| `impersonator_session_id`, `impersonated_profile_id`, `impersonator_profile_id`, `impersonation_reason` | Set together for an impersonation child session, null together otherwise (see [Impersonation sessions](#impersonation-sessions)) |

The credentials that authenticated the session are its **factors** (`mod_users.session_factors`, one row per credential, whose principal must equal the session's principal). Each tab's refresh tokens live in `mod_users.session_refresh_tokens` (see [Refresh lineages](#refresh-lineages)).

A session ends in one of these ways, each recorded as a revoke reason and as a `revoke` observation on resource `session`:

- the user logs out (`POST /v1/session/logout`) or revokes it from another session (`DELETE /v1/me/sessions/{uuid}`): `logout`;
- an administrator disables the principal: `principal_disabled`;
- a credential that is one of its factors is revoked, or a party's continuous compliance revokes a party-confined credential: `credential_revoked`;
- a spent refresh token is presented again: `refresh_reuse`;
- it is an impersonation session of a profile that then ends: `profile_ended`;
- it is an impersonation session that is ended, or whose administrator session ended: `impersonation_ended`, `parent_revoked`.

Revocation is never cached or delayed: the next request on any access token of the session fails [liveness](#per-request-liveness), whatever the token's own expiry says.

## Assurance: acr, amr and auth_time

Each credential kind has a registry row (`mod_users.credential_kinds`) giving its class (`interactive` or `non_interactive`), its factor class (`knowledge`, `possession` or `federated`), its base assurance (`base_acr`, 1 to 3), whether a service account may hold it, and its `amr` value.

| Kind | Class | Factor | `amr` | Service account may hold |
|---|---|---|---|---|
| `password` | interactive | knowledge | `pwd` | no |
| `email_otp` | interactive | possession | `otp` | no |
| `oidc` | interactive | federated | `fed` | no |
| `anon` (guest device token) | interactive | possession | `anon` | no |
| `ssh_key` | non-interactive | possession | `swk` | yes |
| `api_key` | non-interactive | knowledge | `swk` | yes |

Every seeded kind has a base `acr` of 1. The session's `acr` is derived from its unrevoked factors by `mod_users.session_acr`: the highest effective `acr` of any factor, raised to 2 when factors of two distinct factor classes are present, capped at 3. Level 3 is reserved for phishing-resistant verification, which no seeded kind provides. An `oidc` factor contributes the identity provider's asserted `acr` only when the provider is configured with `trust_acr`; providers defined only by environment variables never are. `amr` lists the factors' `amr` values. `auth_time` is when the user last proved a factor, and step-up resets it.

Assurance is enforced at three places, all re-evaluated per request:

1. **The operation gate.** Each authorization operation can carry a `min_acr` and a `max_auth_age` (the `impersonate` and `credentials` operations carry a ten-minute `max_auth_age`). The engine refuses a request whose session is below `min_acr` or older than `max_auth_age` with the action-required response `409 users.step_up_required`. See [API layer](../architecture.md#api-layer).
2. **The party gate.** A party's authentication policy (`credential_rule`, `min_acr`) decides which of a party's profiles a session can act as and which of its records an outside actor can reach, see [Profile availability](#profile-availability).
3. **Self-service freshness.** Writes to the caller's own credentials and identifiers require `auth_time` within `AUTH_SELF_SERVICE_MAX_AUTH_AGE` (default 10 minutes), authorize on the session's principal being the target rather than on grants, and refuse a stale session with the same `users.step_up_required` action. A user who just signed in passes without friction, so the gate is unconditional.

The request's `acr` and `auth_time` always come from the session row, never from the token's claims, so a step-up or a factor revocation takes effect for tokens already issued.

## The access token

An access token is a **carrier** for a session (claim `sid`) or for a non-interactive credential (claim `cid`); it is never authoritative on its own. It is an EdDSA (Ed25519) JWT with header `alg: EdDSA` and a `kid` when `JWT_SIGNING_KID` is set. HS256 and a configurable algorithm are gone: pinning one asymmetric algorithm lets offline verifiers hold only a public key and removes the algorithm-confusion surface (see [jwt-eddsa.md](../jwt-eddsa.md)).

| Claim | Session token (`sid`) | Client-credentials token (`cid`) |
|---|---|---|
| `iss` | `LOCAL_ISSUER` | same |
| `aud` | the session app's slug, else `AUTH_DEFAULT_AUDIENCE` | `AUTH_DEFAULT_AUDIENCE`, or the requested app slug |
| `sub` | principal uuid (under impersonation, the impersonated holder's) | principal uuid |
| `sid` / `cid` | session uuid | credential uuid |
| `prof` | acting profile uuid | acting profile uuid |
| `acr`, `amr`, `auth_time` | the session's | the kind's `base_acr`, `["swk"]`, issue time |
| `act` | `{"sub": "<administrator principal uuid>"}` only while impersonating | absent |
| `iat`, `exp` | `exp` is the earlier of `iat` plus `AUTH_ACCESS_TOKEN_TTL` and the session's `expires_at` | `iat` plus `AUTH_CLIENT_CREDENTIALS_TTL` |

`AUTH_ACCESS_TOKEN_TTL` defaults to one hour and may not exceed one hour. There are no `email`, `roles`, impersonation-subject or anonymity claims: roles and administrator authority are never carried, they are the profile's grants, resolved on the server per request.

Verification pins `EdDSA`, then checks `iss`, `exp`, the carrier shape (uuid `sub` and `prof`, exactly one of `sid` and `cid`, a non-empty `aud`) and the audience: `aud` must be in `AUTH_ACCEPTED_AUDIENCES`, or, when that is empty, be the default audience or the slug of a registered, non-archived app. A token addressed to an app is therefore refused once the app is archived. Verification then runs the liveness query.

**Audience tokens** are a separate artefact for offline verifiers (a managed app acting as a relying party, a relay). `auth.IssueAudienceToken` signs `iss`, `sub` (the principal uuid), `aud`, `iat`, `exp` and caller-supplied extra claims, with the JWS header `typ: aud+jwt`. It carries no `sid` or `cid`, may not set any reserved claim, and the module's own authenticator rejects it, so an audience token can never be replayed as a bearer token against this API.

The signing key is `JWT_SIGNING_KEY` when set. When it is unset, a key is generated on first boot and persisted in the single-row `mod_users.auth_signing_keys` table, so local development needs no configuration. The OAuth `state` parameter of the OIDC flow is signed with an HMAC key that is `JWT_SECRET` when set and is otherwise derived from the signing key; `JWT_SECRET` signs nothing else. Details and rotation are in [jwt-eddsa.md](../jwt-eddsa.md).

## Per-request liveness

The authentication middleware (`auth.Authenticator`, exposed as the `requireAuth` and `resolveOrAnonymous` manifest middleware) verifies the token offline and then asks the database once per request, through `mod_users.session_liveness` (for `sid` tokens) or `mod_users.credential_liveness` (for `cid` tokens). The single round trip returns the session or credential, the principal, the acting profile and its party, the impersonator, the app, the authoritative `acr`, `amr` and `auth_time`, and a status. Nothing is cached, so revocation has no latency.

The status is `ok`, or one of:

- a revocation status: `session_revoked`, `session_expired`, `parent_session_ended` (the administrator session of an impersonation is gone), `principal_mismatch` (the token's `sub` or `prof` is not the session's), `credential_revoked`, `credential_expired`. The response is `401 users.session_revoked`;
- a profile lock reason, from [Profile availability](#profile-availability). The response is `401 users.profile_unavailable` with the reason in the error details, and the client shows the reason and its remedy.

A request whose session is live and whose profile is available then carries an `opctx.AuthContext` (principal, acting profile, session or credential id, `acr`, `auth_time`, impersonator) and an `auth.Identity` on its context. The authorization engine trusts that context and enforces only the operation gate and the owning-party gate, see [API layer](../architecture.md#api-layer). When the session's app is `invite_only`, the authenticator additionally asks the engine for the `login` operation on the app for the acting profile; a refusal is `403 users.app_login_not_granted`, and an unmet `min_acr` or `max_auth_age` on `login` surfaces as `409 users.step_up_required`.

A request with no `Authorization` header at all is `401`, except on a route group that opts in to `resolveOrAnonymous`, where it proceeds as the shared anonymous system actor (see [Authentication flow](../architecture.md#authentication-flow)). A header that is present but invalid, expired or not live is always `401` and is never downgraded to anonymous.

## Profile availability

A principal acts only as a profile, and a profile is usable by a session only if `mod_users.profile_availability(session, profile)` says `available`. The function runs once for the profile list, the landing chain, a switch, a refresh and every request (inside liveness). Otherwise it names the first failing lock reason:

| Lock reason | Meaning |
|---|---|
| `not_held` | The session's principal does not hold the profile |
| `inactive` | The profile is not `active` (invited, suspended or ended) |
| `principal_disabled` | The principal is disabled |
| `confined` | A factor is confined to another party (see below) |
| `credential_not_authorized` | The party's `authorized` rule is not met: no factor is authorized for the party or confined to it |
| `provider_required` | The party's `provider` rule is not met: no factor from the required OIDC provider |
| `acr_too_low` | The session's `acr` is below the party's `min_acr` |

The rules, in order:

1. **Held and live.** The profile is held by the session's principal (or is the impersonated profile of an impersonation session), is `active`, and the principal is not disabled.
2. **Confinement.** A credential can be confined to one party (`credentials.confined_to_party_id`); it then authenticates only profiles of that party. The check covers every factor of the session: a session that includes a credential confined to party X can act only as profiles of X, so a managed user's party-controlled credential cannot be used to reach any other party.
3. **The party gate** of the profile's party. Personal profiles have no party gate. A party's authentication policy (`mod_users.party_authn_policies`; no row means `any` and `min_acr` 1) has a `credential_rule` of `any`, `authorized` (a factor must be listed in `credential_party_authorizations` for the party or be confined to it) or `provider` (a factor must come from the required OIDC provider), and a `min_acr`. An outside-collaborator or invitation confirmation (`profile_policy_confirmations`) exempts the profile from the `credential_rule` only, never from `min_acr`.
4. **`min_acr` always.** The session's `acr` must be at least the party's `min_acr`.

Compliance is continuous because the function runs on every request: raising a party's `min_acr`, changing its rule, deauthorizing a credential or revoking a factor locks the affected profiles on the holders' next request without touching any token. Revoking a credential also revokes every session that holds it as a factor.

Client-credentials calls use the sibling `mod_users.credential_profile_availability`, which applies the same rules with the single credential as the only factor and its kind's `base_acr`. The SSH resolver uses it for the profile an SSH key acts as (see [ssh-keys.md](./ssh-keys.md#d9-credentials-operation-and-profile-derivation)).

The gate that guards **content owned by a party the acting profile is outside of** is a separate check made by the authorization engine, not by availability: see the owning-party gate in [API layer](../architecture.md#api-layer).

## Login landing and profile switching

A sign-in (password, email code, OIDC, guest, registration, bootstrap, and a handoff exchange) returns a **token response**:

```json
{
  "access_token": "<jwt, or null>", "token_type": "Bearer", "expires_in": 3600,
  "refresh_token": "<opaque, per tab, or null>",
  "session": {"uuid": "...", "acr": 1, "amr": ["pwd"], "auth_time": "RFC3339", "impersonating": false},
  "profile": {"uuid": "...", "kind": "personal", "available": true, "lock": null},
  "selection_required": false,
  "profiles": []
}
```

`profiles` lists every non-ended profile of the principal with its `available` flag and, when locked, the `lock` reason and a `remedy` (for example `sign_in_with_provider`). The token acts as the **landing profile**, chosen by this chain, taking the first candidate that is available to the new session (and admitted by the app's `login` gate):

1. the profile the client named (`profile_uuid`), if any, and only that one;
2. the default profile of the credential that signed in (`credentials.default_profile_id`);
3. the user's default profile (`users.default_profile_id`);
4. the user's personal profile;
5. the only available profile, if exactly one is.

When the chain yields nothing, `access_token` is null and `selection_required` is true; the client then calls the refresh grant with a chosen `profile_uuid`. The credential-level default is what lets one sign-in method open straight into a business profile while another opens into the personal one: the operator bootstrap's password lands on the administrator profile, an email-code sign-in lands on the personal profile with the administrator profile locked by the operator party's policy.

**Switching profiles** is `POST /v1/session/switch` with a `profile_uuid`. It re-runs availability, writes nothing, mints no refresh token and returns a new access token acting as that profile for the same session. The active profile therefore lives only on the access token, which is what lets several tabs of one browser act as different profiles under one session. An unavailable profile is `401 users.profile_unavailable`; an invite-only app's gate is `403 users.app_login_not_granted`. An impersonation session cannot switch or fork (`403 users.impersonation_not_allowed`): its only profile is the impersonated one.

## Refresh lineages

Each browser tab owns one **refresh lineage**: a chain of opaque, single-use refresh tokens (`mfr_` prefix, only a SHA-256 hash stored) in `mod_users.session_refresh_tokens`, all under the one session. Tokens in a lineage share a `lineage_uuid`. Every use rotates the token:

- `POST /v1/auth/token` with `grant_type: refresh_token`, the token and the `profile_uuid` to act as marks the presented token used, mints its successor in the same lineage, re-runs the profile's availability and returns a new token response. The successor expires `AUTH_SESSION_IDLE_TIMEOUT` (default 168 hours) from now, and never after the session itself.
- **Reuse detection.** Presenting a token that was already used means a copy of it leaked or a client lost a response. The whole session is revoked (`refresh_reuse`, committed before the error returns) and the call answers `401 users.refresh_reuse`. Per-tab lineages are what keep this sound: tabs that shared one rotating token would raise false alarms on every race.
- An unknown or expired token, or a session that is no longer live, is `401 users.session_revoked`. An unavailable profile is `401 users.profile_unavailable` and rolls the rotation back, so the token stays usable and the client can choose another profile.

The profile is not stored on the lineage: each refresh names it. A profile switch does not touch the lineage either.

**Fork.** A new tab has no lineage. It obtains one with `POST /v1/session/fork`, authenticated by a live access token (typically handed over from a sibling tab), which mints a new lineage under the same session and returns a token response acting as the caller's current profile. Revoking the session ends every lineage at once. An impersonation session cannot fork.

## Step-up

Step-up is **adding or re-verifying a factor in the current session**, not a separate token. It recomputes the session's `acr`, sets `auth_time` to now and returns a fresh access token. The HMAC step-up token, the `X-Step-Up-Token` header, `AUTH_REQUIRE_STEP_UP` and the step-up replay cache of the earlier model no longer exist.

A client reaches step-up when a request is refused with the action-required `409 users.step_up_required` (an operation's `min_acr` or `max_auth_age`, a party's `min_acr`, stale self-service `auth_time`). It then runs one of:

- `POST /v1/session/step-up/password` re-verifies a password of the principal;
- `POST /v1/session/step-up/email-code/request` and `.../verify` send and check an emailed code (a `step_up` challenge bound to the session) and add the email credential as a factor;
- `POST /v1/session/step-up/oidc/{provider}/start` returns an authorize URL; the OIDC callback adds the provider credential as a factor and redirects with `#stepped_up=1`. The principal must hold an active `oidc` credential for the provider.

The password and email-code calls answer with a token response (no refresh token). After the OIDC round trip the client takes a new access token with the refresh grant. The client then retries the interrupted request once. A refused step-up is itself `409 users.step_up_required`. An impersonation session cannot step up: its factors belong to the administrator, and it evaluates its rules on the administrator's session.

Email codes (login, verification and step-up) share the challenge limits of `mod_users.auth_challenges`: at most five verification attempts per code (the final claim consumes it and a correct guess that holds an earlier attempt is refused when a concurrent final claim already did), a 60 second cooldown measured from the newest challenge of the same subject in any state, enforced atomically in the database by `mod_users.create_challenge_with_cooldown`, a five minute default lifetime and single use. Password-reset tokens and handoff codes are high-entropy opaque tokens stored as hashes, not attempt-bounded numeric codes.

## The OIDC handoff code

An OIDC sign-in never puts a token, or anything that refreshes one, in a redirect URL, because URLs land in browser history. The flow is:

1. The browser generates a random verifier (43 to 128 characters of `[A-Za-z0-9-._~]`), keeps it, and calls `GET /v1/auth/oidc/{provider}/start?handoff_challenge=<unpadded base64url SHA-256 of the verifier>`. The challenge is carried in the signed `state`, which is also written to the `oidc_state` cookie. A missing or malformed challenge is `400 users.handoff_challenge_invalid`.
2. The provider redirects to `GET /v1/auth/oidc/{provider}/callback`. The module validates the state and cookie, exchanges the code, verifies the ID token and resolves the verified `(issuer, subject)` against active `oidc` credentials. It **never links an identity by email**: an unknown subject whose email matches an existing account is refused with `#error=account_exists_sign_in_and_link`, and the user signs in another way and links the provider from their account.
3. On a sign-in the callback creates the session and a one-time `login_handoff` challenge (60 second lifetime, single use) bound to the challenge from the state, and redirects to the frontend with `#handoff=<code>`. Link and step-up flows redirect with `#linked=1` and `#stepped_up=1`; a refusal redirects with `#error=<code>` (`account_exists_sign_in_and_link`, `no_account`, `authentication_failed` or `missing_email`).
4. The browser posts the code and the verifier to `POST /v1/auth/handoff`. The module hashes the verifier, compares it in constant time and, if it matches, consumes the code and returns the token response. A missing verifier is `400`. A spent, expired or unknown code, and a wrong or malformed verifier, are all `401 users.invalid_credentials`, and a wrong verifier does not spend the code. An intercepted code is useless without the verifier that never left the originating browser.

The `oidc_state` cookie is the only cookie the module sets. It is an anti-forgery state for the redirect flow, not an authentication cookie, and its `Path` follows `USERS_COOKIE_PATH` (see [cookie-path.md](../cookie-path.md)).

## Client-credentials tokens

Service accounts authenticate with an `api_key` credential (a `client_id` and a once-shown `client_secret`, stored only as a hash). `POST /v1/auth/token` with `grant_type: client_credentials`, the `client_id`, the `client_secret` and optionally a `profile_uuid` and an `app` slug issues an access token with a `cid` claim. There is no session, no refresh token and no `auth_time` to refresh: the token lives `AUTH_CLIENT_CREDENTIALS_TTL` (default 15 minutes), `amr` is `["swk"]` and `acr` is the kind's base level. The acting profile is the requested one, else the credential's default profile, else the principal's personal profile, and must pass `credential_profile_availability`. Bad credentials are `401 users.invalid_credentials`. Per-request liveness for such a token is `credential_liveness`, so revoking the credential stops the token on its next request. Issuing and revoking keys is the `credentials` operation on the service-account principal, see [API layer](../architecture.md#api-layer).

## Impersonation sessions

Impersonation is a **separate child session** of the administrator's own session, not a switch of the administrator's profile.

- `POST /v1/impersonations` takes a `profile_uuid` and a `reason`. It needs the `impersonate` operation on the target profile, whose `max_auth_age` is ten minutes (a stale authentication is `409 users.step_up_required`). The child session records the reason, the real administrator (`impersonator_session_id`), the administrator's acting profile (`impersonator_profile_id`), the impersonated profile, and copies the parent's `acr`, `amr` and `auth_time`, so an impersonation never looks more recently authenticated than its administrator.
- The answer is a token response **without a refresh token**; the token's `sub` and `prof` are the impersonated holder and profile and `act.sub` is the administrator's principal uuid. It lives at most `AUTH_IMPERSONATION_TTL` (default 30 minutes).
- `POST /v1/impersonations/current/renew` extends it to now plus the TTL, never beyond `AUTH_IMPERSONATION_MAX` (default 60 minutes) from its start (`409 users.impersonation_limit_reached` when no room is left). A renewal re-checks that the administrator still holds `impersonate` on the profile, from the **parent session** and as the administrator's recorded acting profile; a refusal answers `403` and ends the impersonation, and an ended parent answers `401 users.session_revoked`. The operation's `max_auth_age` is not re-applied (freshness was enforced at start); `min_acr` still binds.
- `DELETE /v1/impersonations/current` ends it (`impersonation_ended`).

The rules that make it safe:

- **The impersonated actor's rules apply to every operation**, except stopping the impersonation. What the session may do is decided by the impersonated profile's authorizations, and every party's authentication policy applies as for that profile, evaluated on the administrator's factors and `acr` (the parent session), because the child has none of its own (`mod_users.session_policy_factors`). The only exemption is the `credential_rule` when the impersonation was started through a platform operator profile, keyed on the recorded acting profile and not on the principal merely holding an operator profile; `min_acr` binds everyone. Confinement is applied to non-operator administrators on the same key.
- **Nesting is impossible by construction.** A database invariant (`MF101`) forbids an impersonation session whose parent is itself an impersonation, so no authorization check has to catch it, and the nested-impersonation audit confusion of the earlier model cannot occur. `POST /v1/impersonations` additionally refuses a caller whose session is already an impersonation, and the profile held by the caller itself, with `403 users.impersonation_not_allowed`.
- **Credential and identification data is off limits.** An impersonating context is refused credential administration, managed-user creation, party policy and credential authorization writes, and ending a profile. A tracked, planned follow-up replaces these narrow refusals with a general **data protection class** mechanism, not yet built: data and fields would be marked `no-write` (readable, never writable), `no-read` (writable, such as appending a support note, but never readable) or `no-access` (neither, for credentials, names, contact details and other identifying information), and the impersonation-context check would run only when a function touches marked data. Until it exists, the refusals listed in [API layer](../architecture.md#api-layer) are the whole of the protection.
- **Revocation cascades.** Revoking or expiring the administrator's session ends the impersonation on its next request (`parent_session_ended`; the child is revoked with `parent_revoked`). Logging out of the API revokes the caller's own session, so a client that holds an impersonation token must not call logout with it and expect the administrator to be signed out: the client restores the administrator session and logs that out, which revokes the administrator session and, with it, the child. The reference GUI does this.
- **The impersonation token is never refreshed and never replayed as the administrator.** There is no refresh token for it. When the impersonation token expires, a client does not exchange a refresh token for a replacement and does not retry the failed request under the administrator's token (that would run the request as the administrator instead of the impersonated profile); it drops the impersonation token, keeps the administrator's own lineage, and restores the administrator's token through that lineage's refresh grant.
- **Audit.** The administrator is the impersonator on every request made inside the session; start, renew and end are observed as `impersonate`, `update` and `revoke` on resource `impersonation`, targeting the impersonated profile.

## Setup-token bootstrap

A fresh deployment has no operator party and an unconfirmed OIDC configuration. At boot the server mints one **setup token** and prints it according to `TOKEN_DISPLAY`. The token is active while the operator party does not exist **or** OIDC is unconfirmed, and it is the credential for both first-run steps:

- `POST /v1/bootstrap`, with the token in `X-Setup-Token` (checked before the body is read), creates in **one transaction** the first user (bring-your-own, with their own person record, personal profile, verified email identifier with its email-code credential, and a password bound to it), the operator party (an organization, or the first user's own natural person), claims it with mod-core's `core_claim_operator_party` (a single race-free winner, replacing the old unlocked `count(*)` first-user check; losing is `409 users.already_bootstrapped`), the administrator's party profile with a wildcard `manage` grant, the operator party's authentication policy (`credential_rule: authorized`) with the bootstrap password authorized for it and set to land on the administrator profile, and a password session. It answers `201` with a token response landing on the administrator profile.
- `POST /v1/oidc-config/confirm` and the provider routes accept the token only while OIDC is unconfirmed. Confirming OIDC while the bootstrap is pending does not clear the token, so the ordering trap of confirming OIDC first is gone.

Whichever step completes last clears the token. From then on OIDC configuration requires an operator administrator session: a session whose acting profile is an operator profile holding a wildcard `manage` grant. `GET /v1/bootstrap/status` reports `bootstrapped` and `oidc_confirmed`. With `TOKEN_DISPLAY=none` no token is shown, so the bootstrap cannot run. The first-user hook that earlier granted `manage` to whichever account registered first is removed.

## Client contract

The API accepts only `Authorization: Bearer`; no route reads an authentication cookie. A client holds its tokens per tab (memory or `sessionStorage`), never in `localStorage` or a cookie, and sends every request with `credentials: 'omit'`. A new tab obtains a lineage by forking from an access token a sibling tab hands over (never a refresh token), or by signing in. On `401 users.profile_unavailable` it shows the lock reason and remedy; on `401 users.session_revoked` and `401 users.refresh_reuse` it signs out; on `409 users.step_up_required` it runs a step-up and retries once.

There is one deliberate exception to `credentials: 'omit'`: the two OIDC authorize-URL start calls, `POST /v1/me/credentials/oidc/{provider}/start` (link) and `POST /v1/session/step-up/oidc/{provider}/start` (step-up), set the signed `oidc_state` cookie that the redirect callback validates, and a browser discards a cross-origin `Set-Cookie` unless the request sends `credentials: 'include'`. Both calls still carry the Bearer token, and the cookie is anti-forgery state rather than authentication, so the contract (no authentication cookie) is intact. The API does not change for this. The sign-in start is a plain browser navigation (`GET /v1/auth/oidc/{provider}/start`) and needs no exception. The client library's handling is in [`gui/README.md`](../../gui/README.md).

## Related documents

- [docs/architecture.md](../architecture.md) — [Data model](../architecture.md#data-model), the [authorization engine](../architecture.md#api-layer) that consumes the request's context, and the [Authentication flow](../architecture.md#authentication-flow) per channel.
- [docs/jwt-eddsa.md](../jwt-eddsa.md) — signing key, rotation, verify-only deployment and the derived OAuth state key.
- [docs/architecture/ssh-keys.md](./ssh-keys.md) — SSH key credentials, which authenticate without a session.
- [docs/mod-users-spec.md](../mod-users-spec.md) — the functional specification and API checklist.
- [`api/openapi.yaml`](../../api/openapi.yaml) — the authoritative route and schema reference.
