# Gate System-Actor Contexts That Carry A Session Or Credential

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task makes the mod-users authorization engine fail closed for a context that is marked as a mod-core system actor but also carries a session or a credential. It closes finding `tpls`, and it fixes the stale comment in `model/migrations/sql/0100_baseline.sql` that finding `c6Dh` names.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Roles: read `developer-go.md` and `developer.md` (Go developer role) before starting.

Environment and working rules:

- Set `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users`. Do not assume `/Users/zane/playground/../..` or any other path for it.
- Never run `link-siblings.sh` and never run a bare `make preflight`.
- Integration tests run against a throwaway `postgres:16` container that you start and remove yourself. Remove it even when tests fail.
- Do not edit sibling projects.
- Decisions D through M in `plan/notes/decisions-and-resolutions.md` still apply.

## Requirements

1. In `api/internal/authz/authz.go`, `gated()` skips the prelude gates (assurance and owning-party) only when the context is anonymous, or when `opctx.IsSystemActor(ctx)` is true AND `ac.SessionID == 0` AND `ac.CredentialID == 0`. A marked context that carries a session or a credential is gated and fails closed. Keep calling `opctx.IsSystemActor`; do not otherwise re-derive the marker rule.
2. In `model/migrations/sql/0100_baseline.sql`, edit `mod_users.access_row_permitted` in place so it exempts only when `p_system_actor IS TRUE AND COALESCE(p_session_id, 0) = 0 AND COALESCE(p_credential_id, 0) = 0`. List and single-row paths must keep parity.
3. Update the WHY and IMPLIES comments at `gated()` and at the predicate's system-actor branch to state the new condition and its reason: `SystemActor` is an exported field, so a mis-built context is gated rather than trusted.
4. In `0100_baseline.sql` near lines 1703-1705, correct the stale comment that says trusted contexts without a session or credential are the engine's to skip (finding `c6Dh`, same file).

## Validation

1. `gated()` unit test: a context with `SystemActor` set via `opctx.WithSystemActor` plus a non-zero `SessionID` is refused by the assurance gate and by the party-policy stub. A context with `SystemActor` set and a non-zero `CredentialID` is refused the same way. A sessionless `opctx.WithSystemActor` context still skips both gates.
2. Row-filter parity: `access_row_permitted(actor, row, owner, <session>, NULL, true)` on a gated row is not true, while `(actor, row, owner, 0, 0, true)` is true. `paritytest.Compare` passes with a marked context that carries a session.
3. Mutation check: reverting either the Go side or the SQL side makes the new tests fail. Restore the code afterwards and confirm the tests pass again.
4. `make build`, `make test`, `make lint` and `make -C model verify` pass with `MODULEFORGE_SIBLINGS_DIR` set. The `authz`, `config` and `integration` suites pass against the throwaway `postgres:16`, and no container of yours remains.

## References

- Finding `tpls` (plan findings, `plan/findings.yaml`): the engine infers trust for a system-actor-marked context that carries a session or credential.
- Finding `c6Dh` (plan findings, `plan/findings.yaml`): stale comment in `0100_baseline.sql` about sessionless trusted contexts.
- [Decisions and resolutions](../notes/decisions-and-resolutions.md): Decisions D through M.
