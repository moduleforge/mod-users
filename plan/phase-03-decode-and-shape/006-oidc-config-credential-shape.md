# OIDC Config Credential Shape

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Fix the decode path and the 401/400 ordering of the credential-authenticated OIDC config routes:

- `POST /oidc-config/confirm` (`api/internal/handlers/oidc_config.go:302`);
- `POST /oidc-config/providers`, and `PUT`, `GET` and `DELETE /oidc-config/providers/{id}` (`api/internal/handlers/oidc_providers.go`).

These routes authenticate by an admin bearer (`AdminChecker`), the `X-Setup-Token` header, or a `setup_token` body field. They must never get `RequireAuth` ([common rule 2](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#4-rules-all-plans)). Implement decisions D7 and D8 in the [handler inventory](../notes/handler-inventory.md#planning-decisions), and the empty-body and raw-bytes guidance in [common section 3](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#3-helper-reference-final-signatures). This is the reserved-slot item `oidc_providers.go:421`. It partly adopts project followup 7J9z.

## Requirements

1. **Credential order (D8).** Split `authorizeConfirm` (`oidc_config.go:401`), or add a sibling, so the body-independent credentials can be checked without a body: admin via `AdminChecker`, then the `X-Setup-Token` header. For `Confirm`, provider `Create` and provider `Update`:
   1. If a body-independent credential authenticates, decode (400) and continue.
   2. Otherwise decode the body. A malformed body gives 400 (documented exception: the body carries the credential). Then verify the body `setup_token`; a failure gives 401 `unauthenticated`.
   3. Then run the stateless shape checks: provider id pattern, required `id` on `Create`, and any field-type checks (400).

   `AdminChecker` internal faults stay 500. Keep `GET`/`DELETE /providers/{id}` as they are (credential, then id pattern) and confirm they match the rule. Preserve the setup-token comparison and clearing semantics exactly.
2. **`parseBody` (D7, `oidc_providers.go:418`).**
   - Bound the read with a `MaxBytesReader` at `apiresp.DefaultMaxBodyBytes`.
   - Keep the zero-byte body meaning an empty map.
   - Otherwise restore `r.Body` and decode with `server.Decode` into `map[string]json.RawMessage`, which rejects trailing data and over-cap bodies with `DecodeJSON`'s detail codes.
   - Add an explicit allowlist of the top-level keys the provider handlers read. Derive it from `stringField`/`boolField`/`merge*`/`upsertAndRespond` call sites, plus `setup_token` and `id`. An unknown key gives 400 `FieldError{Field: <key>, Code: "invalid_json_body", Message: "request body contains an unknown field"}`.
   - `parseBody` callers write nothing themselves when it reports a failure (it has already written), or adapt the signature so they don't; pick one and keep it consistent.
3. **`Confirm`'s body** goes through `server.Decode` (already done by task 3.2); confirm it now follows step 1's order.
4. **Conflict literal (D8).** Replace the ZVum carve-out at `oidc_providers.go:222` with `apiresp.WriteError(w, r, apiresp.Conflict(apiresp.FieldError{Field: "id", Code: "users.oidc_provider_exists", Message: "provider id already exists; use PUT to update"}))`. Remove its marker comment, and lower `EXPECTED_CARVEOUTS` in `api/scripts/check-server-error-literals.sh` from 4 to 3 in the same change. Update the script's header prose that says "Exactly 4".
5. **Tests** (`oidc_config_test.go`, `oidc_providers_test.go`). Update `TestConfirm_BadJSON` (`oidc_config_test.go:585`) to the new order and add:
   - header-token or admin caller with a malformed body: 400;
   - no credential and a well-formed body without a token: 401;
   - no header or admin and a malformed body: 400 (the documented exception, with a comment saying why);
   - an unknown key, trailing data or an over-cap body: 400;
   - an empty body on `PUT`: still accepted;
   - an existing provider id on `Create`: 409 with the new detail.

   Use identifiers prefixed `oidccfg`, and grep for collisions first.
6. Record the documented exception in the [handler inventory](../notes/handler-inventory.md#audit-table) row for each route. Append the wire changes (planned item 10) to the [wire-risk note](../notes/wire-risk.md#recorded-by-tasks).

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go test ./...
make lint                                    # reports 3/3 documented carve-outs
grep -n 'json.NewDecoder' internal/handlers/oidc_providers.go   # no hits
grep -n 'EXPECTED_CARVEOUTS=3' scripts/check-server-error-literals.sh
```

## Metadata

architectural_impact: false

## Assumptions

- Tasks 3.1-3.5 have landed.
- The GUI's OIDC config page (`gui/src/components/oidc-config-page*`, `gui/src/lib/api.ts`) sends only keys the handlers read. Verify this while building the allowlist; if it sends any other key, halt and report.

## Checkpoint hints

- After the credential split and the `Confirm` ordering, with tests green.
- After `parseBody` and the provider handlers.
- After the conflict literal and the lint script.
