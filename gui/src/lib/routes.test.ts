import { describe, expect, test } from 'bun:test';
import { DEFAULT_UNAUTHENTICATED_REDIRECT_URL, isSafeSitePath } from './config';
import { USERS_GUI_ROUTES } from './routes';

describe('USERS_GUI_ROUTES', () => {
  test('exposes the exact standard paths', () => {
    expect(USERS_GUI_ROUTES).toEqual({
      login: '/auth/login',
      oidcReturn: '/auth/oidc/return',
      forgotPassword: '/forgot-password',
      resetPassword: '/reset-password',
      emailCode: '/auth/email-code',
      verifyEmail: '/verify-email',
      oidcConfig: '/oidc-config',
      selectProfile: '/auth/select-profile',
      invitations: '/invitations',
      account: '/account',
      accountCredentials: '/account/credentials',
      accountIdentifiers: '/account/identifiers',
      accountSessions: '/account/sessions',
      bootstrap: '/bootstrap',
    });
  });

  test('every route is a safe site-relative path', () => {
    for (const path of Object.values(USERS_GUI_ROUTES)) {
      expect(isSafeSitePath(path)).toBe(true);
    }
  });

  test('the default unauthenticated redirect is the login route constant', () => {
    expect(DEFAULT_UNAUTHENTICATED_REDIRECT_URL).toBe(USERS_GUI_ROUTES.login);
  });
});
