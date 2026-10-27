# Re-Check Impersonate Authority From The Parent Session On Renew

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates the phase 8 gate findings 0chQ and LAgn, recorded as findings in this plan's `plan/findings.yaml`. ImpersonationService.Renew extends the child session without re-authorizing impersonate, and the child row does not record the administrator's acting profile, so a revoked grant stays effective up to AUTH_IMPERSONATION_MAX. LAgn is the same gap seen from the renewal window, including a suspended administrator profile.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Sequencing: this is task 004 of four sequenced tasks (001 -> 002 -> 003 -> 004) that touch the same gate SQL and services. They must not run in parallel. It runs after task 003 (credentials-refuse-impersonation).

Decision I (impersonation Renew; 0chQ, LAgn) governs the remedy: Renew re-checks `impersonate` from the parent session, and the acting profile is recorded on the child session in the baseline migration. Decision I is recorded in the Amendments (2026-10-27, phase 8 gate) section of the decisions notes.

Scope is limited to fixing the linked findings. Anything beyond that is out of scope.

## Requirements

1. Order: runs after credentials-refuse-impersonation (last of the four sequenced tasks touching impersonation and the gate SQL).
2. Decision I: add a column on mod_users.sessions recording the administrator's acting profile for impersonation children (edit 0100_baseline.sql in place, extend the sessions_impersonation_complete check and the immutability trigger as fits, regenerate sqlc), and set it in ImpersonationService.Start.
3. Renew loads the parent session and re-authorizes impersonate on the impersonated profile in a context built from the parent (parent principal, recorded acting profile, parent session id); on refusal, Renew revokes the child session and returns the refusal (403) instead of extending it.
4. The freshness (max_auth_age) part of the assurance gate was enforced at Start; state in the report whether Renew re-applies it, and if re-checking without it would need a change to the Authorize signatures fixed by the plan, halt and report rather than change them.
5. Tests: revoking the administrator's impersonate grant (or suspending the administrator's acting profile) after Start makes the next Renew fail and ends the child session; an unchanged grant still renews up to AUTH_IMPERSONATION_MAX.
6. Never run scripts/link-siblings.sh or make preflight in a worktree; set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users and verify the sibling links afterwards. Do not edit mod-core or any sibling project.
7. The Go developer role (`plugins/flow/roles/developer-go.md`) applies to this task.

## Validation

1. make build, make test, make lint and cd model && make verify pass.
2. api/internal/integration impersonation tests (including TTL and renewal cap, parent liveness and observations) pass against a throwaway Postgres with the new revoked-grant renew case.
3. The sessions column appears in API surface 2.9 as an amendment line.

## References

- Finding 0chQ in this plan's `plan/findings.yaml` (security, high): ImpersonationService.Renew extends the child session without re-authorizing impersonate, and the child row does not record the administrator's acting profile.
- Finding LAgn in this plan's `plan/findings.yaml` (security, medium): the same renewal gap, including a suspended administrator profile.
- [Decisions and resolutions](../notes/decisions-and-resolutions.md) — Decision I (impersonation Renew; 0chQ, LAgn) and the Amendments (2026-10-27, phase 8 gate) section.
