import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { SSHKeysPanel } from './ssh-keys-panel';
import { API_BASE_URL, ApiActionRequiredError } from '../lib/api';
import type { SSHKey } from '../lib/api';

// Behavior tests for SSHKeysPanel against a stubbed `globalThis.fetch`. The
// panel only ever talks to the shared `api` singleton, so every assertion
// here is about the HTTP requests it issues and what the user sees.

const originalFetch = globalThis.fetch;
const originalHref = window.location.href;

interface RecordedRequest {
  url: string;
  path: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
}

type Handler = (request: RecordedRequest) => Response | Promise<Response>;

let requests: RecordedRequest[] = [];

function installFetch(handler: Handler): void {
  requests = [];
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const rawBody = typeof init?.body === 'string' ? init.body : undefined;
    const recorded: RecordedRequest = {
      url,
      path: url.startsWith(API_BASE_URL) ? url.slice(API_BASE_URL.length) : url,
      method: init?.method ?? 'GET',
      headers: { ...(init?.headers as Record<string, string> | undefined) },
      body: rawBody ? (JSON.parse(rawBody) as unknown) : undefined,
    };
    requests.push(recorded);
    return handler(recorded);
  }) as unknown as typeof fetch;
}

beforeEach(() => {
  localStorage.setItem('auth_token', 'session-token');
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  localStorage.clear();
  if (window.location.href !== originalHref) {
    (window as unknown as { happyDOM: { setURL(url: string): void } }).happyDOM.setURL(
      originalHref,
    );
  }
});

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function noContent(): Response {
  return new Response(null, { status: 204 });
}

function apiError(
  status: number,
  code: string,
  message: string,
  details?: Array<{ field: string; code: string; message: string }>,
): Response {
  return json(status, { error: { code, message, ...(details ? { details } : {}) } });
}

function actionRequired(status: number, code: string, message: string, path: string): Response {
  return json(status, { action: { code, message, path } });
}

const KEY_A: SSHKey = {
  uuid: '11111111-1111-4111-8111-111111111111',
  key_type: 'ssh-ed25519',
  fingerprint: 'SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG',
  public_key: 'ssh-ed25519 AAAA',
  label: 'Work laptop',
  created_at: '2026-09-28T12:34:56Z',
};

const KEY_B: SSHKey = {
  uuid: '22222222-2222-4222-8222-222222222222',
  key_type: 'ecdsa-sha2-nistp256',
  fingerprint: 'SHA256:zyxwvutsrqponmlkjihgfedcba9876543210ZYXWVUT',
  public_key: 'ecdsa-sha2-nistp256 AAAA',
  label: '',
  created_at: '2026-09-29T08:00:00Z',
};

const KEY_LINE = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample you@example.com';

function listOf(items: SSHKey[], total = items.length): Response {
  return json(200, { items, total });
}

function isList(request: RecordedRequest): boolean {
  return request.method === 'GET' && request.path.startsWith('/v1/self/ssh-keys');
}

function isRegister(request: RecordedRequest): boolean {
  return request.method === 'POST' && request.path === '/v1/self/ssh-keys';
}

function fillAndSubmit(key: string, label?: string): void {
  fireEvent.change(screen.getByLabelText('Public key'), { target: { value: key } });
  if (label !== undefined) {
    fireEvent.change(screen.getByLabelText(/^Label/), { target: { value: label } });
  }
  fireEvent.click(screen.getByRole('button', { name: 'Add key' }));
}

describe('SSHKeysPanel list', () => {
  test('renders label, type, fingerprint, and date for each key', async () => {
    installFetch(() => listOf([KEY_A, KEY_B]));
    render(<SSHKeysPanel />);

    expect(await screen.findByText('Work laptop')).toBeInTheDocument();
    expect(screen.getByText('ssh-ed25519')).toBeInTheDocument();
    expect(screen.getByText('ecdsa-sha2-nistp256')).toBeInTheDocument();
    expect(screen.getByText(KEY_A.fingerprint)).toHaveAttribute('title', KEY_A.fingerprint);
    expect(screen.getByText(KEY_A.fingerprint)).toHaveClass('font-mono');
    const date = screen.getByText(new Date(KEY_A.created_at).toLocaleDateString());
    expect(date).toHaveAttribute('title', KEY_A.created_at);
    expect(screen.getByText('Untitled key')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Revoke Work laptop' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: `Revoke ${KEY_B.fingerprint}` })).toBeInTheDocument();
    expect(requests[0]?.path).toBe('/v1/self/ssh-keys?limit=200');
  });

  test('renders the empty state', async () => {
    installFetch(() => listOf([]));
    render(<SSHKeysPanel />);
    expect(await screen.findByText('No SSH keys yet.')).toBeInTheDocument();
  });

  test('shows "Showing N of M keys" when total exceeds the returned items', async () => {
    installFetch(() => listOf([KEY_A, KEY_B], 250));
    render(<SSHKeysPanel />);
    expect(await screen.findByText('Showing 2 of 250 keys')).toBeInTheDocument();
  });

  test('renders a label with a bidi override inside a bdi element', async () => {
    const spoofed = '‮gpj.exe';
    installFetch(() => listOf([{ ...KEY_A, label: spoofed }]));
    const { container } = render(<SSHKeysPanel />);
    await screen.findByText('ssh-ed25519');
    const bdi = container.querySelector('bdi');
    expect(bdi).not.toBeNull();
    expect(bdi?.textContent).toBe(spoofed);
  });

  test('load error shows a Retry button that reloads', async () => {
    let calls = 0;
    installFetch(() => {
      calls += 1;
      return calls === 1 ? apiError(500, 'internal', 'Boom') : listOf([KEY_A]);
    });
    render(<SSHKeysPanel />);
    expect(await screen.findByText('Boom')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Work laptop')).toBeInTheDocument();
  });

  test('a list-time action-required error is delegated to the host', async () => {
    const onActionRequired = mock((_error: ApiActionRequiredError) => {});
    installFetch(() =>
      actionRequired(403, 'users.email_unverified', 'Verify your email.', '/verify-email'),
    );
    render(<SSHKeysPanel onActionRequired={onActionRequired} />);
    expect(
      await screen.findByText(/only after your email address is verified/),
    ).toBeInTheDocument();
    expect(onActionRequired).toHaveBeenCalledTimes(1);
  });
});

describe('SSHKeysPanel register', () => {
  test('sends the trimmed key, omits a blank label, clears the form, and reloads', async () => {
    let lists = 0;
    installFetch((request) => {
      if (isRegister(request)) return json(201, KEY_A);
      lists += 1;
      return lists === 1 ? listOf([]) : listOf([KEY_A]);
    });
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');

    fillAndSubmit(`  ${KEY_LINE}\n`, '   ');

    expect(await screen.findByText('Work laptop')).toBeInTheDocument();
    const post = requests.find(isRegister);
    expect(post?.body).toEqual({ public_key: KEY_LINE });
    expect(post?.headers['X-Step-Up-Token']).toBeUndefined();
    expect(screen.getByText('SSH key added.')).toBeInTheDocument();
    expect(screen.getByLabelText('Public key')).toHaveValue('');
    expect(screen.getByLabelText(/^Label/)).toHaveValue('');
    expect(lists).toBe(2);
  });

  test('sends a trimmed label when one is given', async () => {
    installFetch((request) => (isRegister(request) ? json(201, KEY_A) : listOf([])));
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE, '  CI runner ');
    await screen.findByText('SSH key added.');
    expect(requests.find(isRegister)?.body).toEqual({ public_key: KEY_LINE, label: 'CI runner' });
  });

  test('the submit button is disabled while the key is blank', async () => {
    installFetch(() => listOf([]));
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    const button = screen.getByRole('button', { name: 'Add key' });
    expect(button).toBeDisabled();
    fireEvent.change(screen.getByLabelText('Public key'), { target: { value: '   \n ' } });
    expect(button).toBeDisabled();
    fireEvent.change(screen.getByLabelText('Public key'), { target: { value: KEY_LINE } });
    expect(button).toBeEnabled();
  });

  test('409 users.ssh_key_in_use renders on the key field', async () => {
    installFetch((request) =>
      isRegister(request)
        ? apiError(409, 'conflict', 'Conflict', [
            { field: 'public_key', code: 'users.ssh_key_in_use', message: 'server text' },
          ])
        : listOf([]),
    );
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);
    const error = await screen.findByText('This key is already registered.');
    expect(error).toBeInTheDocument();
    expect(screen.getByLabelText('Public key')).toHaveAttribute('aria-invalid', 'true');
    expect(screen.getByLabelText('Public key').getAttribute('aria-describedby')).toBe(error.id);
    expect(screen.getByLabelText('Public key')).toHaveValue(KEY_LINE);
  });

  test('400 users.ssh_key_type_unsupported renders on the key field', async () => {
    installFetch((request) =>
      isRegister(request)
        ? apiError(400, 'invalid_input', 'Invalid', [
            { field: 'public_key', code: 'users.ssh_key_type_unsupported', message: 'server text' },
          ])
        : listOf([]),
    );
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);
    expect(await screen.findByText(/Ed25519, a FIDO \(sk-\) key, ECDSA/)).toBeInTheDocument();
  });

  test('400 users.ssh_key_label_invalid renders on the label field', async () => {
    installFetch((request) =>
      isRegister(request)
        ? apiError(400, 'invalid_input', 'Invalid', [
            { field: 'label', code: 'users.ssh_key_label_invalid', message: 'server text' },
          ])
        : listOf([]),
    );
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE, 'bad');
    const error = await screen.findByText(/control or bidirectional-text characters/);
    expect(screen.getByLabelText(/^Label/).getAttribute('aria-describedby')).toBe(error.id);
    expect(screen.getByLabelText('Public key')).not.toHaveAttribute('aria-invalid');
  });

  test('an unknown detail code falls back to the server message', async () => {
    installFetch((request) =>
      isRegister(request)
        ? apiError(400, 'invalid_input', 'Invalid', [
            { field: 'public_key', code: 'users.something_new', message: 'Server says no.' },
          ])
        : listOf([]),
    );
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);
    expect(await screen.findByText('Server says no.')).toBeInTheDocument();
  });

  test('an error without field details shows the error message', async () => {
    installFetch((request) =>
      isRegister(request) ? apiError(500, 'internal', 'Server exploded') : listOf([]),
    );
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);
    expect(await screen.findByText('Server exploded')).toBeInTheDocument();
  });
});

describe('SSHKeysPanel step-up', () => {
  const stepUpAction = () =>
    actionRequired(409, 'users.step_up_required', 'Step-up required.', '/step-up');

  test('completes step-up inline, retries once with the token, and does not reuse it', async () => {
    let registers = 0;
    installFetch((request) => {
      if (isRegister(request)) {
        registers += 1;
        if (registers === 1) return stepUpAction();
        if (registers === 2) {
          return request.headers['X-Step-Up-Token'] === 't1'
            ? json(201, KEY_A)
            : apiError(500, 'internal', 'missing token');
        }
        return json(201, KEY_B);
      }
      if (request.path === '/v1/self/credential/step-up') return noContent();
      if (request.path === '/v1/self/credential/step-up/verify') {
        return json(200, { step_up_token: 't1', expires_in: 300 });
      }
      return listOf([]);
    });
    const onActionRequired = mock((_error: ApiActionRequiredError) => {});
    render(<SSHKeysPanel onActionRequired={onActionRequired} />);
    await screen.findByText('No SSH keys yet.');

    fillAndSubmit(KEY_LINE, 'Laptop');
    fireEvent.click(await screen.findByRole('button', { name: 'Send code' }));
    // No code request before the click.
    const codeInput = await screen.findByLabelText('Verification code');
    expect(requests.filter((r) => r.path === '/v1/self/credential/step-up')).toHaveLength(1);
    expect(codeInput).toHaveAttribute('inputmode', 'numeric');
    expect(codeInput).toHaveAttribute('autocomplete', 'one-time-code');
    expect(screen.getByRole('button', { name: /Resend code/ })).toBeDisabled();

    fireEvent.change(codeInput, { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify and continue' }));

    expect(await screen.findByText('SSH key added.')).toBeInTheDocument();
    const posts = requests.filter(isRegister);
    expect(posts).toHaveLength(2);
    expect(posts[0]?.headers['X-Step-Up-Token']).toBeUndefined();
    expect(posts[1]?.headers['X-Step-Up-Token']).toBe('t1');
    expect(posts[1]?.body).toEqual({ public_key: KEY_LINE, label: 'Laptop' });
    expect(requests.find((r) => r.path.endsWith('/step-up/verify'))?.body).toEqual({
      code: '123456',
    });
    expect(onActionRequired).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Send code' })).toBeNull();

    // A later registration does not carry the spent token.
    fillAndSubmit(KEY_LINE);
    await waitFor(() => expect(requests.filter(isRegister)).toHaveLength(3));
    expect(requests.filter(isRegister)[2]?.headers['X-Step-Up-Token']).toBeUndefined();
  });

  test('revoke is also completed through step-up', async () => {
    let deletes = 0;
    installFetch((request) => {
      if (request.method === 'DELETE') {
        deletes += 1;
        return deletes === 1 ? stepUpAction() : noContent();
      }
      if (request.path === '/v1/self/credential/step-up') return noContent();
      if (request.path === '/v1/self/credential/step-up/verify') {
        return json(200, { step_up_token: 't9', expires_in: 300 });
      }
      return listOf([KEY_A]);
    });
    render(<SSHKeysPanel />);
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Work laptop' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke key' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Send code' }));
    fireEvent.change(await screen.findByLabelText('Verification code'), {
      target: { value: '654321' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Verify and continue' }));
    await waitFor(() => expect(requests.filter((r) => r.method === 'DELETE')).toHaveLength(2));
    const deleteRequests = requests.filter((r) => r.method === 'DELETE');
    expect(deleteRequests[1]?.headers['X-Step-Up-Token']).toBe('t9');
    expect(deleteRequests[1]?.path).toBe(`/v1/self/ssh-keys/${KEY_A.uuid}`);
  });

  test('a wrong code shows an inline error, keeps the session, and does not navigate', async () => {
    installFetch((request) => {
      if (isRegister(request)) return stepUpAction();
      if (request.path === '/v1/self/credential/step-up') return noContent();
      if (request.path === '/v1/self/credential/step-up/verify') {
        return apiError(401, 'unauthenticated', 'Invalid code');
      }
      return listOf([]);
    });
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);
    fireEvent.click(await screen.findByRole('button', { name: 'Send code' }));
    fireEvent.change(await screen.findByLabelText('Verification code'), {
      target: { value: '000000' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Verify and continue' }));

    expect(await screen.findByText('That code is wrong or has expired.')).toBeInTheDocument();
    expect(localStorage.getItem('auth_token')).toBe('session-token');
    expect(window.location.href).toBe(originalHref);
    // The challenge stays open and no retry was sent.
    expect(screen.getByLabelText('Verification code')).toBeInTheDocument();
    expect(requests.filter(isRegister)).toHaveLength(1);
  });

  test('cancel discards the pending operation without retrying', async () => {
    installFetch((request) => {
      if (isRegister(request)) return stepUpAction();
      return listOf([]);
    });
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);
    await screen.findByRole('button', { name: 'Send code' });
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Send code' })).toBeNull());
    expect(requests.filter(isRegister)).toHaveLength(1);
    expect(requests.some((r) => r.path.startsWith('/v1/self/credential'))).toBe(false);
  });

  test('a retry that fails shows its error and the next attempt goes through step-up again', async () => {
    let registers = 0;
    installFetch((request) => {
      if (isRegister(request)) {
        registers += 1;
        if (registers === 2) {
          return apiError(400, 'invalid_input', 'Invalid', [
            { field: 'public_key', code: 'users.ssh_key_invalid', message: 'bad' },
          ]);
        }
        return stepUpAction();
      }
      if (request.path === '/v1/self/credential/step-up') return noContent();
      if (request.path === '/v1/self/credential/step-up/verify') {
        return json(200, { step_up_token: 'spent', expires_in: 300 });
      }
      return listOf([]);
    });
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);
    fireEvent.click(await screen.findByRole('button', { name: 'Send code' }));
    fireEvent.change(await screen.findByLabelText('Verification code'), {
      target: { value: '111111' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Verify and continue' }));
    expect(await screen.findByText(/Paste exactly one public key line/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Verification code')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Add key' }));
    expect(await screen.findByRole('button', { name: 'Send code' })).toBeInTheDocument();
    const posts = requests.filter(isRegister);
    expect(posts).toHaveLength(3);
    expect(posts[2]?.headers['X-Step-Up-Token']).toBeUndefined();
  });

  test('step-up demanded again on the retry shows the challenge again', async () => {
    installFetch((request) => {
      if (isRegister(request)) return stepUpAction();
      if (request.path === '/v1/self/credential/step-up') return noContent();
      if (request.path === '/v1/self/credential/step-up/verify') {
        return json(200, { step_up_token: 'x', expires_in: 300 });
      }
      return listOf([]);
    });
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);
    fireEvent.click(await screen.findByRole('button', { name: 'Send code' }));
    fireEvent.change(await screen.findByLabelText('Verification code'), {
      target: { value: '222222' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Verify and continue' }));
    expect(await screen.findByRole('button', { name: 'Send code' })).toBeInTheDocument();
    // Exactly one retry; no automatic loop.
    expect(requests.filter(isRegister)).toHaveLength(2);
  });
});

describe('SSHKeysPanel action-required delegation', () => {
  const unverified = () =>
    actionRequired(403, 'users.email_unverified', 'Verify your email.', '/verify-email');

  test('email-unverified on register calls onActionRequired once and shows the alert', async () => {
    installFetch((request) => (isRegister(request) ? unverified() : listOf([])));
    const onActionRequired = mock((_error: ApiActionRequiredError) => {});
    render(<SSHKeysPanel onActionRequired={onActionRequired} />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);

    expect(
      await screen.findByText(/only after your email address is verified/),
    ).toBeInTheDocument();
    expect(onActionRequired).toHaveBeenCalledTimes(1);
    const error = onActionRequired.mock.calls[0]?.[0];
    expect(error).toBeInstanceOf(ApiActionRequiredError);
    expect(error?.path).toBe('/verify-email');
    expect(error?.code).toBe('users.email_unverified');
    expect(window.location.href).toBe(originalHref);
  });

  test('without a callback the alert still renders and nothing throws', async () => {
    installFetch((request) => (isRegister(request) ? unverified() : listOf([])));
    render(<SSHKeysPanel />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);
    expect(
      await screen.findByText(/only after your email address is verified/),
    ).toBeInTheDocument();
  });

  test('another action code surfaces the server message', async () => {
    installFetch((request) =>
      isRegister(request)
        ? actionRequired(403, 'users.something_else', 'Do the other thing.', '/other')
        : listOf([]),
    );
    const onActionRequired = mock((_error: ApiActionRequiredError) => {});
    render(<SSHKeysPanel onActionRequired={onActionRequired} />);
    await screen.findByText('No SSH keys yet.');
    fillAndSubmit(KEY_LINE);
    expect(await screen.findByText('Do the other thing.')).toBeInTheDocument();
    expect(onActionRequired).toHaveBeenCalledTimes(1);
  });
});

describe('SSHKeysPanel revoke', () => {
  test('confirms, deletes, closes the dialog, and reloads', async () => {
    let lists = 0;
    installFetch((request) => {
      if (request.method === 'DELETE') return noContent();
      lists += 1;
      return lists === 1 ? listOf([KEY_A]) : listOf([]);
    });
    render(<SSHKeysPanel />);
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Work laptop' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Work laptop')).toBeInTheDocument();
    expect(within(dialog).getByText(KEY_A.fingerprint)).toBeInTheDocument();
    expect(within(dialog).getByText(/stops working for new SSH connections/)).toBeInTheDocument();
    expect(requests.some((r) => r.method === 'DELETE')).toBe(false);

    fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke key' }));

    expect(await screen.findByText('No SSH keys yet.')).toBeInTheDocument();
    const del = requests.find((r) => r.method === 'DELETE');
    expect(del?.url).toBe(`${API_BASE_URL}/v1/self/ssh-keys/${KEY_A.uuid}`);
    expect(del?.headers['X-Step-Up-Token']).toBeUndefined();
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  test('cancelling the dialog sends no request', async () => {
    installFetch(() => listOf([KEY_A]));
    render(<SSHKeysPanel />);
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Work laptop' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(requests.some((r) => r.method === 'DELETE')).toBe(false);
  });

  test('a masked 403 shows the could-not-be-revoked message and reloads', async () => {
    let lists = 0;
    installFetch((request) => {
      if (request.method === 'DELETE') return apiError(403, 'forbidden', 'Forbidden');
      lists += 1;
      return lists === 1 ? listOf([KEY_A]) : listOf([]);
    });
    render(<SSHKeysPanel />);
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Work laptop' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke key' }));
    expect(
      await screen.findByText('This key could not be revoked. It may already have been removed.'),
    ).toBeInTheDocument();
    await waitFor(() => expect(lists).toBe(2));
  });

  test('email-unverified on revoke is delegated', async () => {
    installFetch((request) =>
      request.method === 'DELETE'
        ? actionRequired(403, 'users.email_unverified', 'Verify your email.', '/verify-email')
        : listOf([KEY_A]),
    );
    const onActionRequired = mock((_error: ApiActionRequiredError) => {});
    render(<SSHKeysPanel onActionRequired={onActionRequired} />);
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Work laptop' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke key' }));
    expect(
      await screen.findByText(/only after your email address is verified/),
    ).toBeInTheDocument();
    expect(onActionRequired).toHaveBeenCalledTimes(1);
  });
});

describe('SSHKeysPanel props and base URL', () => {
  test('uses the default and custom title and description', async () => {
    installFetch(() => listOf([]));
    const { unmount } = render(<SSHKeysPanel />);
    expect(await screen.findByText('SSH keys')).toBeInTheDocument();
    expect(screen.getByText(/authenticate git operations over SSH/)).toBeInTheDocument();
    unmount();

    render(<SSHKeysPanel title="Git keys" description="Custom text." />);
    expect(await screen.findByText('Git keys')).toBeInTheDocument();
    expect(screen.getByText('Custom text.')).toBeInTheDocument();
  });

  test('every request starts with the shared client base URL and ends with the expected path', async () => {
    installFetch((request) => {
      if (request.method === 'DELETE') return noContent();
      if (isRegister(request)) return json(201, KEY_A);
      return listOf([KEY_A]);
    });
    render(<SSHKeysPanel />);
    await screen.findByText('Work laptop');
    fillAndSubmit(KEY_LINE);
    await screen.findByText('SSH key added.');
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Work laptop' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke key' }));
    await waitFor(() => expect(requests.some((r) => r.method === 'DELETE')).toBe(true));

    expect(requests.length).toBeGreaterThanOrEqual(4);
    for (const request of requests) {
      expect(request.url.startsWith(API_BASE_URL)).toBe(true);
      expect(request.path.startsWith('/v1/')).toBe(true);
    }
    expect(requests[0]?.url).toBe(`${API_BASE_URL}/v1/self/ssh-keys?limit=200`);
    expect(requests.find(isRegister)?.url).toBe(`${API_BASE_URL}/v1/self/ssh-keys`);
    expect(requests.find((r) => r.method === 'DELETE')?.url).toBe(
      `${API_BASE_URL}/v1/self/ssh-keys/${KEY_A.uuid}`,
    );
  });
});
