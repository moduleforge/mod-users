# Document the App Option as a UUID

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses finding paQk. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. The `app` option/prop doc comments in AuthContext RegisterOptions, LoginForm, RegisterForm, EmailCodePage and AuthPage say app uuid, matching api/openapi.yaml and the api.ts types.

## Validation

1. grep -rn 'app (slug)' gui/src returns nothing; bun run typecheck passes.

## References

- Finding `paQk` in this plan's `plan/findings.yaml`.

## Status

- Outcome: succeeded
- Date: 2026-10-31
- Validation: check 1 passed. `grep -rn 'app (slug)' gui/src` returns nothing (exit 1); `bun run typecheck` (gui) exits 0, log at `.flow/validation-logs/01-typecheck.log`.
- Affected source files:
  - `gui/src/lib/auth-context.tsx` (RegisterOptions `app`)
  - `gui/src/components/register-form.tsx`
  - `gui/src/components/login-form.tsx`
  - `gui/src/components/email-code-page.tsx`
  - `gui/src/components/auth-page.tsx`
  - `gui/src/stories/LoginForm.stories.tsx` (task-caused drift: story doc comment still said slug; required for the validation grep to return nothing)
- Assumptions: none declared. `api/openapi.yaml` and `gui/src/lib/api.ts` already describe `app` as uuid, so no change there.
