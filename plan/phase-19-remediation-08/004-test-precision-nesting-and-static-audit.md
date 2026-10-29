# Tighten Nesting, Party-Record Gate And Sessionless Audit Tests

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This remediation task fixes the findings gxvu, ehkS and FvL1, recorded in this plan's `plan/findings.yaml`. Together they tighten three test suites: the impersonation nesting check, the party-record gate HTTP test, and the static audit of sessionless contexts that backs the mitigation for finding j75L. No production code changes.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. Read the roles developer-go.md and developer.md before starting.
2. Ordering: runs after impersonator-profile-credential-exemption and confine-impersonator-parent-factors.
3. gxvu: in the nesting SQL check (api/internal/integration/impersonation_integration_test.go ~307-318), add impersonator_profile_id to the INSERT, set to a profile held by the inserted child's parent principal, so the row violates only the no-nesting invariant. Also assert on the no-nesting message, not only on MF101.
4. ehkS: in TestPartyPolicy_PartyRecordGate_NonMemberUpdateGrantCannotWeakenThePolicy, pin the exact status the Decision F gate yields. Add a positive control: once the outsider's credential is authorized for the party and min_acr is met, the PUT succeeds.
5. FvL1: in api/internal/service/sessionless_contexts_test.go, treat a SessionID or CredentialID key whose value is the literal 0 as absent. State in the file comment that variable-valued SessionID is out of scope and that the receiver closure covers the same package only.
6. Integration tests must run, in the make -C model compose style flow: start a throwaway postgres:16 container with user 'users' as superuser, run the tests, then remove the container.
7. Set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users for every make target. Never run scripts/link-siblings.sh or bare 'make preflight'.
8. Test changes only. No production-code change and no work beyond the linked findings.

## Validation

1. make build, make test and make lint pass.
2. The integration suite passes against the throwaway postgres:16. The report shows that removing the no-nesting branch locally makes the nesting check fail (then reverted).
3. The static audit flags a temporary AuthContext{SessionID: 0, ...} literal (demonstrated once, then reverted).

## References

- Finding `gxvu` in this plan's `plan/findings.yaml` (the nesting SQL check omits `impersonator_profile_id`, so the MF101 invariant can raise the same SQLSTATE and the test does not isolate the nesting invariant).
- Finding `ehkS` in this plan's `plan/findings.yaml` (the party-record gate HTTP test accepts either 403 or 404 and has no positive control).
- Finding `FvL1` in this plan's `plan/findings.yaml` (the static audit counts a literal `SessionID: 0` as session-bearing, and its receiver walk covers the same package only, which is not stated).

## Status

- Outcome: succeeded (2026-10-29).
- Validation: api build, unit tests and lint pass; the integration suite (authz, config, integration) passed against a throwaway postgres:16 container (since removed). Removing the no-nesting branch locally made TestImpersonation_NoNesting fail ("inserting a child of an impersonation session succeeded"); reverted. A temporary `AuthContext{SessionID: 0, ...}` literal made TestSessionlessContexts_SourceAudit fail on the changed root set; removed.
- Changed: `api/internal/integration/impersonation_integration_test.go`, `api/internal/integration/party_policy_integration_test.go`, `api/internal/service/sessionless_contexts_test.go`.
- Decision: the party-record gate yields the masked 404 (not 403) for a non-member below the policy.
