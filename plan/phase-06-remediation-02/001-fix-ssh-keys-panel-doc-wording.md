# Correct SSHKeysPanel Action-Required And Ordinal Wording In Architecture Doc

## Purpose and scope

This remediation task closes findings `3oGr` and `uNF5` from the phase-02 gate's correctness-lens review. It targets the plan branch `plan/mfgit-upstream-modules` and lands through the ordinary per-task loop (its own worktree, merged back into the plan branch).

`docs/architecture.md`'s GUI component library section, in the sentence introducing `SSHKeysPanel`, carries two inaccuracies against the merged `gui/src/components/ssh-keys-panel.tsx`:

1. It says the panel delegates non-step-up action-required responses to an optional `onActionRequired` callback "with an inline fallback alert when none is supplied." In the merged code, `handleActionRequired` always calls `setNotice(...)` and also invokes `onActionRequiredRef.current?.(error)` when present — the inline alert renders every time, not only as a fallback.
2. It calls `SSHKeysPanel` "a sixth component," but the same paragraph names seven other pieces before this sentence (`LoginForm`, `RegisterForm`, `AuthPage`, `OidcCallbackPage`, `ForgotPasswordPage`, `ResetPasswordPage`, `EmailCodePage`), so a literal count reads as the eighth named item, not the sixth.

## Requirements

1. In `docs/architecture.md` (GUI component library section, the `SSHKeysPanel` sentences), state that for every non-step-up action-required response the panel always shows an inline alert, and also calls the optional `onActionRequired` callback when one is supplied. Do not describe the alert as a fallback shown only when no callback is supplied.
2. In the same paragraph, remove the inaccurate ordinal "a sixth component" (seven components are named before it). Either drop the ordinal or name the counted set explicitly, e.g. "an additional component, not a full-page composition."
3. Change nothing else in the paragraph or file. In particular, leave the Data model section's `mod_users` grant text alone.

## Validation

1. `grep -n "fallback alert when none is supplied" docs/architecture.md` returns no hits. The new wording matches `gui/src/components/ssh-keys-panel.tsx`'s `handleActionRequired`, which calls `setNotice` unconditionally and then the optional callback.
2. `grep -n "sixth component" docs/architecture.md` returns no hits.
3. `git diff --stat`, excluding `plan/`, touches only `docs/architecture.md`.

## References

- Findings `3oGr` and `uNF5` in this plan's `plan/findings.yaml`.
