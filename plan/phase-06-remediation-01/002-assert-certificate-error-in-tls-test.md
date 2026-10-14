# Assert Certificate Error In Default TLS Verification Test

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Remediate finding `J4N9` (recorded in this plan's plan/findings.yaml): `TestNewVerifierWithOptions_TLSVerifiedByDefault` only checks that discovery fails and that the error mentions "discovery". It does not prove the failure is a TLS verification failure, so an unrelated discovery error would still pass the test. This task tightens the assertion so the test proves the failure comes from certificate verification.

Scope: `api/internal/auth/jwt_tls_test.go` only. The work targets the plan branch plan/users-security-hardening and lands through the ordinary per-task loop.

## Requirements

1. `TestNewVerifierWithOptions_TLSVerifiedByDefault` additionally asserts that the returned error text contains either "x509" or "certificate", proving the failure is a TLS verification failure.

## Validation

1. `cd api && go test ./internal/auth/...` passes.
2. `cd api && go vet ./...` and `make lint` pass.

## References

- Finding `J4N9` in this plan's plan/findings.yaml (type:security).
