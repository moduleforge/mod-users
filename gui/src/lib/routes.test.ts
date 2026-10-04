import { describe, expect, test } from 'bun:test';
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
    });
  });
});
