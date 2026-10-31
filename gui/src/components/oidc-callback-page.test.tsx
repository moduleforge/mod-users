import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { jwtFixture } from '../test-support/jwt';
import { cleanup, screen, waitFor } from '@testing-library/react';
import { OidcCallbackPage } from './oidc-callback-page';
import {
  getStoredHandoffVerifier,
  getStoredToken,
  setStoredHandoffVerifier,
  setStoredProfileUuid,
  setStoredRefreshToken,
  setStoredToken,
} from '../lib/config';
import {
  type Call,
  errorReply,
  mockApi,
  mountUnder,
  setUpHarness,
  tearDownHarness,
} from '../test-support/chrome-harness';
import { PROFILE_A, PROFILE_B, tokenResponse, USER_VIEW } from '../test-support/users-fixtures';

const VERIFIER = 'v'.repeat(43);

beforeEach(() => {
  setUpHarness();
  window.location.hash = '';
});

afterEach(() => {
  cleanup();
  tearDownHarness();
  window.location.hash = '';
});

function setHash(hash: string): void {
  // The test DOM has no real URL, so only the fragment is settable.
  window.location.hash = hash;
}

interface Outcome {
  completed: string[];
  errors: string[];
  selections: Array<[string, string]>;
}

async function mountCallback(
  routes: Parameters<typeof mockApi>[0],
  props: { withSelectionHandler?: boolean } = {},
) {
  const outcome: Outcome = { completed: [], errors: [], selections: [] };
  const calls = mockApi({ 'GET /v1/me': () => USER_VIEW, ...routes });
  const mounted = await mountUnder(
    <OidcCallbackPage
      onComplete={(p) => outcome.completed.push(p)}
      onError={(m) => outcome.errors.push(m)}
      onSelectionRequired={
        props.withSelectionHandler
          ? (selectPath, returnPath) => outcome.selections.push([selectPath, returnPath])
          : undefined
      }
    />,
    calls,
    () => true,
  );
  return { ...mounted, outcome };
}

const handoffCalls = (calls: Call[]) =>
  calls.filter((c) => c.path === '/v1/auth/handoff');

describe('OidcCallbackPage handoff', () => {
  test('exchanges the code once with the stored verifier, signs in, and completes with the safe return path', async () => {
    setStoredHandoffVerifier(VERIFIER);
    setHash('#handoff=code-1&return=%2Fopen%2F3');
    const { calls, outcome } = await mountCallback({
      'POST /v1/auth/handoff': () => tokenResponse(),
    });
    await waitFor(() => expect(outcome.completed).toEqual(['/open/3']));
    expect(handoffCalls(calls)).toHaveLength(1);
    expect(handoffCalls(calls)[0]!.body).toEqual({ code: 'code-1', verifier: VERIFIER });
    expect(getStoredToken()).toBe(jwtFixture('access-1'));
    // The verifier is single-use.
    expect(getStoredHandoffVerifier()).toBeNull();
    expect(outcome.errors).toEqual([]);
  });

  test('strips the fragment from the URL', async () => {
    setStoredHandoffVerifier(VERIFIER);
    setHash('#handoff=code-1');
    const { outcome } = await mountCallback({ 'POST /v1/auth/handoff': () => tokenResponse() });
    await waitFor(() => expect(outcome.completed).toHaveLength(1));
    expect(window.location.hash).toBe('');
  });

  test('an unsafe return value falls back to the default path', async () => {
    setStoredHandoffVerifier(VERIFIER);
    setHash('#handoff=code-1&return=%2F%2Fevil.test');
    const { outcome } = await mountCallback({ 'POST /v1/auth/handoff': () => tokenResponse() });
    await waitFor(() => expect(outcome.completed).toEqual(['/']));
  });

  test('selection_required goes to the selection handler with the return path', async () => {
    setStoredHandoffVerifier(VERIFIER);
    setHash('#handoff=code-1&return=%2Fdash');
    const { outcome } = await mountCallback(
      {
        'POST /v1/auth/handoff': () =>
          tokenResponse({
            access_token: null,
            selection_required: true,
            profile: null,
            profiles: [PROFILE_A, PROFILE_B],
          }),
      },
      { withSelectionHandler: true },
    );
    await waitFor(() => expect(outcome.selections).toEqual([['/auth/select-profile', '/dash']]));
    expect(outcome.completed).toEqual([]);
  });

  test('selection_required without a handler hands onComplete the selection route', async () => {
    setStoredHandoffVerifier(VERIFIER);
    setHash('#handoff=code-1&return=%2Fdash');
    const { outcome } = await mountCallback({
      'POST /v1/auth/handoff': () =>
        tokenResponse({ access_token: null, selection_required: true, profile: null }),
    });
    await waitFor(() => expect(outcome.completed).toEqual(['/auth/select-profile']));
  });
});

/** A tab that is already signed in, as at a link or step-up return. */
function signInTab(): Parameters<typeof mockApi>[0] {
  setStoredToken(jwtFixture('old-access'));
  setStoredRefreshToken('old-refresh');
  setStoredProfileUuid('prof-a');
  const base = tokenResponse();
  return {
    'GET /v1/session': () => ({
      session: base.session,
      profile: PROFILE_A,
      profiles: [PROFILE_A],
    }),
  };
}

const tokenCalls = (calls: Call[]) => calls.filter((c) => c.path === '/v1/auth/token');

describe('OidcCallbackPage link and step-up returns', () => {
  test('#linked=1 routes back to a safe return path without a handoff or error', async () => {
    const session = signInTab();
    setHash('#linked=1&return=%2Fsettings');
    const { calls, outcome } = await mountCallback(session);
    await waitFor(() => expect(outcome.completed).toEqual(['/settings']));
    expect(outcome.errors).toEqual([]);
    expect(handoffCalls(calls)).toHaveLength(0);
    // A link does not raise assurance, so no token is minted.
    expect(tokenCalls(calls)).toHaveLength(0);
    expect(window.location.hash).toBe('');
  });

  test('#linked=1 with an unsafe return path falls back to the default', async () => {
    const session = signInTab();
    setHash('#linked=1&return=%2F%2Fevil.test');
    const { outcome } = await mountCallback(session);
    await waitFor(() => expect(outcome.completed).toEqual(['/']));
    expect(outcome.errors).toEqual([]);
  });

  test('#stepped_up=1 takes a fresh access token, then routes to a safe return path', async () => {
    const session = signInTab();
    setHash('#stepped_up=1&return=%2Fadmin%2Fusers');
    const { calls, outcome } = await mountCallback({
      ...session,
      'POST /v1/auth/token': () => tokenResponse({ access_token: jwtFixture('fresh-access') }),
    });
    await waitFor(() => expect(outcome.completed).toEqual(['/admin/users']));
    expect(outcome.errors).toEqual([]);
    expect(tokenCalls(calls)).toHaveLength(1);
    expect(tokenCalls(calls)[0]!.body).toMatchObject({
      grant_type: 'refresh_token',
      refresh_token: 'old-refresh',
      profile_uuid: 'prof-a',
    });
    expect(getStoredToken()).toBe(jwtFixture('fresh-access'));
    expect(handoffCalls(calls)).toHaveLength(0);
  });

  test('#stepped_up=1 joins a hydrate-driven refresh in flight: one grant, no sign-out', async () => {
    const session = signInTab();
    setHash('#stepped_up=1&return=%2Fadmin');
    let sessionCalls = 0;
    const { calls, outcome } = await mountCallback({
      // The first session read answers 401 so hydrate itself starts a refresh.
      'GET /v1/session': (call) => {
        sessionCalls += 1;
        if (sessionCalls === 1) return errorReply(401, 'unauthenticated');
        return (session['GET /v1/session'] as (c: Call) => object)(call);
      },
      'POST /v1/auth/token': async () => {
        // Hold the rotation open long enough that both the hydrate refresh and
        // the step-up callback ask for it while it is in flight.
        await new Promise((resolve) => setTimeout(resolve, 50));
        return tokenResponse({ access_token: jwtFixture('fresh-access') });
      },
    });
    await waitFor(() => expect(outcome.completed).toEqual(['/admin']));
    // WHY: a second grant with the spent refresh token would trip reuse
    // detection and sign the tab out; exactly one POST proves the join.
    expect(tokenCalls(calls)).toHaveLength(1);
    expect(outcome.errors).toEqual([]);
    expect(getStoredToken()).toBe(jwtFixture('fresh-access'));
  });

  test('#stepped_up=1 with an unsafe return path falls back to the default', async () => {
    const session = signInTab();
    setHash('#stepped_up=1&return=https%3A%2F%2Fevil.test');
    const { outcome } = await mountCallback({
      ...session,
      'POST /v1/auth/token': () => tokenResponse({ access_token: jwtFixture('fresh-access') }),
    });
    await waitFor(() => expect(outcome.completed).toEqual(['/']));
    expect(outcome.errors).toEqual([]);
  });

  test('#stepped_up=1 reports an error when the refresh fails', async () => {
    const session = signInTab();
    setHash('#stepped_up=1&return=%2Fadmin');
    const { outcome } = await mountCallback({
      ...session,
      'POST /v1/auth/token': () => errorReply(500, 'internal', 'boom'),
    });
    await waitFor(() => expect(outcome.errors).toHaveLength(1));
    expect(outcome.completed).toEqual([]);
  });
});

describe('OidcCallbackPage failures', () => {
  test.each([
    ['account_exists_sign_in_and_link', /already exists[\s\S]*link this provider/],
    ['no_account', /No account exists/],
    ['authentication_failed', /could not confirm your identity/],
    ['missing_email', /did not share an email/],
  ])('#error=%s shows readable copy and exchanges nothing', async (code, pattern) => {
    setStoredHandoffVerifier(VERIFIER);
    setHash(`#error=${code}`);
    const { calls, outcome } = await mountCallback({});
    await waitFor(() => expect(outcome.errors).toHaveLength(1));
    expect(outcome.errors[0]).toMatch(pattern);
    expect(screen.getByText(pattern)).toBeTruthy();
    expect(handoffCalls(calls)).toHaveLength(0);
    expect(window.location.hash).toBe('');
    expect(outcome.completed).toEqual([]);
  });

  test('an unknown error code gets generic copy and is not echoed', async () => {
    setHash('#error=%3Cscript%3Ealert(1)%3C%2Fscript%3E');
    const { outcome } = await mountCallback({});
    await waitFor(() => expect(outcome.errors).toEqual(['Sign-in failed. Please try again.']));
  });

  test('a missing fragment reports an error and exchanges nothing', async () => {
    const { calls, outcome } = await mountCallback({});
    await waitFor(() => expect(outcome.errors).toHaveLength(1));
    expect(handoffCalls(calls)).toHaveLength(0);
  });

  test('a missing verifier shows the same-browser-tab message and sends nothing', async () => {
    setHash('#handoff=code-1');
    const { calls, outcome } = await mountCallback({
      'POST /v1/auth/handoff': () => tokenResponse(),
    });
    await waitFor(() => expect(outcome.errors).toHaveLength(1));
    expect(outcome.errors[0]).toMatch(/same browser tab/);
    expect(screen.getByText(/same browser tab/)).toBeTruthy();
    expect(handoffCalls(calls)).toHaveLength(0);
    expect(outcome.completed).toEqual([]);
  });

  test('a refused exchange reports a generic failure and keeps the tab signed out', async () => {
    setStoredHandoffVerifier(VERIFIER);
    setHash('#handoff=code-1');
    const { outcome } = await mountCallback({
      'POST /v1/auth/handoff': () => errorReply(401, 'users.invalid_credentials'),
    });
    await waitFor(() => expect(outcome.errors).toHaveLength(1));
    expect(outcome.errors[0]).toMatch(/session could not be established/);
    expect(getStoredToken()).toBeNull();
    expect(getStoredHandoffVerifier()).toBeNull();
  });
});
