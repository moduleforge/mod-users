# Consumer Compat And Pinning

## Purpose and scope

Covers how the mod-users change reaches its consumers (mod-authz, mod-core, mod-workflows, app-mfmanager, app-mftodo, app-mfgit, mod-repos test suites), what stays API-stable, and what the other slices of this four-project federated plan (mod-authz 3-4, mod-core 5-6, mod-workflows 7-8) need from mod-users.

## API stability

- **Additive only.** The new surface is the `(*Authorizer).AuthorizeType` method (visible as `localAuthz.Authorizer` through the existing alias), and the `localAuthz.TypeAuthorizer` interface. `Authorize`'s signature and its entity-target behavior do not change, and `localAuthz.New` does not change.
- **No change to the mod-core `Authorizer` interface.** Every existing stub, decorator, and implementer still compiles.
- **Behavioral change, intentionally breaking for exploiters only.** After task 003, `UserAccountService.Create` no longer admits an actor merely because they own, or hold a targeted grant over, the entity whose id equals the `natural_person` type id. Wildcard `manage`/`create` holders are unaffected. No legitimate grant can target a type (`grants.target_id` references `entities.id`), so nothing legitimate is lost.
- **No schema or migration change** in mod-users, and none in the composed mod-core or mod-authz migrations it tests against.

## How modules pin each other (observed conventions)

- Go modules are all `v0.0.0`, resolved through `replace` directives to sibling checkouts (`api/go.mod`: `../../mod-core/*`, `../../mod-authz/*`, `../../mod-audit/*`). There is **no semver or git-tag release convention** in the ecosystem today. `docs-mf-standards/versions-lockfile.md` says module semver/pseudo-version pinning is a reserved, unimplemented "published mode".
- `versions.lock.yaml` pins **git commit SHAs of sibling repos** for CI and fresh clones only. Local and worktree builds float against whatever is checked out. SHAs are derived and never hand-edited: run `make pins.update`, sourced from each sibling's `origin/main`. Today the carriers are mod-users, app-mftodo, and app-mfmanager (`app-mfmanager/versions.lock.yaml` pins `mod-users: b248a07…` and `mod-authz: 3f9b9b9…`). mod-core and mod-authz carry no lockfile.
- So "releasing" the mod-users fix means merging it to mod-users `main` and pushing to `origin`. Local consumers see it immediately through the sibling checkout. CI consumers see it once their lockfile's `mod-users` pin is bumped with `make pins.update`.

## Recommendation

- **Do not cut a git tag** for this change. No consumer resolves tags, and adding a one-off tag would create a convention nobody reads. The close-out merge commit on mod-users `main` is the consumable artifact. Report its SHA to the manager so the mod-authz slice and app pin bumps can reference it.
- **mod-users' own `versions.lock.yaml` needs no change.** mod-users does not consume mod-authz's fix. It imports only `authz-api/authz` (the operation registry) and `authz-model`.
- **Ordering:** mod-users lands first, merged and pushed to `origin/main`. Then the mod-authz slice migrates its four type-id call sites using the structural assert-or-nil-fallback pattern. Then app-mfmanager (and app-mftodo, if it composes mod-authz group services) bump the `mod-users` and `mod-authz` pins in one `make pins.update` and re-run their smoke tests. The app pin bumps are outside this plan and are filed as followups.
- **Four-project landing order.** All dependencies are soft. Preferred order: mod-users, mod-core, mod-authz, mod-workflows, so production takes the explicit `AuthorizeType` path. Each slice closes the hole through the nil fallback even against today's mod-users.
- **Pin-bump consumers (followups, after everything lands):** app-mfmanager (`zdj9`), app-mftodo (`mJ3M`), and mod-core, which is pinned by app-mfgit and mod-users.
- **mod-core's helper.** mod-core adds `authz.TypeAuthorizer` and `authz.AuthorizeType` in core-api with a method signature identical to `localAuthz.TypeAuthorizer`, so `*Authorizer` satisfies it automatically. mod-users does not depend on it. Optional follow-on after both land: alias `localAuthz.TypeAuthorizer` to core's (note only, no task).
- **Soft dependency, flagged to the manager:** mod-authz's fallback branch (`Authorize(ctx, op, nil)`) closes the hole even against today's mod-users. So mod-authz's fix is safe to land in either order, but only the combination gives the explicit type-level API.

## Guidance for the mod-authz slice (informational; mod-users does not edit mod-authz)

- In `actor_group_service.go` and `target_group_service.go`, replace each `s.az.Authorize(ctx, "create"|"list", &typeID)` with a helper that type-asserts `interface{ AuthorizeType(context.Context, string, int64) error }` on `s.az` and otherwise calls `s.az.Authorize(ctx, op, nil)`. Never fall back to `&typeID`.
- Do not import `github.com/moduleforge/mod-users/api/localAuthz`. Declare the structural interface locally.
- Regression: a non-wildcard actor who owns the entity whose id equals the group type id gets `ErrForbidden` on Create and List. A wildcard-manage actor is allowed.
