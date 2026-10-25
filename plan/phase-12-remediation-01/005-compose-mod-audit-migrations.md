# Compose Mod-Audit Migrations Into The Model Schema

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `LPl2` (bug). The `compose` target in `model/Makefile` copies only the core and authz migrations, but the API server registers mod-audit's observer, so a database built from `schema/migrations` fails on the first observed write. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-go.md`

## Requirements

1. `model/Makefile` resolves `AUDIT_DIR` like `CORE_DIR` and `AUTHZ_DIR` (`go list -m github.com/moduleforge/audit-model`, falling back to `../../mod-audit/model`), and the `compose` target copies its migrations (`0400_audit_log.sql`) into `schema/migrations`.
2. `api/internal/integration/harness_integration_test.go` drops its temporary-copy workaround and uses the composed directory directly.

## Validation

1. `make -C model compose` produces `schema/migrations` containing the audit_log migration.
2. The api integration suite (`go test -tags=integration ./internal/integration/...`) passes against a database built from the composed directory.

## References

- Finding `LPl2` in this plan's `plan/findings.yaml` (bug, high): the compose target omits the mod-audit migrations that the API server's observer needs, so migrated databases fail on the first observed write.

## Status

Succeeded, 2026-10-25. `model/Makefile` now resolves `AUDIT_DIR` and `compose` copies the audit migrations; `api/internal/integration/harness_integration_test.go` drops the temp-copy workaround and applies the composed directory directly. Validation: `make -C model compose` yields `0400_audit_log.sql`; the api integration suite passed against a throwaway Postgres.
