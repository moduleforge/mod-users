# Update Architecture Docs

## Purpose and scope

Update mod-users' architecture and specification documents to reflect the SSH public-key ownership capability delivered in Phase 01:

- the new `mod_users` schema and `mod_users.ssh_public_keys` table
- the `sshKeyService` and `sshKeyResolver` services
- the six `/v1` SSH key endpoints
- the key acceptance policy
- the resolver's consumer contract

Follow the `update-architecture-docs` task-procedure.

## Requirements

- **Implementation task documents that surfaced the architectural implications.** They will all have completed by the time this phase runs. Paths are relative to the plan worktree root:
  - `plan/phase-01-ssh-public-keys/001-add-ssh-key-storage.md`: the first table under a `mod_*` schema, a partial unique index as the global-uniqueness invariant, and archive-on-revoke.
  - `plan/phase-01-ssh-public-keys/003-add-ssh-key-service-and-resolver.md`: the new pre-authentication resolver service, its no-cache/no-write/no-`Authorize` contract, and the `usersservice` facade additions.
  - `plan/phase-01-ssh-public-keys/004-add-ssh-key-http-routes.md`: new public API routes, manifest services and routes, and step-up gating extended to SSH keys.
  - Read each one's Status section for what actually landed, including any deviation from the design. Also read `plan/notes/ssh-key-design.md`, the decision record whose rationale the docs should carry forward in condensed form.
- **Files to review and update:**
  - `docs/architecture.md`:
    - Data model table: add `mod_users.ssh_public_keys`, and explain why it alone lives in `mod_users` while older tables stay in `public`.
    - Runtime service dependencies: the new services consume only existing ones, so check whether any statement changes.
    - API layer tag table: add an `SSH Keys` row.
    - Authentication flow and multi-channel account model: add SSH public keys as a git-access credential that resolves to the account holder, with revocation semantics and the explicit statement that the anonymous system actor can never authenticate over SSH, because it has no `user_accounts` row and so can hold no key.
    - The consumer-facing resolver contract, for app-mfgit and mod-repos.
  - `docs/mod-users-spec.md` (the project's `docs/*-spec.md`):
    - Key use cases: add self-service key management and operator key management.
    - General features and Security requirements: key algorithm policy, global uniqueness, immediate revocation, step-up, and masking.
    - Data model: add the table and correct the "owns ten Postgres tables" count against the actual table list.
    - API definition checklist: add the six endpoints under the actually served paths `/v1/self/ssh-keys` and `/v1/user-accounts/{uuid}/ssh-keys`. Do not rewrite the pre-existing `/v1/users` admin paths; that drift is out of scope, but a one-line note is acceptable.
  - `AGENTS.md`: review only. Update it if a new convention emerged, for example the first `mod_*`-schema table and how sqlc names its model. Otherwise leave it unchanged.
- **role_doc:** `plugins/flow/roles/architect-backend.md`. The implications are primarily a new backend service, an API surface, and an authentication component. The schema change is secondary.
- **Procedure:** `plugins/flow/task-procedures/update-architecture-docs/SKILL.md`.

## Validation

- `docs/architecture.md` and `docs/mod-users-spec.md` were each reviewed against the three task documents listed above and updated where their content no longer matched the landed implementation. For each file, the Status section records either a list of the sections changed or "reviewed, no change needed" with a reason.
- `grep -n "ssh" docs/architecture.md docs/mod-users-spec.md` shows coverage of each of these:
  - the table
  - the resolver contract
  - all six endpoints (spec checklist)
  - the algorithm policy
  - revocation semantics
- No statement in either document contradicts `plan/notes/ssh-key-design.md` or the landed code, as reported in the implementation tasks' Status sections.
- Markdown style: sentence-case section headings, and relative links that resolve.

## References

- `plan/notes/ssh-key-design.md`: the decision record.
- `/Users/zane/playground/moduleforge/app-mfgit/docs/handoffs/mod-users-ssh-extension.md`: the originating requirements, useful for the consumer-contract wording.
- `/Users/zane/playground/moduleforge/docs-mf-standards/architecture/schema-ownership-design.md`: the schema standard to cite when explaining table placement.
