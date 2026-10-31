# Update Architecture Docs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Update mod-users' architecture and specification documents to reflect the identity-model changes
this plan implements in phases 6 to 9: principals and credentials replacing `user_accounts`,
sessions as the revocation unit, the EdDSA-only access-token carrier, profiles and party gating,
impersonation, setup-token bootstrap and the profile-as-subject authorization engine. Follow the
`update-architecture-docs` task-procedure
(`plugins/flow/task-procedures/update-architecture-docs/SKILL.md`) for the existing files, and
additionally author one new architecture document the procedure does not create.

The scope also covers changes folded in from the phase 8 gate findings pKAO (owning-party gate
documentation), Q1lB and Hd71 (removal of the `/assume` route and the admin-assume JWT), and EfE8
(stale D9 SSH-key text), recorded as findings in this plan's `plan/findings.yaml`. Those findings
were dismissed as already planned against this task on the condition that this task covers them.

Out of scope: `README.md` beyond the single link named below, `AGENTS.md`, sub-project READMEs,
`docs/consumer-upgrade-notes.md` and the other operational docs (tasks
`002-write-consumer-upgrade-notes` and `003-review-repository-docs`), and code changes.

## Requirements

1. `role_doc: plugins/flow/roles/architect-backend.md` — the implications are primarily backend,
   API and component changes (with a data-model share the backend architect covers).
2. **Implementation task documents that surfaced the architectural implications** (paths relative
   to the plan worktree). The implementation phases were decomposed concurrently with this phase,
   so their files are named by directory; pass every task document in each directory as
   `completed_task_docs`:
   - `plan/phase-06-users-identity-core/*.md` — baseline schema rewrite into `mod_users`, queries,
     token carrier and authentication middleware, identifiers/credentials/challenges, sessions and
     the `/v1/auth` and `/v1/session` routes, OIDC resolver, self-service and principals wiring.
   - `plan/phase-07-parties-profiles-and-bootstrap/*.md` — profile lifecycle and invitations, party
     gating administration, outside collaborators and continuous compliance, setup-token
     bootstrap, party-aware SSH profile derivation.
   - `plan/phase-08-users-authorization-engine/*.md` — engine rewrite (profile subject, own arm,
     owned scope, `AuthorizeTypeIn`, assurance gate), impersonation, the `credentials` operation
     and app `login` gate, personal party.
   - `plan/phase-09-users-gui-and-frontend-contract/*.md` — the Bearer-only, per-tab frontend
     contract (relevant only where the architecture docs describe the client contract).

   Also read `plan/overview.md`, `plan/notes/decisions-and-resolutions.md` (decisions U1 to U20;
   the durable ones become recorded architecture decisions) and
   `plan/notes/mod-users-api-surface.md` (the contract as planned). Where merged code differs from
   these notes, the merged code wins; note the difference in the report.
3. **Existing architecture and spec files to review and update** (repo-relative; pass as
   `arch_files`):
   - `docs/architecture.md` — data model; authentication flow; the multi-channel account model
     rewritten as principals (users, service accounts, system actors), identifiers and credentials;
     profiles, parties and party authn policies; the authorization engine's consumer side; key
     decisions (U3 schema ownership, U4 EdDSA only, U5 per-request liveness in middleware, U6
     refresh lineages, U7 OIDC handoff, U8 step-up as factor composition, U9/U10 bootstrap, U1/U2
     personal party ownership, U13 SQLSTATE codes MF101 to MF103, U15 app `login` gate, U16 route
     replacement, U17 first-user hook removal).
   - `docs/mod-users-spec.md` (the only `docs/*-spec.md` match) — use cases and the API definition
     (`/v1/auth*`, `/v1/session*`, `/v1/me*`, `/v1/principals*`, `/v1/parties/{uuid}/*`,
     `/v1/profiles/{uuid}`, `/v1/impersonations*`, `/v1/bootstrap`; removed routes gone).
   - `docs/architecture/ssh-keys.md` — SSH keys as `ssh_key` credentials of a principal, management
     under the `credentials` operation instead of `assume`, `SSHKeyResolver.ResolveCredential` and
     party-aware `AuthContextForOwner` profile derivation.
   - `docs/jwt-eddsa.md` — EdDSA-only carrier, claims (`sid`, `prof`, `acr`, `amr`, `act`, audience
     handling), the persisted `mod_users.auth_signing_keys` key when `JWT_SIGNING_KEY` is unset,
     `JWT_SECRET` reduced to the optional OAuth-state key, HS256 and `JWT_ALG` removed.
4. **New document (outside the procedure's scope; author it before running the procedure):**
   `docs/architecture/sessions-and-tokens.md`, following the project's existing
   `docs/architecture/ssh-keys.md` conventions and the markdown style standards (Title Case H1,
   `## Purpose and scope` first, sentence-case sections). Cover: the `sessions` row as revocation
   unit and its factors and assurance (`acr`, `amr`, `auth_time`); per-request liveness
   (`mod_users.session_liveness`) and profile availability (confinement, party gate, `min_acr`,
   outside-collaborator confirmations); login landing and profile switching; per-tab refresh
   lineages with rotation and reuse detection, and fork; step-up; the OIDC handoff code;
   client-credentials tokens; impersonation sessions (reason, TTL, renewal cap, no nesting); and
   the setup-token bootstrap. Link it from `docs/architecture.md` (inline, at the first relevant
   mention) and add one link to it in `README.md`'s documentation list; make no other `README.md`
   edit.
5. Write durable content into the project docs; carry the reasoning of each decision into the
   docs rather than referencing decision ids. Never link any plan document from a project
   document: the task-document paths in item 2 and the plan notes are plain code text in this task
   document, not links to copy. mod-core's ADR (`docs/architecture/identity-model.md` in mod-core)
   may be referenced as the source of the model, using the project's existing cross-repository
   reference style.
6. Edit stale sections in place per the procedure; do not restructure accurate content.
7. **Decision D9 (SSH keys as credentials; EfE8).** Rewrite the D9 text in `docs/architecture.md` and
   `docs/architecture/ssh-keys.md` (lines ~51-55) to the credentials operation: management of SSH keys
   is the `credentials` operation (ownership_confers true, max_auth_age 10 minutes) on the principal
   entity under `/v1/principals/{uuid}/credentials*`. Drop the `assume` and manage-implies-assume
   wording. Mark the old `/v1/user-accounts` SSH-key routes as removed.
8. **Owning-party gate (pKAO).** Document in `docs/architecture.md` and `docs/mod-users-spec.md`
   where party gating is described: `credential_rule` and `min_acr` are enforced on outside actors;
   a party record is its own owning party (a legal entity gates its own record); platform operator
   profiles are exempt from the owning-party gate; nested ownership is one level only (no ancestor
   walk); and while impersonating, the impersonated actor's rules apply to every operation, so
   impersonation never skips a party's `credential_rule`. Carry the reasoning into the docs, not the
   decision ids.
9. **Removal of `/assume` and the admin-assume JWT (Q1lB, Hd71).** Rewrite `docs/mod-users-spec.md`
   section 10 and lines ~114-120, 218, 308 and 362, and `docs/architecture.md` lines ~84, 235, 267
   and 273, to describe `/v1/impersonations` child sessions and the `impersonate` operation instead.
   Remove the `/assume` route wording and the admin-assume JWT wording everywhere they appear in these
   files, so that no remaining text describes an assume route or assumed-user token.

## Validation

- Each of `docs/architecture.md`, `docs/mod-users-spec.md`, `docs/architecture/ssh-keys.md` and
  `docs/jwt-eddsa.md` was read in full and either updated or reported as already correct, with the
  sections changed listed in the report.
- `docs/architecture/sessions-and-tokens.md` exists, opens with `# Sessions and Tokens` followed by
  `## Purpose and scope`, and covers every topic in requirement 4.
- `grep -n "sessions-and-tokens.md" docs/architecture.md README.md` finds a link in each.
- `grep -rn "](.*plan/" docs/architecture.md docs/mod-users-spec.md docs/architecture/ docs/jwt-eddsa.md`
  finds no link into the plan directory.
- `grep -rniE "user_accounts|account_holder|HS256|JWT_ALG|X-Step-Up-Token|/v1/self|/v1/assume|SudoUserUUID|AssumedUser|first.user hook"`
  over the four reviewed files and the new file returns only intentional historical mentions
  (none expected), each justified in the report.
- Every route, table, claim and configuration variable the updated docs name exists in the merged
  code (spot-check against `api/openapi.yaml`, `model/migrations/sql/0100_baseline.sql` and
  `api/internal/config/config.go`).
- Relative links in the edited and new files resolve.
- `grep -rniE "/assume|manage.implies.assume|admin.assume|assumed.user" docs/architecture.md docs/mod-users-spec.md docs/architecture/ssh-keys.md`
  returns no route, operation or token wording (any remaining hit is a justified historical mention).
- `grep -rn "/v1/user-accounts" docs/architecture.md docs/architecture/ssh-keys.md` shows only text
  that states those SSH-key routes were removed.
- `grep -n "/v1/impersonations" docs/mod-users-spec.md docs/architecture.md` finds the child-session
  description that replaces the assume wording, and `grep -n "/v1/principals/.*credentials"
  docs/architecture/ssh-keys.md` finds the credentials-operation description of D9.

## Metadata

architectural_impact: true

## Assumptions

- Phases 6 to 9 are merged into the plan branch; their task documents exist in the directories
  named in requirement 2.
- Tasks in earlier phases updated `AGENTS.md`, `README.md`, `model/README.md` and `gui/README.md`
  rows for the code they changed (work-packages convention); this task does not re-review them.

## References

- `plugins/flow/task-procedures/update-architecture-docs/SKILL.md` — the procedure for the existing
  files.
- [Decisions and resolutions](../notes/decisions-and-resolutions.md) — decisions U1 to U20 and the
  ADR resolution table.
- [Mod-users API surface](../notes/mod-users-api-surface.md) — schema (section 2), SQL functions
  (3), SQLSTATE codes (4), token carrier (5), HTTP API (7), configuration (8).
- [Plan overview](../overview.md) — what changes and what must not change.

## Checkpoint hints

- After authoring `docs/architecture/sessions-and-tokens.md` and its two links.
- After updating `docs/architecture.md`.
- After updating `docs/mod-users-spec.md`.
- After updating `docs/architecture/ssh-keys.md` and `docs/jwt-eddsa.md`.

## Status

- Outcome: succeeded (2026-10-31).
- Validation: new `docs/architecture/sessions-and-tokens.md` covers every topic of requirement 4 and is linked from `docs/architecture.md` (overview, further reading) and `README.md`; no link into `plan/` from the edited docs; relative links and anchors in the edited and new files resolve (script check, 0 dangling); the stale-term and `/assume` greps return only intentional removal statements; routes, tables, SQL functions, claims and configuration variables the docs name were spot-checked against `api/openapi.yaml`, `model/migrations/sql/0100_baseline.sql`, `moduleforge.module.yaml` and `api/internal/config/config.go`.
- Files: `docs/architecture/sessions-and-tokens.md` (new), `docs/architecture.md`, `docs/mod-users-spec.md`, `docs/architecture/ssh-keys.md`, `docs/jwt-eddsa.md`, `README.md` (one documentation-list link).
- Decisions: logout while impersonating is documented as the client restoring and revoking the administrator session (the server's `POST /v1/session/logout` revokes the caller's own session, which is the impersonation child when an impersonation token is used). The OIDC start `credentials: 'include'` exception is documented in the new doc, the spec and the architecture GUI section; `gui/README.md` already states it. The impersonation data-protection-class design is described as planned, not built.
