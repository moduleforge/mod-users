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
   - Emit observer events per the existing style.
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
7. **Owned-scope re-test of phase 7 flows.** Re-run phase 7's party-admin integration tests with the admin holding an owned-scope `manage` grant on the party instead of a wildcard. This applies to invitation, suspend/resume/end, managed-user creation, authn-policy and credential-authorization administration. You can parameterize the existing tests or add a parallel suite. Also add one negative case per flow: an owned-scope grant on another party is refused.

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
- [Work packages](../notes/work-packages.md#phase-users-authorization-engine): package 5.
- mod-core ADR `docs/architecture/identity-model.md`, section "Billing" (personal team plans).

## Checkpoint hints

- After `EnsurePersonalParty` and its unit and integration tests.
- After the facade and manifest wiring.
- After the phase 7 owned-scope re-test suite.
