# Register User And Profile Display Renderers Through The Manifest

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `xgGT` (bug). The API surface requires the user and profile display renderers (U19) to be registered, but `moduleforge.module.yaml` has no path to `RegisterDisplayRenderers`, so generated apps render no names. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-go.md`

## Requirements

1. `moduleforge.module.yaml` wires the user and profile display renderers (U19) with no mfgen change, by passing `service:displayRegistry` into a `usersservice` constructor that registers them (the mod-workflows pattern), for example `NewDirectoryService` or a dedicated constructor-style registration service.
2. `api/cmd/server/main.go` uses the same constructor path, so the dev server and generated apps register renderers identically.

## Validation

1. A unit test: constructing the service with a `*display.Registry` registers renderers for the user and profile types.
2. The manifest validates (the project's manifest check or mfgen dry run, if available), and `make build`, `make test` and `make lint` pass.

## References

- Finding `xgGT` in this plan's `plan/findings.yaml` (bug, medium): `moduleforge.module.yaml` has no path to `RegisterDisplayRenderers`, so generated apps render no user or profile names.
