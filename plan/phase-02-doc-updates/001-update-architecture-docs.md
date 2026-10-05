# Update Architecture Docs

## Purpose and scope

Update mod-users' architecture and spec docs to reflect the type-level vs entity-level authorization contract introduced in phase 01. Follow the `update-architecture-docs` task-procedure at `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.

## Requirements

- These implementation task documents surfaced the architectural implications:
  - `plan/phase-01-type-target-authorization/002-authorize-type-api.md`. It adds the new exported method `(*Authorizer).AuthorizeType` and the `localAuthz.TypeAuthorizer` interface, which changes the public API of `api/localAuthz`.
  - `plan/phase-01-type-target-authorization/003-migrate-type-level-call-sites.md`. It adds the assert-or-nil-fallback caller rule and migrates `UserAccountService.Create`.
- Files to review and update where needed:
  - `docs/architecture.md`. Add a key decision (next free `D<n>` in "Key decisions") covering:
    - `Authorize`'s target is always an `entities.id`;
    - type-level `create`/`list` go through `AuthorizeType`;
    - type-level authority is wildcard-only, because `grants.target_id` references `entities`;
    - consumers holding a `coreAuthz.Authorizer` assert the capability and otherwise fall back to a nil target, never `&typeID`;
    - why the mod-core interface was left unchanged (source compatibility).
    
    Mention the `localAuthz` facade's new exported interface wherever the API layer section lists exported facades.
  - `docs/architecture/ssh-keys.md`. Confirm nothing there is affected. It uses entity targets only.
  - `docs/mod-users-spec.md` (discovered with the `docs/*-spec.md` glob). In "Security requirements", "Authorization", state that admin-only user-account creation is authorized at the type level (wildcard grant), and is not satisfied by owning or holding grants over any entity.
  - Do **not** edit `docs/mf-standards/` (a git submodule of docs-mf-standards) or any file outside mod-users. The ecosystem call-shape table fix is tracked as a followup against docs-mf-standards/mod-core.
- role_doc: `plugins/flow/roles/architect-backend.md`
- Procedure: `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`

## Validation

- `docs/architecture.md` contains a key decision naming `AuthorizeType`, `TypeAuthorizer`, the wildcard-only type-level semantics, and the nil-target fallback rule.
- `docs/mod-users-spec.md`'s security requirements describe type-level authorization for user-account creation.
- `docs/architecture/ssh-keys.md` was reviewed. Record "no change needed" or the edit made in Status/notes.
- `git diff --stat` (excluding `plan/`) touches only files under `docs/`, and none under `docs/mf-standards/`.
- Markdown follows the project's markdown standards: sentence-case headings and inline links.
