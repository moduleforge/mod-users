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

Depends on tasks 001, 002 and 003. Run it after task 004 and 005 have merged too, since it owns
the phase-closing gate.

## Requirements

1. **ADR worked example**, end to end through the routes (one test, steps as subtests):
   1. John signs up with a personal OIDC login (use the phase-6 fake-IdP test harness): user,
      OIDC credential C1, personal profile PR1, own person record owned by the user.
   2. Acme (organization, `credential_rule = 'authorized'`); Acme admin Alice (Acme profile with a
      wildcard or type grant, per U20) invites John; John accepts from a C1 session; PR2 active.
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
   revoking the authorizing factor revokes the session and the next login lands elsewhere.
6. **Phase gate**: `make build`, `make test`, `make lint`, `cd model && make verify` and every
   integration suite green; fix small defects found in tasks 001 to 005 code and report each fix;
   report larger defects instead of fixing them.

## Validation

- The new test files exist under `api/internal/handlers/` or `api/internal/service/` (implementer's
  choice, matching phase-6 conventions) and pass in the throwaway-Postgres recipe of `AGENTS.md`.
- Every scenario of requirements 1 to 5 maps to a named test or subtest; list them in the report.
- `make build`, `make test`, `make lint`, `cd model && make verify` pass.

## Assumptions

- `scripts/link-siblings.sh` points `mod-core` and `mod-authz` at their plan worktrees.
- If mod-authz's `CollaboratorGate` hook has not landed, group-membership collaborator scenarios
  are covered at the `CollaboratorGate` unit level in task 003 only; say so in the report.

## References

- [Work packages](../notes/work-packages.md), phase `parties-profiles-and-bootstrap`, package 6, and
  the throwaway-Postgres note in its purpose section.
- [API surface](../notes/mod-users-api-surface.md) sections 3, 7.
- [Decisions](../notes/decisions-and-resolutions.md) resolution (a), U20.
- mod-core ADR `docs/architecture/identity-model.md`, "Party profiles" (worked example),
  "Outside collaborators".

## Checkpoint hints

- After the worked-example test.
- After the transition and managed-user tests.
- After the gating, confinement and compliance tests.
