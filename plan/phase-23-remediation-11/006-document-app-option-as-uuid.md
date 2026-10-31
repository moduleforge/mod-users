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
