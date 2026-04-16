# Phase 2, Task 7 — sqlc query files per concept

## Context
sqlc generates type-safe Go query methods from `.sql` files. We organize one file per concept so future package extraction is mechanical.

## Acceptance
Files under `model/queries/`:

- `entities.sql` — `CreateEntity`, `GetEntityByUUID`, `ArchiveEntity`, `UnarchiveEntity`.
- `legal_entities.sql` — `CreateLegalEntity`, `GetLegalEntityByEntityID`.
- `natural_persons.sql` — `CreateNaturalPerson`, `GetNaturalPersonByLegalEntityID`, `UpdateNaturalPerson`.
- `corporations.sql` — `CreateCorporation`, `GetCorporationByLegalEntityID`, `UpdateCorporation`.
- `service_accounts.sql` — `CreateServiceAccount`, `GetServiceAccountByEntityID`.
- `users.sql` — `CreateUser`, `GetUserByID`, `GetUserByUUID`, `GetUserByEmail`, `GetUserByAuth` (`auth_issuer`, `auth_id`), `UpdateUser`, `SetAdmin`, `SearchUsers` (param: `q TEXT`, paginated), `SetDefaultApp`.
- `auth_local.sql` — `UpsertAuthLocal`, `GetAuthLocal`, `DeleteAuthLocal`.
- `email_codes.sql` — `CreateEmailCode`, `GetActiveEmailCode`, `ConsumeEmailCode`.
- `password_resets.sql` — `CreatePasswordReset`, `GetActivePasswordReset`, `ConsumePasswordReset`.
- `apps.sql` — `CreateApp`, `GetAppByUUID`, `GetAppBySlug`, `ListApps`, `UpdateApp`, `ArchiveApp`.
- `apps_users.sql` — `AssignUserToApp`, `RemoveUserFromApp`, `ListAppUsers`, `ListUserApps`, `SetAppUserRoles`.
- `audit_log.sql` — `WriteAudit`, `ListAuditByActor`, `ListAuditByTarget`, `ListRecentAudit`.

Each query uses sqlc annotations (`-- name: Foo :one|:many|:exec`). All inputs/outputs use named parameters.

## How to verify
- `make model.gen` produces Go packages under `model/internal/<concept>/` with no errors.
- `cd model && go vet ./...` passes (after Phase 3's Go module wiring).

## Notes
- `SearchUsers` should support case-insensitive email match (`lower(email) = lower($1)`) and substring search on display fields. Pagination via `LIMIT $N OFFSET $M`.
- `WriteAudit` must accept null for `assumed_user_id` and null for `target_entity_id`.
