# Personal Party

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Implement `PersonalPartyService.EnsurePersonalParty`. This is resolution (b) of the ADR, as refined by decisions U1 and U2; mod-billing calls it for personal team plans. Also re-test phase 7's party-admin flows with owned-scope grants in place of the wildcard grants those tests used.

Suggested role: `plugins/flow/roles/developer-go.md`. No standard skill covers this task.

Scope:

- `api/internal/service/personal_party.go` (new)
- the `usersservice` facade
- the manifest service entry
- tests
- re-targeted fixtures in phase 7's party-admin tests

Dependency: requires task 001, because owned-scope grants only authorize once the engine lands. It can run in parallel with tasks 002, 003 and 004.

## Requirements

1. **Signature (binding).** `func (s *PersonalPartyService) EnsurePersonalParty(ctx context.Context, tx pgx.Tx) (PersonalParty, error)`, where `PersonalParty` is `{ PartyID int64; PartyUUID uuid.UUID; GrantID int64; Created bool }`. It runs entirely in the caller's transaction.
2. **Preconditions.** The acting profile (`opctx.Auth(ctx).ProfileID`) must be a user's personal profile. Otherwise:

   | Condition | Error |
   |---|---|
   | Guest | `ErrGuestMustUpgrade` (409 `users.guest_upgrade_required`) |
   | Managed user, or any non-personal profile | `ErrPersonalProfileRequired` (403) |
   | `users.person_id` links a person the user does not own | `ErrPersonalPartyNotOwned` (409 `users.person_not_owned`) |
3. **Behavior.**
   - When `users.person_id` is NULL, create the user's `natural_person` through mod-core's `service.CreateEntity` with `owner_id` = the user (U1), and link `users.person_id`.
   - Reuse an existing linked person the user owns.
   - Ensure an owned-scope `manage` grant (U2) with these fields, inserting it through `authzdb.CreateGrantParams`:
     - held by the personal profile
     - `scope = 'owned'`
     - `target_id` = the person party
     - `target_type_id` NULL
     - `include_subtypes` false
   - Reuse an equal existing grant. `Created` is true only when the person or the grant was created.
   - Emit observer events with only the ops of [API surface section 10.1](../notes/mod-users-api-surface.md) (decision U21):
     - `grant` on resource `grant` (target = the person party) for the trusted grant insert;
     - `create` for the person record, unless mod-core's entity helper already observes it;
     - `update` on `user` for the `person_id` link.

     A reused person or grant emits nothing.
   - Document in the method's doc comment the visibility rule of decision U28. The grant is invisible to the production `Authorizer`, which reads through its own pool, until the caller's transaction commits. A caller that must authorize against the grant commits this call first, as mod-billing's B6 does.
4. **Idempotency under concurrency.** Two concurrent calls for the same user produce one person and one grant. Use a row lock on `mod_users.users` (`SELECT ... FOR UPDATE`) or an equivalent. Document the choice in a code comment.
5. **Wiring.**
   - Add the `usersservice.PersonalPartyService` facade and constructor in the existing style.
   - Add the `personalPartyService` entry in `moduleforge.module.yaml` per [API surface section 11](../notes/mod-users-api-surface.md#11-module-manifest-moduleforgemoduleyaml-binding).
   - Wire `api/cmd/server/main.go` if services are constructed there.
   - No HTTP route: this is a Go API consumed by mod-billing.
6. **Tests.**
   - First call creates the person and the grant. A second call is a no-op returning the same ids with `Created = false`.
   - Two concurrent calls on separate connections yield exactly one person and one grant.
   - Guest, managed-user, party-profile and person-not-owned refusals.
   - The resulting grant reaches a profile owned by the person party, for example a seat created through `ProfileService.InviteInTx` after `EnsurePersonalParty`.
   - The resulting grant does not reach the person record itself (mod-core D7).
   - **mod-billing's B6 sequence** (decision U28), through the production `Authorizer` built by `localAuthz.New` on a real pool:
     1. call `EnsurePersonalParty` in its own transaction and commit;
     2. in a fresh context, `authz.AuthorizeTypeIn(create, <a type>, &personParty)` succeeds for the personal profile;
     3. a repeated sequence is a no-op.

     A companion test documents the limitation. Inside the still-open transaction, the same authorization is refused, because the grant is not yet visible to the pool.
   - If the manager accepts the committed-variant option in the decisions note's open question (U28; recommended answer: no), add `EnsurePersonalPartyCommitted(ctx) (PersonalParty, error)` with the same errors, and list it as binding in API surface 6.3. Otherwise add nothing.
7. **Owned-scope re-test of phase 7 flows.** Re-run phase 7's party-admin integration tests with the admin holding an owned-scope `manage` grant on the party instead of a wildcard. The party is a non-operator party. Phase 7 used the operator party for these flows, because mod-authz's trigger allows wildcard and type-entity grants only for operator actors (decision U27). This includes the ADR worked example (phase 7 task 006 requirement 1) with a non-operator Acme. This applies to invitation, suspend/resume/end, managed-user creation, authn-policy and credential-authorization administration. You can parameterize the existing tests or add a parallel suite. Also add one negative case per flow: an owned-scope grant on another party is refused.

## Validation

- `make build`, `make test` and `make lint` pass.
- `cd model && make verify` passes.
- The integration suites pass against a throwaway Postgres per `AGENTS.md`.
- `grep -n 'personalPartyService' moduleforge.module.yaml` shows the entry.
- `grep -n 'EnsurePersonalParty' api/usersservice/*.go` shows the facade method.
- The report lists the phase 7 flows re-tested with owned-scope grants and their test functions.

## Assumptions

- Task 001 has landed, and mod-authz's extended `CreateGrantParams` carries `scope`, `target_type_id` and `include_subtypes`.
- Phase 6 created the user's own `natural_person` at sign-up (U11), so most users already have `person_id` set. The NULL path covers users created otherwise.
- Phase 7's party-admin integration tests exist and currently use wildcard grants.

## References

- [API surface section 6.3](../notes/mod-users-api-surface.md#63-usersservice-public-facade-over-internalservice): the `PersonalPartyService` binding signature and errors.
- [Decisions](../notes/decisions-and-resolutions.md):
  - resolution (b)
  - U1: the user owns their self-created person
  - U2: the owned-scope `manage` grant with `target_type_id` NULL, flagged as a deliberate widening
  - U11
  - U21: audit ops
  - U27: phase 7 fixtures used operator-party administrators, which requirement 7 replaces with non-operator owned-scope administrators
  - U28: mod-billing's B6 deviation, and the open question on a committed variant
- mod-billing plan worktree, `plan/notes/decisions-and-resolutions.md` B6 and
  `plan/notes/mod-billing-api-surface.md` section 4.2: how mod-billing calls the method.
- [Work packages](../notes/work-packages.md#phase-users-authorization-engine): package 5.
- mod-core ADR `docs/architecture/identity-model.md`, section "Billing" (personal team plans).

## Checkpoint hints

- After `EnsurePersonalParty` and its unit and integration tests.
- After the facade and manifest wiring.
- After the phase 7 owned-scope re-test suite.

## Status

- **Outcome:** succeeded. Date 2026-10-26.
- **Validation:** `make build`, `make test`, `make lint` and `cd model && make verify` pass. The `internal/authz`, `internal/config` and `internal/integration` suites pass against a throwaway `postgres:16` with migrations composed from the plan worktrees. The `personalPartyService` and `EnsurePersonalParty` greps show the manifest entry and the facade.
- **Changed files:**
  - `api/internal/service/personal_party.go`, `personal_party_test.go`: `PersonalPartyService.EnsurePersonalParty`, `PersonalParty`, `ErrPersonalProfileRequired` (403 `users.personal_profile_required`), `ErrPersonalPartyNotOwned` (409 `users.person_not_owned`), unit tests of every precondition.
  - `api/usersservice/service.go`: facade (`PersonalPartyService`, `PersonalParty`, the two errors, `NewPersonalPartyService`, a compile-time signature assertion).
  - `moduleforge.module.yaml`, `api/cmd/server/main.go`: `personalPartyService` entry and construction.
  - `api/internal/integration/personal_party_integration_test.go` (new): creation and idempotency, reuse of the sign-up person, equal-grant reuse, 6-way concurrency, the refusals, the seat reached through `InviteInTx`, the person record not reached (D7), the B6 sequence through the production `Authorizer`, the companion test for the visibility limitation (U28), rollback.
  - `api/internal/integration/owned_scope_party_integration_test.go` (new) and fixture switches in `party_policy_`, `profiles_`, `party_flows_` and `authorized_invite_integration_test.go`: the owned-scope re-test (below).
  - `AGENTS.md`: key-files row.
- **Decisions:**
  - `EnsurePersonalParty` locks the acting user's `mod_users.users` row (`SELECT ... FOR UPDATE`, the same holder lock `AdmitInTx` takes) for the rest of the caller's transaction.
  - The NULL-person path creates the entity (`CreateEntity`, owner = the user, created by the acting profile), the `legal_entities` row and a `natural_persons` row with NULL names (no name is known there), and observes `create` `natural_person` itself, because mod-core's entity helper does not observe.
  - Managed users, party profiles and non-user principals all refuse with `ErrPersonalProfileRequired`; the guest check comes after the profile check, so a guest's personal profile gives `ErrGuestMustUpgrade`.
  - The committed-variant option of decision U28 (`EnsurePersonalPartyCommitted`) was not added (the recommended answer was no).
  - The service has no HTTP route; `api/cmd/server/main.go` only constructs it (`_ =`) to keep the wiring identical to the manifest's.
  - An owned-scope grant never reaches the target's own record (mod-core D7), so a non-operator party administrator also needs an entity-scope `manage` grant on the party record for the policy routes (`update` on the party). The owned-scope fixture holds both; neither is wildcard or type-entity.
- **Owned-scope re-test (requirement 7):** `ownedScopeAdmins` (owned_scope_party_integration_test.go) switches `newGateParty`, `newParty`, `newAcme`, `joinBeta` and `setPolicy` to a non-operator administrator who is a member of a non-operator party, and `TestOwnedScope_Flows` runs the phase 7 test functions listed in `ownedScopeFlows` in that mode. `TestOwnedScope_AdministratorIsNotAnOperator` proves the fixture is not an operator. Because the administrator is a member, a policy write could lock the administrator out; `keepOwnedAdminUnlocked` confirms the administrator's profile (credential rule) and `stepUpOwnedAdmin` steps up to `min_acr` 2, as a real administrator would.
  - Flows and test functions: ADR worked example (`TestPartyFlows_ADRWorkedExample`, `_ConfinedVariant`), invitation (`TestProfiles_Invite*`, `TestAuthorizedInvite_*`, `TestCollaborator_Invite*`), suspend/resume/end (`TestProfiles_AdminStatusChanges`, `TestProfiles_AcceptDeclineLeave`, `TestPartyFlows_SuspendedProfileTokenIsRefusedUntilResumed`), managed-user creation (`TestProfiles_ManagedUsers`, `TestPartyFlows_ManagedUserCreatedThroughTheRouteIsNotInvitable`, `TestManagedUserProvisioning_SerializesWithPolicySwitch_*`, `TestOwnedScope_ManagedUserAutoAuthorizationNeedsUpdateOnTheParty`), authn-policy and credential-authorization administration (`TestPartyPolicy_*`, `TestConfinement_*`), leave gating (`TestLeaveGate_LockedSessionCannotLeave`, `TestLeaveGate_SuspendedProfile*`).
  - Negative case per flow: `TestOwnedScope_AnotherPartysAdministratorIsRefusedOnEveryFlow` (invitation, managed-user creation, authn-policy, credential authorization, suspend; refused on party B, same call succeeds on party A) and `TestOwnedScope_ListPartyProfilesIsScopedToTheAdministeredParty`.
  - Not re-run, with reasons: `TestProfiles_ListPartyProfiles` (a member administrator is itself a listed profile; the scoped list is covered by the test above), `TestCollaborator_InviteMismatchByRule` (sets an unreachable `min_acr` 3 and then keeps administering, which only an operator, exempt from the gate, can do), and the `TestLeaveGate_*` tests built on `bootstrapOperator` (they test last-operator-administrator rules). `TestAuthorizedInvite_ConfirmationNeedsUpdateOnTheParty` and `TestAuthorizedManagedUser_AutoAuthorizationNeedsUpdateOnTheParty` use operator-party callers, so they are replaced by `TestOwnedScope_ConfirmationNeedsUpdateOnTheParty` and `TestOwnedScope_ManagedUserAutoAuthorizationNeedsUpdateOnTheParty` with non-operator member callers.
