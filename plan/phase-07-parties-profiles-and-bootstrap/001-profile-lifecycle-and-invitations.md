# Profile Lifecycle and Invitations

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Implement `ProfileService` (party profile lifecycle, invitations, managed users, lifecycle
observers) and its HTTP routes, and create the `PartiesHandler` that the other tasks in this phase
extend. This is the first task of the phase: tasks 002 (party gating), 003 (outside
collaborators), 004 (bootstrap) and 007 (trusted admission, `AdmitInTx`) build on the service and
handler skeleton it delivers. Task 005 (SSH derivation) does not depend on it.

Roles: `plugins/flow/roles/developer-go.md` (primary), `plugins/flow/roles/developer-sql.md` (for
any query changes). No standard skill covers this task.

In scope: `api/internal/service/` (new profile service files), `api/usersservice/` facade,
`api/internal/handlers/` (new parties, profiles and `/v1/me/profiles/*` action handlers),
`api/handlers/handlers.go` facade, `api/cmd/server/main.go` and `moduleforge.module.yaml` wiring,
`api/openapi.yaml` for the routes added, `AGENTS.md`/`README.md` rows describing them.

Out of scope: policy-mismatch detection (task 003 fills the seam this task leaves), party authn
policy and credential-authorization routes (task 002), bootstrap (task 004), owned-scope grants
(the engine phase re-tests these flows with them).

## Requirements

1. **`ProfileService`** in `api/internal/service/`, re-exported from `api/usersservice` with the
   binding names of API surface section 6.3:
   - `InviteInput`, `Profile`, `ProfileStatusChange`, `ProfileLifecycleObserver`,
     `(*ProfileService).InviteInTx`, `EndInTx`, `AddLifecycleObserver` with exactly the signatures
     in section 6.3.
   - Invite by `HolderUserID` or by `HolderEmail` (exactly one; 400 otherwise). Email resolves an
     existing user through `mod_users.user_identifiers` (`kind = 'email'`, canonicalized value).
     An unknown email returns a not-found error; no account is created by an invite.
   - Invite creates a mod-core `profiles` row with `status = 'invited'`, `source = 'manual'`, owner
     = the party, `invited_by_profile_id` = the acting profile (mod-core column). A second open row
     for the same (holder, party) maps `profiles_one_open_per_holder_party` to 409
     `users.profile_exists`. The mod-users trigger on `public.profiles` (phase 6) already rejects
     guests and managed users of another party. Check these cases in Go first and map the trigger's
     `MF103` to the binding errors of API surface 6.3: `ErrGuestMustUpgrade` (409
     `users.guest_upgrade_required`) for a guest, and `ErrHolderNotAdmissible` (409
     `users.holder_not_admissible`) for a managed user of another party or a non-user holder.
     Task 007's `AdmitInTx` reuses both errors (decision U22).
   - Authorization: `authz.AuthorizeTypeIn(ctx, "create", <profile type id>, partyID)` for invite.
     Until the engine phase, mod-core's helper falls back to wildcard and type grants (U20); do not
     add any other fallback. mod-authz phase 13 has merged by now (decision U27), so its trigger
     admits wildcard and type-entity grants only for operator actors. This task's tests therefore
     authorize invites and managed-user creation with an operator-party administrator. Phase 8
     task 005 re-tests the flows with a non-operator party's owned-scope grant.
   - `ConfirmPolicyMismatch` is accepted and carried through `InviteInTx` to a single, clearly named
     internal seam (for example `checkInvitePolicyMismatch(ctx, tx, in) error`) that this task
     implements as a no-op returning nil, with a comment naming task 003 as its owner.
   - Holder actions: accept (`invited → active`), decline (`invited → ended`), leave
     (`active|suspended → ended`). Authorized by `session.principal = profile.holder` (self-service,
     not grants), from any of the holder's sessions.
   - Admin actions: suspend (`active → suspended`), resume (`suspended → active`), end
     (`→ ended`, with an `end_reason`). Authorized by `update` on the profile entity
     (`Authorize`), plus a step-up freshness check on `auth_time` using the same mechanism and
     setting the phase-6 principal disable route uses (do not invent a second setting).
   - Status transitions follow mod-core's rules (`invited → active ⇄ suspended → ended`; `ended`
     terminal; MF004 from mod-core). Map MF004 to 409.
   - **Departure cleanup** in `EndInTx` (used by end, leave and decline paths where applicable):
     delete the holder's `credential_party_authorizations` rows for the party; revoke credentials
     confined to the party through `CredentialService.RevokeInTx` with reason `departure` (which
     revokes dependent sessions); when the holder is a managed user and no non-ended profile remains,
     disable the principal through the phase-6 principal-disable path (which revokes sessions). No
     cascade trigger on sessions is needed: per-request liveness rejects tokens naming an ended
     profile.
   - **Managed users**: `CreateManagedUserInTx` (name is the implementer's) creates, in one
     transaction, a `user` principal owned by the party with `managing_party_id` = the party, no
     personal profile and no person (U11), an email identifier, an active party profile
     (`source = 'manual'`), and an initial interactive credential (password bound to the email
     identifier, or an email OTP credential; choose one and document it in `api/openapi.yaml`).
     Authorization: `AuthorizeTypeIn(ctx, "create", <user type id>, partyID)` and the profile
     `create` check as above. Respect the phase-6 deferred personal-profile check (a managed user
     has none).
   - **Lifecycle observers**: every status change (including creation as `invited` or `active`)
     calls each registered `ProfileLifecycleObserver.ProfileStatusChanged` inside the transaction;
     an observer error aborts the transaction. Emit `profile` observer events for mod-audit with
     only the ops of [API surface section 10.1](../notes/mod-users-api-surface.md) (decision U21):
     - `create` for a new profile (invite, managed user);
     - `update` for every status change (accept, decline, leave, suspend, resume, end);
     - departure cleanup's credential revocations emit `revoke` on `credential` (through
       `RevokeInTx`);
     - deleted credential authorizations emit `delete` on `credential_party_authorization`;
     - a managed user's disable emits `update` on `user`.

     No other verb is allowed. Keep the internal creation and status-change paths reusable,
     because task 007's `AdmitInTx` calls them.
2. **Routes** (API surface section 7), mounted behind `requireAuth`:
   - `PartiesHandler` with `RegisterPartyRoutes` (binding names, section 6.4):
     `GET /v1/parties/{party_uuid}/profiles` (authorize as `AuthorizeTypeIn(read, profile, party)`
     or `list`; pick one and document it), `POST /v1/parties/{party_uuid}/profiles` (invite:
     `holder_email` or `holder_user_uuid`, `confirm_policy_mismatch`),
     `POST /v1/parties/{party_uuid}/managed-users`. Structure the handler so task 002 can add the
     authn-policy, credential-authorization and confinement routes to the same registration
     function.
   - `PATCH /v1/profiles/{uuid}` (`status`, `end_reason`).
   - `POST /v1/me/profiles/{uuid}/accept`, `/decline`, `/leave` on the phase-6 `MeHandler` (or a
     handler it delegates to); `GET /v1/me/profiles` already exists from phase 6 and must show
     invited profiles.
   - Keep the HTTP error order (401, 400, 403, 404/409) and masked lookups: an actor without read
     access to a party or profile gets 404, not 403. Internal ids never leave the process; responses
     use `ProfileView` (section 7).
3. **Wiring and docs**: `profileService` and `partiesHandler` in `moduleforge.module.yaml` and
   `api/cmd/server/main.go`; facade re-exports in `api/usersservice` and `api/handlers/handlers.go`;
   `api/openapi.yaml` entries for every route added; `AGENTS.md`/`README.md` rows for the new
   routes.
4. **Unit and service tests** for the service and handlers: transition table (allowed and refused
   transitions), holder-versus-admin authorization, step-up refusal (409 `users.step_up_required`),
   masked 404s, managed-user creation rules, departure cleanup effects, observer invocation and
   rollback on observer error, route-order tests in the phase-6 `AuthContext` identity style.

## Validation

- `make build`, `make test`, `make lint` pass; `cd model && make verify` passes if any query changed.
- The integration suite (throwaway-Postgres recipe in `AGENTS.md`) passes for the new service tests.
- `grep -n "RegisterPartyRoutes\|profileService\|partiesHandler" moduleforge.module.yaml api/cmd/server/main.go api/handlers/handlers.go`
  shows the wiring.
- `api/openapi.yaml` lists every route in requirement 2 and validates with the repository's
  OpenAPI lint target (if present).
- `grep -rn "checkInvitePolicyMismatch\|task 003" api/internal/service` shows the seam for task 003.

## Metadata

architectural_impact: true

## Assumptions

- Phase 6 (`users-identity-core`) is merged into this plan's mod-users branch: the `mod_users`
  schema, `CredentialService.RevokeInTx`, the principal disable path, `MeHandler`, the session
  middleware and liveness are in place.
- mod-core's `profiles` table, status transition rules (MF004) and `profile_policy_confirmations`
  are available through the sibling link to mod-core's plan worktree (`scripts/link-siblings.sh`).
- Invitations do not send an email notification in this plan; the invited profile appears in the
  holder's `GET /v1/me/profiles`. Report it if the code makes a notification look necessary.

## References

- [API surface](../notes/mod-users-api-surface.md) sections 2.1, 6.3, 6.4, 7, 10, 11.
- [Decisions](../notes/decisions-and-resolutions.md) U11, U16, U19, U20, U21, U22, U27.
- [Work packages](../notes/work-packages.md), phase `parties-profiles-and-bootstrap`, package 1.
- mod-core ADR `docs/architecture/identity-model.md`, sections "Party profiles" and "Credential
  gating and profile availability" (departure row).

## Checkpoint hints

- After the service and its unit tests.
- After the parties and profiles handlers and route tests.
- After wiring, OpenAPI and docs rows.
