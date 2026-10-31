import { ApiRequestError, type ApiErrorResponse } from './api';
import { getApiBaseUrl } from './config';

/**
 * OIDC onboarding API helpers. `status` and `saved` are public
 * (`/v1/oidc-config/*` is exempt from `RequireOIDCConfirmed`), so those two
 * send no credential. `confirm` authorizes with one of two credentials:
 *   - an operator-admin Bearer token (session mode), or
 *   - the setup token in the `X-Setup-Token` header (token mode). The server
 *     accepts the token only while OIDC is unconfirmed; once confirmed, an
 *     operator-admin session is required even while the bootstrap is pending.
 *
 * Every request sends `credentials: 'omit'`: the credential is always an
 * explicit header, never an ambient cookie. (The `oidc_state` cookie of the
 * provider redirect is set and read by the server during the browser
 * navigation and needs no client handling.)
 */

export type OIDCState =
  | 'confirmed_ok'
  | 'confirmed_opt_out'
  | 'init_failed'
  | 'no_env_no_flag';

export interface OIDCProviderStatus {
  id: string;
  display_name: string;
  configured: boolean;
  enabled: boolean;
  init_ok: boolean;
  error: string | null;
}

export interface OIDCStatus {
  state: OIDCState;
  confirmed: boolean;
  providers: OIDCProviderStatus[];
  no_oidc_accounts_env: boolean;
  needs_setup_token: boolean;
  /** Optional: the API returns this as an extra field; not always present. */
  saved_at?: string | null;
}

export interface OIDCConfirmRequest {
  enabled_providers: string[];
  opt_out: boolean;
}

/**
 * Args for {@link postOIDCConfirm}. Exactly one authorization path must be
 * provided:
 *   - `setup_token`, sent as `X-Setup-Token` (onboarding while OIDC is
 *     unconfirmed).
 *   - `adminBearer`, sent as `Authorization: Bearer` (an operator admin
 *     session; required once OIDC is confirmed).
 */
export type OIDCConfirmArgs =
  | {
      enabled_providers: string[];
      opt_out: boolean;
      setup_token: string;
      adminBearer?: never;
    }
  | {
      enabled_providers: string[];
      opt_out: boolean;
      adminBearer: string;
      setup_token?: never;
    };

export interface OIDCSavedConfig {
  enabled_providers: Record<string, boolean> | null;
  opt_out: boolean;
  saved_at: string | null;
}

async function parseError(response: Response): Promise<ApiRequestError> {
  let code = 'unknown_error';
  let message = `Request failed with status ${response.status}`;
  let details: ApiErrorResponse['error']['details'];
  try {
    const body = (await response.json()) as ApiErrorResponse;
    if (body.error) {
      code = body.error.code;
      message = body.error.message;
      // Per-field details (for example users.party_unknown on party_uuid)
      // let the modals show the specific reason instead of a generic line.
      details = body.error.details;
    }
  } catch {
    // ignore JSON parse errors — keep defaults
  }
  return new ApiRequestError(code, message, response.status, details);
}

/**
 * Fetches the current OIDC onboarding status. No auth header sent.
 *
 * Network failures are surfaced as an ApiRequestError with status=0 so the
 * caller can treat unreachable-API the same as unconfirmed (the plan calls
 * for routing to `/oidc-config` in that case).
 */
export async function fetchOIDCStatus(): Promise<OIDCStatus> {
  let response: Response;
  try {
    response = await fetch(`${getApiBaseUrl()}/v1/oidc-config/status`, {
      headers: { 'Content-Type': 'application/json' },
      credentials: 'omit',
    });
  } catch (err) {
    console.error('[oidc-config] fetchOIDCStatus network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }

  if (!response.ok) {
    throw await parseError(response);
  }

  return response.json() as Promise<OIDCStatus>;
}

/**
 * Submits the onboarding confirmation. Accepts EITHER a `setup_token`
 * (sent as `X-Setup-Token`) OR an `adminBearer` string (sent as
 * Authorization: Bearer). On 200 the API
 * returns the refreshed status (same shape as GET /status). Errors (401
 * invalid token, 400 bad body, 500 rebuild failure) surface as
 * ApiRequestError so the page can render `err.message` inline.
 */
export async function postOIDCConfirm(
  args: OIDCConfirmArgs,
): Promise<OIDCStatus> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };
  // The body never carries a credential: the server evaluates the Bearer and
  // the X-Setup-Token header before it reads the body, so a wrong token is a
  // 401 even when the body is malformed.
  const body: OIDCConfirmRequest = {
    enabled_providers: args.enabled_providers,
    opt_out: args.opt_out,
  };
  if ('adminBearer' in args && args.adminBearer) {
    // Session mode: implies the OIDC may already be confirmed, where the
    // setup token would be refused.
    headers.Authorization = `Bearer ${args.adminBearer}`;
  } else if ('setup_token' in args && args.setup_token) {
    // Token mode: only valid while OIDC is unconfirmed.
    headers['X-Setup-Token'] = args.setup_token;
  } else {
    throw new ApiRequestError(
      'bad_request',
      'postOIDCConfirm requires either adminBearer or setup_token.',
      0,
    );
  }

  let response: Response;
  try {
    response = await fetch(`${getApiBaseUrl()}/v1/oidc-config/confirm`, {
      method: 'POST',
      headers,
      body: JSON.stringify(body),
      credentials: 'omit',
    });
  } catch (err) {
    console.error('[oidc-config] postOIDCConfirm network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }

  if (!response.ok) {
    throw await parseError(response);
  }

  return response.json() as Promise<OIDCStatus>;
}

/**
 * Fetches the last-saved config to power the "Revert" button. Returns empty
 * / null fields when nothing has ever been saved.
 */
export async function fetchOIDCSaved(): Promise<OIDCSavedConfig> {
  let response: Response;
  try {
    response = await fetch(`${getApiBaseUrl()}/v1/oidc-config/saved`, {
      headers: { 'Content-Type': 'application/json' },
      credentials: 'omit',
    });
  } catch (err) {
    console.error('[oidc-config] fetchOIDCSaved network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }

  if (!response.ok) {
    throw await parseError(response);
  }

  return response.json() as Promise<OIDCSavedConfig>;
}
