# Persist Pending Profile Selection and Return Path Across Reload

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses findings b1HZ and AReg. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. After a sign-in answering selection_required, a reload of the tab restores the selection state (selectionRequired and the profile list, stored per tab or re-read) instead of calling session.get and signing out; doRefresh with a refresh token but no profile uuid surfaces selection-required rather than ending the tab session.
2. SelectProfilePage accepts (or restores from the same per-tab state) the return path handed over by onSelectionRequired and resumes at it after a profile is chosen, validated with isSafeReturnPath.

## Validation

1. auth-context/api tests: commit a selection_required response, simulate a reload (fresh provider over the same sessionStorage), and assert selectionRequired is true with the stored profiles and the refresh token kept.
2. select-profile-page test: choosing a profile navigates to the stored safe return path; an unsafe path falls back to the default.
3. cd gui && bun test, bun run typecheck; make build.gui and make lint.gui pass.

## References

- Finding `b1HZ` in this plan's `plan/findings.yaml`.
- Finding `AReg` in this plan's `plan/findings.yaml`.

## Status

Succeeded, 2026-10-31. Pending selection (profile list under `<key>.selection`, return path under `<key>.selection_return`) is persisted per tab by `commitTokenResponse` and `finishSignIn`; `establishSession` restores it on reload; `doRefresh` with a refresh token and no profile uuid publishes selection-required and throws `selection_required` instead of ending the session; `SelectProfilePage` resumes at the validated return path (prop, else stored, else `defaultReturnPath`). Validation: bun test (431 pass), typecheck, make build.gui, make lint.gui all green. Files: `gui/src/lib/config.ts`, `gui/src/lib/api.ts`, `gui/src/lib/auth-context.tsx`, `gui/src/lib/sign-in.ts`, `gui/src/components/select-profile-page.tsx`, tests in `gui/src/lib/auth-context.test.tsx` and `gui/src/components/profile-switcher.test.tsx`.
