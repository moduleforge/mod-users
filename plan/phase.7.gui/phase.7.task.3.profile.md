# Phase 7, Task 3 — Profile view/edit

## Acceptance

`/profile`:
- Shows current user JSON in a clean form: email (read-only with "Change email" button), given_name / family_name (or legal_name / jurisdiction for corporations, label for service accounts), default app dropdown (from `/v1/self/apps`), is_admin badge if true.
- "Change email" opens a modal: new email → triggers verify flow.
- Save → PUT `/v1/self`. Toast on success; field-level errors on 400.
- "Sign out" button clears session, redirects to `/login`.
- "Change password" link → small inline form posting to a new `POST /v1/auth/password-change` endpoint (auth required, body `{ current, new }`). Add this endpoint as a Phase 4 follow-up if not already implemented.

## How to verify
- Edit name → reload shows persisted change.
- Switching default app updates header app switcher.
- Change-password flow rejects wrong current password.

## Notes
- Render the entity-kind-specific fields conditionally based on `entity.kind`.
