# Call Site Audit

## Purpose and scope

Lists every `Authorize` call site in mod-users, and the type-id call sites seen elsewhere in the ecosystem during planning (2026-10-05, read-only grep of the sibling checkouts under `/Users/zane/playground/moduleforge`). Phase-01 task 003 re-runs and finalizes the mod-users part. The ecosystem part is informational: this plan does not edit it.

## mod-users call sites (non-test)

Grep: `grep -rn --include='*.go' -E '\.Authorize\(' api model | grep -v _test.go`

| Site | Operation | Target | Kind | Verdict |
|---|---|---|---|---|
| `api/internal/service/user_accounts.go:192-193` (`UserAccountService.Create`) | `create` | `&typeID`, `typeRes.IDForSlugMust("natural_person")` | **type id** | **Vulnerable, migrate to the type-level API.** An actor owning (or holding any grant over) the entity whose id equals the `natural_person` type id can create user accounts, including setting a password. |
| `api/internal/service/user_accounts.go:375` | `list` | `nil` | none | OK (wildcard-only) |
| `api/internal/service/user_accounts.go:418,446,536` | `read`/`update`/`delete` | `&eid` | entity | OK |
| `api/internal/service/user_accounts.go:586` | `login` | `&entityID` | entity | OK |
| `api/internal/service/user_accounts.go:634` | `assume` | `&targetEntityID` | entity | OK |
| `api/internal/service/ssh_keys.go:111` | op | `&accountHolder` | entity | OK |
| `api/internal/handlers/apps.go:81,144,174,220` | `update`/`read` | `nil` | none | OK (wildcard-only) |
| `api/cmd/server/main.go:202,445,464` | `manage` | `nil` | none | OK |

Exported surface that carries an authorizer: `api/localAuthz` (the `Authorizer` alias and `New`), `api/usersservice/service.go` (constructors that take a `coreAuthz.Authorizer`), and `api/handlers/handlers.go` (facade constructors that take a `coreAuthz.Authorizer`). None of them performs an `Authorize` call itself. Task 003 must re-verify this, because the line numbers above may drift.

## Ecosystem type-id call sites (outside mod-users, not edited here)

Found with `grep -rn --include='*.go' -E 'Authorize\([^,]+, "(list|create)", &'` and then manual classification. Calls that pass an entity id (an own-entity create, or a dependent-data list) are excluded.

| Project | Site | Operation |
|---|---|---|
| mod-authz | `api/service/actor_group_service.go:95` | `create` (`authz_actor_group` type id); this is the origin bug |
| mod-authz | `api/service/actor_group_service.go:147` | `list` (`authz_actor_group` type id) |
| mod-authz | `api/service/target_group_service.go:86` | `create` (`authz_target_group` type id) |
| mod-authz | `api/service/target_group_service.go:136` | `list` (`authz_target_group` type id) |
| mod-core | `api/service/natural_person.go:184` | `create` (`natural_person`) |
| mod-core | `api/service/corporation.go:79` | `create` |
| mod-core | `api/service/service_account.go:72` | `create` |
| mod-core | `api/httpapi/apps.go:162` | `create` |
| mod-workflows | `api/service/definition.go:232,437` | `create`, `list` |
| mod-workflows | `api/service/instance.go:347,651` | `create`, `list` |

The mod-authz rows belong to the federated mod-authz slice of this plan. The mod-core and mod-workflows rows are filed as followups against those projects. A wider sweep for other variable names (anything derived from `IDForSlug`/`IDForSlugMust` and passed to `Authorize`) should be part of each project's own fix.
