import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { InvitationsView } from './invitations-view';
import { PROFILE_A, PROFILE_B } from '../test-support/users-fixtures';
import {
  errorReply,
  jsonResponse,
  mountSignedIn,
  setUpHarness,
  tearDownHarness,
} from '../test-support/chrome-harness';

beforeEach(setUpHarness);
afterEach(tearDownHarness);

const INVITED = {
  ...PROFILE_B,
  uuid: 'prof-i',
  status: 'invited',
  display_name: 'Ada @ Globex',
  party: { uuid: 'party-2', kind: 'organization', display_name: 'Globex' },
  available: false,
  lock: { reason: 'not_held', remedy: { kind: 'accept_invitation' } },
};

/** A profile list that changes after each action, as the server's would. */
function listing(initial: unknown[]) {
  let current = initial;
  return {
    set: (next: unknown[]) => {
      current = next;
    },
    route: () => ({ profiles: current }),
  };
}

describe('InvitationsView', () => {
  test('lists invited profiles with accept/decline and active party profiles with leave', async () => {
    const list = listing([PROFILE_A, PROFILE_B, INVITED]);
    await mountSignedIn(<InvitationsView />, {
      profiles: [PROFILE_A, PROFILE_B],
      routes: { 'GET /v1/me/profiles': list.route },
    });
    await screen.findByRole('button', { name: 'Accept invitation to Globex' });
    expect(screen.getByRole('button', { name: 'Decline invitation to Globex' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Leave Acme' })).toBeTruthy();
    // A personal profile is never a membership to leave.
    expect(screen.queryByRole('button', { name: 'Leave Ada' })).toBeNull();
  });

  test('accepting posts accept and refreshes both lists', async () => {
    const list = listing([PROFILE_A, INVITED]);
    const m = await mountSignedIn(<InvitationsView />, {
      profiles: [PROFILE_A],
      routes: {
        'GET /v1/me/profiles': list.route,
        'POST /v1/me/profiles/prof-i/accept': () => {
          list.set([PROFILE_A, { ...INVITED, status: 'active', available: true, lock: null }]);
          return { ...INVITED, status: 'active', available: true, lock: null };
        },
      },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Accept invitation to Globex' }));
    await screen.findByText('No pending invitations.');
    const sessionReads = m.calls.filter((c) => c.path === '/v1/session').length;
    // Hydration read it once; the post-action refresh read it again.
    expect(sessionReads).toBeGreaterThanOrEqual(2);
    expect(screen.getByRole('button', { name: 'Leave Globex' })).toBeTruthy();
  });

  test('declining posts decline and drops the invitation', async () => {
    const list = listing([PROFILE_A, INVITED]);
    const m = await mountSignedIn(<InvitationsView />, {
      profiles: [PROFILE_A],
      routes: {
        'GET /v1/me/profiles': list.route,
        'POST /v1/me/profiles/prof-i/decline': () => {
          list.set([PROFILE_A]);
          return undefined;
        },
      },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Decline invitation to Globex' }));
    await screen.findByText('No pending invitations.');
    expect(m.calls.some((c) => c.method === 'POST' && c.path === '/v1/me/profiles/prof-i/decline')).toBe(true);
  });

  test('leaving an active party profile posts leave and refreshes', async () => {
    const list = listing([PROFILE_A, PROFILE_B]);
    const m = await mountSignedIn(<InvitationsView />, {
      profiles: [PROFILE_A, PROFILE_B],
      routes: {
        'GET /v1/me/profiles': list.route,
        'POST /v1/me/profiles/prof-b/leave': () => {
          list.set([PROFILE_A]);
          return undefined;
        },
      },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Leave Acme' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Leave Acme' })).toBeNull());
    expect(m.calls.some((c) => c.path === '/v1/me/profiles/prof-b/leave')).toBe(true);
  });

  test('step_up_required with no handler tells the user and notifies the host', async () => {
    const list = listing([PROFILE_A, PROFILE_B]);
    const seen: string[] = [];
    await mountSignedIn(<InvitationsView onStepUpRequired={(e) => seen.push(e.code)} />, {
      profiles: [PROFILE_A, PROFILE_B],
      routes: {
        'GET /v1/me/profiles': list.route,
        'POST /v1/me/profiles/prof-b/leave': () =>
          jsonResponse(
            { action: { code: 'users.step_up_required', message: 'Step up.', path: '/step-up' } },
            409,
          ),
      },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Leave Acme' }));
    await screen.findByText(/Confirm your identity/);
    expect(seen).toEqual(['users.step_up_required']);
  });

  test('a registered step-up handler is used and the action retried', async () => {
    const list = listing([PROFILE_A, PROFILE_B]);
    let attempts = 0;
    const handler = async () => 'fresh-token';
    const m = await mountSignedIn(<InvitationsView stepUpHandler={handler} />, {
      profiles: [PROFILE_A, PROFILE_B],
      routes: {
        'GET /v1/me/profiles': list.route,
        'POST /v1/me/profiles/prof-b/leave': () => {
          attempts += 1;
          if (attempts === 1) {
            return jsonResponse(
              { action: { code: 'users.step_up_required', message: 'Step up.', path: '/step-up' } },
              409,
            );
          }
          list.set([PROFILE_A]);
          return undefined;
        },
      },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Leave Acme' }));
    await waitFor(() => expect(attempts).toBe(2));
    expect(m.calls.filter((c) => c.path === '/v1/me/profiles/prof-b/leave').length).toBe(2);
  });

  test('a failed action shows the server message', async () => {
    const list = listing([PROFILE_A, INVITED]);
    await mountSignedIn(<InvitationsView />, {
      profiles: [PROFILE_A],
      routes: {
        'GET /v1/me/profiles': list.route,
        'POST /v1/me/profiles/prof-i/accept': () => errorReply(404, 'users.not_found', 'Invitation gone.'),
      },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Accept invitation to Globex' }));
    await screen.findByText('Invitation gone.');
  });
});
