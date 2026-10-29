# Key The Impersonation Credential-Rule Exemption On The Recorded Acting Profile

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This remediation task fixes the findings DChD, crQE and bKBt, recorded in this plan's `plan/findings.yaml`. Together they cover the principal-wide operator exemption from `credential_rule` for impersonation sessions, its stale baseline comments, and the test coverage that should pin the recorded acting profile as the basis for the exemption.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. Read the roles developer-go.md and developer.md before starting.
2. Decision J: in mod_users.session_policy_factors (model/migrations/sql/0100_baseline.sql, edited in place, no new migration), read the impersonation child's impersonator_profile_id. Set skip_credential_rule only when that recorded profile is an active profile and public.core_is_operator_profile(impersonator_profile_id) holds. Do not use any test over the parent principal's other profiles.
3. Replace the stale comment at baseline ~1161-1168 ("The parent session does not record the administrator's acting profile ...") with the Decision J rule. Update any other baseline comment that states the principal-wide rule (party_policy_gate_for / profile_gate_for headers).
4. Add an integration test: a principal holding both an operator profile and a customer-party administrator profile impersonates through the customer profile and is refused by a restrictive third party's credential_rule. The same principal impersonating through the operator profile is still exempt, so TestImpersonation_OperatorReachesMembersOfRestrictiveParties keeps passing.
5. Integration tests must run, in the make -C model compose style flow: start a throwaway postgres:16 container with user 'users' as superuser, run the tests, then remove the container.
6. Set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users for every make target. Never run scripts/link-siblings.sh or bare 'make preflight'. Do not edit mod-core or any sibling project.
7. No work beyond the linked findings. Confinement (Decision L) is the next task.

## Validation

1. make build, make test and make lint pass, and make -C model verify passes, all with MODULEFORGE_SIBLINGS_DIR set.
2. The authz and integration suites pass against the throwaway postgres:16, including the new dual-profile refusal test and the existing operator-exemption and impersonation tests.
3. grep finds no principal-wide justification left in the session_policy_factors comment.

## References

- Finding `DChD` in this plan's `plan/findings.yaml` (principal-wide operator exemption; stale baseline comment).
- Finding `crQE` in this plan's `plan/findings.yaml` (`skip_credential_rule` tests `core_is_operator_profile` over every profile the parent principal holds).
- Finding `bKBt` in this plan's `plan/findings.yaml` (same principal-wide operator exemption, also covering the stale comment and the `docs/architecture.md` localAuthz sentence).
