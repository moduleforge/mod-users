# Step-Up Tokens Refuse An Empty Secret

## Purpose and scope
Remediates plan finding SUyS in this plan's `plan/findings.yaml`. Work targets the plan branch `plan/flow-accounts-upstream` and lands through the ordinary per-task loop.

## Requirements
1. `IssueStepUpToken` and `VerifyStepUpToken` (`api/internal/auth/stepup.go`, callers in `api/internal/handlers/identities.go`) must return an error (issuing) or fail verification (verifying) when the secret is empty, so a handler built with an empty `jwtSecret` can never mint or accept step-up tokens.
2. Keep existing exported signatures; existing callers in this repo must keep working. Surface the error to callers in the same style as other step-up failures.
3. Do not weaken the existing exact-header (`alg=HS256`, `typ=step-up`) and `credential_change` purpose checks.

## Validation
1. `cd api && go build ./... && go vet ./... && go test ./...` passes.
2. New tests: issuing with an empty secret errors; a token forged with an empty-key HMAC is rejected by `VerifyStepUpToken` with an empty secret and with a non-empty secret; normal issue/verify with a non-empty secret still works.

## References
- Finding SUyS in this plan's `plan/findings.yaml`.

## Status
- Outcome: succeeded (2026-10-10).
- `IssueStepUpToken` returns new `ErrStepUpEmptySecret` for an empty secret; `VerifyStepUpToken` returns `ErrStepUpRequired` for an empty secret. Header and purpose checks untouched. The `stepup_verify` handler already wraps issue errors.
- Validation: `go build`, `go vet`, `go test ./...` in `api` pass; new tests in `api/internal/auth/stepup_test.go`.
- Files: `api/internal/auth/stepup.go`, `api/internal/auth/stepup_test.go`.
