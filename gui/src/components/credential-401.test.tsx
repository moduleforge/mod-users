import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AuthProvider } from '../lib/auth-context';
import { createUsersClient } from '../lib/api';
import {
  configureUsersApi,
  getStoredToken,
  resetUsersApiConfig,
  setStoredRefreshToken,
  setStoredToken,
} from '../lib/config';
import { PROFILE_A, tokenResponse, USER_VIEW } from '../test-support/users-fixtures';
import { EmailCodePage } from './email-code-page';
import { LoginForm } from './login-form';
import { ResetPasswordPage } from './reset-password-page';

const originalFetch = globalThis.fetch;
const realLocationDescriptor = Object.getOwnPropertyDescriptor(window, 'location');

let hrefs: string[];
let unauthCalls: number;
let responder: (url: string) => Response;

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const unauthorized = () =>
  json(401, { error: { code: 'unauthorized', message: 'server wording' } });
const invalidCredentials = () =>
  json(401, { error: { code: 'users.invalid_credentials', message: 'server wording' } });

// Mount-time calls (AuthProvider's session read and user load, the provider
// list) must succeed so the seeded tokens survive until the credential call
// under test.
function baseResponder(url: string): Response | undefined {
  if (url.endsWith('/v1/auth/providers')) return json(200, []);
  if (url.endsWith('/v1/session')) {
    const base = tokenResponse();
    return json(200, { session: base.session, profile: PROFILE_A, profiles: [PROFILE_A] });
  }
  if (url.endsWith('/v1/me')) return json(200, USER_VIEW);
  return undefined;
}

beforeEach(() => {
  hrefs = [];
  unauthCalls = 0;
  Object.defineProperty(window, 'location', {
    value: {
      pathname: '/auth/login',
      search: '',
      hash: '',
      origin: 'http://app.test',
      get href() {
        return 'http://app.test/auth/login';
      },
      set href(v: string) {
        hrefs.push(v);
      },
      assign: (v: string) => hrefs.push(v),
    },
    configurable: true,
    writable: true,
  });
  configureUsersApi({ baseUrl: '', onUnauthenticated: () => unauthCalls++ });
  setStoredToken('keep-me');
  setStoredRefreshToken('keep-refresh');
  responder = (url) => baseResponder(url) ?? unauthorized();
  globalThis.fetch = (async (input: RequestInfo | URL) =>
    responder(String(input))) as unknown as typeof fetch;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  if (realLocationDescriptor) {
    Object.defineProperty(window, 'location', realLocationDescriptor);
  }
  sessionStorage.clear();
  localStorage.clear();
  resetUsersApiConfig();
});

function expectNoSessionSideEffects(): void {
  expect(getStoredToken()).toBe('keep-me');
  expect(hrefs).toEqual([]);
  expect(unauthCalls).toBe(0);
}

describe('credential-failure 401s show inline errors', () => {
  test('LoginForm: wrong password shows the inline error without redirecting', async () => {
    render(
      <AuthProvider>
        <LoginForm />
      </AuthProvider>,
    );
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'a@b.co' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'wrong' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));

    expect((await screen.findByRole('alert')).textContent).toContain(
      'Invalid email or password.',
    );
    expectNoSessionSideEffects();
  });

  test('LoginForm: a coded users.invalid_credentials 401 also stays inline', async () => {
    responder = (url) =>
      baseResponder(url) ??
      (url.endsWith('/v1/auth/login') ? invalidCredentials() : unauthorized());
    render(
      <AuthProvider>
        <LoginForm />
      </AuthProvider>,
    );
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'a@b.co' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'wrong' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));

    expect((await screen.findByRole('alert')).textContent).toContain(
      'Invalid email or password.',
    );
    expectNoSessionSideEffects();
  });

  test('EmailCodePage: wrong code shows the inline error without redirecting', async () => {
    responder = (url) =>
      baseResponder(url) ??
      (url.endsWith('/v1/auth/email-code/request')
        ? new Response(null, { status: 204 })
        : unauthorized());
    render(
      <AuthProvider>
        <EmailCodePage />
      </AuthProvider>,
    );
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'a@b.co' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
    fireEvent.change(await screen.findByLabelText('Code'), { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify code' }));

    expect((await screen.findByRole('alert')).textContent).toContain('Invalid or expired code.');
    expectNoSessionSideEffects();
  });

  test('EmailCodePage: requestEmailCode non-401 errors still surface their message', async () => {
    responder = (url) =>
      baseResponder(url) ?? json(400, { error: { code: 'invalid_input', message: 'bad email' } });
    render(
      <AuthProvider>
        <EmailCodePage />
      </AuthProvider>,
    );
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'a@b.co' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
    expect((await screen.findByRole('alert')).textContent).toContain('bad email');
  });

  test('ResetPasswordPage: invalid token shows the inline error without redirecting', async () => {
    render(<ResetPasswordPage token="tok" />);
    fireEvent.change(screen.getByLabelText(/New password/), {
      target: { value: 'a-long-enough-password' },
    });
    fireEvent.change(screen.getByLabelText('Confirm new password'), {
      target: { value: 'a-long-enough-password' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Reset password' }));

    expect((await screen.findByRole('alert')).textContent).toContain(
      'Invalid or expired reset link.',
    );
    expectNoSessionSideEffects();
  });

  test('regression: a 401 on an authenticated call still invokes onUnauthenticated', async () => {
    responder = unauthorized;
    const client = createUsersClient({ baseUrl: '' });
    await client.me.update({ given_name: 'x' }).catch(() => undefined);
    expect(unauthCalls).toBe(1);
  });

  test('regression: a 401 on an authenticated call still clears the token and redirects by default', async () => {
    resetUsersApiConfig();
    configureUsersApi({ baseUrl: '' });
    responder = unauthorized;
    const client = createUsersClient({ baseUrl: '' });
    await client.me.update({ given_name: 'x' }).catch(() => undefined);
    expect(getStoredToken()).toBeNull();
    expect(hrefs).toEqual(['/auth/login']);
  });
});
