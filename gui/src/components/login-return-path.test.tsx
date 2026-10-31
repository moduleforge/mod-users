import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AuthProvider } from '../lib/auth-context';
import { configureUsersApi, resetUsersApiConfig } from '../lib/config';
import { tokenResponse } from '../test-support/users-fixtures';
import { AuthPage } from './auth-page';
import { LoginForm } from './login-form';

const originalFetch = globalThis.fetch;
const realLocationDescriptor = Object.getOwnPropertyDescriptor(window, 'location');

let assigned: string[] = [];

beforeEach(() => {
  assigned = [];
  configureUsersApi({ baseUrl: '' });
  setSearch('');
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const url = String(input);
    const json = (body: unknown) =>
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    if (url.endsWith('/v1/auth/providers')) {
      return json([{ id: 'google', display_name: 'Google' }]);
    }
    if (url.endsWith('/v1/auth/login') || url.endsWith('/v1/auth/register')) {
      return json(tokenResponse());
    }
    // Session hydration after sign-in and any other read: nothing to add.
    return json({});
  }) as unknown as typeof fetch;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  if (realLocationDescriptor) {
    Object.defineProperty(window, 'location', realLocationDescriptor);
  }
  localStorage.clear();
  sessionStorage.clear();
  resetUsersApiConfig();
});

function setSearch(search: string) {
  Object.defineProperty(window, 'location', {
    value: {
      pathname: '/auth/login',
      search,
      hash: '',
      origin: 'http://app.test',
      href: `http://app.test/auth/login${search}`,
      assign: (url: string) => {
        assigned.push(url);
      },
    },
    configurable: true,
    writable: true,
  });
}

type Surface = 'LoginForm' | 'AuthPage';

async function loginAndCapture(
  surface: Surface,
  props: { returnPath?: string } = {},
): Promise<{ callbackArgs: unknown[][]; startUrl: string }> {
  const callbackArgs: unknown[][] = [];
  const cb = (...args: unknown[]) => {
    callbackArgs.push(args);
  };
  render(
    <AuthProvider>
      {surface === 'LoginForm' ? (
        <LoginForm onSuccess={cb} {...props} />
      ) : (
        <AuthPage onAuthenticated={cb} {...props} />
      )}
    </AuthProvider>,
  );
  const provider = await screen.findByRole('button', { name: /Sign in with Google/ });
  fireEvent.click(provider);
  // The start URL is built after the tab's handoff challenge is computed.
  await waitFor(() => expect(assigned.length).toBe(1));
  fireEvent.change(document.getElementById('login-email')!, {
    target: { value: 'a@b.test' },
  });
  fireEvent.change(document.getElementById('login-password')!, {
    target: { value: 'pw' },
  });
  const form = document.getElementById('login-email')!.closest('form')!;
  fireEvent.submit(form);
  await waitFor(() => expect(callbackArgs.length).toBe(1));
  return { callbackArgs, startUrl: assigned[0] };
}

/** The query params of an OIDC start URL, with the challenge checked to be well formed. */
function startParams(startUrl: string): { path: string; returnPath: string | null } {
  const url = new URL(startUrl, 'http://app.test');
  // PKCE-style binding: every start carries a 43 character base64url challenge.
  expect(url.searchParams.get('handoff_challenge')).toMatch(/^[A-Za-z0-9_-]{43}$/);
  return { path: url.pathname, returnPath: url.searchParams.get('return') };
}

// AuthPage additionally passes the mode as a second callback argument.
function expected(surface: Surface, path: string | null): unknown[] {
  return surface === 'AuthPage' ? [path, 'login'] : [path];
}

const SURFACES: Surface[] = ['LoginForm', 'AuthPage'];

describe.each(SURFACES)('%s return-path handling', (surface) => {
  test('ignores ?return= when unauthenticatedReturnParam is not configured', async () => {
    setSearch('?return=/x');
    const { callbackArgs, startUrl } = await loginAndCapture(surface);
    expect(startParams(startUrl)).toEqual({
      path: '/v1/auth/oidc/google/start',
      returnPath: '/',
    });
    expect(callbackArgs[0]).toEqual(expected(surface, null));
  });

  test('reads the configured param into the OIDC start URL and callback', async () => {
    configureUsersApi({ unauthenticatedReturnParam: 'return' });
    setSearch('?return=/open/3');
    const { callbackArgs, startUrl } = await loginAndCapture(surface);
    expect(startParams(startUrl)).toEqual({
      path: '/v1/auth/oidc/google/start',
      returnPath: '/open/3',
    });
    expect(callbackArgs[0]).toEqual(expected(surface, '/open/3'));
  });

  test.each(['//evil.test', 'https://x', 'javascript:x'])(
    'drops unsafe value %p to null',
    async (bad) => {
      configureUsersApi({ unauthenticatedReturnParam: 'return' });
      setSearch(`?return=${encodeURIComponent(bad)}`);
      const { callbackArgs, startUrl } = await loginAndCapture(surface);
      expect(startParams(startUrl)).toEqual({
      path: '/v1/auth/oidc/google/start',
      returnPath: '/',
    });
      expect(callbackArgs[0]).toEqual(expected(surface, null));
    },
  );

  test('an explicit returnPath prop wins', async () => {
    configureUsersApi({ unauthenticatedReturnParam: 'return' });
    setSearch('?return=/open/3');
    const { callbackArgs, startUrl } = await loginAndCapture(surface, {
      returnPath: '/explicit',
    });
    expect(startParams(startUrl)).toEqual({
      path: '/v1/auth/oidc/google/start',
      returnPath: '/explicit',
    });
    expect(callbackArgs[0]).toEqual(expected(surface, '/explicit'));
  });

  test('works with a custom param name', async () => {
    configureUsersApi({ unauthenticatedReturnParam: 'next' });
    setSearch('?next=/dash&return=/ignored');
    const { callbackArgs, startUrl } = await loginAndCapture(surface);
    expect(startParams(startUrl)).toEqual({
      path: '/v1/auth/oidc/google/start',
      returnPath: '/dash',
    });
    expect(callbackArgs[0]).toEqual(expected(surface, '/dash'));
  });
});

describe('AuthPage onAuthenticated mode argument', () => {
  test('passes "login" after login', async () => {
    const { callbackArgs } = await loginAndCapture('AuthPage');
    expect(callbackArgs[0]).toEqual([null, 'login']);
  });

  test('passes "register" after registration', async () => {
    const callbackArgs: unknown[][] = [];
    render(
      <AuthProvider>
        <AuthPage
          initialMode="register"
          onAuthenticated={(...args: unknown[]) => {
            callbackArgs.push(args);
          }}
        />
      </AuthProvider>,
    );
    fireEvent.change(document.getElementById('register-email')!, {
      target: { value: 'a@b.test' },
    });
    fireEvent.change(document.getElementById('register-password')!, {
      target: { value: 'pw1234567890ab' },
    });
    const form = document.getElementById('register-email')!.closest('form')!;
    fireEvent.submit(form);
    await waitFor(() => expect(callbackArgs.length).toBe(1));
    expect(callbackArgs[0]).toEqual([null, 'register']);
  });
});
