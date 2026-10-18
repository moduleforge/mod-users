# Setup-Token Bootstrap

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Implement the ADR's first-run bootstrap: `BootstrapService` and the public `/v1/bootstrap` routes,
which, on presentation of the setup token, create the first user, the operator party, the admin
profile and its wildcard `manage` grant in one transaction with `core_claim_operator_party` as the
race-free single winner (U10); extend the setup-token lifecycle so one token serves both OIDC
onboarding and bootstrap (U9); and switch the OIDC-config ambient admin check to "operator profile
with wildcard `manage`". This replaces the first-user `count(*)` race (followup `sxjM`).

Roles: `plugins/flow/roles/developer-go.md` (primary), `plugins/flow/roles/developer-sql.md`.

Depends on task 001 (admin-profile creation goes through `ProfileService`'s internal creation path
so lifecycle observers fire). Runs in parallel with tasks 002, 003 and 005. Writes
`party_authn_policies` and `credential_party_authorizations` rows through the phase-6 queries
directly (trusted flow), not through `PartyPolicyService`.

## Requirements

1. **`BootstrapService`** (`api/internal/service/`, re-exported from `api/usersservice`) with a
   `Bootstrap(ctx, input) (result, error)` method (names the implementer's) that runs one
   transaction creating, in order:
   1. the first user per the phase-6 registration path: bring-your-own user, email identifier
      (treated as verified: the setup-token holder is trusted), a password credential bound to it,
      an `email_otp` credential (U12), the user's own `natural_person` owned by the user (U1, U11),
      `users.person_id`, and the personal profile;
   2. the operator party: either an `organization` (`legal_name`, `legal_form`, optional
      `jurisdiction`; owner NULL) through mod-core `service.CreateEntity`/its legal-entity helpers,
      or, for `{kind: "natural_person"}`, the first user's own person record from step 1;
   3. `core_claim_operator_party(party)`; on false, roll back the whole transaction and return
      `409 users.already_bootstrapped`;
   4. the admin party profile (holder = the first user, party = the operator party, `active`,
      `source = 'manual'`);
   5. a wildcard `manage` grant (entity scope, `target_id` NULL) held by the admin profile,
      inserted in the same transaction through mod-authz's generated `authzdb` create-grant query
      (API surface section 9 item 3). mod-authz phase 13 has merged by now (decision U27), so
      `CreateGrantParams` carries `Scope` (leave it NULL; the query coalesces it to `'entity'`),
      `TargetTypeID` and `IncludeSubtypes`. Its operator-actor trigger accepts the grant only
      because step 3 already claimed the operator party; keep this order;
   6. `party_authn_policies(operator party, credential_rule = 'authorized')`;
   7. `credential_party_authorizations(bootstrap password, operator party, authorized_by = admin
      profile)`;
   8. `credentials.default_profile_id` = the admin profile on the bootstrap password.
   The operation runs as a trusted system-actor context (no grants consulted); no `count(*)` or
   "is this the first user" check remains anywhere.
2. **Routes** (`BootstrapHandler`, `RegisterBootstrapRoutes`; binding names, API surface 6.4),
   public (not behind `requireAuth` or `requireOIDCConfirmed`):
   - `GET /v1/bootstrap/status` → `{bootstrapped, oidc_confirmed}`.
   - `POST /v1/bootstrap` with `X-Setup-Token` and body `operator`, `admin` per API surface
     section 7. Missing or wrong token: 401 `users.setup_token_invalid` (constant-time compare via
     `auth.VerifySetupToken`). Already bootstrapped: 409 `users.already_bootstrapped`. Success:
     201 with the standard token response, a new session with the password factor, landing on the
     admin profile. Emit a `bootstrap` observer event. The events of the bootstrap transaction use
     only the ops of [API surface section 10.1](../notes/mod-users-api-surface.md) (decision
     U21):
     - `create` on `bootstrap` (target the operator party);
     - `create` on `user`, `credential` and `profile` from the shared primitives;
     - `grant` on `grant` for the trusted wildcard insert;
     - `login` on `session` for the new session.

     The request is unauthenticated (setup token only), so install the unauthenticated-flow
     `AuthContext` of section 10.1 for these observations: the new admin profile, its holder and
     the new session. Writes made before the admin profile exists are observed once it exists,
     still inside the transaction. Alternatively, they can be observed under the first user's
     personal profile, which step 1 creates. Choose one approach and record it in this document.
   - Error order: the token header is evaluated before the body (credential-in-header route, as
     `/v1/oidc-config/confirm` does today).
3. **Setup-token lifecycle (U9)** in `api/internal/handlers/oidc_config.go`,
   `api/handlers/onboarding_boot.go` and `api/internal/auth/setup_token.go`:
   - The token is minted and printed at boot (`EnsureSetupTokenAndPrint`) while
     `platform_settings.operator_party_id` is NULL **or** OIDC is unconfirmed, including the
     opt-out and externally-managed boot states.
   - `POST /v1/oidc-config/confirm` with the token no longer clears it while bootstrap is pending;
     a successful bootstrap clears it when OIDC is already confirmed (otherwise it stays for the
     OIDC confirm, which then clears it).
   - `GET /v1/oidc-config/setup-token` (localhost mode) stays available while the token is active
     under the new rule, not only while OIDC is unconfirmed.
   - The banner printed at boot names the bootstrap step as well as OIDC configuration.
4. **Admin check.** The OIDC-config `AdminChecker` (and any other ambient admin check left from
   phase 6) becomes: an authenticated session whose acting profile is an operator profile
   (`core_is_operator_profile`) and holds wildcard `manage`. After bootstrap, OIDC configuration
   requires that session; the setup token is no longer accepted once it is cleared.
5. **Wiring and docs**: `bootstrapService` and `bootstrapHandler` in `moduleforge.module.yaml` and
   `api/cmd/server/main.go`; facades; `api/openapi.yaml`; `AGENTS.md`/`README.md` first-run
   instructions (setup token → bootstrap → OIDC config).
6. **Tests** (unit and integration):
   - happy path for both operator kinds, asserting every row of requirement 1;
   - concurrency: two concurrent bootstraps with the valid token, exactly one succeeds and the
     other gets 409 with nothing left behind (no orphan user, party, profile or grant);
   - a second bootstrap after success gets 409;
   - token lifecycle per U9: confirm-OIDC-first keeps the token for bootstrap; bootstrap-first keeps
     it for OIDC confirm; both done clears it; an invalid token gets 401;
   - the admin check: operator admin session accepted; a personal-profile session of the same user
     refused; a non-operator wildcard holder impossible (mod-authz trigger) or refused;
   - landing: login with the bootstrap password lands on the admin profile; login through the
     email OTP credential lands on the personal profile with the admin profile locked.

## Validation

- `make build`, `make test`, `make lint`, `cd model && make verify` pass; integration tests pass,
  including the concurrency test run with `-count=5` without flakes.
- `grep -rn -i "count(\*)" model/queries api/internal | grep -i -E "user|first"` returns no
  first-user path.
- `grep -rn "FirstUserHook\|firstUserGrant" api moduleforge.module.yaml` returns nothing.
- `api/openapi.yaml` documents `/v1/bootstrap/status` and `POST /v1/bootstrap`.

## Metadata

architectural_impact: true

## Assumptions

- Phase 6 removed the first-user hook (U17) and provides the registration, session and landing
  machinery reused here.
- mod-core's `core_claim_operator_party`, `core_is_operator_profile` and `platform_settings` exist
  in its plan worktree. mod-authz phase 13's grants schema and triggers are merged (decision U27):
  a wildcard grant is accepted for a profile of the claimed operator party.
- The bootstrap GUI page arrives in the GUI phase; this task is API only.

## References

- [API surface](../notes/mod-users-api-surface.md) sections 6.3, 6.4, 7, 9 item 3, 10, 11.
- [Decisions](../notes/decisions-and-resolutions.md) U1, U9, U10, U11, U12, U17, U21, U27.
- [Work packages](../notes/work-packages.md), phase `parties-profiles-and-bootstrap`, package 4.
- mod-core ADR `docs/architecture/identity-model.md`, "Platform administration and bootstrap".
- Existing code: `api/internal/handlers/oidc_config.go`, `api/handlers/onboarding_boot.go`,
  `api/internal/auth/setup_token.go`.

## Checkpoint hints

- After the service transaction and its concurrency test.
- After the routes and setup-token lifecycle changes.
- After the admin check, wiring and docs.
