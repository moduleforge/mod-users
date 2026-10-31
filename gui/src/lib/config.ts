// Runtime configuration and per-tab token storage for the users GUI module.
// Imports only the dependency-free `routes` constants so it can never
// participate in an import cycle. Values are read lazily (at call time) so
// consumers may configure after import.

import { USERS_GUI_ROUTES } from './routes';

/**
 * Default sessionStorage key under which the tab's access token is stored.
 * This is the key `@moduleforge/core-gui`'s api-client reads (decision D17), so
 * a host app's other module clients send the same tab's token.
 */
export const USERS_TOKEN_KEY = 'auth_token';

/** Default time a new tab waits for a sibling tab to offer an access token. */
export const DEFAULT_FORK_TIMEOUT_MS = 1500;

const DEFAULT_BASE_URL = 'http://localhost:8080';

export interface UsersApiConfig {
  /**
   * API base URL. `''` means same-origin; otherwise an absolute http(s) URL or
   * a single-slash-rooted path. Trailing slashes are stripped.
   *
   * Trust note: this configured value is validated by `configureUsersApi`, but
   * the fallbacks used when it is unset (`NEXT_PUBLIC_API_BASE_URL` and
   * `window.__USERS_API_URL__`, see `getApiBaseUrl`) are trusted deploy-time
   * inputs used as-is, WITHOUT that validation. The bearer token is sent to
   * whatever base URL is resolved, so those fallbacks must come only from
   * deploy-time configuration, never from user-controlled input.
   */
  baseUrl?: string;
  /**
   * sessionStorage key holding the tab's access token (default `'auth_token'`,
   * the key core-gui's api-client reads). The refresh token, active profile
   * uuid and handoff verifier are stored under suffixed variants of this key.
   * Must be a non-empty string.
   */
  tokenStorageKey?: string;
  /**
   * How long (ms) a tab that starts with no tokens waits for a sibling tab to
   * answer its BroadcastChannel fork request before it stays signed out
   * (default 1500). A positive integer no larger than 30000.
   */
  forkTimeoutMs?: number;
  /**
   * Where an unauthenticated response (a 401 that a token refresh cannot
   * repair, or a revoked session) sends the browser when no
   * `onUnauthenticated` handler is configured (default: the login route
   * `USERS_GUI_ROUTES.login`). Either a path starting with exactly one `/`, or
   * an absolute http(s) URL. Apps with a client-side router should also set
   * `AuthProvider`'s `loginPath`.
   *
   * Security note: an absolute URL target combined with
   * `unauthenticatedReturnParam` sends the current `pathname + search` to that
   * origin as a query parameter. Only point it at an origin you trust with
   * those values.
   */
  unauthenticatedRedirectUrl?: string;
  /**
   * Query parameter name under which the current path is appended to the
   * redirect target on a 401 (e.g. `'return'`). `null` (the default) appends
   * nothing. Must match `/^[A-Za-z0-9_-]+$/`. When the redirect target is an
   * absolute URL, the current `pathname + search` is sent to that origin (see
   * `unauthenticatedRedirectUrl`). The appended value is always one
   * `isSafeReturnPath` accepts; a current page it would reject (for example
   * `/team:alpha/x`) is replaced by `'/'`.
   */
  unauthenticatedReturnParam?: string | null;
  /**
   * Custom unauthenticated handler, called after the tab's tokens are cleared;
   * replaces the default redirect. `null` restores the default handler. If the
   * handler throws, the error is logged via `console.error` and the failure is
   * still surfaced to the caller as usual.
   */
  onUnauthenticated?: ((ctx: UnauthenticatedContext) => void) | null;
}

/** Context handed to a custom `onUnauthenticated` handler. */
export interface UnauthenticatedContext {
  /** `location.pathname + location.search` (never the hash), or `'/'`. */
  returnPath: string;
}

/** The default unauthenticated redirect target: the standard login route. */
export const DEFAULT_UNAUTHENTICATED_REDIRECT_URL: string = USERS_GUI_ROUTES.login;

let configuredBaseUrl: string | undefined;
let configuredTokenKey: string | undefined;
let configuredForkTimeoutMs: number | undefined;
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

/**
 * True for a site-relative path: one leading `/`, no `//`, backslash, or control
 * chars. Used to validate `unauthenticatedRedirectUrl` and `AuthProvider`'s
 * `loginPath`. Deliberately does NOT apply the colon-in-first-segment rule:
 * those are developer-supplied configuration, not page-derived values, so
 * `isSafeReturnPath` (which layers that rule on top of this predicate) is the
 * single rule for any value derived from the current page.
 */
export function isSafeSitePath(value: unknown): boolean {
  return (
    typeof value === 'string' &&
    value.startsWith('/') &&
    !value.startsWith('//') &&
    !value.includes('\\') &&
    !CONTROL_CHARS.test(value)
  );
}

/**
 * Validates that a candidate return path is a safe, same-origin relative
 * path: `isSafeSitePath` plus a rejection of any `:` in the first path
 * segment (so no scheme-like `javascript:...` prefix can sneak in). Rejects
 * absolute URLs, protocol-relative URLs (`//evil.com`), backslashes and C0
 * control characters (the WHATWG URL parser strips these, so `/\t/evil.com`
 * would collapse into `//evil.com`).
 *
 * This is the one predicate for return paths: the 401 handler uses it before
 * appending a return value, and `readReturnPath`/`OidcCallbackPage` use it
 * before accepting one. Defined here (config.ts imports no other lib module)
 * and re-exported from `return-path.ts` and the package index.
 *
 * Exported so consuming apps can validate their own `?return=` values with
 * the same rules before navigating to them.
 */
export function isSafeReturnPath(candidate: string | null): candidate is string {
  if (typeof candidate !== 'string' || !isSafeSitePath(candidate)) return false;
  const firstSlashAfterStart = candidate.indexOf('/', 1);
  const firstSegment =
    firstSlashAfterStart === -1
      ? candidate.slice(1)
      : candidate.slice(1, firstSlashAfterStart);
  return !firstSegment.includes(':');
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
  // Validated before anything is applied (all-or-nothing), like every field.
  if (config.forkTimeoutMs !== undefined) {
    if (
      !Number.isInteger(config.forkTimeoutMs) ||
      config.forkTimeoutMs <= 0 ||
      config.forkTimeoutMs > 30000
    ) {
      throw new TypeError(
        'configureUsersApi: forkTimeoutMs must be a positive integer no larger than 30000',
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
  if (config.forkTimeoutMs !== undefined) {
    configuredForkTimeoutMs = config.forkTimeoutMs;
  }
  if (nextRedirectUrl !== undefined) configuredRedirectUrl = nextRedirectUrl;
  if (nextReturnParam !== undefined) configuredReturnParam = nextReturnParam;
  if (nextOnUnauth !== undefined) configuredOnUnauthenticated = nextOnUnauth;
}

/** Restores all defaults. Primarily a test helper. */
export function resetUsersApiConfig(): void {
  configuredBaseUrl = undefined;
  configuredTokenKey = undefined;
  configuredForkTimeoutMs = undefined;
  configuredRedirectUrl = undefined;
  configuredReturnParam = undefined;
  configuredOnUnauthenticated = undefined;
}

/**
 * Resolves the API base URL: configured value, then NEXT_PUBLIC_API_BASE_URL
 * (when defined, including ''), then window.__USERS_API_URL__ (when a string,
 * including ''), then the localhost default. Evaluated on every call.
 *
 * Trust note: the `NEXT_PUBLIC_API_BASE_URL` and `window.__USERS_API_URL__`
 * fallbacks are trusted deploy-time inputs, used as-is WITHOUT the validation
 * `configureUsersApi` applies to `baseUrl`. The bearer token is sent to the
 * resolved URL, so they must never carry user-controlled data.
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

/** The configured 401 return-param name, or null when none is configured. */
export function getUnauthenticatedReturnParam(): string | null {
  return configuredReturnParam ?? null;
}

export function getTokenStorageKey(): string {
  return configuredTokenKey ?? USERS_TOKEN_KEY;
}

/** The configured fork-request timeout in ms. */
export function getForkTimeoutMs(): number {
  return configuredForkTimeoutMs ?? DEFAULT_FORK_TIMEOUT_MS;
}

// ─── Per-tab storage ─────────────────────────────────────────────────────────
//
// Frontend contract: the access token, refresh token and active profile live
// in sessionStorage (one value set per tab), never in any cross-tab or
// persistent store and never in cookies. Where sessionStorage is unavailable
// (blocked, quota exhausted) a module-level in-memory map stands in, which is
// per-page-load and therefore also per tab. Without a window at all (SSR) there
// is NO fallback: a server-side module map would be shared across requests and
// could hand one user's token to another, so reads return null and writes are
// no-ops there.

const memoryStore = new Map<string, string>();

/** sessionStorage, or null when absent or when merely touching it throws. */
function tabStorage(): Storage | null {
  if (typeof window === 'undefined') return null;
  try {
    return typeof window.sessionStorage === 'undefined'
      ? null
      : window.sessionStorage;
  } catch {
    // Some browsers throw a SecurityError on access when storage is blocked;
    // that means "unavailable", so the memory fallback applies.
    return null;
  }
}

function readTab(key: string): string | null {
  if (typeof window === 'undefined') return null;
  const storage = tabStorage();
  if (storage) {
    try {
      const value = storage.getItem(key);
      if (value !== null) return value;
    } catch {
      // Fall through to the memory copy (a write that overflowed earlier).
    }
  }
  return memoryStore.get(key) ?? null;
}

function writeTab(key: string, value: string): void {
  if (typeof window === 'undefined') return;
  const storage = tabStorage();
  if (storage) {
    try {
      storage.setItem(key, value);
      // The durable copy is authoritative; drop any stale memory copy so a
      // later read can never return the older value.
      memoryStore.delete(key);
      return;
    } catch {
      // Quota or policy failure: keep the value in memory instead of losing
      // the session.
    }
  }
  memoryStore.set(key, value);
}

function removeTab(key: string): void {
  if (typeof window === 'undefined') return;
  memoryStore.delete(key);
  const storage = tabStorage();
  if (!storage) return;
  try {
    storage.removeItem(key);
  } catch {
    // Nothing further to clean up.
  }
}

const refreshKey = (): string => `${getTokenStorageKey()}.refresh`;
const profileKey = (): string => `${getTokenStorageKey()}.profile`;
const verifierKey = (): string => `${getTokenStorageKey()}.handoff_verifier`;
const selectionKey = (): string => `${getTokenStorageKey()}.selection`;
const selectionReturnKey = (): string => `${getTokenStorageKey()}.selection_return`;
const impersonatingKey = (): string => `${getTokenStorageKey()}.impersonating`;

/** Returns this tab's access token, or null when none (or unavailable, e.g. SSR). */
export function getStoredToken(): string | null {
  return readTab(getTokenStorageKey());
}

/** Stores this tab's access token (read by core-gui's api-client too). */
export function setStoredToken(token: string): void {
  writeTab(getTokenStorageKey(), token);
}

/** Removes this tab's access token only; a no-op without a window. */
export function clearStoredToken(): void {
  removeTab(getTokenStorageKey());
}

/** This tab's refresh token (its own lineage), or null. */
export function getStoredRefreshToken(): string | null {
  return readTab(refreshKey());
}

export function setStoredRefreshToken(token: string): void {
  writeTab(refreshKey(), token);
}

/** The profile uuid the refresh grant names for this tab, or null. */
export function getStoredProfileUuid(): string | null {
  return readTab(profileKey());
}

export function setStoredProfileUuid(uuid: string): void {
  writeTab(profileKey(), uuid);
}

export function clearStoredProfileUuid(): void {
  removeTab(profileKey());
}

/**
 * The pending profile-selection state (the serialized profile list a
 * `selection_required` sign-in answered), or null. Opaque here: config.ts
 * imports no other lib module, so the owner of the profile type parses it.
 */
export function getStoredPendingSelection(): string | null {
  return readTab(selectionKey());
}

export function setStoredPendingSelection(serializedProfiles: string): void {
  writeTab(selectionKey(), serializedProfiles);
}

/** Removes the pending profile list and its return path together. */
export function clearStoredPendingSelection(): void {
  removeTab(selectionKey());
  removeTab(selectionReturnKey());
}

/**
 * The return path to resume at after the pending selection, or null. Stored
 * raw; readers must still validate it with `isSafeReturnPath` because
 * sessionStorage is writable by anything running in the page.
 */
export function getStoredSelectionReturnPath(): string | null {
  return readTab(selectionReturnKey());
}

export function setStoredSelectionReturnPath(path: string): void {
  writeTab(selectionReturnKey(), path);
}

/** Removes only the stored selection return path. */
export function clearStoredSelectionReturnPath(): void {
  removeTab(selectionReturnKey());
}

/**
 * Reads the `act` (actor) claim presence from a JWT access token WITHOUT
 * verifying it. Returns true/false when the payload decodes, and null when the
 * token is not a decodable JWT (opaque or malformed). Used only for the
 * routing decision "may this request be refreshed and replayed", never for
 * authorization: the server verifies every token.
 */
export function tokenActClaim(token: string | null): boolean | null {
  if (!token) return null;
  const parts = token.split('.');
  if (parts.length !== 3) return null;
  try {
    const b64 = parts[1]!.replace(/-/g, '+').replace(/_/g, '/');
    const payload: unknown = JSON.parse(atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4)));
    if (typeof payload !== 'object' || payload === null) return null;
    const act = (payload as Record<string, unknown>)['act'];
    return act !== undefined && act !== null;
  } catch {
    // WHY: a payload that will not decode says nothing about the identity.
    // IMPLIES callers fall back to the marker rather than assuming "not impersonating".
    return null;
  }
}

/**
 * True when the access token this tab holds is an impersonation token. The
 * tab's refresh token and profile stay the administrator's own while it
 * impersonates, so nothing else in storage tells a request which identity its
 * token carries. The request pipeline reads this to refuse refresh-and-replay
 * of an impersonated request (the replay would run as the administrator).
 *
 * Fails closed: the answer is true when EITHER the per-tab marker is set OR
 * the stored token itself carries an `act` claim (decoded, not verified), so a
 * lost or never-written marker cannot turn an impersonation token into one
 * that is refreshed and replayed as the administrator. Only an opaque token
 * with no marker reads as "not impersonating"; sessionStorage is writable by
 * any script in the page, but such a script already holds the token itself.
 */
export function getStoredImpersonating(): boolean {
  if (readTab(impersonatingKey()) === '1') return true;
  return tokenActClaim(getStoredToken()) === true;
}

/** Records whether this tab's access token is an impersonation token. */
export function setStoredImpersonating(impersonating: boolean): void {
  if (impersonating) writeTab(impersonatingKey(), '1');
  else removeTab(impersonatingKey());
}

/** Clears the whole per-tab session: access token, refresh token, profile, pending selection. */
export function clearStoredSession(): void {
  removeTab(getTokenStorageKey());
  removeTab(refreshKey());
  removeTab(profileKey());
  // The marker describes the access token just removed.
  removeTab(impersonatingKey());
  // A pending selection belongs to the lineage being cleared; leaving it
  // would resurrect a selection screen for a session that no longer exists.
  clearStoredPendingSelection();
}

/** The pending OIDC sign-in attempt's handoff verifier, or null. */
export function getStoredHandoffVerifier(): string | null {
  return readTab(verifierKey());
}

export function setStoredHandoffVerifier(verifier: string): void {
  writeTab(verifierKey(), verifier);
}

export function clearStoredHandoffVerifier(): void {
  removeTab(verifierKey());
}

/** Current `pathname + search` when it is a safe site path, else `'/'`. */
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
 * Shared unauthenticated handling for `request()` and the auth context: clears
 * the tab's tokens, then runs the custom `onUnauthenticated` handler if
 * configured, else the default redirect to the configured login route. This
 * is the ONLY place the library navigates on loss of authentication, so an
 * app overrides it in one spot. A no-op without a window (SSR).
 */
export function handleUnauthenticated(): void {
  if (typeof window === 'undefined') return;
  clearStoredSession();
  const returnPath = currentReturnPath();
  if (configuredOnUnauthenticated) {
    try {
      configuredOnUnauthenticated({ returnPath });
    } catch (error) {
      // A throwing consumer handler must not replace the 401 the caller is
      // about to receive; surface it for debugging instead.
      console.error('users: onUnauthenticated handler threw', error);
    }
    return;
  }
  let target = configuredRedirectUrl ?? DEFAULT_UNAUTHENTICATED_REDIRECT_URL;
  const param = configuredReturnParam ?? null;
  if (param !== null) {
    if (sameOriginPathname(target) === window.location.pathname) return;
    // Only ever write a value readReturnPath will accept; otherwise fall back.
    const value = isSafeReturnPath(returnPath) ? returnPath : '/';
    target += `${target.includes('?') ? '&' : '?'}${param}=${encodeURIComponent(value)}`;
  }
  window.location.href = target;
}
