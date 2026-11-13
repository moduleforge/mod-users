# Fail Closed On Non-String Or Undecodable OIDC Token Typ

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Security-sensitive remediation of finding GCnC. The OIDC typ check (`checkOIDCTokenTyp`, used by `Verifier.Verify` and `OAuth.Exchange`) currently treats a typ that is not a string, or a protected header that cannot be decoded, as if no typ were present. This task makes both cases fail closed while keeping absent, `JWT` and `aud+jwt` (case-insensitive) accepted and the carrier path's local-token behavior unchanged.

This work targets the plan branch `plan/oidc-eddsa-audience-tokens` and lands through the ordinary per-task loop (implement, validate, review, merge into the plan branch).

## Requirements

1. The typ check on the OIDC paths (`checkOIDCTokenTyp`, used by `Verifier.Verify` and `OAuth.Exchange`) rejects a 3-segment token whose JWS header has a `typ` that is present but not a string (array, number, object, bool), returning the existing fixed `errOIDCTokenTyp`.
2. The OIDC typ check rejects a 3-segment token whose protected header cannot be base64url-decoded or JSON-decoded, instead of treating it as having no typ.
3. An absent typ, `JWT` and `aud+jwt` (case-insensitive) are still accepted; other string values are still rejected. A non-3-segment input keeps its current outcome unless the implementer shows rejecting it is equivalent and safe.
4. `verifyCarrier`'s local-token behavior is unchanged: either keep `rawTokenTyp`'s semantics for the carrier path, or show the shared-helper change does not alter which local tokens `verifyCarrier` accepts or rejects.
5. Error text never echoes header contents.

## Validation

1. New unit tests in `api/internal/auth/oidc_algs_test.go`: a `typ` as array, number, or object, and an undecodable header on a 3-segment token, are each rejected by `checkOIDCTokenTyp` with `errOIDCTokenTyp`, and the error does not contain the header value.
2. A `Verifier.Verify` test and an `OAuth.Exchange` test with an issuer-signed EdDSA token whose `typ` is non-string both fail. Use the Ed25519 issuer harness, minting the header by hand if the mint helper only takes a string `typ`.
3. Existing typ tests and carrier tests pass unchanged.
4. `cd api && go test ./internal/auth/... -count=1` passes; `go vet ./internal/auth/` is clean; `make test.unit` passes.

## References

- Finding GCnC, recorded in this plan's `plan/findings.yaml`.
