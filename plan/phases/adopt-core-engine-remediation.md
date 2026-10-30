# Adopt Mod-Core Identity Engine Remediation

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Goals

Adopt in mod-users the mod-core surfaces that mod-core's remediation phase 11
(`identity-engine-proof-remediation`, merged on mod-core's plan branch) added at mod-users'
request, and close the mod-users side of the findings that phase resolved on the mod-core side:

- **Explicit system-actor marker** (`opctx.WithSystemActor`, `opctx.IsSystemActor`). The engine's
  `gated()` stops inferring a trusted system actor from absent session and credential ids. Every
  sessionless context mod-users builds is reviewed: a flow acting for a real user fails closed, a
  genuine system flow uses `opctx.WithSystemActor` (findings `FZSa`, `j75L`).
- **List-side owning-party gate** through mod-core's module-agnostic row-filter hook
  (`setup.NewRowFilterGenerator`, `setup.BindAuthInputs`, six-argument predicate,
  `setup/paritytest`), so list endpoints refuse exactly the rows single-row `Authorize` refuses
  (findings `8hQB`, `Ddx9`; the 8FYk-style all-NULL conformance check).
- **Generated row lock and trusted organization creation** (`dblock.GetProfileByEntityIDForUpdate`,
  `OrganizationService.CreateInTx` with `service.WithoutOwner()`), removing mod-users' hand-written
  SQL against mod-core tables (findings `T9ZL`, `FAK8`, `0b75`; `h0m8` needs no mod-users change).
- **Docs** that describe the six-argument predicate, the marker, and the dev-database rebuild the
  in-place baseline edits require.

It is an ordinary phase added after the phase 8 remediation rounds and **runs before** the pending
phases 9, 10 and 11: the GUI phase builds on the list endpoints this phase changes, and the
documentation and final phases must describe the adopted behavior.

## Inputs

- mod-core plan worktree `/Users/zane/playground/moduleforge/mod-core/worktrees/plan/identity-model-impl`
  (read only): `AGENTS.md`, `plan/phase-11-identity-engine-proof-remediation/` task documents 005,
  006 and 007 (their Status sections describe the shipped design), and the code under
  `api/authz/setup/` (`row_filter.go`, `paritytest/`), `api/opctx/opctx.go`,
  `api/service/organization.go` and `model/dblock/`.
- Findings in mod-core's project followups (`/Users/zane/playground/moduleforge/mod-core/plan/followups.yaml`,
  read through `followups_list` with `project_root` mod-core): `8hQB`, `FZSa`, `T9ZL`, and the
  informational hardening items `aQow`, `erS4`, `6nAj`, `8FYk`, `OUgZ`, `ujE9`, `7Bb9`, `U6SE`.
- Findings in mod-users' project followups (`followups_list` with `project_root` mod-users):
  `Ddx9`, `j75L`, `qvSR`, `FAK8`, `0b75`, `h0m8`, `WZTj`.
- [Decisions](../notes/decisions-and-resolutions.md) U5 and U14 (to be superseded by the marker),
  and the amendments D, F, G, J, K and L.
- [The API surface](../notes/mod-users-api-surface.md) sections 3 (SQL functions) and 6.2
  (`localAuthz`).

## Outputs

- `gated()` keyed on `opctx.IsSystemActor`; every sessionless context builder reviewed, with tests
  pinning which helpers yield a system actor and that a real user's sessionless context fails
  closed in the engine.
- `mod_users.access_row_permitted` (six arguments) in `0100_baseline.sql`, sharing one SQL
  definition of the owning-party gate with the single-row prelude; the dev server and test
  harnesses install access functions through `setup.NewRowFilterGenerator`; every mod-users list
  transaction calls `setup.BindAuthInputs`; parity tests through `setup/paritytest` including the
  all-NULL fail-closed check; integration tests showing a below-policy cross-party collaborator sees
  no gated rows in lists.
- `lockProfile` on `dblock`; the bootstrap organization created through
  `OrganizationService.CreateInTx(ctx, tx, in, service.WithoutOwner())`.
- `AGENTS.md`, `README.md`, `docs/` and `api/openapi.yaml` current; a list of plan-note edits for
  the manager.

## Working rules for every task

- Never run `scripts/link-siblings.sh` or a bare root `make preflight` in a plan or task worktree.
  Every build and `make` invocation sets
  `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users`.
- Integration suites run against a throwaway `postgres:16` container (superuser `users`) that the
  agent starts and removes, after `make -C model compose`.
- Never edit a sibling project. File findings with `followups_add` (`plan_slug`
  `identity-model-impl`, `phase_slug` `adopt-core-engine-remediation`).
- Every new branch of logic carries an inline comment at the branch explaining why it exists and
  what it implies (user requirement, 2026-10-30).
