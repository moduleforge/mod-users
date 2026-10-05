# Type Target Design

## Purpose and scope

Records the root cause of the type-id-as-entity-target confusion in mod-users' `Authorizer`, and the API design decision this plan implements. The phase-01 task documents treat this note as the design contract.

## Root cause

- The `mod-core` contract (`mod-core/api/authz/authz.go`, `Authorizer.Authorize(ctx, operation string, target *int64) error`) carries a single untyped `*int64` target. The ecosystem standard `docs-mf-standards/architecture/authorization-design.md` ("Call-shape table") overloads it: `create` (type-level) and `list` (entity) pass a **`types.id`**, while `create` (own-entity), `read`, `update`, `delete`, `assume`, `login`, `grant`, and `revoke` pass an **`entities.id`**. The standard admits "there is no runtime tag distinguishing them. Implementations that need the distinction MUST consult this table." mod-users' implementation never did.
- `mod-users/api/internal/authz/authz.go` `Authorize` sends every non-nil target to `checkGrantOrOwn`, which matches it as an `entities.id` twice: `grants.target_id` through `TargetChain`, and `entities.owner_id` through the ownership arm (`e.id = $2 AND e.owner_id = $1`).
- `types.id` (mod-core `0002_types.sql`, its own `BIGSERIAL`) and `entities.id` (mod-core `0008_entities.sql`, a separate `BIGSERIAL`) are independent sequences, and both start at 1. Type ids are small. So `Authorize(ctx, "create", &typeID)` passes for any actor who owns the entity whose id happens to equal `typeID`, or who holds any targeted grant over that entity (including through a target group containing it).
- Natural-person entities self-own (the `entities_owner_default_self` trigger, mod-core `0013_entity_ownership.sql`), so in a young deployment the user whose own entity id equals the type id is already the "owner".
- Reproduced in app-mfmanager (origin finding `ilu6` in app-mfmanager's `managed-app-home-nav` plan store): fresh DB, `types.id` for `authz_actor_group` = 7, an ordinary user gets 403 on `POST /v1/access/actor-groups`, then 201 once they own entity 7.

There is no legitimate type-scoped grant to preserve. `grants.target_id` is `REFERENCES entities(id)` (mod-authz `0505_grants.sql`), so a grant cannot name a type. Every row that matches `target_id = <typeID>` today is a grant over an unrelated entity that happens to share the number. The only grant that can legitimately satisfy a type-level check is a **wildcard grant** (`target_id IS NULL`) whose operation is in the requested operation's `SatisfiedBy` closure, for example `manage` (which implies `create`; mod-authz `0506_authz_create_operation.sql`). That matches the standard's own description of type-level `create` as "used for admin-gated resource types".

## Decision

Add an explicit, separate type-level entry point next to the unchanged entity-level `Authorize`.

1. **New method** on mod-users' `Authorizer` (`api/internal/authz/authz.go`), reachable externally through the existing type alias `localAuthz.Authorizer`:

   ```go
   // AuthorizeType answers "may the effective actor perform operation on
   // resources of type typeID?" typeID is a types.id, never an entities.id.
   func (a *Authorizer) AuthorizeType(ctx context.Context, operation string, typeID int64) error
   ```

   Semantics:
   - The same prelude as `Authorize`: resolve the effective actor via `opctx.EffectiveActorEntityID` (sudo first), returning `ErrUnauthenticated` when absent. Compute `SatisfiedBy(operation)`, with the same unknown-slug fallback to a wildcard-`manage` check, and the same `ErrForbidden` when even `manage` is unknown.
   - `typeID <= 0` returns `ErrForbidden`. Fail closed: never "no target, so allow".
   - A wildcard grant (actor chain, `target_id IS NULL`, op in the closure) means allow.
   - Anything else returns `ErrForbidden`. **It never calls `checkGrantOrOwn`, and never compares `typeID` against `entities.id`, `entities.owner_id`, or `grants.target_id`.** This is the security property the tests pin.
   - A real DB error propagates rather than being swallowed into a denial, matching `Authorize`.
   - Implementation: factor the shared prelude (actor, opIDs, wildcard) out of `Authorize` into an unexported helper used by both methods, so the two cannot drift. `Authorize`'s observable behavior must stay byte-for-byte identical. The existing `authz_test.go` and integration suites are the guard.
   - Forward compatibility: if type-scoped grants are ever introduced (a schema change in mod-authz, filed as a separate followup), they slot into `AuthorizeType` as an extra arm keyed on `types.id`. No caller changes.

2. **Exported capability interface** in `api/localAuthz/authz.go`, plus a compile-time assertion:

   ```go
   // TypeAuthorizer is implemented by Authorizers that can answer type-level
   // questions (create/list of a resource type) distinctly from entity-level ones.
   type TypeAuthorizer interface {
       AuthorizeType(ctx context.Context, operation string, typeID int64) error
   }
   var _ TypeAuthorizer = (*Authorizer)(nil)
   ```

   Do not add sentinel re-exports. The errors are aliases of `apiresp.ErrUnauthenticated` and `apiresp.ErrForbidden`, which consumers already match with `errors.Is`. (The `localAuthz.ErrUnauthenticated` references in `internal/handlers` tests come from an import alias of `internal/authz`, not from this facade.)

3. **Caller pattern: assert, or else fall back to a nil target.** Consumers hold a `coreAuthz.Authorizer` (or a narrower structural interface), and decorators such as mod-repos' wrapping Authorizer hide extra methods. So callers must:

   ```go
   if ta, ok := az.(interface{ AuthorizeType(context.Context, string, int64) error }); ok {
       return ta.AuthorizeType(ctx, op, typeID)
   }
   return az.Authorize(ctx, op, nil) // nil = "no specific target" per the core contract
   ```

   The fallback to `nil` is fail-safe. Under the core contract a nil target means "no specific target exists yet (create / list / search)", and mod-users' `Authorize` grants nil-target calls to wildcard holders only, which is exactly `AuthorizeType`'s current semantics. **A caller must never fall back to `Authorize(ctx, op, &typeID)`.** mod-users implements this as an unexported helper in `api/internal/service`, used by its own call sites. External modules should use a structural interface rather than importing `mod-users/api/localAuthz`. mod-users/api already imports authz-api, so mod-authz importing mod-users/api would add a reverse module edge and a heavy dependency.

4. **`Authorize` stays entity-only, and its signature does not change.** Its doc comment and the package doc change to say that the target is an `entities.id`, that passing a `types.id` is a caller bug, and that type-level checks go through `AuthorizeType`. The current comment in the nil-target branch ("Type-level checks ... are supported; they fall through to checkGrantOrOwn") **is the bug's documentation** and must be removed or rewritten. `Authorize` cannot detect a misused type id at runtime because both are `int64`. The protection comes from migrating callers.

## Alternatives considered and rejected

- **Typed `Target` value** (`Authorize(ctx, op, Target{Kind, ID})`). This breaks the `mod-core` `Authorizer` interface that every module and every test stub implements. It is not source-compatible, and it is a mod-core change outside this plan. Worth revisiting as the long-term contract in mod-core (filed as a followup).
- **Heuristic in `Authorize`** (treat `create` or `list` with a non-nil target as a type id). Rejected: `create` (own-entity) and `list` (dependent data) legitimately pass entity ids (mod-repos, mod-tags, mod-contacts, mod-billing, mod-notifications), so a heuristic would break them or stay ambiguous.
- **Callers just pass `nil`.** This is safe today and is the fallback above, but it erases the type from the call and so blocks any future type-scoped policy. It also leaves the misuse pattern documented in the ecosystem standard. Kept as the fallback, not as the API.
- **Type-scoped grants now** (a `grants.target_type_id` column). This is a mod-authz schema change, needs a product decision about who may create what, and is outside this plan. Filed as a followup.
