// Runtime configuration for the users GUI module. Imports nothing from the
// other lib modules so it can never participate in an import cycle. Values are
// read lazily (at call time) so consumers may configure after import.

/** Default localStorage key under which the auth token is stored. */
export const USERS_TOKEN_KEY = 'auth_token';

const DEFAULT_BASE_URL = 'http://localhost:8080';

export interface UsersApiConfig {
  /**
   * API base URL. `''` means same-origin; otherwise an absolute http(s) URL or
   * a single-slash-rooted path. Trailing slashes are stripped.
   */
  baseUrl?: string;
  /** localStorage key holding the auth token. Must be a non-empty string. */
  tokenStorageKey?: string;
}

let configuredBaseUrl: string | undefined;
let configuredTokenKey: string | undefined;

// eslint-disable-next-line no-control-regex
const CONTROL_CHARS = /[\u0000-\u001f\u007f]/;

function normalizeBaseUrl(value: unknown): string {
  if (typeof value !== 'string') {
    throw new TypeError('configureUsersApi: baseUrl must be a string');
  }
  if (CONTROL_CHARS.test(value) || value.includes('\\')) {
    throw new TypeError(
      'configureUsersApi: baseUrl must not contain control characters or backslashes',
    );
  }
  if (value === '') return '';
  if (/^https?:\/\//i.test(value)) {
    let parsed: URL;
    try {
      parsed = new URL(value);
    } catch {
      throw new TypeError('configureUsersApi: baseUrl is not a valid URL');
    }
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      throw new TypeError('configureUsersApi: baseUrl must be http(s)');
    }
    return value.replace(/\/+$/, '');
  }
  if (value.startsWith('/') && !value.startsWith('//')) {
    return value.replace(/\/+$/, '');
  }
  throw new TypeError(
    "configureUsersApi: baseUrl must be '', an absolute http(s) URL, or a path starting with a single '/'",
  );
}

/**
 * Configure the users API at runtime. An omitted/undefined field leaves the
 * current value unchanged. All fields are validated before any is applied; on
 * a validation failure a TypeError is thrown and nothing changes.
 */
export function configureUsersApi(config: UsersApiConfig): void {
  const nextBaseUrl =
    config.baseUrl !== undefined ? normalizeBaseUrl(config.baseUrl) : undefined;
  if (config.tokenStorageKey !== undefined) {
    if (
      typeof config.tokenStorageKey !== 'string' ||
      config.tokenStorageKey === ''
    ) {
      throw new TypeError(
        'configureUsersApi: tokenStorageKey must be a non-empty string',
      );
    }
  }
  if (nextBaseUrl !== undefined) configuredBaseUrl = nextBaseUrl;
  if (config.tokenStorageKey !== undefined) {
    configuredTokenKey = config.tokenStorageKey;
  }
}

/** Restores all defaults. Primarily a test helper. */
export function resetUsersApiConfig(): void {
  configuredBaseUrl = undefined;
  configuredTokenKey = undefined;
}

/**
 * Resolves the API base URL: configured value, then NEXT_PUBLIC_API_BASE_URL
 * (when defined, including ''), then window.__USERS_API_URL__ (when a string,
 * including ''), then the localhost default. Evaluated on every call.
 */
export function getApiBaseUrl(): string {
  if (configuredBaseUrl !== undefined) return configuredBaseUrl;
  // Keep this literal form so Next.js build-time inlining keeps working.
  if (
    typeof process !== 'undefined' &&
    process.env.NEXT_PUBLIC_API_BASE_URL !== undefined
  ) {
    return process.env.NEXT_PUBLIC_API_BASE_URL;
  }
  if (typeof window !== 'undefined') {
    const w = (window as { __USERS_API_URL__?: unknown }).__USERS_API_URL__;
    if (typeof w === 'string') return w;
  }
  return DEFAULT_BASE_URL;
}

export function getTokenStorageKey(): string {
  return configuredTokenKey ?? USERS_TOKEN_KEY;
}

/** Returns the stored token, or null when unavailable (e.g. SSR). */
export function getStoredToken(): string | null {
  if (typeof window === 'undefined' || typeof localStorage === 'undefined') {
    return null;
  }
  return localStorage.getItem(getTokenStorageKey());
}

/** Removes the stored token; a no-op without a window. */
export function clearStoredToken(): void {
  if (typeof window === 'undefined' || typeof localStorage === 'undefined') {
    return;
  }
  localStorage.removeItem(getTokenStorageKey());
}
