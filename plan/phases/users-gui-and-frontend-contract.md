# Users GUI and Frontend Contract

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Goals

Bring `@moduleforge/users-gui` onto the new API and implement the frontend contract of resolution
(c): per-tab token storage, one refresh lineage per tab, Bearer-only requests, the OIDC handoff
exchange, the always-visible profile chrome (switcher, locked profiles with remedies, landing
selection, invitations, impersonation banner), credential, identifier and session self-service with
step-up flows, and the bootstrap and OIDC configuration pages. It comes after the three API phases
so it codes against the final routes; it is independent of the downstream participants.

## Inputs

- The final mod-users HTTP API (API surface sections 7 and 12).
- mod-core's core-gui changes (per-tab api-client default, `credentials: 'omit'`, `AccountEditor`).
- Existing GUI code under `gui/src/` and `gui/README.md`; followups `HPJi` and `PRvD`.
- [Work packages](../notes/work-packages.md) for this phase.

## Outputs

- `gui/src/lib` client and auth context implementing per-tab tokens, refresh, fork and handoff.
- Profile chrome components, landing selection and invitation views, impersonation banner.
- Credentials, identifiers and sessions panels replacing the identities and SSH-key panels.
- Bootstrap page; OIDC configuration page and provider modals with the new provider fields.
- Stories and tests updated; `gui/README.md` documenting the frontend contract and exports.
