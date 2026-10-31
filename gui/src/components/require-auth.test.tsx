import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { screen, waitFor } from '@testing-library/react';
import { RequireAuth } from './require-auth';
import { api } from '../lib/api';
import { PROFILE_A, tokenResponse } from '../test-support/users-fixtures';
import {
  errorReply,
  mockApi,
  mountSignedIn,
  mountUnder,
  setUpHarness,
  tearDownHarness,
} from '../test-support/chrome-harness';
import { setStoredRefreshToken } from '../lib/config';

beforeEach(setUpHarness);
afterEach(tearDownHarness);

describe('RequireAuth', () => {
  test('renders children for a signed-in user', async () => {
    await mountSignedIn(
      <RequireAuth>
        <p>secret</p>
      </RequireAuth>,
      { profiles: [PROFILE_A] },
    );
    await screen.findByText('secret');
  });

  test('calls onUnauthenticated and renders nothing when signed out', async () => {
    mockApi({});
    let called = 0;
    await mountUnder(
      <RequireAuth onUnauthenticated={() => (called += 1)}>
        <p>secret</p>
      </RequireAuth>,
      [],
      () => true,
    );
    await waitFor(() => expect(called).toBeGreaterThan(0));
    expect(screen.queryByText('secret')).toBeNull();
  });

  test('redirects to selection, not sign-in, when a profile must be chosen', async () => {
    setStoredRefreshToken('old-refresh');
    const calls = mockApi({
      'POST /v1/auth/login': () =>
        tokenResponse({ access_token: null, selection_required: true, profile: null }),
    });
    const events: string[] = [];
    const m = await mountUnder(
      <RequireAuth
        onUnauthenticated={() => events.push('unauthenticated')}
        onSelectionRequired={() => events.push('selection')}
      >
        <p>secret</p>
      </RequireAuth>,
      calls,
      () => true,
    );
    // The tab starts signed out (which legitimately reports unauthenticated);
    // only what happens once the sign-in lands on selection is under test.
    events.length = 0;
    await m.auth().login('a@b.test', 'pw');
    await waitFor(() => expect(events).toContain('selection'));
    expect(events).not.toContain('unauthenticated');
    expect(screen.queryByText('secret')).toBeNull();
  });

  test('shows the lock surface instead of the content, without redirecting to sign-in', async () => {
    const events: string[] = [];
    await mountSignedIn(
      <RequireAuth onUnauthenticated={() => events.push('unauthenticated')}>
        <p>secret</p>
      </RequireAuth>,
      {
        profiles: [PROFILE_A],
        routes: { 'GET /v1/me/identifiers': () => errorReply(401, 'users.profile_unavailable') },
      },
    );
    await screen.findByText('secret');
    await api.me.identifiers.list().catch(() => {});
    await screen.findByRole('alert');
    expect(screen.getByText('This profile is unavailable')).toBeTruthy();
    expect(screen.queryByText('secret')).toBeNull();
    expect(events).toEqual([]);
  });
});
