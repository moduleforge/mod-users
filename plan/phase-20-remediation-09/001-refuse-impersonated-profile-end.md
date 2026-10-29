# Refuse Ending A Profile Under Impersonation

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates findings `uBfI` and `zzo0` recorded in this plan's `plan/findings.yaml`. It targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Scope is limited to refusing a profile-end status transition when the caller is operating under impersonation. The general per-field mechanism is tracked separately as followup `H8qi` and is out of scope here.

## Requirements

1. In `ProfileService.UpdateProfileStatus` (`api/internal/service/profile_admin.go`), when the validated target status is `ended` and the caller's AuthContext has `ImpersonatorPrincipalID != 0`, refuse with `apiresp.ErrForbidden` (403). Place the refusal after `validateStatusUpdate` and before `partyProfileByUUID` so the error order stays 401, 400, 403, 404, 409.
2. Leave the departure cleanup unchanged: it is neither skipped nor altered. Suspend and resume under impersonation keep their current behavior.
3. Add an integration test in which an impersonation session, whose impersonated profile holds `update` on a party profile, PATCHes that profile to `ended`. Expect 403, and verify that the profile status, the holder's `credential_party_authorizations` rows for the party, and the holder's party-confined credentials (`revoked_at` NULL) are all unchanged.
4. Document the refusal in the `PATCH /v1/profiles/{uuid}` description in `api/openapi.yaml` and in the Decision K bullet of `docs/architecture.md`: ending a profile under impersonation is refused with a plain 403.
5. Do no work beyond these findings.

## Validation

1. The new integration test passes, and fails when the refusal is removed.
2. Existing profile-status, departure-cleanup and impersonation integration tests still pass.
3. `make build` and `make lint` pass. Task agents set `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users`. Never run `scripts/link-siblings.sh` or bare `make preflight`.
4. `grep` shows the refusal sentence in the `updateProfileStatus` description in `api/openapi.yaml` and in the Decision K bullet in `docs/architecture.md`.

## References

- Finding `uBfI` in this plan's `plan/findings.yaml` (refusal of profile end under impersonation).
- Finding `zzo0` in this plan's `plan/findings.yaml` (documentation of the impersonation profile-end refusal).
