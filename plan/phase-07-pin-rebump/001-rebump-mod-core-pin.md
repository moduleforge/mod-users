# Rebump Mod Core Pin

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Re-bump mod-users' mod-core pin in `versions.lock.yaml`, so that CI and fresh clones build against the mod-core that carries the `core-handler-conformance` changes this plan relies on (the reserved slot's "re-bump mod-core pin after foundation changes"). This is the plan's final task. Follow [common section 7](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#7-lockfile-pins-consumer-apps-and-releasing) and [common rule 8](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#4-rules-all-plans).

**Consent gate.** `make pins.update` derives each SHA from the sibling's `origin/main`. At planning time (2026-10-11), mod-core's local `main` was 243 commits ahead of `origin/main`, and `origin/main` did not contain the `core-handler-conformance` merges (for example `4acf517`, `76535fc`). Pushing mod-core is a user decision that this task never makes. The task proceeds only if the dispatch states that the user has consented and mod-core has already been pushed. This task itself never pushes anything, in any repo.

## Requirements

1. Check the gate without changing anything: `git -C /Users/zane/playground/moduleforge/mod-core fetch origin`, then `git -C /Users/zane/playground/moduleforge/mod-core merge-base --is-ancestor 4acf517 origin/main`. If it fails, or the dispatch carries no statement of user consent, halt with status `blocked`. Report the ahead count (`git -C /Users/zane/playground/moduleforge/mod-core rev-list --count origin/main..main`) and the exact push that needs consent.
2. Run `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/moduleforge make pins.update REPOS="mod-core"` from the worktree root. Never edit SHAs by hand, and do not bump `mod-audit` or `mod-authz`.
3. Confirm that the new pin contains the conformance merges and the wave-1 helpers, and that `make pins.check` is clean for mod-core.
4. Commit the lockfile on the task branch only. Do not push, and do not open a pull request.
5. Report the old and new SHA, and state that a push of mod-users and any downstream app pin bumps are separate, consent-gated steps.

## Validation

```bash
git diff --stat -- . ':!plan/'            # only versions.lock.yaml, one pin line (mod-core)
git diff -- versions.lock.yaml            # mod-core SHA and comment changed; mod-audit and mod-authz unchanged
(cd api && go build ./... && go test ./...)
git log origin/HEAD..HEAD --oneline       # commits exist locally only; nothing pushed by this task
```

## Metadata

architectural_impact: false

## Assumptions

- Every other phase, including `doc-updates`, has landed.
- Building locally still floats against the on-disk mod-core checkout; the pin governs CI and fresh clones only (header comment in `versions.lock.yaml`).
