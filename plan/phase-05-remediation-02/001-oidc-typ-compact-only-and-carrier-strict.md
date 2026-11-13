# Reject Non-Compact OIDC Tokens In The Typ Check And Make Carrier Typ Parsing Strict

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses findings JxLt and bPTZ recorded in this plan's `plan/findings.yaml`. It hardens the OIDC `typ` check so that only exactly three dot-separated segments (compact serialization) are accepted, and makes the locally signed carrier `typ` parsing reject a present but non-string `typ` value.

The work targets the plan branch `plan/oidc-eddsa-audience-tokens` and lands through the ordinary per-task loop.

## Requirements

1. checkOIDCTokenTyp returns errOIDCTokenTyp for any input that is not exactly three dot-separated segments, including JWS JSON serialization (flattened and general) and whitespace-padded input that go-jose would strip and parse.
2. Verifier.Verify and OAuth.Exchange both reject a JSON-serialized re-wrap of a validly signed issuer token carrying typ at+jwt, and 2- and 4-segment inputs, before or instead of reaching go-oidc verification.
3. Absent typ, JWT and aud+jwt (case-insensitive) on compact tokens remain accepted; every existing OIDC acceptance test still passes.
4. verifyCarrier rejects a locally signed token whose typ is present but not a string (for example ["aud+jwt"]), and its accept or reject outcome is unchanged for every token shape the module's own signer produces (string typ or absent typ).
5. Comments describe the current fail-closed behaviour only; the claim that non-3-segment input is left to the signature verifier is removed. Error text never echoes header or token content.

## Validation

1. New unit tests in api/internal/auth/oidc_algs_test.go: checkOIDCTokenTyp returns errOIDCTokenTyp for 2-segment, 4-segment, JSON-flattened and JSON-general inputs, and for a whitespace-wrapped JSON input; the existing "not-a-jwt" assertion is updated to expect rejection.
2. A Verifier.Verify test and an OAuth.Exchange test using the Ed25519 issuer harness: a JSON-serialized token with typ at+jwt and a valid signature fails in both paths.
3. A carrier test: a locally signed token with a non-string typ is rejected by verifyCarrier; existing carrier tests (including carrier_test.go's rawTokenTyp assertion, adapted if the helper's signature changes) pass.
4. cd api && go test ./internal/auth/... -count=1 passes; go vet ./internal/auth/ is clean; make test.unit passes.

## References

- JxLt: a finding in this plan's `plan/findings.yaml` covered by this task (OIDC `typ` check accepts non-compact input).
- bPTZ: a finding in this plan's `plan/findings.yaml` covered by this task (carrier `typ` parsing accepts a present but non-string value).
