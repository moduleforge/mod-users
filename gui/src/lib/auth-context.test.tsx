import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { act, render, waitFor } from '@testing-library/react';
import React from 'react';
import {
  ApiActionRequiredError,
  ApiRequestError,
  api,
  setStepUpHandler,
  type StepUpHandler,
} from './api';
import {
  AuthProvider,
  useAuth,
  useOptionalAuth,
  useStepUpHandler,
  type AuthContextValue,
} from './auth-context';
import {
  configureUsersApi,
  getStoredHandoffVerifier,
  getStoredProfileUuid,
  getStoredRefreshToken,
  getStoredToken,
  resetUsersApiConfig,
  setStoredHandoffVerifier,
  setStoredProfileUuid,
  setStoredRefreshToken,
  setStoredToken,
} from './config';
import { FakeBroadcastChannel } from '../test-support/fake-broadcast-channel';
import {
  PROFILE_A,
  PROFILE_B,
  tokenResponse,
  USER_VIEW,
} from '../test-support/users-fixtures';

const originalFetch = globalThis.fetch;
const realBroadcastChannel = (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel;

beforeEach(() => {
  // No channel by default: a tab with no tokens then settles immediately
  // instead of waiting out the fork timeout. Tests that exercise the fork
  // install the fake bus.
  (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = undefined;
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = realBroadcastChannel;
  FakeBroadcastChannel.reset();
  resetUsersApiConfig();
  setStepUpHandler(null);
  sessionStorage.clear();
});

// ─── Harness ─────────────────────────────────────────────────────────────────

interface Call {
  method: string;
  path: string;
  body: unknown;
  authorization: string | undefined;
  credentials: RequestCredentials | undefined;
}

type RouteReply = Response | object | undefined;
type Route = (call: Call, index: number) => RouteReply | Promise<RouteReply>;

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

const errorReply = (status: number, code: string, details?: unknown): Response =>
  jsonResponse({ error: { code, message: `${code} message`, ...(details ? { details } : {}) } }, status);

/** Scripts fetch by "METHOD /path"; unscripted routes answer 404. */
function mockApi(routes: Record<string, Route>): Call[] {
  const calls: Call[] = [];
  globalThis.fetch = (async (url: string, init: RequestInit = {}) => {
    const path = String(url).replace('http://localhost:8080', '');
    const headers = (init.headers ?? {}) as Record<string, string>;
    const call: Call = {
      method: init.method ?? 'GET',
      path,
      body: typeof init.body === 'string' ? JSON.parse(init.body) : undefined,
      authorization: headers['Authorization'],
      credentials: init.credentials,
    };
    calls.push(call);
    const route = routes[`${call.method} ${path}`];
    if (!route) return errorReply(404, 'not_found');
    const reply = await route(call, calls.length - 1);
    if (reply instanceof Response) return reply;
    if (reply === undefined) return new Response(null, { status: 204 });
    return jsonResponse(reply);
  }) as unknown as typeof fetch;
  return calls;
}

const SESSION_INFO = {
  session: tokenResponse().session,
  profile: PROFILE_A,
  profiles: [PROFILE_A, PROFILE_B],
};

interface Mounted {
  auth: () => AuthContextValue;
  navigations: string[];
  unmount: () => void;
}

function mount(
  props: Partial<React.ComponentProps<typeof AuthProvider>> = {},
  extra: React.ReactNode = null,
): Mounted {
  let latest: AuthContextValue | null = null;
  const navigations: string[] = [];
  function Capture() {
    latest = useAuth();
    return null;
  }
  const view = render(
    <AuthProvider onNavigate={(p) => navigations.push(p)} {...props}>
      <Capture />
      {extra}
    </AuthProvider>,
  );
  return {
    auth: () => {
      if (!latest) throw new Error('provider has not rendered');
      return latest;
    },
    navigations,
    unmount: view.unmount,
  };
}

async function settled(m: Mounted): Promise<void> {
  await waitFor(() => expect(m.auth().isLoading).toBe(false));
}

function seedTab(access: string | null = 'old-access'): void {
  if (access) setStoredToken(access);
  setStoredRefreshToken('old-refresh');
  setStoredProfileUuid('prof-a');
}

// ─── Mount: reload and new tab ───────────────────────────────────────────────

describe('mount', () => {
  test('a tab with no tokens and no sibling channel settles signed out without any request', async () => {
    const calls = mockApi({});
    const m = mount();
    await settled(m);
    expect(m.auth().token).toBeNull();
    expect(m.auth().user).toBeNull();
    expect(m.auth().session).toBeNull();
    expect(calls).toEqual([]);
  });

  test('a reload hydrates session, profile, profiles and user with Bearer and credentials omit', async () => {
    seedTab();
    const calls = mockApi({
      'GET /v1/session': () => SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
    });
    const m = mount();
    await settled(m);
    expect(m.auth().token).toBe('old-access');
    expect(m.auth().profile?.uuid).toBe('prof-a');
    expect(m.auth().profiles.map((p) => p.uuid)).toEqual(['prof-a', 'prof-b']);
    expect(m.auth().session?.uuid).toBe('sess-1');
    expect(m.auth().user?.email).toBe('ada@example.test');
    expect(m.auth().emailVerified).toBe(true);
    expect(m.auth().impersonating).toBe(false);
    for (const call of calls) {
      expect(call.authorization).toBe('Bearer old-access');
      expect(call.credentials).toBe('omit');
    }
  });

  test('an expired access token refreshes through the lineage during hydration', async () => {
    seedTab('expired');
    const calls = mockApi({
      'GET /v1/session': (call) =>
        call.authorization === 'Bearer access-2'
          ? SESSION_INFO
          : errorReply(401, 'unauthorized'),
      'POST /v1/auth/token': () => tokenResponse({ access_token: 'access-2', refresh_token: 'refresh-2' }),
      'GET /v1/me': () => USER_VIEW,
    });
    const m = mount();
    await settled(m);
    expect(m.auth().token).toBe('access-2');
    expect(getStoredRefreshToken()).toBe('refresh-2');
    expect(calls.filter((c) => c.path === '/v1/auth/token').length).toBe(1);
    // The user record is loaded once, not once per path that learned of the session.
    await waitFor(() => expect(m.auth().user).not.toBeNull());
    expect(calls.filter((c) => c.path === '/v1/me').length).toBe(1);
  });

  test('an action-required answer navigates and keeps the tokens', async () => {
    seedTab();
    mockApi({
      'GET /v1/session': () =>
        jsonResponse(
          { action: { code: 'users.email_unverified', message: 'Verify.', path: '/verify-email' } },
          403,
        ),
    });
    const m = mount();
    await settled(m);
    expect(m.navigations).toEqual(['/verify-email']);
    expect(getStoredToken()).toBe('old-access');
  });

  test('does not navigate to the page the action points at when already there', async () => {
    seedTab();
    const real = Object.getOwnPropertyDescriptor(window, 'location');
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { pathname: '/verify-email', search: '', origin: 'http://app.test', href: 'http://app.test/verify-email' },
    });
    try {
      mockApi({
        'GET /v1/session': () =>
          jsonResponse(
            { action: { code: 'users.email_unverified', message: 'Verify.', path: '/verify-email' } },
            403,
          ),
      });
      const m = mount();
      await settled(m);
      expect(m.navigations).toEqual([]);
    } finally {
      if (real) Object.defineProperty(window, 'location', real);
    }
  });

  test('a non-auth client failure drops the unusable stored session', async () => {
    seedTab();
    mockApi({ 'GET /v1/session': () => errorReply(400, 'bad_request') });
    const m = mount();
    await settled(m);
    expect(m.auth().token).toBeNull();
    expect(getStoredToken()).toBeNull();
    expect(getStoredRefreshToken()).toBeNull();
  });

  test('a network failure on reload keeps the tokens and exposes a retryable error', async () => {
    seedTab();
    globalThis.fetch = (async () => {
      throw new Error('offline');
    }) as unknown as typeof fetch;
    const m = mount();
    await settled(m);
    expect(getStoredRefreshToken()).toBe('old-refresh');
    expect(getStoredToken()).toBe('old-access');
    expect(m.auth().sessionError?.status).toBe(0);
    // Back online: the retry establishes the session and clears the error.
    mockApi({
      'GET /v1/session': () => SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
    });
    await act(async () => {
      await m.auth().retrySession();
    });
    expect(m.auth().sessionError).toBeNull();
    await waitFor(() => expect(m.auth().user).not.toBeNull());
  });

  test('a 503 on reload keeps the stored refresh token', async () => {
    seedTab();
    mockApi({ 'GET /v1/session': () => errorReply(503, 'unavailable') });
    const m = mount();
    await settled(m);
    expect(getStoredRefreshToken()).toBe('old-refresh');
    expect(m.auth().sessionError?.status).toBe(503);
  });

  test('a revoked session on reload signs the tab out and runs the unauthenticated handler once', async () => {
    seedTab();
    const unauth: unknown[] = [];
    configureUsersApi({ onUnauthenticated: (c) => unauth.push(c) });
    mockApi({ 'GET /v1/session': () => errorReply(401, 'users.session_revoked') });
    const m = mount();
    await settled(m);
    expect(m.auth().token).toBeNull();
    expect(getStoredRefreshToken()).toBeNull();
    expect(unauth.length).toBe(1);
  });

  test('a new tab forks a lineage from a sibling tab over the channel', async () => {
    (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = FakeBroadcastChannel;
    const calls = mockApi({
      'POST /v1/session/fork': () => tokenResponse({ access_token: 'fork-access', refresh_token: 'fork-refresh' }),
      'GET /v1/me': () => USER_VIEW,
    });
    // The sibling: another provider with a session answers fork requests.
    const sibling = (await import('./fork')).startForkResponder(() => 'sibling-access');
    const m = mount();
    await settled(m);
    sibling();
    expect(calls[0]!.path).toBe('/v1/session/fork');
    expect(calls[0]!.authorization).toBe('Bearer sibling-access');
    expect(m.auth().token).toBe('fork-access');
    expect(getStoredRefreshToken()).toBe('fork-refresh');
    expect(m.auth().profile?.uuid).toBe('prof-a');
    await waitFor(() => expect(m.auth().user).not.toBeNull());
  });

  test('a new tab nobody answers is signed out after the fork timeout', async () => {
    (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = FakeBroadcastChannel;
    configureUsersApi({ forkTimeoutMs: 40 });
    const calls = mockApi({});
    const m = mount();
    await settled(m);
    expect(m.auth().token).toBeNull();
    expect(calls).toEqual([]);
  });

  test('a signed-in tab answers siblings with its access token but never offers an impersonation', async () => {
    (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = FakeBroadcastChannel;
    seedTab();
    mockApi({
      'GET /v1/session': () => ({
        ...SESSION_INFO,
        session: { ...tokenResponse().session!, impersonating: true },
      }),
      'GET /v1/me': () => USER_VIEW,
    });
    const m = mount();
    await settled(m);
    expect(m.auth().impersonating).toBe(true);

    const asker = new FakeBroadcastChannel('moduleforge-users-gui:fork:auth_token');
    const heard: unknown[] = [];
    asker.onmessage = (e) => heard.push(e.data);
    asker.postMessage({ kind: 'fork_request', id: 'r1' });
    await new Promise((r) => setTimeout(r, 20));
    expect(heard).toEqual([]);
    asker.close();
  });
});

// ─── Sign-in, selection, switch ──────────────────────────────────────────────

describe('login and profile selection', () => {
  test('login lands on the profile and loads the user', async () => {
    const m = mount();
    await settled(m);
    mockApi({
      'POST /v1/auth/login': () => tokenResponse(),
      'GET /v1/me': () => USER_VIEW,
    });
    await act(async () => {
      await m.auth().login('ada@example.test', 'pw');
    });
    await waitFor(() => expect(m.auth().user).not.toBeNull());
    expect(m.auth().token).toBe('access-1');
    expect(m.auth().profile?.uuid).toBe('prof-a');
    expect(m.auth().selectionRequired).toBe(false);
    expect(m.auth().session?.uuid).toBe('sess-1');
  });

  test('a child gated on the user does not see unauthenticated while GET /v1/me is pending', async () => {
    const m = mount();
    await settled(m);
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    mockApi({
      'POST /v1/auth/login': () => tokenResponse(),
      'GET /v1/me': async () => {
        await gate;
        return USER_VIEW;
      },
    });
    await act(async () => {
      await m.auth().login('ada@example.test', 'pw');
    });
    // Token committed, user not yet loaded: must read as loading.
    expect(m.auth().token).toBe('access-1');
    expect(m.auth().user).toBeNull();
    expect(m.auth().isLoading).toBe(true);
    await act(async () => {
      release();
    });
    await waitFor(() => expect(m.auth().user).not.toBeNull());
    expect(m.auth().isLoading).toBe(false);
  });

  test('a forked tab reads as loading until its delayed user record arrives', async () => {
    (globalThis as { BroadcastChannel?: unknown }).BroadcastChannel = FakeBroadcastChannel;
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    mockApi({
      'POST /v1/session/fork': () => tokenResponse({ access_token: 'fork-access', refresh_token: 'fork-refresh' }),
      'GET /v1/me': async () => {
        await gate;
        return USER_VIEW;
      },
    });
    const sibling = (await import('./fork')).startForkResponder(() => 'sibling-access');
    const m = mount();
    await waitFor(() => expect(m.auth().token).toBe('fork-access'));
    sibling();
    expect(m.auth().user).toBeNull();
    expect(m.auth().isLoading).toBe(true);
    await act(async () => {
      release();
    });
    await waitFor(() => expect(m.auth().user).not.toBeNull());
    expect(m.auth().isLoading).toBe(false);
  });

  test('selection_required flow: no access token until selectProfile makes the refresh grant', async () => {
    const m = mount();
    await settled(m);
    const calls = mockApi({
      'POST /v1/auth/login': () =>
        tokenResponse({
          access_token: null,
          profile: null,
          selection_required: true,
          refresh_token: 'lineage-1',
          profiles: [PROFILE_A, PROFILE_B],
        }),
      'POST /v1/auth/token': () =>
        tokenResponse({ access_token: 'chosen-access', refresh_token: 'lineage-2', profile: PROFILE_B }),
      'GET /v1/me': () => USER_VIEW,
    });
    await act(async () => {
      await m.auth().login('ada@example.test', 'pw');
    });
    expect(m.auth().selectionRequired).toBe(true);
    expect(m.auth().token).toBeNull();
    expect(m.auth().profile).toBeNull();
    expect(m.auth().profiles.length).toBe(2);
    // No user fetch without an access token.
    expect(calls.some((c) => c.path === '/v1/me')).toBe(false);

    await act(async () => {
      await m.auth().selectProfile('prof-b');
    });
    const grant = calls.find((c) => c.path === '/v1/auth/token')!;
    expect(grant.body).toEqual({
      grant_type: 'refresh_token',
      refresh_token: 'lineage-1',
      profile_uuid: 'prof-b',
    });
    expect(m.auth().selectionRequired).toBe(false);
    expect(m.auth().token).toBe('chosen-access');
    expect(m.auth().profile?.uuid).toBe('prof-b');
    expect(getStoredRefreshToken()).toBe('lineage-2');
    expect(getStoredProfileUuid()).toBe('prof-b');
  });

  test('selectProfile without a refresh token fails readably and makes no request', async () => {
    const m = mount();
    await settled(m);
    const calls = mockApi({});
    const err = await m.auth().selectProfile('prof-b').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiRequestError);
    expect(calls).toEqual([]);
  });

  test('switchProfile replaces only this tab access token and active profile', async () => {
    seedTab();
    const calls = mockApi({
      'GET /v1/session': () => SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
      'POST /v1/session/switch': () =>
        tokenResponse({ access_token: 'switched', refresh_token: null, profile: PROFILE_B }),
    });
    const m = mount();
    await settled(m);
    await act(async () => {
      await m.auth().switchProfile('prof-b');
    });
    expect(calls.find((c) => c.path === '/v1/session/switch')!.body).toEqual({ profile_uuid: 'prof-b' });
    expect(m.auth().token).toBe('switched');
    expect(m.auth().profile?.uuid).toBe('prof-b');
    expect(getStoredProfileUuid()).toBe('prof-b');
    expect(getStoredRefreshToken()).toBe('old-refresh');
    // Same session: the user record is not refetched for a mere switch.
    expect(calls.filter((c) => c.path === '/v1/me').length).toBe(1);
  });

  test('register establishes the session from the 201 response without a second login call', async () => {
    const m = mount();
    await settled(m);
    const calls = mockApi({
      'POST /v1/auth/register': () => jsonResponse(tokenResponse(), 201),
      'GET /v1/me': () => USER_VIEW,
    });
    await act(async () => {
      await m.auth().register('ada@example.test', 'pw-pw-pw-pw-12', 'Ada', 'Lovelace');
    });
    expect(calls.map((c) => c.path)).toContain('/v1/auth/register');
    expect(calls.some((c) => c.path === '/v1/auth/login')).toBe(false);
    expect(calls[0]!.body).toEqual({
      email: 'ada@example.test',
      password: 'pw-pw-pw-pw-12',
      given_name: 'Ada',
      family_name: 'Lovelace',
    });
    expect(m.auth().token).toBe('access-1');
  });
});

// ─── Locks, revocation, impersonation ────────────────────────────────────────

describe('profile lock and sign-out', () => {
  test('401 users.profile_unavailable populates profileLock with the reason and the cached remedy, keeping the tokens', async () => {
    const locked = {
      ...PROFILE_B,
      available: false,
      lock: { reason: 'credential_not_authorized', remedy: { kind: 'sign_in_with_provider', provider_id: 'google' } },
    };
    seedTab();
    mockApi({
      'GET /v1/session': () => ({ ...SESSION_INFO, profile: PROFILE_B, profiles: [PROFILE_A, locked] }),
      'GET /v1/me': () => USER_VIEW,
      'GET /v1/me/identifiers': () =>
        errorReply(401, 'users.profile_unavailable', [
          { field: 'profile', code: 'credential_not_authorized', message: 'locked' },
        ]),
    });
    setStoredProfileUuid('prof-b');
    const m = mount();
    await settled(m);
    // The tab's active profile is now the locked one.
    await act(async () => {
      await api.me.identifiers.list().catch(() => {});
    });
    expect(m.auth().profileLock).toEqual({
      profileUuid: 'prof-b',
      reason: 'credential_not_authorized',
      remedy: { kind: 'sign_in_with_provider', provider_id: 'google' },
    });
    expect(getStoredToken()).toBe('old-access');
    expect(getStoredRefreshToken()).toBe('old-refresh');
  });

  test('a lock on a profile not in the cached list carries the reason with no remedy, and selecting another profile clears it', async () => {
    seedTab();
    mockApi({
      'GET /v1/session': () => SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
      'GET /v1/me/credentials': () =>
        errorReply(401, 'users.profile_unavailable', [
          { field: 'profile', code: 'profile_suspended', message: 'suspended' },
        ]),
      'POST /v1/auth/token': () => tokenResponse({ access_token: 'other', profile: PROFILE_B }),
    });
    const m = mount();
    await settled(m);
    await act(async () => {
      await api.me.credentials.list().catch(() => {});
    });
    expect(m.auth().profileLock).toEqual({
      profileUuid: 'prof-a',
      reason: 'profile_suspended',
      remedy: null,
    });
    await act(async () => {
      await m.auth().selectProfile('prof-b');
    });
    expect(m.auth().profileLock).toBeNull();
    expect(m.auth().profile?.uuid).toBe('prof-b');
  });

  for (const code of ['users.session_revoked', 'users.refresh_reuse']) {
    test(`${code} on a later request signs the context out`, async () => {
      seedTab();
      const unauth: unknown[] = [];
      configureUsersApi({ onUnauthenticated: (c) => unauth.push(c) });
      mockApi({
        'GET /v1/session': () => SESSION_INFO,
        'GET /v1/me': () => USER_VIEW,
        'GET /v1/me/sessions': () => errorReply(401, code),
      });
      const m = mount();
      await settled(m);
      expect(m.auth().token).toBe('old-access');
      await act(async () => {
        await api.me.sessions.list().catch(() => {});
      });
      expect(m.auth().token).toBeNull();
      expect(m.auth().session).toBeNull();
      expect(m.auth().user).toBeNull();
      expect(getStoredRefreshToken()).toBeNull();
      expect(unauth.length).toBe(1);
    });
  }

  test('401 handling never hard-navigates when a handler is configured', async () => {
    seedTab();
    const assigned: string[] = [];
    const real = Object.getOwnPropertyDescriptor(window, 'location');
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        pathname: '/app',
        search: '',
        origin: 'http://app.test',
        set href(v: string) {
          assigned.push(v);
        },
        get href() {
          return 'http://app.test/app';
        },
      },
    });
    try {
      configureUsersApi({ onUnauthenticated: () => {} });
      mockApi({
        'GET /v1/session': () => SESSION_INFO,
        'GET /v1/me': () => USER_VIEW,
        'GET /v1/me/sessions': () => errorReply(401, 'users.session_revoked'),
      });
      const m = mount();
      await settled(m);
      await act(async () => {
        await api.me.sessions.list().catch(() => {});
      });
      expect(assigned).toEqual([]);
    } finally {
      if (real) Object.defineProperty(window, 'location', real);
    }
  });

  test('impersonating reflects the session and returns to false when the administrator token is restored', async () => {
    seedTab('admin-access');
    const impSession = { ...tokenResponse().session!, uuid: 'imp-sess', impersonating: true };
    mockApi({
      'GET /v1/session': () => SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
      'POST /v1/impersonations': () =>
        jsonResponse(
          tokenResponse({ access_token: 'imp-access', refresh_token: null, session: impSession, profile: PROFILE_B }),
          201,
        ),
      'DELETE /v1/impersonations/current': () => undefined,
      'POST /v1/auth/token': () => tokenResponse({ access_token: 'admin-access-2', refresh_token: 'r2' }),
    });
    const m = mount();
    await settled(m);
    await act(async () => {
      await api.impersonations.start({ profile_uuid: 'prof-b', reason: 'support' });
    });
    expect(m.auth().impersonating).toBe(true);
    expect(m.auth().token).toBe('imp-access');
    // The stored profile stays the administrator's own for the refresh grant.
    expect(getStoredProfileUuid()).toBe('prof-a');

    await act(async () => {
      await api.impersonations.end();
    });
    expect(m.auth().impersonating).toBe(false);
    expect(m.auth().token).toBe('admin-access-2');
  });
});

// ─── Handoff ─────────────────────────────────────────────────────────────────

describe('completeHandoff', () => {
  test('makes a single exchange with the stored verifier, stores the session, and clears the verifier', async () => {
    const m = mount();
    await settled(m);
    const verifier = 'v'.repeat(43);
    setStoredHandoffVerifier(verifier);
    const calls = mockApi({
      'POST /v1/auth/handoff': () => tokenResponse(),
      'GET /v1/me': () => USER_VIEW,
    });
    let first!: Promise<unknown>;
    let second!: Promise<unknown>;
    await act(async () => {
      // A double-invoked effect calls twice with the same code.
      first = m.auth().completeHandoff('code-1');
      second = m.auth().completeHandoff('code-1');
      await Promise.all([first, second]);
    });
    expect(calls.filter((c) => c.path === '/v1/auth/handoff').length).toBe(1);
    expect(calls.find((c) => c.path === '/v1/auth/handoff')!.body).toEqual({ code: 'code-1', verifier });
    expect(getStoredHandoffVerifier()).toBeNull();
    expect(m.auth().token).toBe('access-1');
    expect(m.auth().profile?.uuid).toBe('prof-a');
  });

  test('without a verifier the exchange fails readably and sends nothing', async () => {
    const m = mount();
    await settled(m);
    const calls = mockApi({});
    const err = (await m.auth().completeHandoff('code-2').catch((e: unknown) => e)) as ApiRequestError;
    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.code).toBe('handoff_verifier_missing');
    expect(err.message.length).toBeGreaterThan(20);
    expect(calls).toEqual([]);
  });

  test('a refused exchange still clears the verifier and leaves the tab signed out', async () => {
    const m = mount();
    await settled(m);
    setStoredHandoffVerifier('w'.repeat(43));
    mockApi({ 'POST /v1/auth/handoff': () => errorReply(401, 'users.invalid_credentials') });
    const err = (await m.auth().completeHandoff('code-3').catch((e: unknown) => e)) as ApiRequestError;
    expect(err.code).toBe('users.invalid_credentials');
    expect(getStoredHandoffVerifier()).toBeNull();
    expect(m.auth().token).toBeNull();
  });
});

// ─── Logout ──────────────────────────────────────────────────────────────────

describe('logout', () => {
  test('revokes the session, clears the tab, and navigates to the login path', async () => {
    seedTab();
    const calls = mockApi({
      'GET /v1/session': () => SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
      'POST /v1/session/logout': () => undefined,
    });
    const m = mount({ loginPath: '/signin' });
    await settled(m);
    await act(async () => {
      await m.auth().logout();
    });
    expect(calls.at(-1)!.path).toBe('/v1/session/logout');
    expect(calls.at(-1)!.authorization).toBe('Bearer old-access');
    expect(m.auth().token).toBeNull();
    expect(m.auth().user).toBeNull();
    expect(getStoredToken()).toBeNull();
    expect(getStoredRefreshToken()).toBeNull();
    expect(m.navigations).toEqual(['/signin']);
  });

  test('still signs out locally when the server is unreachable', async () => {
    seedTab();
    mockApi({
      'GET /v1/session': () => SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
    });
    const m = mount();
    await settled(m);
    globalThis.fetch = (async () => {
      throw new Error('offline');
    }) as unknown as typeof fetch;
    await act(async () => {
      await m.auth().logout();
    });
    expect(m.auth().token).toBeNull();
    expect(getStoredToken()).toBeNull();
    expect(m.navigations).toEqual(['/auth/login']);
  });

  test('an expired access token refreshes once, then the logout request revokes the session', async () => {
    seedTab();
    let expired = false;
    const calls = mockApi({
      'GET /v1/session': (call) =>
        expired && call.authorization === 'Bearer old-access'
          ? errorReply(401, 'unauthorized')
          : SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
      'POST /v1/auth/token': () => tokenResponse({ access_token: 'access-2', refresh_token: 'refresh-2' }),
      'POST /v1/session/logout': (call) =>
        call.authorization === 'Bearer access-2' ? undefined : errorReply(401, 'unauthorized'),
    });
    const m = mount();
    await settled(m);
    expired = true;
    await act(async () => {
      await m.auth().logout();
    });
    const paths = calls.map((c) => `${c.method} ${c.path}`);
    const afterMount = paths.slice(paths.indexOf('GET /v1/me') + 1);
    expect(afterMount).toEqual([
      'POST /v1/session/logout',
      'GET /v1/session',
      'POST /v1/auth/token',
      'GET /v1/session',
      'POST /v1/session/logout',
    ]);
    expect(calls.at(-1)!.authorization).toBe('Bearer access-2');
    expect(getStoredRefreshToken()).toBeNull();
    expect(m.auth().token).toBeNull();
  });

  test('an unsafe loginPath falls back to the default login route', async () => {
    seedTab();
    mockApi({
      'GET /v1/session': () => SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
      'POST /v1/session/logout': () => undefined,
    });
    const m = mount({ loginPath: '//evil.test' });
    await settled(m);
    await act(async () => {
      await m.auth().logout();
    });
    expect(m.navigations).toEqual(['/auth/login']);
  });
});

// ─── Step-up ─────────────────────────────────────────────────────────────────

describe('step-up registration', () => {
  const STEP_UP = jsonResponse(
    { action: { code: 'users.step_up_required', message: 'Step up.', path: '/account' } },
    409,
  );

  function stepUpApi(): Call[] {
    return mockApi({
      'GET /v1/session': () => SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
      'PUT /v1/me': (call) =>
        call.authorization === 'Bearer fresh' ? USER_VIEW : STEP_UP.clone(),
    });
  }

  test('the stepUpHandler prop retries the interrupted request once with the fresh token', async () => {
    seedTab();
    const calls = stepUpApi();
    const handler: StepUpHandler = async () => 'fresh';
    const m = mount({ stepUpHandler: handler });
    await settled(m);
    await act(async () => {
      await api.me.update({ given_name: 'Zed' });
    });
    const puts = calls.filter((c) => c.method === 'PUT');
    expect(puts.length).toBe(2);
    expect(puts[1]!.authorization).toBe('Bearer fresh');
  });

  test('without a handler the original ApiActionRequiredError propagates', async () => {
    seedTab();
    stepUpApi();
    const m = mount();
    await settled(m);
    const err = await api.me.update({ given_name: 'Zed' }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiActionRequiredError);
  });

  test('unmounting the provider unregisters the handler', async () => {
    seedTab();
    stepUpApi();
    const m = mount({ stepUpHandler: async () => 'fresh' });
    await settled(m);
    m.unmount();
    const err = await api.me.update({ given_name: 'Zed' }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiActionRequiredError);
  });

  test('useStepUpHandler registers while the component is mounted', async () => {
    seedTab();
    const calls = stepUpApi();
    function Prompt() {
      const handler = React.useCallback<StepUpHandler>(async () => 'fresh', []);
      useStepUpHandler(handler);
      return null;
    }
    const m = mount({}, <Prompt />);
    await settled(m);
    await act(async () => {
      await api.me.update({ given_name: 'Zed' });
    });
    expect(calls.filter((c) => c.method === 'PUT').length).toBe(2);
  });
});

// ─── Hooks ───────────────────────────────────────────────────────────────────

describe('hooks', () => {
  test('useAuth throws outside a provider; useOptionalAuth returns null', () => {
    let optional: AuthContextValue | null | undefined;
    function Optional() {
      optional = useOptionalAuth();
      return null;
    }
    render(<Optional />);
    expect(optional).toBeNull();
    function Required() {
      useAuth();
      return null;
    }
    const spy = console.error;
    console.error = () => {};
    try {
      expect(() => render(<Required />)).toThrow('useAuth must be used within an AuthProvider');
    } finally {
      console.error = spy;
    }
  });
});

describe('accessTokenExpiresAt', () => {
  test('is unknown after a reload and set from expires_in by the next token response', async () => {
    seedTab();
    mockApi({
      'GET /v1/session': () => SESSION_INFO,
      'GET /v1/me': () => USER_VIEW,
      'POST /v1/session/switch': () =>
        tokenResponse({ access_token: 'switched', refresh_token: null, expires_in: 120, profile: PROFILE_B }),
    });
    const m = mount();
    await settled(m);
    // Hydration reads no token response, so no deadline is known.
    expect(m.auth().accessTokenExpiresAt).toBeNull();
    const before = Date.now();
    await act(async () => {
      await m.auth().switchProfile('prof-b');
    });
    const expiresAt = m.auth().accessTokenExpiresAt;
    expect(expiresAt).not.toBeNull();
    expect(expiresAt!).toBeGreaterThanOrEqual(before + 120_000);
    expect(expiresAt!).toBeLessThan(Date.now() + 121_000);
  });
});
