/**
 * Standard frontend paths every app embedding the users GUI mounts.
 *
 * Paths marked "backend-fixed" are hard-coded by the users API (action-required
 * envelopes, emailed links, or deploy env) and must be mounted exactly as
 * listed; the rest are the library's recommended defaults and an app may
 * mount them elsewhere if it also passes matching props/config.
 */
export const USERS_GUI_ROUTES = {
  /** Recommended default: the sign-in page. */
  login: '/auth/login',
  /** Backend-fixed via deploy env (the OIDC redirect URI); recommended default `/auth/oidc/return`. */
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
} as const;

export type UsersGuiRoutes = typeof USERS_GUI_ROUTES;
