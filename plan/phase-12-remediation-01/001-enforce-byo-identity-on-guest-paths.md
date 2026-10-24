# Enforce Allow BYO Identity on Guest Sign-In and Guest Upgrade

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `TrRi` (security). Guest creation and guest upgrade create or promote a BYO user with a personal profile without checking `platform_settings.allow_byo_identity`. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-go.md`

## Requirements

1. `POST /v1/auth/anonymous` refuses guest creation (no `device_token`) with 403 `users.byo_identity_disabled` when `platform_settings.allow_byo_identity` is false. Signing in an existing guest with a valid `device_token` is decided and documented (refuse as well, consistent with the tenant-strict rule).
2. `UserService.SelfUpgradeGuest` (`POST /v1/me/upgrade`) loads `GetPlatformSettings` in its transaction and refuses with `ErrBYOIdentityDisabled` (403 `users.byo_identity_disabled`) when `allow_byo_identity` is false, writing nothing.
3. `api/openapi.yaml` lists the 403 `users.byo_identity_disabled` response on both routes.

## Validation

1. Handler or service tests with `allow_byo_identity=false`: anonymous guest creation returns 403 `users.byo_identity_disabled` and creates no user; guest upgrade returns 403 and leaves `is_guest` true.
2. Tests with `allow_byo_identity=true` keep the existing guest and upgrade behavior.
3. `make build`, `make test` and `make lint` pass in `api/`.

## References

- Finding `TrRi` in this plan's `plan/findings.yaml` (security, high): `POST /v1/auth/anonymous` and `SelfUpgradeGuest` do not check `allow_byo_identity`.
