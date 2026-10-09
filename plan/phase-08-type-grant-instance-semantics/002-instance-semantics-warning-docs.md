# Instance Semantics Warning Docs

## Purpose and scope

Write the documented warning that the user made mandatory for Q1 "instance semantics": a grant over a type's entity also confers the operation over every instance of exactly that type. This task owns the prose docs and the `localAuthz` facade doc. Task 001 owns the code and its in-code comments.

This is a standard documentation task: follow the `implement-task` procedure. No code changes.

Files in scope:

- `docs/architecture.md`
- `docs/mod-users-spec.md`
- `api/localAuthz/authz.go` (doc comments only)

## Requirements

1. **`docs/architecture.md`.**
   - Add a key decision after D12, titled along the lines of "Type grants confer instance access". It states the semantics: exact type only, through target groups and actor groups, never applied to type entities themselves, `AuthorizeType` unchanged.
   - Include a clearly marked **Warning** block with every point in the [design note's required warning](../notes/users-type-grant-design.md#required-warning-documented). Name the concrete mod-users consequences: `manage` on the `natural_person` type entity confers `assume` of every user and the operator SSH-key routes (which authorize `update` on the account holder).
   - Amend D12 where it says or implies that a type grant confers no instance access.
2. **`docs/mod-users-spec.md`**, Security requirements, "Authorization" bullet: one or two sentences stating that a grant on a type's entity also authorizes that operation on every instance of the type, with a link to the new architecture decision.
3. **`api/localAuthz/authz.go`.** In the `Authorizer` alias or `New` doc comment, add a short warning paragraph that entity-level authorization honours type grants over instances, and link the reader to `docs/architecture.md` by name. Keep `TypeAuthorizer`'s doc accurate: it is unchanged.
4. Do not name plan, phase or task identifiers in the docs. Use inline links, not "see" pointers.

## Validation

- `grep -n -i "warning" docs/architecture.md` shows the new block, and a manual read confirms that every point in the design note's warning list is present.
- `grep -n "no access to existing instances\|confers no instance access" docs/architecture.md docs/mod-users-spec.md` returns nothing.
- `make -C api lint` passes (gofmt on the facade).
- `git diff -- api/localAuthz/authz.go` changes comment lines only.
- `git diff --stat -- . ':(exclude)plan'` touches only the three files in [Purpose and scope](#purpose-and-scope).

## Assumptions

- The semantics are as specified in the design note. If task 001 has landed when this runs, re-read its `checkGrantOrOwn` doc comment and keep the two consistent. If they disagree, the code is authoritative: report the difference.
- Phase 7 has landed, so D12 already describes the phase 6 arm.

## References

- [Users type grant design note, Q1 section](../notes/users-type-grant-design.md#q1-instance-semantics-optional-last-phase): the semantics and the warning list.
- `docs/architecture.md` D12 and D9 (operator authorization reuses `update`).
- [`instance-semantics-arm`](./001-instance-semantics-arm.md): the code side.

## Status

- Outcome: succeeded (2026-10-09).
- Validation: warning block present with all design-note points; stale-phrase grep empty; `make -C api lint` passed; `authz.go` diff is comment lines only; substantive diff limited to the three scoped files.
- Files: `docs/architecture.md` (new D13, D12 rollout sentence amended), `docs/mod-users-spec.md` (Authorization bullet), `api/localAuthz/authz.go` (Authorizer doc comment).
- Consistency with the code comment: matches. The code is authoritative; no differences found.
