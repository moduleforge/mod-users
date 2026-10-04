import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { VerifyEmailPage } from './verify-email-page';
import { AuthProvider } from '../lib/auth-context';
import { configureUsersApi, resetUsersApiConfig } from '../lib/config';

const originalFetch = globalThis.fetch;

interface Call {
  url: string;
  method: string;
  body: unknown;
}

let calls: Call[];
let responder: (call: Call) => Response;

const SELF = {
  uuid: 'u1',
  entity_uuid: 'e1',
  email: 'session@example.com',
  given_name: 'A',
  family_name: 'B',
  is_admin: false,
  created_at: '',
  updated_at: '',
};

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  calls = [];
  responder = (call) =>
    call.url.endsWith('/v1/self') ? json(200, SELF) : new Response(null, { status: 204 });
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
  globalThis.fetch = originalFetch;
  localStorage.clear();
  resetUsersApiConfig();
});

function typeCode(value: string): void {
  fireEvent.change(screen.getByLabelText('Code'), { target: { value } });
}

const callsTo = (suffix: string) => calls.filter((c) => c.url.endsWith(suffix));

describe('VerifyEmailPage', () => {
  test('send a new code posts purpose verify_email to the configured base URL and shows a polite status', async () => {
    configureUsersApi({ baseUrl: 'https://api.example.test' });
    render(<VerifyEmailPage email="me@example.com" resendCooldownSeconds={0} />);

    fireEvent.click(screen.getByRole('button', { name: 'Send a new code' }));

    await waitFor(() => expect(callsTo('/v1/auth/email-code/request')).toHaveLength(1));
    const call = callsTo('/v1/auth/email-code/request')[0]!;
    expect(call.url).toBe('https://api.example.test/v1/auth/email-code/request');
    expect(call.body).toEqual({ email: 'me@example.com', purpose: 'verify_email' });

    const status = await screen.findByText(
      'A new code was sent to me@example.com. It expires in 5 minutes.',
    );
    expect(status.closest('[aria-live="polite"]')).not.toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText('Code')));
  });

  test('does not send a code on mount', () => {
    render(<VerifyEmailPage email="me@example.com" />);
    expect(calls).toHaveLength(0);
  });

  test('verify posts {email, code, purpose} and a 204 calls onVerified once, with a success state', async () => {
    let verified = 0;
    render(<VerifyEmailPage email="me@example.com" onVerified={() => verified++} />);

    typeCode('123456');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));

    await waitFor(() => expect(verified).toBe(1));
    const call = callsTo('/v1/auth/email-code/verify')[0]!;
    expect(call.url).toBe('http://localhost:8080/v1/auth/email-code/verify');
    expect(call.body).toEqual({ email: 'me@example.com', code: '123456', purpose: 'verify_email' });
    expect((await screen.findAllByText('Email verified')).length).toBeGreaterThan(0);
    expect(verified).toBe(1);
  });

  test('wrong code (401) shows the fixed message and neither calls onVerified nor clears the token', async () => {
    localStorage.setItem('auth_token', 'keep-me');
    responder = () =>
      json(401, { error: { code: 'unauthenticated', message: 'invalid or expired code' } });
    let verified = 0;
    render(<VerifyEmailPage email="me@example.com" onVerified={() => verified++} />);

    typeCode('000000');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('That code is invalid or has expired. Request a new one.');
    expect(verified).toBe(0);
    expect(localStorage.getItem('auth_token')).toBe('keep-me');
  });

  test('other API errors show their message', async () => {
    responder = () => json(400, { error: { code: 'invalid_input', message: 'missing fields' } });
    render(<VerifyEmailPage email="me@example.com" />);
    typeCode('123456');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));
    expect((await screen.findByRole('alert')).textContent).toContain('missing fields');
  });

  test('email from the provider user is used and shown', async () => {
    localStorage.setItem('auth_token', 'tok');
    render(
      <AuthProvider>
        <VerifyEmailPage resendCooldownSeconds={0} />
      </AuthProvider>,
    );
    await screen.findByText('session@example.com');
    expect(screen.queryByLabelText('Email')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Send a new code' }));
    await waitFor(() => expect(callsTo('/v1/auth/email-code/request')).toHaveLength(1));
    expect(callsTo('/v1/auth/email-code/request')[0]!.body).toEqual({
      email: 'session@example.com',
      purpose: 'verify_email',
    });
  });

  test('the email prop wins over the provider user', async () => {
    localStorage.setItem('auth_token', 'tok');
    render(
      <AuthProvider>
        <VerifyEmailPage email="prop@example.com" resendCooldownSeconds={0} />
      </AuthProvider>,
    );
    await waitFor(() => expect(callsTo('/v1/self')).toHaveLength(1));
    await screen.findByText('prop@example.com');
    expect(screen.queryByText('session@example.com')).toBeNull();
  });

  test('without a known email, an Email field is shown and its value is used', async () => {
    render(<VerifyEmailPage resendCooldownSeconds={0} />);
    const send = screen.getByRole('button', { name: 'Send a new code' }) as HTMLButtonElement;
    expect(send.disabled).toBe(true);

    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'typed@example.com' } });
    expect(send.disabled).toBe(false);
    fireEvent.click(send);

    await waitFor(() => expect(callsTo('/v1/auth/email-code/request')).toHaveLength(1));
    expect(callsTo('/v1/auth/email-code/request')[0]!.body).toEqual({
      email: 'typed@example.com',
      purpose: 'verify_email',
    });
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
    await waitFor(() => expect(callsTo('/v1/auth/email-code/verify')).toHaveLength(1));
    expect(callsTo('/v1/auth/email-code/verify')[0]!.url).toBe('/v1/auth/email-code/verify');
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
    localStorage.setItem('auth_token', 'tok');
    const order: string[] = [];
    render(
      <AuthProvider>
        <VerifyEmailPage onVerified={() => order.push(`verified@${callsTo('/v1/self').length}`)} />
      </AuthProvider>,
    );
    await screen.findByText('session@example.com');
    const selfBefore = callsTo('/v1/self').length;
    typeCode('123456');
    fireEvent.click(screen.getByRole('button', { name: 'Verify email' }));
    await waitFor(() => expect(order).toHaveLength(1));
    expect(callsTo('/v1/self').length).toBe(selfBefore + 1);
    expect(order[0]).toBe(`verified@${selfBefore + 1}`);
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
