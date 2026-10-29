# Document Impersonation Re-Auth, Refusals And Decisions G J K L M

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This remediation task fixes the findings IDjP, 7TSm, SO3A, HGTD, 33w3, MGUW and MkEx, recorded in this plan's `plan/findings.yaml`. Together they bring the OpenAPI spec, AGENTS.md, the `credentialsTarget` doc comment and `docs/architecture.md` in line with the final impersonation behavior: the credential-endpoint refusal order, the Renew re-authorization, the personal-party refusal, and Decisions G, J, K, L and M. It runs last, so the docs describe the behavior left by the earlier tasks.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. Read the roles developer.md (plus developer-go.md for the Go doc comment) before starting. Runs last, after the four tasks above, so the docs match the final behavior.
2. IDjP and Decision M: in api/openapi.yaml listPrincipalCredentials, change the stated order to 401, 400, 403 (impersonating), 404 (a principal the caller cannot read), 403 (own principal or credentials refused), 409. Add the impersonation and own-principal 403s to the api-keys, ssh-keys and credential-revoke operations. State that own-principal routes are refused for every caller, service accounts included, and that keys are rotated only by their owner or an operator.
3. 7TSm: extend renewImpersonation in api/openapi.yaml. Renewal re-authorizes impersonate from the administrator's session as the recorded acting profile. A refusal answers 403 and ends the impersonation. An ended parent answers 401 users.session_revoked. max_auth_age is not re-applied.
4. SO3A: add ErrImpersonationNotAllowed (403 users.impersonation_not_allowed) to the AGENTS.md PersonalPartyService refusals.
5. HGTD: the credentialsTarget doc comment (api/internal/service/principal_credentials.go) lists every refusal in actual order. Comment-only change.
6. 33w3, MGUW, MkEx: docs/architecture.md states Decisions G, J, K and L (impersonated actor's rules with the operator credential_rule exemption keyed on the recorded acting profile; managed-user, credential and authorization writes refused under impersonation; confinement for non-operator impersonators). It also covers the impersonation lifecycle: credential and personal-party refusals, child auth_time inheritance, and Renew re-authorization without max_auth_age. It notes that the app gate's login can surface 409 users.step_up_required and that the operator-admin checker fails closed. It records the sessionless-context inference and its pinning test as a known limitation pending a mod-core system-actor marker. Correct the localAuthz sentence that states the principal-wide operator rule.
7. No links from project docs into plan/. Set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users for any make target. Never run scripts/link-siblings.sh or bare 'make preflight'.
8. No work beyond the linked findings.

## Validation

1. make build and make lint pass, and the OpenAPI spec still validates through the project's lint.
2. grep shows the corrected order in listPrincipalCredentials, the refusal sentence on the other three credential operations, and the re-authorization text in renewImpersonation.
3. docs/architecture.md names Decisions G, J, K and L, the Renew re-auth, the step-up app-gate path and the sessionless-inference limitation. No principal-wide operator sentence remains.

## References

- Finding `IDjP` in this plan's `plan/findings.yaml` (`api/openapi.yaml` `listPrincipalCredentials` states the order as "401, 400, 404 ..." although impersonation is refused before the lookup).
- Finding `7TSm` in this plan's `plan/findings.yaml` (the `renewImpersonation` description omits the Renew re-authorization).
- Finding `SO3A` in this plan's `plan/findings.yaml` (the AGENTS.md PersonalPartyService row omits the `ErrImpersonationNotAllowed` refusal).
- Finding `HGTD` in this plan's `plan/findings.yaml` (the `credentialsTarget` doc comment omits the impersonation and own-principal refusals).
- Finding `33w3` in this plan's `plan/findings.yaml` (`docs/architecture.md` omits the impersonation-lifecycle rules from Decisions H and I).
- Finding `MGUW` in this plan's `plan/findings.yaml` (`docs/architecture.md` does not mention the app gate's 409 `users.step_up_required` or the fail-closed operator-admin checker).
- Finding `MkEx` in this plan's `plan/findings.yaml` (`docs/architecture.md` does not record the sessionless-trust inference or the `sessionless_contexts_test.go` pin).
