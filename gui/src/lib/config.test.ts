import { afterEach, describe, expect, test } from 'bun:test';
import { api, fetchProviders } from './api';
import {
  clearStoredHandoffVerifier,
  clearStoredSession,
  clearStoredToken,
  configureUsersApi,
  DEFAULT_FORK_TIMEOUT_MS,
  getApiBaseUrl,
  getForkTimeoutMs,
  getStoredHandoffVerifier,
  getStoredProfileUuid,
  getStoredRefreshToken,
  getStoredToken,
  getTokenStorageKey,
  resetUsersApiConfig,
  setStoredHandoffVerifier,
  setStoredProfileUuid,
  setStoredRefreshToken,
  setStoredToken,
  USERS_TOKEN_KEY,
} from './config';

const originalFetch = globalThis.fetch;
const originalEnv = process.env.NEXT_PUBLIC_API_BASE_URL;
type W = { __USERS_API_URL__?: string };

afterEach(() => {
  resetUsersApiConfig();
  sessionStorage.clear();
  globalThis.fetch = originalFetch;
  delete (window as W).__USERS_API_URL__;
  if (originalEnv === undefined) delete process.env.NEXT_PUBLIC_API_BASE_URL;
  else process.env.NEXT_PUBLIC_API_BASE_URL = originalEnv;
});

function clearEnv(): void {
  delete process.env.NEXT_PUBLIC_API_BASE_URL;
}

function captureFetch(body: unknown = []): { urls: string[]; headers: Record<string, string>[] } {
  const seen = { urls: [] as string[], headers: [] as Record<string, string>[] };
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    seen.urls.push(String(url));
    seen.headers.push((init?.headers ?? {}) as Record<string, string>);
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }) as unknown as typeof fetch;
  return seen;
}

describe('getApiBaseUrl precedence', () => {
  test('defaults to localhost:8080 with nothing set', () => {
    clearEnv();
    expect(getApiBaseUrl()).toBe('http://localhost:8080');
  });

  test('window.__USERS_API_URL__ empty string yields empty string', () => {
    clearEnv();
    (window as W).__USERS_API_URL__ = '';
    expect(getApiBaseUrl()).toBe('');
  });

  test('window.__USERS_API_URL__ value is used', () => {
    clearEnv();
    (window as W).__USERS_API_URL__ = 'https://w.test';
    expect(getApiBaseUrl()).toBe('https://w.test');
  });

  test('env var: empty string and URL win over window; unset falls through', () => {
    (window as W).__USERS_API_URL__ = 'https://w.test';
    process.env.NEXT_PUBLIC_API_BASE_URL = '';
    expect(getApiBaseUrl()).toBe('');
    process.env.NEXT_PUBLIC_API_BASE_URL = 'https://env.test';
    expect(getApiBaseUrl()).toBe('https://env.test');
    clearEnv();
    expect(getApiBaseUrl()).toBe('https://w.test');
  });

  test('configureUsersApi beats env and window', () => {
    process.env.NEXT_PUBLIC_API_BASE_URL = 'https://env.test';
    (window as W).__USERS_API_URL__ = 'https://w.test';
    configureUsersApi({ baseUrl: 'https://cfg.test' });
    expect(getApiBaseUrl()).toBe('https://cfg.test');
  });

  test('configured empty string beats env', () => {
    process.env.NEXT_PUBLIC_API_BASE_URL = 'https://env.test';
    configureUsersApi({ baseUrl: '' });
    expect(getApiBaseUrl()).toBe('');
  });
});

describe('lazy evaluation', () => {
  test('same-origin baseUrl produces relative /v1/ URLs and reconfiguring takes effect', async () => {
    const seen = captureFetch({ token: 't', user: {} });
    configureUsersApi({ baseUrl: '' });
    await api.auth.login('a@b.test', 'pw');
    await fetchProviders();
    expect(seen.urls[0]!.startsWith('/v1/')).toBe(true);
    expect(seen.urls[1]!.startsWith('/v1/')).toBe(true);
    expect(seen.urls.some((u) => u.includes('localhost:8080'))).toBe(false);

    configureUsersApi({ baseUrl: 'https://x.test' });
    await fetchProviders();
    expect(seen.urls[2]).toBe('https://x.test/v1/auth/providers');
    expect(api.baseUrl).toBe('https://x.test');
  });
});

describe('validation', () => {
  const bad: unknown[] = [
    '//evil.test',
    'javascript:alert(1)',
    'ftp://x',
    'a\\b',
    '/a\\b',
    '/a\nb',
    42,
    null,
  ];
  for (const value of bad) {
    test(`rejects ${JSON.stringify(value)} and leaves prior config intact`, () => {
      configureUsersApi({ baseUrl: 'https://prior.test', tokenStorageKey: 'prior_key' });
      expect(() =>
        configureUsersApi({ baseUrl: value as string, tokenStorageKey: 'new_key' }),
      ).toThrow(TypeError);
      expect(getApiBaseUrl()).toBe('https://prior.test');
      expect(getTokenStorageKey()).toBe('prior_key');
    });
  }

  test('rejects empty or non-string tokenStorageKey without changing baseUrl', () => {
    configureUsersApi({ baseUrl: 'https://prior.test' });
    expect(() => configureUsersApi({ baseUrl: 'https://new.test', tokenStorageKey: '' })).toThrow(TypeError);
    expect(() => configureUsersApi({ tokenStorageKey: 5 as unknown as string })).toThrow(TypeError);
    expect(getApiBaseUrl()).toBe('https://prior.test');
  });

  test('normalizes trailing slashes', () => {
    configureUsersApi({ baseUrl: '/api/' });
    expect(getApiBaseUrl()).toBe('/api');
    configureUsersApi({ baseUrl: 'https://a.test/' });
    expect(getApiBaseUrl()).toBe('https://a.test');
    configureUsersApi({ baseUrl: '/' });
    expect(getApiBaseUrl()).toBe('');
  });

  test('omitted fields leave current values unchanged', () => {
    configureUsersApi({ baseUrl: 'https://a.test', tokenStorageKey: 'k' });
    configureUsersApi({ baseUrl: undefined });
    configureUsersApi({});
    expect(getApiBaseUrl()).toBe('https://a.test');
    expect(getTokenStorageKey()).toBe('k');
  });
});

describe('token key', () => {
  test('default key is auth_token', () => {
    expect(getTokenStorageKey()).toBe(USERS_TOKEN_KEY);
    sessionStorage.setItem('auth_token', 'd');
    expect(getStoredToken()).toBe('d');
  });

  test('requests send the Bearer from the custom key only', async () => {
    const seen = captureFetch({});
    sessionStorage.setItem('auth_token', 'default-token');
    sessionStorage.setItem('custom_key', 'custom-token');
    configureUsersApi({ tokenStorageKey: 'custom_key' });
    await api.me.get();
    expect(seen.headers[0]!['Authorization']).toBe('Bearer custom-token');

    sessionStorage.removeItem('custom_key');
    await api.me.get();
    expect(seen.headers[1]!['Authorization']).toBeUndefined();
  });

  test('getStoredToken and clearStoredToken honor the key', () => {
    configureUsersApi({ tokenStorageKey: 'custom_key' });
    sessionStorage.setItem('custom_key', 'c');
    sessionStorage.setItem('auth_token', 'd');
    expect(getStoredToken()).toBe('c');
    clearStoredToken();
    expect(sessionStorage.getItem('custom_key')).toBeNull();
    expect(sessionStorage.getItem('auth_token')).toBe('d');
  });

  test('a 401 clears only the custom key', async () => {
    globalThis.fetch = (async () =>
      new Response(JSON.stringify({ error: { code: 'unauthenticated', message: 'x' } }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })) as unknown as typeof fetch;
    configureUsersApi({ tokenStorageKey: 'custom_key' });
    sessionStorage.setItem('custom_key', 'c');
    sessionStorage.setItem('auth_token', 'd');
    await api.me.get().catch(() => {});
    expect(sessionStorage.getItem('custom_key')).toBeNull();
    expect(sessionStorage.getItem('auth_token')).toBe('d');
  });
});

describe('per-tab storage', () => {
  test('access, refresh and profile live in sessionStorage under suffixed keys, never localStorage', () => {
    setStoredToken('a');
    setStoredRefreshToken('r');
    setStoredProfileUuid('p');
    setStoredHandoffVerifier('v');
    expect(sessionStorage.getItem('auth_token')).toBe('a');
    expect(sessionStorage.getItem('auth_token.refresh')).toBe('r');
    expect(sessionStorage.getItem('auth_token.profile')).toBe('p');
    expect(sessionStorage.getItem('auth_token.handoff_verifier')).toBe('v');
    // Assertion that the persistent store is unused.
    expect(localStorage.length).toBe(0);
    expect(getStoredToken()).toBe('a');
    expect(getStoredRefreshToken()).toBe('r');
    expect(getStoredProfileUuid()).toBe('p');
    expect(getStoredHandoffVerifier()).toBe('v');
  });

  test('the suffixed keys follow a configured tokenStorageKey', () => {
    configureUsersApi({ tokenStorageKey: 'custom_key' });
    setStoredToken('a');
    setStoredRefreshToken('r');
    setStoredProfileUuid('p');
    expect(sessionStorage.getItem('custom_key')).toBe('a');
    expect(sessionStorage.getItem('custom_key.refresh')).toBe('r');
    expect(sessionStorage.getItem('custom_key.profile')).toBe('p');
    expect(sessionStorage.getItem('auth_token')).toBeNull();
  });

  test('clearStoredSession removes access, refresh and profile but not the handoff verifier', () => {
    setStoredToken('a');
    setStoredRefreshToken('r');
    setStoredProfileUuid('p');
    setStoredHandoffVerifier('v');
    clearStoredSession();
    expect(getStoredToken()).toBeNull();
    expect(getStoredRefreshToken()).toBeNull();
    expect(getStoredProfileUuid()).toBeNull();
    expect(getStoredHandoffVerifier()).toBe('v');
    clearStoredHandoffVerifier();
    expect(getStoredHandoffVerifier()).toBeNull();
  });

  test('falls back to memory when sessionStorage throws, and reads, overwrites and clears there', () => {
    const real = Object.getOwnPropertyDescriptor(window, 'sessionStorage');
    Object.defineProperty(window, 'sessionStorage', {
      configurable: true,
      get() {
        throw new Error('storage blocked');
      },
    });
    try {
      setStoredToken('mem-a');
      expect(getStoredToken()).toBe('mem-a');
      setStoredToken('mem-b');
      expect(getStoredToken()).toBe('mem-b');
      clearStoredSession();
      expect(getStoredToken()).toBeNull();
    } finally {
      if (real) Object.defineProperty(window, 'sessionStorage', real);
    }
    // The blocked-storage session never leaked into the real store.
    expect(sessionStorage.getItem('auth_token')).toBeNull();
  });

  test('a storage write that throws (quota) keeps the value in memory', () => {
    const realSet = Storage.prototype.setItem;
    Storage.prototype.setItem = () => {
      throw new Error('QuotaExceededError');
    };
    try {
      setStoredToken('quota-token');
      expect(getStoredToken()).toBe('quota-token');
    } finally {
      Storage.prototype.setItem = realSet;
    }
    // A later successful write supersedes the memory copy.
    setStoredToken('stored-token');
    expect(getStoredToken()).toBe('stored-token');
    clearStoredToken();
    expect(getStoredToken()).toBeNull();
  });

  test('requests send Bearer plus credentials omit', async () => {
    const calls: RequestInit[] = [];
    globalThis.fetch = (async (_url: string, init?: RequestInit) => {
      calls.push(init ?? {});
      return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
    }) as unknown as typeof fetch;
    setStoredToken('tab-token');
    await api.me.get();
    expect(calls[0]!.credentials).toBe('omit');
    expect((calls[0]!.headers as Record<string, string>)['Authorization']).toBe('Bearer tab-token');
  });
});

describe('forkTimeoutMs', () => {
  test('defaults, accepts a positive integer, and resets', () => {
    expect(getForkTimeoutMs()).toBe(DEFAULT_FORK_TIMEOUT_MS);
    configureUsersApi({ forkTimeoutMs: 250 });
    expect(getForkTimeoutMs()).toBe(250);
    resetUsersApiConfig();
    expect(getForkTimeoutMs()).toBe(DEFAULT_FORK_TIMEOUT_MS);
  });

  for (const bad of [0, -1, 1.5, 30001, Number.NaN, '5' as unknown as number]) {
    test(`rejects ${String(bad)} and leaves other fields unapplied`, () => {
      configureUsersApi({ baseUrl: 'https://prior.test' });
      expect(() =>
        configureUsersApi({ baseUrl: 'https://new.test', forkTimeoutMs: bad }),
      ).toThrow(TypeError);
      expect(getApiBaseUrl()).toBe('https://prior.test');
      expect(getForkTimeoutMs()).toBe(DEFAULT_FORK_TIMEOUT_MS);
    });
  }
});
