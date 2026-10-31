# Trusted Profile Admission

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Add `ProfileService.AdmitInTx`, a trusted admission of a user into a party in the caller's
transaction. mod-billing requested it (decision U22): accepting a seat invitation (its S3) and
self-assigning a seat (its S7) must make the holder an active member of the paying party. This
happens in the billing transaction, with the holder's own consent as the actor. `InviteInTx`
cannot serve this. It authorizes the acting profile, which in S3 is the invitee and holds no
authority, and it leaves an `invited` row that needs a second acceptance.

Roles: `plugins/flow/roles/developer-go.md` (primary), `plugins/flow/roles/developer-sql.md` (for
any query added). No standard skill covers this task.

In scope: `api/internal/service/` (the profile service files task 001 created), the
`api/usersservice` facade, `model/queries/` and regenerated `model/db/` if a query is needed, unit
and integration tests, and the `AGENTS.md`/`README.md` rows describing the profile service.

Out of scope: any HTTP route (this is a Go API for mod-billing), and any change to `InviteInTx`,
`EndInTx` or the lifecycle observer contract.

Dependencies:

- Hard dependency on task 001. This task uses `ProfileService`, `Profile`,
  `ProfileStatusChange`, the lifecycle observer dispatch, the `profile` observations and the
  `ErrHolderNotAdmissible` mapping.
- Parallel-eligible with tasks 002 to 005.
- Must merge before task 006 starts, because task 006 owns the phase-closing gate.
- Outside the phase: mod-billing phase 41 task 003 consumes this method (mod-billing overview
  constraint E2). Report the merge promptly.

## Requirements

1. **Signature (binding, [API surface 6.3](../notes/mod-users-api-surface.md)).**

   ```go
   type AdmitInput struct {
       PartyID            int64
       HolderUserID       int64
       InvitedByProfileID int64 // 0 when none
   }
   func (s *ProfileService) AdmitInTx(ctx context.Context, tx pgx.Tx, in AdmitInput) (Profile, error)
   ```

   Re-export it, `AdmitInput` and the new error from `api/usersservice`.
2. **Behavior.** The method returns the holder's active profile in the party. It runs entirely in
   the caller's transaction.
   - An `active` profile is returned as is. No write, no observer call.
   - An `invited` profile becomes `active` through the same internal status-change path that
     accept uses. Lifecycle observers fire, and a `profile` observation is written.
   - A `suspended` profile is refused with `ErrProfileSuspended` (409 `users.profile_suspended`).
     Nothing is written.
   - When there is no open profile (none, or only `ended` ones), create one through the same
     creation path task 001 uses, with:
     - `status = 'active'` and `source = 'manual'`;
     - owner = the party;
     - `invited_by_profile_id` = `InvitedByProfileID`, or NULL when 0;
     - `created_by_profile_id` = the acting profile, set by mod-core's `service.CreateEntity`.

     Lifecycle observers fire (creation as `active`), and a `profile` observation is written.
3. **No authorization.** The method is trusted: it consults no grant. The caller has authorized
   the admission: the holder consented, and the party consented through an earlier authorized
   invitation. State this in the method's doc comment. The context must still carry an
   `AuthContext` with an acting profile, because the observations need one (mod-audit `ErrNoActor`).
   On mod-billing's paths that profile is the accepting holder's or the `billing.paddle_sync`
   system actor's.
4. **Refusals.**
   - A guest holder: `ErrGuestMustUpgrade` (409 `users.guest_upgrade_required`).
   - A managed user of another party, or a holder that is not a user: `ErrHolderNotAdmissible`
     (409 `users.holder_not_admissible`).

   Check these in Go before the insert. Also map the mod-users `MF103` trigger on
   `public.profiles` to the same errors, so a direct violation never surfaces as a 500. No
   policy-mismatch check runs. Admission is not an outside-collaborator invite. A holder whose
   credentials do not meet the party's policy gets an active profile that `profile_availability`
   shows as locked until the holder adds a qualifying credential. Document this in the doc
   comment.
5. **Concurrency.** Two concurrent admissions of the same holder into the same party yield one
   profile and no error. Serialize per holder with `SELECT ... FOR UPDATE` on the
   `mod_users.users` row, then re-read. This is the same pattern phase 8 task 005 uses for
   `EnsurePersonalParty`. Do not rely on catching `23505` from
   `profiles_one_open_per_holder_party`, because that would abort the caller's transaction.
6. **Audit ops.** Only the [API surface section 10.1](../notes/mod-users-api-surface.md) ops are
   used: `create` with resource `profile` for a new profile, and `update` with resource `profile`
   for `invited → active`.
7. **Tests.**
   - Unit and service tests for each branch of requirement 2 and each refusal of requirement 4.
   - Observer invocation and rollback when an observer returns an error.
   - No observation and no observer call for an already-active profile.
   - Two concurrent calls on separate connections yield exactly one profile. This is an
     integration test on the throwaway-Postgres harness.
   - An admitted profile appears in the holder's `GET /v1/me/profiles`.
8. **Docs.** Update the `AGENTS.md`/`README.md` rows that list the profile service's public
   methods. Do not add an `api/openapi.yaml` entry, because no route changes.

## Validation

- `make build`, `make test`, `make lint` pass. `cd model && make verify` passes if a query
  changed.
- The integration suite passes against a throwaway Postgres per `AGENTS.md`, with mod-core and
  mod-authz linked to their plan worktrees.
- `grep -n 'AdmitInTx\|AdmitInput\|ErrProfileSuspended\|ErrHolderNotAdmissible' api/usersservice/*.go`
  shows the facade.
- The report maps each requirement 7 case to its test function.
- Any change to a binding name is recorded in this document and flagged in the report.

## Assumptions

- Task 001 has merged. It provides the profile creation and status-change paths, the lifecycle
  observer dispatch and `ErrHolderNotAdmissible`.
- mod-core's `profiles` status rules (MF004) and `profiles_one_open_per_holder_party` are as in
  mod-core's API surface note.

## References

- [API surface section 6.3](../notes/mod-users-api-surface.md): the binding signature and errors.
- [API surface section 10.1](../notes/mod-users-api-surface.md): audit op vocabulary per event.
- [Decisions](../notes/decisions-and-resolutions.md) U21 and U22.
- mod-billing plan worktree, `plan/notes/mod-billing-api-surface.md` sections 4.4 and 7: the
  request and how mod-billing calls the method.
- mod-billing plan worktree, `plan/notes/decisions-and-resolutions.md` decision B9.

## Checkpoint hints

- After `AdmitInTx` and its unit tests.
- After the concurrency integration test and the facade.

## Status

Succeeded, 2026-10-25. `make build`, `make test`, `make lint` pass; the integration suites (`api/internal/authz`, `config`, `integration`) pass on a throwaway Postgres with mod-core, mod-authz and mod-audit linked through the shared siblings dir. No query changed, so `model verify` was not needed. No binding name changed.

Source: `api/internal/service/profile_admit.go` (AdmitInTx, AdmitInput), `api/internal/service/profile_service.go` (ErrProfileSuspended, shared `scanProfile`), `api/usersservice/service.go` (facade).

Requirement 7 test map:

- Each requirement 2 branch: `TestAdmitInTx_CreatesAnActiveProfile`, `TestAdmitInTx_NullInviterWhenZero`, `TestAdmitInTx_ActiveProfileIsReturnedAsIs`, `TestAdmitInTx_InvitedProfileBecomesActive`, `TestAdmitInTx_SuspendedProfileIsRefused`, `TestAdmitInTx_EndedProfileIsReplacedByANewOne` (integration); `TestAdmitActionFor` (unit).
- Each requirement 4 refusal: `TestAdmitInTx_Refusals` (integration), `TestAdmitInTx_RefusalsBeforeAnyWrite` (unit); the MF103 mapping is the existing `mapProfileInsertError` (`TestMapProfileInsertError`).
- Observer error and rollback: `TestAdmitInTx_ObserverErrorAbortsTheTransaction`, `TestAdmitInTx_InvitedActivationRollsBackWithTheObserver`.
- No observation or observer call for an active profile: `TestAdmitInTx_ActiveProfileIsReturnedAsIs`.
- Concurrency: `TestAdmitInTx_ConcurrentAdmissionsYieldOneProfile`.
- Appears in `GET /v1/me/profiles`: `TestAdmitInTx_CreatesAnActiveProfile`.
