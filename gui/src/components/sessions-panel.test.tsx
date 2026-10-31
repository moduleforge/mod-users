import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { SessionsPanel } from './sessions-panel';
import { PROFILE_A } from '../test-support/users-fixtures';
import { getStoredToken } from '../lib/config';
import {
  errorReply,
  mountSignedIn,
  setUpHarness,
  tearDownHarness,
  type Call,
} from '../test-support/chrome-harness';

beforeEach(setUpHarness);
afterEach(tearDownHarness);

const SESSION_BASE = {
  acr: 1,
  amr: ['pwd'],
  auth_time: '2026-01-01T00:00:00Z',
  created_at: '2026-01-01T00:00:00Z',
  last_seen_at: '2026-01-02T00:00:00Z',
  expires_at: '2026-02-01T00:00:00Z',
  impersonating: false,
};
const CURRENT = { ...SESSION_BASE, uuid: 'sess-1', current: true };
const OTHER = { ...SESSION_BASE, uuid: 'sess-2', current: false };

function mountPanel(routes: Record<string, (call: Call) => unknown>) {
  return mountSignedIn(<SessionsPanel />, { profiles: [PROFILE_A], routes: routes as never });
}

describe('SessionsPanel', () => {
  test('lists sessions with the current one marked', async () => {
    await mountPanel({ 'GET /v1/me/sessions': () => ({ sessions: [CURRENT, OTHER] }) });
    await screen.findByText('This session');
    expect(screen.getAllByText('This session')).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Sign out of this session' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Revoke session 2' })).toBeTruthy();
  });

  test('revokes another session and drops its row', async () => {
    let listed = [CURRENT, OTHER];
    const m = await mountPanel({
      'GET /v1/me/sessions': () => ({ sessions: listed }),
      'DELETE /v1/me/sessions/sess-2': () => {
        listed = [CURRENT];
        return undefined;
      },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke session 2' }));
    await screen.findByText('Session revoked.');
    expect(screen.queryByRole('button', { name: 'Revoke session 2' })).toBeNull();
    expect(m.calls.some((c) => c.method === 'DELETE' && c.path === '/v1/me/sessions/sess-2')).toBe(true);
    // Another session's revocation leaves this tab signed in.
    expect(getStoredToken()).toBe('old-access');
  });

  test('revoking the current session signs the tab out', async () => {
    const m = await mountPanel({
      'GET /v1/me/sessions': () => ({ sessions: [CURRENT, OTHER] }),
      'DELETE /v1/me/sessions/sess-1': () => undefined,
      // The server session is already gone, so the logout call is refused.
      'POST /v1/session/logout': () => errorReply(401, 'users.session_revoked', 'revoked'),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Sign out of this session' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Sign out' }));
    await waitFor(() => expect(getStoredToken()).toBeNull());
    expect(m.navigations).toContain('/auth/login');
    expect(m.auth().token).toBeNull();
  });

  test('cancelling the current-session confirmation revokes nothing', async () => {
    const m = await mountPanel({ 'GET /v1/me/sessions': () => ({ sessions: [CURRENT] }) });
    fireEvent.click(await screen.findByRole('button', { name: 'Sign out of this session' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(m.calls.some((c) => c.method === 'DELETE')).toBe(false);
    expect(getStoredToken()).toBe('old-access');
  });

  test('a failed revoke is reported and the row stays', async () => {
    await mountPanel({
      'GET /v1/me/sessions': () => ({ sessions: [CURRENT, OTHER] }),
      'DELETE /v1/me/sessions/sess-2': () => errorReply(500, 'internal', 'Could not revoke.'),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke session 2' }));
    await screen.findByText('Could not revoke.');
    expect(screen.getByRole('button', { name: 'Revoke session 2' })).toBeTruthy();
  });
});
