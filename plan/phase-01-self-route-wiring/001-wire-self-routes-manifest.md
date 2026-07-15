# Wire Self Routes Into The Manifest Codegen Path

## Purpose and scope

Make `GET/PUT /v1/self` flow through the manifest-driven mfgen codegen path so any
app composing mod-users receives `/self` routes in its generated server. This
task delivers the complete code + manifest change plus the dev-server comment
reconciliation. It does **not** rewrite `SelfHandler.Get`/`Put` (they already
work) and does **not** touch anything outside the `mod-users/` tree (no `mfgen/`,
no `app-mftodo/`, no sibling modules). The mfgen schema/generator are used as-is;
no generator change is needed (verified — see References).

No standard skill covers manifest wiring; follow the `## Procedure` below.

## Requirements

Three edits within `mod-users/`, all consistent with each other:

### 1. New Go function — `api/internal/handlers/self_routes.go`

Add an exported `RegisterSelfRoutes` that mounts the two self routes and applies
the verified-email gate to `PUT` only:

```go
package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// RegisterSelfRoutes mounts GET/PUT /self onto r. The caller supplies the /v1
// prefix and the group-level middleware (requireOIDCConfirmed, requireAuth)
// BEFORE calling this function; RegisterSelfRoutes adds neither a prefix nor
// those gates itself.
//
// The email-verification gate is deliberately NOT applied at the group level.
// The caller passes requireVerifiedEmail in, and this function applies it to
// PUT /self only, via a nested r.Group — keeping GET /self reachable to accounts
// whose email is not yet verified (the GUI renders the "verify your email" page
// from it) while PUT /self stays restricted to verified accounts.
func RegisterSelfRoutes(r chi.Router, h *SelfHandler, requireVerifiedEmail func(http.Handler) http.Handler) {
	// GET /self bypasses the email-verification gate.
	r.Get("/self", h.Get)

	// PUT /self requires a verified email.
	r.Group(func(r chi.Router) {
		r.Use(requireVerifiedEmail)
		r.Put("/self", h.Put)
	})
}
```

- The parameter type is `func(http.Handler) http.Handler` — exactly the return
  type of `auth.NewRequireVerifiedEmail()` (`api/auth/auth.go:92`) and the type of
  the generated `requireVerifiedEmail` variable. Do not import `chi` solely for a
  `chi.Middleware` alias; the plain func type is clearer and `chi` is already
  imported for `chi.Router`.
- Model the doc-comment and structure on the sibling
  `api/internal/handlers/account_routes.go` (`RegisterAccountRoutes`) —
  caller-owns-prefix-and-middleware convention.

### 2. Manifest wiring — `moduleforge.module.yaml`

**(a)** Add a `selfHandler` entry under `provides.services` (place it near the
other handler entries, e.g. after `usersHandler`):

```yaml
    # selfHandler — /v1/self profile read/update handler. Composite identity
    # endpoint: core-module owns entity data (given/family name) via
    # coreServices.Entity.GetSelf; users-module owns the account row (email,
    # timestamps, uuid). Depends on both query sets plus the core services aggregate.
    - name: selfHandler
      type: "*handlers.SelfHandler"
      constructor: handlers.NewSelfHandler
      args:
        - queries:usersdb            # *db.Queries
        - queries:coredb             # *coredb.Queries
        - service:coreServices       # *coreservice.Services (from mod-core)
```

`NewSelfHandler(q *db.Queries, coreQ *coredb.Queries, coreSvcs *coreservice.Services)`
— arg order and kinds match: `queries:usersdb` → `usersdb.New(pool)`,
`queries:coredb` → `coredb.New(pool)`, `service:coreServices` → the `coreServices`
var. This mirrors the existing sibling handler entries (all handlers live under
`provides.services` and are referenced by routes via `handler:`).

**(b)** Add a `/v1` route entry under `provides.routes` (place it after the
`RegisterAccountRoutes` `/v1` entry):

```yaml
    # /v1/self — authenticated profile read/update. GET /self is reachable to
    # accounts with UNVERIFIED email (so the GUI can render the "verify your
    # email" page); PUT /self requires a verified email. The split is expressed
    # by giving this group only requireOIDCConfirmed + requireAuth at the group
    # level and passing requireVerifiedEmail into RegisterSelfRoutes as an expr
    # register-arg, so the handler applies it internally to just the PUT route via
    # a nested r.Group. See docs/architecture.md and AGENTS.md manifest conventions.
    - prefix: /v1
      handler: selfHandler
      register: handlers.RegisterSelfRoutes
      register_args:
        - expr:requireVerifiedEmail   # in-scope chi.Middleware var; applied to PUT only, inside the handler
      middleware:
        - requireOIDCConfirmed
        - requireAuth
```

- `middleware:` MUST list only `requireOIDCConfirmed` and `requireAuth` — NOT
  `requireVerifiedEmail`. Adding the verified gate here would (incorrectly) gate
  `GET /self` too.
- `register_args` uses `expr:requireVerifiedEmail` (emitted verbatim by mfgen;
  resolves to the generated `requireVerifiedEmail := auth.NewRequireVerifiedEmail()`
  local var). Do not use `service:`/`method:` forms — `expr:` is correct here.

**(c)** Add `coreServices` under `requires.services` (mod-users already requires
sibling core services `naturalPersonService` and `typeResolver` the same way):

```yaml
    - name: coreServices           # *coreservice.Services from core-module
```

### 3. Dev-server comment reconciliation — `api/cmd/server/main.go`

**Decision (explicit, do not deviate):** LEAVE the working hand-written
`GET/PUT /self` block (currently ~L515–525) in place. This file is a
NON-generated standalone dev server that mfgen does not regenerate; deleting the
routes would break it. Add a brief reference comment immediately above the
`r.Get("/self", selfHandler.Get)` line noting that `GET/PUT /self` are now
expressed in the manifest via the `handlers.RegisterSelfRoutes` register entry
(`register_args: [expr:requireVerifiedEmail]`), and that this hand-written block
mirrors that generated wiring — consistent with the existing `TODO(generated)`
markers on the sibling auth/oidc-config/account route groups (~L491, L506, L566).

- Do NOT alter the Phase-4 identity sub-routes (`/self/identities`,
  `/self/credential/*`, `/self/credential/step-up*`) — they are served by
  `identitiesHandler`, remain hand-written, and are out of scope.
- The other `TODO(generated)` markers (auth/oidc-config/account) are out of scope
  for this task — do not touch them.

## Validation

- `make build.api` succeeds — `RegisterSelfRoutes` compiles. (It is an exported
  function not yet called within mod-users' own build; Go does not flag exported
  functions as unused, so this is expected and fine.)
- `make lint` (or `make lint.api`) passes for the new/edited Go files.
- `grep -n "RegisterSelfRoutes" moduleforge.module.yaml api/internal/handlers/self_routes.go`
  returns the manifest register entry and the function definition.
- `grep -n "expr:requireVerifiedEmail" moduleforge.module.yaml` returns exactly the
  new register_args line.
- Confirm the new `/v1` route entry's `middleware:` list does **not** contain
  `requireVerifiedEmail` (only `requireOIDCConfirmed`, `requireAuth`).
- Confirm `selfHandler` appears once under `provides.services` and `coreServices`
  appears once under `requires.services`.
- `api/cmd/server/main.go` still builds and the hand-written `GET/PUT /self` block
  is intact with the added reference comment.
- **Verification-only (optional, do NOT commit its output):** if you want an
  end-to-end sanity check, run mfgen against a scratch copy of the app manifest and
  diff the generated `/v1` block against the expected snippet in the References
  notes. Do NOT modify `app-mftodo/` or `mfgen/`; regenerating app-mftodo is a
  separate agent's responsibility.
- **Scope guard:** `git status` shows changes only under `mod-users/` — no edits to
  `mfgen/`, `app-mftodo/`, or any sibling module.

## Metadata

architectural_impact: true

## Assumptions

- `requireVerifiedEmail`, `requireAuth`, `requireOIDCConfirmed`, and `coreServices`
  are all emitted as in-scope variables in the generated `main.go` (verified against
  the already-generated `app-mftodo/cmd/server/main.go:158,188–190`). The self
  route's `expr:requireVerifiedEmail` depends on the `requireVerifiedEmail` var
  remaining emitted, which is guaranteed while any route's `middleware:` list
  references it (the account routes do — unchanged by this task). See the fragility
  note in the References.
- `SelfHandler.Get`/`Put` and `NewSelfHandler`'s signature are unchanged.

## References

- [mfgen expr middleware-split pattern notes](../notes/mfgen-expr-middleware-pattern.md)
  — source-level verification of the `expr:` arg-kind, middleware var naming,
  `/v1` prefix merging, `service:coreServices` resolvability, the expected generated
  output, and the documented fragility + robust fallback
  (`expr:auth.NewRequireVerifiedEmail()`).
- `api/cmd/server/main.go:515–525` — the working hand-written reference for the
  exact GET-unverified / PUT-verified split this wiring reproduces.
- `api/internal/handlers/self.go` — the existing `SelfHandler` (unchanged).
- `api/internal/handlers/account_routes.go` — sibling `RegisterAccountRoutes` to
  model the new function's shape and doc-comment on.
- `moduleforge.module.yaml` — existing `provides.services` handler entries,
  `provides.routes` register entries, and `requires.services` to mirror.

## Checkpoint hints

- After creating `api/internal/handlers/self_routes.go` and confirming `make build.api`.
- After adding the three manifest edits (`selfHandler` service, `/v1` route entry,
  `coreServices` requires).
- After reconciling the dev-server comment in `api/cmd/server/main.go`.

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-14
- **Validation summary:**
  - `make build.api` — passed (`RegisterSelfRoutes` compiles; confirmed via
    both `go build -o bin/server ./cmd/server` and `go build ./...` across the
    whole `api` module).
  - `go vet ./...` — passed, no findings.
  - `make lint.api` — **not fully green**: `gofmt` flags `api/config/config.go`,
    but that file is untouched by this task and the same failure is present at
    the pre-task HEAD (confirmed via `git stash`). The two files this task
    edited/added (`api/internal/handlers/self_routes.go`,
    `api/cmd/server/main.go`) both pass `gofmt -l` cleanly. Flagged for the
    manager below rather than fixed, per scope (it lives outside this task's
    diff).
  - `grep -n "RegisterSelfRoutes" moduleforge.module.yaml api/internal/handlers/self_routes.go` — passed.
  - `grep -n "expr:requireVerifiedEmail" moduleforge.module.yaml` — passed, exactly one match.
  - New `/v1` self route entry's `middleware:` list — confirmed to contain only
    `requireOIDCConfirmed` and `requireAuth` (no `requireVerifiedEmail`).
  - `selfHandler` under `provides.services` and `coreServices` under
    `requires.services` — each confirmed to appear exactly once.
  - `api/cmd/server/main.go` — builds; hand-written `GET/PUT /self` block left
    in place per the task doc's explicit decision, with the new reference
    comment added immediately above `r.Get("/self", selfHandler.Get)`.
  - Optional mfgen end-to-end sanity check — skipped (optional per the task
    doc; the `expr:` pattern was already source-level-verified in the
    referenced notes doc, and running mfgen risked touching sibling-project
    scope for no material gain).
  - Scope guard (`git status`) — confirmed changes only under `mod-users/`:
    `api/internal/handlers/self_routes.go` (new),
    `api/cmd/server/main.go`, `moduleforge.module.yaml`, plus this task doc.
- **Environment note:** `make build.api` initially failed in this worktree
  with `replacement directory ../../mod-core/api does not exist` (and
  similarly for mod-audit/mod-authz) — a pre-existing structural artifact of
  this worktree's nesting depth under `mod-users/worktrees/`, unrelated to
  this task's edits. Resolved locally by adding a worktree-local `go.work` at
  the worktree root (gitignored per the existing `/go.work` /.gitignore rule
  and the project's documented worktree-local-go.work convention — see commit
  `370c1c4`); this file is not part of the task's diff and was not committed.
- **Decisions applied under `## Assumptions`:** relied on the task doc's
  assumption that `requireVerifiedEmail`, `requireAuth`, `requireOIDCConfirmed`,
  and `coreServices` are all in-scope generated `main.go` variables (per the
  referenced mfgen verification notes) — no independent re-verification against
  `mfgen`/`app-mftodo` source was performed beyond what the notes already
  documented.
- **Affected source files:**
  - `api/internal/handlers/self_routes.go` (new)
  - `moduleforge.module.yaml`
  - `api/cmd/server/main.go`
</content>
