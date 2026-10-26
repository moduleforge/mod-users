# Gate Holder Leave On Profile Availability

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `aNYW` (security, high). The holder's leave path runs the destructive departure cleanup without checking that the caller's session still passes the party gate. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

The task also restores a requirement dropped from the evaluation: the last-operator-admin guard. It is not a separate finding; it is the optional recommendation of `aNYW`.

## Requirements

1. `LeaveProfile` (`POST /v1/me/profiles/{uuid}/leave`) refuses unless `mod_users.profile_availability(session, profile)` is `available` for the caller's session. The refusal uses the same masking/status the availability lock uses elsewhere (document the choice in `api/openapi.yaml`). Accept and decline stay ungated.
2. The `profile_holder.go` doc comment about freshness and gating is updated to match.
3. **Last operator admin guard.** `LeaveProfile` (`api/internal/service/profile_holder.go`) and the administrator's `UpdateProfileStatus` to `ended` (`api/internal/service/profile_admin.go`) refuse to end or leave the last active operator-party profile that holds the wildcard `manage` grant. The refusal is a 409 with a detail code, documented in `api/openapi.yaml` on both operations. Run the check in the same transaction as the ending, so two concurrent endings cannot both pass.

## Validation

1. An integration test: a session whose credential the party gate locks out of the profile (for example an unauthorized password under `credential_rule` `authorized`) gets a refusal on leave, and the profile and its credential authorizations are unchanged.
2. An integration test: the bootstrap operator admin cannot leave, and cannot be ended by an administrator, while they are the only wildcard-`manage` operator admin; with a second such admin present, the ending succeeds.
3. `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `aNYW` in this plan's `plan/findings.yaml` (security, high): `holderProfile` in `api/internal/service/profile_holder.go` checks only holder identity, and `LeaveProfile` runs the destructive departure cleanup with no availability or freshness gate. A session the party gate locks out can still end the party profile, including the only operator admin's. The finding's optional recommendation, refusing to end the last wildcard-`manage` operator admin, is requirement 3 and is not tracked as a separate finding.
