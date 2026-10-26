# Take The Holder Profile Lock Before The Credential Lock In The Credential Write Gate

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `9Mvf` (security, priority medium), `PL6d` (bug, priority medium) and `zwbM` (bug, priority medium). All three concern the lock order between `gateCredentialWrite`, which takes the credential row first, and the profile ending path, which takes the profile row first and then revokes confined credentials. The inverted order can deadlock (SQLSTATE 40P01, surfacing as a 500). The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. `gateCredentialWrite` in `api/internal/service/party_policy.go` takes the holder's active profile `FOR SHARE` before the credential row `FOR UPDATE` (`GetCredentialByUUIDForUpdate`). The credential is read unlocked first if needed for the principal, then locked and re-read after the profile lock, and `revoked_at` is re-checked after the credential lock.
2. The resulting order matches the ending path (`EndInTx`, `endLocked` and `departureCleanupInTx`): profile first, then credential rows.
3. A code comment at the lock site states the lock order (holder profile, then credential) and why it must hold.
4. A confine, unconfine, authorize or deauthorize write racing a leave or an ending of the same holder no longer produces SQLSTATE 40P01; the caller does not receive a 500 for this interleaving.

## Validation

1. An interleaved integration test of `UnconfineCredential` (and confine) of a party-confined credential against `LeaveProfile` and against an ending of the holder's profile passes on repeated runs, with no 40P01 and no 500.
2. `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `9Mvf` in this plan's `plan/findings.yaml` (type:security, priority:medium): `gateCredentialWrite` locks the credential first and the holder profile second, the reverse of the ending path, so a confine or unconfine racing an ending can deadlock.
- Finding `PL6d` in this plan's `plan/findings.yaml` (type:bug, priority:medium): the same inversion, with the unconfine of a party-confined credential racing a holder leave as the concrete lock cycle; the recommendation is a uniform profile-then-credential order and a concurrency test.
- Finding `zwbM` in this plan's `plan/findings.yaml` (type:bug, priority:medium): the lock-order inversion between the credential write gate and the profile ending path, with the comment that the share lock serializes both paths being true only for the profile lock.
