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

## Preliminary route table

The "today" columns describe base `a2d704c`. Route classes: A = authenticated (registrar group carries `requireAuth`), V = authenticated plus verified email, C = credential-authenticated, P = public. "nil" means a nil-target `Authorize` (wildcard only); "type" means `AuthorizeType`.

| # | Route | Handler | Class | Stateless checks today | Authorize | Resolve / existence today | Post-authz errors | Planned (task) |
|---|---|---|---|---|---|---|---|---|
| 1 | GET /user-accounts | `UserAccountsHandler.List` | V | limit/offset clamp silently | type `list` | none | none | 1.2 |
| 2 | POST /user-accounts | `.Create` | V | decode, then `AuthorizeCreate` on failure (403-before-400); required fields and password length AFTER authz (service) | type `create` | none | 409 `users.email_taken` | 1.2, 3.2 (D4), 3.3 |
| 3 | GET /user-accounts/{uuid} | `.Get` | V | uuid parse | entity `read` on holder | lookup before authz, 404 leak | none | 2.1, 3.3 |
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
| 17 | DELETE /self/identities/{identity_uuid} | `.Unlink` | V | step-up before uuid parse | step-up | own identity lookup gives 404 (ZVum literal) | 409 last identity | 3.5 |
| 18 | POST /self/credential/password | `.SetPassword` | V | step-up before decode; length | step-up | none | 401 current_password (ZVum) | 3.5 |
| 19 | DELETE /self/credential/password | `.RemovePassword` | V | step-up | step-up | none | verify (409 last credential?) | 3.5, 4.1 |
| 20 | POST /self/credential/step-up | `.StepUpRequest` | V | verify | none (self) | none | none | 1.1 verify |
| 21 | POST /self/credential/step-up/verify | `.StepUpVerify` | V | decode; code required | none (self) | none | 401 bad code (credential) | 3.2 |
| 22 | GET /self/ssh-keys | `SSHKeysHandler.ListSelf` | A | pagination clamp | `read` on own holder | masked | none | 1.2 |
| 23 | POST /self/ssh-keys | `.RegisterSelf` | V | step-up FIRST; 32 KiB decode; public_key required; key parse AFTER authz | step-up; `update` | masked | 409 duplicate | 3.2, 3.5 (D9, D10) |
| 24 | DELETE /self/ssh-keys/{key_uuid} | `.RevokeSelf` | V | step-up before key_uuid parse | step-up; `update` | masked 403 | none | 3.5 |
| 25 | GET /user-accounts/{uuid}/ssh-keys | `.ListForAccount` | V | uuid parse | `read` | masked 403 (no target-0 call) | none | 2.1 |
| 26 | POST /user-accounts/{uuid}/ssh-keys | `.RegisterForAccount` | V | uuid parse; step-up (self) before decode; key parse AFTER authz | step-up (self); `update` | masked | 409 duplicate | 2.1, 3.5 |
| 27 | DELETE /user-accounts/{uuid}/ssh-keys/{key_uuid} | `.RevokeForAccount` | V | both parses; step-up (self) | step-up (self); `update` | masked | none | 2.1, 3.5 |
| 28 | GET /oidc-config/status | `OIDCConfigHandler.Status` | P | none | none | none | none | 4.4 |
| 29 | POST /oidc-config/confirm | `.Confirm` | C | decode BEFORE credential | admin / header / body token | none | none | 3.6 (D8) |
| 30 | GET /oidc-config/saved | `.Saved` | P (verify) | none | none seen | none | none | 1.1 verify, 4.4 |
| 31 | POST /oidc-config/providers | `ProvidersHandler.Create` | C | map decode (no cap, empty ok) BEFORE credential; id required/pattern after | credential | none | 409 exists (ZVum literal) | 3.6 (D7, D8) |
| 32 | GET /oidc-config/providers/{id} | `.Get` | C | id after credential | credential | unknown id gives 404 | none | 4.4 |
| 33 | PUT /oidc-config/providers/{id} | `.Update` | C | map decode BEFORE credential; id pattern after | credential | none | verify | 3.6 |
| 34 | DELETE /oidc-config/providers/{id} | `.Revert` | C | id pattern after credential | credential | none | idempotent 204 | 4.4 |
| 35 | GET /oidc-config/setup-token | `.SetupToken` | P (loopback) | none | loopback check gives 403 | none | 404 when none | 4.4 |
| 36 | POST /auth/register | `auth.Handler.Register` | P | decode + required fields (literals) | none | none | 409 email taken (literal) | 3.2, 3.7 |
| 37 | POST /auth/login | `.Login` | P | decode + required (literal) | credential | none | 401 credential (keep) | 3.2, 3.7 |
| 38 | POST /auth/anonymous | `.Anonymous` | P | decode + device_id | none | none | none | 3.2, 3.7 |
| 39 | POST /auth/email-code/request | `.EmailCodeRequest` | P | decode | none | none | always 202 (anti-enumeration) | 3.2, 3.7 |
| 40 | POST /auth/email-code/verify | `.EmailCodeVerify` | P | decode + required | credential (code) | none | 401 credential (keep) | 3.2, 3.7 |
| 41 | POST /auth/password-reset/request | `.PasswordResetRequest` | P | decode | none | none | always 202 | 3.2, 3.7 |
| 42 | POST /auth/password-reset/confirm | `.PasswordResetConfirm` | P | decode + token + length | credential (token) | none | 401 credential (keep) | 3.2, 3.7 |
| 43 | GET /auth/providers | `OIDCHandler.ListProviders` | P | none | none | none | none | 4.4 |
| 44 | GET /auth/oidc/{provider}/start | `.Start` | P | provider | none | unknown gives 404 (literal) | 400 echoes `err.Error()` (literal) | 3.7 |
| 45 | GET /auth/oidc/{provider}/callback | `.Callback` | P (state cookie) | code/state, cookie | state validation | unknown provider gives 404 | redirects; 500 literals | 3.7 |
| D1 | DELETE /v1/assume | `AssumeHandler.EndAssume` | A | none | none | none | none | dev server `main.go` only; no registrar or manifest entry: record only |
| D2 | GET /healthz, GET /readyz | `health.go` | P | none | none | none | none | record only |

Tests that depend on today's behavior (task 1.1 completes this list):

- `api/internal/handlers/user_accounts_authz_test.go` covers the 403-before-400 `Create` behavior through a hand-copied shim (followup N9AG), plus the 404 `Get` mapping.
- `api/internal/handlers/apps_authz_test.go` asserts authorize-before-resolve and 404 on an unknown app.
- `api/internal/handlers/oidc_config_test.go:585` (`TestConfirm_BadJSON`) and `oidc_providers_test.go` cover body and credential ordering.
- `api/internal/handlers/identities_stepup_test.go` and `ssh_keys_test.go` assert step-up before body/UUID errors in places.
- `api/internal/service/user_accounts_*_test.go`, `ssh_keys_test.go` and the `-tags=integration` suites in `api/internal/authz/` may assert `ErrNotFound` on misses.

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

Task 1.1 appends the authoritative five-axis table here: one row per route, including the D-rows, plus the literal-site classification table.
