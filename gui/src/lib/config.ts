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
  /**
   * Where a 401 response sends the browser (default `'/auth/login'`). Either a
   * path starting with exactly one `/`, or an absolute http(s) URL. Apps with
   * a client-side router should also set `AuthProvider`'s `loginPath`.
   */
  unauthenticatedRedirectUrl?: string;
  /**
   * Query parameter name under which the current path is appended to the
   * redirect target on a 401 (e.g. `'return'`). `null` (the default) appends
   * nothing. Must match `/^[A-Za-z0-9_-]+$/`.
   */
  unauthenticatedReturnParam?: string | null;
  /**
   * Custom 401 handler, called after the stored token is cleared; replaces the
   * default redirect. `null` restores the default handler.
   */
  onUnauthenticated?: ((ctx: UnauthenticatedContext) => void) | null;
}

/** Context handed to a custom `onUnauthenticated` handler. */
export interface UnauthenticatedContext {
  /** `location.pathname + location.search` (never the hash), or `'/'`. */
  returnPath: string;
}

export const DEFAULT_UNAUTHENTICATED_REDIRECT_URL = '/auth/login';

let configuredBaseUrl: string | undefined;
let configuredTokenKey: string | undefined;
let configuredRedirectUrl: string | undefined;
let configuredReturnParam: string | null | undefined;
let configuredOnUnauthenticated:
  | ((ctx: UnauthenticatedContext) => void)
  | null
  | undefined;

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

/** True for a site-relative path: one leading `/`, no `//`, backslash, or control chars. */
export function isSafeSitePath(value: unknown): boolean {
  return (
    typeof value === 'string' &&
    value.startsWith('/') &&
    !value.startsWith('//') &&
    !value.includes('\\') &&
    !CONTROL_CHARS.test(value)
  );
}

function validateRedirectUrl(value: unknown): string {
  if (typeof value !== 'string') {
    throw new TypeError(
      'configureUsersApi: unauthenticatedRedirectUrl must be a string',
    );
  }
  if (isSafeSitePath(value)) return value;
  if (
    /^https?:\/\//i.test(value) &&
    !CONTROL_CHARS.test(value) &&
    !value.includes('\\')
  ) {
    try {
      const parsed = new URL(value);
      if (parsed.protocol === 'http:' || parsed.protocol === 'https:') {
        return value;
      }
    } catch {
      // fall through to the TypeError below
    }
  }
  throw new TypeError(
    "configureUsersApi: unauthenticatedRedirectUrl must be a path starting with a single '/' or an absolute http(s) URL",
  );
}

function validateReturnParam(value: unknown): string | null {
  if (value === null) return null;
  if (typeof value !== 'string' || !/^[A-Za-z0-9_-]+$/.test(value)) {
    throw new TypeError(
      'configureUsersApi: unauthenticatedReturnParam must be null or match /^[A-Za-z0-9_-]+$/',
    );
  }
  return value;
}

function validateOnUnauthenticated(
  value: unknown,
): ((ctx: UnauthenticatedContext) => void) | null {
  if (value === null) return null;
  if (typeof value !== 'function') {
    throw new TypeError(
      'configureUsersApi: onUnauthenticated must be a function or null',
    );
  }
  return value as (ctx: UnauthenticatedContext) => void;
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
  const nextRedirectUrl =
    config.unauthenticatedRedirectUrl !== undefined
      ? validateRedirectUrl(config.unauthenticatedRedirectUrl)
      : undefined;
  const nextReturnParam =
    config.unauthenticatedReturnParam !== undefined
      ? validateReturnParam(config.unauthenticatedReturnParam)
      : undefined;
  const nextOnUnauth =
    config.onUnauthenticated !== undefined
      ? validateOnUnauthenticated(config.onUnauthenticated)
      : undefined;
  if (nextBaseUrl !== undefined) configuredBaseUrl = nextBaseUrl;
  if (config.tokenStorageKey !== undefined) {
    configuredTokenKey = config.tokenStorageKey;
  }
  if (nextRedirectUrl !== undefined) configuredRedirectUrl = nextRedirectUrl;
  if (nextReturnParam !== undefined) configuredReturnParam = nextReturnParam;
  if (nextOnUnauth !== undefined) configuredOnUnauthenticated = nextOnUnauth;
}

/** Restores all defaults. Primarily a test helper. */
export function resetUsersApiConfig(): void {
  configuredBaseUrl = undefined;
  configuredTokenKey = undefined;
  configuredRedirectUrl = undefined;
  configuredReturnParam = undefined;
  configuredOnUnauthenticated = undefined;
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

function currentReturnPath(): string {
  if (typeof window === 'undefined') return '/';
  const candidate = window.location.pathname + window.location.search;
  return isSafeSitePath(candidate) ? candidate : '/';
}

/** Pathname of `target` when it points at this origin, else null. */
function sameOriginPathname(target: string): string | null {
  if (target.startsWith('/')) return target.split(/[?#]/)[0];
  try {
    const parsed = new URL(target);
    return parsed.origin === window.location.origin ? parsed.pathname : null;
  } catch {
    return null;
  }
}

/**
 * Shared 401 handling for `request()`: clears the stored token, then runs the
 * custom `onUnauthenticated` handler if configured, else the default redirect.
 * A no-op without a window (SSR).
 */
export function handleUnauthenticated(): void {
  if (typeof window === 'undefined') return;
  clearStoredToken();
  const returnPath = currentReturnPath();
  if (configuredOnUnauthenticated) {
    configuredOnUnauthenticated({ returnPath });
    return;
  }
  let target = configuredRedirectUrl ?? DEFAULT_UNAUTHENTICATED_REDIRECT_URL;
  const param = configuredReturnParam ?? null;
  if (param !== null) {
    if (sameOriginPathname(target) === window.location.pathname) return;
    target += `${target.includes('?') ? '&' : '?'}${param}=${encodeURIComponent(returnPath)}`;
  }
  window.location.href = target;
}
