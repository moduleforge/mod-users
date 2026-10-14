# Bound And Recover The Detached Email Code Background Work

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates finding `rQjH` in this plan's `plan/findings.yaml`: the unauthenticated email-code request endpoint (`EmailCodeRequest`) spawns an unbounded background goroutine that has no deadline and no panic recovery.

The work targets the plan branch `plan/users-security-hardening` and lands through the ordinary per-task loop (implement, validate, review, and merge as for any other plan task). It is limited to bounding and recovering the detached background send. No change to the endpoint's externally visible behavior is in scope.

## Requirements

1. The background `sendEmailCode` work in `EmailCodeRequest` runs under a bounded deadline, using `context.WithTimeout` over `context.WithoutCancel` with a deadline of roughly 10 to 30 seconds.
2. The goroutine has a deferred `recover` that logs the panic without logging secrets or the email code, and does not crash the process.
3. Concurrent background sends are bounded by a semaphore or a small worker pool. When the bound is saturated, the work is dropped and logged, and the handler still answers 204 at the approximately 200 ms latency floor.
4. No status code, response body, route, or latency-floor behavior changes. Existing and unknown email addresses remain indistinguishable to the caller.

## Validation

1. Unit test: a panicking sender running inside the goroutine is recovered, and the test process survives.
2. Unit test: a sender that blocks past the deadline observes its context being cancelled.
3. Unit test: with the bound saturated, a further request still returns 204 within about the latency floor, and its background work is dropped.
4. From `api/`: `go build ./...`, `go vet ./...`, `go test -race ./internal/handlers/...`, and `make lint` all pass.

Files likely touched: `api/internal/handlers/auth/emailcode.go` and its tests.

## References

1. Finding `rQjH` in this plan's `plan/findings.yaml`: unauthenticated `EmailCodeRequest` spawns an unbounded, deadline-less, recover-less background goroutine.
