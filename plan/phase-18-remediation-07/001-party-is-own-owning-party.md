# Gate The Party Record By Its Own Policy And Document One-Level Ownership

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates the phase 8 gate findings bYK4, N4y2 and iacw, recorded as findings in this plan's `plan/findings.yaml`. Together they cover the party record being authorized without its own owning-party gate (Decision F), and the one-level limit of nested ownership, which must be documented.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Sequencing: this is task 001 of four sequenced tasks (001 -> 002 -> 003 -> 004) that touch the same gate SQL and services. They must not run in parallel.

Scope is limited to fixing the linked findings. Anything beyond that is out of scope.

## Requirements

1. Order: this is the first task of the round; it lands before the impersonated-actor-rules, credentials-impersonation and renew tasks, which touch the same gate SQL and services.
2. Decision F: when the Authorize target (or the AuthorizeTypeIn owner) is itself a legal_entity, treat it as its own owning party in checkPartyPolicy and/or mod_users.session_meets_party_policy, so Authorize(update, &party) on a restrictive party is gated by that party's credential_rule and min_acr; otherwise keep resolving the owner as entities.owner_id.
3. Do not break Decision D: platform operator profiles stay exempt from the owning-party gate (the core_is_operator_profile skip); in-party actors and owners without a restrictive policy keep passing.
4. Nested ownership stays one-level only (no ancestor walk). State that limit in the api/internal/authz/authz.go package documentation and in API surface section 6.2 (plan/notes/mod-users-api-surface.md), as an amendment line.
5. Add a party_gate integration case: a non-member with an entity-scope update grant on a party whose credential_rule/min_acr its session does not meet is refused on Authorize(update, &party) and on PUT /v1/parties/{uuid}/authn-policy; an operator and a compliant member are admitted.
6. Edit model/migrations/sql/0100_baseline.sql in place (no new migration); regenerate sqlc only if queries change.
7. Never run scripts/link-siblings.sh or make preflight in a worktree; set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users and verify the sibling links afterwards. Do not edit mod-core or any sibling project.
8. The Go developer role (`plugins/flow/roles/developer-go.md`) applies to this task.

## Validation

1. make build, make test and make lint pass; cd model && make verify passes.
2. The api/internal/authz and api/internal/integration suites pass against a throwaway Postgres per AGENTS.md, including the new party-record gate case and the existing operator-exemption cases.
3. grep shows the one-level ownership note in api/internal/authz/authz.go and in API surface 6.2.
4. Sibling links still resolve to the paths under MODULEFORGE_SIBLINGS_DIR after the task.

## Status

- Outcome: succeeded (2026-10-27).
- Change: `checkPartyPolicy` (`api/internal/authz/authz.go`) now evaluates `mod_users.session_meets_party_policy` for the Authorize target itself when it is a `legal_entity` (a row in `public.legal_entities`), in addition to its `entities.owner_id`; both must pass. AuthorizeTypeIn is unchanged (its ownerID is already the gate party). The SQL function body is unchanged (no migration change beyond its header comment in `model/migrations/sql/0100_baseline.sql`); no sqlc regeneration.
- Decision: the owner's gate is kept alongside the target-as-party gate rather than replaced. Replacing it would ungate party-owned legal entities (for example managed users, `owner_id` = managing party) from the managing party's policy, and would break the existing party_gate cases, whose content entities are organizations owned by the gated party.
- Decision D preserved: the operator skip lives inside the function and applies to each call.
- One-level note: package documentation of `api/internal/authz/authz.go` and `docs/architecture.md`. The API surface 6.2 amendment line (`plan/notes/mod-users-api-surface.md`) is not edited by this task (plan file outside the task agent's write scope); requested from the manager.
- Tests: `TestInteg_PartyGate_PartyRecordIsGatedByItsOwnPolicy` (`api/internal/authz/party_gate_integration_test.go`) and `TestPartyPolicy_PartyRecordGate_NonMemberUpdateGrantCannotWeakenThePolicy` (`api/internal/integration/party_policy_integration_test.go`); both fail against the pre-change engine.
- Validation: make build, make test, make lint, model verify pass; the authz, config and integration suites pass against a throwaway postgres:16; sibling links resolve to the plan worktrees (restored after make repointed them through the siblings dir).

## References

- Finding bYK4 in this plan's `plan/findings.yaml` (security, high): an outside collaborator with an entity-scope update or manage grant on a restrictive party's record can weaken that party's authn policy from a session that does not meet it.
- Finding N4y2 in this plan's `plan/findings.yaml` (bug, high): checkPartyPolicy resolves the gate party only as the target's owner_id, so Authorize on an organization record without an owner skips the gate.
- Finding iacw in this plan's `plan/findings.yaml` (documentation, low): the one-level limit of nested ownership is not stated in the authz.go package documentation or in API surface 6.2.
- [Decisions and resolutions](../notes/decisions-and-resolutions.md) — Decision F (party is its own owning party; bYK4, N4y2), Decision D (operators and the owning-party gate), and the Amendments (2026-10-27, phase 8 gate) section.
