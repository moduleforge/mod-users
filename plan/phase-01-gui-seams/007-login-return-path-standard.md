# Standard Login Return-Path Handling in AuthPage and LoginForm

## Purpose and scope

Applies the "standard unless there is a design reason" principle to the one seam the original plan left app-owned for convenience: after a local login, every app had to read its own `?return=`, sanitize it, pass it as `returnPath` for the OIDC round trip, and navigate. Now that `unauthenticatedReturnParam` (task 002) makes the library itself write `?<param>=<path>` on a 401, the library should also read it back. Opt-in by configuration so behavior is identical for apps that do not set `unauthenticatedReturnParam`. Depends on tasks `002` (config field) and `003` (`isSafeReturnPath`, the `AuthPage`/`LoginForm` prop edits). Scope: `gui/src/components/{auth-page,login-form}.tsx`, `gui/src/lib/` helper, `gui/src/index.ts` (if a helper is exported), tests, stories.

## Requirements

1. Helper in `gui/src/lib/return-path.ts` (task 003's file): `readReturnPath(): string | null`. Returns `null` unless `unauthenticatedReturnParam` is configured (non-null) and `window` exists; otherwise reads that query param from `window.location.search` and returns it only if `isSafeReturnPath` accepts it. Reads at call time, no module state.
2. `LoginForm` and `AuthPage`: the effective return path is `props.returnPath ?? readReturnPath()` (an explicit prop always wins; the existing `returnPath` JSDoc is updated). It is used for the OIDC `start` URL exactly as `returnPath` is today. The callbacks widen compatibly: `LoginForm onSuccess?: (returnPath: string | null) => void` and `AuthPage onAuthenticated?: (returnPath: string | null) => void`, invoked with the effective return path (`null` when none). Existing callbacks that declare no parameter keep compiling and behaving the same.
3. Document that apps then navigate with one line (`onAuthenticated={(r) => router.replace(r ?? '/')}`), that the value is already validated by `isSafeReturnPath`, and that apps that do not configure `unauthenticatedReturnParam` see no change (the argument is `null` unless they pass `returnPath` themselves, which is then echoed).
4. Tests: with no param configured, `?return=/x` is ignored (OIDC start URL unchanged, callback receives `null`); with `unauthenticatedReturnParam: 'return'`, `?return=/open/3` reaches both the OIDC start URL and the callback; unsafe values (`//evil.test`, `https://x`, `javascript:x`) are dropped to `null`; an explicit `returnPath` prop wins; works with `unauthenticatedReturnParam: 'next'` (custom name); existing `AuthPage`/`LoginForm` tests pass unchanged. Update stories only if signatures require it.

## Validation

- `cd gui && bun run typecheck && bun test` pass; existing call sites with zero-argument callbacks compile.
- `git diff --stat` touches only `gui/src/`.

## Metadata

architectural_impact: true

## References

- [`../notes/seam-design.md`](../notes/seam-design.md), [`../overview.md`](../overview.md), tasks `002`, `003`
- `gui/src/components/auth-page.tsx`, `login-form.tsx`, `gui/src/lib/return-path.ts`

## Checkpoint hints

- After the helper and its tests
- After the component prop changes and tests
