import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { fireEvent, screen, within } from '@testing-library/react';
import { IdentifiersPanel } from './identifiers-panel';
import { PROFILE_A } from '../test-support/users-fixtures';
import {
  errorReply,
  jsonResponse,
  mountSignedIn,
  setUpHarness,
  tearDownHarness,
  type Call,
} from '../test-support/chrome-harness';

beforeEach(setUpHarness);
afterEach(tearDownHarness);

const PRIMARY: Record<string, unknown> = {
  uuid: 'id-1',
  kind: 'email',
  value: 'ada@example.test',
  is_primary: true,
  verified: true,
  verified_at: '2026-01-01T00:00:00Z',
  created_at: '2026-01-01T00:00:00Z',
};
const SECOND: Record<string, unknown> = {
  uuid: 'id-2',
  kind: 'email',
  value: 'ada@work.test',
  is_primary: false,
  verified: false,
  verified_at: null,
  created_at: '2026-02-01T00:00:00Z',
};

const conflict = (code: string, field: string): Response =>
  jsonResponse({ error: { code: 'conflict', message: 'Conflict', details: [{ field, code, message: 'raw' }] } }, 409);

function mountPanel(routes: Record<string, (call: Call) => unknown>) {
  return mountSignedIn(<IdentifiersPanel />, { profiles: [PROFILE_A], routes: routes as never });
}

describe('IdentifiersPanel', () => {
  test('lists addresses with primary and verification status and per-row actions', async () => {
    await mountPanel({ 'GET /v1/me/identifiers': () => ({ identifiers: [PRIMARY, SECOND] }) });
    await screen.findByText('ada@example.test');
    expect(screen.getByText('Primary')).toBeTruthy();
    expect(screen.getByText('Unverified')).toBeTruthy();
    // Only an unverified address offers Verify; only a verified, non-primary one offers Make primary.
    expect(screen.getByRole('button', { name: 'Verify ada@work.test' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Verify ada@example.test' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Make ada@example.test primary' })).toBeNull();
  });

  test('adds an address and reloads', async () => {
    let listed = [PRIMARY];
    const m = await mountPanel({
      'GET /v1/me/identifiers': () => ({ identifiers: listed }),
      'POST /v1/me/identifiers': () => {
        listed = [PRIMARY, SECOND];
        return SECOND;
      },
    });
    await screen.findByText('ada@example.test');
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: ' ada@work.test ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add address' }));
    await screen.findByText('ada@work.test');
    expect(m.calls.find((c) => c.method === 'POST' && c.path === '/v1/me/identifiers')!.body).toEqual({
      kind: 'email',
      value: 'ada@work.test',
    });
  });

  test('a duplicate address maps users.identifier_taken to readable copy', async () => {
    await mountPanel({
      'GET /v1/me/identifiers': () => ({ identifiers: [PRIMARY] }),
      'POST /v1/me/identifiers': () => conflict('users.identifier_taken', 'value'),
    });
    await screen.findByText('ada@example.test');
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'taken@example.test' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add address' }));
    await screen.findByText('That email address is already registered.');
  });

  test('verify emails a code, then confirms it', async () => {
    let verified = false;
    const m = await mountPanel({
      'GET /v1/me/identifiers': () => ({ identifiers: [PRIMARY, verified ? { ...SECOND, verified: true } : SECOND] }),
      'POST /v1/me/identifiers/id-2/verification': () => undefined,
      'POST /v1/me/identifiers/id-2/verify': () => {
        verified = true;
        return { ...SECOND, verified: true };
      },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Verify ada@work.test' }));
    const dialog = await screen.findByRole('dialog');
    const input = await within(dialog).findByLabelText('Verification code');
    // A short code cannot be submitted.
    fireEvent.change(input, { target: { value: '12' } });
    expect((within(dialog).getByRole('button', { name: 'Verify' }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(input, { target: { value: '123456' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Verify' }));
    await screen.findByText('Address verified.');
    expect(m.calls.some((c) => c.path === '/v1/me/identifiers/id-2/verification')).toBe(true);
    expect(m.calls.find((c) => c.path === '/v1/me/identifiers/id-2/verify')!.body).toEqual({ code: '123456' });
    // The row now offers Make primary instead of Verify.
    await screen.findByRole('button', { name: 'Make ada@work.test primary' });
  });

  test('a wrong code keeps the dialog open with a message', async () => {
    await mountPanel({
      'GET /v1/me/identifiers': () => ({ identifiers: [PRIMARY, SECOND] }),
      'POST /v1/me/identifiers/id-2/verification': () => undefined,
      'POST /v1/me/identifiers/id-2/verify': () => errorReply(400, 'bad_request', 'bad code'),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Verify ada@work.test' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(await within(dialog).findByLabelText('Verification code'), { target: { value: '000000' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Verify' }));
    await within(dialog).findByText('That code is wrong or has expired.');
  });

  test('sets the primary address', async () => {
    const verifiedSecond = { ...SECOND, verified: true };
    const m = await mountPanel({
      'GET /v1/me/identifiers': () => ({ identifiers: [PRIMARY, verifiedSecond] }),
      'PUT /v1/me/identifiers/id-2/primary': () => ({ ...verifiedSecond, is_primary: true }),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Make ada@work.test primary' }));
    await screen.findByText('ada@work.test is now your primary address.');
    expect(m.calls.some((c) => c.method === 'PUT' && c.path === '/v1/me/identifiers/id-2/primary')).toBe(true);
  });

  test('deletes an address after confirmation', async () => {
    let listed = [PRIMARY, SECOND];
    const m = await mountPanel({
      'GET /v1/me/identifiers': () => ({ identifiers: listed }),
      'DELETE /v1/me/identifiers/id-2': () => {
        listed = [PRIMARY];
        return undefined;
      },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Delete ada@work.test' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete address' }));
    await screen.findByText('Address removed.');
    expect(screen.queryByText('ada@work.test')).toBeNull();
    expect(m.calls.some((c) => c.method === 'DELETE' && c.path === '/v1/me/identifiers/id-2')).toBe(true);
  });

  test('a delete the API refuses is explained in the dialog', async () => {
    await mountPanel({
      'GET /v1/me/identifiers': () => ({ identifiers: [PRIMARY, SECOND] }),
      'DELETE /v1/me/identifiers/id-2': () => conflict('users.identifier_in_use', 'identifier'),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Delete ada@work.test' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete address' }));
    await screen.findByText('This address is still in use elsewhere and cannot be removed.');
  });
});
