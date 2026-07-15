# Redesign Self Route Registration Using Two Manifest Entries

## Purpose and scope

Task 001 of this phase wired `GET/PUT /v1/self` into the manifest-driven mfgen
codegen path using a single `RegisterSelfRoutes` function plus a
`register_args: [expr:requireVerifiedEmail]` manifest entry, so the handler
applies the verified-email gate internally to `PUT` only. The phase-1
architecture-conformance review (dispatched from `phase-review`) found this
introduces a second, inconsistent way of expressing per-route middleware
differentiation: every other entry in `moduleforge.module.yaml` (including
this file's own `/v1` account-routes entry) differentiates access purely via
each entry's `middleware:` list, and the plan's own verification notes
(`plan/notes/mfgen-expr-middleware-pattern.md`, fact 4) already confirm mfgen
merges multiple `provides.routes` entries sharing a prefix into isolated,
non-bleeding `r.Group`s — i.e. the simpler, standard two-entries mechanism was
already verified viable but not used.

The user was asked to choose between documenting the `expr:` approach as a
deliberate convention, or redesigning to match the file's existing two-entries
convention. **The user chose to redesign.** This task replaces task 001's
`expr:`-based wiring with two separate manifest entries and two single-verb
register functions, eliminating the `expr:` register-arg mechanism for
`/self` entirely.

Note: the security lens's independent trace of mfgen's reachability graph
found that the "fragility" concern in `plan/notes/mfgen-expr-middleware-pattern.md`
(that `expr:requireVerifiedEmail` could dangle) was factually incorrect —
middleware nodes are unconditional reachability roots in mfgen, so the var is
always emitted. This redesign is **not** motivated by that fragility claim
(which is void); it is motivated solely by the architecture-conformance
finding on pattern consistency/discoverability. Do not re-introduce the
fragility framing when touching related docs or notes.

This task also folds in a corroborating suggestion from two independent
review lenses (correctness and security): add a small test asserting the
GET-unguarded / PUT-gated split actually holds.

## Requirements

### 1. Replace `RegisterSelfRoutes` with two single-verb register functions

In `api/internal/handlers/self_routes.go`, replace the existing
`RegisterSelfRoutes` function with two functions:

```go
package handlers

import "github.com/go-chi/chi/v5"

// RegisterSelfGetRoute mounts GET /self onto r. The caller supplies the /v1
// prefix and whatever middleware this entry's manifest group carries
// (requireOIDCConfirmed, requireAuth) — deliberately NOT requireVerifiedEmail,
// so accounts with an unverified email can still reach this endpoint (the GUI
// renders the "verify your email" page from it).
func RegisterSelfGetRoute(r chi.Router, h *SelfHandler) {
	r.Get("/self", h.Get)
}

// RegisterSelfPutRoute mounts PUT /self onto r. The caller supplies the /v1
// prefix and this entry's manifest middleware group, which includes
// requireVerifiedEmail — only accounts with a verified email may update their
// own profile.
func RegisterSelfPutRoute(r chi.Router, h *SelfHandler) {
	r.Put("/self", h.Put)
}
```

Delete the old `RegisterSelfRoutes` function and its `net/http` import (no
longer needed — neither function takes a middleware parameter).

### 2. Replace the manifest's single `/v1` self entry with two entries

In `moduleforge.module.yaml`, replace the single self route entry task 001
added (the one with `register: handlers.RegisterSelfRoutes` and
`register_args: [expr:requireVerifiedEmail]`) with two entries:

```yaml
    # /v1/self (GET) — read own profile. Reachable to accounts with UNVERIFIED
    # email (so the GUI can render the "verify your email" page). Matches this
    # file's standard convention: differentiation via each entry's own
    # middleware: list, not a register-arg.
    - prefix: /v1
      handler: selfHandler
      register: handlers.RegisterSelfGetRoute
      middleware:
        - requireOIDCConfirmed
        - requireAuth

    # /v1/self (PUT) — update own profile. Requires a verified email.
    - prefix: /v1
      handler: selfHandler
      register: handlers.RegisterSelfPutRoute
      middleware:
        - requireOIDCConfirmed
        - requireAuth
        - requireVerifiedEmail
```

- Do not add a `register_args` field to either entry — neither register
  function takes extra arguments now.
- The `selfHandler` service entry task 001 added under `provides.services` is
  unchanged — both new route entries reference `handler: selfHandler`.
- The `coreServices` entry under `requires.services` (added by task 001) is
  unchanged.
- Confirm (via `plan/notes/mfgen-expr-middleware-pattern.md` fact 4, and by
  inspecting `mergeRouteGroup` in `mfgen/internal/codegen/main_gen.go` if you
  want to verify directly) that two `/v1`-prefixed entries with different
  middleware lists each get their own isolated `r.Group` in generated output —
  this is the mechanism this redesign now relies on instead of `expr:`.

### 3. Update the dev-server reconciliation comment

`api/cmd/server/main.go`'s hand-written `GET/PUT /self` block currently has a
reconciliation comment (added by task 001) referencing
`handlers.RegisterSelfRoutes` and `register_args: [expr:requireVerifiedEmail]`.
Update that comment to reference the new two-entry manifest shape
(`handlers.RegisterSelfGetRoute` / `handlers.RegisterSelfPutRoute`, each with
its own `middleware:` list) instead. Do not otherwise touch this file's
hand-written route block — same non-generated-file rationale as task 001.

### 4. Add test coverage for the GET/PUT split

Add a small test (in `api/internal/handlers/self_routes_test.go` or similar,
matching this package's existing test conventions if any exist nearby) that
mounts both `RegisterSelfGetRoute` and `RegisterSelfPutRoute` on a `chi.Router`
under representative middleware groups (one without `requireVerifiedEmail`,
one with a no-op/marker verified-email middleware) and asserts: a request to
`GET /self` succeeds without the marker firing; a request to `PUT /self`
only succeeds when the marker's underlying check passes. This is no longer
guarding an internal `r.Group` (since the split is now expressed at the
manifest/registration level, not inside a single function), but it still
guards against a future accidental edit that merges the two entries back into
one function with the wrong middleware applied.

### 5. Correct the framing in `plan/notes/mfgen-expr-middleware-pattern.md` (optional, low-cost)

If convenient while already in this file for context, add a short note (do
not rewrite the whole doc) clarifying that `/self` ultimately did **not** use
the `expr:` register-arg pattern — it was redesigned to use two manifest
entries per the phase-1 architecture-conformance review — and that the
"documented fragility" section's premise (that `expr:requireVerifiedEmail`
could dangle) was independently shown to be incorrect by the phase-1 security
review (middleware nodes are unconditional reachability roots in mfgen, per
`mfgen/internal/resolver/reachability.go`). This is a plan-notes correction,
not a requirement for the code change itself — skip it if it would eat
significant time; the manager will otherwise carry this correction forward
into Phase 2's doc-updates task directly.

## Validation

- `make build.api` succeeds.
- `go vet ./...` passes.
- `gofmt -l api/internal/handlers/self_routes.go api/cmd/server/main.go` outputs nothing.
- The new test (Requirement 4) passes and actually fails if you temporarily
  swap which function gets which middleware (sanity-check the test is not a
  false-positive tautology).
- `grep -n "RegisterSelfGetRoute\|RegisterSelfPutRoute" moduleforge.module.yaml api/internal/handlers/self_routes.go` — each function name appears in both files.
- `grep -n "expr:requireVerifiedEmail" moduleforge.module.yaml` — returns **no** matches (the pattern is fully removed for `/self`).
- `grep -n "RegisterSelfRoutes" moduleforge.module.yaml api/internal/handlers/self_routes.go api/cmd/server/main.go` — returns **no** matches (old single-function name fully removed/replaced).
- The PUT entry's `middleware:` list contains `requireVerifiedEmail`; the GET entry's does not.
- `selfHandler` still appears exactly once under `provides.services`; `coreServices` still appears exactly once under `requires.services` (both unchanged from task 001).
- Scope guard: `git status` shows changes only under `mod-users/`.

## Metadata

architectural_impact: true

## Assumptions

- `chi.Router`'s `r.Get`/`r.Put` registration order across the two separate
  `register:` entries does not matter for correctness (chi routes by
  method+path, not registration order, for non-overlapping method/path pairs
  on the same router).
- The `selfHandler` service and `coreServices` requires-entry from task 001
  need no changes — only the route-registration shape changes.

## References

- `plan/phase-01-self-route-wiring/001-wire-self-routes-manifest.md` — the task this redesigns; read its `## Status` section for what was actually implemented.
- [mfgen expr middleware-split pattern notes](../notes/mfgen-expr-middleware-pattern.md) — fact 4 (two-entries-per-prefix merge behavior) and the now-corrected fragility section.
- `api/internal/handlers/account_routes.go` — the sibling convention this redesign now matches exactly (one register function per verb/entry, differentiation via each entry's own `middleware:` list).
- `moduleforge.module.yaml` — the account-routes `/v1` entries as the direct model for the two new self-route entries.

## Checkpoint hints

- After replacing `RegisterSelfRoutes` with the two new functions and confirming `make build.api`.
- After replacing the manifest's single self entry with two entries.
- After adding and passing the new test.

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-14
- **Validation summary:**
  - `make build.api` — passed.
  - `go vet ./...` — passed, no findings.
  - `gofmt -l api/internal/handlers/self_routes.go api/cmd/server/main.go` — no output (clean); also confirmed clean for the new `api/internal/handlers/self_routes_test.go`.
  - New test `TestSelfRoutes_GetPutSplit` (`api/internal/handlers/self_routes_test.go`) — passed (`go test ./internal/handlers/ -run TestSelfRoutes_GetPutSplit -v`, 3 subtests). Sanity-checked per the task doc: temporarily swapped which register call sat behind the verified-email marker (marker moved from the PUT group to the GET group) and reran — all three subtests failed as expected, then reverted to the correct wiring, which passes again. Not a tautology.
  - `grep -n "RegisterSelfGetRoute\|RegisterSelfPutRoute" moduleforge.module.yaml api/internal/handlers/self_routes.go` — both function names present in both files.
  - `grep -n "expr:requireVerifiedEmail" moduleforge.module.yaml` — zero matches, confirmed removed.
  - `grep -n "RegisterSelfRoutes" moduleforge.module.yaml api/internal/handlers/self_routes.go api/cmd/server/main.go` — zero matches, confirmed removed/replaced everywhere.
  - PUT entry's `middleware:` list contains `requireVerifiedEmail`; GET entry's does not — confirmed by inspection.
  - `selfHandler` appears exactly once under `provides.services`; `coreServices` appears exactly once under `requires.services` — confirmed by grep count, both unchanged from task 001.
  - Scope guard (`git status`) — confirmed changes only under `mod-users/`.
- **Verification against real mfgen source (beyond the referenced notes doc):** read `mergeRouteGroup` directly in `mfgen/internal/codegen/main_gen.go` (~L528-562) to confirm fact 4 independently rather than relying solely on the notes doc — confirmed that when multiple `routeEntry`s share a prefix, each entry with `hasTopMW` (i.e. a non-empty `middleware:` list) is wrapped in its own `r.Group`, so the two new self-route entries (each carrying `middleware:`) each get an isolated, non-bleeding group. This directly supports the two-entries mechanism this redesign now relies on.
- **Environment note:** as flagged in task 001's Status section, `make build.api` in this worktree initially failed with `replacement directory ... does not exist` for the mod-core/mod-audit/mod-authz replace directives — a pre-existing artifact of this worktree's nesting depth (`mod-users/worktrees/plan/self-route-manifest/worktrees/<task>`, six levels below `moduleforge/`). Resolved locally with a worktree-local `go.work` (six `../` segments to the sibling modules) per the dispatch instructions; this file is gitignored (`/go.work` rule) and was not part of any commit.
- **Requirement 5 (optional plan-notes correction) — deliberately skipped.** The task doc invites (but does not require) a short correction note in `plan/notes/mfgen-expr-middleware-pattern.md`. The `implement-task` procedure this agent follows explicitly forbids editing any file under `<worktree>/plan/` other than the assigned task document, and the task doc itself provides an explicit skip path ("the manager will otherwise carry this correction forward into Phase 2's doc-updates task directly"), so this was skipped rather than risk an ownership-boundary violation. Flagged below for the manager to apply directly (or via Phase 2) if desired.
- **Decisions applied under `## Assumptions`:** relied on both stated assumptions — chi's `r.Get`/`r.Put` registration order across the two separate `register:` entries does not affect correctness, and the `selfHandler`/`coreServices` entries from task 001 needed no changes (confirmed unchanged by grep count).
- **Affected source files:**
  - `api/internal/handlers/self_routes.go` (replaced `RegisterSelfRoutes` with `RegisterSelfGetRoute`/`RegisterSelfPutRoute`; dropped the now-unused `net/http` import)
  - `api/internal/handlers/self_routes_test.go` (new)
  - `moduleforge.module.yaml` (single self route entry replaced with two entries)
  - `api/cmd/server/main.go` (reconciliation comment updated; no functional change)
