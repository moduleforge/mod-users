# 001 AuthPage Passes Mode To Callback

## Purpose and scope
Flow-accounts 5F38: `gui/src/components/auth-page.tsx` calls the same `onAuthenticated(returnPath)` after login and registration, mode kept internal.

## Requirements
1. Add the mode as a second argument: `onAuthenticated(returnPath, mode: 'login' | 'register')`. Backward compatible (existing one-arg callbacks still type-check).
2. Update `login-return-path.test.tsx` and add tests for both modes; update docs/Ladle stories.

## Validation
- `cd gui && bun test && bun run typecheck && bun run build`.

## Status
- Outcome: succeeded (2026-10-10).
- Validation: `cd gui && bun test && bun run typecheck && bun run build` passed (225 tests).
- Files: gui/src/components/auth-page.tsx, gui/src/components/login-return-path.test.tsx, gui/src/stories/AuthPage.stories.tsx, gui/README.md.
