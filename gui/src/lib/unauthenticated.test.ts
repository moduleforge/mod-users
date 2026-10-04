import { afterEach, beforeEach, describe, expect, spyOn, test } from 'bun:test';
import { ApiRequestError, createUsersClient } from './api';
import {
  configureUsersApi,
  getTokenStorageKey,
  handleUnauthenticated,
  resetUsersApiConfig,
  type UnauthenticatedContext,
} from './config';
import { readReturnPath } from './return-path';

// Replaceable window.location stub: records every href assignment.
const realLocationDescriptor = Object.getOwnPropertyDescriptor(window, 'location');
let hrefs: string[];

function stubLocation(url: string): void {
  const u = new URL(url);
  hrefs = [];
  const stub = {
    pathname: u.pathname,
    search: u.search,
    hash: u.hash,
    origin: u.origin,
    get href() {
      return url;
    },
    set href(v: string) {
      hrefs.push(v);
    },
  };
  Object.defineProperty(window, 'location', {
    value: stub,
    configurable: true,
    writable: true,
  });
}

const originalFetch = globalThis.fetch;

beforeEach(() => {
  stubLocation('http://app.test/deployments/3?tab=a#frag');
  globalThis.fetch = (async () =>
    new Response(JSON.stringify({ error: { code: 'unauthenticated', message: 'x' } }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    })) as unknown as typeof fetch;
  localStorage.setItem('auth_token', 'tok');
});

afterEach(() => {
  resetUsersApiConfig();
  localStorage.clear();
  globalThis.fetch = originalFetch;
  if (realLocationDescriptor) {
    Object.defineProperty(window, 'location', realLocationDescriptor);
  }
});

const client = createUsersClient({ baseUrl: 'http://localhost:8080' });

async function hit(skipAuthRedirect = false): Promise<unknown> {
  return client.self
    .get(skipAuthRedirect ? { skipAuthRedirect: true } : undefined)
    .catch((err: unknown) => err);
}

describe('default 401 handling', () => {
  test('clears the token, goes to /auth/login with no return param, still throws', async () => {
    const err = await hit();
    expect(err).toBeInstanceOf(ApiRequestError);
    expect((err as ApiRequestError).status).toBe(401);
    expect(localStorage.getItem('auth_token')).toBeNull();
    expect(hrefs).toEqual(['/auth/login']);
  });

  test('skipAuthRedirect: no clear, no navigation, no handler, still throws 401', async () => {
    const calls: UnauthenticatedContext[] = [];
    configureUsersApi({ onUnauthenticated: (c) => calls.push(c) });
    const err = await hit(true);
    expect(err).toBeInstanceOf(ApiRequestError);
    expect((err as ApiRequestError).status).toBe(401);
    expect(localStorage.getItem('auth_token')).toBe('tok');
    expect(hrefs).toEqual([]);
    expect(calls).toEqual([]);
  });

  test('clears the configured tokenStorageKey', async () => {
    configureUsersApi({ tokenStorageKey: 'custom_key' });
    localStorage.setItem('custom_key', 'c');
    await hit();
    expect(getTokenStorageKey()).toBe('custom_key');
    expect(localStorage.getItem('custom_key')).toBeNull();
    expect(localStorage.getItem('auth_token')).toBe('tok');
  });
});

describe('configured redirect', () => {
  test('custom redirect URL without a return param', async () => {
    configureUsersApi({ unauthenticatedRedirectUrl: '/login' });
    await hit();
    expect(hrefs).toEqual(['/login']);
  });

  test('return param appends the encoded path, dropping the hash', async () => {
    configureUsersApi({
      unauthenticatedRedirectUrl: '/login',
      unauthenticatedReturnParam: 'return',
    });
    await hit();
    expect(hrefs).toEqual(['/login?return=%2Fdeployments%2F3%3Ftab%3Da']);
  });

  test('a target already containing ? uses &', async () => {
    configureUsersApi({
      unauthenticatedRedirectUrl: '/login?reason=expired',
      unauthenticatedReturnParam: 'return',
    });
    await hit();
    expect(hrefs).toEqual(['/login?reason=expired&return=%2Fdeployments%2F3%3Ftab%3Da']);
  });

  test('absolute https target works', async () => {
    configureUsersApi({
      unauthenticatedRedirectUrl: 'https://manager.example.test/login',
    });
    await hit();
    expect(hrefs).toEqual(['https://manager.example.test/login']);
  });

  test('same-path guard: token cleared, no navigation, with a return param', async () => {
    stubLocation('http://app.test/login');
    configureUsersApi({
      unauthenticatedRedirectUrl: '/login',
      unauthenticatedReturnParam: 'return',
    });
    await hit();
    expect(localStorage.getItem('auth_token')).toBeNull();
    expect(hrefs).toEqual([]);
  });

  test('same-path guard applies to a same-origin absolute URL', async () => {
    stubLocation('http://app.test/login');
    configureUsersApi({
      unauthenticatedRedirectUrl: 'http://app.test/login',
      unauthenticatedReturnParam: 'return',
    });
    await hit();
    expect(hrefs).toEqual([]);
  });

  test('without a return param the default still navigates at the target path', async () => {
    stubLocation('http://app.test/auth/login');
    await hit();
    expect(hrefs).toEqual(['/auth/login']);
  });

  test('returnPath falls back to / when the location path is unsafe', async () => {
    stubLocation('http://app.test//evil');
    const calls: UnauthenticatedContext[] = [];
    configureUsersApi({ onUnauthenticated: (c) => calls.push(c) });
    await hit();
    expect(calls).toEqual([{ returnPath: '/' }]);
  });
});

describe('onUnauthenticated', () => {
  test('receives { returnPath } after the token is cleared; redirect URL unused', async () => {
    const seen: { ctx: UnauthenticatedContext; tokenAtCall: string | null }[] = [];
    configureUsersApi({
      unauthenticatedRedirectUrl: '/login',
      onUnauthenticated: (ctx) =>
        seen.push({ ctx, tokenAtCall: localStorage.getItem('auth_token') }),
    });
    const err = await hit();
    expect(err).toBeInstanceOf(ApiRequestError);
    expect(seen).toEqual([
      { ctx: { returnPath: '/deployments/3?tab=a' }, tokenAtCall: null },
    ]);
    expect(hrefs).toEqual([]);
  });

  test('null restores the default handler', async () => {
    configureUsersApi({ onUnauthenticated: () => {} });
    configureUsersApi({ onUnauthenticated: null });
    await hit();
    expect(hrefs).toEqual(['/auth/login']);
  });
});

describe('validation', () => {
  const badUrls = [
    '//evil.test',
    'javascript:alert(1)',
    'login',
    '/a\\b',
    'ftp://x',
    '/a\nb',
    '',
  ];
  for (const bad of badUrls) {
    test(`rejects unauthenticatedRedirectUrl ${JSON.stringify(bad)} and leaves config intact`, async () => {
      configureUsersApi({
        unauthenticatedRedirectUrl: '/login',
        tokenStorageKey: 'k1',
      });
      expect(() =>
        configureUsersApi({
          tokenStorageKey: 'k2',
          unauthenticatedRedirectUrl: bad,
        }),
      ).toThrow(TypeError);
      expect(getTokenStorageKey()).toBe('k1');
      localStorage.setItem('k1', 'x');
      await hit();
      expect(hrefs).toEqual(['/login']);
    });
  }

  for (const bad of ['a b', '', 'a/b', 5 as unknown as string]) {
    test(`rejects unauthenticatedReturnParam ${JSON.stringify(bad)}`, () => {
      configureUsersApi({ tokenStorageKey: 'k1' });
      expect(() =>
        configureUsersApi({
          tokenStorageKey: 'k2',
          unauthenticatedReturnParam: bad,
        }),
      ).toThrow(TypeError);
      expect(getTokenStorageKey()).toBe('k1');
    });
  }

  test('rejects a non-function onUnauthenticated', () => {
    expect(() =>
      configureUsersApi({
        onUnauthenticated: 'nope' as unknown as () => void,
      }),
    ).toThrow(TypeError);
  });

  test('null return param explicitly disables', async () => {
    configureUsersApi({ unauthenticatedReturnParam: 'return' });
    configureUsersApi({ unauthenticatedReturnParam: null });
    await hit();
    expect(hrefs).toEqual(['/auth/login']);
  });

  test('resetUsersApiConfig restores the new fields', async () => {
    configureUsersApi({
      unauthenticatedRedirectUrl: '/login',
      unauthenticatedReturnParam: 'return',
      onUnauthenticated: () => {},
    });
    resetUsersApiConfig();
    await hit();
    expect(hrefs).toEqual(['/auth/login']);
  });
});

describe('throwing onUnauthenticated', () => {
  test('does not replace the 401: token cleared, error logged, ApiRequestError 401 thrown', async () => {
    const errorSpy = spyOn(console, 'error').mockImplementation(() => {});
    try {
      configureUsersApi({
        onUnauthenticated: () => {
          throw new Error('handler boom');
        },
      });
      const err = await hit();
      expect(err instanceof ApiRequestError).toBe(true);
      expect((err as ApiRequestError).status).toBe(401);
      expect(localStorage.getItem('auth_token')).toBeNull();
      expect(errorSpy).toHaveBeenCalled();
      expect(hrefs).toEqual([]);
    } finally {
      errorSpy.mockRestore();
    }
  });
});

describe('return value round-trip', () => {
  function roundTrip(pageUrl: string): string | null {
    stubLocation(pageUrl);
    configureUsersApi({
      unauthenticatedRedirectUrl: '/login',
      unauthenticatedReturnParam: 'return',
    });
    handleUnauthenticated();
    expect(hrefs.length).toBe(1);
    const written = new URL(hrefs[0], 'http://app.test');
    stubLocation(`http://app.test${written.pathname}${written.search}`);
    return readReturnPath();
  }

  test('colon-in-first-segment page falls back to / and the reader accepts it', () => {
    expect(roundTrip('http://app.test/team:alpha/x')).toBe('/');
  });

  test('colon in the query of a slashless path falls back consistently', () => {
    expect(roundTrip('http://app.test/team?next=a:b')).toBe('/');
  });

  test('an ordinary page round-trips unchanged', () => {
    expect(roundTrip('http://app.test/deployments/3?tab=a:b')).toBe(
      '/deployments/3?tab=a:b',
    );
  });

  test('custom handler still receives the colon path as returnPath', () => {
    stubLocation('http://app.test/team:alpha/x');
    const calls: UnauthenticatedContext[] = [];
    configureUsersApi({ onUnauthenticated: (c) => calls.push(c) });
    handleUnauthenticated();
    expect(calls).toEqual([{ returnPath: '/team:alpha/x' }]);
  });
});
