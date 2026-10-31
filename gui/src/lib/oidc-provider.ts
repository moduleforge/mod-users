import { ApiRequestError, type ApiErrorResponse } from './api';
import { getApiBaseUrl } from './config';

/**
 * Per-provider OIDC override CRUD helpers (phase 9.11b). These wrap the
 * /v1/oidc-config/providers/* endpoints added in 9.11a. Kept separate from
 * `oidc-config.ts` (which hosts the status/confirm/saved helpers) so the
 * onboarding flow and the per-provider edit flow evolve independently.
 *
 * Auth: every method accepts `auth` — either an operator-admin Bearer token
 * (session mode) or a setup token (token mode, accepted by the server only
 * while OIDC is unconfirmed). The setup token travels only in the
 * `X-Setup-Token` header, on every method; it is never echoed into a body.
 * Mirrors the dual-auth pattern used by {@link postOIDCConfirm}.
 *
 * Every request sends `credentials: 'omit'`: the credential is always an
 * explicit header, never an ambient cookie.
 */

export interface OIDCProviderView {
  id: string;
  display_name: string | null;
  display_name_default: string | null;
  display_name_source: OIDCFieldSource;
  issuer_url: string | null;
  issuer_url_default: string | null;
  issuer_url_source: OIDCFieldSource;
  client_id: string | null;
  client_id_default: string | null;
  client_id_source: OIDCFieldSource;
  has_client_secret: boolean;
  client_secret_source: OIDCFieldSource;
  claim_style: string | null;
  claim_style_default: string | null;
  claim_style_source: OIDCFieldSource;
  scopes: string[] | null;
  scopes_default: string[];
  scopes_source: OIDCFieldSource;
  enabled: boolean;
  /** The party this provider is bound to, or null for a deployment-wide provider. */
  party_uuid: string | null;
  /** The effective account-creation policy for an unknown identity. */
  jit_mode: OIDCJitMode;
  /** Whether the IdP's asserted `acr` counts toward the session's assurance. */
  trust_acr: boolean;
  init_ok: boolean;
  error?: string;
  callback_url: string;
  well_known: boolean;
}

/**
 * What a first sign-in by an unknown identity creates:
 *   - `byo`: a personal account the person owns (the default for a new row).
 *   - `managed`: an account managed by the provider's party; requires a party.
 *   - `none`: nothing; unknown identities are refused.
 */
export type OIDCJitMode = 'byo' | 'managed' | 'none';

export const OIDC_JIT_MODES: readonly OIDCJitMode[] = ['byo', 'managed', 'none'];

/** Short operator-facing explanation of each `jit_mode`. */
export const OIDC_JIT_MODE_HELP: Record<OIDCJitMode, string> = {
  byo: 'byo: an unknown identity gets a personal account of its own.',
  managed:
    'managed: an unknown identity gets an account managed by the party below; a party is required.',
  none: 'none: unknown identities are refused; only existing accounts can sign in.',
};

/**
 * Client-side mirror of the server rule "jit_mode managed requires a party".
 * Returns the message to show, or null when the policy is acceptable. The
 * server stays the authority (it answers `400 users.jit_managed_requires_party`
 * and the modals surface that message too); this only saves a round trip.
 */
export function validateJitPolicy(
  jitMode: OIDCJitMode,
  partyUuid: string,
): string | null {
  // Only `managed` depends on a party: it creates accounts on the party's
  // behalf. byo and none are valid unbound, so they pass without a party.
  if (jitMode === 'managed' && partyUuid.trim() === '') {
    return 'jit_mode managed requires a party UUID.';
  }
  return null;
}

/**
 * Which configuration layer provides the currently-effective value
 * for a given field. Returned per-field by the API so the modal can
 * show the operator exactly where a value came from.
 */
export type OIDCFieldSource = 'db' | 'env' | 'well_known' | 'fallback' | 'none';

/**
 * Fields that can be sent on a PUT or POST. Use these sentinels:
 *   - property absent  → keep existing DB value (for PUT) / don't set (POST).
 *   - property `null`  → clear the override (API falls back to env/default).
 *   - non-empty string → store as override.
 *
 * `client_secret` has the same semantics but must be omitted when you want
 * to preserve the existing secret — sending `null` or "" both clear it.
 */
export interface OIDCProviderWriteBody {
  display_name?: string | null;
  issuer_url?: string | null;
  client_id?: string | null;
  client_secret?: string | null;
  claim_style?: string | null;
  scopes?: string[] | null;
  enabled?: boolean;
  /**
   * Party binding. Absent keeps the stored value; `null` unbinds; a uuid binds
   * (the server answers `400 users.party_unknown` for an unknown party).
   */
  party_uuid?: string | null;
  /** Absent keeps the stored value; `null` resets to `byo`. */
  jit_mode?: OIDCJitMode | null;
  /** Absent keeps the stored value. */
  trust_acr?: boolean;
}

export type OIDCProviderAuth =
  | { adminBearer: string; setupToken?: never }
  | { adminBearer?: never; setupToken: string };

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

function authHeaders(auth: OIDCProviderAuth): Record<string, string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if ('adminBearer' in auth && auth.adminBearer) {
    headers.Authorization = `Bearer ${auth.adminBearer}`;
  } else if ('setupToken' in auth && auth.setupToken) {
    // Header only: it covers GET/DELETE (no body) and PUT/POST alike, and
    // keeps the secret out of any body a proxy or log might capture.
    headers['X-Setup-Token'] = auth.setupToken;
  }
  return headers;
}

/**
 * GET /v1/oidc-config/providers/:id. Accepts either admin bearer or a
 * setup token (sent as X-Setup-Token header since GET has no body).
 */
export async function fetchOIDCProvider(
  id: string,
  auth: OIDCProviderAuth,
): Promise<OIDCProviderView> {
  let response: Response;
  try {
    response = await fetch(
      `${getApiBaseUrl()}/v1/oidc-config/providers/${encodeURIComponent(id)}`,
      {
        method: 'GET',
        headers: authHeaders(auth),
        credentials: 'omit',
      },
    );
  } catch (err) {
    console.error('[oidc-provider] fetchOIDCProvider network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }
  if (!response.ok) {
    throw await parseError(response);
  }
  return response.json() as Promise<OIDCProviderView>;
}

/**
 * PUT /v1/oidc-config/providers/:id. Accepts admin bearer or setup token.
 */
export async function updateOIDCProvider(
  id: string,
  body: OIDCProviderWriteBody,
  auth: OIDCProviderAuth,
): Promise<OIDCProviderView> {
  let response: Response;
  try {
    response = await fetch(
      `${getApiBaseUrl()}/v1/oidc-config/providers/${encodeURIComponent(id)}`,
      {
        method: 'PUT',
        headers: authHeaders(auth),
        body: JSON.stringify(body),
        credentials: 'omit',
      },
    );
  } catch (err) {
    console.error('[oidc-provider] updateOIDCProvider network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }
  if (!response.ok) {
    throw await parseError(response);
  }
  return response.json() as Promise<OIDCProviderView>;
}

/**
 * POST /v1/oidc-config/providers. Body shape is the write-body plus `id`.
 * 201 on success, 400 on bad slug, 409 when the id already exists.
 */
export async function createOIDCProvider(
  id: string,
  body: OIDCProviderWriteBody,
  auth: OIDCProviderAuth,
): Promise<OIDCProviderView> {
  let response: Response;
  try {
    response = await fetch(`${getApiBaseUrl()}/v1/oidc-config/providers`, {
      method: 'POST',
      headers: authHeaders(auth),
      body: JSON.stringify({ ...body, id }),
      credentials: 'omit',
    });
  } catch (err) {
    console.error('[oidc-provider] createOIDCProvider network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }
  if (!response.ok) {
    throw await parseError(response);
  }
  return response.json() as Promise<OIDCProviderView>;
}

/**
 * DELETE /v1/oidc-config/providers/:id (Revert). Accepts admin bearer
 * or a setup token (via X-Setup-Token header). Returns nothing
 * (204 on success).
 */
export async function revertOIDCProvider(
  id: string,
  auth: OIDCProviderAuth,
): Promise<void> {
  let response: Response;
  try {
    response = await fetch(
      `${getApiBaseUrl()}/v1/oidc-config/providers/${encodeURIComponent(id)}`,
      {
        method: 'DELETE',
        headers: authHeaders(auth),
        credentials: 'omit',
      },
    );
  } catch (err) {
    console.error('[oidc-provider] revertOIDCProvider network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }
  if (!response.ok) {
    throw await parseError(response);
  }
}

/**
 * Slug validation mirrors the API regex from
 * `api/internal/handlers/oidc_providers.go` (providerIDPattern).
 */
export const PROVIDER_ID_PATTERN = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;

/**
 * Well-known hints for the Add modal — lightweight client-side preview of
 * the defaults the server will apply when an override field is blank.
 * The server remains the source of truth; these are UI hints only.
 */
export interface WellKnownHint {
  display_name: string;
  issuer_url: string;
  claim_style: string;
  scopes: string[];
}

export const WELL_KNOWN_HINTS: Record<string, WellKnownHint> = {
  google: {
    display_name: 'Google',
    issuer_url: 'https://accounts.google.com',
    claim_style: 'google',
    scopes: ['openid', 'email', 'profile'],
  },
  microsoft: {
    display_name: 'Microsoft',
    issuer_url: 'https://login.microsoftonline.com/common/v2.0',
    claim_style: 'microsoft',
    scopes: ['openid', 'email', 'profile'],
  },
  authelia: {
    display_name: 'Authelia',
    issuer_url: '',
    claim_style: 'authelia',
    scopes: ['openid', 'email', 'profile'],
  },
};

/**
 * URL the "Test configuration" button navigates to. The /start endpoint
 * handler reads `?mode=test` and carries the marker through the state
 * cookie to the callback, which redirects back to /oidc-config with a
 * `test_result=…` query string instead of minting a session.
 */
export function testProviderURL(id: string): string {
  return `${getApiBaseUrl()}/v1/auth/oidc/${encodeURIComponent(id)}/start?mode=test`;
}
