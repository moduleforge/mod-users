/**
 * Standard frontend paths every app embedding the users GUI mounts.
 *
 * Paths marked "backend-fixed" are hard-coded by the users API (action-required
 * envelopes, emailed links, or deploy env) and must be mounted exactly as
 * listed; the rest are the library's recommended defaults and an app may
 * mount them elsewhere if it also passes matching props/config.
 *
 * This module imports nothing so any other lib module (including `config`)
 * can read the constants without an import cycle.
 */
export const USERS_GUI_ROUTES = {
  /** Recommended default: the sign-in page. Also the default unauthenticated redirect target. */
  login: '/auth/login',
  /**
   * Backend-fixed via deploy env (the OIDC redirect URI); recommended default
   * `/auth/oidc/return`. The API callback redirects here carrying
   * `#handoff=<code>` (sign-in) or `#error=<code>` in the URL fragment; the
   * page exchanges the code once with `completeHandoff`.
   */
  oidcReturn: '/auth/oidc/return',
  /** Recommended default: the "forgot password" request page. */
  forgotPassword: '/forgot-password',
  /** Backend-fixed: the password-reset email links here. */
  resetPassword: '/reset-password',
  /** Recommended default: sign-in with an emailed code. */
  emailCode: '/auth/email-code',
  /** Backend-fixed: the `users.email_unverified` action-required envelope navigates here. */
  verifyEmail: '/verify-email',
  /** Backend-fixed: the `users.oidc_not_confirmed` action-required envelope navigates here. */
  oidcConfig: '/oidc-config',
  /**
   * Recommended default: landing profile selection, shown when a sign-in
   * answers `selection_required` (no landing profile could be chosen).
   */
  selectProfile: '/auth/select-profile',
  /** Recommended default: the caller's party-profile invitations (accept / decline). */
  invitations: '/invitations',
  /** Recommended default: account settings home (profile name, default profile). */
  account: '/account',
  /** Recommended default: account settings, credentials (password, SSH keys, OIDC links). */
  accountCredentials: '/account/credentials',
  /** Recommended default: account settings, email identifiers. */
  accountIdentifiers: '/account/identifiers',
  /** Recommended default: account settings, live sessions. */
  accountSessions: '/account/sessions',
  /** Recommended default: first-run bootstrap (setup token, operator party, first administrator). */
  bootstrap: '/bootstrap',
} as const;

export type UsersGuiRoutes = typeof USERS_GUI_ROUTES;
