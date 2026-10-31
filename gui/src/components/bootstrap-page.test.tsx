import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { jwtFixture } from '../test-support/jwt';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { BootstrapPage } from './bootstrap-page';
import { AuthProvider, useAuth } from '../lib/auth-context';
import { getStoredToken } from '../lib/config';
import { PROFILE_A, tokenResponse, USER_VIEW } from '../test-support/users-fixtures';
import {
  errorReply,
  jsonResponse,
  mockApi,
  setUpHarness,
  tearDownHarness,
  type Call,
} from '../test-support/chrome-harness';

beforeEach(setUpHarness);
afterEach(tearDownHarness);

/** The headers of the last fetch to `path`, captured by wrapping the scripted fetch. */
let lastHeaders: Record<string, string> = {};
let lastCredentials: RequestCredentials | undefined;

function script(routes: Parameters<typeof mockApi>[0]): Call[] {
  const calls = mockApi(routes);
  const inner = globalThis.fetch;
  globalThis.fetch = (async (url: string, init: RequestInit = {}) => {
    if (String(url).endsWith('/v1/bootstrap')) {
      lastHeaders = init.headers instanceof Headers
        ? Object.fromEntries(init.headers.entries())
        : { ...(init.headers as Record<string, string>) };
      lastCredentials = init.credentials;
    }
    return inner(url, init);
  }) as unknown as typeof fetch;
  return calls;
}

const NOT_BOOTSTRAPPED = { 'GET /v1/bootstrap/status': () => ({ bootstrapped: false, oidc_confirmed: false }) };

function field(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

function fillAdmin() {
  field('First name', 'Ada');
  field('Last name', 'Lovelace');
  field('Administrator email', 'ada@example.test');
  field('Password', 'correct horse battery');
}

function fillOrganization() {
  field('Legal name', 'Acme Corp.');
  field('Legal form', 'llc');
  field('Jurisdiction (optional)', 'Delaware, USA');
}

describe('BootstrapPage', () => {
  test('shows already-set-up with a sign-in link when the deployment is bootstrapped', async () => {
    script({ 'GET /v1/bootstrap/status': () => ({ bootstrapped: true, oidc_confirmed: true }) });
    render(<BootstrapPage />);
    expect(await screen.findByText('Already set up')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Go to sign in' })).toHaveAttribute('href', '/auth/login');
    expect(screen.queryByLabelText('Setup token')).toBeNull();
  });

  test('the sign-in link calls onNavigateToLogin when supplied', async () => {
    script({ 'GET /v1/bootstrap/status': () => ({ bootstrapped: true, oidc_confirmed: true }) });
    let navigated = 0;
    render(<BootstrapPage onNavigateToLogin={() => navigated++} />);
    fireEvent.click(await screen.findByRole('link', { name: 'Go to sign in' }));
    expect(navigated).toBe(1);
  });

  test('a status failure shows the error and no form', async () => {
    script({ 'GET /v1/bootstrap/status': () => errorReply(500, 'internal_error', 'database unavailable') });
    render(<BootstrapPage />);
    expect(await screen.findByText('Could not load setup status.')).toBeInTheDocument();
    expect(screen.getByRole('alert').textContent).toContain('database unavailable');
    expect(screen.queryByLabelText('Setup token')).toBeNull();
  });

  test('organization bootstrap posts the setup token header, stores the session and calls onComplete', async () => {
    const calls = script({
      ...NOT_BOOTSTRAPPED,
      'POST /v1/bootstrap': () => jsonResponse(tokenResponse({ access_token: 'admin-access' }), 201),
    });
    let completed: string | null = null;
    render(<BootstrapPage onComplete={(r) => (completed = r.access_token)} />);
    await screen.findByLabelText('Setup token');

    field('Setup token', '  tok-123  ');
    fillOrganization();
    fillAdmin();
    fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));

    await waitFor(() => expect(completed).toBe('admin-access'));
    const post = calls.find((c) => c.method === 'POST' && c.path === '/v1/bootstrap')!;
    expect(post.body).toEqual({
      operator: { kind: 'organization', legal_name: 'Acme Corp.', legal_form: 'llc', jurisdiction: 'Delaware, USA' },
      admin: {
        email: 'ada@example.test',
        password: 'correct horse battery',
        given_name: 'Ada',
        family_name: 'Lovelace',
      },
    });
    expect(lastHeaders['X-Setup-Token'] ?? lastHeaders['x-setup-token']).toBe('tok-123');
    expect(lastCredentials).toBe('omit');
    // The client committed the 201 token response to the tab.
    expect(getStoredToken()).toBe('admin-access');
    expect(screen.getByText('Setup complete')).toBeInTheDocument();
  });

  test('a natural-person operator sends no organization details and hides those fields', async () => {
    const calls = script({
      ...NOT_BOOTSTRAPPED,
      'POST /v1/bootstrap': () => jsonResponse(tokenResponse(), 201),
    });
    render(<BootstrapPage />);
    await screen.findByLabelText('Setup token');
    fireEvent.click(screen.getByLabelText('A single person (self-hosted)'));
    expect(screen.queryByLabelText('Legal name')).toBeNull();

    field('Setup token', 'tok');
    fillAdmin();
    fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
    await screen.findByText('Setup complete');
    const post = calls.find((c) => c.method === 'POST' && c.path === '/v1/bootstrap')!;
    expect((post.body as { operator: unknown }).operator).toEqual({ kind: 'natural_person' });
  });

  test('offers the mod-core organization legal forms', async () => {
    script(NOT_BOOTSTRAPPED);
    render(<BootstrapPage />);
    const select = (await screen.findByLabelText('Legal form')) as HTMLSelectElement;
    const values = Array.from(select.options).map((o) => o.value).filter((v) => v !== '');
    expect(values).toEqual(['corporation', 'llc', 'partnership', 'nonprofit', 'public_body', 'trust']);
  });

  test('omits an empty jurisdiction', async () => {
    const calls = script({
      ...NOT_BOOTSTRAPPED,
      'POST /v1/bootstrap': () => jsonResponse(tokenResponse(), 201),
    });
    render(<BootstrapPage />);
    await screen.findByLabelText('Setup token');
    field('Setup token', 'tok');
    field('Legal name', 'Acme');
    field('Legal form', 'trust');
    fillAdmin();
    fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
    await screen.findByText('Setup complete');
    const post = calls.find((c) => c.method === 'POST' && c.path === '/v1/bootstrap')!;
    expect((post.body as { operator: Record<string, unknown> }).operator).toEqual({
      kind: 'organization',
      legal_name: 'Acme',
      legal_form: 'trust',
    });
  });

  test('401 users.setup_token_invalid asks for the token again and keeps the form', async () => {
    script({
      ...NOT_BOOTSTRAPPED,
      'POST /v1/bootstrap': () => errorReply(401, 'users.setup_token_invalid', 'bad token'),
    });
    render(<BootstrapPage />);
    await screen.findByLabelText('Setup token');
    field('Setup token', 'wrong');
    fillOrganization();
    fillAdmin();
    fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
    expect((await screen.findByRole('alert')).textContent).toContain('The setup token is not valid');
    expect(screen.getByLabelText('Legal name')).toHaveValue('Acme Corp.');
    expect(screen.getByLabelText('Administrator email')).toHaveValue('ada@example.test');
  });

  test('409 users.already_bootstrapped switches to the already-set-up state', async () => {
    script({
      ...NOT_BOOTSTRAPPED,
      'POST /v1/bootstrap': () => errorReply(409, 'users.already_bootstrapped', 'already'),
    });
    render(<BootstrapPage />);
    await screen.findByLabelText('Setup token');
    field('Setup token', 'tok');
    fillOrganization();
    fillAdmin();
    fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
    expect(await screen.findByText('Already set up')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Go to sign in' })).toBeInTheDocument();
  });

  test('a server validation error shows its field details', async () => {
    script({
      ...NOT_BOOTSTRAPPED,
      'POST /v1/bootstrap': () =>
        jsonResponse(
          {
            error: {
              code: 'invalid_input',
              message: 'invalid input',
              details: [{ field: 'admin.password', code: 'users.password_weak', message: 'Password is too short.' }],
            },
          },
          400,
        ),
    });
    render(<BootstrapPage />);
    await screen.findByLabelText('Setup token');
    field('Setup token', 'tok');
    fillOrganization();
    fillAdmin();
    fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
    expect((await screen.findByRole('alert')).textContent).toContain('Password is too short.');
  });

  test('an organization without a legal form is refused before any request', async () => {
    const calls = script(NOT_BOOTSTRAPPED);
    render(<BootstrapPage />);
    await screen.findByLabelText('Setup token');
    field('Setup token', 'tok');
    field('Legal name', 'Acme');
    fillAdmin();
    // `required` is bypassed by submitting the form directly.
    fireEvent.submit(screen.getByRole('button', { name: 'Create administrator' }).closest('form')!);
    expect((await screen.findByRole('alert')).textContent).toContain('choose a legal form');
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
  });

  test('under an AuthProvider the committed session lands the context on the administrator profile', async () => {
    script({
      ...NOT_BOOTSTRAPPED,
      'POST /v1/bootstrap': () => jsonResponse(tokenResponse({ profile: PROFILE_A }), 201),
      'GET /v1/me': () => USER_VIEW,
    });
    let token: string | null = null;
    function Probe() {
      token = useAuth().token;
      return null;
    }
    render(
      <AuthProvider>
        <Probe />
        <BootstrapPage />
      </AuthProvider>,
    );
    await screen.findByLabelText('Setup token');
    field('Setup token', 'tok');
    fillOrganization();
    fillAdmin();
    fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }));
    await screen.findByText('Setup complete');
    await waitFor(() => expect(token).toBe(jwtFixture('access-1')));
  });
});
