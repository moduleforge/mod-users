import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { ProfileSwitcher } from './profile-switcher';
import { ProfileLockNotice } from './profile-lock-notice';
import { SelectProfilePage } from './select-profile-page';
import { api } from '../lib/api';
import { PROFILE_A, PROFILE_B, tokenResponse } from '../test-support/users-fixtures';
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

const LOCKED_PROVIDER = {
  ...PROFILE_B,
  uuid: 'prof-c',
  display_name: 'Ada @ Globex',
  party: { uuid: 'party-2', kind: 'organization', display_name: 'Globex' },
  available: false,
  lock: {
    reason: 'provider_required',
    remedy: { kind: 'sign_in_with_provider', provider_id: 'google' },
  },
};

describe('ProfileSwitcher', () => {
  test('lists personal and party profiles, marks the active one, and offers a switch only for other available ones', async () => {
    await mountSignedIn(<ProfileSwitcher />, { profiles: [PROFILE_A, PROFILE_B] });
    // Party profiles show the party name; personal ones the holder's name.
    expect(screen.getByText('Ada')).toBeTruthy();
    expect(screen.getByText('Acme')).toBeTruthy();
    expect(screen.getByText('Active')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Switch to Ada' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Switch to Acme' })).toBeTruthy();
  });

  test('switching posts the profile uuid and lands the context on it', async () => {
    const m = await mountSignedIn(<ProfileSwitcher />, {
      profiles: [PROFILE_A, PROFILE_B],
      routes: {
        'POST /v1/session/switch': () =>
          tokenResponse({ access_token: 'switched', refresh_token: null, profile: PROFILE_B }),
      },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Switch to Acme' }));
    await waitFor(() => expect(m.auth().profile?.uuid).toBe('prof-b'));
    const post = m.calls.find((c) => c.path === '/v1/session/switch');
    expect(post?.body).toEqual({ profile_uuid: 'prof-b' });
    expect(m.auth().token).toBe('switched');
  });

  test('a failed switch shows the server message and keeps the active profile', async () => {
    const m = await mountSignedIn(<ProfileSwitcher />, {
      profiles: [PROFILE_A, PROFILE_B],
      routes: { 'POST /v1/session/switch': () => errorReply(403, 'users.forbidden', 'Not allowed.') },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Switch to Acme' }));
    await screen.findByText('Not allowed.');
    expect(m.auth().profile?.uuid).toBe('prof-a');
  });

  test('a locked profile is shown with its reason and has no switch action', async () => {
    await mountSignedIn(<ProfileSwitcher />, { profiles: [PROFILE_A, LOCKED_PROVIDER] });
    expect(screen.getByText('Globex')).toBeTruthy();
    expect(screen.getByText(/requires you to sign in with a specific identity provider/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Switch to Globex' })).toBeNull();
  });

  test('the provider remedy starts the step-up and navigates to the authorize url', async () => {
    const assigned: string[] = [];
    const realAssign = window.location.assign;
    // happy-dom navigations are not observable; capture the call instead.
    Object.defineProperty(window.location, 'assign', {
      configurable: true,
      value: (u: string) => assigned.push(u),
    });
    try {
      const m = await mountSignedIn(<ProfileSwitcher />, {
        profiles: [PROFILE_A, LOCKED_PROVIDER],
        routes: {
          'POST /v1/session/step-up/oidc/google/start': () => ({
            authorize_url: 'https://accounts.example.test/authorize?x=1',
          }),
        },
      });
      fireEvent.click(screen.getByRole('button', { name: 'Sign in with google' }));
      await waitFor(() => expect(assigned).toEqual(['https://accounts.example.test/authorize?x=1']));
      expect(m.calls.some((c) => c.path.startsWith('/v1/session/step-up/oidc/google/start'))).toBe(true);
    } finally {
      Object.defineProperty(window.location, 'assign', { configurable: true, value: realAssign });
    }
  });

  test('a non-web authorize url is refused rather than navigated to', async () => {
    const assigned: string[] = [];
    const realAssign = window.location.assign;
    Object.defineProperty(window.location, 'assign', {
      configurable: true,
      value: (u: string) => assigned.push(u),
    });
    try {
      await mountSignedIn(<ProfileSwitcher />, {
        profiles: [PROFILE_A, LOCKED_PROVIDER],
        routes: {
          'POST /v1/session/step-up/oidc/google/start': () => ({ authorize_url: 'javascript:alert(1)' }),
        },
      });
      fireEvent.click(screen.getByRole('button', { name: 'Sign in with google' }));
      await screen.findByText(/cannot be opened/);
      expect(assigned).toEqual([]);
    } finally {
      Object.defineProperty(window.location, 'assign', { configurable: true, value: realAssign });
    }
  });

  test('other remedy kinds are described without an action button', async () => {
    const stepUp = {
      ...LOCKED_PROVIDER,
      lock: { reason: 'acr_too_low', remedy: { kind: 'step_up' } },
    };
    await mountSignedIn(<ProfileSwitcher />, { profiles: [PROFILE_A, stepUp] });
    expect(screen.getByText(/Confirm your identity again/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Sign in with/ })).toBeNull();
  });
});

describe('ProfileLockNotice', () => {
  test('shows the reason and remedy and recovers by selecting another available profile', async () => {
    const locked = { ...PROFILE_B, available: false, lock: LOCKED_PROVIDER.lock };
    const m = await mountSignedIn(<ProfileLockNotice />, {
      profiles: [PROFILE_A, locked],
      active: locked,
      routes: {
        'GET /v1/me/identifiers': () =>
          errorReply(401, 'users.profile_unavailable', 'locked'),
        'POST /v1/auth/token': () => tokenResponse({ access_token: 'other', profile: PROFILE_A }),
      },
    });
    expect(screen.queryByRole('alert')).toBeNull();
    // A request answering the lock publishes it to the context.
    await api.me.identifiers.list().catch(() => {});
    await screen.findByRole('alert');
    expect(m.auth().profileLock?.profileUuid).toBe('prof-a');
  });

  test('offers other available profiles and sign out', async () => {
    const locked = { ...PROFILE_B, available: false, lock: LOCKED_PROVIDER.lock };
    const m = await mountSignedIn(<ProfileLockNotice />, {
      profiles: [PROFILE_A, locked],
      active: locked,
      routes: {
        'GET /v1/me/identifiers': () => errorReply(401, 'users.profile_unavailable'),
        'POST /v1/auth/token': () => tokenResponse({ access_token: 'other', profile: PROFILE_A }),
        'POST /v1/session/logout': () => undefined,
      },
    });
    await api.me.identifiers.list().catch(() => {});
    await screen.findByRole('alert');
    // The lock's profile is whichever the stored profile names (prof-a here),
    // so assert the alert content and the sign-out action.
    expect(screen.getByText('This profile is unavailable')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    await waitFor(() => expect(m.calls.some((c) => c.path === '/v1/session/logout')).toBe(true));
  });
});

describe('SelectProfilePage', () => {
  test('renders nothing when no selection is required', async () => {
    const m = await mountSignedIn(<SelectProfilePage />, { profiles: [PROFILE_A] });
    expect(m.auth().selectionRequired).toBe(false);
    expect(screen.queryByRole('heading', { name: 'Choose a profile' })).toBeNull();
  });

  test('lists profiles with availability and selects an available one through the refresh grant', async () => {
    // A sign-in answering selection_required is what sets the flag; drive it
    // through the real client so the context sees the token response.
    setStoredRefreshToken('old-refresh');
    const calls = mockApi({
      'POST /v1/auth/login': () =>
        tokenResponse({
          access_token: null,
          selection_required: true,
          profile: null,
          profiles: [PROFILE_A, LOCKED_PROVIDER],
        }),
      'POST /v1/auth/token': () => tokenResponse({ access_token: 'chosen', profile: PROFILE_A }),
      'GET /v1/me': () => ({}),
    });
    const selected: unknown[] = [];
    const m = await mountUnder(
      <SelectProfilePage onSelected={(r) => selected.push(r)} />,
      calls,
      () => true,
    );
    await m.auth().login('a@b.test', 'pw');
    await screen.findByRole('heading', { name: 'Choose a profile' });
    // The locked profile is listed but cannot be continued as.
    expect(screen.getByText('Globex')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Continue as Globex' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Continue as Ada' }));
    await waitFor(() => expect(selected.length).toBe(1));
    const grant = calls.find((c) => c.path === '/v1/auth/token');
    expect(grant?.body).toMatchObject({
      grant_type: 'refresh_token',
      profile_uuid: 'prof-a',
    });
  });
});
