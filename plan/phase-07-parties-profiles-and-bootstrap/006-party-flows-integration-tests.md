# Party Flows Integration Tests

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Prove the phase's party flows end to end through the HTTP API against a throwaway Postgres
(mod-core, mod-authz and mod-users migrations composed), and close the phase with every gate
green. Tasks 001 to 003 carry their own unit and service tests; this task adds the cross-cutting
scenarios, chief among them the ADR's worked example. Bootstrap concurrency and token lifecycle
are tested in task 004 and SSH derivation in task 005; this task only re-runs them as part of the
phase gate.

Roles: `plugins/flow/roles/developer-go.md`.

Depends on tasks 001, 002 and 003. Run it after tasks 004, 005 and 007 have merged too, since it
owns the phase-closing gate. Task 007 (`trusted-profile-admission`) is listed after this task but
executes before it.

## Requirements

1. **ADR worked example**, end to end through the routes (one test, steps as subtests):
   1. John signs up with a personal OIDC login (use the phase-6 fake-IdP test harness): user,
      OIDC credential C1, personal profile PR1, own person record owned by the user.
   2. Acme (organization, `credential_rule = 'authorized'`); Acme admin Alice invites John; John
      accepts from a C1 session; PR2 active. Until the engine phase, the invite's
      `AuthorizeTypeIn` falls back to wildcard and type grants (U20), and with mod-authz phase 13
      merged those go only to operator actors (U27). So in this phase Acme is the bootstrapped
      operator party and Alice its admin with the bootstrap wildcard grant. Beta stays a
      non-operator party. Phase 8 task 005 re-runs the scenario with a non-operator Acme and
      Alice holding an owned-scope grant.
   3. John adds a password C2 bound to `john@acme.example`; Alice authorizes C2 for Acme; John sets
      C2's `default_profile_id` to PR2.
   4. Login with C1 lands on PR1, lists PR2 locked with reason `credential_not_authorized` and a
      remedy; switching to PR2 is refused. Login with C2 lands on PR2.
   5. Beta (`credential_rule = 'any'`) profile PR3 is reachable from the C1 session.
   6. John leaves Acme: the next request with a PR2 token is 401 `users.profile_unavailable`; C2's
      Acme authorization is gone; PR1 and PR3 still work.
   7. Variant: C2 confined to Acme instead of authorized; a C2 session cannot reach PR1 or PR3;
      after departure C2 is revoked (`departure`) and the C2 session is revoked.
2. **Invitation and status transitions through the API**: invite → accept, invite → decline,
   suspend → resume, end, illegal transitions (409), duplicate open profile (409
   `users.profile_exists`), guest and managed-user holders refused, holder versus admin
   authorization, step-up required on admin actions, masked 404 for a non-member actor, and a
   suspended profile's token refused on the next request then accepted again after resume.
3. **Managed users**: created through `POST /v1/parties/{uuid}/managed-users`; owned by the party;
   no personal profile; cannot be invited to another party; login lands on the party profile; ending
   the last profile disables the principal and revokes its sessions.
4. **Gating and confinement**: policy `provider` with an OIDC remedy; `min_acr` 2 lock and unlock
   through step-up adding a second factor class to the same session; a session with a confined
   factor never lists the personal profile as available.
5. **Collaborator compliance**: an outside collaborator invited with confirmation reaches the
   profile despite the credential rule; raising the party's `min_acr` locks it on the next request;
   revoking the authorizing factor revokes the session and the next login lands elsewhere. The
   wired gate refuses an outside profile added to a party-owned actor group through mod-authz's
   route without `confirm_policy_mismatch`, and admits it with confirmation (decision U26).
6. **Trusted admission** (task 007): an invited profile admitted through `AdmitInTx` becomes
   active and is reachable through the routes; a suspended one is refused.
7. **Phase gate**: `make build`, `make test`, `make lint`, `cd model && make verify` and every
   integration suite green; fix small defects found in tasks 001 to 005 and 007 code and report each fix;
   report larger defects instead of fixing them.

## Validation

- The new test files exist under `api/internal/handlers/` or `api/internal/service/` (implementer's
  choice, matching phase-6 conventions) and pass in the throwaway-Postgres recipe of `AGENTS.md`.
- Every scenario of requirements 1 to 6 maps to a named test or subtest; list them in the report.
- `make build`, `make test`, `make lint`, `cd model && make verify` pass.

## Assumptions

- `scripts/link-siblings.sh` points `mod-core` and `mod-authz` at their plan worktrees.
- mod-authz phase 14 task 004 (the `CollaboratorGate` hook) merged before task 003 started, and
  task 003 wired the gate (decision U26). There is no unit-level-only fallback.

## References

- [Work packages](../notes/work-packages.md), phase `parties-profiles-and-bootstrap`, package 6, and
  the throwaway-Postgres note in its purpose section.
- [API surface](../notes/mod-users-api-surface.md) sections 3, 7.
- [Decisions](../notes/decisions-and-resolutions.md) resolution (a), U20, U22, U26, U27.
- mod-core ADR `docs/architecture/identity-model.md`, "Party profiles" (worked example),
  "Outside collaborators".

## Checkpoint hints

- After the worked-example test.
- After the transition and managed-user tests.
- After the gating, confinement and compliance tests.

## Status

Outcome: succeeded (2026-10-25). New file `api/internal/integration/party_flows_integration_test.go`.

Scenario map (requirement to test):

- 1.1 to 1.6: `TestPartyFlows_ADRWorkedExample` subtests 1 to 6; 1.7: `TestPartyFlows_ADRWorkedExample_ConfinedVariant`.
- 2: `TestProfiles_InviteRefusals`, `TestProfiles_AcceptDeclineLeave`, `TestProfiles_AdminStatusChanges`, `TestProfiles_UnauthorizedCallersSeeNotFound` (existing); new `TestPartyFlows_SuspendedProfileTokenIsRefusedUntilResumed`, `TestPartyFlows_HolderAndAdministratorAuthority`.
- 3: `TestProfiles_ManagedUsers` (existing); new `TestPartyFlows_ManagedUserCreatedThroughTheRouteIsNotInvitable`.
- 4: new `TestPartyFlows_ProviderPolicyRemedy`, `TestPartyFlows_MinACRLockAndStepUpUnlock`, `TestPartyFlows_ConfinedFactorNeverListsThePersonalProfileAsAvailable`.
- 5: `TestPartyFlows_OutsideCollaboratorThroughTheRoutes`; existing `TestCollaborator_Compliance_*`, `TestCollaborator_AddToPartyActorGroupThroughAuthzRoute`.
- 6: `TestPartyFlows_TrustedAdmissionIsReachableThroughTheRoutes` (plus existing `TestAdmitInTx_*`).

Decision: in the worked example Acme's `authorized` rule is set after John's admission, because inviting a holder with no authorized credential under that rule needs a policy-mismatch confirmation, which exempts the rule and would contradict step 4. Phase gate: `make build`, `make test`, `make lint`, `cd model && make verify` and the authz, config and integration suites all pass. No defects found in tasks 001 to 005 and 007 code.
