# Document Lock Order And Correct Phase 15 Contract Drift

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `A202` (documentation, medium), `J6jP` (documentation, medium), `u2bW` (documentation, medium), `jBbn` (documentation, medium), `EvkR` (documentation, low), `xWC3` (documentation, low), `yPFO` (documentation, low), `8mcq` (documentation, medium) and `VXFQ` (bug, low). Together they record the undocumented lock-order contract and correct OpenAPI, AGENTS.md, model README and message-wording drift introduced by phases 13-15.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. Run after tasks 001 (strict-collaborator-policy-mismatch) and 002 (close-admin-disable-race-and-fail-closed-policy-lock) of this round: the lock-order text and the policy_mismatch.go wording depend on their edits.
2. Add a Lock order subsection to docs/architecture.md: platform_settings first for operator-party transitions (ending, suspension, and the principal-disable path from the sibling task), then users row, then profile, then credential (credential writes take credential then profile FOR SHARE as implemented — state the actual order); the per-party advisory lock (lockPartyPolicyExclusive/Shared) before the policy row. Confirm by reading the code that Bootstrap never locks an existing profile after taking platform_settings, and state it.
3. Record in docs/architecture.md (Key decisions or a topic file) the two-door credential-write gate, leave gated on profile_availability while accept/decline stay open to a locked session, credential_rule or required-provider change voiding policy confirmations, the invite-time semantics of the 'authorized' rule (collaborators excluded, per the ggb7 decision) and that update on the party is required to confirm a mismatch.
4. AGENTS.md: point to the lock-order subsection; add party_policy_lock.go to the key-files row; update the collaborator_gate.go row to cover the provider-change purge and the shared/exclusive party policy lock.
5. model/README.md: add profile_gate/profile_gate_for to the wrapper list.
6. api/openapi.yaml: PUT authn-policy — changing credential_rule or the required provider deletes confirmations, a write leaving both unchanged keeps them; PATCH profile status — 409 users.last_operator_admin covers ending or suspending, drop "ending's" from the transaction wording; POST managed-users — under 'authorized' the new password is authorized for the party only when the caller holds update on the party, otherwise it stays unauthorized until an administrator authorizes it.
7. api/internal/service/policy_mismatch.go:72 — reword the credential_not_authorized message so it does not imply the holder can fix it by authorizing a credential when it has no usable one (e.g. "the account has no usable credential"); keep it accurate for the strict collaborator mode added by the ggb7 task (coordinate wording; run after that task if both touch this file).
8. No links from non-plan docs into plan/.

## Validation

1. grep finds the Lock order subsection in docs/architecture.md and a pointer in AGENTS.md; party_policy_lock.go appears in AGENTS.md; profile_gate_for appears in model/README.md.
2. OpenAPI lints/validates (project's existing openapi check, if any) and the three descriptions read as required.
3. make build.api and make test.unit pass (message wording assertions updated if any test checks the text).

## References

- Finding `A202` in this plan's plan/findings.yaml (type:documentation, priority:medium): the lock-order contract is undocumented; party_policy_lock.go is missing from the AGENTS.md key-files row; model/README.md's wrapper list lacks profile_gate/profile_gate_for. Anchor finding of this task.
- Finding `J6jP` in this plan's plan/findings.yaml (type:documentation, priority:medium): architectural decisions from phases 13-15 (lock order, two-door credential-write gate, leave gating, provider-change confirmation voiding, invite-time 'authorized' semantics) are undocumented.
- Finding `u2bW` in this plan's plan/findings.yaml (type:documentation, priority:medium): the platform_settings lock position (lockPlatformSettingsForEndSQL) is part of the undocumented lock-order contract and should be recorded, with confirmation that Bootstrap never locks an existing profile after taking platform_settings.
- Finding `jBbn` in this plan's plan/findings.yaml (type:documentation, priority:medium): whether to document the global lock-order contract; A202, J6jP and u2bW establish that it must be documented.
- Finding `EvkR` in this plan's plan/findings.yaml (type:documentation, priority:low): the AGENTS.md collaborator_gate.go row omits the provider-change purge and the party policy lock.
- Finding `xWC3` in this plan's plan/findings.yaml (type:documentation, priority:low): the OpenAPI PUT authn-policy description omits that a required-provider change also deletes confirmations.
- Finding `yPFO` in this plan's plan/findings.yaml (type:documentation, priority:low): the OpenAPI PATCH profile status 409 users.last_operator_admin text covers ending only, while the guard also refuses suspension.
- Finding `8mcq` in this plan's plan/findings.yaml (type:documentation, priority:medium): the OpenAPI contract does not show that POST managed-users under credential_rule 'authorized' can return 201 with an unauthorized password when the caller lacks update on the party.
- Finding `VXFQ` in this plan's plan/findings.yaml (type:bug, priority:low): the credential_not_authorized message at api/internal/service/policy_mismatch.go:72 implies the holder can fix it by authorizing a credential, unlike the acr_too_low message.

## Status

Succeeded, 2026-10-26. Added the Lock order section and decision D22 to `docs/architecture.md`, updated `AGENTS.md` (pointer, `party_policy_lock.go`, `collaborator_gate.go` row), `model/README.md` (`profile_gate`, `profile_gate_for`), three `api/openapi.yaml` descriptions, and the `credential_not_authorized` wording in `api/internal/service/policy_mismatch.go`. Validation: greps pass; `make build.api` and `make test.unit` pass; OpenAPI YAML parses (`make openapi.validate` could not run, no `yaml` module for python3 and no spectral). Decision: the documented order states the actual code order, in which a profile status transition locks the profile before `platform_settings` (the principal-disable path locks `platform_settings` first and never locks a profile).
