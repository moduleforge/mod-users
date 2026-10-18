# Write Consumer Upgrade Notes

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Add one section to `docs/consumer-upgrade-notes.md` that tells every consumer of mod-users (host
apps, mfgen-generated composition roots and the sibling modules) what the identity-model rewrite
removed, renamed and added, and what to use instead. No standard skill covers this; it is a
technical-writing task against the merged code. Only `docs/consumer-upgrade-notes.md` is edited;
the existing sections are reviewed for statements the rewrite made false.

Suggested role doc: `plugins/flow/roles/tech-writer.md`.

## Requirements

1. Add a section (sentence-case heading, for example `## Identity model rewrite`) placed before
   `## Known limits and open items`, listing every removed and renamed:
   - **Go symbol** in the public packages (`api/auth`, `api/localAuthz`, `api/usersservice`,
     `api/handlers`, `api/handlers/authhandlers`, `model/db`): for example `auth.Principal`,
     `auth.UserContext`, `auth.UserResolver`, `Principal.Roles`, `UserContext.AppRoles`,
     `usersdb.UserAccount`, `users.NewFirstUserHook`, `FirstUserHookFn`, `SetFirstUserHook`,
     `ResolveActor`, and their replacements (`auth.Identity`, `auth.Authenticator`,
     `ResolveCredential`, `AuthContextForOwner`, `CredentialService.IssueAPIKeyInTx`,
     `ProfileService.InviteInTx`, `ProfileService.AdmitInTx`, `ProfileLifecycleObserver`,
     `PersonalPartyService.EnsurePersonalParty` (with its commit-first visibility rule, decision
     U28), `DirectoryService.PrincipalSummaries` and `ProfileSummaries`,
     `usersservice.ReadPrincipalSummaries`, `CredentialService.LinkOIDCInTx`,
     `auth.IssueAudienceToken`), and the binding `mod_users.user_identifiers` read surface.
   - **HTTP route**: removed `/v1/self*`, `/v1/user-accounts*`, `/v1/assume`, `grant-admin`/
     `revoke-admin`, `/v1/apps/{uuid}/user-accounts*`; added `/v1/me*`, `/v1/session*`,
     `/v1/principals*`, `/v1/parties/{uuid}/*`, `/v1/profiles/{uuid}`, `/v1/impersonations*`,
     `/v1/bootstrap`, `/v1/auth/handoff`, `/v1/auth/token`; app membership as a `login` grant
     through mod-authz's grants API.
   - **Configuration variable**: removed `JWT_ALG`, `AUTH_REQUIRE_STEP_UP`, the admin-role claim
     setting; changed `JWT_SECRET` (optional OAuth-state key only) and `JWT_SIGNING_KEY` (optional;
     persisted key when unset); added the `AUTH_*` variables with their defaults.
   - **Header, cookie, claim and token behavior**: `X-Step-Up-Token` removed; no authentication
     cookie (Bearer only; `oidc_state` is the only cookie); HS256 removed; carrier claims `sid`,
     `prof`, `acr`, `amr`, `act`; 1-hour access tokens with per-tab refresh lineages; the OIDC
     callback returns `#handoff=<code>` instead of a token.
   - **Database**: tables moved into the `mod_users` schema; deleted tables (`user_accounts`,
     `auth_local`, `email_codes`, `password_resets`, `auth_oidc_identities`, `anon_tokens`,
     `apps_user_accounts`, the `system_actors` copy, `auth_jwt_secrets`); SQLSTATE codes MF101 to
     MF103; MF002 gone.
   - **Manifest** (`moduleforge.module.yaml`): removed `hooks.firstUserGrant` and any renamed
     service, middleware and route names.
2. Add per-consumer migration notes for mod-audit, mod-tokens, mod-billing, mod-workflows and
   mod-tasks, mod-repos, app-mfmanager, and the non-federated host apps and mfgen, carrying the
   content of the API surface note's section 10 into the doc as durable prose.
3. Derive the lists from the merged code, not only the plan notes: diff the public packages,
   `api/openapi.yaml`, `api/internal/config/config.go` and `moduleforge.module.yaml` against the
   plan branch's base (`git diff <base>...HEAD -- <paths>`), and reconcile with the API surface
   note. Where they disagree, the merged code wins; list each disagreement in the report.
4. Review the existing sections (EdDSA verify-only key, token hardening, CORS, cookie base path,
   GUI, OIDC issuer TLS verification, HTTP error precedence) and correct statements the rewrite
   made false (for example HS256 options, authentication cookies, step-up headers). Leave accurate
   content untouched.
5. Never link a plan document from the doc; plan paths named here are plain code text.

## Validation

- `docs/consumer-upgrade-notes.md` contains the new section and the per-consumer notes.
- Every removed public Go identifier found by `git diff <base>...HEAD -- api/auth api/localAuthz
  api/usersservice api/handlers model/db` appears in the section with its replacement or "no
  replacement".
- Every route removed from or added to `api/openapi.yaml` in that diff appears in the section.
- Every variable removed from or added to `api/internal/config/config.go` in that diff appears.
- `grep -nE "HS256|X-Step-Up-Token|JWT_ALG|AUTH_REQUIRE_STEP_UP" docs/consumer-upgrade-notes.md`
  matches only lines stating their removal.
- `grep -n "plan/" docs/consumer-upgrade-notes.md` finds no link into the plan directory.
- Relative links in the file resolve.

## Assumptions

- Phases 6 to 9 are merged into the plan branch. This task runs in parallel with
  `001-update-architecture-docs` and edits no file that task edits.

## References

- [Mod-users API surface](../notes/mod-users-api-surface.md) — sections 6 (Go packages), 7 (HTTP
  API), 8 (configuration), 10 (notes for specific consumers), 11 (manifest).
- [Decisions and resolutions](../notes/decisions-and-resolutions.md) — U4, U7, U8, U16, U17 and the
  migration impact table.
