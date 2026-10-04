# Correct AuthProvider 401 Navigation Wording In GUI README

## Purpose and scope

Remediates finding aaEB: `gui/README.md` says `AuthProvider` navigates through `onNavigate`/`loginPath` when its own `/v1/self` check returns 401, but in `gui/src/lib/auth-context.tsx` only `refreshUser()`'s 401 branch does that (it calls `logout()`, which navigates). The mount-time `/v1/self` check on a 401 only clears the stored token. Work targets the plan branch `plan/users-gui-integration-seams` and lands through the ordinary per-task loop. Scope is limited to `gui/README.md`; no code is edited.

## Requirements

1. In `gui/README.md`, reword the `AuthProvider` `loginPath` bullet (around line 70) so the provider navigates via `onNavigate` on `logout()` and when `refreshUser()` receives a 401. State that the mount-time `/v1/self` check on a 401 only clears the stored token and sets the token to null, with the fetch-layer redirect handling it.
2. Reword the `AuthProvider` row of the components table (around line 84) the same way: navigates through `onNavigate` on logout, on a 401 from `refreshUser()`, and on an action-required response. Remove the claim that a 401 from `/v1/self` navigates.
3. Optionally note that without `unauthenticatedReturnParam` the default fetch-layer redirect always fires, even from the login page, while the return param enables the same-path guard that can skip it.
4. Change no other README content. Do not edit any code.

## Validation

1. `grep -n 'v1/self' gui/README.md` shows no remaining statement that a mount-time `/v1/self` 401 navigates through `onNavigate`/`loginPath`.
2. The reworded text matches `gui/src/lib/auth-context.tsx`: `refreshUser` 401 leads to `logout()` leads to `navigate(resolvedLoginPath)`, and the mount-time 401 only clears the token.
3. Only `gui/README.md` is changed (plus this task document's Status section).

## References

- Finding aaEB in this plan's `plan/findings.yaml` ("README overstates AuthProvider 401 navigation").
