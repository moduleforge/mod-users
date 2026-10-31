import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { CredentialsPanel } from './credentials-panel';
import { StepUpDialog } from './step-up-dialog';
import { PROFILE_A, PROFILE_B, USER_VIEW, tokenResponse } from '../test-support/users-fixtures';
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

const BASE = {
  created_at: '2026-01-01T00:00:00Z',
  last_used_at: null,
  expires_at: null,
  default_profile_uuid: null,
  confined_to_party_uuid: null,
};
const PASSWORD = { ...BASE, uuid: 'cred-pw', kind: 'password', label: 'My password' };
const SSH = {
  ...BASE,
  uuid: 'cred-ssh',
  kind: 'ssh_key',
  label: 'Work laptop',
  key_type: 'ssh-ed25519',
  fingerprint: 'SHA256:abcdef',
  public_key: 'ssh-ed25519 AAAA',
};
const OIDC = {
  ...BASE,
  uuid: 'cred-oidc',
  kind: 'oidc',
  label: '',
  provider_id: 'google',
  email_at_link: 'ada@gmail.test',
};

const conflict = (code: string, field = 'credential', message = `${code} text`): Response =>
  jsonResponse({ error: { code: 'conflict', message: 'Conflict', details: [{ field, code, message }] } }, 409);

const STEP_UP_ACTION = {
  action: { code: 'users.step_up_required', message: 'Confirm it is you.', path: '/step-up' },
};

function mountPanel(routes: Record<string, (call: Call) => unknown>) {
  return mountSignedIn(<CredentialsPanel />, {
    profiles: [PROFILE_A, PROFILE_B],
    routes: { 'GET /v1/auth/providers': () => [], ...routes } as never,
  });
}

describe('CredentialsPanel', () => {
  test('lists credentials by kind with their kind-specific details', async () => {
    await mountPanel({
      'GET /v1/me/credentials': () => ({ credentials: [PASSWORD, SSH, OIDC] }),
    });
    await screen.findByText('My password');
    expect(screen.getByText('Password')).toBeTruthy();
    expect(screen.getByText('SSH key')).toBeTruthy();
    expect(screen.getByText('SHA256:abcdef')).toBeTruthy();
    expect(screen.getByText('Linked provider')).toBeTruthy();
    expect(screen.getByText('google (ada@gmail.test)')).toBeTruthy();
  });

  test('adds a password with an optional label, then reloads', async () => {
    let listed = [PASSWORD];
    const m = await mountPanel({
      'GET /v1/me/credentials': () => ({ credentials: listed }),
      'POST /v1/me/credentials/password': () => {
        listed = [PASSWORD, { ...PASSWORD, uuid: 'cred-pw2', label: 'Second' }];
        return { ...PASSWORD, uuid: 'cred-pw2', label: 'Second' };
      },
    });
    await screen.findByText('My password');
    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'a very long passphrase' } });
    fireEvent.change(screen.getAllByLabelText('Label (optional)')[0]!, { target: { value: 'Second' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add password' }));
    await screen.findByText('Password added.');
    await screen.findByText('Second');
    expect(m.calls.find((c) => c.path === '/v1/me/credentials/password')!.body).toEqual({
      password: 'a very long passphrase',
      label: 'Second',
    });
  });

  test('a second password is refused with readable copy', async () => {
    await mountPanel({
      'GET /v1/me/credentials': () => ({ credentials: [PASSWORD] }),
      'POST /v1/me/credentials/password': () => conflict('users.password_exists', 'password'),
    });
    await screen.findByText('My password');
    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'a very long passphrase' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add password' }));
    await screen.findByText('You already have a password. Revoke it before adding a new one.');
  });

  test('adds an SSH key; a rejected key shows the field copy', async () => {
    let attempts = 0;
    const m = await mountPanel({
      'GET /v1/me/credentials': () => ({ credentials: [] }),
      'POST /v1/me/credentials/ssh-keys': () => {
        attempts += 1;
        if (attempts === 1) {
          return jsonResponse(
            {
              error: {
                code: 'validation_error',
                message: 'bad key',
                details: [{ field: 'public_key', code: 'users.ssh_key_invalid', message: 'raw' }],
              },
            },
            400,
          );
        }
        return SSH;
      },
    });
    await screen.findByText('No credentials yet.');
    const textarea = screen.getByLabelText('Public key');
    fireEvent.change(textarea, { target: { value: '  not a key ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add key' }));
    await screen.findByText(/Paste exactly one public key line/);

    fireEvent.change(textarea, { target: { value: 'ssh-ed25519 AAAA you@example.test' } });
    fireEvent.change(screen.getAllByLabelText('Label (optional)')[1]!, { target: { value: 'Work laptop' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add key' }));
    await screen.findByText('SSH key added.');
    const posts = m.calls.filter((c) => c.path === '/v1/me/credentials/ssh-keys');
    expect(posts[1]!.body).toEqual({ public_key: 'ssh-ed25519 AAAA you@example.test', label: 'Work laptop' });
  });

  test('edits the label and default profile with only the changed members', async () => {
    const m = await mountPanel({
      'GET /v1/me/credentials': () => ({ credentials: [SSH] }),
      'PATCH /v1/me/credentials/cred-ssh': () => ({ ...SSH, label: 'Renamed', default_profile_uuid: 'prof-b' }),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Work laptop' }));
    fireEvent.change(screen.getByLabelText('Label'), { target: { value: 'Renamed' } });
    fireEvent.change(screen.getByLabelText('Default profile'), { target: { value: 'prof-b' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByText('Credential updated.');
    expect(m.calls.find((c) => c.method === 'PATCH')!.body).toEqual({
      label: 'Renamed',
      default_profile_uuid: 'prof-b',
    });
  });

  test('saving an unchanged edit makes no request', async () => {
    const m = await mountPanel({ 'GET /v1/me/credentials': () => ({ credentials: [SSH] }) });
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Work laptop' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.queryByLabelText('Default profile')).toBeNull());
    expect(m.calls.some((c) => c.method === 'PATCH')).toBe(false);
  });

  test('revoking deletes the credential and says sessions using it have ended', async () => {
    let listed = [PASSWORD, SSH];
    const m = await mountPanel({
      'GET /v1/me/credentials': () => ({ credentials: listed }),
      'DELETE /v1/me/credentials/cred-ssh': () => {
        listed = [PASSWORD];
        return undefined;
      },
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Work laptop' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Revoke credential' }));
    await screen.findByText('Credential revoked. Sessions that signed in with it have ended.');
    expect(screen.queryByText('Work laptop')).toBeNull();
    expect(m.calls.some((c) => c.method === 'DELETE' && c.path === '/v1/me/credentials/cred-ssh')).toBe(true);
  });

  test('users.last_credential is explained in the dialog and the credential stays', async () => {
    await mountPanel({
      'GET /v1/me/credentials': () => ({ credentials: [PASSWORD] }),
      'DELETE /v1/me/credentials/cred-pw': () => conflict('users.last_credential'),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke My password' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Revoke credential' }));
    await screen.findByText(/This is your last sign-in credential/);
    // The dialog stays open so the user can read it, and the row is still listed.
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(screen.getAllByText('My password').length).toBeGreaterThan(0);
  });

  test('a step-up on revoke prompts, then retries the delete once', async () => {
    let unlocked = false;
    let deletes = 0;
    await mountSignedIn(
      <>
        <StepUpDialog providers={[]} />
        <CredentialsPanel />
      </>,
      {
        profiles: [PROFILE_A],
        routes: {
          'GET /v1/auth/providers': () => [],
          'GET /v1/me/credentials': () => ({ credentials: [PASSWORD, SSH] }),
          'DELETE /v1/me/credentials/cred-ssh': () => {
            deletes += 1;
            return unlocked ? undefined : jsonResponse(STEP_UP_ACTION, 409);
          },
          'POST /v1/session/step-up/password': () => {
            unlocked = true;
            return tokenResponse({ access_token: 'fresh', refresh_token: null });
          },
        },
      },
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Work laptop' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Revoke credential' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Use my password' }));
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'secret secret' } });
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    await screen.findByText('Credential revoked. Sessions that signed in with it have ended.');
    expect(deletes).toBe(2);
  });

  test('a declined step-up leaves the credential and says nothing was changed', async () => {
    await mountPanel({
      'GET /v1/me/credentials': () => ({ credentials: [SSH] }),
      // No step-up handler is mounted, so the 409 reaches the panel.
      'DELETE /v1/me/credentials/cred-ssh': () => jsonResponse(STEP_UP_ACTION, 409),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Work laptop' }));
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Revoke credential' }));
    await screen.findByText('We could not confirm it’s you, so nothing was changed.');
  });

  test('a guest sees the upgrade form instead of the add forms and upgrades', async () => {
    let guest = true;
    const m = await mountPanel({
      'GET /v1/me': () => ({ ...USER_VIEW, is_guest: guest }),
      'GET /v1/me/credentials': () => ({ credentials: [] }),
      'POST /v1/me/upgrade': () => {
        guest = false;
        return { ...USER_VIEW, is_guest: false };
      },
    });
    await screen.findByText('Create your account');
    expect(screen.queryByRole('button', { name: 'Add password' })).toBeNull();
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'ada@example.test' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'a very long passphrase' } });
    fireEvent.change(screen.getByLabelText('Given name'), { target: { value: 'Ada' } });
    fireEvent.change(screen.getByLabelText('Family name'), { target: { value: 'Lovelace' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create account' }));
    // The user record is re-read, so the add forms replace the upgrade form.
    await screen.findByRole('button', { name: 'Add password' });
    expect(m.calls.find((c) => c.path === '/v1/me/upgrade')!.body).toEqual({
      email: 'ada@example.test',
      password: 'a very long passphrase',
      given_name: 'Ada',
      family_name: 'Lovelace',
    });
  });

  test('users.guest_upgrade_required on an add swaps in the upgrade form', async () => {
    await mountPanel({
      'GET /v1/me/credentials': () => ({ credentials: [] }),
      'POST /v1/me/credentials/ssh-keys': () => conflict('users.guest_upgrade_required', 'user'),
    });
    await screen.findByText('No credentials yet.');
    fireEvent.change(screen.getByLabelText('Public key'), { target: { value: 'ssh-ed25519 AAAA' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add key' }));
    await screen.findByText('Create your account');
    expect(screen.queryByRole('button', { name: 'Add key' })).toBeNull();
  });

  test('a duplicate email on upgrade shows readable copy', async () => {
    await mountPanel({
      'GET /v1/me': () => ({ ...USER_VIEW, is_guest: true }),
      'GET /v1/me/credentials': () => ({ credentials: [] }),
      'POST /v1/me/upgrade': () => conflict('users.identifier_taken', 'value'),
    });
    await screen.findByText('Create your account');
    for (const [label, value] of [
      ['Email', 'taken@example.test'],
      ['Password', 'a very long passphrase'],
      ['Given name', 'Ada'],
      ['Family name', 'Lovelace'],
    ] as const) {
      fireEvent.change(screen.getByLabelText(label), { target: { value } });
    }
    fireEvent.click(screen.getByRole('button', { name: 'Create account' }));
    await screen.findByText('That email address is already registered.');
  });

  test('linking a provider refuses an authorize URL that is not http(s)', async () => {
    await mountPanel({
      'GET /v1/auth/providers': () => [{ id: 'google', display_name: 'Google' }],
      'GET /v1/me/credentials': () => ({ credentials: [] }),
      'POST /v1/me/credentials/oidc/google/start': () => ({ authorize_url: 'javascript:alert(1)' }),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Link Google' }));
    await screen.findByText('The sign-in provider returned an address that cannot be opened.');
  });

  test('a provider already linked elsewhere is reported readably', async () => {
    await mountPanel({
      'GET /v1/auth/providers': () => [{ id: 'google', display_name: 'Google' }],
      'GET /v1/me/credentials': () => ({ credentials: [] }),
      'POST /v1/me/credentials/oidc/google/start': () => conflict('users.oidc_identity_in_use', 'subject'),
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Link Google' }));
    await screen.findByText('That external account is already linked to a sign-in.');
  });

  test('a load failure offers a retry', async () => {
    let fail = true;
    await mountPanel({
      'GET /v1/me/credentials': () => (fail ? errorReply(500, 'internal', 'Boom.') : { credentials: [PASSWORD] }),
    });
    await screen.findByText('Boom.');
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByText('My password');
  });
});
