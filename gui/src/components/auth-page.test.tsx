import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AuthProvider } from '../lib/auth-context';
import { resetUsersApiConfig } from '../lib/config';
import { AuthPage } from './auth-page';
import { EmailCodePage } from './email-code-page';
import { LoginForm } from './login-form';
import { RegisterForm } from './register-form';
import { errorReply, mockApi } from '../test-support/chrome-harness';
import { PROFILE_A, PROFILE_B, tokenResponse, USER_VIEW } from '../test-support/users-fixtures';

const originalFetch = globalThis.fetch;

beforeEach(() => {
  // fetchProviders() runs on LoginForm mount; return no providers.
  globalThis.fetch = (async () =>
    new Response('[]', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })) as unknown as typeof fetch;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  localStorage.clear();
  sessionStorage.clear();
  resetUsersApiConfig();
});

/** Lets the fetchProviders() effect settle so state updates don't leak. */
async function settle() {
  await waitFor(() => expect(document.getElementById('login-email')).not.toBeNull());
}

describe('AuthPage allowRegistration', () => {
  test('default shows "Create one" and toggles to the register panel', async () => {
    render(
      <AuthProvider>
        <AuthPage />
      </AuthProvider>,
    );
    await settle();
    const createOne = screen.getByRole('button', { name: 'Create one' });
    const registerHeading = screen.getByText('Create an account');
    expect(registerHeading.closest('.hidden')).not.toBeNull();
    fireEvent.click(createOne);
    expect(registerHeading.closest('.hidden')).toBeNull();
    expect(registerHeading.closest('.contents')).not.toBeNull();
  });

  test('false hides "Create one" and mounts no register panel', async () => {
    render(
      <AuthProvider>
        <AuthPage allowRegistration={false} />
      </AuthProvider>,
    );
    await settle();
    expect(screen.queryByText('Create one')).toBeNull();
    expect(screen.queryByText('Create an account')).toBeNull();
    expect(screen.queryByText('Already have an account?')).toBeNull();
    expect(document.getElementById('register-email')).toBeNull();
    expect(document.getElementById('register-password')).toBeNull();
  });

  test('false forces login mode even with initialMode="register"', async () => {
    render(
      <AuthProvider>
        <AuthPage allowRegistration={false} initialMode="register" />
      </AuthProvider>,
    );
    await settle();
    const signIn = screen.getByText('Sign in', { selector: '[data-slot="card-title"], div, h3' });
    expect(signIn.closest('.hidden')).toBeNull();
    expect(screen.queryByText('Create an account')).toBeNull();
  });
});

describe('Forgot password control', () => {
  test('is absent by default in LoginForm and AuthPage', async () => {
    const { unmount } = render(
      <AuthProvider>
        <LoginForm />
      </AuthProvider>,
    );
    await settle();
    expect(screen.queryByText('Forgot password?')).toBeNull();
    unmount();
    render(
      <AuthProvider>
        <AuthPage />
      </AuthProvider>,
    );
    await settle();
    expect(screen.queryByText('Forgot password?')).toBeNull();
  });

  test('calls the callback when present and does not submit the form', async () => {
    let calls = 0;
    let submits = 0;
    render(
      <AuthProvider>
        <div onSubmit={() => submits++}>
          <AuthPage onForgotPassword={() => calls++} />
        </div>
      </AuthProvider>,
    );
    await settle();
    const button = screen.getByRole('button', { name: 'Forgot password?' });
    expect(button.getAttribute('type')).toBe('button');
    fireEvent.click(button);
    expect(calls).toBe(1);
    expect(submits).toBe(0);
  });
});

// ─── Sign-in outcomes ────────────────────────────────────────────────────────

const SELECTION_RESPONSE = () =>
  tokenResponse({
    access_token: null,
    selection_required: true,
    profile: null,
    profiles: [PROFILE_A, PROFILE_B],
  });

function fillLogin(): void {
  fireEvent.change(document.getElementById('login-email')!, { target: { value: 'a@b.test' } });
  fireEvent.change(document.getElementById('login-password')!, { target: { value: 'pw' } });
  fireEvent.submit(document.getElementById('login-email')!.closest('form')!);
}

const baseRoutes = {
  'GET /v1/auth/providers': () => [],
  'GET /v1/session': () => ({ session: tokenResponse().session, profile: PROFILE_A, profiles: [PROFILE_A] }),
  'GET /v1/me': () => USER_VIEW,
};

describe('LoginForm outcomes', () => {
  test('selection_required navigates to the selection route, not the return path', async () => {
    mockApi({ ...baseRoutes, 'POST /v1/auth/login': SELECTION_RESPONSE });
    const selections: unknown[][] = [];
    let successes = 0;
    render(
      <AuthProvider>
        <LoginForm
          returnPath="/dash"
          onSuccess={() => successes++}
          onSelectionRequired={(...args) => selections.push(args)}
        />
      </AuthProvider>,
    );
    await settle();
    fillLogin();
    await waitFor(() => expect(selections).toEqual([['/auth/select-profile', '/dash']]));
    expect(successes).toBe(0);
  });

  test('selection_required without a handler hands onSuccess the selection route', async () => {
    mockApi({ ...baseRoutes, 'POST /v1/auth/login': SELECTION_RESPONSE });
    const paths: Array<string | null> = [];
    render(
      <AuthProvider>
        <LoginForm returnPath="/dash" onSuccess={(p) => paths.push(p)} />
      </AuthProvider>,
    );
    await settle();
    fillLogin();
    await waitFor(() => expect(paths).toEqual(['/auth/select-profile']));
  });

  test('a landed login continues to the return path', async () => {
    mockApi({ ...baseRoutes, 'POST /v1/auth/login': () => tokenResponse() });
    const paths: Array<string | null> = [];
    const selections: unknown[] = [];
    render(
      <AuthProvider>
        <LoginForm
          returnPath="/dash"
          onSuccess={(p) => paths.push(p)}
          onSelectionRequired={() => selections.push(1)}
        />
      </AuthProvider>,
    );
    await settle();
    fillLogin();
    await waitFor(() => expect(paths).toEqual(['/dash']));
    expect(selections).toEqual([]);
  });

  test('sends app on login only when configured', async () => {
    const calls = mockApi({ ...baseRoutes, 'POST /v1/auth/login': () => tokenResponse() });
    const { unmount } = render(
      <AuthProvider>
        <LoginForm app="shop" onSuccess={() => {}} />
      </AuthProvider>,
    );
    await settle();
    fillLogin();
    await waitFor(() => expect(calls.filter((c) => c.path === '/v1/auth/login')).toHaveLength(1));
    expect(calls.find((c) => c.path === '/v1/auth/login')!.body).toEqual({
      email: 'a@b.test',
      password: 'pw',
      app: 'shop',
    });
    unmount();
    cleanup();
    sessionStorage.clear();

    const calls2 = mockApi({ ...baseRoutes, 'POST /v1/auth/login': () => tokenResponse() });
    render(
      <AuthProvider>
        <LoginForm onSuccess={() => {}} />
      </AuthProvider>,
    );
    await settle();
    fillLogin();
    await waitFor(() => expect(calls2.filter((c) => c.path === '/v1/auth/login')).toHaveLength(1));
    expect(calls2.find((c) => c.path === '/v1/auth/login')!.body).toEqual({
      email: 'a@b.test',
      password: 'pw',
    });
  });

  test.each([
    [401, 'users.invalid_credentials', 'Invalid email or password.'],
    [403, 'users.byo_identity_disabled', /does not allow signing in or creating an account/],
    [403, 'users.app_login_not_granted', /not been granted access to this application/],
    [401, 'users.profile_unavailable', /profile for this account is not available/],
  ])('maps %i %s to readable copy', async (status, code, expectedMessage) => {
    mockApi({ ...baseRoutes, 'POST /v1/auth/login': () => errorReply(status as number, code as string, 'raw server text') });
    render(
      <AuthProvider>
        <LoginForm />
      </AuthProvider>,
    );
    await settle();
    fillLogin();
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toMatch(expectedMessage);
    expect(alert.textContent).not.toContain('raw server text');
  });
});

describe('RegisterForm outcomes', () => {
  function fillRegister(): void {
    fireEvent.change(document.getElementById('given-name')!, { target: { value: 'Ada' } });
    fireEvent.change(document.getElementById('family-name')!, { target: { value: 'Lovelace' } });
    fireEvent.change(document.getElementById('register-email')!, { target: { value: 'a@b.test' } });
    fireEvent.change(document.getElementById('register-password')!, {
      target: { value: 'pw1234567890ab' },
    });
    fireEvent.submit(document.getElementById('register-email')!.closest('form')!);
  }

  test('sends app and continues to the return path', async () => {
    const calls = mockApi({ ...baseRoutes, 'POST /v1/auth/register': () => tokenResponse() });
    const paths: Array<string | null> = [];
    render(
      <AuthProvider>
        <RegisterForm app="shop" returnPath="/welcome" onSuccess={(p) => paths.push(p)} />
      </AuthProvider>,
    );
    fillRegister();
    await waitFor(() => expect(paths).toEqual(['/welcome']));
    expect(calls.find((c) => c.path === '/v1/auth/register')!.body).toEqual({
      email: 'a@b.test',
      password: 'pw1234567890ab',
      given_name: 'Ada',
      family_name: 'Lovelace',
      app: 'shop',
    });
  });

  test('selection_required goes to the selection handler', async () => {
    mockApi({ ...baseRoutes, 'POST /v1/auth/register': SELECTION_RESPONSE });
    const selections: unknown[][] = [];
    render(
      <AuthProvider>
        <RegisterForm returnPath="/welcome" onSelectionRequired={(...a) => selections.push(a)} />
      </AuthProvider>,
    );
    fillRegister();
    await waitFor(() => expect(selections).toEqual([['/auth/select-profile', '/welcome']]));
  });

  test('users.byo_identity_disabled shows readable copy', async () => {
    mockApi({
      ...baseRoutes,
      'POST /v1/auth/register': () => errorReply(403, 'users.byo_identity_disabled', 'raw'),
    });
    render(
      <AuthProvider>
        <RegisterForm />
      </AuthProvider>,
    );
    fillRegister();
    expect((await screen.findByRole('alert')).textContent).toMatch(
      /does not allow signing in or creating an account/,
    );
  });
});

describe('AuthPage selection routing', () => {
  test('forwards onSelectionRequired with the mode', async () => {
    mockApi({ ...baseRoutes, 'POST /v1/auth/login': SELECTION_RESPONSE });
    const selections: unknown[][] = [];
    render(
      <AuthProvider>
        <AuthPage returnPath="/dash" onSelectionRequired={(...a) => selections.push(a)} />
      </AuthProvider>,
    );
    await settle();
    fillLogin();
    await waitFor(() =>
      expect(selections).toEqual([['/auth/select-profile', '/dash', 'login']]),
    );
  });
});

describe('EmailCodePage sign-in', () => {
  async function signInWithCode(routes: Parameters<typeof mockApi>[0], page: React.ReactElement) {
    const calls = mockApi({ ...baseRoutes, 'POST /v1/auth/email-code/request': () => undefined, ...routes });
    render(<AuthProvider>{page}</AuthProvider>);
    fireEvent.change(await screen.findByLabelText('Email'), { target: { value: 'a@b.test' } });
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
    fireEvent.change(await screen.findByLabelText('Code'), { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify code' }));
    return calls;
  }

  test('verifies through /v1/auth/email-code/verify and establishes the session', async () => {
    let succeeded = 0;
    const calls = await signInWithCode(
      { 'POST /v1/auth/email-code/verify': () => tokenResponse() },
      <EmailCodePage app="shop" onSuccess={() => succeeded++} />,
    );
    await waitFor(() => expect(succeeded).toBe(1));
    expect(calls.find((c) => c.path === '/v1/auth/email-code/verify')!.body).toEqual({
      email: 'a@b.test',
      code: '123456',
      app: 'shop',
    });
    expect(sessionStorage.length).toBeGreaterThan(0);
  });

  test('selection_required goes to the selection handler', async () => {
    const selections: unknown[][] = [];
    await signInWithCode(
      { 'POST /v1/auth/email-code/verify': SELECTION_RESPONSE },
      <EmailCodePage onSelectionRequired={(...a) => selections.push(a)} />,
    );
    await waitFor(() => expect(selections).toEqual([['/auth/select-profile', null]]));
  });

  test('users.invalid_credentials reads as a code problem', async () => {
    await signInWithCode(
      { 'POST /v1/auth/email-code/verify': () => errorReply(401, 'users.invalid_credentials') },
      <EmailCodePage />,
    );
    expect((await screen.findByRole('alert')).textContent).toContain('Invalid or expired code.');
  });
});
