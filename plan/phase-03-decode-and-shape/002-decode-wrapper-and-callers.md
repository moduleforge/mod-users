# Decode Wrapper And Callers

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Turn `server.Decode` (`api/internal/server/respond.go:48-51`) into a thin wrapper over `apiresp.DecodeJSON` and convert all 16 callers, per decision D6 in the [handler inventory](../notes/handler-inventory.md#planning-decisions). This closes the size-cap, unknown-field and trailing-data gaps. The `DecodeJSON` contract is in [common section 3](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#3-helper-reference-final-signatures). The swap leaves each decode where it is, with one exception: it necessarily deletes the 403-before-400 special case in `UserAccountsHandler.Create` (`api/internal/handlers/user_accounts.go:63-76`; D4, followup hunL dismissed), because `DecodeJSON` writes the 400 itself. Reordering for other routes is done by tasks 3.3-3.6.

## Requirements

1. Change `server.Decode` to `func Decode(w http.ResponseWriter, r *http.Request, v any, opts ...apiresp.DecodeOption) bool { return apiresp.DecodeJSON(w, r, v, opts...) }`, with a doc comment that names the contract (it has already written the 400 when it returns false).
2. Convert every caller to `if !server.Decode(w, r, &req) { return }`. The planner found 16 (re-grep):
   - `ssh_keys.go:267`, `oidc_config.go:304`, `self.go:61`, `identities.go:359` and `:593`, `user_accounts.go:64` and `:187`, `apps.go:92` and `:248`;
   - `auth/emailcode.go:37` and `:123`, `auth/register.go:159`, `auth/anonymous.go:25`, `auth/login.go:29`, `auth/reset.go:37` and `:95`.

   Drop the now-dead `apiresp.ErrInvalidInput` writes and the literal "invalid JSON body" `server.Error` calls that followed them.
3. SSH-key register: replace `http.MaxBytesReader(w, r.Body, sshKeyRegisterBodyMaxBytes)` with `server.Decode(w, r, &req, apiresp.WithMaxBytes(sshKeyRegisterBodyMaxBytes))`, keeping the 32 KiB cap.
4. `UserAccountsHandler.Create`: delete the special case that authorizes on decode failure, so a malformed body is 400 for any authenticated caller. Check whether `UserAccountService.AuthorizeCreate` is still used outside `Create` and tests. If it is not, keep it (it is exported; record that it is unused) unless deleting it is trivially safe; report your choice.
5. Before converting, check every client of each route for fields the server struct lacks, as D6 requires. A strict decode would now reject such a field. Grep `gui/src/lib/api.ts`, `/Users/zane/playground/moduleforge/app-*/gui/src` and the other playground callers. The known `apps.create`/`update`/`addMember` mismatches are fixed by task 5.1; list them and do not halt for them. Halt and report any other mismatch.
6. Update the tests that sent unknown fields or asserted the old literal messages, without weakening what they check. Add a small table test through real handlers covering:
   - an unknown field;
   - trailing data;
   - an over-cap body: 1 MiB on a default route, and just over 32 KiB on SSH-key register;
   - an empty body.

   Each must give 400 `invalid_input` with the expected detail code. Use identifiers prefixed `decode` and grep the package for collisions first.
7. Append the wire changes (planned items 3 and 4) to the [wire-risk note](../notes/wire-risk.md#recorded-by-tasks).

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go test ./...
make lint
grep -rn 'server.Decode(r,' --include='*.go' .        # no hits (old signature)
grep -rn 'MaxBytesReader' internal/handlers/ssh_keys.go   # no hits
grep -n 'AuthorizeCreate' internal/handlers/user_accounts.go   # no hits
```

## Metadata

architectural_impact: false

## Assumptions

- Task 3.1 has landed.
- `oidc_providers.go`'s `parseBody` is not a `server.Decode` caller; task 3.6 handles it.

## Checkpoint hints

- After `respond.go` and the `api/internal/handlers` callers compile and pass.
- After the `api/internal/handlers/auth` callers and the new decode tests.
