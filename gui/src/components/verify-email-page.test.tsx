import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { VerifyEmailPage } from './verify-email-page';
import { AuthProvider } from '../lib/auth-context';
import { configureUsersApi, resetUsersApiConfig, setStoredRefreshToken, setStoredToken } from '../lib/config';
import { PROFILE_A, tokenResponse, USER_VIEW } from '../test-support/users-fixtures';

const originalFetch = globalThis.fetch;

interface Call {
  url: string;
  method: string;
  body: unknown;
}

let calls: Call[];
let responder: (call: Call) => Response;

const IDENTIFIER = {
  uuid: 'ident-1',
  kind: 'email',
  value: 'me@example.com',
  is_primary: true,
  verified: false,
  verified_at: null,
  created_at: '2026-01-01T00:00:00Z',
};

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** The default server: one unverified address; verification and resend answer 200 and 204. */
function defaultResponder(call: Call): Response {
  if (call.url.endsWith('/v1/me/identifiers') && call.method === 'GET') {
    return json(200, { identifiers: [IDENTIFIER] });
  }
  if (call.url.endsWith('/verify')) return json(200, { ...IDENTIFIER, verified: true });
  if (call.url.endsWith('/v1/session')) {
    const base = tokenResponse();
    return json(200, { session: base.session, profile: PROFILE_A, profiles: [PROFILE_A] });
  }
  if (call.url.endsWith('/v1/me')) return json(200, { ...USER_VIEW, email: 'session@example.com' });
  return new Response(null, { status: 204 });
}

beforeEach(() => {
  calls = [];
  // A signed-in tab: the identifier routes need a session.
  setStoredToken('tok');
  setStoredRefreshToken('refresh');
  responder = defaultResponder;
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    const call: Call = {
      url: String(url),
      method: init?.method ?? 'GET',
      body: init?.body ? JSON.parse(init.body as string) : undefined,
    };
    calls.push(call);
    return responder(call);
  }) as unknown as typeof fetch;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  localStorage.clear();
  sessionStorage.clear();
  resetUsersApiConfig();
});

function typeCode(value: string): void {
  fireEvent.change(screen.getByLabelText('Code'), { target: { value } });
}

const callsTo = (suffix: string) => calls.filter((c) => c.url.endsWith(suffix));

describe('VerifyEmailPage', () => {
  test('send a new code resolves the identifier and posts its verification to the configured base URL', async () => {
    configureUsersApi({ baseUrl: 'https://api.example.test' });
    render(<VerifyEmailPage email="me@example.com" resendCooldownSeconds={0} />);

    fireEvent.click(screen.getByRole('button', { name: 'Send a new code' }));

    await waitFor(() => expect(callsTo('/v1/me/identifiers/ident-1/verification')).toHaveLength(1));
    const call = callsTo('/v1/me/identifiers/ident-1/verification')[0]!;
    expect(call.method).toBe('POST');
    expect(call.url).toBe('https://api.example.test/v1/me/identifiers/ident-1/verification');

    const status = await screen.findByText(
      'A new code was sent to me@example.com. It expires in 5 minutes.',
    );
    expect(status.closest('[aria-live="polite"]')).not.toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText('Code')));
    // No retired email-code purpose is used for verification any more.
    expect(callsTo('/v1/auth/email-code/request')).toHaveLength(0);
  });

  test('does not call the API on mount', () => {
    render(<VerifyEmailPage email="me@example.com" />);
    expect(calls).toHaveLength(0);
  });

  test('verify posts {code} to the identifier and a 200 calls onVerified once, with a success state', async () => {
    let verified = 0;
    render(<VerifyEmailPage email="me@example.com" onVerified={() => verified++} />);

    typeCode('123456');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));

    await waitFor(() => expect(verified).toBe(1));
    const call = callsTo('/v1/me/identifiers/ident-1/verify')[0]!;
    expect(call.method).toBe('POST');
    expect(call.url).toBe('http://localhost:8080/v1/me/identifiers/ident-1/verify');
    expect(call.body).toEqual({ code: '123456' });
    expect((await screen.findAllByText('Email verified')).length).toBeGreaterThan(0);
    expect(verified).toBe(1);
  });

  test('the identifier is matched to the email case-insensitively and looked up once', async () => {
    responder = (call) =>
      call.url.endsWith('/v1/me/identifiers') && call.method === 'GET'
        ? json(200, {
            identifiers: [
              { ...IDENTIFIER, uuid: 'other', value: 'other@example.com' },
              { ...IDENTIFIER, uuid: 'mine', value: 'Me@Example.com' },
            ],
          })
        : defaultResponder(call);
    render(<VerifyEmailPage email="me@example.com" resendCooldownSeconds={0} />);
    fireEvent.click(screen.getByRole('button', { name: 'Send a new code' }));
    await waitFor(() => expect(callsTo('/v1/me/identifiers/mine/verification')).toHaveLength(1));
    typeCode('123456');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));
    await waitFor(() => expect(callsTo('/v1/me/identifiers/mine/verify')).toHaveLength(1));
    expect(calls.filter((c) => c.url.endsWith('/v1/me/identifiers') && c.method === 'GET')).toHaveLength(1);
  });

  test('wrong code (400 users.code_invalid) shows the fixed message and neither calls onVerified nor clears the token', async () => {
    responder = (call) =>
      call.url.endsWith('/verify')
        ? json(400, {
            error: {
              code: 'invalid_input',
              message: 'invalid input',
              details: [{ field: 'code', code: 'users.code_invalid', message: 'the code is invalid or has expired' }],
            },
          })
        : defaultResponder(call);
    let verified = 0;
    render(<VerifyEmailPage email="me@example.com" onVerified={() => verified++} />);

    typeCode('000000');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('That code is invalid or has expired. Request a new one.');
    expect(verified).toBe(0);
    expect(sessionStorage.length).toBeGreaterThan(0);
  });

  test('other API errors show their message', async () => {
    responder = (call) =>
      call.url.endsWith('/verify')
        ? json(400, { error: { code: 'invalid_input', message: 'missing fields' } })
        : defaultResponder(call);
    render(<VerifyEmailPage email="me@example.com" />);
    typeCode('123456');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));
    expect((await screen.findByRole('alert')).textContent).toContain('missing fields');
  });

  test('email from the provider user is used and shown', async () => {
    responder = (call) =>
      call.url.endsWith('/v1/me/identifiers') && call.method === 'GET'
        ? json(200, { identifiers: [{ ...IDENTIFIER, value: 'session@example.com' }] })
        : defaultResponder(call);
    render(
      <AuthProvider>
        <VerifyEmailPage resendCooldownSeconds={0} />
      </AuthProvider>,
    );
    await screen.findByText('session@example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Send a new code' }));
    await waitFor(() => expect(callsTo('/v1/me/identifiers/ident-1/verification')).toHaveLength(1));
  });

  test('the email prop wins over the provider user', async () => {
    render(
      <AuthProvider>
        <VerifyEmailPage email="prop@example.com" resendCooldownSeconds={0} />
      </AuthProvider>,
    );
    await waitFor(() => expect(callsTo('/v1/me')).toHaveLength(1));
    await screen.findByText('prop@example.com');
    expect(screen.queryByText('session@example.com')).toBeNull();
  });

  test('without a known email, the first unverified address is verified and then shown', async () => {
    render(<VerifyEmailPage resendCooldownSeconds={0} />);
    expect(screen.queryByLabelText('Email')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Send a new code' }));

    await waitFor(() => expect(callsTo('/v1/me/identifiers/ident-1/verification')).toHaveLength(1));
    await screen.findAllByText('me@example.com', { exact: false });
  });

  test('an account with no email address reports it and sends nothing', async () => {
    responder = (call) =>
      call.url.endsWith('/v1/me/identifiers') && call.method === 'GET'
        ? json(200, { identifiers: [] })
        : defaultResponder(call);
    render(<VerifyEmailPage resendCooldownSeconds={0} />);
    fireEvent.click(screen.getByRole('button', { name: 'Send a new code' }));
    expect((await screen.findByRole('alert')).textContent).toContain(
      'No email address on this account needs verification.',
    );
    expect(callsTo('/verification')).toHaveLength(0);
  });

  test('an already verified identifier completes without sending a code', async () => {
    responder = (call) =>
      call.url.endsWith('/v1/me/identifiers') && call.method === 'GET'
        ? json(200, { identifiers: [{ ...IDENTIFIER, verified: true }] })
        : defaultResponder(call);
    let verified = 0;
    render(<VerifyEmailPage email="me@example.com" onVerified={() => verified++} />);
    typeCode('123456');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));
    await waitFor(() => expect(verified).toBe(1));
    expect(callsTo('/verify')).toHaveLength(0);
  });

  test('resend cooldown disables then re-enables the button', async () => {
    render(<VerifyEmailPage email="me@example.com" resendCooldownSeconds={1} />);
    const send = screen.getByRole('button', { name: 'Send a new code' }) as HTMLButtonElement;
    fireEvent.click(send);

    await waitFor(() => expect(send.disabled).toBe(true));
    expect(send.getAttribute('aria-describedby')).not.toBeNull();
    expect(screen.getByText(/You can request another code in 1s/)).toBeTruthy();

    await waitFor(() => expect(send.disabled).toBe(false), { timeout: 3000 });
    expect(screen.queryByText(/You can request another code/)).toBeNull();
  });

  test('configureUsersApi({ baseUrl: "" }) yields relative URLs', async () => {
    configureUsersApi({ baseUrl: '' });
    render(<VerifyEmailPage email="me@example.com" resendCooldownSeconds={0} />);
    typeCode('123456');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));
    await waitFor(() => expect(callsTo('/v1/me/identifiers/ident-1/verify')).toHaveLength(1));
    expect(callsTo('/v1/me/identifiers/ident-1/verify')[0]!.url).toBe(
      '/v1/me/identifiers/ident-1/verify',
    );
  });

  test('code input strips non-digits and has the right attributes; labels present', () => {
    render(<VerifyEmailPage email="me@example.com" />);
    const code = screen.getByLabelText('Code') as HTMLInputElement;
    expect(code.getAttribute('autocomplete')).toBe('one-time-code');
    expect(code.getAttribute('inputmode')).toBe('numeric');
    expect(code.maxLength).toBe(6);
    typeCode('12a3-4');
    expect(code.value).toBe('1234');
  });

  test('refreshes the user inside an AuthProvider before calling onVerified', async () => {
    const order: string[] = [];
    render(
      <AuthProvider>
        <VerifyEmailPage
          email="me@example.com"
          onVerified={() => order.push(`verified@${callsTo('/v1/session').length}`)}
        />
      </AuthProvider>,
    );
    await waitFor(() => expect(callsTo('/v1/me')).toHaveLength(1));
    const sessionBefore = callsTo('/v1/session').length;
    typeCode('123456');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));
    await waitFor(() => expect(order).toHaveLength(1));
    expect(callsTo('/v1/session').length).toBe(sessionBefore + 1);
    expect(order[0]).toBe(`verified@${sessionBefore + 1}`);
  });

  test('renders without an AuthProvider and offers the sign-in-different-account button', () => {
    let navigated = 0;
    render(<VerifyEmailPage email="me@example.com" onNavigateToLogin={() => navigated++} />);
    fireEvent.click(screen.getByRole('button', { name: 'Sign in with a different account' }));
    expect(navigated).toBe(1);
    expect(
      screen.getByText('Verify your email address before continuing.', { exact: false }),
    ).toBeTruthy();
  });

  test('custom message replaces the default lead sentence', () => {
    render(<VerifyEmailPage email="me@example.com" message="Please confirm it is you." />);
    expect(screen.getByText('Please confirm it is you.', { exact: false })).toBeTruthy();
  });
});
