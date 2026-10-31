# Link Deploy And Model READMEs From The Root README

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `W55I`, `Kkmz`, `fWsd` and `xqMm` (all documentation). `deploy/k8s/README.md`, `deploy/local/README.md`, `deploy/serverless/README.md` and `model/README.md` are not reachable from the root `README.md`. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/tech-writer.md`

## Requirements

1. `README.md`'s documentation list links `deploy/k8s/README.md`, `deploy/local/README.md`, `deploy/serverless/README.md` and `model/README.md`, each with a one-line description; no other README restructuring and no plan links.

## Validation

1. The readme-reachability check reports none of the four files as unreachable; the relative links resolve.

## References

- Finding `W55I` in this plan's `plan/findings.yaml` (documentation, low): `deploy/k8s/README.md` is not reachable from `README.md`.
- Finding `Kkmz` in this plan's `plan/findings.yaml` (documentation, low): `deploy/local/README.md` is not reachable from `README.md`.
- Finding `fWsd` in this plan's `plan/findings.yaml` (documentation, low): `deploy/serverless/README.md` is not reachable from `README.md`.
- Finding `xqMm` in this plan's `plan/findings.yaml` (documentation, low): `model/README.md` is not reachable from `README.md`.

## Status

Succeeded, 2026-10-25. Added four documentation-list links (`model/README.md`, `deploy/local/README.md`, `deploy/k8s/README.md`, `deploy/serverless/README.md`) to `README.md`. Validation: all four relative link targets exist and are linked from `README.md`.
