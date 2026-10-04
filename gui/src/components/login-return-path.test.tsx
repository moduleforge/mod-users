import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AuthProvider } from '../lib/auth-context';
import { configureUsersApi, resetUsersApiConfig } from '../lib/config';
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
    if (url.endsWith('/v1/auth/login')) {
      return json({ token: 't', user: { id: 'u1', email: 'a@b.test' } });
    }
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

const SURFACES: Surface[] = ['LoginForm', 'AuthPage'];

describe.each(SURFACES)('%s return-path handling', (surface) => {
  test('ignores ?return= when unauthenticatedReturnParam is not configured', async () => {
    setSearch('?return=/x');
    const { callbackArgs, startUrl } = await loginAndCapture(surface);
    expect(startUrl).toBe('/v1/auth/oidc/google/start?return=%2F');
    expect(callbackArgs[0]).toEqual([null]);
  });

  test('reads the configured param into the OIDC start URL and callback', async () => {
    configureUsersApi({ unauthenticatedReturnParam: 'return' });
    setSearch('?return=/open/3');
    const { callbackArgs, startUrl } = await loginAndCapture(surface);
    expect(startUrl).toBe('/v1/auth/oidc/google/start?return=%2Fopen%2F3');
    expect(callbackArgs[0]).toEqual(['/open/3']);
  });

  test.each(['//evil.test', 'https://x', 'javascript:x'])(
    'drops unsafe value %p to null',
    async (bad) => {
      configureUsersApi({ unauthenticatedReturnParam: 'return' });
      setSearch(`?return=${encodeURIComponent(bad)}`);
      const { callbackArgs, startUrl } = await loginAndCapture(surface);
      expect(startUrl).toBe('/v1/auth/oidc/google/start?return=%2F');
      expect(callbackArgs[0]).toEqual([null]);
    },
  );

  test('an explicit returnPath prop wins', async () => {
    configureUsersApi({ unauthenticatedReturnParam: 'return' });
    setSearch('?return=/open/3');
    const { callbackArgs, startUrl } = await loginAndCapture(surface, {
      returnPath: '/explicit',
    });
    expect(startUrl).toBe('/v1/auth/oidc/google/start?return=%2Fexplicit');
    expect(callbackArgs[0]).toEqual(['/explicit']);
  });

  test('works with a custom param name', async () => {
    configureUsersApi({ unauthenticatedReturnParam: 'next' });
    setSearch('?next=/dash&return=/ignored');
    const { callbackArgs, startUrl } = await loginAndCapture(surface);
    expect(startUrl).toBe('/v1/auth/oidc/google/start?return=%2Fdash');
    expect(callbackArgs[0]).toEqual(['/dash']);
  });
});
