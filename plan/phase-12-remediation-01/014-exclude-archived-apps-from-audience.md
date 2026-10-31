# Exclude Archived Apps From Token Audience Checks

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `nD8J` (bug, low). The audience liveness checks accept archived apps as token audiences. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-go.md`

## Requirements

1. Add `AND a.archived_at IS NULL` to both liveness audience-registration queries (`EXISTS (SELECT 1 FROM public.apps WHERE slug = ANY(...))`) in `api/internal/auth/authenticator.go`, and to the underlying SQL and baseline liveness functions, so archived apps stop being accepted token audiences.
2. Note the audience policy in the docs.

## Validation

1. A test shows a token whose `aud` is an archived app is rejected, and a token whose `aud` is an active app is accepted.
2. `make build`, `make test` and `make lint` pass in `api/`.

## References

- Finding `nD8J` in this plan's `plan/findings.yaml` (bug, low): the audience liveness queries do not exclude archived apps, so tokens addressed to archived apps are accepted.

## Status

- Outcome: succeeded (2026-10-25).
- Both liveness audience queries in `api/internal/auth/authenticator.go` now join `public.entities` and require `archived_at IS NULL` (apps carry no `archived_at` of their own; it lives on the entity row). The baseline liveness functions do not reference apps, so no SQL migration change was needed.
- Test: `TestApp_ArchivedAppIsNotAnAcceptedAudience` in `api/internal/integration/app_integration_test.go` (active app token 200, archived 401) passes against a throwaway Postgres.
- Docs: "App audiences" bullet added to Security requirements in `docs/mod-users-spec.md`.
- `make build`, `make test`, `make lint` in `api/` pass.
