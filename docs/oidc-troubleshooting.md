Purpose: Troubleshooting checklist when OIDC login is rejected by the IdP (not by users-module code).
---
# OIDC login verification — troubleshooting

This doc is a step-by-step for when a user clicks a provider button in
`/auth/login`, gets rejected, and the error is coming from the IdP's
sign-in page rather than from go-oidc / our handler. Standard
symptoms: "not a recognized domain", "you can't login with a personal
account", "AADSTS…" codes, Google consent page that won't advance.

The code-side of the OIDC flow is covered by unit tests. Most real
failures are configuration mismatches between the IdP app registration
and our env / DB.

## First action — rotate the secret if it leaked

If a client_secret showed up in any chat transcript, log, or shared
screen, rotate it before anything else. Azure Portal / Google Cloud
Console → the app → create a new secret → paste into `.env` → `make
dev.restart`.

## Microsoft-specific: Endpoint ↔ signInAudience matrix

The issuer URL used at runtime MUST match the Azure manifest's
`signInAudience` field. Portal radio buttons can drift from the
manifest value — check the manifest directly.

| signInAudience                               | Endpoint                         | Accepts                         |
|---                                           |---                               |---                              |
| `AzureADMyOrg`                               | `/{tenant-id}/v2.0`              | One tenant only                 |
| `AzureADMultipleOrgs`                        | `/organizations/v2.0`            | Any Entra ID tenant, no MSA     |
| `AzureADandPersonalMicrosoftAccount`         | `/common/v2.0`                   | Entra ID + personal MSA         |
| `PersonalMicrosoftAccount`                   | `/consumers/v2.0`                | Personal MSA only               |

To pick "Entra ID + personal": Azure → App registration → Authentication →
Supported account types → "Accounts in any organizational directory …
and personal Microsoft accounts" → Save. Then verify the manifest's
`signInAudience` is exactly `AzureADandPersonalMicrosoftAccount`.

## Universal checks

1. **Redirect URI exact match**. Azure / GCP must have the exact URL we
   use: `http://localhost:8080/v1/auth/oidc/<provider>/callback`.
   Protocol, host, port, path — byte-for-byte. Trailing slashes,
   `https` vs `http`, wrong provider slug → generic IdP rejection.

2. **No stale DB override**. The modal's Save writes an
   `oidc_providers` row that overrides env. If an issuer URL there
   conflicts with the Azure setting, you get the symptoms without any
   env evidence.
   ```
   make dev.db-connect
   users=# SELECT id, issuer_url, client_id FROM oidc_providers WHERE id = 'microsoft';
   ```
   Expect zero rows if relying purely on env. Delete with
   `DELETE FROM oidc_providers WHERE id = 'microsoft';` and restart.

3. **Authorize URL inspection**. Click the provider button in the GUI,
   look at the URL in the browser before authenticating. For
   Microsoft it should start with
   `https://login.microsoftonline.com/common/oauth2/v2.0/authorize?…`
   (or `/organizations/`, `/consumers/`, or a specific tenant GUID —
   whichever matches the signInAudience). If it disagrees with env /
   expectations, you've got a stale DB override or the effective
   merged config isn't what you think.

4. **Enterprise application gotchas** (Microsoft). Even with the right
   signInAudience, Enterprise Applications → [your app] → Properties:
   - "Assignment required?" = No (or assign the test user).
   - Visible to users = Yes if you want it discoverable.

5. **Personal MSA admin-consent**. If scopes require admin consent,
   personal accounts can't grant it. Trim scopes to basics
   (`openid email profile`) when testing with MSA.

## Use the Test button (9.18)

The "Test configuration" button in the provider Edit modal exercises
the full round-trip without touching the current session. After Azure
changes, hit Test — the banner on `/oidc-config` tells you directly
whether the id_token verified, and if not, the exact error.

Flow: click Test → a new tab opens → authenticate at the IdP →
redirected back to `/oidc-config?test_result=ok|fail&…` → banner
renders the outcome. Your admin session in the original tab is
untouched; no user records are created or modified.

## When code really is the problem

If Test returns a verified token with the right email/sub but real
login still fails, the bug is on our side: UserResolver, multi-tenant
issuer validator, claim mapper, or JWT issuance. Run
`go test ./internal/auth/…` and inspect the Test result payload for
clues.
