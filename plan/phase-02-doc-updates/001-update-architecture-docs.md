# Update Architecture Docs

## Purpose and scope

Update mod-users' architecture and specification documents to reflect the SSH public-key ownership capability delivered in Phase 01:

- the new `mod_users` schema and `mod_users.ssh_public_keys` table
- the `sshKeyService` and `sshKeyResolver` services
- the six `/v1` SSH key endpoints
- the key acceptance policy
- the resolver's consumer contract

Follow the `update-architecture-docs` task-procedure.

## Requirements

- **Implementation task documents that surfaced the architectural implications.** They will all have completed by the time this phase runs. Paths are relative to the plan worktree root:
  - `plan/phase-01-ssh-public-keys/001-add-ssh-key-storage.md`: the first table under a `mod_*` schema, a partial unique index as the global-uniqueness invariant, and archive-on-revoke.
  - `plan/phase-01-ssh-public-keys/003-add-ssh-key-service-and-resolver.md`: the new pre-authentication resolver service, its no-cache/no-write/no-`Authorize` contract, and the `usersservice` facade additions.
  - `plan/phase-01-ssh-public-keys/004-add-ssh-key-http-routes.md`: new public API routes, manifest services and routes, and step-up gating extended to SSH keys.
  - Read each one's Status section for what actually landed, including any deviation from the design. Also read `plan/notes/ssh-key-design.md`, the decision record whose rationale the docs should carry forward in condensed form.
- **Files to review and update:**
  - `docs/architecture.md`:
    - Data model table: add `mod_users.ssh_public_keys`, and explain why it alone lives in `mod_users` while older tables stay in `public`.
    - Runtime service dependencies: the new services consume only existing ones, so check whether any statement changes.
    - API layer tag table: add an `SSH Keys` row.
    - Authentication flow and multi-channel account model: add SSH public keys as a git-access credential that resolves to the account holder, with revocation semantics and the explicit statement that the anonymous system actor can never authenticate over SSH, because it has no `user_accounts` row and so can hold no key.
    - The consumer-facing resolver contract, for app-mfgit and mod-repos.
  - `docs/mod-users-spec.md` (the project's `docs/*-spec.md`):
    - Key use cases: add self-service key management and operator key management.
    - General features and Security requirements: key algorithm policy, global uniqueness, immediate revocation, step-up, and masking.
    - Data model: add the table and correct the "owns ten Postgres tables" count against the actual table list.
    - API definition checklist: add the six endpoints under the actually served paths `/v1/self/ssh-keys` and `/v1/user-accounts/{uuid}/ssh-keys`. Do not rewrite the pre-existing `/v1/users` admin paths; that drift is out of scope, but a one-line note is acceptable.
  - `AGENTS.md`: review only. Update it if a new convention emerged, for example the first `mod_*`-schema table and how sqlc names its model. Otherwise leave it unchanged.
- **role_doc:** `plugins/flow/roles/architect-backend.md`. The implications are primarily a new backend service, an API surface, and an authentication component. The schema change is secondary.
- **Procedure:** `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.

## Validation

- `docs/architecture.md` and `docs/mod-users-spec.md` were each reviewed against the three task documents listed above and updated where their content no longer matched the landed implementation. For each file, the Status section records either a list of the sections changed or "reviewed, no change needed" with a reason.
- `grep -n "ssh" docs/architecture.md docs/mod-users-spec.md` shows coverage of each of these:
  - the table
  - the resolver contract
  - all six endpoints (spec checklist)
  - the algorithm policy
  - revocation semantics
- No statement in either document contradicts `plan/notes/ssh-key-design.md` or the landed code, as reported in the implementation tasks' Status sections.
- Markdown style: sentence-case section headings, and relative links that resolve.

## References

- `plan/notes/ssh-key-design.md`: the decision record.
- `/Users/zane/playground/moduleforge/app-mfgit/docs/handoffs/mod-users-ssh-extension.md`: the originating requirements, useful for the consumer-contract wording.
- `/Users/zane/playground/moduleforge/docs-mf-standards/architecture/schema-ownership-design.md`: the schema standard to cite when explaining table placement.

## Status

**Outcome:** succeeded. Date: 2026-09-28.

Implemented both the task doc's base scope and the additional required scope named in the dispatch prompt (arch-001 architecture-doc-drift, arch-002 undocumented key decisions, plus two small deferred fixes on stale operator step-up comments in code/openapi). No behavior changes; documentation, manifest/code comments, and one OpenAPI structural correction only.

**`docs/architecture.md`** — sections changed (not "no change needed"):
- Data model table: added `mod_users.ssh_public_keys` row, plus a new "Schema placement is mixed by design" paragraph explaining the mixed `public`/`mod_users` rule and linking to the new topic doc's D1 section (including the arch-007 operational consequence — composing-app runtime grants and backup/test tooling scoped to `public`).
- Runtime service dependencies: reviewed, no change needed — `sshKeyService`/`sshKeyResolver` consume only existing `provides.services` (`authorizer`, `observerGroup`) and existing `infra`/`queries`; `requires.services` gains no new entry (confirmed by inspecting `moduleforge.module.yaml`'s `requires:` block).
- API layer tag table: added an `SSH Keys` row covering the self/operator route split and the corrected step-up gating (operator register/revoke gated only when the path UUID is the caller's own account, per task 006's fix — not "operator routes are exempt" as the original design note said before that fix).
- Added a paragraph documenting the `sshKeyResolver` manifest service as a second exported cross-module integration point alongside `resolveActorOrAnonymous`, including its pre-authentication/no-write/no-cache contract and a pointer to the full consumer contract (including the rate-limiting/candidate-key-capping precondition, arch-005/security-003) in the new topic doc.
- Authentication flow: added a new "SSH public key (git-over-SSH access)" credential-channel paragraph — resolution to `account_holder`, immediate revocation semantics, and the explicit statement that the anonymous system actor cannot hold a key (no `user_accounts` row).
- Multi-channel account model: added `mod_users.ssh_public_keys` to the credential-channel list.
- New `## Key decisions` section (arch-002): six one-line-with-link entries (D1, D2, D3, D4, D9, D11) pointing into the new topic doc.
- New `docs/architecture/ssh-keys.md` topic doc (arch-002): promotes D1 (schema placement, with the arch-007 operational consequence), D2 (global uniqueness enforced in the schema), D3 (archive-on-revoke, contrasted with hard-delete siblings `auth_oidc_identities`/`anon_tokens`), D4 (no-cache/immediate-revocation), D9 (operator authorization via plain `update`, including the accepted-trade-off note per arch-003, and the corrected step-up-gating behavior from the phase-01 `security-001` fix), and D11 (resolver consumer contract, including the arch-005/security-003 consumer precondition), each with rationale, condensed from `plan/notes/ssh-key-design.md`.
- Further reading: added the new topic doc.

**`docs/mod-users-spec.md`** — sections changed:
- Key use cases: added #16 (self-service SSH key management) and #17 (operator SSH key management), including the corrected step-up behavior.
- General features: added one bullet on SSH keys as a standing credential.
- Data model: corrected "ten Postgres tables" to "eleven" and added the `mod_users.ssh_public_keys` row (plus a note that ten of eleven live in `public`).
- API definition: added a new `### SSH keys (self-service and operator)` subsection with all six endpoints under `/v1/self/ssh-keys` and `/v1/user-accounts/{uuid}/ssh-keys`, plus a one-line note on the pre-existing `/v1/users` vs. `/v1/user-accounts` drift (left unfixed, per the task doc's explicit instruction).
- Security requirements: added one detailed bullet ("SSH public-key authentication") covering the algorithm policy, global uniqueness, immediate revocation, the corrected step-up condition, and existence masking.

**`AGENTS.md`** — reviewed and updated: added one sentence to "Code generation (sqlc)" documenting the new convention this plan surfaced (a non-`public`-schema table's sqlc struct name is prefixed with the schema, e.g. `ModUsersSshPublicKey`), per the task doc's own example.

**Additional required scope from the dispatch prompt (beyond the task doc's own text):**
- **arch-001 / item 3 (stale "operator routes are never/not step-up-gated" comments).** Corrected in `api/cmd/server/main.go` (~line 658), `api/handlers/handlers.go` (~line 222), and `moduleforge.module.yaml` (~line 418-420) to state operator register/revoke are step-up-gated only when the path UUID is the caller's own account (task-006 fix), not exempt.
- **item 4 (same stale claim in `api/openapi.yaml`).** Corrected the operator POST/DELETE endpoint descriptions (~lines 1628, 1679) to match. Also added the `StepUpToken` parameter and a `409` step-up response (`SSHKeyRegisterConflict` for POST, `SSHKeyStepUpRequired` for DELETE) to both operator write endpoints — the prose correction alone would have left the spec's machine-checkable parameters/responses still describing the pre-fix (never-gated) behavior, which would have been a second inconsistency in the same document.
- **arch-002 dangling-citation sweep.** Updated every comment in `api/cmd/server/main.go`, `api/handlers/handlers.go`, `api/usersservice/service.go`, `moduleforge.module.yaml`, `api/internal/service/ssh_keys.go`, and `api/internal/service/ssh_key_resolver.go` that cited `plan/notes/ssh-key-design.md Dn` or a task number, to instead cite `docs/architecture/ssh-keys.md` (for the six promoted decisions D1/D2/D3/D4/D9/D11) or state the constraint inline (for non-promoted decisions D6/D7/D8/D10, whose content is now covered in `docs/mod-users-spec.md` instead).

## Validation

- `docs/architecture.md` and `docs/mod-users-spec.md` reviewed against `plan/phase-01-ssh-public-keys/001`, `003`, `004`, and `006`'s Status sections (task 006 — the step-up bypass fix — surfaced after the base task doc was written but is load-bearing for the API surface/step-up wording; read per the dispatch prompt's explicit instruction) and against `plan/notes/ssh-key-design.md`. Sections changed are listed above.
- `grep -n "ssh" docs/architecture.md docs/mod-users-spec.md`: `docs/architecture.md` 14 matching lines, `docs/mod-users-spec.md` 15 matching lines — passed; covers the table, the resolver contract, all six endpoints, the algorithm policy, and revocation semantics (spot-checked each).
- No statement in either document contradicts `plan/notes/ssh-key-design.md` or the landed code — passed. The one place this task's text deliberately diverges from the design note is D8/D9's step-up claim ("operator routes are not step-up-gated"), which the design note itself never updated after task 006's fix; the durable docs now state the corrected, as-landed behavior instead, per the dispatch prompt's explicit instruction to fix that stale claim.
- Markdown style: sentence-case section headings (spot-checked via `grep -n "^##" docs/architecture.md docs/mod-users-spec.md docs/architecture/ssh-keys.md`) — passed. Relative links resolve — verified every `](.` and `](..` link's target file exists and every `#anchor` matches a heading's generated GitHub-style slug in the target document — passed.
- `cd api && go build ./...` / `make build.api` — passed.
- `make lint.api` (`go vet` + `check-server-error-literals`) — passed.
- `gofmt -l` on every edited `.go` file — no output (all formatted).
- `cd api && go test ./internal/service/... ./internal/handlers/... ./usersservice/...` — all packages pass (comment-only changes; no behavior touched).
- `make openapi.validate` — YAML syntax OK (no `spectral` available in this environment; fell back to the Makefile's documented `pyyaml` fallback).
- `python3 -c "import yaml; yaml.safe_load(open('moduleforge.module.yaml'))"` — valid YAML.

## Decisions made

- Created `docs/architecture/ssh-keys.md` as a dedicated topic doc (rather than inlining the six decisions' full rationale into `docs/architecture.md`) per the Project Architecture Document Standards' guidance to push a considerations-doc-shaped section into a topic doc "when the rationale is long, or when it's likely to be revisited" — six decisions each with rationale, evidence, and a consumer-precondition note would have dominated the main file. `docs/architecture.md` carries only a six-entry summary linking into it.
- For code/manifest comments citing a *non-promoted* design-note letter (D6, D7, D8, D10 — key algorithm policy, key metadata, step-up, and response/masking conventions, none of which this task's dispatch instruction named for promotion into the new topic doc), stated the constraint inline and dropped the dangling `Dn` citation, rather than pointing at a doc section that doesn't exist. Where a corresponding durable location exists (the algorithm policy is now in `docs/mod-users-spec.md`'s Security requirements), pointed there instead.
- Extended the dangling-citation sweep to two files not named in the dispatch prompt's explicit six-file list — `api/internal/handlers/ssh_keys.go` (which independently carries the identical literal `plan/notes/ssh-key-design.md Dn` citation the prompt described, apparently missed due to the `ssh_keys.go` basename collision with `api/internal/service/ssh_keys.go`, which *was* named) and `api/internal/handlers/ssh_keys_routes.go` (bare `design note Dn` citations plus the same stale "operator routes never require step-up" claim fix #3 targets, in the same file family). `[task-caused-drift]`.
- `docs/project-structure.md`'s "Documentation directories" listing was updated to add the new `docs/architecture/ssh-keys.md` entry — a direct, mechanical consequence of creating that file, even though `project-structure.md` was not in the task doc's own "Files to review and update" list. `[task-caused-drift]`.
- Added the `X-Step-Up-Token` parameter and a `409` step-up-required response to `api/openapi.yaml`'s two operator write endpoints (register/revoke), beyond the prose-only correction item 4 asked for, because leaving the parameter/response absent while the corrected prose says step-up can apply would have been a second, self-inflicted inconsistency in the same document. No behavior change — this only documents behavior task 006 already shipped.

## Flagged for manager

- Three files carry bare `(design note Dn)` citations this task did not touch, because none of them is in the dispatch prompt's six-file list, they aren't adjacent to a file this task otherwise opened, and their `Dn` letters (D6, D7, D8) are not among the decisions promoted into `docs/architecture/ssh-keys.md`: `api/internal/sshkey/label.go:20`, `api/internal/sshkey/sshkey.go:2,26,54,117` (also has a `task 003` reference at line 117), and `api/internal/handlers/identities.go:639`. These will dangle once `plan/notes/ssh-key-design.md` is torn down with the rest of the plan. Low severity — internal comments, not user-facing docs — but worth a follow-up sweep, possibly folded into whatever task tears down `plan/notes/ssh-key-design.md`.
