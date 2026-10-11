# Treat Blank JWT_SECRET As Unset Under EdDSA And Tidy Step-Up Key Comment

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Remediates plan findings `WutU` (security) and `ccun` (style). The work targets the plan branch `plan/catalog-ent-and-derived-keys` and lands through the ordinary per-task loop.

Under EdDSA, `JWT_SECRET` is no longer required. On main a whitespace-only value failed startup (validate trims); after this plan a whitespace-only value is non-empty, so `resolveKey` returns it as the HMAC key for OAuth state and step-up tokens instead of deriving. Treat a blank or whitespace-only `JWT_SECRET` as unset. A non-blank explicit secret keeps winning byte-for-byte (no length policy change, so in-flight tokens survive the upgrade). Also reorder an out-of-order comment in `main.go` and add a doc note.

## Requirements

1. `resolveKey` (`api/internal/auth/derived_keys.go`) treats a `JWTSecret` that is empty after `strings.TrimSpace` as unset. Under EdDSA with a valid signing key it derives; otherwise it returns the existing fixed error. It never returns whitespace bytes as a key.
2. A non-blank explicit `JWT_SECRET` is still returned byte-for-byte, untrimmed (no length policy change).
3. `config.Load` under `JWT_ALG=EdDSA` normalizes a whitespace-only `JWT_SECRET` to `""` so `LocalAuth.JWTSecret` consistently means "derive". Under HS256 behavior is unchanged: whitespace-only still fails validate as missing and does not trigger the DB bootstrap.
4. Errors and logs carry no key material (existing guarantee preserved).
5. In `api/cmd/server/main.go`, put the step-up key resolution comment and code in their own paragraph, separate from the identities-handler comment; the consumed-JTI cache / janitor sentence must sit next to the `stepUpConsumed` / `StartStepUpJanitor` lines.
6. In `docs/jwt-eddsa.md` ("Derived state and step-up keys" → Precedence): say a blank or whitespace-only `JWT_SECRET` is treated as unset under EdDSA, and that an explicit `JWT_SECRET` keys both OAuth state and step-up so label separation does not apply (the token types stay distinguishable by structure and purpose claim / step-up `typ` header).

## Validation

1. Unit tests in `api/internal/auth/derived_keys_test.go`: EdDSA + signing key + `JWTSecret` of `"   "` (and `"\t\n"`) returns the derived key for both `OAuthStateKey` and `StepUpKey`, equal to the empty-secret result; HS256 (or no signing key) + whitespace-only secret returns an error and no key; a non-blank secret with surrounding spaces is returned exactly, untrimmed.
2. Config test in `api/internal/config/jwt_signing_test.go`: EdDSA + signing key + `JWT_SECRET="   "` means `Load` succeeds with `LocalAuth.JWTSecret == ""` and no bootstrap; HS256 + `JWT_SECRET="   "` still reports `JWT_SECRET` missing.
3. Review the `main.go` comment order by inspection; grep `docs/jwt-eddsa.md` for the new precedence sentences.
4. In `api/`: `go test ./internal/auth/... ./internal/config/... ./internal/handlers/...`, `go vet ./...`, `make build.api`. `make lint` may fail only on the pre-existing `identities.go` allowlist issue (promoted finding `0NVf`); golangci-lint must report nothing in the changed files.

## References

- Finding `WutU` in this plan's `plan/findings.yaml` (type:security).
- Finding `ccun` in this plan's `plan/findings.yaml` (type:style).

## Status

Outcome: succeeded (2026-10-11). `resolveKey` treats blank/whitespace-only `JWT_SECRET` as unset; `config.Load` normalizes it to `""` under EdDSA only; `main.go` comment reordered; `docs/jwt-eddsa.md` Precedence updated. Tests added in `api/internal/auth/derived_keys_test.go` and `api/internal/config/jwt_signing_test.go`. `go test` (auth, config, handlers), `go vet ./...`, `make build.api` pass; `make lint` fails only on the pre-existing `identities.go:415` allowlist check (0NVf); golangci-lint reports only pre-existing issues in unchanged lines.
