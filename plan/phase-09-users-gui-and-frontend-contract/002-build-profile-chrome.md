# Build Profile Chrome

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Build the always-visible profile chrome of `@moduleforge/users-gui` on the auth context from task
001: the profile switcher with locked profiles and their remedies, the landing profile-selection
page, the invitations view, the impersonation banner, and the updated layout, navigation and
route-guard components. Suggested role: `plugins/flow/roles/developer-react.md`. No standard skill
covers this task.

Depends on task 001 (`rebuild-api-client-and-auth-context`). Parallel-eligible with tasks 003, 004
and 005.

## Requirements

1. **Profile switcher** (new component, for example `gui/src/components/profile-switcher.tsx`):
   lists the profiles from the auth context with personal versus party presentation
   (`ProfileView.kind`, `party.display_name`), marks the active one, and calls `switchProfile(uuid)`
   for available profiles. Locked profiles (`available: false`) are shown, not hidden, with their
   `lock.reason` in plain language and an action for the `lock.remedy`: for
   `sign_in_with_provider`, start the provider step-up (`POST /v1/session/step-up/oidc/{provider}/start`,
   redirect to `authorize_url`); other remedy kinds the API defines get a readable description. A
   switch replaces only the current tab's token.
2. **Profile-lock surface**: when the auth context's `profileLock` is set (from
   `401 users.profile_unavailable`), show the reason and remedy and offer switching to another
   available profile or signing out.
3. **Landing selection page** (new component mounted at task 001's route constant): shown when
   `selectionRequired` is true; lists the profiles with availability and calls `selectProfile(uuid)`.
4. **Invitations view** (new component): lists `invited` profiles from `GET /v1/me/profiles` with
   accept and decline actions, and a leave action for active party profiles; refreshes the profile
   list afterwards. Surface `users.step_up_required` through the step-up hook from task 001.
5. **Impersonation banner** (new component): visible whenever the session is impersonating; shows
   the impersonated profile, the remaining time, renew (`POST /v1/impersonations/current/renew`) and
   end (`DELETE /v1/impersonations/current`) actions, and restores the parent session state on end.
6. **Existing components**: update `gui/src/components/sidebar-nav.tsx` (switcher, invitations
   entry, no `isAdmin`), `require-auth.tsx` (redirect to selection when `selectionRequired`, lock
   surface when `profileLock`; replace the admin gate since `isAdmin` no longer exists — gate on
   authentication only and document that admin authority is enforced by the server's 403),
   `client-layout.tsx` (banner and chrome placement). If removing `RequireAuth`'s admin prop changes
   its public props, record the change for `gui/README.md`.
7. **Exports and stories**: export the new components and props from `gui/src/index.ts` (own
   section only); add or update stories under `gui/src/stories/` (`SidebarNav`, `RequireAuth`, and
   new stories for the switcher with locked profiles, selection page, invitations and banner).
8. **Tests**: component tests for switching, locked-profile rendering and remedy action, selection
   flow, invitation accept/decline/leave, banner renew/end, and `RequireAuth` redirects.
9. **`gui/README.md`**: document the new components and the changed `RequireAuth`/`SidebarNav` props.

## Validation

- `make -C gui test` passes for the new and updated tests.
- `make -C gui lint` reports no errors in the files this task owns (`sidebar-nav.tsx`,
  `require-auth.tsx`, `client-layout.tsx`, the new components, their tests and stories).
- `make -C gui build` succeeds, or fails only in files owned by tasks 003 to 005 (list them).
- `grep -rn "isAdmin" gui/src/components/{sidebar-nav,require-auth,client-layout}.tsx` returns
  nothing.
- Ladle stories for the new components render (`cd gui && bun run preview:build`), or the report
  states why the Ladle build could not run.

## Metadata

architectural_impact: false

## Assumptions

- The sibling `../mod-core/gui` (bun workspace member, pointed at mod-core's plan worktree by
  `scripts/link-siblings.sh`) is built with `bun run build` before `make -C gui build`/`lint` (see
  `.claude/CLAUDE.md`).
- Task 001 is merged; use its auth-context API and route constants without changing them. If a
  needed value is missing, add it to the auth context in a minimal, additive way and state it in the
  report.
- Lock reasons and remedy kinds come from the merged mod-users handlers (`ProfileView.lock`); read
  them from `api/openapi.yaml` or the handler code rather than inventing new ones.
- Party administration screens (inviting members, authn policies, confinements) and starting an
  impersonation are out of scope for this phase.

## References

- [API surface section 7](../notes/mod-users-api-surface.md#7-http-api) — `ProfileView`, `/v1/me/profiles`,
  `/v1/session/switch`, `/v1/impersonations`.
- [API surface section 12](../notes/mod-users-api-surface.md#12-frontend-contract-resolution-c).
- [Decisions](../notes/decisions-and-resolutions.md) — resolution (a) (locked collaborator
  profiles), U16 (no `isAdmin`).
- [Work packages, phase `users-gui-and-frontend-contract` item 2](../notes/work-packages.md).

## Checkpoint hints

- After the profile switcher and lock surface with tests.
- After the selection page and invitations view.
- After the impersonation banner and the layout/nav/guard updates.
- After stories, exports and `gui/README.md`.
