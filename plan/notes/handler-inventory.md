# Handler Inventory

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This note holds the planner's findings for the mod-users conformance plan:

- how task worktrees build;
- the planning decisions every task follows;
- the consumers that are affected;
- a preliminary per-route table;
- the literal-site summary;
- dispositions for the existing project followups this plan touches.

Task 1.1 verifies every row against source and appends the authoritative five-axis audit table ([common section 2](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#2-per-handler-conformance-audit-procedure)) under [Audit table](#audit-table). Later tasks re-verify the rows they touch. Where source disagrees with this note, the source wins, and the task records the difference in its report. Line numbers are from plan-branch base `a2d704c`.

## Building and validating from a task worktree

`api/go.mod` resolves its siblings through relative replaces: `../../mod-core/{api,model}`, `../../mod-audit/{api,model}` and `../../mod-authz/{api,model}`. Take a task worktree at `/Users/zane/playground/moduleforge/mod-users/worktrees/task/<name>/`. From its `api/`, `../../mod-core` is `mod-users/worktrees/task/mod-core`, a symlink to `/Users/zane/playground/moduleforge/mod-core`. That is the mod-core `main` checkout, which carries the wave-1 helpers and the merged `core-handler-conformance` changes. `mod-audit` and `mod-authz` work the same way. The symlinks already exist; `scripts/link-siblings.sh` plants them, run by root `make preflight` from the worktree root. Check the resolution with:

```bash
cd <worktree>/api
go list -m -f '{{.Dir}}' github.com/moduleforge/core-api   # expect .../worktrees/task/mod-core/api
```

If the replace still does not resolve, do not edit `go.mod`, `go.sum` or `versions.lock.yaml` (common rule 8). Use an external workspace file in your scratch directory instead:

```bash
cat > "$SCRATCH/go.work" <<EOF
go 1.26.2

use (
	$WT/api
	$WT/model
)

replace github.com/moduleforge/core-api v0.0.0 => /Users/zane/playground/moduleforge/mod-core/api
replace github.com/moduleforge/core-model v0.0.0 => /Users/zane/playground/moduleforge/mod-core/model
replace github.com/moduleforge/audit-api v0.0.0 => /Users/zane/playground/moduleforge/mod-audit/api
replace github.com/moduleforge/audit-model v0.0.0 => /Users/zane/playground/moduleforge/mod-audit/model
replace github.com/moduleforge/authz-api v0.0.0 => /Users/zane/playground/moduleforge/mod-authz/api
replace github.com/moduleforge/authz-model v0.0.0 => /Users/zane/playground/moduleforge/mod-authz/model
EOF
export GOWORK="$SCRATCH/go.work"   # $WT = absolute task worktree path
```

Sibling builds float against whatever is checked out. The `authz-conformance` plan may land in mod-authz while this plan runs, and it keeps `RegisterRoutes`, `service.New` and the `*Servicer` interfaces unchanged. If a sibling change breaks this module's build, halt and report it; do not code around it.

Standard per-task validation, run from `api/`:

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go vet -tags=integration ./...
go test ./...
make lint        # api/Makefile: vet + gofmt + scripts/check-server-error-literals.sh
```

The Postgres integration suites live in `api/internal/authz/*_integration_test.go`, which also covers user-account create/list and ssh-keys, and in `api/internal/config`. Run them with the throwaway-container recipe in AGENTS.md, "Authz integration tests against a throwaway Postgres". Never use `make dev.start` or the shared `users-module-postgres`. A task that changes service behavior runs the suite when Docker is available and otherwise says so. Task 5.3 must run it.

GUI: task worktrees have no `node_modules`. Only tasks that touch `gui/` (5.1, 5.3) run the following, and only if dependency installation is permitted in the dispatch: `bun install --frozen-lockfile` at the worktree root, then `cd gui && bun run typecheck && bun test`. AGENTS.md "Working in worktrees" has a known gap: build `mod-core/gui` from mod-core's own real checkout, not through the `../mod-core` symlink. If installation is not permitted or fails, report the GUI checks as not run; do not mark them passed. Root `make test` includes `test.unit.gui`, so it fails without the GUI dependencies. Per-task validation therefore uses the `api/` commands above.

Baseline at plan start (planner, 2026-10-11): all of the `api/` commands above pass, and `check-server-error-literals.sh` reports 4/4 carve-outs.

## Planning decisions

Tasks implement these decisions unless the source contradicts their premise, in which case the task halts and reports.

- **D1 RequireAuth placement.** Mount mod-core `authz.RequireAuth` (`github.com/moduleforge/core-api/authz`, imported as `coreAuthz` in this package) with `r.Group(func(r chi.Router) { r.Use(coreAuthz.RequireAuth); ... })` inside the 8 authenticated registrars:
  - `RegisterAccountRoutes`
  - `RegisterSelfGetRoute`
  - `RegisterSelfPutRoute`
  - `RegisterSelfIdentitiesReadRoute`
  - `RegisterSelfIdentitiesWriteRoutes`
  - `RegisterSelfSSHKeysReadRoute`
  - `RegisterSelfSSHKeysWriteRoutes`
  - `RegisterUserAccountSSHKeyRoutes`

  Never call a bare `r.Use` on the router passed in. mfgen merges several `/v1` entries onto one router (common section 3).

  Do NOT mount it in `authhandlers.RegisterRoutes` (public plus credential routes) or in `RegisterOIDCConfigRoutes`. That group is public (status, saved, setup-token) or credential-authenticated by admin bearer or setup token (confirm, providers). Common rule 2 applies.

  In production, every manifest entry for these registrars already lists mod-users' own token-verifying `requireAuth` middleware (`api/internal/auth/middleware.go`, 401 code `unauthorized`). That middleware runs before the registrar, so the core gate is defense in depth that fires only on direct mounts and in tests. When it does fire, its 401 code is `unauthenticated`.

  Do NOT add `scope: authenticated` to `moduleforge.module.yaml`. mfgen applies the `middleware:` list regardless of scope, and `scope: authenticated` would wrap a second `requireAuth` around the leaf (`mfgen/internal/codegen/main_gen.go:900-940`). The mfgen empty-scope issue VFHF therefore does not bite these entries.

  Leave the dev server `api/cmd/server/main.go` alone. It hand-mounts handlers inside its own `RequireAuth` group and is not regenerated.

  There are no inline `opctx.ActorEntityID`/`RequireAuthenticated` 401 checks in `api/internal/handlers`. The only one is service-level, in `UserAccountService.Assume` (`user_accounts.go:683`, missing actor gives 404), and D2 handles it.

- **D2 Masked user-account miss.** `user_accounts.uuid` is the account's own UUID, not an `entities.uuid` (`model/migrations/sql/0100_baseline.sql:25-34`), so `entity.Resolver.AuthorizeResolve` does not apply. Add one unexported helper in `api/internal/service` that mirrors `AuthorizeResolve` semantics (common section 3):
  1. `authz.RequireAuthenticated(ctx)` (gives 401).
  2. A nil authorizer fails closed with `apiresp.ErrForbidden`.
  3. `GetUserAccountByUUID`.
  4. On `pgx.ErrNoRows`: call `az.Authorize(ctx, op, &zero)` with `zero := int64(0)`, discard the result, and return `apiresp.ErrForbidden`.
  5. Wrap other DB errors.
  6. On a hit, return the result of `az.Authorize(ctx, op, &ua.AccountHolder)` unchanged.

  This is one branch-carrying helper, not new machinery (common rule 7). It is used by:
  - `Get` (read)
  - `Update` (update)
  - `Delete` (delete)
  - `Assume` (assume). The sudo-account lookup moves after authorization, and a missing actor becomes 401.
  - `SSHKeyService.loadAuthorizedAccount` (`service/ssh_keys.go:93`). This adds the target-0 call to its existing masked miss and maps a `GetEntityByID` `ErrNoRows` to the masked 403 (followup 433h). Keep its archived-holder masking.

  A missing account is 403 for every caller, wildcard admins included. This is the accepted `AuthorizeResolve` residual, and order tests use `AuthorizedMissingStatus: 403`. Followup CuAF(a) suggested a 404 for wildcard holders; the wave template overrides that.

  The handlers drop their own miss mappings: the `pgx.ErrNoRows` to 404 branch at `user_accounts.go:152` and the `coreservice.ErrNotFound` branch at `assume.go:70`.

  The grant-admin and revoke-admin closures (composition roots) authorize nil-target `manage` first, so their 404 is post-authorization and conformant. Apps' copies of those closures are out of scope.

- **D3 Apps membership** (`api/internal/handlers/apps.go`). Keep the nil-target `Authorize` (wildcard admin; not `AuthorizeType`), but move it after every stateless check:
  - `apiresp.ParseUUIDParam("uuid", ...)`;
  - `ParseUUIDParam("user_account_uuid", ...)` on Remove and UpdateRoles;
  - the body decode on Assign and UpdateRoles;
  - on Assign, `user_uuid` required (keep code `users.user_uuid_required`) and UUID-formatted (`FieldError{Field: "user_uuid", Code: "invalid_uuid"}`) in a `Validator`.

  After authorization, look the app up with `coreQ.GetAppByUUID` only; `GetAppByUUIDRow.ID` is the `entities.id`. A miss is 404, which is legitimate post-authorization. Drop the `entityResolver.Resolve` call and the `entityResolver.AllowNotFound("app")` call in `NewAppsHandler`. That call mutates the resolver shared with mod-core's services and re-opens the 404 policy that mod-core's plan closed.

  Keep the `NewAppsHandler` signature (manifest and facade); the `entityResolver` parameter may become unused, which is documented. Preserve today's treatment of archived apps. If `GetAppByUUID` and `Resolve` differ on archived rows, report it.

- **D4 User-account create and update shape.**
  - Delete the 403-before-400 special case (`user_accounts.go:63-76`). It cannot survive D6 anyway, because `DecodeJSON` writes the 400 itself.
  - Create's stateless checks (email, given_name and family_name required after trim; password at least 12 characters when present) move into a `Validator` on `createUserAccountRequest`, keeping the `users.*` detail codes, so they run before `AuthorizeCreate`. The service copies stay for in-process callers.
  - Update rejects present-but-blank `email`, `given_name` and `family_name` statelessly (`users.email_required`, `users.given_name_required`, `users.family_name_required`). Today it stores them (followup 1eYt). A service-level copy for in-process callers is optional; record the choice.
  - Path UUIDs go through `apiresp.ParseUUIDParam("uuid", ...)` (detail `invalid_uuid`), replacing the local `parseUUIDParam`.

- **D5 PUT /self and the mod-core change at `self.go:79`.** `NaturalPersonService.UpdateByEntityUUID` now runs `AuthorizeResolve` (a miss is `ErrForbidden` and needs an authenticated actor) and then rejects a present-but-blank `given_name`/`family_name` with `natural_persons.given_name_required`/`natural_persons.family_name_required` after authorization.
  - For `PUT /self` the target is the caller's own existing entity, and `requireAuth` guarantees an actor, so the masked-miss change is not observable. Verify that a non-admin caller still updates their own name; the ownership arm of mod-users' `Authorizer` must grant `update` on the caller's own entity.
  - The handler adds the same blank-name checks statelessly, before calling the service, with the same `natural_persons.*` codes. The 400 then precedes any authorization, and the wire code matches what mod-core's own handler returns.
  - Record the behavior for a corporation or service-account holder who sends `given_name`, and for an assume (sudo) session.
  - mod-users does not proxy mod-core's corporation or service-account update routes. The dev server mounts mod-core's router directly, so the new `legal_name`/`label` 400s reach mod-users clients only through mod-core's own handlers. They get a wire note and no code change here.
  - mod-core's `ProfileEditor` (`mod-core/gui/src/ProfileEditor.tsx`) is not used by `mod-users/gui`. Apps that pair it with `PUT /v1/self` get a wire note (blank names are now 400).

- **D6 `server.Decode` becomes a `DecodeJSON` wrapper.**
  - New signature: `func Decode(w http.ResponseWriter, r *http.Request, v any, opts ...apiresp.DecodeOption) bool`, returning `apiresp.DecodeJSON(w, r, v, opts...)`. The reserved slot names the wrapper; it keeps one module seam for options.
  - All 16 callers become `if !server.Decode(w, r, &req) { return }`.
  - No `WithCodePrefix`. Today's decode failures are a bare `invalid_input` with no details in `handlers/*`, or a literal `invalid_input` "invalid JSON body" in `handlers/auth/*`, so there is no detail-code namespace to preserve. New details: `invalid_json_body` and `body_too_large`.
  - Default 1 MiB cap. SSH-key register keeps its 32 KiB cap through `WithMaxBytes(sshKeyRegisterBodyMaxBytes)`, replacing its `MaxBytesReader`.
  - All 16 callers already reject an empty body (`json.Decoder` returns `EOF`), so there is no empty-body special case among them.
  - Unknown fields become 400 everywhere, including the public auth routes. Before converting, grep every client of each route: `gui/src/lib/api.ts`, the app GUIs under `/Users/zane/playground/moduleforge/app-*/gui/src`, and any other playground caller. If one sends a field the server struct lacks, halt and report rather than reaching for `WithAllowUnknownFields`. The known exceptions, `apps.create`/`update` and `apps.addMember` in `gui/src/lib/api.ts`, are fixed by task 5.1 and have no callers today.

- **D7 `oidc_providers.go` `parseBody`** (`:418-431`). Follow the empty-body special case in common section 3:
  - cap the read with a `MaxBytesReader` (1 MiB);
  - treat zero bytes as an empty map (preserved);
  - otherwise restore `r.Body` and call `server.Decode` into `map[string]json.RawMessage`, so trailing data and over-cap bodies are rejected;
  - add an explicit allowlist of the keys the handlers read (`setup_token`, `id` and the provider fields used by `upsertAndRespond`/`merge*`). An unknown key gives 400 `FieldError{Field: <key>, Code: "invalid_json_body", Message: "request body contains an unknown field"}`, matching `DecodeJSON`'s unknown-field code.

- **D8 Credential-in-body routes** (`POST /oidc-config/confirm`, `POST /oidc-config/providers`, `PUT /oidc-config/providers/{id}`). These are credential-authenticated by an admin bearer (`AdminChecker`), the `X-Setup-Token` header, or a `setup_token` body field. Order:
  1. Check the body-independent credentials first. If either authenticates, decode (400) and continue.
  2. Otherwise decode the body. A malformed body gives 400 here, unavoidably: the body carries the credential, the same class as `POST /auth/login`. Then verify the body token (401).
  3. Then run the remaining stateless shape checks (id pattern, required id), each 400.

  The bodyless `GET`/`DELETE /oidc-config/providers/{id}` stay: credential (401), then id pattern (400).

  This partly adopts followup 7J9z: a header or admin caller with a bad body now gets 400 after 401. The remaining "400 before 401" case (no header or admin, malformed body) is a documented exception. It is recorded here, flagged to the manager, and covered by a dedicated test in task 4.4.

  The 409 "provider id already exists" ZVum carve-out (`oidc_providers.go:222`) becomes `apiresp.Conflict(FieldError{Field: "id", Code: "users.oidc_provider_exists", Message: "provider id already exists; use PUT to update"})`. Lower `EXPECTED_CARVEOUTS` in `api/scripts/check-server-error-literals.sh` to 3 in the same change.

- **D9 Step-up after stateless checks.** The step-up gate (`409 users.step_up_required`, action-required) is authorization-dependent (common rule 6), so it belongs at the 403 stage: after every stateless check, before any state-dependent one. Move the path-UUID parses, body decodes and stateless field checks ahead of `requireStepUp`/`checkStepUp` in:
  - identities `Unlink`, `SetPassword` and `StartLink` (if it has stateless checks);
  - ssh-keys `RegisterSelf`, `RevokeSelf`, `RegisterForAccount` and `RevokeForAccount`.

  Side benefit: a malformed request no longer consumes a single-use step-up token. The action-required envelope itself is unchanged.

  `SetPassword`'s length check before `current_password` is already conformant: it is stateless, and the `current_password` 401 is a credential check (part of followup 1eYt).

- **D10 SSH key parsing before authorization.** `sshkey.Parse` and `sshkey.NormalizeLabel` are stateless. In `SSHKeyService.Register`, run them before `loadAuthorizedAccount`, so a caller without `update` on the account gets 400 for a malformed key. This reverses followup HHFv's proposal, which predates the rule. Duplicate-fingerprint 409 stays after authorization.

- **D11 Middleware caller-state gates (flagged exception).** Three gates run before any handler 400:
  - `requireOIDCConfirmed`: 503 `users.oidc_not_confirmed`, and it runs even before 401.
  - `requireVerifiedEmail`: 403 action-required `users.email_unverified`.
  - On `/v1` only: CORS and the like.

  They depend only on service state or the caller's own account. They are body-independent and reveal nothing about a target or about request-shape rules. Decision: keep them as they are, record them as a documented exception, and flag them to the manager. Common section 1 says only `AuthorizeType` is pre-approved for running before shape validation.

  Order tests install a verified `UserContext`, so the email gate passes. Task 4.3 adds one explicit test of each gate's position.

- **D12 Literal `server.Error` conversion.** "Equivalent" means the replacement produces the same status, the same top-level `code` and the same `details`; the message may become apiresp's fixed public message for that code.
  - Convert:
    - 500 `internal_error` literals become `apiresp.WriteError(w, r, fmt.Errorf("<op>: %w", err))`;
    - decode literals become `server.Decode` (D6);
    - required-field 400s become `apiresp.InvalidInput(FieldError{...})`, keeping existing `users.*` codes. A literal that had no details gains them; new detail codes are recorded as wire changes;
    - 409 with details becomes `apiresp.Conflict(...)`;
    - plain 404 "unknown provider" becomes `apiresp.ErrNotFound`.
  - Keep, as literals with a one-line comment giving the reason:
    - credential-failure 401s with a specific message (login, email-code verify, password-reset confirm, and similar);
    - every literal in `api/internal/auth/middleware.go` (401 code `unauthorized`, a different code from apiresp's);
    - the three `identities.go` ZVum carve-outs (a 401 and a 404 with details; apiresp has no detail-carrying constructor for either);
    - any literal whose message a client displays or asserts. Grep `gui/src` and the tests for the message string before converting.

  Planner count: 66 literal sites in `handlers/auth` (anonymous 4, emailcode 10, login 7, oidc 21, register 16, reset 8), 4 ZVum carve-outs in top-level `handlers`, 6 in `internal/auth/middleware.go`, and the definition in `server/respond.go`. The reserved slot says "65"; task 1.1 gives the authoritative count.

- **D13 Pass-through wrappers.** Delete `writeAuthzError` (and `api/internal/handlers/errors.go`), `writeServiceError` (`user_accounts.go:49`) and `writeCoreServiceErr` (`self.go:155`; followup 6ZEz). Callers call `apiresp.WriteError` directly. Then delete the UUID-parse wrappers `parseUUIDParam` (`user_accounts.go:259`) and `parseUUIDPathParam` (`ssh_keys.go:337`) in favor of `apiresp.ParseUUIDParam` in the phase-3 tasks that touch those files.

- **D14 Order-test harness.** The existing handler tests use stubs that cannot model authorization (for example `stubUserAccountService`), so the order tests wire real services. Use `svc.NewUserAccountService`, `usersservice.NewSSHKeyService` and the real handlers, with fakes for `db.Querier` and `coredb.Querier` (embed the interface and implement only what is used, as in `/Users/zane/playground/moduleforge/mod-audit/api/httpapi/order_test.go`), a fake `txhelper.DB`, a `types.Resolver` and an observer group.
  - The `orderAuthorizer` allows the owner or admin, denies the stranger, and quietly denies target 0 (common rule 5).
  - `ordertest` installs only an `opctx` identity, but mod-users handlers call `auth.MustFromContext` and the email gate needs a verified `UserContext`. The harness therefore wraps the router in an `orderUserContext` shim: when `opctx` carries an effective actor, it installs a matching verified `localauth.UserContext` via `WithUserContext`; with no actor it does nothing, so the core `RequireAuth` still answers 401.
  - All shared helpers live in `api/internal/handlers/order_helpers_test.go`, created only by task 4.2, with every identifier prefixed `order`. Tasks 4.3 and 4.4 reuse it and add helpers only under their own prefixes (`orderSelf`, `orderOIDC`).
  - The `api/internal/handlers/auth` package is separate and uses its own file with prefix `orderAuth`.
  - `AuthzFirst` is not used for any mod-users route.

- **D15 Post-authorization classification (task 4.1; planned, not decided).**
  - `PUT /user-accounts/{uuid}`: an email unique violation (500 today) becomes 409 `users.email_taken` via `svc.ErrEmailTaken`.
  - `POST /apps/{uuid}/user-accounts`: a body `user_uuid` that names no account (404 today) becomes 422. It is a referenced value in the body (common section 1).
  - Path `{user_account_uuid}` misses on Remove and UpdateRoles stay 404.
  - Remove or UpdateRoles on a non-member succeeds silently today. Record it and do not re-status it.
  - Identities `Unlink` last-identity 409 and ssh-keys duplicate 409 already exist.
  - OIDC `StartLink` and provider registry misses are classified by the task.
  - A capability gap is flagged, never given a status silently.

## Consumers affected

- **HTTP clients** of mod-users routes:
  - `@moduleforge/users-gui` (`gui/src/lib/api.ts`);
  - the app GUIs that embed it (app-mftodo, app-mfgit, app-mfdemo, app-mfmanager and any other app composing mod-users; task 5.2 greps `/Users/zane/playground/moduleforge/app-*/moduleforge.app.yaml` or the equivalent for the exact set);
  - external clients of `/v1/auth/*`.

  Composing apps generate their `cmd/server` from `moduleforge.module.yaml`, so they pick up the registrar-level `RequireAuth` automatically and need no regeneration for it.
- **In-process callers** of `UserAccountService`: the `api/usersservice` facade, `api/internal/users/hooks.go`, and any app hook. A missing account on `Get`/`Update`/`Delete`/`Assume` returns `ErrForbidden` instead of `ErrNotFound`, and `Assume` with no actor returns `ErrUnauthenticated` instead of `ErrNotFound`. Task 1.1 greps the playground for callers.
- **The consumer authorizer**: mod-users' own `Authorizer` (`api/internal/authz/authz.go:220`) and test fakes receive an extra target-0 `Authorize` call per masked miss. The planner read that a non-admin's target 0 matches no grant or ownership row and returns `ErrForbidden`, while a wildcard admin returns nil, which is discarded. Task 1.1 confirms this.
- **Composition-root closures** (`grantAdmin`/`revokeAdmin` in each app manifest) get wire notes only.

### Consumers found by task 1.1 (playground grep, `worktrees/` and `node_modules/` excluded)

- **In-process callers of `UserAccountService.Get`/`Update`/`Delete`/`Assume`/`LoadByUUID`**: only mod-users' own handlers. `UserAccountsHandler` calls `Get`, `Update`, `Delete`; `AssumeHandler` calls `Assume` through the `assumeServicer` interface. `LoadByUUID` (no authorization) has no non-test caller. The `api/usersservice` facade and `api/internal/users/hooks.go` do not call any of the five. `app-mfdemo`, `app-mfgit` and `app-mftodo` (`cmd/server/main.go`) only construct the service through `usersservice.NewUserAccountService` and hand it to `NewUserAccountsHandler` and `NewAssumeHandler`; `mod-billing` mentions `UserAccountService.Delete` in a comment only (`seatrelease_observer.go`).
- **In-process callers of `SSHKeyService`**: `SSHKeysHandler` only, built by `NewSSHKeyService` in mod-users' dev server and in the three apps above. `SSHKeyResolver` (git-over-SSH key resolution) is separate, calls no `Authorize`, and is unaffected.
- **Direct registrar mounts**: the three apps above mount every registrar from `cmd/server/main.go` (generated), one `r.Group` per entry. `app-mfmanager` mounts `RegisterSelfGetRoute` directly in `api/svcconfine/svcconfine_integration_test.go:325`.
- **HTTP clients**: `@moduleforge/users-gui` (`gui/src/lib/api.ts`, `oidc-config.ts`, `oidc-provider.ts`), which `app-mftodo/gui`, `app-mfmanager/gui`, `app-mfgit/gui` and `app-mfdemo` depend on (`package.json`). The GUI calls login, register, password-reset request/confirm, email-code request/verify, `GET`/`PUT /self`, `/user-accounts` (list, get, update, assume, `/admin`, `/audit`), `/apps/{uuid}/user-accounts`, `/self/ssh-keys`, step-up request/verify, `/auth/providers`, `/auth/oidc/{id}/start`, and all `/oidc-config/*` routes. The GUI has no client for `/self/identities` or `/self/credential/password`. Field mismatches against server structs: `auth.resetPassword` sends `new_password` (server reads `password`; finding F1) and `apps.addMember` sends `role` (server reads `roles`; task 5.1). GUI path mismatches: `userAccounts.grantAdmin`/`revokeAdmin` use `/admin` (server: `/grant-admin`, `/revoke-admin`) and `userAccounts.audit` uses a removed path (finding F15). `app-mfdemo` `admin/apps/[uuid]/page.tsx` calls `addMember` with `role`. No other playground HTTP client of a mod-users route was found.
- **Consumer authorizers**: confirmed as the planner read it. See "D2 premise" under the audit table.

## Preliminary route table

The "today" columns describe base `a2d704c`. Route classes: A = authenticated (registrar group carries `requireAuth`), V = authenticated plus verified email, C = credential-authenticated, P = public. "nil" means a nil-target `Authorize` (wildcard only); "type" means `AuthorizeType`.

| # | Route | Handler | Class | Stateless checks today | Authorize | Resolve / existence today | Post-authz errors | Planned (task) |
|---|---|---|---|---|---|---|---|---|
| 1 | GET /user-accounts | `UserAccountsHandler.List` | V | limit/offset clamp silently | type `list` | none | none | 1.2 |
| 2 | POST /user-accounts | `.Create` | V | decode, then `AuthorizeCreate` on failure (403-before-400); required fields and password length AFTER authz (service) | type `create` | none | 409 `users.email_taken` is declared but never fires: the wrapped unique violation is not matched, so a duplicate email is 500 today (task 1.1 correction) | 1.2, 3.2 (D4), 3.3, 4.1 |
| 3 | GET /user-accounts/{uuid} | `.Get` | V | uuid parse | entity `read` on holder | lookup before authz, 404 leak (the handler's `pgx.ErrNoRows` branch is dead: the service already returns `coreservice.ErrNotFound`) | none | 2.1, 3.3 |
| 4 | PUT /user-accounts/{uuid} | `.Update` | V | uuid parse; decode | entity `update` | lookup before authz | unique email gives 500; blank fields stored | 2.1, 3.3, 4.1 |
| 5 | DELETE /user-accounts/{uuid} | `.Delete` | V | uuid parse | entity `delete` | lookup before authz | none | 2.1, 3.3 |
| 6 | POST /user-accounts/{uuid}/grant-admin | `.GrantAdmin` | V | uuid parse | closure: nil `manage` | lookup after authz (404 ok) | closure-defined | 3.3 (parse only) |
| 7 | POST /user-accounts/{uuid}/revoke-admin | `.RevokeAdmin` | V | uuid parse | closure: nil `manage` | as 6 | as 6 | 3.3 |
| 8 | POST /user-accounts/{uuid}/assume | `AssumeHandler.Assume` | V | uuid parse | entity `assume` | target and sudo lookups before authz; no actor gives 404 | none | 2.1, 3.3 |
| 9 | POST /apps/{uuid}/user-accounts | `AppsHandler.AssignUser` | V | ALL after authz: uuid parse, decode, user_uuid required/parse | nil `update` | Resolve(AllowNotFound) + GetAppByUUID after authz | user miss 404 | 3.4, 4.1 (422) |
| 10 | GET /apps/{uuid}/user-accounts | `.ListAppUsers` | V | uuid parse after authz | nil `read` | as 9 | none | 3.4 |
| 11 | DELETE /apps/{uuid}/user-accounts/{user_account_uuid} | `.RemoveUser` | V | both parses after authz | nil `update` | as 9; user miss 404 | non-member silent 204 | 3.4, 4.1 |
| 12 | PUT /apps/{uuid}/user-accounts/{user_account_uuid}/roles | `.UpdateUserRoles` | V | parses after authz; decode after user lookup | nil `update` | as 9 | non-member silent 200 | 3.4, 4.1 |
| 13 | GET /self | `SelfHandler.Get` | A | none | none (self) | none | none | 1.2 |
| 14 | PUT /self | `.Put` | V | decode; blank names checked in core AFTER its authz | core `AuthorizeResolve` `update` on own entity | own entity | corp/SA holder sending names: verify | 3.3 (D5) |
| 15 | GET /self/identities | `IdentitiesHandler.List` | A | none | none (self) | none | none | 1.2 |
| 16 | POST /self/identities/oidc/{provider}/start | `.StartLink` | V | step-up FIRST; provider normalize | step-up | provider registry miss gives 404 | `LinkAuthorizeURL` error gives 400 | 3.5, 4.1 |
| 17 | DELETE /self/identities/{identity_uuid} | `.Unlink` | V | step-up before uuid parse | step-up | own identity lookup gives 404 (ZVum literal), but the 409 last-identity check runs BEFORE that lookup (task 1.1 correction) | 409 last identity | 3.5, 4.1 |
| 18 | POST /self/credential/password | `.SetPassword` | V | step-up before decode; length | step-up | none | 401 current_password (ZVum) | 3.5 |
| 19 | DELETE /self/credential/password | `.RemovePassword` | V | step-up | step-up | none | 409 `users.last_identity` when no OIDC identity remains (confirmed); no local credential gives an idempotent 204 | 3.5, 4.1 |
| 20 | POST /self/credential/step-up | `.StepUpRequest` | V | none | none (self) | none | none | none; verified: no stateless checks, no authz, always 204 after at least 200 ms |
| 21 | POST /self/credential/step-up/verify | `.StepUpVerify` | V | decode; code required | none (self) | none | 401 bad code (credential) | 3.2 |
| 22 | GET /self/ssh-keys | `SSHKeysHandler.ListSelf` | A | pagination clamp | `read` on own holder | masked | none | 1.2 |
| 23 | POST /self/ssh-keys | `.RegisterSelf` | V | step-up FIRST; 32 KiB decode; public_key required; key parse AFTER authz | step-up; `update` | masked | 409 duplicate | 3.2, 3.5 (D9, D10) |
| 24 | DELETE /self/ssh-keys/{key_uuid} | `.RevokeSelf` | V | step-up before key_uuid parse | step-up; `update` | masked 403 | none | 3.5 |
| 25 | GET /user-accounts/{uuid}/ssh-keys | `.ListForAccount` | V | uuid parse | `read` | masked 403 (no target-0 call) | none | 2.1 |
| 26 | POST /user-accounts/{uuid}/ssh-keys | `.RegisterForAccount` | V | uuid parse; step-up (self) before decode; key parse AFTER authz | step-up (self); `update` | masked | 409 duplicate | 2.1, 3.5 |
| 27 | DELETE /user-accounts/{uuid}/ssh-keys/{key_uuid} | `.RevokeForAccount` | V | both parses; step-up (self) | step-up (self); `update` | masked | none | 2.1, 3.5 |
| 28 | GET /oidc-config/status | `OIDCConfigHandler.Status` | P | none | none | none | none | 4.4 |
| 29 | POST /oidc-config/confirm | `.Confirm` | C | decode BEFORE credential | admin / header / body token | none | none | 3.6 (D8) |
| 30 | GET /oidc-config/saved | `.Saved` | P (confirmed unauthenticated) | none | none | none | none | 4.4 |
| 31 | POST /oidc-config/providers | `ProvidersHandler.Create` | C | map decode (no cap, empty ok) BEFORE credential; id required/pattern after | credential | none | 409 exists (ZVum literal) | 3.6 (D7, D8) |
| 32 | GET /oidc-config/providers/{id} | `.Get` | C | id pattern after credential, but a pattern mismatch gives 404, not 400 (task 1.1 correction; `TestProviders_Get_InvalidSlug_404` pins it) | credential | unknown id gives 404 | none | 3.6 (pattern mismatch to 400, per D8), 4.4 |
| 33 | PUT /oidc-config/providers/{id} | `.Update` | C | map decode BEFORE credential; id pattern after | credential | none | none: PUT is an upsert for any slug-valid id (no 404, no 409) | 3.6 |
| 34 | DELETE /oidc-config/providers/{id} | `.Revert` | C | id pattern after credential | credential | none | idempotent 204 | 4.4 |
| 35 | GET /oidc-config/setup-token | `.SetupToken` | P (loopback) | none | loopback check gives 403 | none | 404 when none | 4.4 |
| 36 | POST /auth/register | `auth.Handler.Register` | P | decode + required fields (literals) | none | none | 409 email taken (literal) | 3.2, 3.7 |
| 37 | POST /auth/login | `.Login` | P | decode + required (literal) | credential | none | 401 credential (keep) | 3.2, 3.7 |
| 38 | POST /auth/anonymous | `.Anonymous` | P | decode + device_id | none | none | none | 3.2, 3.7 |
| 39 | POST /auth/email-code/request | `.EmailCodeRequest` | P | decode | none | none | always 204 (anti-enumeration; the planner's 202 was wrong) | 3.2, 3.7 |
| 40 | POST /auth/email-code/verify | `.EmailCodeVerify` | P | decode + required | credential (code) | none | 401 credential (keep) | 3.2, 3.7 |
| 41 | POST /auth/password-reset/request | `.PasswordResetRequest` | P | decode | none | none | always 204 (the planner's 202 was wrong) | 3.2, 3.7 |
| 42 | POST /auth/password-reset/confirm | `.PasswordResetConfirm` | P | decode + token + length | credential (token) | none | 401 credential (keep) | 3.2, 3.7 |
| 43 | GET /auth/providers | `OIDCHandler.ListProviders` | P | none | none | none | none | 4.4 |
| 44 | GET /auth/oidc/{provider}/start | `.Start` | P | provider | none | unknown gives 404 (literal) | 400 echoes `err.Error()` (literal); the 400 is the stateless `return` check, mixed into the same call as the registry lookup | 3.7 |
| 45 | GET /auth/oidc/{provider}/callback | `.Callback` | P (state cookie) | code/state, cookie | state validation | unknown provider gives 404 | redirects; 500 literals | 3.7 |
| D1 | DELETE /v1/assume | `AssumeHandler.EndAssume` | A | none | none | none | none | dev server `main.go` only; no registrar or manifest entry: record only |
| D2 | GET /healthz | `health.go` `Live` | P | none | none | none | none | record only |
| D3 | GET /readyz | `health.go` `Ready` | P | none | none | none | 503 with the raw DB ping error text | record only (task 1.1 split the planner's single D2 row; D1/D2/D3 in this table are route-row labels, not planning decisions) |

Tests that depend on today's behavior (task 1.1 completes this list):

- `api/internal/handlers/user_accounts_authz_test.go` covers the 403-before-400 `Create` behavior through a hand-copied shim (followup N9AG), plus the 404 `Get` mapping.
- `api/internal/handlers/apps_authz_test.go` asserts authorize-before-resolve and 404 on an unknown app.
- `api/internal/handlers/oidc_config_test.go:585` (`TestConfirm_BadJSON`) and `oidc_providers_test.go` cover body and credential ordering.
- `api/internal/handlers/identities_stepup_test.go` and `ssh_keys_test.go` assert step-up before body/UUID errors in places.
- `api/internal/service/user_accounts_*_test.go`, `ssh_keys_test.go` and the `-tags=integration` suites in `api/internal/authz/` may assert `ErrNotFound` on misses.
- Added by task 1.1 after reading the tests:
  - `api/internal/handlers/oidc_providers_test.go:315` (`TestProviders_Get_InvalidSlug_404`) pins the 404 for a bad provider id (D8 changes it to 400).
  - `api/internal/handlers/user_accounts_authz_test.go` calls `writeServiceError` and `writeAuthzError` by name (`TestWriteServiceError_Mapping`, `TestWriteAuthzError_Mapping`) and uses a hand-copied handler shim (`TestShim_*`, with its own `server_Error`/`decodeJSON`); `TestShim_Get_ServiceNotFound` exercises the dead `pgx.ErrNoRows` branch only through the shim.
  - `api/internal/handlers/apps_authz_test.go` (authorize-before-resolve with a nil `coreQ` canary, and 404 for an unknown app) breaks when D3 moves stateless checks before `Authorize` and drops `Resolve`.
  - `api/internal/handlers/oidc_config_test.go:585` (`TestConfirm_BadJSON`) only asserts 400 for an unauthenticated caller; it survives D8.
  - `api/internal/handlers/identities_stepup_test.go` (`TestWrappedEndpoints_FlagOn_NoHeader_Returns409`) and `ssh_keys_test.go` (`TestSSHKeysHandler_StepUp_*`, `TestSSHKeysHandler_MalformedUUID_Returns400`, `TestSSHKeysHandler_OversizedBody_Returns400`, `TestSSHKeysHandler_MalformedJSON_Returns400`) assert step-up before body and UUID errors in places.
  - `api/internal/auth/middleware_test.go` and `anonymous_actor_test.go` assert the middleware literals' messages (kept by D12).

## Existing followups this plan touches

The manager disposes of these; tasks cite them and do not close them.

| Id | Title | Disposition by this plan |
|---|---|---|
| 6ZEz | writeCoreServiceErr pass-through in self.go | Fixed by 3.1 (D13). |
| PRvD | GUI apps client body shape mismatches core | Fixed by 5.1. |
| hunL | (dismissed) 403-before-400 special case | Special case deleted by 3.2 (D4). |
| CuAF | Mask user-account miss; authz before decode | (a) fixed by 2.1 (D2). (b) superseded by the rule: decode now precedes authorization. |
| N9AG | Real-handler test for Create decode-failure | Fixed by 3.3 (real-handler test: 400 for an unauthorized caller with a bad body). |
| HHFv | Authz before validation: operator ssh-key add | Superseded and reversed by D10. |
| 7J9z | Authz before 400 on OIDC confirm/writes | Partly adopted by 3.6 (D8); the residual exception is documented. |
| tbmN | SSH-key routes mask 403, sibling routes 404 | Resolved by 2.1: legacy user-account routes now mask too. |
| 433h | loadAuthorizedAccount doesn't mask ErrNoRows | Fixed by 2.1 (D2). |
| 1eYt | Minor authz-ordering/hygiene cleanups | Partly: Assume 401 (2.1), Update blank email (3.3), PUT /self ordering (3.3, D5), SetPassword ordering confirmed conformant (D9). Out of scope here: grant-admin/revoke-admin lookup mapping (composition-root closures), the swallowed `UpdateNaturalPerson` error, `/self` credential routes in assume sessions, SetupToken behind a proxy, the `/oidc-config/status` init-error leak, and the `email_taken` enumeration note. |
| E5py | identities.go lint: server.Error literals | Already resolved by the ZVum marker lint (baseline 4/4); the carve-out count changes to 3 in 3.6. |
| biJk | api/openapi.yaml missing identities surface | Not fixed; 5.2 documents only paths that exist in `api/openapi.yaml`. |

## Audit table

Task 1.1 (read-only audit, 2026-10-11) verified the preliminary table and decisions D1-D15 against source at plan base `06e5764` (code unchanged since `a2d704c`). The preliminary table above was corrected in place; the corrections are listed under [Corrections to the preliminary table](#corrections-to-the-preliminary-table). No source file was changed.

### Reading the table

- **Class**: A = authenticated, V = authenticated plus verified email, C = credential-authenticated, P = public (as in the preliminary table).
- **MW-A** = manifest middleware `requireOIDCConfirmed, requireAuth`. **MW-V** = MW-A plus `requireVerifiedEmail`. `requireAuth` is mod-users' own token-verifying middleware (401 code `unauthorized`). `requireOIDCConfirmed` runs first, so it can answer before 401 (D11). Every generated app main (`app-mfdemo`, `app-mfgit`, `app-mftodo`) mounts each registrar in its own `r.Group` with the same middleware list, so a bare `r.Use` inside a registrar would reach the shared `/v1` router (D1 confirmed).
- **core RA** = the planned `coreAuthz.RequireAuth` group (D1). It yields 401 code `unauthenticated` and only fires on direct mounts and in tests.
- "Stateless" means the check needs no server state. "Moves" means the check must run before the authorize call (or before the credential check for class C, per D8).
- Task numbers (2.1, 3.3, ...) are the plan's.

### Five-axis audit table

| # | Route | Class | 1. Authn gate | 2. Stateless checks and where they run | 3. Authorize call and target | 4. Resolve / existence | 5. Post-authz errors (planned class) | Wire-visible effect | In-process effect |
|---|---|---|---|---|---|---|---|---|---|
| 1 | GET /user-accounts | V | MW-V; no inline check. Add core RA (1.2). | `limit`/`offset`/`q`/`email` parsed in the handler; bad or out-of-range numbers silently default (never 400); the service clamps to 1..200. Nothing moves. | `authorizeType(list, natural_person)` is the first statement of `UserAccountService.List` (type-level, so `AuthzFirst` is legitimate but unused). | None. | None (DB error is 500). | None, except the 401 code on a direct mount. | None. |
| 2 | POST /user-accounts | V | MW-V; none inline. Add core RA (1.2). | JSON decode in the handler. On decode failure the handler calls `AuthorizeCreate` and then answers 400, so the order is 403-before-400 (`user_accounts.go:63-76`). Email, given_name and family_name (trimmed) and password length (>= 12 when present) run in the SERVICE after `AuthorizeCreate`. All of them are stateless and MOVE before authorization into a `Validator` (D4). | `authorizeType(create, natural_person)` twice: in the handler on decode failure, and at the top of `Create`. | None. | 409 `users.email_taken`: CORRECTION, it never fires. The unique violation reaches `isPgUniqueViolation` wrapped (`fmt.Errorf("...: %w")` inside the tx closure), and `extractPgError` type-asserts instead of `errors.As`, so a duplicate email is a 500 today. Classify as 409 once matched with `errors.As` (finding F2; belongs to 3.3/4.1). | Bad body or missing fields now 400 for an unauthorized caller (was 403). Duplicate email 500 to 409. | `Create` keeps its own validations (no change). The duplicate-email fix changes the error for in-process callers (500-class to `ErrEmailTaken`). |
| 3 | GET /user-accounts/{uuid} | V | MW-V. Add core RA (1.2). | `uuid.Parse` of `{uuid}` in `parseUUIDParam`, before the service. Bare `ErrInvalidInput`, no details (becomes `ParseUUIDParam`, 3.3). | `Authorize(read, &holder)` AFTER the lookup. | `GetUserAccountByUUID` before authz; a miss returns `coreservice.ErrNotFound` (404): existence leak. The handler's own `err == pgx.ErrNoRows` branch (`:152`) is dead because the service already maps the miss. Fix: D2 helper (2.1). | None. `GetNaturalPersonByEntityID` failure is swallowed (name enrichment only). | 404 to 403 for every caller, wildcard admins included (accepted residual). | `Get` miss returns `ErrForbidden` instead of `ErrNotFound`. |
| 4 | PUT /user-accounts/{uuid} | V | MW-V. Add core RA. | uuid parse, then decode (bare 400), both before the service. Present-but-blank `email`, `given_name`, `family_name` are NOT checked (blank is stored, followup 1eYt); add stateless checks with the `users.*_required` codes (D4, 3.3). | `Authorize(update, &holder)` after the lookup. | Lookup before authz, 404 leak. Fix: D2 helper. | Duplicate email: 500 today (tx error is wrapped; same bug as row 2). D15: 409 via `ErrEmailTaken` (4.1). `UpdateNaturalPerson` error is discarded (`_ =`) and the update is silently skipped for a holder that is not a natural person (out of scope, noted under 1eYt). | Blank fields 400 (was stored). 404 to 403. Duplicate email 500 to 409. | `Update` miss: `ErrNotFound` to `ErrForbidden`. A service-level blank check is optional (D4). |
| 5 | DELETE /user-accounts/{uuid} | V | MW-V. Add core RA. | uuid parse. | `Authorize(delete, &holder)` after the lookup. | Lookup before authz, 404 leak. Fix: D2 helper. | None. | 404 to 403. | `Delete` miss: `ErrNotFound` to `ErrForbidden`. |
| 6 | POST /user-accounts/{uuid}/grant-admin | V | MW-V. Add core RA. | uuid parse (handler). | Closure in the composition root (`main.go:464`): `az.Authorize(manage, nil)` FIRST. | The closure then looks the account up. EVERY lookup error (not only a miss) maps to `ErrNotFound`, so a DB fault is a 404 (post-authz, so no leak; noted under 1eYt, out of scope). | `CreateWildcardGrant` errors pass through `WriteError` (closure-defined). | Parse becomes `ParseUUIDParam` (details). | The closure is app-owned; apps carry copies (out of scope). |
| 7 | POST /user-accounts/{uuid}/revoke-admin | V | As 6. | As 6. | As 6 (`manage`, nil). | As 6. | As 6 (`DeleteWildcardGrant`). | As 6. | As 6. |
| 8 | POST /user-accounts/{uuid}/assume | V | MW-V; an inline service check `opctx.ActorEntityID` (sudo-unaware) gives 404 when there is no actor (`:683`). D2 turns this into 401. | uuid parse in the handler (bare `ErrInvalidInput`). | `Authorize(assume, &target holder)` AFTER two state reads and an actor check. | Target lookup (404), actor presence (404), then admin-account lookup (404), all before authz. Fix: D2 helper first, then the admin lookup. See finding F4: the admin lookup uses the sudo-unaware `ActorEntityID`, but a sudo-only identity must not be 401 (ordertest asserts that). | JWT signing errors are 500. | 404 to 403; no actor 404 to 401. | `Assume`: `ErrNotFound` to `ErrForbidden`; no actor `ErrNotFound` to `ErrUnauthenticated`. |
| 9 | POST /apps/{uuid}/user-accounts | V | MW-V. Add core RA. | ALL stateless checks run AFTER authz: uuid parse (inside `loadAppByUUIDParam`), decode, `user_uuid` required (`users.user_uuid_required`), `uuid.Parse(user_uuid)` (bare `ErrInvalidInput`). All MOVE before authz (D3), plus a `Validator` for `user_uuid` format with `invalid_uuid`. `roles` is unconstrained free text (no registry, no limit); recorded, not changed. | `h.az.Authorize(update, nil)` is the FIRST statement (wildcard admin only; stays nil-target, not `AuthorizeType`). | After authz: `entityResolver.Resolve` (with `AllowNotFound("app")`, set in `NewAppsHandler` on the shared resolver), then `GetAppByUUID` (type check). Neither filters `archived_at`, so dropping `Resolve` keeps archived-app behavior identical (D3 question answered). No existence leak: authz comes first. | Body `user_uuid` names no account: 404 today (`GetUserAccountByUUID`); D15 proposes 422. Insert is `ON CONFLICT DO NOTHING` (re-assign is a silent no-op that still answers 201 with the request roles; not re-statused). | A non-admin with a bad request now sees 400 (was 403). | Handler-only (queries are called directly), none. |
| 10 | GET /apps/{uuid}/user-accounts | V | As 9. | uuid parse after authz. Moves (D3). | `Authorize(read, nil)` first. | `Resolve` plus `GetAppByUUID` after authz; unknown app 404. | None. The response exposes the internal `user_account_id` (existing followup ZJ5N). | Bad uuid 400 for a non-admin (was 403). | None. |
| 11 | DELETE /apps/{uuid}/user-accounts/{user_account_uuid} | V | As 9. | Both uuid parses run after authz and after the app lookup. Move (D3). | `Authorize(update, nil)` first. | App lookup (404), then `GetUserAccountByUUID` (404). Both after authz, so legitimate; they stay 404. | Non-member: `DELETE` is a no-op and answers 204 (recorded, not re-statused). | Bad uuid 400 for a non-admin. | None. |
| 12 | PUT /apps/{uuid}/user-accounts/{user_account_uuid}/roles | V | As 9. | uuid parses after authz; the decode runs AFTER the user lookup (a state read). All move before authz (D3). `roles` unconstrained. | `Authorize(update, nil)` first. | App lookup (404), user lookup (404) after authz; legitimate. | Non-member: the `UPDATE` matches no row and answers 200 with the request roles (recorded; D15 does not re-status it). | Bad body or uuid 400 for a non-admin. | None. |
| 13 | GET /self | A | MW-A; core RA added (1.2). The handler calls `auth.MustFromContext`, which panics without a `localauth.UserContext`. A direct mount that has `opctx` but no `UserContext` passes core RA and then panics (500 via `Recoverer`); order tests must install both (D14, F18). | None. | None (self by construction: account loaded from `uc.UserAccountID`). | None. | None. | None. | None. |
| 14 | PUT /self | V | MW-V; core RA added. | Decode (bare 400) is first. Blank `given_name`/`family_name` are checked only inside mod-core after its authorize; D5 adds the same checks statelessly in the handler with the `natural_persons.*` codes. A zero-byte body is 400 today (EOF), and `{}` skips the core call and returns the profile. | Inside `NaturalPersonService.UpdateByEntityUUID` (mod-core `main`): `AuthorizeResolve(update)` on the caller's own entity. Source check: `entities.owner_id` defaults to the entity's own id (mod-core migration `0013_entity_ownership.sql`), and `checkGrantOrOwn`'s own arm is not gated by operation, so a non-admin owner is granted `update` on their own entity. | Own entity, exists, so a masked miss cannot be observed. A corporation or service-account holder sending a name passes authz (owner) and then gets `ErrNotFound` (404) from the natural-person lookup. Blank names get 400 first once D5 lands. | Core: blank names 400 after authz (non-HTTP callers). Corporation holder with a name: 404 (post-authz). | Blank names 400 (was stored). | n/a (calls mod-core). Assume session: `uc` is the assumed account and the effective actor is the admin (sudo, `resolver.go:595-607`), so the admin needs `update` on the assumed user's entity; a wildcard admin has it. To be re-verified by 3.3. |
| 15 | GET /self/identities | A | MW-A; core RA added. | None. | None (self). | None. | None. | None. | None. |
| 16 | POST /self/identities/oidc/{provider}/start | V | MW-V; core RA added. | Step-up gate FIRST. The only stateless input is the `return` query value, which `validateReturnPath` checks inside `LinkAuthorizeURL`, coupled to the provider registry lookup; `{provider}` is only lowercased. To run it before step-up (D9), 3.5 needs an exported validator or a split (finding F7). | Step-up (self). | Registry miss or init-failed provider: `ErrUnknownProvider`/`ErrProviderNotAvailable` gives 404. Registry state, not user data, and the caller is authenticated. | Every other `LinkAuthorizeURL` error is mapped to 400, including `randomBase64`/`Sign` failures (these are 500-class; finding F7). `err` is logged, not echoed. | A malformed `return` gives 400 even without a step-up token (was 409). | None. |
| 17 | DELETE /self/identities/{identity_uuid} | V | MW-V; core RA added. | Step-up FIRST, then `uuid.Parse(identity_uuid)` (bare `ErrInvalidInput`). D9 moves the parse first. | Step-up (self). | Inside a serializable tx the last-identity count runs BEFORE the delete, which is the ownership/existence check (`rowsDeleted == 0` gives 404, ZVum literal). Consequence: a bogus `identity_uuid` on an account with exactly one OIDC identity and no password returns 409, not 404. Recorded for 3.5/4.1 (finding F6). Scoped to the caller's own account, so no cross-user oracle. | 409 `users.last_identity` already via `apiresp.Conflict`; 404 `users.identity_not_found` is a ZVum keep. | Malformed uuid 400 without a step-up token (was 409). | None. |
| 18 | POST /self/credential/password | V | MW-V; core RA added. | Step-up FIRST, then decode (bare 400), `new_password` empty (bare 400), length >= 12 (detail `users.password_too_short`). Per D9 the step-up moves after these. The length check is already stateless and before `current_password`. | Step-up (self). | `GetAuthLocal` decides create vs update. | 401 (ZVum literals, `users.bad_credentials`) for a missing or wrong `current_password` (credential check, keep). | Malformed or short password 400 without a step-up token (was 409); unknown JSON fields rejected. | None. |
| 19 | DELETE /self/credential/password | V | MW-V; core RA added. | None besides step-up. Nothing to move. | Step-up (self). | None. | 409 `users.last_identity` when no OIDC identity remains (already `Conflict`). No local credential gives idempotent 204. | None. | None. |
| 20 | POST /self/credential/step-up | V | MW-V; core RA added. | None (no body). | None (self). | None. | None. Always 204 after at least 200 ms; the work runs in a goroutine and the handler waits for it. The code row is inserted with purpose `credential_change`, which the baseline `email_codes` CHECK (`login`, `verify_email` only) rejects, so the code is probably never created (finding F13; source-verified, not run against a database). | None. | None. |
| 21 | POST /self/credential/step-up/verify | V | MW-V; core RA added. | Decode (bare 400), `code` empty (bare 400), both before any DB read. They stay; convert to `server.Decode` plus a detail (3.2). | None (self). | `GetActiveEmailCode` miss gives 401. | 401 for a wrong or missing code (credential, keep). No attempt limit (existing followup GDol). | Unknown JSON fields rejected. | None. |
| 22 | GET /self/ssh-keys | A | MW-A; core RA added. | Lenient `limit`/`offset` parse; the service clamps. | `loadAuthorizedAccount(read)` on the caller's own holder. | Masked: account miss or archived holder gives 403 (no target-0 call yet; D2 adds it); `GetEntityByID` error is wrapped (500, followup 433h). | None. | None. | None. |
| 23 | POST /self/ssh-keys | V | MW-V; core RA added. | Step-up FIRST. Then in `register`: 32 KiB `MaxBytesReader`, decode (bare 400), `public_key` required (bare 400). `sshkey.Parse` and `NormalizeLabel` run in the SERVICE after authz (`ssh_keys.go:133-145`); they are stateless and MOVE before authz (D10). | Step-up (self); `loadAuthorizedAccount(update)`. | Masked as row 22. | Duplicate fingerprint is 409 (`Conflict`, `users.ssh_key_in_use`, already correct, post-authz). | Malformed JSON or key 400 without a step-up token (was 409) or for a caller who may not update (was 403). Over-cap bodies are 400 with `body_too_large` (cap stays 32 KiB). | `Register` returns 400 for a bad key before the account is loaded. |
| 24 | DELETE /self/ssh-keys/{key_uuid} | V | MW-V; core RA added. | Step-up FIRST, then `callerAccountUUID` (an internal parse, 500 on failure), then `key_uuid` parse. The parse moves first (D9). | Step-up (self); `loadAuthorizedAccount(update)`. | Masked account. A key that does not exist or is already revoked for an AUTHORIZED caller is also `ErrForbidden` (403), not 404 (`ssh_keys.go:239`, `:264`). That is a masked miss after authorization, which the rule would make 404; kept by the existing design (finding F5, for the manager). | None. | Malformed uuid 400 without a step-up token. | None. |
| 25 | GET /user-accounts/{uuid}/ssh-keys | V | MW-V; core RA added. | `parseUUIDPathParam("uuid")`. | `loadAuthorizedAccount(read)` on the path account. | Masked 403 (account miss, archived holder). No target-0 call today (D2 adds it); `GetEntityByID` error not masked (433h). | None. | None apart from the target-0 authz call. | None. |
| 26 | POST /user-accounts/{uuid}/ssh-keys | V | MW-V; core RA added. | uuid parse first, then `isSelfTargetingOperatorCall` (an internal parse of the caller uuid), then step-up ONLY when the path account is the caller's own (before decode), then 32 KiB cap, decode, `public_key` required. Key parse after authz (as 23). D9, D10 apply. | Step-up (self-targeting only); `loadAuthorizedAccount(update)`. | Masked as row 25. | 409 duplicate as row 23. | As row 23. | As row 23. |
| 27 | DELETE /user-accounts/{uuid}/ssh-keys/{key_uuid} | V | MW-V; core RA added. | Both parses first (already conformant), then step-up (self-targeting only). Nothing moves. | Step-up (self-targeting only); `loadAuthorizedAccount(update)`. | Masked account; key miss is a masked 403 even for an authorized caller (F5). | None. | Target-0 authz call only. | None. |
| 28 | GET /oidc-config/status | P | None, deliberately. Manifest entry has no middleware at all. | None. | None. | None. | `LoadMergedProviders` error 500. The body includes each provider's raw init `error` string (existing followup 1eYt, out of scope). | None. | None. |
| 29 | POST /oidc-config/confirm | C | `AdminChecker` (admin bearer), else body `setup_token`, else `X-Setup-Token` header (the header is consulted only when the body token is empty). `AdminChecker` error is 500. | Decode runs BEFORE the credential (400 for an unauthenticated caller). D8: check body-independent credentials first, otherwise decode then verify the body token (a documented exception). | The credential. | None. | `normalizeEnabledSet` silently drops unknown provider ids and the request still succeeds (finding F8: the rule would make a value outside the registry 422; flagged, no status chosen). | Admin or header caller with a bad body 400 after 401. Closed by 3.6 (2026-10-11): `authenticateWithBody` evaluates admin then the header token (AdminChecker fault 500), then decodes (400), then verifies the body `setup_token` (401). **Documented exception (D8):** no admin and no header token with a malformed body is 400, not 401, because the body carries the credential; tested by `TestOidccfg_Confirm_Order`. A valid header token now authenticates even when the body also carries a different (wrong) `setup_token`; before, a non-empty body token shadowed the header. Setup-token comparison (`auth.VerifySetupToken`) and the one-shot clearing are unchanged. | n/a. |
| 30 | GET /oidc-config/saved | P | None: CONFIRMED unauthenticated (no check in `Saved`, no middleware on the manifest entry). | None. | None. | None. | Returns `enabled_providers` (merged map of provider id to enabled flag, for every env and DB provider, including disabled ones), `opt_out`, and `saved_at`. No secrets, but it exposes provider ids and enabled state to anyone (finding F17). | None. | None. |
| 31 | POST /oidc-config/providers | C | As 29 (`authorize` helper). | `parseBody` BEFORE the credential: no size cap, an empty body is `{}`, trailing data after the first value is ignored, unknown keys ignored. `id` required and slug pattern run AFTER the credential (fine for class C). D7/D8. | The credential. | `GetOIDCProvider` preflight. | 409 `provider id already exists` (ZVum literal at `:222`; the comment is stale because `apiresp.Conflict` exists); D8 converts. | Over-cap, trailing-data and unknown-key bodies 400; the 409 gains a detail. Closed by 3.6: same credential order and **documented exception** as row 29; `parseBody` now caps at 1 MiB, keeps a zero-byte body as `{}`, and allowlists `setup_token`, `id`, `display_name`, `issuer_url`, `client_id`, `client_secret`, `claim_style`, `scopes`, `enabled`. The 409 is `apiresp.Conflict` with detail `users.oidc_provider_exists`. | n/a. |
| 32 | GET /oidc-config/providers/{id} | C | Credential first. | id pattern runs after the credential, but a mismatch returns 404, not 400 (`TestProviders_Get_InvalidSlug_404` pins it). Becomes 400 `invalid_input` per D8 (3.6, F10). | The credential. | Unknown id (`buildView` not found) gives 404; legitimate after the credential. | None. | Invalid slug 404 to 400. Closed by 3.6 (`TestProviders_Get_InvalidSlug_400`). Credential then id pattern, as row 34. | n/a. |
| 33 | PUT /oidc-config/providers/{id} | C | As 29. | `parseBody` before the credential (as 31); id pattern (400) after. Present keys with a wrong JSON type are silently treated as absent by the merge helpers (recorded). | The credential. | None: PUT is an upsert for any slug-valid id (no 404, no 409). | None. | As 31. Closed by 3.6: same credential order and **documented exception** as row 29; an empty (zero-byte) body is still accepted. A body `id` is accepted and ignored (the path id wins), as before. Wrong-typed fields are still treated as absent by the merge helpers (recorded, not changed). | n/a. |
| 34 | DELETE /oidc-config/providers/{id} | C | Credential first. | id pattern (400) after the credential. Already conformant (confirmed by 3.6; `TestOidccfg_GetDelete_CredentialThenIDPattern`). | The credential. | `DeleteOIDCProvider` ignores the row count: idempotent 204 whether or not a row existed. | None. | None. | n/a. |
| 35 | GET /oidc-config/setup-token | P (loopback-gated) | `isLoopbackAddr(r.RemoteAddr)` else 403 (behind a proxy this passes for everyone, existing note under 1eYt). | None. | Loopback check. | Confirmed state or no token gives 404. | None. | None. | n/a. |
| 36 | POST /auth/register | P | None (public). | Decode, then email (trim, lowercase) non-empty, password >= 12, given_name and family_name (trimmed) non-empty: all stateless and already first, as literals with `users.*` details. No email format or length check (recorded, not required by the rule). | None. | None. | 409 `users.email_taken` (a literal with details; works because `isPgError` receives the unwrapped error). The email-enumeration oracle is accepted (1eYt). 500 literals. | Unknown JSON fields rejected; decode 400 gains details. | None. The `sendEmailCode` goroutine uses the request context after the response (finding F14). |
| 37 | POST /auth/login | P | None. | Decode, then email and password required (literal, bare message). Already first. | The credential (password). | `GetUserAccountByEmail` miss runs a dummy verify (timing-equalized). | 401 credential failure (keep). | Unknown JSON fields rejected. | None. |
| 38 | POST /auth/anonymous | P | None. | Decode, `device_id` required (literal with detail). No length or format limit (recorded). | None. | None. | 500 literals. Responds 201. | Unknown JSON fields rejected. | None. |
| 39 | POST /auth/email-code/request | P | None. | Decode only. `purpose` is any string (default `login`): an unknown value is silently dropped inside the goroutine when the `email_codes` CHECK rejects the insert. An enum check would be a stateless 400 but a new behavior (finding F9, not chosen). | None. | Account lookup is silent (anti-enumeration). | Always 204 after at least 200 ms. | Unknown JSON fields rejected. | None. |
| 40 | POST /auth/email-code/verify | P | None. | Decode, `email` and `code` required (literal). `purpose` free text. | The credential (code). | Unknown email or code gives 401. | 401 credential failure (keep); `verify_email` marks the address verified (204). | Unknown JSON fields rejected. | None. |
| 41 | POST /auth/password-reset/request | P | None. | Decode only. | None. | Silent. | Always 204 (not 202). The goroutine uses `r.Context()` after the handler returns, so the request context is already canceled and the real work probably fails (finding F14). | Unknown JSON fields rejected. | None. |
| 42 | POST /auth/password-reset/confirm | P | None. | Decode, `token` required, `password` >= 12 (literal with detail). FIELD MISMATCH: the server reads `password`, but `@moduleforge/users-gui` `auth.resetPassword` sends `new_password`, so the field is dropped today and every GUI reset gets 400 `users.password_too_short`. Under D6 the unknown field would instead be rejected with `invalid_json_body` (finding F1; this is the halt case D6 names). | The credential (reset token). | Token miss gives 401. | 401 credential failure (keep). | D6: the GUI request keeps failing with a different detail unless the GUI sends `password` (or the server accepts `new_password`). | None. |
| 43 | GET /auth/providers | P | None. | None. | None. | None. | None. | None. | None. |
| 44 | GET /auth/oidc/{provider}/start | P | None. | `{provider}` lowercased only. `return` (validated inside `AuthorizeURL`) and `mode=test` are query values. The 400 echoes `err.Error()`. | None. | Unknown or init-failed provider gives 404 (public provider list, not an oracle). | Any other `AuthorizeURL` error is a 400 echoing the error text, including `Sign`/`rand` failures (500-class). | The 400 message stops echoing internal text (D12). | None. |
| 45 | GET /auth/oidc/{provider}/callback | P (state cookie) | State cookie plus signed state token. | Provider availability (state-dependent 404) runs BEFORE the stateless `error`/`code`/`state`/cookie 400s. Kept: public browser redirect flow, and init-failed providers are indistinguishable from unknown by design. | State validation. | Unknown or unavailable provider gives 404. | `redirectToFrontendError` redirects for most failures; 500 literals for internal faults. | 400/404/500 bodies become `apiresp` envelopes (message text changes). | None. |
| D1 | DELETE /v1/assume | A | Dev-server only: `main.go:664` mounts `assumeHandler.EndAssume` inside the authenticated `/v1` group. No registrar or manifest entry, so generated apps do not serve it. | None (no body). | None. | None. | Always 204. | None. | None. |
| D2 | GET /healthz | P | None. | None. | None. | None. | Always 200. | None. | None. |
| D3 | GET /readyz | P | None. | None. | None. | `pool.Ping`. | 503 with the raw ping error in the body (`"db": err.Error()`), on an unauthenticated route (finding F16). | None. | None. |

Route count: 45 registrar routes (rows 1-45: 12 account/apps, 2 self, 1+6 identities, 3 ssh self, 3 ssh operator, 8 oidc-config, 10 auth) plus the three D-rows. Source agrees with the validation command (see the report in the task document).

### Decisions D1-D15 against source

| Decision | Verdict | Notes |
|---|---|---|
| D1 | Confirmed | All 8 registrars exist as named. No non-test code in `api/internal/handlers` calls `opctx.ActorEntityID` or `RequireAuthenticated` (grep, no hits). The only inline check is service-level (`user_accounts.go:683`). Manifest entries for the 8 registrars list `requireAuth`; the `/v1/oidc-config` entry has no middleware and `/v1/auth` has only `requireOIDCConfirmed`. Generated mains mount each registrar in its own `r.Group`. Add (F18): handlers call `MustFromContext`, so core RA alone does not make a direct mount safe. |
| D2 | Confirmed, with two notes | `user_accounts.uuid` is the account's own uuid, not an `entities.uuid` (`0100_baseline.sql:25-34`). Target 0 is denied quietly for a non-admin (see D2 premise below). The handler miss branches are `user_accounts.go:152` (dead, see row 3) and `assume.go:70`. Note F4 on `Assume`'s sudo-unaware actor lookup. `loadAuthorizedAccount` details confirmed (archived-holder masking, unwrapped `GetEntityByID` error). `UserAccountService.LoadByUUID` is unguarded and has no non-test caller. |
| D3 | Confirmed | `AllowNotFound("app")` is at `apps.go:52`. `GetAppByUUID` and `Resolve` both ignore `archived_at`, so dropping `Resolve` preserves archived-app behavior. `roles` is unconstrained. |
| D4 | Confirmed | Special case at `user_accounts.go:63-76`; `parseUUIDParam` at `:259`. |
| D5 | Confirmed | See row 14 for the ownership arm, corporation holder and assume session. |
| D6 | Count confirmed (16 callers: apps 2, anonymous 1, emailcode 2, login 1, register 1, reset 2, identities 2, oidc_config 1, self 1, ssh_keys 1, user_accounts 2). All reject an empty body (`json.Decoder` returns EOF). The client grep found a halt-case field mismatch: F1 (`new_password`), plus the known `role` vs `roles` (5.1). |
| D7 | Confirmed | `parseBody` is at `oidc_providers.go:418-431`; no cap, EOF is `{}`, trailing data ignored. The GUI sends exactly these keys: `display_name`, `issuer_url`, `client_id`, `client_secret`, `claim_style`, `scopes`, `enabled`, plus `id` (POST) and `setup_token`. The allowlist must also take `id` and `setup_token`; whether PUT may carry `id` is for 3.6 to decide (the GUI's PUT does not send it). |
| D8 | Confirmed with one correction | Order and sites as stated. The ZVum 409 is at `:222`, and `EXPECTED_CARVEOUTS=4` is in `check-server-error-literals.sh`. Correction: `GET /oidc-config/providers/{id}` answers 404 for a bad id pattern, which D8's "id pattern (400)" would change (F10). |
| D9 | Confirmed with one gap | `StartLink` has a stateless input (`return`) but it is validated inside `LinkAuthorizeURL` (F7). |
| D10 | Confirmed | `Register` order is `loadAuthorizedAccount` (`:133`), `Parse` (`:138`), `NormalizeLabel` (`:142`). |
| D11 | Confirmed | Manifest order is `requireOIDCConfirmed`, `requireAuth`, `requireVerifiedEmail`; the first answers before 401 and the third before any handler 400. |
| D12 | Counts confirmed, two flags | 66 in `handlers/auth` (the "65" in the reserved slot is wrong), 4 ZVum literals in `handlers/` top level, 6 in `internal/auth/middleware.go`: 76 literal calls. One more in-package call exists (`internal/server/middleware.go:56`, the panic `Recoverer`) that neither the planner nor the lint counts. Flag F3: D12 says required-field 400s convert but literals whose message a client displays stay, and the GUI `register-form.tsx` shows `err.message` verbatim for exactly the register 400s and the 409. |
| D13 | Confirmed | Non-test call counts: `writeAuthzError` 4 (apps), `writeServiceError` 9, `writeCoreServiceErr` 1, `parseUUIDParam` 5, `parseUUIDPathParam` 5. `user_accounts_authz_test.go` references `writeServiceError` and `writeAuthzError` by name (`TestWriteServiceError_Mapping`, `TestWriteAuthzError_Mapping`), so those tests change with the wrappers. |
| D14 | Confirmed, with a harness note | `UserAccountsHandler` takes the concrete `*UserAccountService`, so the real service plus fake `db.Querier`/`coredb.Querier` works. `AssumeHandler` and `SSHKeysHandler` take interfaces. `IdentitiesHandler` holds a concrete `*pgxpool.Pool` and `*db.Queries`, and `AppsHandler`'s `coreQ` is a concrete `*coredb.Queries`, so their order tests need a DBTX-level fake (as `apps_authz_test.go` does) rather than a Querier fake (F11). |
| D15 | Mostly confirmed | The `PUT` unique-violation 500 is real, and so is the `POST /user-accounts` one (row 2). `Unlink` and ssh-keys duplicate 409s already exist (`apiresp.Conflict`). `StartLink`'s 400 is the stateless `return` check plus misclassified internal errors (row 16). Non-member `Remove`/`UpdateRoles` succeed silently (rows 11-12). |

D2 premise, `Authorizer.Authorize` (`api/internal/authz/authz.go:220`): a wildcard admin short-circuits in `authorizePrelude` and returns nil for any target, including 0 (harmless: the result is discarded). Any other actor reaches `checkGrantOrOwn` with target 0. That query's grant arm needs `grants.target_id = 0` or a `TargetChain` containing 0, and its ownership arm needs `entities.id = 0`; entity ids are `BIGSERIAL` (start at 1, `0008_entities.sql`), so none can match and the result is `ErrForbidden` with no panic and no log line. An unauthenticated context gives `ErrUnauthenticated` (before target 0 matters). The extra call costs one DB round trip for a non-admin. Common rule 5 holds as written.

### Corrections to the preliminary table

1. Row 2: `409 users.email_taken` never fires on `POST /user-accounts`; a duplicate email is 500 (wrapped unique violation not matched). Added task 4.1.
2. Row 3: the handler's `pgx.ErrNoRows` branch is dead (the service returns `coreservice.ErrNotFound`).
3. Row 17: the 409 last-identity check precedes the 404 identity lookup. Added 4.1.
4. Row 19: confirmed 409 `users.last_identity`; "verify" cleared.
5. Row 20: nothing to verify; confirmed no checks.
6. Row 30: confirmed unauthenticated; "none seen" replaced by the confirmed state.
7. Row 32: a bad id pattern returns 404, not a pattern-check 400; added 3.6.
8. Row 33: PUT is an upsert; "verify" cleared.
9. Rows 39 and 41: the success status is 204, not 202.
10. Row 44: the 400 is the stateless `return` check mixed with the registry lookup.
11. Row D2 split into D2 (`/healthz`) and D3 (`/readyz`) so each route has a row. Labels D1-D3 in the route table are not the planning decisions.
12. D12: 66 literals in `handlers/auth` (not "65"); matches 66 + 4 + 6 below.

### Literal `server.Error` / `server.ErrorWithDetails` call sites (D12)

Searched all of `api/` (`grep -rnE 'server\.(Error|ErrorWithDetails)\('` over every `.go` file, test files included, excluding `server/respond.go`); no site exists in `gui/`, `model/`, or outside `api/`. Test files contain no such call (the test shim has its own `server_Error` copy, which is not a call to the package).

| File | Exact count | Planner's | Matches |
|---|---|---|---|
| `internal/handlers/auth/anonymous.go` | 4 | 4 | yes |
| `internal/handlers/auth/emailcode.go` | 10 | 10 | yes |
| `internal/handlers/auth/login.go` | 7 | 7 | yes |
| `internal/handlers/auth/oidc.go` | 21 | 21 | yes |
| `internal/handlers/auth/register.go` | 16 | 16 | yes |
| `internal/handlers/auth/reset.go` | 8 | 8 | yes |
| `internal/handlers/auth/` total | 66 | 66 | yes |
| `internal/handlers/identities.go` | 3 | 4 together | yes |
| `internal/handlers/oidc_providers.go` | 1 | (in the 4) | yes |
| `internal/handlers/` total | 4 | 4 | yes |
| `internal/auth/middleware.go` | 6 | 6 | yes |
| Total | 76 | 66 + 4 + 6 = 76 | yes |

Outside the count: `internal/server/middleware.go:56` calls the in-package `Error(...)` from the panic `Recoverer` (500 `internal_error`, "internal server error"). It is not a `server.Error(` call site and the lint does not see it. Disposition: keep (it has no request-error to wrap; the code matches apiresp's `internal_error`, only the message differs).

Disposition summary: convert 56 (auth 55, providers 1), keep 20 (auth 11, identities 3, middleware 6). "Convert" means the replacement keeps status, top-level code and details (D12); "Keep" gets a one-line reason comment in 3.7.

| Site | Status / code | Disposition | Reason |
|---|---|---|---|
| `auth/anonymous.go:26` | 400 `invalid_input` "invalid JSON body" | convert | `server.Decode` (D6). |
| `auth/anonymous.go:31` | 400 with `users.device_id_required` | convert | `apiresp.InvalidInput(FieldError{...})`, same detail. Machine client, no displayed message. |
| `auth/anonymous.go:41` | 500 | convert | `WriteError` with a wrapped error. |
| `auth/anonymous.go:60` | 500 | convert | as above. |
| `auth/emailcode.go:38`, `:124` | 400 decode | convert | `server.Decode`. |
| `auth/emailcode.go:130` | 400 "email and code are required" | convert | `InvalidInput` with new detail codes (recorded as a wire change). |
| `auth/emailcode.go:139`, `:153`, `:163` | 401 "invalid or expired code" | keep | Credential failure with a specific message (the GUI tests assert it, `api.test.ts`, `verify-email-page.test.tsx`). |
| `auth/emailcode.go:144`, `:158`, `:176`, `:194` | 500 | convert | `WriteError`. |
| `auth/login.go:30` | 400 decode | convert | `server.Decode`. |
| `auth/login.go:36` | 400 "email and password are required" | convert | `InvalidInput` with new detail codes. |
| `auth/login.go:43`, `:70`, `:81` | 500 | convert | `WriteError`. |
| `auth/login.go:63`, `:74` | 401 "invalid email or password" | keep | Credential failure with a specific message; `api.ts` documents this 401 as wrong credentials. |
| `auth/oidc.go:154`, `:182`, `:210` | 404 "unknown provider" | convert | Plain 404 to `apiresp.ErrNotFound` (no client asserts the text; the callback is a browser page). |
| `auth/oidc.go:158` | 400 echoes `err.Error()` | convert | `InvalidInput` with a `return` field detail; stops echoing internal text. |
| `auth/oidc.go:195`, `:201`, `:217` | 400 missing code/state, missing state cookie, invalid state | convert | `InvalidInput` with new detail codes; browser-facing, no client reads the text. |
| `auth/oidc.go:273`, `:280`, `:288`, `:298`, `:304`, `:336`, `:357`, `:409`, `:423`, `:457`, `:472`, `:483`, `:489`, `:507` | 500 | convert | `WriteError` (14 sites). |
| `auth/register.go:160` | 400 decode | convert | `server.Decode`. |
| `auth/register.go:167`, `:172`, `:177`, `:182` | 400 with `users.email_required`, `users.password_too_short`, `users.given_name_required`, `users.family_name_required` | keep (flag F3) | `register-form.tsx` shows `err.message`; converting changes the text to "one or more fields are invalid". Alternative: convert and have task 5.1 render `details[0].message`. Manager to choose. |
| `auth/register.go:190`, `:197`, `:209`, `:218`, `:225`, `:233`, `:244`, `:267`, `:276`, `:282` | 500 | convert | `WriteError` (10 sites). |
| `auth/register.go:258` | 409 `conflict` with `users.email_taken` | keep (flag F3) | Message is displayed by `register-form.tsx`; `apiresp.Conflict` would replace it with a generic text. Same alternative as above. |
| `auth/reset.go:38`, `:96` | 400 decode | convert | `server.Decode`. |
| `auth/reset.go:101` | 400 "token is required" | convert | `InvalidInput` with a new detail code. |
| `auth/reset.go:105` | 400 with `users.password_too_short` | convert | `InvalidInput` with the same detail. |
| `auth/reset.go:116` | 401 "invalid or expired reset token" | keep | Credential failure with a specific message (`api.ts`, `reset-password-page.tsx`). |
| `auth/reset.go:121`, `:128`, `:137` | 500 | convert | `WriteError`. |
| `handlers/identities.go:318` | 404 with `users.identity_not_found` | keep | ZVum carve-out: apiresp has no detail-carrying 404. |
| `handlers/identities.go:397`, `:415` | 401 with `users.bad_credentials` | keep | ZVum carve-outs: apiresp has no detail-carrying 401; credential failure. |
| `handlers/oidc_providers.go:222` | 409 `conflict` "provider id already exists; use PUT to update" | convert | `apiresp.Conflict(FieldError{Field: "id", Code: "users.oidc_provider_exists", ...})` (D8); the stale comment says no such constructor. `EXPECTED_CARVEOUTS` drops to 3. The GUI shows the server message, so the actionable text is lost; 3.6 should keep it in the detail message. |
| `internal/auth/middleware.go:80`, `:82`, `:85`, `:87` | 401 `unauthorized` | keep | A different code from apiresp's `unauthenticated`; messages asserted in `middleware_test.go`. |
| `internal/auth/middleware.go:94`, `:97` | 500 `internal_error` with specific messages | keep | The messages are asserted in `middleware_test.go` and `anonymous_actor_test.go`; D12 keeps every literal in this file. |

### Findings and flags for the manager

1. **F1 (D6 halt case).** `@moduleforge/users-gui` `auth.resetPassword` sends `new_password`; `POST /v1/auth/password-reset/confirm` reads `password`. The GUI reset flow is broken today, and D6 would turn the silent drop into an `invalid_json_body` 400. Decision needed: fix the GUI field (task 5.1 scope), or accept `new_password` server-side. Filed as a followup.
2. **F2.** `POST /user-accounts` never returns 409 `users.email_taken` (rows 2, 4). `isPgUniqueViolation` type-asserts a wrapped error. Fix with `errors.As`; belongs to 3.3/4.1. Filed as a followup.
3. **F3 (D12 tension).** The register literals (5 sites) are displayed verbatim by the GUI; D12's convert rule and keep rule disagree for them. Table above recommends keep.
4. **F4.** `Assume` resolves the admin account with `opctx.ActorEntityID` (sudo-unaware) while the authorizer uses the effective actor. D2's "missing actor gives 401" must not turn a sudo-only identity into a 401 (ordertest asserts a sudo-only actor is not 401). Task 2.1 should use the effective actor for that lookup; confirm.
5. **F5.** A key miss on `RevokeSelf`/`RevokeForAccount` for an authorized caller is a masked 403, where the rule gives 404 after authorization. Kept by the existing ssh-keys design (followup tbmN); the manager should confirm that D2's "keep its archived-holder masking" also means keeping this.
6. **F6.** `Unlink` answers 409 before 404 for a bogus `identity_uuid` on a single-identity account.
7. **F7.** `StartLink`: the `return` validation cannot run before step-up without exporting `validateReturnPath` (or splitting `LinkAuthorizeURL`); internal `Sign`/`rand` errors are reported as 400.
8. **F8.** `POST /oidc-config/confirm` silently drops unknown provider ids; the rule says a value outside a registry is 422. No status chosen.
9. **F9.** Unconstrained stateless inputs not covered by the plan: `purpose` on email-code request/verify, email format and length on register/create, `device_id` on anonymous, `roles` on app membership. Recorded only.
10. **F10.** `GET /oidc-config/providers/{id}` returns 404 for a bad id; D8 (and the common rule) say 400. `TestProviders_Get_InvalidSlug_404` must change.
11. **F11.** Order tests for identities and apps need a DBTX-level fake (concrete `*pgxpool.Pool`, `*db.Queries`, `*coredb.Queries` in those handlers).
12. **F13.** Step-up code insert uses purpose `credential_change`, but the baseline CHECK allows only `login` and `verify_email`; the insert probably fails silently (source-verified, not run). Filed as a followup.
13. **F14.** `PasswordResetRequest` and `Register` (verify-email send) start a goroutine on `r.Context()` and return, so the context is canceled. Filed as a followup.
14. **F15.** `@moduleforge/users-gui` `userAccounts.grantAdmin`/`revokeAdmin` call `/v1/user-accounts/{uuid}/admin` (server routes are `/grant-admin`, `/revoke-admin`), and `userAccounts.audit` calls a removed `/v1/user-accounts/{uuid}/audit`. Filed as a followup.
15. **F16.** `GET /readyz` returns the raw DB ping error text unauthenticated. Filed as a followup.
16. **F17.** `GET /oidc-config/saved` is unauthenticated and lists every provider id with its enabled flag; recorded for 4.4 and the docs, no change planned.
17. **F18.** Handlers call `auth.MustFromContext`; core RA does not install a `UserContext`, so order-test wiring must supply both (D14's `orderUserContext` shim does).

