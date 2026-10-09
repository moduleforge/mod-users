# Update Architecture Docs

## Purpose and scope

Update mod-users' architecture and spec docs to reflect phase 6 of this plan: `AuthorizeType` now honours grants on the exact type's entity, directly or through target groups, in addition to wildcard grants. It still never consults ownership or parent types.

Follow the `update-architecture-docs` task-procedure at `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.

This phase runs before the optional Q1 phase on purpose. Describe phase 6 only. Do not describe instance semantics; phase 8 owns that warning if the manager keeps it.

## Requirements

role_doc: plugins/flow/roles/architect-backend.md

Implementation task docs that surfaced the architectural implications:

- `plan/phase-06-authorize-type-grant-arm/001-authorize-type-arm.md`
- `plan/phase-06-authorize-type-grant-arm/002-type-grant-integration-matrix.md`

Files to review, and update where needed:

- `docs/architecture.md`:
  - **D12** ("Type-level authorization is distinct from entity-level"). It says type-level authority is wildcard-only "because `grants.target_id` references `entities` and no grant can target a type". Restate it: every type has an entity (`types.entity_id`, from mod-core). `AuthorizeType` allows on a wildcard grant or a grant on the exact type's entity, directly or through target groups, by the actor or its actor groups. No ownership, no subtype or parent inheritance, unknown type ids denied, deprecation not consulted (mod-core rejects the write). Keep the `ilu6` rationale: a grant on an unrelated entity whose id equals the type id confers nothing. Keep the fallback rule, and add that the nil-target fallback denies type-grant holders (fail closed). Update the parenthetical about `UserAccountService.List`: it stays on a nil target, so it remains wildcard-only even though type-scoped grants now exist.
  - **The API-layer paragraph** about `localAuthz.TypeAuthorizer` (around line 88), if it implies wildcard-only.
  - Mention that target groups used for type grants can hold only type entities (mod-authz's kind rule), with a link to mod-authz's docs rather than restating them.
- `docs/mod-users-spec.md`, Security requirements, "Authorization" bullet (around line 350): "requires a wildcard grant covering `create` ... and is not satisfied by owning, or holding grants over, any entity". Restate: it requires a wildcard grant covering `create`, or a grant covering `create` on the `natural_person` type's entity (directly or through a type-only target group). It is not satisfied by owning, or holding grants over, any other entity.
- `AGENTS.md`: check for a statement of the old rule. Add a short note on building against sibling plan branches only if the manager asks; the plan's design note covers it for this plan.
- `README.md`: check only; no change expected.
- `docs/*-spec.md` glob: `docs/mod-users-spec.md` is the only spec file.

Wording must match mod-core's reworded `TypeAuthorizer` contract (`api/authz/authz.go` and `docs/architecture/type-level-authorization.md` on mod-core's plan branch or `main`, from mod-core phase 2). Do not name plan, phase or task identifiers in the docs.

Also confirm that docs-mf-standards followup `qjI3` (the upstream `authorization-design.md` rewrite) still describes mod-users' arm accurately. If it does not, add a finding with `followups_add`, `target_project` `docs-mf-standards`, describing the gap. Do not edit the submodule.

## Validation

- `grep -n "wildcard-only" docs/architecture.md docs/mod-users-spec.md`: every remaining hit is about nil-target `Authorize` or `UserAccountService.List`, not about `AuthorizeType`.
- `grep -n "no grant can target a type" docs/architecture.md` returns nothing.
- `grep -n "entity_id" docs/architecture.md` shows the exact-type lookup named in D12.
- Manual read: D12 and the spec bullet state "no ownership", "no subtype inheritance" and "fail closed", and do not mention instance access.
- `git diff --stat -- . ':(exclude)plan'` touches only files under `docs/`, plus `AGENTS.md` if it needed a change.
- Markdown style: sentence-case headings, inline links, no "see"-style pointers added.

## References

- [Users type grant design note](../notes/users-type-grant-design.md): the contract the docs describe.
- mod-core's phase 2 contract-wording task: `/Users/zane/playground/moduleforge/mod-core/worktrees/plan/type-scoped-grants/plan/phase-02-type-entity-api-and-contract/002-type-authorizer-contract-wording.md`.
- mod-authz's [authz type entity design note](../../../../../../mod-authz/worktrees/plan/type-scoped-grants/plan/notes/authz-type-entity-design.md#q3-target-group-kind-separation): the kind rule.

## Status

- Outcome: succeeded (2026-10-09).
- Validation: all six checks passed. Remaining `wildcard-only` hits in `docs/architecture.md` concern the nil-target fallback and `UserAccountService.List`; `no grant can target a type` is gone; D12 names `types.entity_id`.
- Files: `docs/architecture.md` (API-layer paragraph and D12), `docs/mod-users-spec.md` (Authorization bullet). `AGENTS.md` and `README.md` hold no statement of the old rule and were left unchanged.
- docs-mf-standards `qjI3` describes mod-users' arm accurately (wildcard or exact type entity, direct or through target groups, no ownership, no subtype inheritance, nil fallback fails closed); no finding filed.
