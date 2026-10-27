# Pin Sessionless Contexts Away From Authorize

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates the phase 8 gate finding j75L, recorded as a finding in this plan's `plan/findings.yaml`. The authorization engine's `gated` treats every sessionless, credential-less, non-anonymous AuthContext as a trusted system actor, and mod-users builds such contexts for real users. This task pins the current behavior with a test and a comment. It does not add the explicit system-actor marker, which is a mod-core change that the manager files separately.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

This task is independent of the four sequenced tasks (001 to 004 of this phase). It may run in parallel with them, but it must not edit the gate SQL they change.

Scope is limited to fixing the linked finding. Anything beyond that is out of scope.

## Requirements

1. Independent of the four sequenced tasks; may run in parallel with them but must not edit the gate SQL they change.
2. Add a unit test that enumerates every mod-users helper producing a non-anonymous AuthContext with SessionID == 0 and CredentialID == 0 (withActingProfile in api/internal/service/helpers.go, UnauthenticatedActingContext, CreatedUser.ActingContext, the idp_jit context in api/internal/handlers/auth/oidc_flows.go, and any other found), asserts each one is sessionless, and asserts (with a recording or failing authorizer stub) that none of the code paths using them reaches Authorize, AuthorizeType or AuthorizeTypeIn.
3. Add a comment at gated in api/internal/authz/authz.go stating that sessionless trust is inferred from absent fields pending an explicit mod-core system-actor marker, and pointing at the pinning test.
4. The explicit system-actor marker itself is a mod-core change the manager files separately; do not implement it or edit mod-core.
5. Never run scripts/link-siblings.sh or make preflight in a worktree; set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users and verify the sibling links afterwards. Do not edit mod-core or any sibling project.
6. The Go developer role (`plugins/flow/roles/developer-go.md`) applies to this task.

## Validation

1. make build, make test and make lint pass.
2. The new test fails if a helper's context is passed to a stubbed Authorize (demonstrated once locally and reported).

## References

- Finding j75L in this plan's `plan/findings.yaml` (security, medium): gated in api/internal/authz/authz.go treats every sessionless, credential-less, non-anonymous AuthContext as a trusted system actor, while mod-users builds such contexts for real users.
- [Decisions and resolutions](../notes/decisions-and-resolutions.md) — the Amendments (2026-10-27, phase 8 gate) section, for the phase 8 gate context in which this finding was recorded.
