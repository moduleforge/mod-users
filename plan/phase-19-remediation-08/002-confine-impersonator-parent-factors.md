# Apply Credential Confinement To Non-Operator Impersonators

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This remediation task fixes the findings KkGF and bFGy, recorded in this plan's `plan/findings.yaml`. Together they cover the skipped confinement check for impersonation sessions, which lets a parent credential confined to party X drive an impersonation into party Y.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. Read the roles developer-go.md and developer.md before starting.
2. Ordering: runs after impersonator-profile-credential-exemption, because both change the same baseline SQL.
3. Decision L: in mod_users.profile_gate_for (and in session_meets_party_policy wherever an impersonation's parent factors gate a party), apply the 'confined' check to the parent session's factors when the impersonator is not an operator. Decide operator status from the recorded impersonator_profile_id, using the same test as Decision J. Operator impersonators stay exempt (Decision G).
4. Replace the comment at baseline ~1316-1321 that says confinement is not applied to parent factors with the Decision L rule.
5. Add an integration test: a non-operator administrator whose parent-session credential is confined to party X is refused when impersonating a member of party Y. An operator impersonation of a restrictive-policy member still succeeds.
6. Edit 0100_baseline.sql in place. Regenerate sqlc only if the queries change.
7. Integration tests must run, in the make -C model compose style flow: start a throwaway postgres:16 container with user 'users' as superuser, run the tests, then remove the container.
8. Set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users for every make target. Never run scripts/link-siblings.sh or bare 'make preflight'. Do not edit sibling projects.
9. No work beyond the linked findings.

## Validation

1. make build, make test and make lint pass, and make -C model verify passes.
2. The authz and integration suites pass against the throwaway postgres:16, including the new confined-impersonation refusal and the operator-impersonation tests.

## References

- Finding `KkGF` in this plan's `plan/findings.yaml` (a parent credential confined to party X can drive an impersonation into party Y because the confined check is skipped for impersonation sessions).
- Finding `bFGy` in this plan's `plan/findings.yaml` (`profile_gate_for` skips the confined check for impersonation sessions).

## Status

- **Outcome:** succeeded, 2026-10-29.
- **Change (Decision L):** `mod_users.profile_gate_for` (`model/migrations/sql/0100_baseline.sql`, edited in place) now applies the `confined` check to an impersonation's parent-session factors unless the impersonator is an operator; operator status is `p_skip_credential_rule`, which under impersonation is exactly Decision J's test from `session_policy_factors` (recorded `impersonator_profile_id` active and `core_is_operator_profile`). The old comment saying confinement is not applied to parent factors is replaced with the Decision L rule. `mod_users.session_meets_party_policy` gets the same check where an impersonation's parent factors gate a party: for a non-operator impersonation, a parent factor confined to a party other than the acting (impersonated) profile's fails the gate. No query changes, so sqlc was not regenerated. `docs/architecture.md` (localAuthz paragraph) and the `api/internal/authz/authz.go` header comment state the rule.
- **Tests:** `TestImpersonation_ConfinedParentFactorsBindNonOperators` (`api/internal/integration/impersonation_integration_test.go`): a party X administrator holding manage over X and Y, with its credentials confined to X, gets a 409 `users.impersonation_target_unavailable` (reason `confined`) for a Y member and leaves no child session behind. An impersonation of the Y member that started before the confinement fails liveness, while a member of X can still be impersonated. An operator whose credentials are confined to the operator party still impersonates a member of a party with an `authorized` rule. The SQL-level tests are `TestInteg_Availability_ImpersonationConfinesNonOperatorParentFactors` and `TestInteg_PartyGate_ImpersonationConfinesNonOperatorParentFactors` (`api/internal/authz/`). A mutation check that reverted the SQL fix made all three new tests fail.
- **Validation:** `make build`, `make test`, `make lint` and `make -C model verify` pass with `MODULEFORGE_SIBLINGS_DIR` set. The authz, config and integration suites ran against a throwaway postgres:16 container (superuser `users`) and passed; the container was removed afterwards.
