// ─── Types ───────────────────────────────────────────────────────────────────
//
// The wire/client error types below are the canonical shapes from
// `@moduleforge/core-gui` (docs/mf-standards/architecture/api-response-design.md
// "GUI-facing error-data contract"), imported and re-exported here rather than
// redefined locally so this module stays the single source of truth consumers
// import from (`@moduleforge/users-gui`) without diverging from the shared
// contract. The local `request()` implementation below is kept (it carries
// users-specific auth/token/redirect logic); only the types are reconciled.

import type {
  ApiError,
  ApiErrorResponse,
  FieldErrorData,
} from '@moduleforge/core-gui';
import { ApiRequestError } from '@moduleforge/core-gui';
import { getApiBaseUrl, getStoredToken, handleUnauthenticated } from './config';

export type { ApiError, ApiErrorResponse, FieldErrorData };
export { ApiRequestError };

// ─── Action-required types ──────────────────────────────────────────────────
//
// Local to this module for now — NOT imported from `@moduleforge/core-gui`,
// which does not yet export action-required wire types (Wave 0 of the
// action-required migration was Go-only). Mirrors the design doc's
// "Action-required: navigate, don't alarm" TS sketch
// (docs/mf-standards/architecture/api-response-design.md). Like `ApiError`
// et al. above, these are strong candidates for a future
// `@moduleforge/core-gui` promotion, following the same
// originate-locally-then-promote precedent those types went through.

export interface ApiAction {
  code: string;
  message: string;
  path: string;
  data?: Record<string, unknown>;
}

export interface ApiActionResponse {
  action: ApiAction;
}

/**
 * Thrown by `request()` when a non-2xx response carries a top-level `action`
 * member instead of an `error` member — a flow-control signal (navigate to
 * complete an out-of-band action), not an error. Parallel to
 * `ApiRequestError`; carries `status` for the same symmetry.
 */
export class ApiActionRequiredError extends Error {
  code: string;
  path: string;
  status: number;
  data?: Record<string, unknown>;

  constructor(
    code: string,
    message: string,
    path: string,
    status: number,
    data?: Record<string, unknown>,
  ) {
    super(message);
    this.name = 'ApiActionRequiredError';
    this.code = code;
    this.path = path;
    this.status = status;
    this.data = data;
  }
}

/**
 * Runtime shape check for a single `FieldErrorData` entry. The server
 * envelope is not user input, but a malformed/unexpected body should degrade
 * gracefully rather than making React throw at render time ("Objects are not
 * valid as a React child") wherever `<FieldError>`/`<ErrorBanner>` render
 * `details` entries.
 */
function isFieldErrorData(value: unknown): value is FieldErrorData {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as Record<string, unknown>).field === 'string' &&
    typeof (value as Record<string, unknown>).code === 'string' &&
    typeof (value as Record<string, unknown>).message === 'string'
  );
}

/** Runtime shape check for `ApiError.details`: an array of `FieldErrorData`. */
function isFieldErrorDataArray(value: unknown): value is FieldErrorData[] {
  return Array.isArray(value) && value.every(isFieldErrorData);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

/**
 * Runtime shape check for `ApiError` (the object shape `error` takes in a
 * genuine error body). Guards against the latent bug where a top-level
 * `error` member is assumed to always be an object (`errorBody.error.code`):
 * a flat string `error` (as some endpoints emit) fails this check and falls
 * back to the generic `unknown_error` code below instead of silently
 * producing an `undefined` code/message.
 */
function isApiError(value: unknown): value is ApiError {
  return (
    isRecord(value) &&
    typeof value.code === 'string' &&
    typeof value.message === 'string'
  );
}

/** Extracts a well-shaped `ApiError` from a parsed response body, if present. */
function extractApiError(body: unknown): ApiError | undefined {
  if (!isRecord(body) || !('error' in body)) return undefined;
  return isApiError(body.error) ? body.error : undefined;
}

/** Runtime shape check for `ApiAction` (the object shape `action` takes in an action-required body). */
function isApiAction(value: unknown): value is ApiAction {
  return (
    isRecord(value) &&
    typeof value.code === 'string' &&
    typeof value.message === 'string' &&
    typeof value.path === 'string' &&
    (value.data === undefined || isRecord(value.data))
  );
}

/** Extracts a well-shaped `ApiAction` from a parsed response body, if present. */
function extractApiAction(body: unknown): ApiAction | undefined {
  if (!isRecord(body) || !('action' in body)) return undefined;
  return isApiAction(body.action) ? body.action : undefined;
}

/**
 * Defense-in-depth guard on `action.path` before it is attached to a thrown
 * `ApiActionRequiredError`. Mirrors the server-side guard
 * (`WriteActionRequired` panics on a non-relative path) — the client must not
 * trust the server response unconditionally. Accepts only a single leading
 * `/` not followed by another `/` or a backslash, rejecting empty strings,
 * absolute URLs with a scheme, `//host/...`-style protocol-relative
 * authorities, and `/\host/...`-style backslash tricks some browsers
 * normalize as protocol-relative. Also strips ASCII tab/CR/LF before
 * matching: per the WHATWG URL Standard, browsers strip those characters
 * from a URL string while parsing, so a value like `"/\t/evil.example.com"`
 * would otherwise pass the leading-slash check here yet normalize to
 * `//evil.example.com` at any sink that re-parses the navigated path as a
 * URL. Falls back to a safe default route.
 */
const SAFE_ACTION_PATH_FALLBACK = '/';
const SAFE_ACTION_PATH_PATTERN = /^\/(?![/\\])/;
const ASCII_TAB_CR_LF_PATTERN = /[\t\r\n]/g;

function sanitizeActionPath(path: string): string {
  const stripped = path.replace(ASCII_TAB_CR_LF_PATTERN, '');
  if (SAFE_ACTION_PATH_PATTERN.test(stripped)) {
    return stripped;
  }
  console.error('[api] rejected unsafe action.path', path);
  return SAFE_ACTION_PATH_FALLBACK;
}

export interface RequestOptions extends RequestInit {
  /**
   * When true, a 401 response is surfaced to the caller as an
   * `ApiRequestError` without clearing the stored token or triggering a hard
   * redirect (default `/auth/login`, see `configureUsersApi`). Use this when the caller needs to handle
   * authentication failures itself (e.g., the OAuth return page, which must
   * redirect to a login URL that carries an `?error=...` message).
   *
   * Defaults to false: a 401 clears the token and hard-redirects, matching
   * the original behavior for normal authenticated requests.
   */
  skipAuthRedirect?: boolean;
}

// ─── Auth ────────────────────────────────────────────────────────────────────

export interface LoginResponse {
  token: string;
  user: UserAccountSelf;
}

export interface OIDCProvider {
  id: string;
  display_name: string;
}

export interface RegisterRequest {
  email: string;
  password: string;
  given_name: string;
  family_name: string;
}

/**
 * Response shape for `/v1/auth/register`. Unlike `LoginResponse`, this
 * intentionally carries no token/user — registration does not establish a
 * session because email verification runs asynchronously.
 */
export interface RegisterResponse {
  uuid: string;
  email: string;
  email_verification_required: boolean;
}

export interface EmailCodeRequest {
  email: string;
}

export interface EmailCodeVerifyRequest {
  email: string;
  code: string;
}

export interface ForgotPasswordRequest {
  email: string;
}

export interface ResetPasswordRequest {
  token: string;
  new_password: string;
}

// ─── User Accounts ───────────────────────────────────────────────────────────

export interface UserAccountSelf {
  uuid: string;
  entity_uuid: string;
  email: string;
  given_name: string;
  family_name: string;
  is_admin: boolean;
  created_at: string;
  updated_at: string;
}

export interface UserAccount {
  uuid: string;
  email: string;
  given_name: string;
  family_name: string;
  is_admin: boolean;
  created_at: string;
  updated_at: string;
}

export interface UserAccountListResponse {
  user_accounts: UserAccount[];
  total: number;
}

export interface UpdateProfileRequest {
  given_name?: string;
  family_name?: string;
}

// ─── Audit ───────────────────────────────────────────────────────────────────

export interface AuditEntry {
  id: string;
  actor_uuid: string;
  actor_email: string;
  action: string;
  entity_uuid: string;
  entity_type: string;
  changes: Record<string, unknown>;
  created_at: string;
}

export interface AuditListResponse {
  entries: AuditEntry[];
  total: number;
}

// ─── Apps ────────────────────────────────────────────────────────────────────

export interface App {
  uuid: string;
  name: string;
  description: string;
  created_at: string;
  updated_at: string;
}

export interface AppListResponse {
  apps: App[];
  total: number;
}

export interface AppMember {
  user_uuid: string;
  email: string;
  given_name: string;
  family_name: string;
  role: string;
  added_at: string;
}

export interface AppMembersResponse {
  members: AppMember[];
}

export interface CreateAppRequest {
  name: string;
  description?: string;
}

export interface AddAppMemberRequest {
  user_uuid: string;
  role: string;
}

// ─── Client factory ──────────────────────────────────────────────────────────

/**
 * Options for {@link createUsersClient}.
 */
export interface UsersClientOptions {
  /** A fixed base URL, or a function resolved on every request. */
  baseUrl: string | (() => string);
}

/**
 * Creates a bound users-module API client.
 *
 * Usage (in the consuming app):
 * ```ts
 * import { createUsersClient } from '@moduleforge/users-gui';
 * export const api = createUsersClient({ baseUrl: process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8080' });
 * ```
 */
export function createUsersClient({ baseUrl }: UsersClientOptions) {
  const resolveBaseUrl = (): string =>
    typeof baseUrl === 'function' ? baseUrl() : baseUrl;

  function getToken(): string | null {
    return getStoredToken();
  }

  async function request<T>(
    path: string,
    options: RequestOptions = {},
  ): Promise<T> {
    const { skipAuthRedirect = false, ...fetchOptions } = options;
    const token = getToken();
    const headers: HeadersInit = {
      'Content-Type': 'application/json',
      ...fetchOptions.headers,
    };

    if (token) {
      (headers as Record<string, string>)['Authorization'] = `Bearer ${token}`;
    }

    let response: Response;
    try {
      response = await fetch(`${resolveBaseUrl()}${path}`, {
        ...fetchOptions,
        headers,
      });
    } catch (err) {
      // Network error — API is unreachable.
      console.error(`[api] Network error: ${path}`, err);
      throw new ApiRequestError(
        'network_error',
        'Could not reach the API server. Is it running?',
        0,
      );
    }

    if (response.status === 401) {
      if (!skipAuthRedirect) {
        handleUnauthenticated();
      }
      // Unconditional: this throw is intentional and relied upon even when
      // skipAuthRedirect suppresses the redirect above — see the
      // skipAuthRedirect doc comment on RequestOptions. Only the redirect is
      // skipped; the throw always happens so opted-out callers (e.g. the
      // OAuth return page) can catch and handle the failure themselves.
      throw new ApiRequestError('unauthenticated', 'Authentication required', 401);
    }

    if (!response.ok) {
      // Parse the body once, then inspect the top-level member before
      // classifying: a body carrying `action` is a flow-control signal, not
      // an error, and MUST NEVER be thrown as ApiRequestError. This
      // short-circuit must run before any error-body handling below.
      let body: unknown;
      try {
        body = await response.json();
      } catch {
        // ignore JSON parse errors — body stays undefined, falls through to
        // the generic error path below.
      }

      const action = extractApiAction(body);
      if (action) {
        throw new ApiActionRequiredError(
          action.code,
          action.message,
          sanitizeActionPath(action.path),
          response.status,
          action.data,
        );
      }

      let errorCode = 'unknown_error';
      let errorMessage = `Request failed with status ${response.status}`;
      let errorDetails: FieldErrorData[] | undefined;
      const apiError = extractApiError(body);
      if (apiError) {
        errorCode = apiError.code;
        errorMessage = apiError.message;
        const { details } = apiError;
        if (details !== undefined) {
          if (isFieldErrorDataArray(details)) {
            errorDetails = details;
          } else {
            // Malformed body: degrade gracefully (treat details as absent)
            // rather than passing through a shape that could crash a
            // FieldError/ErrorBanner render downstream.
            console.error(
              '[api] malformed error.details in response body',
              details,
            );
          }
        }
      }
      throw new ApiRequestError(errorCode, errorMessage, response.status, errorDetails);
    }

    if (response.status === 204) {
      return undefined as T;
    }

    return response.json() as Promise<T>;
  }

  return {
    /** The configured base URL, e.g. for use in OIDC redirect construction. */
    get baseUrl(): string {
      return resolveBaseUrl();
    },

    auth: {
      login: (email: string, password: string) =>
        request<LoginResponse>('/v1/auth/login', {
          method: 'POST',
          body: JSON.stringify({ email, password }),
        }),

      register: (data: RegisterRequest) =>
        request<RegisterResponse>('/v1/auth/register', {
          method: 'POST',
          body: JSON.stringify(data),
        }),

      forgotPassword: (data: ForgotPasswordRequest) =>
        request<void>('/v1/auth/password-reset/request', {
          method: 'POST',
          body: JSON.stringify(data),
        }),

      resetPassword: (data: ResetPasswordRequest) =>
        request<void>('/v1/auth/password-reset/confirm', {
          method: 'POST',
          body: JSON.stringify(data),
        }),

      requestEmailCode: (data: EmailCodeRequest) =>
        request<void>('/v1/auth/email-code/request', {
          method: 'POST',
          body: JSON.stringify(data),
        }),

      verifyEmailCode: (data: EmailCodeVerifyRequest) =>
        request<LoginResponse>('/v1/auth/email-code/verify', {
          method: 'POST',
          body: JSON.stringify(data),
        }),
    },

    self: {
      get: (options?: Pick<RequestOptions, 'skipAuthRedirect'>) =>
        request<UserAccountSelf>('/v1/self', options),
      update: (data: UpdateProfileRequest) =>
        request<UserAccountSelf>('/v1/self', {
          method: 'PUT',
          body: JSON.stringify(data),
        }),
    },

    userAccounts: {
      list: (query?: string) => {
        const qs = query ? `?q=${encodeURIComponent(query)}` : '';
        return request<UserAccountListResponse>(`/v1/user-accounts${qs}`);
      },
      get: (uuid: string) => request<UserAccount>(`/v1/user-accounts/${uuid}`),
      update: (uuid: string, data: UpdateProfileRequest) =>
        request<UserAccount>(`/v1/user-accounts/${uuid}`, {
          method: 'PUT',
          body: JSON.stringify(data),
        }),
      grantAdmin: (uuid: string) =>
        request<UserAccount>(`/v1/user-accounts/${uuid}/admin`, { method: 'POST' }),
      revokeAdmin: (uuid: string) =>
        request<UserAccount>(`/v1/user-accounts/${uuid}/admin`, { method: 'DELETE' }),
      assume: (uuid: string) =>
        request<LoginResponse>(`/v1/user-accounts/${uuid}/assume`, { method: 'POST' }),
      audit: (uuid: string) =>
        request<AuditListResponse>(`/v1/user-accounts/${uuid}/audit`),
    },

    audit: {
      list: () => request<AuditListResponse>('/v1/audit'),
      byEntity: (entityUuid: string) =>
        request<AuditListResponse>(`/v1/audit?entity_uuid=${encodeURIComponent(entityUuid)}`),
    },

    apps: {
      list: () => request<AppListResponse>('/v1/apps'),
      get: (uuid: string) => request<App>(`/v1/apps/${uuid}`),
      create: (data: CreateAppRequest) =>
        request<App>('/v1/apps', {
          method: 'POST',
          body: JSON.stringify(data),
        }),
      update: (uuid: string, data: Partial<CreateAppRequest>) =>
        request<App>(`/v1/apps/${uuid}`, {
          method: 'PUT',
          body: JSON.stringify(data),
        }),
      delete: (uuid: string) =>
        request<void>(`/v1/apps/${uuid}`, { method: 'DELETE' }),
      getMembers: (uuid: string) =>
        request<AppMembersResponse>(`/v1/apps/${uuid}/user-accounts`),
      addMember: (uuid: string, data: AddAppMemberRequest) =>
        request<void>(`/v1/apps/${uuid}/user-accounts`, {
          method: 'POST',
          body: JSON.stringify(data),
        }),
      removeMember: (uuid: string, userUuid: string) =>
        request<void>(`/v1/apps/${uuid}/user-accounts/${userUuid}`, {
          method: 'DELETE',
        }),
    },
  };
}

export type UsersClient = ReturnType<typeof createUsersClient>;

// ─── Default singleton (convenience for the embedded AuthProvider) ────────────
// The library's AuthProvider calls api.self.get() / api.auth.login() etc.
// internally. It reads the base URL from the environment at module load time
// so consumers do NOT need to wire in the client themselves — the env var is
// enough for 95% of use cases. Consumers that need SSR-safe base URL injection
// can instead use `createUsersClient` directly.

/**
 * Computed once at module load.
 * @deprecated Use `getApiBaseUrl()` (evaluated lazily) and `configureUsersApi()`.
 */
export const API_BASE_URL: string = getApiBaseUrl();

/**
 * Module-level singleton used by `AuthProvider` and the OIDC config helpers.
 * Configured from `NEXT_PUBLIC_API_BASE_URL` (or the `window.__USERS_API_URL__`
 * escape hatch for non-Next.js consumers).
 */
export const api = createUsersClient({ baseUrl: getApiBaseUrl });

/**
 * Fetches the list of configured OIDC providers for the login page.
 *
 * Intentionally does NOT send an Authorization header (endpoint is public) and
 * never throws: on any network or HTTP failure the login page must still
 * render so local auth keeps working. Failures are logged to the console.
 */
export async function fetchProviders(): Promise<OIDCProvider[]> {
  try {
    const response = await fetch(`${getApiBaseUrl()}/v1/auth/providers`, {
      headers: { 'Content-Type': 'application/json' },
    });
    if (!response.ok) {
      console.error(
        `[api] fetchProviders failed with status ${response.status}`,
      );
      return [];
    }
    const body = (await response.json()) as unknown;
    if (!Array.isArray(body)) {
      console.error('[api] fetchProviders: unexpected response shape', body);
      return [];
    }
    return body as OIDCProvider[];
  } catch (err) {
    console.error('[api] fetchProviders network error', err);
    return [];
  }
}
