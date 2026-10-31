// Shared outcome handling for the public sign-in pages: which route a finished
// sign-in continues to, and the readable copy for the sign-in error codes.

import { ApiRequestError, USERS_ERROR_CODES, type TokenResponse } from './api';
import { USERS_GUI_ROUTES } from './routes';

/** The `users.*` codes a sign-in can answer that have a dedicated message. */
const BYO_IDENTITY_DISABLED = 'users.byo_identity_disabled';
const APP_LOGIN_NOT_GRANTED = 'users.app_login_not_granted';

/**
 * Readable copy for the sign-in error codes. The code set is open on the wire,
 * so a code not listed here falls through to the caller's own fallback rather
 * than rendering a raw code.
 */
const SIGN_IN_ERROR_COPY: Record<string, string> = {
  [USERS_ERROR_CODES.invalidCredentials]: 'Invalid email or password.',
  [BYO_IDENTITY_DISABLED]:
    'This site does not allow signing in or creating an account with your own email address.',
  [APP_LOGIN_NOT_GRANTED]:
    'Your account has not been granted access to this application.',
  [USERS_ERROR_CODES.profileUnavailable]:
    'The profile for this account is not available right now.',
};

/**
 * The readable message for a failed sign-in call, or null when the error has
 * no dedicated copy (the caller then shows the server message or its own
 * fallback). Only an `ApiRequestError` carries a code worth mapping.
 */
export function describeSignInError(err: unknown): string | null {
  if (!(err instanceof ApiRequestError)) return null;
  return SIGN_IN_ERROR_COPY[err.code] ?? null;
}

/**
 * What a form does once a sign-in call resolved.
 *
 * `selection_required` means the server found no landing profile: the tab
 * holds a refresh token but no access token, so the next step is the
 * profile-selection route, not the return path (which would load pages that
 * need an access token). The caller's `onSelectionRequired` receives that
 * route and the return path to resume after selection. A host that registered
 * no such handler still gets to the selection route: `onSuccess` is then
 * handed the selection route in place of the return path.
 */
export function finishSignIn(
  response: Pick<TokenResponse, 'selection_required'>,
  returnPath: string | null,
  handlers: {
    onSuccess?: (returnPath: string | null) => void;
    onSelectionRequired?: (selectProfilePath: string, returnPath: string | null) => void;
  },
): void {
  if (response.selection_required) {
    // Selection pending: never continue to the return path yet.
    if (handlers.onSelectionRequired) {
      handlers.onSelectionRequired(USERS_GUI_ROUTES.selectProfile, returnPath);
    } else {
      // No dedicated handler: route through onSuccess so a host that simply
      // navigates to the path it is handed still lands on the selection page.
      handlers.onSuccess?.(USERS_GUI_ROUTES.selectProfile);
    }
    return;
  }
  handlers.onSuccess?.(returnPath);
}
