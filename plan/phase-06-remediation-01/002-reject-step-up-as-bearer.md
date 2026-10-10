# Reject Step-Up Tokens As Bearer Tokens

## Purpose and scope
Remediates plan finding nIyL in this plan's `plan/findings.yaml`. Work targets the plan branch `plan/flow-accounts-upstream` and lands through the ordinary per-task loop.

## Requirements
1. In `verifyLocal` (`api/internal/auth/jwt.go`), reject any token whose header `typ` is `step-up`, so a step-up token can never be accepted as a bearer token regardless of issuer configuration.
2. Make the legacy `auth.NewVerifier(ctx, issuerURL, clientID, jwtSecret, localIssuer)` refuse (or otherwise never accept HS256 tokens for) an empty `localIssuer` when a `jwtSecret` is set, without changing the function signature; existing callers in this repo must keep working.
3. Confirm and record in the task Status that `VerifyStepUpToken` still requires its exact `typ` and purpose claim.

## Validation
1. `cd api && go build ./... && go vet ./... && go test ./...` passes.
2. New tests: a validly signed step-up token (HS256, same secret, with and without an `iss` claim) is rejected by the local verifier; the legacy `NewVerifier` with a secret and an empty issuer accepts no HS256 tokens; normal HS256 and EdDSA bearer tokens still verify.

## References
- Finding nIyL in this plan's `plan/findings.yaml`.

## Status
- Outcome: succeeded (2026-10-10).
- `verifyLocal` (`api/internal/auth/jwt.go`) now rejects any token whose header `typ` is `step-up` (case-insensitive) before signature checks. Legacy `NewVerifier` returns an error when `jwtSecret` is set and `localIssuer` is empty (signature unchanged; `api/auth/auth.go` wrapper needed no change; no in-repo caller passes an empty issuer).
- `VerifyStepUpToken` confirmed unchanged: it still requires the exact header (`alg=HS256`, `typ=step-up`) and payload purpose `credential_change`.
- Tests: `api/internal/auth/jwt_stepup_bearer_test.go`. Validation: `go build/vet/test ./...` passes.
