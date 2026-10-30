# Align Docs With The Six-Argument Predicate And The System-Actor Marker

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task brings mod-users' project documentation in line with the behavior that tasks 001 to 003 of this phase merged:

- the engine's gates are skipped only for an explicit system-actor context (`opctx.WithSystemActor` / `opctx.IsSystemActor`) and for the anonymous context; a sessionless, credential-less context without the marker is gated and fails closed
- list endpoints apply the owning-party gate through mod-core's row-filter hook and mod-users' six-argument predicate `mod_users.access_row_permitted(p_actor_entity_id, p_entity_id, p_owner_id, p_session_id, p_credential_id, p_system_actor)`, with `setup.BindAuthInputs` in every list transaction and fail-closed behavior when unbound
- host composition roots must install access functions through mod-users' exported generator
- `lockProfile` uses mod-core's generated row lock, and the bootstrap creates the operator organization through `OrganizationService.CreateInTx`
- the in-place baseline edits (mod-users' `0100_baseline.sql`, and mod-core's `0001_baseline.sql`: hardened `core_is_operator_profile`, `profile_policy_confirmations_party_id_idx`) require existing dev databases to be rebuilt

Role: read `tech-writer.md` before starting. Pure documentation: no Go, SQL or test changes. It closes the documentation part of findings `FZSa`, `8hQB` and `T9ZL` (and of `qvSR`, whose mod-core contract text is already done: mod-users' docs must not contradict it).

Scope: `AGENTS.md`, `README.md`, `docs/` (at least `docs/architecture.md`, `docs/mod-users-spec.md`, `docs/consumer-upgrade-notes.md` if present), `model/README.md`, and `api/openapi.yaml` descriptions of list endpoints. Plan notes under `plan/notes/` are **not** edited by this task (the manager edits them); list the needed edits in the report instead.

Runs last in the phase, after tasks 001 to 003 have merged.

## Requirements

1. **Read the merged code first.** Base every statement on the merged code and SQL (`api/internal/authz/authz.go` `gated`, `model/migrations/sql/0100_baseline.sql` `access_row_permitted` and its shared gate function, the exported facade from task 002, `api/internal/service/principal_admin.go`, `api/cmd/server/main.go`, `profile_service.go`, `bootstrap_service.go`) and the three task reports' Status sections; do not restate this task document's expectations where the code differs. Report any difference.
2. **Remove superseded statements.** Find and rewrite every statement that the system actor is inferred from absent session and credential ids, that sessionless contexts are trusted or skip the gates, that list endpoints do not apply the owning-party gate (or apply a five-argument or other predicate shape), or that the bootstrap composes raw organization inserts. Starting points: `docs/architecture.md` ("Sessionless contexts (known limitation)" and the localAuthz paragraph), `docs/mod-users-spec.md`, `AGENTS.md` (the "Database migrations" paragraph on the `accessible_user_ids_for_actor` stub and `setup.ApplyFuncs`, and the key files table). Use `grep -rniE 'sessionless|system.actor|ApplyFuncs|accessible_|five|row.filter|BindAuthInputs|lockProfile|createOrganization' AGENTS.md README.md docs model/README.md api/openapi.yaml`.
3. **Describe the current behavior.** Document, in the architecture doc's authorization section and briefly in `AGENTS.md`:
   - which contexts skip the prelude gates (marker, anonymous) and that unmarked sessionless contexts are gated; that mod-users' own unauthenticated flows build unmarked contexts and never call `Authorize`; who may use `opctx.WithSystemActor` (trusted system flows only, never a user's profile);
   - the list-side gate: the predicate name, its six arguments in order, what it decides (system actor, owning-party gate with Decision D operators, F party-as-own-owner, G/J impersonation exemption, L confinement, all inside the shared gate function), NULL/unbound fail-closed semantics and the anonymous handling chosen in task 002;
   - the host obligation: install access functions through mod-users' exported generator, and call `setup.BindAuthInputs` (checking its error) in the transaction of every list query that joins an access function, one transaction per request context; skipping either breaks list/single-row parity (silently fewer rows for unbound queries);
   - `docs/consumer-upgrade-notes.md` (or the doc that carries consumer upgrade guidance): an entry for host apps and sibling modules covering the generator, the binding and the `NewBootstrapService` signature change from task 003.
4. **Dev database rebuild.** State in `AGENTS.md` ("Database migrations") and `model/README.md` that mod-users' baseline and mod-core's baseline were edited in place again and that an existing dev database must be dropped and recreated (`make clean.data`), naming what changed in plain terms (new predicate function; hardened operator test; new index). Write current behavior only: no plan, phase, task or finding names in project docs, and no links into `plan/`.
5. **OpenAPI.** Update the description of `GET /v1/principals` (and any other list operation backed by an access function) to say that rows owned by a party whose authentication policy the caller's session does not meet are omitted, consistent with the single-row 403.
6. **Plan-note edits for the manager (report only).** Do not edit `plan/notes/`. In the report, list each needed edit with file, section and proposed wording, at least:
   - `plan/notes/decisions-and-resolutions.md`: U5 and U14 (the inferred system-actor rule is superseded by the explicit marker; record as an amendment), Decision E (Ddx9 and qvSR now resolved on both sides), the coordination note on `link-siblings.sh` if stale;
   - `plan/notes/mod-users-api-surface.md`: section 3 (add `mod_users.access_row_permitted` and the shared gate function), section 6.2 (the marker-based gate rule and the exported generator), the `/v1/principals` row (list filtering), the `NewBootstrapService` signature;
   - `plan/overview.md` or `plan/notes/work-packages.md` if they state the old behavior.
7. **Comments.** No code branch is added by this task, so the inline-comment requirement does not apply; do not edit code comments here (tasks 001 to 003 own them). If you find a stale code comment, report it rather than editing it.
8. **Working rules.** Never run `scripts/link-siblings.sh` or a bare root `make preflight` in a plan or task worktree. Any `make` invocation (for example `make lint` to check markdown) sets `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users`; afterwards verify with `ls -l /Users/zane/playground/moduleforge/mod-users/worktrees/plan` that `mod-core`, `mod-authz` and `mod-audit` resolve to the siblings' `worktrees/plan/identity-model-impl`. No database is needed for this task. Never edit any sibling.
9. **Findings.** File out-of-scope issues with `followups_add` (`plan_slug: identity-model-impl`, `phase_slug: adopt-core-engine-remediation`, a `type:` tag). Never edit any `followups.yaml`, never call `followups_patch`, never remove a followup.
10. **Report.** Return a structured report: files changed and what each now says, the plan-note edit list (requirement 6), any code/doc mismatch found.

## Validation

1. `grep -rniE 'inferred from absent|sessionless.*(trusted|skip)|known limitation' AGENTS.md README.md docs model/README.md` returns no statement of the old rule.
2. `grep -rn 'access_row_permitted' AGENTS.md docs` shows the predicate documented with six arguments in the merged order; `grep -rn 'BindAuthInputs' AGENTS.md docs` shows the host obligation.
3. `grep -rn 'clean.data' AGENTS.md model/README.md` shows the rebuild note.
4. `grep -rn '](.*plan/' AGENTS.md README.md docs model/README.md` returns no link into `plan/`.
5. `git diff --stat` touches only documentation files and `api/openapi.yaml`; if `make lint` covers markdown or OpenAPI, it passes with `MODULEFORGE_SIBLINGS_DIR` set.
6. The report contains the plan-note edit list.

## References

- Tasks [001](./001-key-gates-on-system-actor-marker.md), [002](./002-adopt-list-row-filter-hook.md) and [003](./003-adopt-profile-lock-and-org-create-in-tx.md) of this phase, including their Status sections.
- mod-core plan worktree (read only): `AGENTS.md`, `docs/architecture/type-level-authorization.md`, `docs/architecture/identity-model.md` (the marker and the hook as mod-core documents them).
- Findings `FZSa`, `8hQB`, `T9ZL`, `znLa` (mod-core project followups) and `Ddx9`, `qvSR`, `j75L` (mod-users project followups).
- [Decisions](../notes/decisions-and-resolutions.md) and [the API surface](../notes/mod-users-api-surface.md) (read only here).
- [Phase summary](../phases/adopt-core-engine-remediation.md).

## Status

- Outcome: succeeded (2026-10-30). Documentation only.
- Validation: greps 1 to 4 pass (no old rule, six-argument predicate and `BindAuthInputs` documented, `clean.data` note present, no link into `plan/`); only `AGENTS.md`, `api/openapi.yaml`, `docs/` and `model/README.md` changed. `make lint` covers Go and GUI only, and its root `preflight` is forbidden here, so it was not run (not applicable). No integration tests ran; no database was needed.
- Changed: `AGENTS.md` (new Authorization section, rebuild note, key-files bootstrap row), `docs/architecture.md` (marker and list-side gate paragraphs replace the known-limitation paragraph; prelude wording; lock-order and bootstrap notes), `docs/mod-users-spec.md` (Security bullet), `docs/consumer-upgrade-notes.md` (new section), `model/README.md` (rebuild note), `api/openapi.yaml` (`GET /v1/principals` description).
- Code/doc mismatch: the `api/localAuthz` package comment (prelude item 1) is stale; filed as a finding, not edited.
