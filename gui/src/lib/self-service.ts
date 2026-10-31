// Shared error handling and copy for the account self-service panels
// (credentials, identifiers, sessions, guest upgrade).
//
// The API reports a state conflict as `409` with `error.code: conflict` and the
// specific cause as a detail code (`error.details[].code`, for example
// `users.last_credential`), so the panels branch on detail codes, not on the
// top-level code.

import type { FieldErrorData } from '@moduleforge/core-gui';
import {
  ApiActionRequiredError,
  ApiRequestError,
  STEP_UP_REQUIRED_ACTION,
} from './api';

/** Length of the emailed one-time codes (step-up and identifier verification). */
export const EMAIL_CODE_LENGTH = 6;

/** Seconds before an emailed code may be requested again. */
export const RESEND_COOLDOWN_SECONDS = 30;

export const GENERIC_FAILURE =
  'Something went wrong. Check the browser console for details.';

/** The detail codes the panels branch on. */
export const SELF_SERVICE_CODES = {
  lastCredential: 'users.last_credential',
  guestUpgradeRequired: 'users.guest_upgrade_required',
  identifierTaken: 'users.identifier_taken',
  identifierInUse: 'users.identifier_in_use',
  oidcIdentityInUse: 'users.oidc_identity_in_use',
  passwordExists: 'users.password_exists',
  emailUnverified: 'users.email_unverified',
} as const;

/** Friendly copy for the server's known detail codes. */
export const DETAIL_COPY: Record<string, string> = {
  'users.last_credential':
    'This is your last sign-in credential, so it cannot be revoked. Add another way to sign in first.',
  'users.guest_upgrade_required':
    'Create an account to do this. Guests must upgrade first.',
  'users.identifier_taken': 'That email address is already registered.',
  'users.identifier_in_use':
    'This address is still in use elsewhere and cannot be removed.',
  'users.oidc_identity_in_use':
    'That external account is already linked to a sign-in.',
  'users.password_exists':
    'You already have a password. Revoke it before adding a new one.',
  'users.ssh_key_in_use': 'This key is already registered.',
  'users.ssh_key_invalid':
    'Paste exactly one public key line, without options (for example, the contents of your id_ed25519.pub file).',
  'users.ssh_key_type_unsupported':
    'This key type is not supported. Use Ed25519, a FIDO (sk-) key, ECDSA, or RSA of at least 2048 bits.',
  'users.ssh_key_too_weak':
    'This key is too weak to register. Use a stronger key (RSA keys must be at least 2048 bits).',
  'users.ssh_key_label_too_long': 'The label is too long (100 characters at most).',
  'users.ssh_key_label_invalid':
    'The label contains control or bidirectional-text characters. Use plain text.',
};

export const STEP_UP_DECLINED_NOTICE =
  'We could not confirm it’s you, so nothing was changed.';

export const EMAIL_UNVERIFIED_NOTICE =
  'Verify your email address first, then try again.';

/** Every code an error carries: its own plus its field details'. */
export function errorCodes(err: unknown): string[] {
  if (!(err instanceof ApiRequestError)) return [];
  return [err.code, ...(err.details ?? []).map((d) => d.code)];
}

/** True when `err` is an API error carrying `code` (top-level or as a detail). */
export function hasErrorCode(err: unknown, code: string): boolean {
  return errorCodes(err).includes(code);
}

/** True for the step-up action that the user declined or could not satisfy. */
export function isStepUpDeclined(err: unknown): boolean {
  return (
    err instanceof ApiActionRequiredError && err.code === STEP_UP_REQUIRED_ACTION
  );
}

/**
 * Turns a failed call into the message to show, and hands any action-required
 * error other than a step-up to `onActionRequired` so the host can navigate.
 */
export function describeFailure(
  err: unknown,
  scope: string,
  onActionRequired?: (error: ApiActionRequiredError) => void,
): string {
  if (err instanceof ApiActionRequiredError) {
    if (err.code === STEP_UP_REQUIRED_ACTION) {
      // Reaching a caller means no prompt is registered or the user
      // cancelled; nothing was written, which the message states.
      return STEP_UP_DECLINED_NOTICE;
    }
    // Any other action (for example email verification) is a navigation the
    // host owns; the panel only explains why nothing happened.
    onActionRequired?.(err);
    return err.code === SELF_SERVICE_CODES.emailUnverified
      ? EMAIL_UNVERIFIED_NOTICE
      : err.message;
  }
  if (err instanceof ApiRequestError) {
    // Prefer the friendly copy for a known detail code, else the server text.
    for (const code of errorCodes(err)) {
      const copy = DETAIL_COPY[code];
      if (copy) return copy;
    }
    return err.message;
  }
  // Not an API error: a programming or environment fault worth a console trace.
  console.error(`[${scope}]`, err);
  return GENERIC_FAILURE;
}

/** Groups an error's field details by field, with friendly copy applied. */
export function fieldErrorsOf(
  err: unknown,
  fields: readonly string[],
): Record<string, FieldErrorData[]> {
  const mapped: Record<string, FieldErrorData[]> = {};
  if (!(err instanceof ApiRequestError)) return mapped;
  for (const detail of err.details ?? []) {
    // Details for fields the form does not show are surfaced by the caller as
    // the form-level message instead of being dropped silently.
    if (!fields.includes(detail.field)) continue;
    (mapped[detail.field] ??= []).push({
      ...detail,
      message: DETAIL_COPY[detail.code] ?? detail.message,
    });
  }
  return mapped;
}

/** Locale date for an RFC 3339 time; the raw text when it does not parse. */
export function formatDate(iso: string | null | undefined): string {
  if (!iso) return 'Never';
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleDateString();
}

/** Locale date and time for an RFC 3339 time; the raw text when it does not parse. */
export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return 'Never';
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString();
}

/**
 * True for an absolute http(s) URL. The authorize URL comes from the server,
 * but it is about to become a full-page navigation, so only web schemes may
 * reach that sink (never `javascript:` or `data:`).
 */
export function isWebUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' || url.protocol === 'http:';
  } catch {
    return false;
  }
}
