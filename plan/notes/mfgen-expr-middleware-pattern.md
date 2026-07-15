# mfgen expr: Middleware-Split Pattern — Verification Notes

## Purpose and scope

Records the source-level verification (performed during planning) that the
manifest-driven approach for wiring `/self` with a per-route middleware split is
supported by the *current* mfgen schema and generator — i.e. **no mfgen change is
required**. Captured so the implementing agent and reviewer can trust the exact
`register_args`/`expr:` syntax without re-deriving it.

All mfgen paths below are in the sibling `mfgen/` project
(`/Users/zane/playground/moduleforge/mfgen`), read for verification only. Nothing
under `mfgen/` is edited by this plan.

## The problem

`RouteEntry` (mfgen `internal/schema/module.go` ~L69–84) applies middleware only at
the *group* level via the `middleware:` field — `buildScopeNestingInner` emits one
`r.Use(<mw>)` per named middleware before the register call. There is **no
per-route middleware override field**. A naive single `/v1/self` group would apply
`requireVerifiedEmail` to both GET and PUT, violating the required contract
(GET reachable to unverified accounts; PUT verified-only).

## Why no schema change is needed

The generator's `expr:` arg-kind (`internal/codegen/main_gen.go` `resolveOneArg`,
L631–634) emits the text after `expr:` **verbatim** into the generated call, with no
resolution:

```go
case strings.HasPrefix(arg, "expr:"):
    // expr:<raw-go-expr> — emit verbatim (no-op wrapper for raw expressions).
    return strings.TrimPrefix(arg, "expr:")
```

`buildRegisterArgs` (L749–763) assembles the register call as
`[r, <handlerVar>, <register_args…>]`. So a `register_args: [expr:requireVerifiedEmail]`
entry produces `handlers.RegisterSelfRoutes(r, selfHandler, requireVerifiedEmail)`.

The register function receives the already-constructed middleware value and applies
it **internally** to just the PUT sub-route via its own nested `r.Group`. The
group-level `middleware:` list is set to `requireOIDCConfirmed` + `requireAuth`
only — deliberately excluding `requireVerifiedEmail` — so GET stays ungated by the
verified-email check.

## Confirmed facts (against real source / real generated output)

1. **`requireVerifiedEmail` is an in-scope variable in generated main.go.**
   `app-mftodo/cmd/server/main.go:189` (already-generated output) declares:
   `requireVerifiedEmail := auth.NewRequireVerifiedEmail()`, alongside
   `requireOIDCConfirmed` (L188) and `requireAuth` (L190). The `auth` package
   (`github.com/moduleforge/mod-users/api/auth`) is imported (L38). So
   `expr:requireVerifiedEmail` resolves to a real, typed local var.

2. **Middleware var naming is a pass-through camelCase.** mfgen
   `internal/resolver/graph.go:403` `middlewareVarName(name) = toCamelCase(name)`.
   `requireVerifiedEmail` is already camelCase → var name is exactly
   `requireVerifiedEmail`. `middlewareVarFromNodes` (main_gen.go:450) confirms the
   route `middleware:` field also uses this name.

3. **Type match.** `api/auth/auth.go:92`:
   `func NewRequireVerifiedEmail() func(http.Handler) http.Handler`. So the
   register-function parameter type is `func(http.Handler) http.Handler` (this is
   also chi's `func(http.Handler) http.Handler` middleware shape).

4. **Multiple entries at one prefix don't bleed middleware.** `mergeRouteGroup`
   (main_gen.go:534–562) wraps each `hasTopMW` entry that shares a prefix in its
   own `r.Group`. The already-generated `/v1` block (app-mftodo main.go:203–215)
   shows mod-core (innerMount), mod-users account routes, and mod-tasks each in
   isolated `r.Group`s. A new self entry becomes a fourth isolated `r.Group`.

5. **`*coreservice.Services` is resolvable.** mod-core manifest
   (`mod-core/moduleforge.module.yaml:41–44`) provides
   `name: coreServices`, `type: "*coreservice.Services"`,
   `constructor: coreservice.New`. Generated as the `coreServices` variable
   (app-mftodo main.go:158). Referenced from another module's arg list as
   `service:coreServices`. mod-users already requires sibling core services
   (`naturalPersonService`, `typeResolver`) the same way, so adding
   `coreServices` to `requires.services` follows the established convention.

## Expected generated output (verification target)

Inside the merged `r.Route("/v1", func(r chi.Router) { … })` block, mfgen will add
one more group (order within the block depends on manifest declaration order and is
not functionally significant):

```go
r.Group(func(r chi.Router) {
    r.Use(requireOIDCConfirmed)
    r.Use(requireAuth)
    handlers.RegisterSelfRoutes(r, selfHandler, requireVerifiedEmail)
})
```

With `RegisterSelfRoutes` defined as:

```go
func RegisterSelfRoutes(r chi.Router, h *SelfHandler, requireVerifiedEmail func(http.Handler) http.Handler) {
    r.Get("/self", h.Get)               // reachable to unverified accounts
    r.Group(func(r chi.Router) {
        r.Use(requireVerifiedEmail)
        r.Put("/self", h.Put)            // verified accounts only
    })
}
```

Net effect:
- `GET /v1/self` → `requireOIDCConfirmed` + `requireAuth` (NOT verified-email).
- `PUT /v1/self` → `requireOIDCConfirmed` + `requireAuth` + `requireVerifiedEmail`.

This matches the hand-written reference in
`api/cmd/server/main.go:515–525` exactly.

## One documented fragility (flag, not a blocker)

`expr:requireVerifiedEmail` is emitted verbatim; the generator does **not** create a
dependency edge from the self route to the middleware node. The
`requireVerifiedEmail` variable is emitted only because it is currently referenced
by the account-routes `middleware:` list (which keeps it reachable). This is true
today and is not changed by this plan. If a future refactor ever removes
`requireVerifiedEmail` from every `middleware:` list, the self route's `expr:`
reference would dangle. Robust fallback if that ever matters:
`register_args: [expr:auth.NewRequireVerifiedEmail()]` (constructs a fresh, stateless
instance inline; `auth` is already imported). The plan uses `expr:requireVerifiedEmail`
per the sanctioned pattern and documents this note in AGENTS.md.
</content>
</invoke>
