# Document SSH Key Endpoints In OpenAPI

## Purpose and scope

Add the six SSH key endpoints and their schemas to `api/openapi.yaml`, which AGENTS.md designates as the authoritative REST API specification. Documentation only: no Go, SQL, or manifest changes.

The contract being documented is fixed in the [SSH key design note](../notes/ssh-key-design.md), sections D6–D10. This task depends on nothing else in the plan and may run in parallel with tasks 001–004. Where the note is explicit, document the note, not whatever intermediate code exists.

## Requirements

1. **Tag.** Add an `SSH Keys` tag with a one-line description. Say that keys are SSH public keys used for git-over-SSH authentication by composing applications.
2. **Schemas** under `components/schemas`:
   - `SSHKey`, with fields `uuid` (uuid), `key_type` (string, enum of the seven accepted types in D6), `fingerprint` (string, `SHA256:` form, with an example), `public_key` (string, canonical `<type> <base64>`), `label` (string, maxLength 100), and `created_at` (date-time).
   - `SSHKeyCreate`, with `public_key` required (a single `authorized_keys` line, with the options, certificate, and multi-key refusals described) and `label` optional and nullable (maxLength 100; defaults to the key comment when omitted).
   - A list response schema that matches the envelope the existing list endpoints in this file use, including `total`. Reuse the existing pattern and do not invent a new envelope shape.
3. **Paths.** Four path items covering six operations:

   | Method and path | Success | Documented errors |
   |---|---|---|
   | `GET /v1/self/ssh-keys` | `200` list, with `limit`/`offset` params | `401` |
   | `POST /v1/self/ssh-keys` | `201` `SSHKey` | `400`, `401`, `403` (`users.email_unverified` action), `409` |
   | `DELETE /v1/self/ssh-keys/{key_uuid}` | `204` | `400`, `401`, `403`, `409` (`users.step_up_required`) |
   | `GET /v1/user-accounts/{uuid}/ssh-keys` | `200` list | `401`, `403` |
   | `POST /v1/user-accounts/{uuid}/ssh-keys` | `201` `SSHKey` | `400`, `401`, `403`, `409` |
   | `DELETE /v1/user-accounts/{uuid}/ssh-keys/{key_uuid}` | `204` | `400`, `401`, `403` |

   The self `POST` `409` covers **two** shapes, `users.ssh_key_in_use` (an Error with detail) and `users.step_up_required` (an Action). Document both with `oneOf` or in prose, following how this file already documents action-required responses.

   Each operation's description must state:
   - its middleware posture: verified email required for self write and operator operations, unverified allowed for self list
   - the `X-Step-Up-Token` header on self register and revoke when `AUTH_REQUIRE_STEP_UP` is enabled (an optional header parameter)
   - that every not-found-or-not-permitted outcome returns the same masked `403` (D10)
   - that revoke archives the key and takes effect on the next SSH connection
   - for register, the `400` detail codes `users.ssh_key_invalid`, `users.ssh_key_type_unsupported`, `users.ssh_key_too_weak`, and `users.ssh_key_label_too_long`, plus the D6 accepted-algorithm policy in brief
4. **Reuse** the existing `Error`, `FieldError`, and `Action` schemas and the existing bearer security scheme. Do not define parallel error shapes. Followup `biJk` notes that `Action.code`'s enum already includes `users.step_up_required`.
5. **Path drift note.** Operator paths use `/v1/user-accounts/{uuid}`, the prefix the server actually serves (`api/internal/handlers/account_routes.go`), even though this file's older admin paths say `/v1/users/{user_uuid}`. Do **not** rename the older paths; that pre-existing drift is out of scope. Add one sentence to the operator operations' descriptions noting that the prefix matches the served routes.

## Validation

- The file still parses as YAML: `python3 -c "import yaml,sys; yaml.safe_load(open('api/openapi.yaml'))"` or an equivalent.
- If an OpenAPI linter is available locally (`npx @redocly/cli lint`, `swagger-cli validate`, or a Make target), run it and confirm no *new* errors compared with the file before your change. If none is available, say so in Status.
- `grep -n "ssh-keys\|SSHKey" api/openapi.yaml` shows all four path items and the schemas.
- Every `$ref` you added resolves to an existing component.
- `git diff --stat` touches only `api/openapi.yaml`, excluding this task document.

## References

- [SSH key design note](../notes/ssh-key-design.md): D6–D10.
- `api/openapi.yaml`: the existing `Error`/`FieldError`/`Action` schemas, list envelope patterns, and security scheme.
- `api/internal/handlers/user_accounts.go`: the actual list envelope the server emits, for cross-checking the list schema.
- Followup `biJk` in `plan/followups.yaml`: guidance on documenting the `409` step-up and conflict responses with existing schemas.

## Status

**Outcome:** succeeded. Date: 2026-09-28.

Added the `SSH Keys` tag, three schemas (`SSHKey`, `SSHKeyCreate`, `PaginatedSSHKeys`), two path parameters (`KeyUUID`, `AccountUUID`), one header parameter (`StepUpToken`), and three reusable `409` responses (`SSHKeyConflict`, `SSHKeyStepUpRequired`, `SSHKeyRegisterConflict`) to `api/openapi.yaml`, plus the four path items covering the six operations, per D6–D10 of the design note. All reuse the existing `Error`/`Action`/bearer-auth building blocks; no parallel error shapes were introduced.

Affected source file: `api/openapi.yaml`.

Validation summary:
- YAML parse (`python3 -c "import yaml,sys; yaml.safe_load(open('api/openapi.yaml'))"`): passed.
- OpenAPI lint (`npx @redocly/cli lint api/openapi.yaml`, available locally): passed — 0 errors both before and after the change; the 10 pre-existing warnings are unchanged (no new warnings introduced). Caught and fixed one real bug during implementation: the operator paths use `{uuid}` (matching the served `/v1/user-accounts/{uuid}` routes) but the existing `UserUUID` parameter is named `user_uuid`, which redocly's `path-parameters-defined` rule flagged as a mismatch; added a new `AccountUUID` parameter (`name: uuid`) for these paths instead of reusing `UserUUID`.
- `grep -n "ssh-keys\|SSHKey" api/openapi.yaml`: shows all four path items and all new schemas/responses.
- Every `$ref` added resolves: verified programmatically (walked the full document; 196 total `$ref`s, 0 unresolved) in addition to the lint pass.
- `git diff --stat`: touches only `api/openapi.yaml` (371 insertions), excluding this task document.

Decision recorded: the list envelope (`PaginatedSSHKeys`) follows the `items`/`total` pattern already established in `api/openapi.yaml` (`PaginatedUsers`, `PaginatedAudit`) rather than the ad hoc `{"user_accounts": [...], "total": N}` shape `api/internal/handlers/user_accounts.go`'s `List` emits for the unrelated `/v1/user-accounts` listing endpoint — that handler's key name doesn't apply here since it was written for the unrelated `/v1/user-accounts` listing endpoint, not the SSH-key list endpoints, and the task doc's instruction to "reuse the existing pattern" reads most naturally as the envelope shape already documented in this file. (Updated 2026-09-28 per phase-01 gate finding `documentation-007`: task 004 has since landed a Go implementation of the SSH-key list endpoints, using this same `{items, total}` envelope, so the original "no Go implementation yet" justification no longer holds as stated.)
