import { afterEach, describe, expect, test } from 'bun:test';
import {
  ApiActionRequiredError,
  ApiRequestError,
  createUsersClient,
  setStepUpHandler,
  subscribeSessionEvents,
  type SessionEvent,
} from './api';
import {
  configureUsersApi,
  getStoredHandoffVerifier,
  getStoredImpersonating,
  getStoredProfileUuid,
  getStoredRefreshToken,
  getStoredToken,
  resetUsersApiConfig,
  setStoredImpersonating,
  setStoredProfileUuid,
  setStoredRefreshToken,
  setStoredToken,
  type UnauthenticatedContext,
} from './config';
import { computeHandoffChallenge } from './handoff';
import { PROFILE_A, tokenResponse } from '../test-support/users-fixtures';

// Exercises `request()`'s non-2xx discrimination logic (the fix this task
// implements): a body carrying a top-level `action` member must short-circuit
// straight to `ApiActionRequiredError` and never reach the `ApiRequestError`
// path, a body carrying a top-level `error` object must still throw
// `ApiRequestError` exactly as before, and `action.path` must be sanitized
// against the documented attack shapes before being attached to the thrown
// error.

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
  resetUsersApiConfig();
  setStepUpHandler(null);
  sessionStorage.clear();
});

function stubFetch(body: unknown, status: number): void {
  globalThis.fetch = (async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })) as unknown as typeof fetch;
}

const client = createUsersClient({ baseUrl: 'http://localhost:8080' });

async function requestAndCatch(): Promise<unknown> {
  return client.me.get({ skipAuthRedirect: true }).catch((err: unknown) => err);
}

describe('request() action-required discrimination', () => {
  test('a 403 body with a top-level action throws ApiActionRequiredError with the right fields', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'Verify your email address before continuing.',
          path: '/verify-email',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;

    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(err).not.toBeInstanceOf(ApiRequestError);
    expect(err.code).toBe('users.email_unverified');
    expect(err.message).toBe('Verify your email address before continuing.');
    expect(err.path).toBe('/verify-email');
    expect(err.status).toBe(403);
    expect(err.data).toBeUndefined();
  });

  test('a 503 body with a top-level action carries optional auxiliary data through', async () => {
    stubFetch(
      {
        action: {
          code: 'users.oidc_not_confirmed',
          message: 'Single sign-on is not finished configuring.',
          path: '/oidc-config',
          data: { state: 'awaiting_oidc_config' },
        },
      },
      503,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;

    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(err.status).toBe(503);
    expect(err.data).toEqual({ state: 'awaiting_oidc_config' });
  });

  test('a 409 body with a top-level action (step-up) throws ApiActionRequiredError, never ApiRequestError', async () => {
    stubFetch(
      {
        action: {
          code: 'users.step_up_required',
          message: 'Confirm your identity to remove this login method.',
          path: '/step-up?return=%2Fself%2Fidentities',
        },
      },
      409,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;

    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(err).not.toBeInstanceOf(ApiRequestError);
    expect(err.path).toBe('/step-up?return=%2Fself%2Fidentities');
  });

  test('a 409 body with a top-level error object still throws ApiRequestError exactly as before (regression check)', async () => {
    stubFetch(
      {
        error: {
          code: 'conflict',
          message: "You can't remove your last sign-in method. Add another first.",
        },
      },
      409,
    );

    const err = (await requestAndCatch()) as ApiRequestError;

    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err).not.toBeInstanceOf(ApiActionRequiredError);
    expect(err.code).toBe('conflict');
    expect(err.message).toBe(
      "You can't remove your last sign-in method. Add another first.",
    );
    expect(err.status).toBe(409);
  });

  test('a 403 body with a top-level error object still throws ApiRequestError exactly as before (regression check)', async () => {
    stubFetch(
      { error: { code: 'forbidden', message: 'You may not access this.' } },
      403,
    );

    const err = (await requestAndCatch()) as ApiRequestError;

    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.code).toBe('forbidden');
    expect(err.status).toBe(403);
  });

  test('a flat string error member degrades gracefully to unknown_error instead of crashing (latent-bug fix)', async () => {
    stubFetch({ error: 'some_flat_string_error' }, 403);

    const err = (await requestAndCatch()) as ApiRequestError;

    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.code).toBe('unknown_error');
    expect(err.status).toBe(403);
  });
});

describe('request() action.path same-origin-relative guard', () => {
  test('accepts a well-formed single-leading-slash path unchanged', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'Verify your email address before continuing.',
          path: '/verify-email',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/verify-email');
  });

  test('rejects an empty path and falls back to the safe default', async () => {
    stubFetch(
      { action: { code: 'users.email_unverified', message: 'msg', path: '' } },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/');
  });

  test('rejects an absolute URL with a scheme and falls back to the safe default', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'msg',
          path: 'https://evil.example.com/steal',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/');
  });

  test('rejects a protocol-relative //host/... authority and falls back to the safe default', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'msg',
          path: '//evil.example.com/steal',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/');
  });

  test('rejects a /\\host/... backslash trick and falls back to the safe default', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'msg',
          path: '/\\evil.example.com/steal',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/');
  });

  test('rejects a leading-slash path with an embedded tab before a second slash and falls back to the safe default', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'msg',
          path: '/\t/evil.example.com',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/');
  });
});


// ─── Test harness ────────────────────────────────────────────────────────────

interface RecordedCall {
  url: string;
  method: string;
  headers: Record<string, string>;
  credentials: RequestCredentials | undefined;
  body: unknown;
}

type Responder = (call: RecordedCall, index: number) => Response | Promise<Response>;

/** Replaces fetch with `respond`, recording every call. */
function scriptFetch(respond: Responder): RecordedCall[] {
  const calls: RecordedCall[] = [];
  globalThis.fetch = (async (url: string, init: RequestInit = {}) => {
    const call: RecordedCall = {
      url: String(url),
      method: init.method ?? 'GET',
      headers: (init.headers ?? {}) as Record<string, string>,
      credentials: init.credentials,
      body: typeof init.body === 'string' ? JSON.parse(init.body) : init.body,
    };
    calls.push(call);
    return respond(call, calls.length - 1);
  }) as unknown as typeof fetch;
  return calls;
}

const json = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

const noContent = (): Response => new Response(null, { status: 204 });

const errorBody = (code: string, details?: { field: string; code: string; message: string }[]) => ({
  error: { code, message: `${code} message`, ...(details ? { details } : {}) },
});

/** A tab holding a session: access, refresh and profile in sessionStorage. */
function seedTab(access = 'old-access', refresh = 'old-refresh', profile = 'prof-a'): void {
  setStoredToken(access);
  setStoredRefreshToken(refresh);
  setStoredProfileUuid(profile);
}

/** Captures session events for the duration of one test. */
function collectEvents(): { events: SessionEvent[]; stop: () => void } {
  const events: SessionEvent[] = [];
  const stop = subscribeSessionEvents((e) => events.push(e));
  return { events, stop };
}

const bearer = (call: RecordedCall): string | undefined => call.headers['Authorization'];

// ─── Bearer-only requests ────────────────────────────────────────────────────

describe('Bearer-only requests', () => {
  test('every request sends the tab token as Bearer with credentials omit', async () => {
    seedTab('tab-token');
    const calls = scriptFetch(() => json({}));
    await client.me.get();
    await client.session.get();
    await client.me.sessions.list();
    await client.bootstrap.status();
    await client.auth.forgotPassword({ email: 'a@b.co' });
    expect(calls.length).toBe(5);
    for (const call of calls) {
      expect(bearer(call)).toBe('Bearer tab-token');
      expect(call.credentials).toBe('omit');
    }
  });

  test('no Authorization header is sent when the tab holds no token', async () => {
    const calls = scriptFetch(() => json({}));
    await client.bootstrap.status();
    expect(bearer(calls[0]!)).toBeUndefined();
    expect(calls[0]!.credentials).toBe('omit');
  });

  test('the OIDC authorize-URL start calls are the only requests that include credentials', async () => {
    seedTab();
    const calls = scriptFetch(() => json({ authorize_url: 'https://idp.test/auth' }));
    const link = await client.me.credentials.startOidcLink('google', '/account/credentials');
    const stepUp = await client.session.stepUp.startOidc('google');
    expect(link.authorize_url).toBe('https://idp.test/auth');
    expect(stepUp.authorize_url).toBe('https://idp.test/auth');
    expect(calls[0]!.url).toBe(
      'http://localhost:8080/v1/me/credentials/oidc/google/start?return=%2Faccount%2Fcredentials',
    );
    expect(calls[1]!.url).toBe('http://localhost:8080/v1/session/step-up/oidc/google/start');
    expect(calls.map((c) => c.credentials)).toEqual(['include', 'include']);
    // Still Bearer-authenticated: the cookie is only the OIDC state.
    expect(bearer(calls[0]!)).toBe('Bearer old-access');
  });

  test('an unsafe returnPath is refused before any request', async () => {
    const calls = scriptFetch(() => json({}));
    await expect(
      client.me.credentials.startOidcLink('google', '//evil.test'),
    ).rejects.toBeInstanceOf(TypeError);
    expect(calls.length).toBe(0);
  });

  test('tokens go to sessionStorage only: localStorage is never used', async () => {
    scriptFetch(() => json(tokenResponse()));
    await client.auth.login('a@b.co', 'pw');
    expect(sessionStorage.getItem('auth_token')).toBe('access-1');
    expect(localStorage.length).toBe(0);
    expect(document.cookie).toBe('');
  });
});

// ─── Token response commit ───────────────────────────────────────────────────

describe('token responses are committed to the tab', () => {
  test('login stores access, refresh and profile and publishes the response', async () => {
    const { events, stop } = collectEvents();
    const calls = scriptFetch(() => json(tokenResponse()));
    const resp = await client.auth.login('a@b.co', 'pw', { app: 'app-1', profile_uuid: 'prof-a' });
    stop();
    expect(resp.access_token).toBe('access-1');
    expect(calls[0]!.url).toBe('http://localhost:8080/v1/auth/login');
    expect(calls[0]!.body).toEqual({
      email: 'a@b.co',
      password: 'pw',
      app: 'app-1',
      profile_uuid: 'prof-a',
    });
    expect(getStoredToken()).toBe('access-1');
    expect(getStoredRefreshToken()).toBe('refresh-1');
    expect(getStoredProfileUuid()).toBe('prof-a');
    expect(events.map((e) => e.type)).toEqual(['token_response']);
  });

  test('selection_required keeps the refresh token but stores no access token', async () => {
    seedTab('stale-access', 'stale-refresh', 'stale-prof');
    scriptFetch(() =>
      json(
        tokenResponse({
          access_token: null,
          profile: null,
          selection_required: true,
          profiles: [PROFILE_A],
          refresh_token: 'fresh-refresh',
        }),
      ),
    );
    await client.auth.login('a@b.co', 'pw');
    expect(getStoredToken()).toBeNull();
    expect(getStoredRefreshToken()).toBe('fresh-refresh');
    // The earlier session's profile must not leak into the new lineage.
    expect(getStoredProfileUuid()).toBeNull();
  });

  test('a wrong password is a plain 401 error: nothing cleared, no handler run', async () => {
    seedTab();
    const unauth: UnauthenticatedContext[] = [];
    configureUsersApi({ onUnauthenticated: (c) => unauth.push(c) });
    scriptFetch(() => json(errorBody('users.invalid_credentials'), 401));
    const err = (await client.auth.login('a@b.co', 'bad').catch((e: unknown) => e)) as ApiRequestError;
    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.code).toBe('users.invalid_credentials');
    expect(err.status).toBe(401);
    expect(getStoredToken()).toBe('old-access');
    expect(unauth).toEqual([]);
  });

  test('register answers a session directly (no chained login)', async () => {
    const calls = scriptFetch(() => json(tokenResponse(), 201));
    await client.auth.register({ email: 'a@b.co', password: 'pw-pw-pw-pw-12', given_name: 'A', family_name: 'B' });
    expect(calls.length).toBe(1);
    expect(getStoredToken()).toBe('access-1');
  });

  test('switch replaces only the access token and active profile', async () => {
    seedTab('old-access', 'old-refresh', 'prof-a');
    const calls = scriptFetch(() =>
      json(
        tokenResponse({
          access_token: 'switched-access',
          refresh_token: null,
          profile: { ...PROFILE_A, uuid: 'prof-b' },
        }),
      ),
    );
    await client.session.switch('prof-b');
    expect(calls[0]!.url).toBe('http://localhost:8080/v1/session/switch');
    expect(calls[0]!.body).toEqual({ profile_uuid: 'prof-b' });
    expect(getStoredToken()).toBe('switched-access');
    expect(getStoredProfileUuid()).toBe('prof-b');
    // The lineage is untouched: switch never rotates or clears the refresh token.
    expect(getStoredRefreshToken()).toBe('old-refresh');
  });

  test('impersonation start keeps the administrator profile and lineage', async () => {
    seedTab('admin-access', 'admin-refresh', 'prof-admin');
    scriptFetch(() =>
      json(
        tokenResponse({
          access_token: 'imp-access',
          refresh_token: null,
          session: { uuid: 'imp-sess', acr: 1, amr: ['pwd'], auth_time: 'x', impersonating: true },
          profile: { ...PROFILE_A, uuid: 'prof-target' },
        }),
        201,
      ),
    );
    await client.impersonations.start({ profile_uuid: 'prof-target', reason: 'support' });
    expect(getStoredToken()).toBe('imp-access');
    expect(getStoredProfileUuid()).toBe('prof-admin');
    expect(getStoredRefreshToken()).toBe('admin-refresh');
  });

  test('ending an impersonation deletes it, then restores the administrator token by refresh', async () => {
    seedTab('imp-access', 'admin-refresh', 'prof-admin');
    const calls = scriptFetch((call) =>
      call.method === 'DELETE'
        ? noContent()
        : json(tokenResponse({ access_token: 'admin-access', refresh_token: 'admin-refresh-2' })),
    );
    await client.impersonations.end();
    expect(calls.map((c) => `${c.method} ${c.url.replace('http://localhost:8080', '')}`)).toEqual([
      'DELETE /v1/impersonations/current',
      'POST /v1/auth/token',
    ]);
    expect(calls[1]!.body).toEqual({
      grant_type: 'refresh_token',
      refresh_token: 'admin-refresh',
      profile_uuid: 'prof-admin',
    });
    expect(getStoredToken()).toBe('admin-access');
    expect(getStoredRefreshToken()).toBe('admin-refresh-2');
  });
});

// ─── Refresh lineage ─────────────────────────────────────────────────────────

describe('refresh lineage', () => {
  /** A fetch that 401s `/v1/me` until the refreshed token is presented. */
  function expiredThenRefreshed(extra?: Responder): RecordedCall[] {
    return scriptFetch((call, index) => {
      if (call.url.endsWith('/v1/auth/token')) {
        return json(tokenResponse({ access_token: 'new-access', refresh_token: 'new-refresh' }));
      }
      if (bearer(call) === 'Bearer new-access') return json({ uuid: 'u1' });
      return extra ? extra(call, index) : json(errorBody('unauthorized'), 401);
    });
  }

  test('a 401 refreshes once with the tab refresh token and profile, stores the rotation, and replays', async () => {
    seedTab();
    const calls = expiredThenRefreshed();
    const user = await client.me.get();
    expect(user).toEqual({ uuid: 'u1' } as never);
    expect(calls.map((c) => c.url.replace('http://localhost:8080', ''))).toEqual([
      '/v1/me',
      '/v1/auth/token',
      '/v1/me',
    ]);
    expect(calls[1]!.body).toEqual({
      grant_type: 'refresh_token',
      refresh_token: 'old-refresh',
      profile_uuid: 'prof-a',
    });
    expect(getStoredRefreshToken()).toBe('new-refresh');
    expect(getStoredToken()).toBe('new-access');
    expect(bearer(calls[2]!)).toBe('Bearer new-access');
  });

  test('concurrent expired requests share a single in-flight refresh', async () => {
    seedTab();
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const calls = scriptFetch(async (call) => {
      if (call.url.endsWith('/v1/auth/token')) {
        await gate; // hold the rotation open while the other request fails too
        return json(tokenResponse({ access_token: 'new-access', refresh_token: 'new-refresh' }));
      }
      if (bearer(call) === 'Bearer new-access') return json({ ok: true });
      return json(errorBody('unauthorized'), 401);
    });
    const pending = Promise.all([client.me.get(), client.me.sessions.list(), client.session.get()]);
    // Let all three reach their 401 and join the rotation before releasing it.
    await new Promise((r) => setTimeout(r, 20));
    release();
    await pending;
    const tokenCalls = calls.filter((c) => c.url.endsWith('/v1/auth/token'));
    expect(tokenCalls.length).toBe(1);
    expect(calls.filter((c) => bearer(c) === 'Bearer new-access').length).toBe(3);
  });

  test('a 401 for a request sent before another request rotated the token replays without a second rotation', async () => {
    seedTab('old-access');
    let releaseFirst!: () => void;
    const firstGate = new Promise<void>((r) => (releaseFirst = r));
    const calls = scriptFetch(async (call, index) => {
      if (index === 0) {
        await firstGate;
        return json(errorBody('unauthorized'), 401);
      }
      return json({ ok: true });
    });
    const pending = client.me.get(); // goes out carrying old-access
    await new Promise((r) => setTimeout(r, 5));
    // Another request already rotated the token while the first was in flight.
    setStoredToken('rotated-access');
    releaseFirst();
    await pending;
    expect(calls.some((c) => c.url.endsWith('/v1/auth/token'))).toBe(false);
    expect(bearer(calls[1]!)).toBe('Bearer rotated-access');
  });

  test('a replay that still 401s signs the tab out after exactly one refresh', async () => {
    seedTab();
    const unauth: UnauthenticatedContext[] = [];
    configureUsersApi({ onUnauthenticated: (c) => unauth.push(c) });
    const { events, stop } = collectEvents();
    const calls = scriptFetch((call) =>
      call.url.endsWith('/v1/auth/token')
        ? json(tokenResponse({ access_token: 'new-access', refresh_token: 'new-refresh' }))
        : json(errorBody('unauthorized'), 401),
    );
    const err = (await client.me.get().catch((e: unknown) => e)) as ApiRequestError;
    stop();
    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.status).toBe(401);
    expect(calls.filter((c) => c.url.endsWith('/v1/auth/token')).length).toBe(1);
    expect(getStoredToken()).toBeNull();
    expect(getStoredRefreshToken()).toBeNull();
    expect(unauth.length).toBe(1);
    expect(events.at(-1)).toEqual({ type: 'signed_out', reason: 'unauthenticated' });
  });

  test('a 401 with no refresh token signs out without any refresh call', async () => {
    setStoredToken('only-access');
    const unauth: UnauthenticatedContext[] = [];
    configureUsersApi({ onUnauthenticated: (c) => unauth.push(c) });
    const calls = scriptFetch(() => json(errorBody('unauthorized'), 401));
    await client.me.get().catch(() => {});
    expect(calls.length).toBe(1);
    expect(getStoredToken()).toBeNull();
    expect(unauth.length).toBe(1);
  });

  for (const [code, reason] of [
    ['users.session_revoked', 'session_revoked'],
    ['users.refresh_reuse', 'refresh_reuse'],
  ] as const) {
    test(`${code} on a request clears every token and signs out without refreshing`, async () => {
      seedTab();
      const unauth: UnauthenticatedContext[] = [];
      configureUsersApi({ onUnauthenticated: (c) => unauth.push(c) });
      const { events, stop } = collectEvents();
      const calls = scriptFetch(() => json(errorBody(code), 401));
      const err = (await client.me.get().catch((e: unknown) => e)) as ApiRequestError;
      stop();
      expect(err.code).toBe(code);
      expect(calls.length).toBe(1);
      expect(getStoredToken()).toBeNull();
      expect(getStoredRefreshToken()).toBeNull();
      expect(getStoredProfileUuid()).toBeNull();
      expect(unauth.length).toBe(1);
      expect(events).toEqual([{ type: 'signed_out', reason }]);
    });

    test(`${code} answered to the refresh itself signs the tab out`, async () => {
      seedTab();
      const unauth: UnauthenticatedContext[] = [];
      configureUsersApi({ onUnauthenticated: (c) => unauth.push(c) });
      const { events, stop } = collectEvents();
      scriptFetch((call) =>
        call.url.endsWith('/v1/auth/token')
          ? json(errorBody(code), 401)
          : json(errorBody('unauthorized'), 401),
      );
      const err = (await client.me.get().catch((e: unknown) => e)) as ApiRequestError;
      stop();
      expect(err.code).toBe(code);
      expect(getStoredRefreshToken()).toBeNull();
      expect(unauth.length).toBe(1);
      expect(events.at(-1)).toEqual({ type: 'signed_out', reason });
    });
  }

  test('a network failure during refresh keeps the tokens (the failure may be transient)', async () => {
    seedTab();
    const unauth: UnauthenticatedContext[] = [];
    configureUsersApi({ onUnauthenticated: (c) => unauth.push(c) });
    scriptFetch((call) => {
      if (call.url.endsWith('/v1/auth/token')) throw new Error('offline');
      return json(errorBody('unauthorized'), 401);
    });
    const err = (await client.me.get().catch((e: unknown) => e)) as ApiRequestError;
    expect(err.code).toBe('network_error');
    expect(getStoredRefreshToken()).toBe('old-refresh');
    expect(unauth).toEqual([]);
  });

  test('profile_unavailable keeps tokens, skips refresh, and publishes the lock reason', async () => {
    seedTab();
    const { events, stop } = collectEvents();
    const calls = scriptFetch(() =>
      json(
        errorBody('users.profile_unavailable', [
          { field: 'profile', code: 'profile_suspended', message: 'suspended' },
        ]),
        401,
      ),
    );
    const err = (await client.me.get().catch((e: unknown) => e)) as ApiRequestError;
    stop();
    expect(err.code).toBe('users.profile_unavailable');
    expect(calls.length).toBe(1);
    expect(getStoredToken()).toBe('old-access');
    expect(getStoredRefreshToken()).toBe('old-refresh');
    expect(events).toEqual([
      { type: 'profile_unavailable', profileUuid: 'prof-a', reason: 'profile_suspended' },
    ]);
  });

  test('a refresh grant for a locked profile attributes the lock to the requested profile', async () => {
    seedTab();
    const { events, stop } = collectEvents();
    scriptFetch(() =>
      json(
        errorBody('users.profile_unavailable', [
          { field: 'profile', code: 'acr_too_low', message: 'step up' },
        ]),
        401,
      ),
    );
    await client
      .auth.token({ grant_type: 'refresh_token', refresh_token: 'old-refresh', profile_uuid: 'prof-z' })
      .catch(() => {});
    stop();
    expect(events).toEqual([{ type: 'profile_unavailable', profileUuid: 'prof-z', reason: 'acr_too_low' }]);
    expect(getStoredRefreshToken()).toBe('old-refresh');
  });
});

// ─── Impersonation: no refresh-and-replay as the administrator ───────────────

describe('impersonated requests are never replayed as the administrator', () => {
  /** A tab that started an impersonation through the client, as the UI does. */
  async function impersonatingTab(): Promise<void> {
    seedTab('admin-access', 'admin-refresh', 'prof-admin');
    scriptFetch(() =>
      json(
        tokenResponse({
          access_token: 'imp-access',
          refresh_token: null,
          session: { uuid: 'imp-sess', acr: 1, amr: ['pwd'], auth_time: 'x', impersonating: true },
          profile: { ...PROFILE_A, uuid: 'prof-target' },
        }),
        201,
      ),
    );
    await client.impersonations.start({ profile_uuid: 'prof-target', reason: 'support' });
    expect(getStoredToken()).toBe('imp-access');
    expect(getStoredImpersonating()).toBe(true);
  }

  test('a 401 on a write ends the impersonation without refreshing or replaying', async () => {
    await impersonatingTab();
    const { events, stop } = collectEvents();
    const calls = scriptFetch(() => json(errorBody('unauthenticated'), 401));
    const err = (await client.me.update({}).catch((e: unknown) => e)) as ApiRequestError;
    stop();
    // Exactly the one original request: no refresh grant, no replay.
    expect(calls.map((c) => `${c.method} ${c.url.replace('http://localhost:8080', '')}`)).toEqual([
      'PUT /v1/me',
    ]);
    expect(bearer(calls[0]!)).toBe('Bearer imp-access');
    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.code).toBe('users.impersonation_ended');
    expect(err.status).toBe(401);
    expect(events).toEqual([{ type: 'impersonation_ended' }]);
    // The dead token and marker are gone; the administrator's lineage stays.
    expect(getStoredToken()).toBeNull();
    expect(getStoredImpersonating()).toBe(false);
    expect(getStoredRefreshToken()).toBe('admin-refresh');
    expect(getStoredProfileUuid()).toBe('prof-admin');
  });

  test('the restore after the ended impersonation runs as the administrator', async () => {
    await impersonatingTab();
    scriptFetch(() => json(errorBody('unauthenticated'), 401));
    await client.me.update({}).catch(() => {});
    const calls = scriptFetch((call) =>
      call.url.endsWith('/v1/auth/token')
        ? json(tokenResponse({ access_token: 'admin-access-2', refresh_token: 'admin-refresh-2' }))
        : bearer(call) === 'Bearer admin-access-2'
          ? json({ session: null, profile: null, profiles: [] })
          : json(errorBody('unauthenticated'), 401),
    );
    await client.session.get();
    expect(calls[0]!.headers['Authorization']).toBeUndefined();
    expect(calls[1]!.body).toEqual({
      grant_type: 'refresh_token',
      refresh_token: 'admin-refresh',
      profile_uuid: 'prof-admin',
    });
    expect(getStoredToken()).toBe('admin-access-2');
    expect(getStoredImpersonating()).toBe(false);
  });

  test('a request sent before another call restored the administrator is failed, not replayed, and the new token is kept', async () => {
    await impersonatingTab();
    const calls = scriptFetch(async () => {
      // While this request is in flight, another call lands the administrator token.
      setStoredToken('admin-access-2');
      setStoredImpersonating(false);
      return json(errorBody('unauthenticated'), 401);
    });
    const { events, stop } = collectEvents();
    const err = (await client.me.update({}).catch((e: unknown) => e)) as ApiRequestError;
    stop();
    expect(calls).toHaveLength(1);
    expect(err.code).toBe('users.impersonation_ended');
    expect(events).toEqual([]);
    expect(getStoredToken()).toBe('admin-access-2');
  });

  test('a non-impersonating 401 still refreshes and replays', async () => {
    seedTab('old-access', 'old-refresh', 'prof-a');
    const calls = scriptFetch((call) => {
      if (call.url.endsWith('/v1/auth/token')) {
        return json(tokenResponse({ access_token: 'new-access', refresh_token: 'new-refresh' }));
      }
      return bearer(call) === 'Bearer new-access' ? json({}) : json(errorBody('unauthenticated'), 401);
    });
    await client.me.update({});
    expect(calls.map((c) => c.method)).toEqual(['PUT', 'POST', 'PUT']);
  });

  test('a reload that finds an impersonation session marks the tab, so its 401 is not replayed', async () => {
    seedTab('imp-access', 'admin-refresh', 'prof-admin');
    scriptFetch(() =>
      json({
        session: { uuid: 'imp-sess', acr: 1, amr: ['pwd'], auth_time: 'x', impersonating: true },
        profile: PROFILE_A,
        profiles: [PROFILE_A],
      }),
    );
    await client.session.get();
    expect(getStoredImpersonating()).toBe(true);
  });

  test('ending an expired impersonation does not replay the DELETE, then restores the administrator', async () => {
    await impersonatingTab();
    const calls = scriptFetch((call) =>
      call.method === 'DELETE'
        ? json(errorBody('unauthenticated'), 401)
        : json(tokenResponse({ access_token: 'admin-access-2', refresh_token: 'admin-refresh-2' })),
    );
    await client.impersonations.end();
    expect(calls.map((c) => `${c.method} ${c.url.replace('http://localhost:8080', '')}`)).toEqual([
      'DELETE /v1/impersonations/current',
      'POST /v1/auth/token',
    ]);
    // The DELETE went out once, under the impersonation token only.
    expect(bearer(calls[0]!)).toBe('Bearer imp-access');
    expect(getStoredToken()).toBe('admin-access-2');
    expect(getStoredImpersonating()).toBe(false);
  });

  test('ending with a non-401 failure still reports it', async () => {
    await impersonatingTab();
    scriptFetch(() => json(errorBody('boom'), 500));
    const err = (await client.impersonations.end().catch((e: unknown) => e)) as ApiRequestError;
    expect(err.status).toBe(500);
    expect(getStoredToken()).toBe('imp-access');
  });
});

// ─── Step-up retry ───────────────────────────────────────────────────────────

describe('step-up retry', () => {
  const STEP_UP_BODY = {
    action: { code: 'users.step_up_required', message: 'Step up.', path: '/account' },
  };

  /** 409 until the request carries `fresh-access`. */
  function needsStepUp(): RecordedCall[] {
    return scriptFetch((call) =>
      bearer(call) === 'Bearer fresh-access' ? json({ uuid: 'u1' }) : json(STEP_UP_BODY, 409),
    );
  }

  test('with a handler: the handler runs once and the request replays once with the fresh token', async () => {
    seedTab();
    const calls = needsStepUp();
    const seen: ApiActionRequiredError[] = [];
    setStepUpHandler(async ({ error }) => {
      seen.push(error);
      return 'fresh-access';
    });
    const result = await client.me.update({ given_name: 'Zed' });
    expect(result).toEqual({ uuid: 'u1' } as never);
    expect(seen.length).toBe(1);
    expect(seen[0]).toBeInstanceOf(ApiActionRequiredError);
    expect(seen[0]!.code).toBe('users.step_up_required');
    expect(calls.length).toBe(2);
    expect(calls[1]!.method).toBe('PUT');
    expect(calls[1]!.body).toEqual({ given_name: 'Zed' });
    expect(bearer(calls[1]!)).toBe('Bearer fresh-access');
    expect(getStoredToken()).toBe('fresh-access');
  });

  test('without a handler the original ApiActionRequiredError propagates', async () => {
    seedTab();
    const calls = needsStepUp();
    const err = await client.me.update({ given_name: 'Zed' }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect((err as ApiActionRequiredError).code).toBe('users.step_up_required');
    expect(calls.length).toBe(1);
  });

  test('a handler that rejects (user cancelled) leaves the original error and does not replay', async () => {
    seedTab();
    const calls = needsStepUp();
    setStepUpHandler(async () => {
      throw new Error('cancelled');
    });
    const err = await client.me.update({ given_name: 'Zed' }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(calls.length).toBe(1);
  });

  test('a second 409 after the replay propagates instead of prompting again', async () => {
    seedTab();
    const calls = scriptFetch(() => json(STEP_UP_BODY, 409));
    let prompts = 0;
    setStepUpHandler(async () => {
      prompts += 1;
      return 'fresh-access';
    });
    const err = await client.me.update({ given_name: 'Zed' }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(prompts).toBe(1);
    expect(calls.length).toBe(2);
  });

  test('concurrent 409s share one handler call', async () => {
    seedTab();
    needsStepUp();
    let prompts = 0;
    setStepUpHandler(async () => {
      prompts += 1;
      await new Promise((r) => setTimeout(r, 10));
      return 'fresh-access';
    });
    await Promise.all([
      client.me.update({ given_name: 'A' }),
      client.me.identifiers.delete('i1'),
    ]);
    expect(prompts).toBe(1);
  });

  test('a registered handler is not consulted for the step-up routes themselves', async () => {
    seedTab();
    scriptFetch(() => json(STEP_UP_BODY, 409));
    let prompts = 0;
    setStepUpHandler(async () => {
      prompts += 1;
      return 'x';
    });
    const err = await client.session.stepUp.password('wrong').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(prompts).toBe(0);
  });

  test('other action codes never reach the handler', async () => {
    seedTab();
    scriptFetch(() =>
      json({ action: { code: 'users.email_unverified', message: 'Verify.', path: '/verify-email' } }, 403),
    );
    let prompts = 0;
    setStepUpHandler(async () => {
      prompts += 1;
      return 'x';
    });
    const err = await client.me.update({ given_name: 'Zed' }).catch((e: unknown) => e);
    expect((err as ApiActionRequiredError).code).toBe('users.email_unverified');
    expect(prompts).toBe(0);
  });

  test('the unregister function clears only its own registration', async () => {
    let used = '';
    const unregisterA = setStepUpHandler(async () => {
      used = 'a';
      return 'fresh-access';
    });
    setStepUpHandler(async () => {
      used = 'b';
      return 'fresh-access';
    });
    // A stale cleanup from the replaced registration must leave b in place.
    unregisterA();
    seedTab();
    needsStepUp();
    await client.me.update({ given_name: 'Zed' });
    expect(used).toBe('b');
  });

  test('step-up calls store the fresh token themselves', async () => {
    seedTab('old-access', 'old-refresh', 'prof-a');
    scriptFetch(() =>
      json(tokenResponse({ access_token: 'fresh-access', refresh_token: null })),
    );
    await client.session.stepUp.password('pw');
    expect(getStoredToken()).toBe('fresh-access');
    expect(getStoredRefreshToken()).toBe('old-refresh');
  });
});

// ─── Fork and handoff calls ──────────────────────────────────────────────────

describe('fork call', () => {
  test('sends the sibling token, stores the new lineage, and never signs out on failure', async () => {
    const calls = scriptFetch(() =>
      json(tokenResponse({ access_token: 'fork-access', refresh_token: 'fork-refresh' })),
    );
    await client.session.fork({ accessToken: 'sibling-access' });
    expect(calls[0]!.url).toBe('http://localhost:8080/v1/session/fork');
    expect(bearer(calls[0]!)).toBe('Bearer sibling-access');
    expect(getStoredToken()).toBe('fork-access');
    expect(getStoredRefreshToken()).toBe('fork-refresh');
  });

  test('a rejected sibling token surfaces as an error without touching this tab or redirecting', async () => {
    const unauth: UnauthenticatedContext[] = [];
    configureUsersApi({ onUnauthenticated: (c) => unauth.push(c) });
    seedTab('mine', 'mine-refresh', 'prof-a');
    scriptFetch(() => json(errorBody('unauthorized'), 401));
    const err = (await client.session.fork({ accessToken: 'stale' }).catch((e: unknown) => e)) as ApiRequestError;
    expect(err.status).toBe(401);
    expect(getStoredToken()).toBe('mine');
    expect(unauth).toEqual([]);
  });
});

describe('handoff verifier and challenge', () => {
  test('oidcStart stores a verifier and sends only its S256 challenge', async () => {
    const url = await client.auth.oidcStart('google', { returnPath: '/open/3' });
    const verifier = getStoredHandoffVerifier();
    expect(verifier).not.toBeNull();
    expect(verifier).toMatch(/^[A-Za-z0-9_-]{43}$/);
    const parsed = new URL(url);
    expect(parsed.origin + parsed.pathname).toBe('http://localhost:8080/v1/auth/oidc/google/start');
    expect(parsed.searchParams.get('handoff_challenge')).toBe(await computeHandoffChallenge(verifier!));
    expect(parsed.searchParams.get('handoff_challenge')).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(parsed.searchParams.get('return')).toBe('/open/3');
    // The verifier itself must never appear in the URL.
    expect(url).not.toContain(verifier!);
  });

  test('oidcStart refuses an unsafe return path before storing a verifier', async () => {
    await expect(client.auth.oidcStart('google', { returnPath: 'https://evil.test' })).rejects.toBeInstanceOf(TypeError);
    expect(getStoredHandoffVerifier()).toBeNull();
  });

  test('handoff POSTs the code and verifier and stores the session', async () => {
    const calls = scriptFetch(() => json(tokenResponse()));
    await client.auth.handoff('code-1', 'v'.repeat(43));
    expect(calls[0]!.url).toBe('http://localhost:8080/v1/auth/handoff');
    expect(calls[0]!.body).toEqual({ code: 'code-1', verifier: 'v'.repeat(43) });
    expect(getStoredToken()).toBe('access-1');
  });
});

// ─── Route table ─────────────────────────────────────────────────────────────

describe('client methods hit the final routes', () => {
  const cases: [string, (c: typeof client) => Promise<unknown>, string, unknown?][] = [
    ['auth.anonymous', (c) => c.auth.anonymous({ device_id: 'd1' }), 'POST /v1/auth/anonymous', { device_id: 'd1' }],
    ['auth.requestEmailCode', (c) => c.auth.requestEmailCode({ email: 'a@b.co' }), 'POST /v1/auth/email-code/request', { email: 'a@b.co' }],
    ['auth.verifyEmailCode', (c) => c.auth.verifyEmailCode({ email: 'a@b.co', code: '1' }), 'POST /v1/auth/email-code/verify', { email: 'a@b.co', code: '1' }],
    ['auth.forgotPassword', (c) => c.auth.forgotPassword({ email: 'a@b.co' }), 'POST /v1/auth/password-reset/request', { email: 'a@b.co' }],
    ['auth.resetPassword', (c) => c.auth.resetPassword({ token: 't', password: 'p' }), 'POST /v1/auth/password-reset/confirm', { token: 't', password: 'p' }],
    ['session.get', (c) => c.session.get(), 'GET /v1/session'],
    ['session.stepUp.requestEmailCode', (c) => c.session.stepUp.requestEmailCode(), 'POST /v1/session/step-up/email-code/request', {}],
    ['session.stepUp.verifyEmailCode', (c) => c.session.stepUp.verifyEmailCode('123', 'a@b.co'), 'POST /v1/session/step-up/email-code/verify', { code: '123', email: 'a@b.co' }],
    ['session.logout', (c) => c.session.logout(), 'POST /v1/session/logout'],
    ['me.get', (c) => c.me.get(), 'GET /v1/me'],
    ['me.update', (c) => c.me.update({ default_profile_uuid: null }), 'PUT /v1/me', { default_profile_uuid: null }],
    ['me.profiles.list', (c) => c.me.profiles.list(), 'GET /v1/me/profiles'],
    ['me.profiles.accept', (c) => c.me.profiles.accept('p 1'), 'POST /v1/me/profiles/p%201/accept'],
    ['me.profiles.decline', (c) => c.me.profiles.decline('p1'), 'POST /v1/me/profiles/p1/decline'],
    ['me.profiles.leave', (c) => c.me.profiles.leave('p1'), 'POST /v1/me/profiles/p1/leave'],
    ['me.identifiers.list', (c) => c.me.identifiers.list(), 'GET /v1/me/identifiers'],
    ['me.identifiers.add', (c) => c.me.identifiers.add({ value: 'x@y.co' }), 'POST /v1/me/identifiers', { value: 'x@y.co' }],
    ['me.identifiers.verify', (c) => c.me.identifiers.verify('i1', '123'), 'POST /v1/me/identifiers/i1/verify', { code: '123' }],
    ['me.identifiers.resendVerification', (c) => c.me.identifiers.resendVerification('i1'), 'POST /v1/me/identifiers/i1/verification'],
    ['me.identifiers.setPrimary', (c) => c.me.identifiers.setPrimary('i1'), 'PUT /v1/me/identifiers/i1/primary'],
    ['me.identifiers.delete', (c) => c.me.identifiers.delete('i1'), 'DELETE /v1/me/identifiers/i1'],
    ['me.credentials.list', (c) => c.me.credentials.list(), 'GET /v1/me/credentials'],
    ['me.credentials.addPassword', (c) => c.me.credentials.addPassword({ password: 'p' }), 'POST /v1/me/credentials/password', { password: 'p' }],
    ['me.credentials.addSshKey', (c) => c.me.credentials.addSshKey({ public_key: 'ssh-ed25519 AAA' }), 'POST /v1/me/credentials/ssh-keys', { public_key: 'ssh-ed25519 AAA' }],
    ['me.credentials.patch', (c) => c.me.credentials.patch('c1', { label: 'L', default_profile_uuid: null }), 'PATCH /v1/me/credentials/c1', { label: 'L', default_profile_uuid: null }],
    ['me.credentials.delete', (c) => c.me.credentials.delete('c1'), 'DELETE /v1/me/credentials/c1'],
    ['me.sessions.list', (c) => c.me.sessions.list(), 'GET /v1/me/sessions'],
    ['me.sessions.revoke', (c) => c.me.sessions.revoke('s1'), 'DELETE /v1/me/sessions/s1'],
    ['me.upgrade', (c) => c.me.upgrade({ email: 'a@b.co', password: 'p', given_name: 'A', family_name: 'B' }), 'POST /v1/me/upgrade', { email: 'a@b.co', password: 'p', given_name: 'A', family_name: 'B' }],
    ['impersonations.renew', (c) => c.impersonations.renew(), 'POST /v1/impersonations/current/renew'],
    ['bootstrap.status', (c) => c.bootstrap.status(), 'GET /v1/bootstrap/status'],
  ];

  for (const [name, call, expected, body] of cases) {
    test(name, async () => {
      seedTab();
      const calls = scriptFetch(() => json(name === 'auth.anonymous' || name.includes('renew') ? tokenResponse() : {}));
      await call(client);
      const first = calls[0]!;
      expect(`${first.method} ${first.url.replace('http://localhost:8080', '')}`).toBe(expected);
      if (body !== undefined) expect(first.body).toEqual(body);
    });
  }

  test('bootstrap.bootstrap sends X-Setup-Token and the body, and stores the session', async () => {
    const calls = scriptFetch(() => json(tokenResponse(), 201));
    await client.bootstrap.bootstrap(
      {
        operator: { kind: 'natural_person' },
        admin: { email: 'a@b.co', password: 'pw-pw-pw-pw-12', given_name: 'A', family_name: 'B' },
      },
      'setup-123',
    );
    expect(calls[0]!.url).toBe('http://localhost:8080/v1/bootstrap');
    expect(calls[0]!.headers['X-Setup-Token']).toBe('setup-123');
    expect(getStoredToken()).toBe('access-1');
  });

  test('removed route families are gone from the client', () => {
    const surface = client as unknown as Record<string, unknown>;
    for (const removed of ['self', 'userAccounts', 'sshKeys', 'stepUp', 'apps']) {
      expect(surface[removed]).toBeUndefined();
    }
  });
});
