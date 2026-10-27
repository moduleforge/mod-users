# Apply The Impersonated Actor's Party Rules During Impersonation

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates the phase 8 gate finding Y3B8, recorded as a finding in this plan's `plan/findings.yaml`. session_meets_party_policy passes the impersonation flag as p_skip_credential_rule, so a third party's credential_rule never applies to an impersonation session.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Sequencing: this is task 002 of four sequenced tasks (001 -> 002 -> 003 -> 004) that touch the same gate SQL and services. They must not run in parallel. It runs after task 001 (party-is-own-owning-party) and before tasks 003 and 004.

Decision G (impersonation applies the impersonated actor's rules; Y3B8) governs this task. Decision G settles that impersonation never skips any party's credential_rule. If Decision G genuinely conflicts with Decision D or with phase 8 task 003's operator-impersonation requirement (operators can impersonate members of restrictive-policy parties), the implementer must stop and report the conflict, not choose a resolution.

Scope is limited to fixing the linked finding. Anything beyond that is out of scope.

## Requirements

1. Order: runs after party-is-own-owning-party and before credentials-refuse-impersonation and impersonation-renew-recheck.
2. Decision G: while impersonating, the impersonated actor's rules apply to every operation; remove the impersonation-driven p_skip_credential_rule in mod_users.session_meets_party_policy (and wherever else impersonation skips a party's credential_rule), so impersonation never weakens any party's credential_rule. Only stopping the impersonation is not gated on the impersonated party's authorizations; nesting stays prevented by the MF101 no-nesting trigger, not by an authorization check.
3. Settle and document (in the baseline SQL comments and the impersonation notes of the API surface) which session factors satisfy those rules for the child session, which holds no factors of its own (for example, the parent session's factors via impersonator_session_id, with min_acr against the child's copied acr).
4. MUST NOT break Decision D (operator profiles exempt from the owning-party gate) or task 003's requirement that operators can impersonate members of restrictive-policy parties (TestImpersonation_OperatorReachesMembersOfRestrictiveParties). If the two decisions genuinely conflict under every factor choice, halt and report the conflict to the manager instead of choosing.
5. Add a test: a party A administrator impersonating an A member is refused on content of party B whose policy is credential_rule 'authorized' (or 'provider') when the administrator's credentials are not authorized for B; operator impersonation of a restrictive-policy member still works.
6. Never run scripts/link-siblings.sh or make preflight in a worktree; set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users and verify the sibling links afterwards. Do not edit mod-core or any sibling project.
7. The Go developer role (`plugins/flow/roles/developer-go.md`) applies to this task.

## Validation

1. make build, make test, make lint and cd model && make verify pass.
2. api/internal/authz and api/internal/integration suites pass against a throwaway Postgres, including the new cross-party impersonation refusal and the existing impersonation and operator-exemption tests.
3. The report states the chosen factor source for child sessions and maps each requirement to a test.

## References

- Finding Y3B8 in this plan's `plan/findings.yaml` (security, high): session_meets_party_policy passes the impersonation flag as p_skip_credential_rule, so a third party's credential_rule never applies to an impersonation session.
- [Decisions and resolutions](../notes/decisions-and-resolutions.md) — Decision G (impersonation applies the impersonated actor's rules; Y3B8), Decision D (operators and the owning-party gate), and the Amendments (2026-10-27, phase 8 gate) section.
