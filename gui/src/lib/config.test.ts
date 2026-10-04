import { afterEach, describe, expect, test } from 'bun:test';
import { api, fetchProviders } from './api';
import {
  clearStoredToken,
  configureUsersApi,
  getApiBaseUrl,
  getStoredToken,
  getTokenStorageKey,
  resetUsersApiConfig,
  USERS_TOKEN_KEY,
} from './config';

const originalFetch = globalThis.fetch;
const originalEnv = process.env.NEXT_PUBLIC_API_BASE_URL;
type W = { __USERS_API_URL__?: string };

afterEach(() => {
  resetUsersApiConfig();
  localStorage.clear();
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
    localStorage.setItem('auth_token', 'd');
    expect(getStoredToken()).toBe('d');
  });

  test('requests send the Bearer from the custom key only', async () => {
    const seen = captureFetch({});
    localStorage.setItem('auth_token', 'default-token');
    localStorage.setItem('custom_key', 'custom-token');
    configureUsersApi({ tokenStorageKey: 'custom_key' });
    await api.self.get();
    expect(seen.headers[0]!['Authorization']).toBe('Bearer custom-token');

    localStorage.removeItem('custom_key');
    await api.self.get();
    expect(seen.headers[1]!['Authorization']).toBeUndefined();
  });

  test('getStoredToken and clearStoredToken honor the key', () => {
    configureUsersApi({ tokenStorageKey: 'custom_key' });
    localStorage.setItem('custom_key', 'c');
    localStorage.setItem('auth_token', 'd');
    expect(getStoredToken()).toBe('c');
    clearStoredToken();
    expect(localStorage.getItem('custom_key')).toBeNull();
    expect(localStorage.getItem('auth_token')).toBe('d');
  });

  test('a 401 clears only the custom key', async () => {
    globalThis.fetch = (async () =>
      new Response(JSON.stringify({ error: { code: 'unauthenticated', message: 'x' } }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })) as unknown as typeof fetch;
    configureUsersApi({ tokenStorageKey: 'custom_key' });
    localStorage.setItem('custom_key', 'c');
    localStorage.setItem('auth_token', 'd');
    await api.self.get().catch(() => {});
    expect(localStorage.getItem('custom_key')).toBeNull();
    expect(localStorage.getItem('auth_token')).toBe('d');
  });
});
