# Build Bootstrap and OIDC Config Pages

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Add the first-run setup-token bootstrap page to `@moduleforge/users-gui` and update the OIDC
configuration page, setup gate and provider modals for the new provider fields and the U9 setup-token
lifecycle. Suggested role: `plugins/flow/roles/developer-react.md`. No standard skill covers this
task.

Depends on task 001 (`rebuild-api-client-and-auth-context`). Parallel-eligible with tasks 002, 003
and 004. This task owns `gui/src/lib/oidc-config.ts` and `gui/src/lib/oidc-provider.ts`.

## Requirements

1. **Bootstrap page** (new component, for example `gui/src/components/bootstrap-page.tsx`, mounted
   at task 001's route constant): reads `GET /v1/bootstrap/status`; when not bootstrapped, collects
   the setup token (sent as `X-Setup-Token`), the operator choice (`organization` with `legal_name`,
   `legal_form`, optional `jurisdiction`; or `natural_person` for single-person self-host) and the
   admin (`email`, `password`, `given_name`, `family_name`), posts `POST /v1/bootstrap`, and stores
   the `201` token response through the auth context so the tab lands on the admin profile. Handles
   `409 users.already_bootstrapped` (show "already set up", link to sign-in) and
   `401 users.setup_token_invalid`. Offer a legal-form selection consistent with mod-core's
   organization legal forms (read them from mod-core's API or `OrganizationForm` in core-gui rather
   than hard-coding a divergent list).
2. **Setup gate** (`gui/src/components/oidc-setup-gate.tsx`): route first-run traffic by
   `/v1/bootstrap/status` and `/v1/oidc-config/status` per U9 — the setup token is active while the
   operator is not bootstrapped or OIDC is unconfirmed; confirming OIDC no longer clears it while
   bootstrap is pending. Document the order the gate sends an operator through (OIDC configuration
   and bootstrap, in either order), and keep the `'ready' | 'setup'` render contract or extend it
   additively with a bootstrap state.
3. **OIDC config page** (`oidc-config-page.tsx`): replace `isAdmin` with "signed in": with a
   session, use the tab's Bearer token (session mode); without one and while the setup token is
   active, use token mode. A `403` in session mode is shown as "requires an operator admin profile".
   After a token-mode confirmation, do not assume the token is spent while bootstrap is pending
   (U9); direct the operator to the bootstrap page.
4. **Provider modals** (`oidc-provider-add-modal.tsx`, `oidc-provider-edit-modal.tsx`) and
   `gui/src/lib/oidc-provider.ts` types: add `party_uuid` (optional; party-scoped provider),
   `jit_mode` (`byo` / `managed` / `none`, with a short explanation of each) and `trust_acr`, coded
   against the merged `/v1/oidc-config/providers*` handlers and `api/openapi.yaml`. `managed` JIT
   needs a party; validate that client-side and surface the server's error otherwise.
5. **Requests**: every fetch in `oidc-config.ts` and `oidc-provider.ts` sends `credentials: 'omit'`
   and Bearer (or the setup token header) only; the OIDC `oidc_state` cookie is set and read by the
   server during the provider redirect and needs no client handling.
6. **Exports and stories**: export the bootstrap page and any new types from `gui/src/index.ts`
   (own section only); add a `BootstrapPage` story and update `OidcConfigPage.stories.tsx` (session
   mode, token mode, provider with `jit_mode: managed`).
7. **Tests**: bootstrap success, already-bootstrapped and invalid-token paths; setup gate routing
   for the four combinations of bootstrapped/OIDC confirmed; OIDC config page session versus token
   mode and the 403 message; provider modal validation and the new fields in the write body. Update
   `oidc-config-page.test.tsx` and `oidc-setup-gate.test.tsx`.
8. **`gui/README.md`**: document the bootstrap page, the first-run order, and the new provider
   fields.

## Validation

- `make -C gui test` passes for the new and updated tests.
- `make -C gui lint` reports no errors in the files this task owns.
- `make -C gui build` succeeds, or fails only in files owned by tasks 002, 003 or 004 (list them).
- `grep -rn "isAdmin" gui/src/components/oidc-*.tsx` returns nothing.
- `grep -n "credentials:" gui/src/lib/oidc-config.ts gui/src/lib/oidc-provider.ts` shows `'omit'` on
  every fetch.

## Metadata

architectural_impact: false

## Assumptions

- The sibling `../mod-core/gui` (bun workspace member, pointed at mod-core's plan worktree by
  `scripts/link-siblings.sh`) is built with `bun run build` before `make -C gui build`/`lint` (see
  `.claude/CLAUDE.md`).
- Task 001 is merged; use its client (`bootstrap` methods), auth context and route constants. If a
  needed value is missing, add it in a minimal, additive way and state it in the report.
- The merged `/v1/bootstrap` and `/v1/oidc-config` handlers (phase 7) and `api/openapi.yaml` are
  authoritative for shapes and status codes; record differences from the API surface note in the
  report.

## References

- [API surface section 7](../notes/mod-users-api-surface.md#7-http-api) — `/v1/bootstrap`,
  `/v1/oidc-config` rows and error codes.
- [Decisions U9 and U10](../notes/decisions-and-resolutions.md) — setup-token lifecycle and the
  bootstrap transaction.
- [Work packages, phase `users-gui-and-frontend-contract` item 4](../notes/work-packages.md).

## Checkpoint hints

- After the bootstrap page and its tests.
- After the setup gate and OIDC config page changes.
- After the provider modals and `oidc-provider.ts`.
- After stories, exports and `gui/README.md`.
