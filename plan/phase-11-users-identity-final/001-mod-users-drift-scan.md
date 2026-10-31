# Mod-Users Documentation Drift Scan

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Run mod-users' slice of the identity-model documentation drift scan (shared brief requirement 3).
Search mod-users' prose and code comments for terms from the old identity model. Fix every match
whose correct wording is clear from the ADR, the docs the `doc-updates` phase rewrote, and this
plan's notes. For each match you do not fix, report a followup for the manager to file. No standard
skill covers this task. Follow the [Procedure](#procedure).

The task changes only docs and comments. Code identifiers, SQL statements, configuration values and
tests do not change. If a match needs a code change, report it as a followup.

mod-core's final coordination phase (its task `cross-project-coordinating-scan`) checks that this
task is done and consumes its scan report, so the report shape in requirement 5 matters.

This task runs before task 002 (`reconcile-superseded-followups`).

## Requirements

1. Scan these mod-users paths (all relative to the repository root):
   - `README.md`, `AGENTS.md`, `next-steps.md`, `.env.example`
   - `model/README.md`, `gui/README.md`
   - everything under `docs/` **except** `docs/mf-standards/` (a git submodule owned by
     docs-mf-standards; report matches there as followups against that project, do not edit)
   - everything under `deploy/`
   - `api/openapi.yaml` (descriptions, summaries and examples)
   - `moduleforge.module.yaml` comments
   - code comments in `api/`, `model/migrations/`, `model/queries/` and `gui/src/`. Do not scan or
     edit `model/db/` (sqlc output); a comment there comes from `model/queries/` and is fixed there.

   Do not scan `plan/`, `worktrees/`, `node_modules/` or `gui/dist/`.

   Scan for these terms, case-insensitive:
   - `user_accounts` / `user_account`, and `apps_user_accounts`
   - `account_holder`
   - `corporation` (also `Corporation`, `corporations`)
   - `MF002`
   - service-account ownership described as "under review" or "under separate review"
   - `app_service_identity` described as something that logs in or holds a password or email
   - `.invalid` (placeholder email addresses for service identities or guests)
   - stateless JWT or stateless session wording, and "session cookie" or "HttpOnly" wording for
     authentication (the only remaining cookie is the `oidc_state` CSRF cookie)
   - 24-hour token lifetimes (`24-hour`, `24h`, `24 hours` applied to tokens or sessions)
   - `assume` / `assumed` as an operation, route or session concept, as opposed to the ordinary
     English verb
   - `sudo` (also `Sudo`), `SudoUserUUID`, `AssumedUser`
   - `first user` / `first-user` / `firstUser` as the first-registrant-becomes-admin hook
   - `HS256`
   - `X-Step-Up-Token`
   - `/v1/self`
2. Classify each match:
   - **Stale.** The text describes the old model as current. Rewrite it to the new model, using
     `docs/architecture.md`, `docs/architecture/sessions-and-tokens.md`, `docs/jwt-eddsa.md`,
     [the API surface note](../notes/mod-users-api-surface.md) and
     [decisions and resolutions](../notes/decisions-and-resolutions.md) as the source of truth.
     Typical rewrites: `user_accounts` to principals and `mod_users.users`; `/v1/self` to `/v1/me`;
     `assume` to impersonation (`/v1/impersonations`, the `impersonate` operation); sudo to the
     impersonating session's `AuthContext`; HS256 and `X-Step-Up-Token` to the EdDSA-only carrier and
     factor-composition step-up (U4, U8); the first-user hook to setup-token bootstrap (U9, U10,
     U17); 24-hour tokens to the 1-hour access token plus refresh lineages (U6).
   - **Legitimate.** Leave it. Examples:
     - historical or "replaces X" / "removed" wording that is accurate, including every entry in
       `docs/consumer-upgrade-notes.md`'s list of removed and renamed symbols, routes and variables
     - the "first user" that setup-token bootstrap creates (U10); only the first-registrant hook is
       stale
     - the ordinary English verb "assume"
     - names owned by another project that are still correct there
     - tests or comments asserting that a removed route or symbol is gone
   - **Unclear or out of scope.** The fix needs a code change (for example a live identifier such as
     a leftover `SudoUserUUID` field), a design decision, or an edit in another project. Do not edit
     it. Report it as a followup (requirement 4).
3. Keep every edited doc consistent with the binding names in
   [the API surface note](../notes/mod-users-api-surface.md); do not introduce new names.
4. Do not edit `plan/followups.yaml` and do not call any `followups_*` tool. For each unfixed match,
   state the exact intended followup in the structured report under `requests_for_manager`, for the
   manager to file, with:
   - the target project (`mod-users`, or the owning project's slug for a match in a submodule or
     sibling)
   - a proposed title of at most 45 characters
   - tags, including `type:documentation` or `type:bug`
   - text naming the file, the line, the term and the correct current wording
5. Put a scan report in the structured report:
   - each term searched, with its match count before and after the edits
   - each fixed match, as file:line and a one-line description of the change
   - each legitimate match, with the reason
   - each reported followup
   - a one-paragraph summary ("what was fixed, what was filed") for mod-core's coordinating scan

## Validation

- After your edits, run:

  ```bash
  rg -n -i 'user_accounts?|account_holder|corporation|MF002|under (separate )?review|app_service_identity|\.invalid\b|stateless|session cookie|httponly|24[- ]hours?|\b24h\b|\bassumed?\b|assumeduser|sudo|first[- ]?user|HS256|X-Step-Up-Token|/v1/self' \
    README.md AGENTS.md next-steps.md .env.example model/README.md gui/README.md docs deploy \
    api model/migrations model/queries gui/src moduleforge.module.yaml \
    --glob '!docs/mf-standards/**'
  ```

  Every remaining match appears in the scan report as legitimate or as a reported followup.
- `git diff --stat` shows changes only to Markdown, YAML comments or descriptions, `.env.example`
  comments, the OpenAPI descriptions, or comments in `.go`, `.sql`, `.ts` or `.tsx` files. Nothing
  outside comments changes in code files; `git diff -- model/db` is empty unless a query comment
  changed, in which case `cd model && sqlc generate` was run and the only `model/db` change is that
  comment.
- `make build`, `make test`, `make lint` and `cd model && make verify` pass, confirming the comment
  edits broke nothing.
- Markdown edits follow the project's markdown style, and relative links still resolve.

## Assumptions

- Phases 6 to 10 of this plan are merged into the plan branch; `doc-updates` (phase 10) already
  rewrote the architecture, spec and repository docs, so most matches should be legitimate history
  or leftovers that phase missed.
- `mod-core/gui` is built before `make build`/`make lint` (see `.claude/CLAUDE.md`), and the sibling
  links from `scripts/link-siblings.sh` resolve mod-core and mod-authz to their
  `identity-model-impl` plan worktrees.
- Matches in other repositories are not this task's concern; mod-core's coordinating scan handles
  them.

## References

- mod-core `docs/architecture/identity-model.md`: the accepted ADR (read only).
- [The mod-users API surface](../notes/mod-users-api-surface.md): the implemented names (schema,
  routes, configuration, removed symbols in sections 6.3 to 6.5, 7, 8 and 11).
- [Decisions and resolutions](../notes/decisions-and-resolutions.md): U4 (EdDSA only), U6 (refresh
  lineages), U8 (step-up), U9 and U10 (bootstrap), U16 (removed routes), U17 (first-user hook
  deleted), resolution (c) (no authentication cookie).
- [Work packages](../notes/work-packages.md), the `users-identity-final` section: the term list.
- mod-core's plan worktree, `plan/phase-05-identity-final-coordination/001-mod-core-drift-scan.md`:
  the sibling slice this task mirrors.

## Procedure

1. Run the term search from [Validation](#validation) before making any edits, and record the match
   count for each term.
2. Classify each match as stale, legitimate, or unclear/out of scope (requirement 2).
3. Edit the stale matches. Commit after each group of files.
4. Re-run the search, then reconcile every remaining match against the scan report.
5. Run the build, test, lint and verify checks.

## Checkpoint hints

- After rewriting `docs/` and the READMEs.
- After `AGENTS.md`, `next-steps.md`, `.env.example` and `deploy/`.
- After the code comments, the manifest comments and the OpenAPI descriptions.

## Status

- Outcome: succeeded (2026-10-31). Matches 158 before, 148 after; all remaining are legitimate (history, removed-symbol lists, tests asserting rejection, stateless request-shape checks, setup-token first user, `oidc_state` HttpOnly cookie, live `corporation` legal_form value) or reported followups.
- Fixed comments: `api/cmd/server/main.go` (HS256/JWT_ALG, corporation), `api/internal/service/principal_credentials.go` (assume), `api/internal/authz/authz_integration_test.go` (first-user hook), `gui/src/components/{oidc-setup-gate,client-layout}.tsx` (`/v1/self` to `/v1/me`), `gui/src/components/{register-form,reset-password-page}.tsx` (stale `user_accounts.go` path).
- Validation: `make build`, `make test`, `make lint`, `cd model && make verify` pass. `docs/mf-standards` not scanned (submodule uninitialized).
