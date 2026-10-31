# Re-Export API Key And Profile Summary Types From Facade

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates finding iZ3E (facade omits API key and profile summary type aliases).

The work targets the plan branch plan/identity-model-impl and lands through the ordinary per-task loop.

## Requirements

1. In `api/usersservice/service.go`, add type aliases `IssueAPIKeyInput = inner.IssueAPIKeyInput`, `IssuedAPIKey = inner.IssuedAPIKey`, and `ProfileSummary = inner.ProfileSummary`, each with a doc comment in the style of the neighboring aliases.
2. Place each alias next to the service it belongs to: `IssueAPIKeyInput` and `IssuedAPIKey` with `CredentialService`, and `ProfileSummary` with `DirectoryService`.

## Validation

1. `go build ./...` and `go vet ./usersservice/...` pass in `api/`.
2. A compile-only check (an existing or new facade test in package `usersservice_test`) names all three aliases.

## References

- Finding iZ3E in this plan's `plan/findings.yaml`.
