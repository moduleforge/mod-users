// ─── Types ───────────────────────────────────────────────────────────────────
//
// The wire/client error types below are the canonical shapes from
// `@moduleforge/core-gui` (docs/mf-standards/architecture/api-response-design.md
// "GUI-facing error-data contract"), imported and re-exported here rather than
// redefined locally so this module stays the single source of truth consumers
// import from (`@moduleforge/users-gui`) without diverging from the shared
// contract. The local request pipeline below carries the users-specific
// frontend contract (per-tab Bearer tokens, refresh lineage, step-up retry,
// unauthenticated handling); only the types are reconciled with core-gui.

import type {
  ApiError,
  ApiErrorResponse,
  FieldErrorData,
} from '@moduleforge/core-gui';
import { ApiRequestError } from '@moduleforge/core-gui';
import {
  clearStoredPendingSelection,
  clearStoredProfileUuid,
  clearStoredSession,
  clearStoredToken,
  getApiBaseUrl,
  getStoredPendingSelection,
  getStoredImpersonating,
  getStoredProfileUuid,
  getStoredRefreshToken,
  getStoredToken,
  handleUnauthenticated,
  isSafeReturnPath,
  setStoredImpersonating,
  setStoredPendingSelection,
  setStoredProfileUuid,
  setStoredRefreshToken,
  setStoredToken,
} from './config';
import { beginHandoff } from './handoff';

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
   * `ApiRequestError` (carrying the response's own error code) without
   * refreshing the token, clearing the tab's tokens, or running the
   * unauthenticated handler. Use this when the caller needs to handle
   * authentication failures itself: sign-in routes (a 401 there means bad
   * credentials, not an expired session), the OIDC return page, and the
   * step-up and refresh calls themselves.
   *
   * Defaults to false: a 401 first tries one token refresh, and when that
   * cannot repair the session the tab's tokens are cleared and the configured
   * unauthenticated handler runs (see `configureUsersApi`).
   */
  skipAuthRedirect?: boolean;
  /**
   * When true, a `409 users.step_up_required` response is thrown as an
   * `ApiActionRequiredError` without consulting the registered step-up
   * handler. Set by the step-up routes themselves, where that 409 means "wrong
   * password or code" and prompting again would recurse.
   */
  skipStepUp?: boolean;
  /**
   * Bearer token to send instead of this tab's stored access token. Implies no
   * refresh and no sign-out on 401, because the token is not this tab's own
   * (used by the fork call, which carries a sibling tab's access token).
   */
  accessToken?: string;
  /**
   * The profile uuid a `401 users.profile_unavailable` on this call refers to
   * (default: this tab's active profile). Lets a profile selection or switch
   * attribute the lock to the profile the caller asked for.
   */
  profileUuid?: string;
  /**
   * Internal: send this one request with `credentials: 'include'` so the
   * browser stores the `Set-Cookie` of the response. Every other request is
   * `credentials: 'omit'`. Only the OIDC authorize-URL start calls set it.
   */
  allowStateCookie?: boolean;
}

// ─── Token response, session and profile types ───────────────────────────────

/** The session as carried in a token response or `GET /v1/session`. */
export interface SessionView {
  uuid: string;
  acr: number;
  amr: string[];
  /** RFC 3339 time the caller last authenticated; step-up refreshes it. */
  auth_time: string;
  impersonating: boolean;
}

export interface PartyView {
  uuid: string;
  /** The party's fundamental type slug (for example `organization`). */
  kind: string;
  display_name: string;
}

/**
 * What would lift a profile lock. Kept open (`string & {}`) so a server-side
 * addition cannot break typing; components switch on the known kinds and fall
 * back to the lock reason text for any other.
 */
export type LockRemedyKind =
  | 'sign_in_with_provider'
  | 'step_up'
  | 'sign_in_with_authorized_credential'
  | 'accept_invitation'
  | (string & {});

export interface LockRemedy {
  kind: LockRemedyKind;
  /** Present for `sign_in_with_provider`: the provider to sign in with. */
  provider_id?: string;
}

/** Why a profile is not available to the session, and what would fix it. */
export interface ProfileLock {
  /**
   * A `profile_availability` reason (for example `profile_suspended`,
   * `acr_too_low`, `credential_not_authorized`) or `app_login_not_granted`.
   */
  reason: string;
  remedy: LockRemedy | null;
}

export interface ProfileView {
  uuid: string;
  kind: 'personal' | 'party';
  /** The party of a party profile; null for a personal profile. */
  party: PartyView | null;
  status: string;
  display_name: string;
  /** Whether this session (or credential) may act as this profile. */
  available: boolean;
  /** Why the profile is not available; null when it is. */
  lock: ProfileLock | null;
}

/**
 * The token response of every sign-in, refresh, handoff, switch, fork,
 * step-up and impersonation call. `access_token` is null when the landing
 * chain found no profile (`selection_required` is then true and the client
 * calls the refresh grant with a chosen profile via `selectProfile`);
 * `refresh_token` is null when none was minted (switch, step-up,
 * impersonation).
 */
export interface TokenResponse {
  access_token: string | null;
  token_type: 'Bearer';
  /** Access-token lifetime in seconds. */
  expires_in: number;
  refresh_token: string | null;
  session: SessionView | null;
  profile: ProfileView | null;
  selection_required: boolean;
  profiles: ProfileView[];
}

/** The guest sign-in response: a token response plus the one-time device token. */
export interface AnonymousTokenResponse extends TokenResponse {
  /** Returned once, only when a guest is created; signs the guest back in later. */
  device_token?: string;
}

/** `GET /v1/session`. */
export interface SessionInfo {
  session: SessionView | null;
  profile: ProfileView | null;
  profiles: ProfileView[];
}

// ─── Auth request types ──────────────────────────────────────────────────────

export interface OIDCProvider {
  id: string;
  display_name: string;
}

export interface RegisterRequest {
  email: string;
  password: string;
  given_name: string;
  family_name: string;
  /** The app (uuid) the new session signs in to. */
  app?: string;
}

export interface LoginOptions {
  /** The app (uuid) the session signs in to. */
  app?: string;
  /** The profile to land on. */
  profile_uuid?: string;
}

export interface AnonymousRequest {
  /** Stable device fingerprint supplied by the client. */
  device_id: string;
  /** A previous call's device token: signs that guest back in. */
  device_token?: string;
  app?: string;
}

/** The only email-code purpose the API accepts is a sign-in. */
export type EmailCodePurpose = 'login';

export interface EmailCodeRequest {
  email: string;
  purpose?: EmailCodePurpose;
}

export interface EmailCodeVerifyRequest {
  email: string;
  code: string;
  purpose?: EmailCodePurpose;
  app?: string;
  profile_uuid?: string;
}

export interface ForgotPasswordRequest {
  email: string;
}

export interface ResetPasswordRequest {
  token: string;
  password: string;
}

/** The `refresh_token` grant of `POST /v1/auth/token`. */
export interface RefreshTokenRequest {
  grant_type: 'refresh_token';
  refresh_token: string;
  profile_uuid: string;
}

export interface AuthorizeURL {
  authorize_url: string;
}

export interface StartImpersonationRequest {
  profile_uuid: string;
  reason: string;
}

// ─── Me types ────────────────────────────────────────────────────────────────

export interface UserView {
  uuid: string;
  kind: string;
  is_guest: boolean;
  /** True for a managed user (owned by a party). */
  managed: boolean;
  given_name: string | null;
  family_name: string | null;
  display_name: string;
  /** The primary email identifier. */
  email: string | null;
  /** True when the user holds at least one verified email. */
  email_verified: boolean;
  default_profile_uuid: string | null;
  default_app_uuid: string | null;
  created_at: string;
  updated_at: string;
}

/** Absent fields are left unchanged; the two default fields accept null to clear. */
export interface UpdateMeRequest {
  given_name?: string;
  family_name?: string;
  default_profile_uuid?: string | null;
  default_app_uuid?: string | null;
}

export interface ProfileList {
  profiles: ProfileView[];
}

export interface IdentifierView {
  uuid: string;
  kind: 'email';
  value: string;
  is_primary: boolean;
  verified: boolean;
  verified_at: string | null;
  created_at: string;
}

export interface IdentifierList {
  identifiers: IdentifierView[];
}

export interface AddIdentifierRequest {
  kind?: 'email';
  value: string;
}

/**
 * An active credential: identifying and display attributes only, never a
 * secret. Kind-specific members are present only for their kind.
 */
export interface CredentialView {
  uuid: string;
  /** Credential kind (for example `password`, `oidc`, `ssh_key`, `email_otp`, `api_key`, `anon`). */
  kind: string;
  label: string;
  created_at: string;
  last_used_at: string | null;
  expires_at: string | null;
  default_profile_uuid: string | null;
  confined_to_party_uuid: string | null;
  /** The identifier a password or email-code credential is bound to. */
  identifier_uuid?: string;
  /** Present for `oidc`. */
  provider_id?: string;
  /** Present for `oidc`: the email the provider asserted when linked. */
  email_at_link?: string;
  /** Present for `ssh_key`. */
  key_type?: string;
  fingerprint?: string;
  /** Present for `ssh_key`: canonical `authorized_keys` form. */
  public_key?: string;
  /** Present for `api_key`. */
  client_id?: string;
}

export interface CredentialList {
  credentials: CredentialView[];
}

export interface AddPasswordRequest {
  password: string;
  label?: string;
  /** One of the caller's identifiers; the primary email when omitted. */
  identifier_uuid?: string;
}

export interface AddSSHKeyRequest {
  /** A single `authorized_keys`-format line. */
  public_key: string;
  label?: string | null;
}

/** At least one member is required; `default_profile_uuid` accepts null to clear. */
export interface PatchCredentialRequest {
  label?: string;
  default_profile_uuid?: string | null;
}

export interface SelfSession {
  uuid: string;
  acr: number;
  amr: string[];
  auth_time: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  impersonating: boolean;
  /** True for the session that made the request. */
  current: boolean;
}

export interface SessionList {
  sessions: SelfSession[];
}

export interface UpgradeRequest {
  email: string;
  password: string;
  given_name: string;
  family_name: string;
}

// ─── Bootstrap types ─────────────────────────────────────────────────────────

export interface BootstrapStatus {
  /** The deployment has an operator party. */
  bootstrapped: boolean;
  /** The OIDC configuration is confirmed (including an opt-out). */
  oidc_confirmed: boolean;
}

export type BootstrapOperator =
  | {
      kind: 'organization';
      legal_name: string;
      legal_form:
        | 'corporation'
        | 'llc'
        | 'partnership'
        | 'nonprofit'
        | 'public_body'
        | 'trust';
      jurisdiction?: string;
    }
  | { kind: 'natural_person' };

export interface BootstrapRequest {
  operator: BootstrapOperator;
  admin: {
    email: string;
    password: string;
    given_name: string;
    family_name: string;
  };
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

// ─── Error and action codes ──────────────────────────────────────────────────

/**
 * Error codes the client branches on. Other codes pass through on
 * `ApiRequestError.code` untouched.
 */
export const USERS_ERROR_CODES = {
  invalidCredentials: 'users.invalid_credentials',
  sessionRevoked: 'users.session_revoked',
  profileUnavailable: 'users.profile_unavailable',
  refreshReuse: 'users.refresh_reuse',
  /** Client-synthesized (status 401): a refresh found a lineage with a profile selection still pending. */
  selectionRequired: 'selection_required',
  /** `409` from `POST /v1/bootstrap`: the deployment already has an operator party. */
  alreadyBootstrapped: 'users.already_bootstrapped',
  /** `401` from `POST /v1/bootstrap`: the `X-Setup-Token` is missing, wrong or no longer active. */
  setupTokenInvalid: 'users.setup_token_invalid',
  /**
   * Client-synthesized (status 401): a request sent with an impersonation
   * token was rejected, so the client ended the impersonation locally instead
   * of refreshing (which would hand back the administrator's token) and
   * replaying the request as the administrator.
   */
  impersonationEnded: 'users.impersonation_ended',
  /** Client-synthesized (status 0): an OIDC handoff was attempted without this tab's verifier. */
  handoffVerifierMissing: 'handoff_verifier_missing',
} as const;

/** The action-required code that starts the step-up flow. */
export const STEP_UP_REQUIRED_ACTION = 'users.step_up_required';

/** 401 codes that mean the session is dead for good: the tab signs out. */
const SIGN_OUT_CODES: ReadonlySet<string> = new Set([
  USERS_ERROR_CODES.sessionRevoked,
  USERS_ERROR_CODES.refreshReuse,
]);

// ─── Step-up handler ─────────────────────────────────────────────────────────

/** What the registered step-up handler is told when a request needs a step-up. */
export interface StepUpRequest {
  /** The `409 users.step_up_required` action that interrupted the request. */
  error: ApiActionRequiredError;
}

/**
 * Prompts the user for a step-up (password, emailed code or OIDC) and resolves
 * with the FRESH access token the step-up call returned. The client then
 * retries the interrupted request once with it. Reject (for example when the
 * user cancels) to abandon the retry: the original `ApiActionRequiredError`
 * propagates to the caller.
 *
 * The step-up calls (`client.session.stepUp.*`) already store the fresh token
 * for the tab, so the handler only has to pass `response.access_token` along.
 * One handler call serves every request that hits the 409 concurrently.
 */
export interface StepUpHandler {
  (request: StepUpRequest): Promise<string>;
}

let stepUpHandler: StepUpHandler | null = null;
let stepUpInFlight: Promise<string> | null = null;

/**
 * Registers (or with `null` clears) the step-up handler. There is one slot per
 * page; the auth context's `registerStepUpHandler` / `useStepUpHandler` wrap
 * this. A later registration replaces an earlier one. Returns an unregister
 * function that clears the slot only if it still holds this handler, so a
 * stale cleanup cannot remove a newer registration.
 */
export function setStepUpHandler(handler: StepUpHandler | null): () => void {
  stepUpHandler = handler;
  return () => {
    // Skip when another registration replaced this one in the meantime.
    if (stepUpHandler === handler) stepUpHandler = null;
  };
}

/** Runs the handler once for all concurrent callers. */
function runStepUp(error: ApiActionRequiredError): Promise<string> {
  // Callers arriving while a prompt is open share it: asking the user to step
  // up N times for N parallel requests would be both noisy and unnecessary,
  // since one fresh token serves them all.
  if (stepUpInFlight) return stepUpInFlight;
  const handler = stepUpHandler;
  if (!handler) return Promise.reject(error);
  const pending = handler({ error }).finally(() => {
    // Always release the slot, resolved or rejected, so the next 409 can prompt.
    stepUpInFlight = null;
  });
  stepUpInFlight = pending;
  return pending;
}

// ─── Tab session events ──────────────────────────────────────────────────────

/** Why the tab's session ended without the user asking. */
export type SignOutReason =
  | 'session_revoked'
  | 'refresh_reuse'
  | 'unauthenticated';

/**
 * Tab-session changes the auth context mirrors into React state. The request
 * layer cannot touch React, so it publishes here.
 */
export type SessionEvent =
  /** A token response was committed to this tab's storage. */
  | { type: 'token_response'; response: TokenResponse }
  /** The tab's tokens were cleared because the session ended. */
  | { type: 'signed_out'; reason: SignOutReason }
  /**
   * The tab's impersonation token was dropped locally after a 401. The tab
   * keeps the administrator's refresh lineage but holds no access token: a
   * subscriber restores the administrator session (the next authenticated
   * call does, through the refresh grant).
   */
  | { type: 'impersonation_ended' }
  /** A call answered `401 users.profile_unavailable`. */
  | {
      type: 'profile_unavailable';
      /** The profile the lock refers to, when known. */
      profileUuid: string | null;
      /** The lock reason (`error.details[0].code`), or `''` when absent. */
      reason: string;
    };

type SessionListener = (event: SessionEvent) => void;
const sessionListeners = new Set<SessionListener>();

/** Subscribes to tab-session events; returns the unsubscribe function. */
export function subscribeSessionEvents(listener: SessionListener): () => void {
  sessionListeners.add(listener);
  return () => {
    sessionListeners.delete(listener);
  };
}

function emitSessionEvent(event: SessionEvent): void {
  // Iterate a copy: a listener may unsubscribe (or subscribe) while handling.
  for (const listener of [...sessionListeners]) {
    try {
      listener(event);
    } catch (err) {
      // One broken subscriber must not stop the others or fail the request
      // that produced the event.
      console.error('[api] session event listener threw', err);
    }
  }
}

/**
 * Writes a token response into this tab's storage and publishes it. Every
 * client method that returns a `TokenResponse` goes through here, so storage
 * and the auth context can never disagree about the tab's session.
 */
export function commitTokenResponse(response: TokenResponse): void {
  // A non-null refresh token is a new lineage (sign-in, fork) or a rotation;
  // either way the old one is spent. A null means "none minted" (switch,
  // step-up, impersonation) and the tab keeps the lineage it has.
  if (response.refresh_token) {
    setStoredRefreshToken(response.refresh_token);
  }
  if (response.access_token) {
    setStoredToken(response.access_token);
    // A landed profile ends any pending selection; keeping the stored list
    // would make a later reload show a selection screen for a chosen session.
    clearStoredPendingSelection();
  } else {
    // `selection_required`: no acting profile, so no usable access token. Keep
    // no stale one around, or requests would silently act as an old profile.
    clearStoredToken();
    if (response.selection_required) {
      // Persist the list per tab so a reload restores the selection screen
      // instead of calling session.get (which needs a profile) and signing out.
      // This IMPLIES the list is only as fresh as this response.
      setStoredPendingSelection(JSON.stringify(response.profiles));
    }
  }
  // An impersonation token acts as the target profile, but the refresh grant
  // must keep naming the administrator's own profile, because the refresh
  // token belongs to the administrator's lineage. So an impersonating
  // response never overwrites the stored profile.
  const impersonating = response.session?.impersonating === true;
  // Record what the stored access token is, so a later 401 on a request sent
  // with it can refuse to refresh-and-replay as the administrator. Without an
  // access token there is nothing to describe, so the marker is cleared too.
  setStoredImpersonating(impersonating && Boolean(response.access_token));
  if (!impersonating) {
    if (response.profile) {
      setStoredProfileUuid(response.profile.uuid);
    } else if (response.refresh_token) {
      // A fresh lineage with no landing profile: the profile from an earlier
      // session in this tab would be wrong for the new one.
      clearStoredProfileUuid();
    }
  }
  emitSessionEvent({ type: 'token_response', response });
}

/**
 * Ends the tab's session locally: clears every token, tells subscribers, then
 * runs the configured unauthenticated handler (the one place the library
 * navigates). The server session is already dead or unreachable by the time
 * this runs, so no logout call is made.
 */
function endTabSession(reason: SignOutReason): void {
  clearStoredSession();
  emitSessionEvent({ type: 'signed_out', reason });
  handleUnauthenticated();
}

// ─── Refresh lineage ─────────────────────────────────────────────────────────

let refreshInFlight: Promise<void> | null = null;

/**
 * Rotates this tab's refresh token and stores the new access token, at most
 * one rotation in flight per tab: a second rotation of the same refresh token
 * would trip the server's reuse detection and revoke the whole session.
 *
 * `sentToken` is the access token the failing request carried. If the stored
 * token differs, another request already refreshed after this one was sent,
 * so there is nothing to rotate: the caller just retries with the new token.
 */
function refreshTabToken(
  sentToken: string | null,
  resolveBaseUrl: () => string,
): Promise<void> {
  if (getStoredToken() !== sentToken) return Promise.resolve();
  // Concurrent callers share the one rotation (see above).
  if (refreshInFlight) return refreshInFlight;
  const pending = doRefresh(resolveBaseUrl).finally(() => {
    // Release in both outcomes so a later expiry can refresh again.
    refreshInFlight = null;
  });
  refreshInFlight = pending;
  return pending;
}

/**
 * The profile list stored for a pending selection, or null when none is stored
 * or the stored value is not a profile array (the store is writable by any
 * script in the page, so shape is checked rather than trusted).
 */
export function readPendingSelectionProfiles(): ProfileView[] | null {
  const raw = getStoredPendingSelection();
  if (raw === null) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (
      Array.isArray(parsed) &&
      parsed.length > 0 &&
      parsed.every(
        (p) =>
          typeof p === 'object' &&
          p !== null &&
          typeof (p as { uuid?: unknown }).uuid === 'string',
      )
    ) {
      return parsed as ProfileView[];
    }
  } catch {
    // Corrupt value: treated as no pending selection.
  }
  return null;
}

async function doRefresh(resolveBaseUrl: () => string): Promise<void> {
  const refreshToken = getStoredRefreshToken();
  const profileUuid = getStoredProfileUuid();
  if (refreshToken && !profileUuid) {
    // A lineage with no named profile is a selection still pending (a reload
    // or a stray request mid-selection), not a dead session. Re-publish the
    // stored list as selection-required so the UI routes to the picker, and
    // keep the tokens. This IMPLIES the caller's request fails but the tab
    // stays recoverable.
    const profiles = readPendingSelectionProfiles();
    if (profiles) {
      emitSessionEvent({
        type: 'token_response',
        response: {
          access_token: null,
          token_type: 'Bearer',
          expires_in: 0,
          refresh_token: null,
          session: null,
          profile: null,
          selection_required: true,
          profiles,
        },
      });
      throw new ApiRequestError(
        USERS_ERROR_CODES.selectionRequired,
        'Choose a profile to continue.',
        401,
      );
    }
  }
  if (!refreshToken || !profileUuid) {
    // No lineage to refresh (or no profile to name): the session cannot be
    // repaired, so this is a sign-out, not an error to retry.
    endTabSession('unauthenticated');
    throw new ApiRequestError(
      'unauthenticated',
      'Authentication required',
      401,
    );
  }
  const body: RefreshTokenRequest = {
    grant_type: 'refresh_token',
    refresh_token: refreshToken,
    profile_uuid: profileUuid,
  };
  let response: TokenResponse;
  try {
    response = await execute<TokenResponse>(
      resolveBaseUrl,
      '/v1/auth/token',
      {
        method: 'POST',
        body: JSON.stringify(body),
        // The refresh call must never recurse into refresh or step-up.
        skipAuthRedirect: true,
        skipStepUp: true,
        profileUuid,
      },
      { refreshed: true, steppedUp: true },
    );
  } catch (err) {
    if (err instanceof ApiRequestError && err.status === 401) {
      if (err.code === USERS_ERROR_CODES.profileUnavailable) {
        // The lock event was already published by `execute`. The refresh
        // token stays usable (the server does not spend it), so keep the
        // tokens: the user can pick another profile with `selectProfile`.
        throw err;
      }
      // A revoked session, a reused token, or a refresh token the server no
      // longer knows: the lineage is dead for good.
      endTabSession(signOutReasonFor(err.code));
    }
    // Network and server errors keep the tokens: the failure may be transient.
    throw err;
  }
  if (!response.access_token) {
    // A refresh with a named profile must yield an access token; treating the
    // answer as success would leave the retry without credentials.
    throw new ApiRequestError(
      'invalid_token_response',
      'The refresh answered without an access token.',
      0,
    );
  }
  commitTokenResponse(response);
}

function signOutReasonFor(code: string): SignOutReason {
  if (code === USERS_ERROR_CODES.refreshReuse) return 'refresh_reuse';
  if (code === USERS_ERROR_CODES.sessionRevoked) return 'session_revoked';
  return 'unauthenticated';
}

// ─── Request pipeline ────────────────────────────────────────────────────────

/** Per-call retry flags: each repair is attempted at most once per call. */
interface AttemptState {
  refreshed: boolean;
  steppedUp: boolean;
}

/** Reads a JSON body, or undefined when absent or unparseable. */
async function readBody(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    // A missing or malformed body falls through to the generic error path.
    return undefined;
  }
}

function toRequestError(
  status: number,
  body: unknown,
  fallbackCode: string,
  fallbackMessage: string,
): ApiRequestError {
  let code = fallbackCode;
  let message = fallbackMessage;
  let details: FieldErrorData[] | undefined;
  const apiError = extractApiError(body);
  if (apiError) {
    code = apiError.code;
    message = apiError.message;
    if (apiError.details !== undefined) {
      if (isFieldErrorDataArray(apiError.details)) {
        details = apiError.details;
      } else {
        // Malformed body: degrade gracefully (treat details as absent)
        // rather than passing through a shape that could crash a
        // FieldError/ErrorBanner render downstream.
        console.error(
          '[api] malformed error.details in response body',
          apiError.details,
        );
      }
    }
  }
  return new ApiRequestError(code, message, status, details);
}

async function execute<T>(
  resolveBaseUrl: () => string,
  path: string,
  options: RequestOptions,
  state: AttemptState,
): Promise<T> {
  const {
    skipAuthRedirect = false,
    skipStepUp = false,
    accessToken: explicitToken,
    profileUuid,
    allowStateCookie = false,
    ...fetchOptions
  } = options;
  // An explicit token (the fork call) stands in for the tab's own.
  const sentToken = explicitToken ?? getStoredToken();
  // Whether THIS request carries an impersonation token, captured at send time
  // (not when the 401 arrives): another call may swap the tab's token while
  // this one is in flight, and what matters is whose identity this request was
  // sent as. An explicit token is the caller's own business, never the tab's.
  const sentAsImpersonation =
    explicitToken === undefined &&
    sentToken !== null &&
    getStoredImpersonating();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(fetchOptions.headers as Record<string, string> | undefined),
  };
  if (sentToken) {
    headers['Authorization'] = `Bearer ${sentToken}`;
  }

  let response: Response;
  try {
    // `credentials` is applied after the caller's init so nothing but the
    // explicit `allowStateCookie` flag can switch ambient cookies back on:
    // the token travels only as `Authorization: Bearer`. The flag exists for
    // the two OIDC authorize-URL start calls (see `startRedirectFlow`).
    response = await fetch(`${resolveBaseUrl()}${path}`, {
      ...fetchOptions,
      headers,
      credentials: allowStateCookie ? 'include' : 'omit',
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
    const body = await readBody(response);
    const failure = toRequestError(
      401,
      body,
      'unauthenticated',
      'Authentication required',
    );

    if (failure.code === USERS_ERROR_CODES.profileUnavailable) {
      // The session is alive but the acting profile is locked. This is not an
      // expiry: refreshing would fail the same way, and the tokens stay valid
      // for choosing another profile. Publish the lock for the UI and surface
      // the error; the lock reason is the first detail's code.
      emitSessionEvent({
        type: 'profile_unavailable',
        profileUuid: profileUuid ?? getStoredProfileUuid(),
        reason: failure.details?.[0]?.code ?? '',
      });
      throw failure;
    }

    if (skipAuthRedirect || explicitToken !== undefined) {
      // The caller handles this 401 itself (bad credentials, a foreign token,
      // the refresh call): no refresh, no sign-out. The throw is unconditional
      // and relied upon by those callers.
      throw failure;
    }

    if (SIGN_OUT_CODES.has(failure.code)) {
      // Revoked or reused: the server killed the session, so refreshing is
      // pointless and any retained token is a liability. Sign the tab out.
      endTabSession(signOutReasonFor(failure.code));
      throw failure;
    }

    if (sentAsImpersonation) {
      // The impersonation token was rejected (expired or otherwise invalid).
      // The tab's refresh token and profile belong to the ADMINISTRATOR, so the
      // refresh grant below would answer with the administrator's own access
      // token and the replay would run this request as the administrator
      // instead of the impersonated profile: a write issued from the
      // impersonated context would execute with the administrator's authority.
      // So neither refresh nor replay. This IMPLIES the request fails, and the
      // impersonation ends for the whole tab.
      if (getStoredToken() === sentToken) {
        // Still the dead impersonation token: drop it and its marker, keeping
        // the administrator's lineage so the session can be restored. When the
        // stored token differs, another call already ended the impersonation
        // and a (possibly valid) administrator token is stored: it must stay.
        clearStoredToken();
        setStoredImpersonating(false);
        emitSessionEvent({ type: 'impersonation_ended' });
      }
      throw new ApiRequestError(
        USERS_ERROR_CODES.impersonationEnded,
        'The impersonation has ended. Your administrator session is being restored; repeat the action there if you still need it.',
        401,
      );
    }

    if (!state.refreshed && getStoredRefreshToken()) {
      // An expired or missing access token with a lineage to draw on: rotate
      // once and replay. `refreshed` makes a second 401 fall through to the
      // sign-out below instead of looping.
      await refreshTabToken(sentToken, resolveBaseUrl);
      return execute<T>(
        resolveBaseUrl,
        path,
        options,
        { ...state, refreshed: true },
      );
    }

    // No lineage, or the replay still failed: the tab is unauthenticated.
    endTabSession('unauthenticated');
    throw failure;
  }

  if (!response.ok) {
    // Parse the body once, then inspect the top-level member before
    // classifying: a body carrying `action` is a flow-control signal, not
    // an error, and MUST NEVER be thrown as ApiRequestError. This
    // short-circuit must run before any error-body handling below.
    const body = await readBody(response);

    const action = extractApiAction(body);
    if (action) {
      const actionError = new ApiActionRequiredError(
        action.code,
        action.message,
        sanitizeActionPath(action.path),
        response.status,
        action.data,
      );
      if (
        action.code === STEP_UP_REQUIRED_ACTION &&
        !skipStepUp &&
        !state.steppedUp &&
        stepUpHandler
      ) {
        // A handler is registered, so the user can raise the session's
        // assurance in place; replay once with the fresh token. `steppedUp`
        // stops a repeated 409 from prompting forever.
        let fresh: string;
        try {
          fresh = await runStepUp(actionError);
        } catch {
          // The user cancelled or the step-up failed: the original action
          // is what the caller needs to see, not the handler's rejection.
          throw actionError;
        }
        if (!fresh) throw actionError;
        // The step-up calls store their own token; storing again covers a
        // handler that obtained the token another way, and is a no-op
        // otherwise.
        if (fresh !== getStoredToken()) setStoredToken(fresh);
        return execute<T>(
          resolveBaseUrl,
          path,
          options,
          { ...state, steppedUp: true },
        );
      }
      // No handler (or already retried, or a step-up route): the caller
      // decides what to do with the action.
      throw actionError;
    }

    throw toRequestError(
      response.status,
      body,
      'unknown_error',
      `Request failed with status ${response.status}`,
    );
  }

  if (response.status === 204) {
    return undefined as T;
  }

  return response.json() as Promise<T>;
}

// ─── Client factory ──────────────────────────────────────────────────────────

/**
 * Options for {@link createUsersClient}.
 */
export interface UsersClientOptions {
  /** A fixed base URL, or a function resolved on every request. */
  baseUrl: string | (() => string);
}

function jsonBody(data: unknown): Pick<RequestInit, 'method' | 'body'> {
  return { method: 'POST', body: JSON.stringify(data) };
}

/** A path segment from a caller-supplied identifier. */
const seg = (value: string): string => encodeURIComponent(value);

/**
 * Creates a bound users-module API client.
 *
 * Every request sends `Authorization: Bearer <this tab's access token>` and
 * `credentials: 'omit'`. A 401 that is not a revocation triggers one shared
 * refresh and a single replay; a `409 users.step_up_required` consults the
 * handler registered with `setStepUpHandler` and replays once. Methods that
 * return a `TokenResponse` also commit it to this tab's storage.
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

  function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    return execute<T>(resolveBaseUrl, path, options, {
      refreshed: false,
      steppedUp: false,
    });
  }

  /** `request` for a call that answers a `TokenResponse`: commits it, returns it. */
  async function tokenRequest<T extends TokenResponse = TokenResponse>(
    path: string,
    options: RequestOptions = {},
  ): Promise<T> {
    const response = await request<T>(path, options);
    commitTokenResponse(response);
    return response;
  }

  /**
   * A POST that answers `{authorize_url}` and sets the signed OIDC state
   * cookie on that response. Browsers drop `Set-Cookie` from a
   * `credentials: 'omit'` fetch, so these two calls (OIDC link and OIDC
   * step-up start) are the library's only requests that include credentials.
   * The cookie is the callback's anti-forgery state, not an authentication
   * cookie: no route reads authentication from a cookie, and the call still
   * carries the Bearer token.
   */
  function startRedirectFlow(
    path: string,
    returnPath: string | undefined,
  ): Promise<AuthorizeURL> {
    let target = path;
    if (returnPath !== undefined) {
      if (!isSafeReturnPath(returnPath)) {
        // Refuse before any network call: the server would refuse it too,
        // and an unsafe value must never reach a redirect.
        return Promise.reject(
          new TypeError('returnPath must be a safe site-relative path'),
        );
      }
      target += `?return=${encodeURIComponent(returnPath)}`;
    }
    return request<AuthorizeURL>(target, {
      method: 'POST',
      allowStateCookie: true,
    });
  }

  return {
    /** The configured base URL, e.g. for use in OIDC redirect construction. */
    get baseUrl(): string {
      return resolveBaseUrl();
    },

    auth: {
      /** `POST /v1/auth/register` — 201 with the new session's token response. */
      register: (data: RegisterRequest) =>
        tokenRequest('/v1/auth/register', {
          ...jsonBody(data),
          // A 401 here is never an expired session.
          skipAuthRedirect: true,
        }),

      /**
       * `POST /v1/auth/login`. `access_token` is null and `selection_required`
       * true when no landing profile was found.
       */
      login: (email: string, password: string, options: LoginOptions = {}) =>
        tokenRequest('/v1/auth/login', {
          ...jsonBody({ email, password, ...options }),
          // 401 here means wrong email or password, not an expired session.
          skipAuthRedirect: true,
          profileUuid: options.profile_uuid,
        }),

      /** `POST /v1/auth/anonymous` — guest sign-in (201 new guest, 200 returning). */
      anonymous: (data: AnonymousRequest) =>
        tokenRequest<AnonymousTokenResponse>('/v1/auth/anonymous', {
          ...jsonBody(data),
          skipAuthRedirect: true,
        }),

      forgotPassword: (data: ForgotPasswordRequest) =>
        request<void>('/v1/auth/password-reset/request', {
          ...jsonBody(data),
          skipAuthRedirect: true,
        }),

      resetPassword: (data: ResetPasswordRequest) =>
        request<void>('/v1/auth/password-reset/confirm', {
          ...jsonBody(data),
          // 401 here means an invalid or expired reset token, not an expired session.
          skipAuthRedirect: true,
        }),

      requestEmailCode: (
        data: EmailCodeRequest,
        options?: Pick<RequestOptions, 'skipAuthRedirect'>,
      ) =>
        request<void>('/v1/auth/email-code/request', {
          ...jsonBody(data),
          ...options,
        }),

      verifyEmailCode: (data: EmailCodeVerifyRequest) =>
        tokenRequest('/v1/auth/email-code/verify', {
          ...jsonBody(data),
          // 401 here means a wrong, expired, or unknown-user code.
          skipAuthRedirect: true,
          profileUuid: data.profile_uuid,
        }),

      /**
       * `POST /v1/auth/token` with the `refresh_token` grant: rotates the
       * supplied refresh token and issues an access token for the named
       * profile. Used by `selectProfile`; ordinary expiry is refreshed
       * automatically by the request pipeline.
       */
      token: (data: RefreshTokenRequest) =>
        tokenRequest('/v1/auth/token', {
          ...jsonBody(data),
          // The grant carries its own credential; never refresh around it.
          skipAuthRedirect: true,
          skipStepUp: true,
          profileUuid: data.profile_uuid,
        }),

      /**
       * Rotates this tab's refresh token after an OIDC step-up so the access
       * token carries the raised assurance. `sentToken` is the access token the
       * caller observed before asking (null when there was none).
       *
       * Goes through the per-tab single-flight shared with the request
       * pipeline, so it joins a rotation already in flight and never spends
       * the same refresh token twice; a rotation that landed after `sentToken`
       * was observed is not repeated.
       */
      refreshAfterStepUp: async (sentToken: string | null): Promise<void> => {
        // WHY: an impersonating tab holds the target's access token while the
        // stored refresh lineage is the administrator's. IMPLIES rotating here
        // would replace the impersonation token with the administrator's, so
        // the impersonation token is never refreshed; the tab just returns.
        if (getStoredImpersonating()) return;
        await refreshTabToken(sentToken, resolveBaseUrl);
      },

      /**
       * `POST /v1/auth/handoff` — exchanges the one-time OIDC `#handoff=`
       * code, with the verifier behind the challenge this tab sent at OIDC
       * start, for a token response. Prefer the auth context's
       * `completeHandoff`, which supplies and clears the stored verifier.
       */
      handoff: (code: string, verifier: string) =>
        tokenRequest('/v1/auth/handoff', {
          ...jsonBody({ code, verifier }),
          skipAuthRedirect: true,
        }),

      /** `GET /v1/auth/providers` — never throws; see {@link fetchProviders}. */
      providers: (): Promise<OIDCProvider[]> => fetchProviders(),

      /**
       * Begins an OIDC sign-in: creates this tab's handoff verifier, stores
       * it, and resolves with the URL of `GET /v1/auth/oidc/{provider}/start`
       * carrying the verifier's SHA-256 challenge (and the optional return
       * path). The caller navigates the browser to it (the route answers with
       * a redirect, so it cannot be a fetch).
       */
      oidcStart: async (
        provider: string,
        options: { returnPath?: string } = {},
      ): Promise<string> => {
        const { returnPath } = options;
        if (returnPath !== undefined && !isSafeReturnPath(returnPath)) {
          // Reject before storing a verifier or building a URL: an unsafe
          // return value must never be forwarded to the server.
          throw new TypeError('returnPath must be a safe site-relative path');
        }
        const challenge = await beginHandoff();
        const query = new URLSearchParams({ handoff_challenge: challenge });
        if (returnPath !== undefined) query.set('return', returnPath);
        return `${resolveBaseUrl()}/v1/auth/oidc/${seg(provider)}/start?${query.toString()}`;
      },
    },

    session: {
      /** `GET /v1/session` — the session, acting profile and profile list. */
      get: async (options?: Pick<RequestOptions, 'skipAuthRedirect'>) => {
        const info = await request<SessionInfo>('/v1/session', options);
        // A reload (or a tab opened before the marker existed) restores no
        // token response, so this is where the tab learns its token is an
        // impersonation token. Only a present, authoritative session is
        // trusted: a response with no session leaves the marker alone.
        // This IMPLIES a later 401 on an impersonated request is not replayed
        // as the administrator.
        if (info.session && getStoredToken() !== null) {
          setStoredImpersonating(info.session.impersonating === true);
        }
        return info;
      },

      /**
       * `POST /v1/session/switch` — a new access token acting as another
       * profile of the same session. Replaces only this tab's access token and
       * active profile (no refresh token is minted).
       */
      switch: (profileUuid: string) =>
        tokenRequest('/v1/session/switch', {
          ...jsonBody({ profile_uuid: profileUuid }),
          profileUuid,
        }),

      /**
       * `POST /v1/session/fork` — a new refresh lineage for this tab, using
       * an access token handed over by a sibling tab (the caller's own tab
       * has none yet).
       */
      fork: (options: { accessToken: string }) =>
        tokenRequest('/v1/session/fork', {
          method: 'POST',
          accessToken: options.accessToken,
        }),

      stepUp: {
        /** `POST /v1/session/step-up/password` — returns a fresh access token. */
        password: (password: string) =>
          tokenRequest('/v1/session/step-up/password', {
            ...jsonBody({ password }),
            // A wrong password is the same 409 action; it is not a prompt.
            skipStepUp: true,
          }),

        /** `POST /v1/session/step-up/email-code/request` — emails a code (204). */
        requestEmailCode: (email?: string) =>
          request<void>('/v1/session/step-up/email-code/request', {
            ...jsonBody(email ? { email } : {}),
            skipStepUp: true,
          }),

        /** `POST /v1/session/step-up/email-code/verify` — returns a fresh access token. */
        verifyEmailCode: (code: string, email?: string) =>
          tokenRequest('/v1/session/step-up/email-code/verify', {
            ...jsonBody(email ? { code, email } : { code }),
            skipStepUp: true,
          }),

        /**
         * `POST /v1/session/step-up/oidc/{provider}/start` — resolves with the
         * provider's authorize URL; the caller navigates the browser to it.
         */
        startOidc: (provider: string, returnPath?: string) =>
          startRedirectFlow(
            `/v1/session/step-up/oidc/${seg(provider)}/start`,
            returnPath,
          ),
      },

      /**
       * `POST /v1/session/logout` — revokes the session and its lineages.
       * Does not touch local storage; the auth context's `logout` does.
       */
      logout: () =>
        request<void>('/v1/session/logout', {
          method: 'POST',
          // An already-dead session is a successful logout, not a failure to
          // redirect on.
          skipAuthRedirect: true,
        }),
    },

    me: {
      get: (options?: Pick<RequestOptions, 'skipAuthRedirect'>) =>
        request<UserView>('/v1/me', options),
      update: (data: UpdateMeRequest) =>
        request<UserView>('/v1/me', {
          method: 'PUT',
          body: JSON.stringify(data),
        }),

      profiles: {
        list: () => request<ProfileList>('/v1/me/profiles'),
        /** Accepts an invitation; resolves with the profile as this session sees it. */
        accept: (uuid: string) =>
          request<ProfileView>(`/v1/me/profiles/${seg(uuid)}/accept`, {
            method: 'POST',
          }),
        decline: (uuid: string) =>
          request<void>(`/v1/me/profiles/${seg(uuid)}/decline`, {
            method: 'POST',
          }),
        leave: (uuid: string) =>
          request<void>(`/v1/me/profiles/${seg(uuid)}/leave`, {
            method: 'POST',
          }),
      },

      identifiers: {
        list: () => request<IdentifierList>('/v1/me/identifiers'),
        add: (data: AddIdentifierRequest) =>
          request<IdentifierView>('/v1/me/identifiers', jsonBody(data)),
        /** Verifies an identifier with the emailed code. */
        verify: (uuid: string, code: string) =>
          request<IdentifierView>(`/v1/me/identifiers/${seg(uuid)}/verify`, {
            ...jsonBody({ code }),
          }),
        /** Emails a verification code (204). */
        resendVerification: (uuid: string) =>
          request<void>(`/v1/me/identifiers/${seg(uuid)}/verification`, {
            method: 'POST',
          }),
        setPrimary: (uuid: string) =>
          request<IdentifierView>(`/v1/me/identifiers/${seg(uuid)}/primary`, {
            method: 'PUT',
          }),
        delete: (uuid: string) =>
          request<void>(`/v1/me/identifiers/${seg(uuid)}`, {
            method: 'DELETE',
          }),
      },

      credentials: {
        list: () => request<CredentialList>('/v1/me/credentials'),
        addPassword: (data: AddPasswordRequest) =>
          request<CredentialView>('/v1/me/credentials/password', jsonBody(data)),
        addSshKey: (data: AddSSHKeyRequest) =>
          request<CredentialView>(
            '/v1/me/credentials/ssh-keys',
            jsonBody(data),
          ),
        /**
         * `POST /v1/me/credentials/oidc/{provider}/start` — resolves with the
         * provider's authorize URL for linking; the caller navigates to it.
         */
        startOidcLink: (provider: string, returnPath?: string) =>
          startRedirectFlow(
            `/v1/me/credentials/oidc/${seg(provider)}/start`,
            returnPath,
          ),
        /** Updates the label and/or landing profile (`null` clears the latter). */
        patch: (uuid: string, data: PatchCredentialRequest) =>
          request<CredentialView>(`/v1/me/credentials/${seg(uuid)}`, {
            method: 'PATCH',
            body: JSON.stringify(data),
          }),
        /** Revokes a credential; the last interactive one is `409 users.last_credential`. */
        delete: (uuid: string) =>
          request<void>(`/v1/me/credentials/${seg(uuid)}`, {
            method: 'DELETE',
          }),
      },

      sessions: {
        list: () => request<SessionList>('/v1/me/sessions'),
        revoke: (uuid: string) =>
          request<void>(`/v1/me/sessions/${seg(uuid)}`, { method: 'DELETE' }),
      },

      /** `POST /v1/me/upgrade` — turns the calling guest into a registered user. */
      upgrade: (data: UpgradeRequest) =>
        request<UserView>('/v1/me/upgrade', jsonBody(data)),
    },

    impersonations: {
      /**
       * `POST /v1/impersonations` — starts an impersonation of a profile. The
       * tab then holds the impersonation access token; its refresh lineage
       * and stored profile stay the administrator's own.
       */
      start: (data: StartImpersonationRequest) =>
        tokenRequest('/v1/impersonations', jsonBody(data)),

      /** `POST /v1/impersonations/current/renew` — extends the impersonation. */
      renew: () =>
        tokenRequest('/v1/impersonations/current/renew', { method: 'POST' }),

      /**
       * `DELETE /v1/impersonations/current` — ends the impersonation, then
       * restores the administrator's own access token with the tab's refresh
       * lineage.
       */
      end: async (): Promise<void> => {
        try {
          await request<void>('/v1/impersonations/current', { method: 'DELETE' });
        } catch (err) {
          // The impersonation token had already expired: the pipeline dropped
          // it locally and did NOT replay the DELETE (a replay would run under
          // the administrator's token). There is nothing left to end on the
          // server, which is the outcome the caller asked for, so continue to
          // restoring the administrator session. Any other failure is real.
          if (
            !(
              err instanceof ApiRequestError &&
              err.code === USERS_ERROR_CODES.impersonationEnded
            )
          ) {
            throw err;
          }
        }
        // The impersonation token is dead either way; drop it so the refresh
        // below is not skipped as "already refreshed", and so the marker
        // cannot outlive the token it describes.
        clearStoredToken();
        setStoredImpersonating(false);
        // Swap back to the administrator's own token. A non-null stored token
        // here means a concurrent restore (the auth context's reaction to the
        // ended-impersonation event) already landed it: rotating again would
        // only spend the refresh token twice.
        if (getStoredToken() !== null) return;
        if (getStoredRefreshToken()) {
          await refreshTabToken(null, resolveBaseUrl);
        } else {
          // Nothing to restore from: the tab can only sign out.
          endTabSession('unauthenticated');
        }
      },
    },

    bootstrap: {
      /** `GET /v1/bootstrap/status` — public. */
      status: () =>
        request<BootstrapStatus>('/v1/bootstrap/status', {
          skipAuthRedirect: true,
        }),

      /**
       * `POST /v1/bootstrap` — first-run bootstrap with the setup token
       * (sent as `X-Setup-Token`, never stored or logged). 201 with a token
       * response landing on the administrator profile.
       */
      bootstrap: (data: BootstrapRequest, setupToken: string) =>
        tokenRequest('/v1/bootstrap', {
          ...jsonBody(data),
          headers: { 'X-Setup-Token': setupToken },
          // 401 here is a wrong setup token, not an expired session.
          skipAuthRedirect: true,
        }),
    },

    audit: {
      list: () => request<AuditListResponse>('/v1/audit'),
      byEntity: (entityUuid: string) =>
        request<AuditListResponse>(
          `/v1/audit?entity_uuid=${encodeURIComponent(entityUuid)}`,
        ),
    },
  };
}

export type UsersClient = ReturnType<typeof createUsersClient>;

// ─── Default singleton (convenience for the embedded AuthProvider) ────────────
// The library's AuthProvider calls api.session.get() / api.auth.login() etc.
// internally. It reads the base URL lazily so consumers do NOT need to wire in
// the client themselves — the env var is enough for 95% of use cases.
// Consumers that need SSR-safe base URL injection can instead use
// `createUsersClient` directly.

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
      credentials: 'omit',
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
