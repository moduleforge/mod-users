# Correct OIDC typ Helper, Empty typ and RS256 Default Wording in Docs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes review findings xwrM, JOmI and a3im. It corrects three documentation statements that do not match the implemented behaviour: the claim that the local carrier verifier and the OIDC path share one typ check helper, the unqualified statement that RS256 is accepted by default, and the typ policy lists that omit how an empty typ is handled. It also updates the `checkOIDCTokenTyp` doc comment in `api/internal/auth/oidc_algs.go` to match, comment only with no behaviour change.

The work targets the plan branch `plan/oidc-eddsa-audience-tokens` and lands through the ordinary per-task loop.

## Requirements

1. Remove the claim that the local carrier verifier and the OIDC path share one typ check helper and "cannot drift" (`docs/architecture.md`, OIDC ID token verification section, typ paragraph), and the phrase "by the same shared helper" (`docs/architecture/sessions-and-tokens.md`, Audience tokens paragraph). State the actual behaviour instead: OIDC ID tokens accept an absent or empty typ, `JWT` or `aud+jwt` (case-insensitive), and reject anything else; the local carrier verifier separately rejects a typ equal (case-insensitive) to `aud+jwt`; both compare against the same `aud+jwt` constant.
2. Replace the wording "RS256 is accepted/allowed by default" in `docs/architecture.md`, `docs/jwt-eddsa.md` and `docs/mod-users-spec.md` with wording that RS256 is accepted when the issuer advertises it, and is the sole fallback only when the issuer advertises no allowlisted algorithm.
3. Everywhere the typ policy is listed (`docs/architecture.md`, `docs/jwt-eddsa.md`, `docs/mod-users-spec.md`, `docs/consumer-upgrade-notes.md`, `docs/oidc-troubleshooting.md`), state that an empty typ is treated as absent. Update the `checkOIDCTokenTyp` doc comment in `api/internal/auth/oidc_algs.go` the same way (comment only, no behaviour change).
4. Describe current behaviour only. No history narration, no `plan/` links, no finding ids.

## Validation

1. `grep -rn 'same .typ. check helper\|same shared helper\|cannot drift' docs` returns nothing.
2. `grep -rn -i 'RS256. is \(accepted\|allowed\) by default' docs` returns nothing.
3. Each typ-policy statement in the listed docs mentions the empty typ. Reread each against `api/internal/auth/oidc_algs.go` lines 89-115 and `api/internal/auth/carrier.go` lines 331-337 for accuracy.
4. `make test.unit` (or `go vet ./api/internal/auth/...`) still passes after the comment edit, and edited doc links still resolve.

## References

1. Finding xwrM, recorded in this plan's `plan/findings.yaml`.
2. Finding JOmI, recorded in this plan's `plan/findings.yaml`.
3. Finding a3im, recorded in this plan's `plan/findings.yaml`.

## Status

succeeded, 2026-11-13. Docs and the checkOIDCTokenTyp comment corrected; make test.unit passed (log in .flow/validation-logs/04-test-unit.log). The remaining "cannot drift" match at docs/architecture.md line 63 concerns SQL functions, unrelated to typ.
