# Update Architecture Docs

## Purpose and scope

Update mod-users' architecture, spec, and agent-facing convention docs to reflect
the `/self` manifest wiring landed in Phase 1, and to record the sanctioned
`expr:` per-route middleware-split manifest pattern so future modules facing the
same need do not reinvent it or assume an mfgen generator change is required.

This is a documentation-only task. It runs after Phase 1
(`phase-01-self-route-wiring/001-wire-self-routes-manifest.md`) has landed, so the
docs describe the final manifest entry accurately. Follow the
`update-architecture-docs` task-procedure at
`plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.

role_doc: plugins/flow/references/roles/architect-backend.md

## Requirements

The Phase 1 implementation task that surfaced these architectural/spec implications:
- `phase-01-self-route-wiring/001-wire-self-routes-manifest.md` — added the
  `RegisterSelfRoutes` function, the `selfHandler`/route/`coreServices` manifest
  entries wiring `/self` into codegen, and the GET-unverified/PUT-verified split.

Review and update the following files (each named explicitly):

### `docs/architecture.md`
- In the API-layer / route-surface description (the "Self" row of the API-surface
  table, ~L45, and the surrounding "API layer" section ~L36–49), document that
  `GET /v1/self` is reachable to accounts with an **unverified** email (it bypasses
  the email-verification gate so the GUI can render the "verify your email" page),
  while `PUT /v1/self` requires a **verified** email.
- Add a short subsection (in the API layer or a cross-cutting-patterns area)
  describing the **manifest per-route middleware split**: within one route prefix,
  a `register:` entry can carry group-level middleware that intentionally omits a
  gate, and pass that gate into the register function via a `register_args:
  [expr:<middleware-var>]` entry so the handler applies it to a subset of routes
  via a nested `r.Group`. Note this is the mod-users `/self` precedent and the
  first module in the repo to split middleware within one prefix.

### `docs/mod-users-spec.md`
- Update use case 7 ("View and update own profile", ~L84–90) and/or the "Self
  (authenticated)" API section (~L227–230) to state the verified-email contract:
  `GET /v1/self` is reachable to authenticated accounts even when the email is
  unverified; `PUT /v1/self` requires a verified email. Keep it a behavioral
  contract statement, not implementation detail.

### `AGENTS.md` (mod-users)
- Add manifest-authoring guidance (a short subsection, e.g. under Conventions or a
  new "Manifest conventions" heading) documenting the `expr:` arg-kind as the
  sanctioned way to pass an already-constructed, in-scope value (such as a
  middleware) into a `register:` function verbatim — enabling a per-route
  middleware split within one prefix **without** an mfgen schema/generator change.
  Reference the `/self` entry in `moduleforge.module.yaml` as the worked example.
  Include the documented fragility + robust fallback: `expr:<var>` creates no
  dependency edge, so it relies on the referenced middleware var being emitted
  (guaranteed while some route's `middleware:` list references it); the
  self-contained alternative is `expr:<constructor-call>()` (e.g.
  `expr:auth.NewRequireVerifiedEmail()`).
- If `AGENTS.md` links to a canonical manifest-spec doc reachable from
  `project_root` (e.g. `docs-mf-standards/manifest-spec.md`, referenced from the
  AGENTS.md migrations section), check whether the `expr:` pattern belongs there
  too; if that doc is outside the `mod-users/` tree or not writable in this repo,
  document the pattern in `AGENTS.md` only and note the cross-reference. Do not
  edit anything outside the `mod-users/` tree.

### `api/openapi.yaml` (verification only)
- Confirm `GET/PUT /v1/self` are already present in `api/openapi.yaml` (they are
  listed in the spec checklist). Middleware-gating nuance is not typically expressed
  in OpenAPI, so no OpenAPI change is expected. Only note a discrepancy if `/self`
  is missing there.

## Validation

- `docs/architecture.md` states the `GET`-unverified / `PUT`-verified `/self`
  contract and describes the `expr:` per-route middleware-split pattern.
- `docs/mod-users-spec.md` use case 7 / Self API section states the verified-email
  contract for `PUT` vs `GET`.
- `AGENTS.md` documents the `expr:` arg-kind manifest convention with the `/self`
  worked example and the fragility/fallback note.
- Cross-references between the three docs (and to `moduleforge.module.yaml`) are
  consistent and resolve.
- `grep -rn "self" docs/architecture.md docs/mod-users-spec.md` shows the updated
  contract language.
- Markdown lint / doc-standards checks (if the repo runs any) pass.
- `git status` shows edits only under `mod-users/` (`docs/`, `AGENTS.md`) — nothing
  under `mfgen/`, `app-mftodo/`, or sibling modules.

## References

- [mfgen expr middleware-split pattern notes](../notes/mfgen-expr-middleware-pattern.md)
  — the verified mechanism, exact syntax, expected generated output, and the
  fragility/fallback to carry into AGENTS.md.
- `moduleforge.module.yaml` — the landed `/self` register entry to reference as the
  worked example.
- `plugins/flow/task-procedures/update-architecture-docs/SKILL.md` — the procedure
  to follow.
</content>
