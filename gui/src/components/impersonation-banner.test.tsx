import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { ImpersonationBanner } from './impersonation-banner';
import { PROFILE_A, PROFILE_B, tokenResponse } from '../test-support/users-fixtures';
import {
  errorReply,
  mountSignedIn,
  setUpHarness,
  tearDownHarness,
} from '../test-support/chrome-harness';

beforeEach(setUpHarness);
afterEach(tearDownHarness);

const IMPERSONATING = { impersonating: true };

describe('ImpersonationBanner', () => {
  test('renders nothing outside an impersonation', async () => {
    await mountSignedIn(<ImpersonationBanner />, { profiles: [PROFILE_A] });
    expect(screen.queryByRole('status')).toBeNull();
  });

  test('shows the impersonated profile while impersonating, with unknown time after a reload', async () => {
    await mountSignedIn(<ImpersonationBanner />, {
      profiles: [PROFILE_B],
      active: PROFILE_B,
      session: IMPERSONATING,
    });
    expect(screen.getByText('Impersonating Ada @ Acme')).toBeTruthy();
    // No token response has arrived since the reload, so no deadline is known.
    expect(screen.getByText('Time remaining unknown')).toBeTruthy();
  });

  test('renew posts renew and starts the countdown from the new expiry', async () => {
    const m = await mountSignedIn(<ImpersonationBanner />, {
      profiles: [PROFILE_B],
      active: PROFILE_B,
      session: IMPERSONATING,
      routes: {
        'POST /v1/impersonations/current/renew': () =>
          tokenResponse({
            access_token: 'renewed',
            refresh_token: null,
            expires_in: 600,
            profile: PROFILE_B,
            session: { ...tokenResponse().session!, impersonating: true },
          }),
      },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Renew' }));
    await waitFor(() => expect(m.auth().accessTokenExpiresAt).not.toBeNull());
    await screen.findByText(/^(9:5\d|10:00) remaining$/);
    expect(m.calls.some((c) => c.path === '/v1/impersonations/current/renew')).toBe(true);
  });

  test('end deletes the impersonation and restores the parent session', async () => {
    const m = await mountSignedIn(<ImpersonationBanner />, {
      profiles: [PROFILE_B],
      active: PROFILE_B,
      session: IMPERSONATING,
      routes: {
        'DELETE /v1/impersonations/current': () => undefined,
        // The client swaps back to the administrator's token via the refresh grant.
        'POST /v1/auth/token': () =>
          tokenResponse({ access_token: 'admin-access', profile: PROFILE_A }),
      },
    });
    fireEvent.click(screen.getByRole('button', { name: 'End impersonation' }));
    await waitFor(() => expect(m.auth().impersonating).toBe(false));
    expect(m.auth().profile?.uuid).toBe('prof-a');
    expect(m.auth().token).toBe('admin-access');
    expect(screen.queryByRole('status')).toBeNull();
  });

  test('a refused renew shows the server message and keeps the banner', async () => {
    await mountSignedIn(<ImpersonationBanner />, {
      profiles: [PROFILE_B],
      active: PROFILE_B,
      session: IMPERSONATING,
      routes: {
        'POST /v1/impersonations/current/renew': () =>
          errorReply(409, 'users.impersonation_limit_reached', 'Maximum reached.'),
      },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Renew' }));
    await screen.findByText('Maximum reached.');
    expect(screen.getByRole('status')).toBeTruthy();
  });
});
