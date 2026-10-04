import { afterEach, beforeEach, describe, expect, jest, test } from 'bun:test';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { OidcConfigPage } from './oidc-config-page';
import { AuthProvider } from '../lib/auth-context';
import { configureUsersApi, resetUsersApiConfig } from '../lib/config';
import type { OIDCStatus } from '../lib/oidc-config';
import type { OIDCProviderView } from '../lib/oidc-provider';

const originalFetch = globalThis.fetch;
const originalOpen = window.open;
const happyDOM = (window as unknown as { happyDOM: { setURL(url: string): void } }).happyDOM;

interface Call {
  url: string;
  method: string;
  headers: Record<string, string>;
  body: unknown;
}

let calls: Call[];
/** Per-request override, keyed by `${METHOD} ${path}` (path without the base URL). */
let routes: Record<string, (call: Call) => Response | Promise<Response>>;

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function status(overrides: Partial<OIDCStatus> = {}): OIDCStatus {
  return {
    state: 'no_env_no_flag',
    confirmed: false,
    no_oidc_accounts_env: false,
    needs_setup_token: true,
    providers: [
      { id: 'google', display_name: 'Google', configured: true, enabled: true, init_ok: true, error: null },
      { id: 'microsoft', display_name: 'Microsoft', configured: true, enabled: false, init_ok: false, error: 'bad issuer' },
      { id: 'authelia', display_name: 'Authelia', configured: false, enabled: false, init_ok: false, error: null },
    ],
    ...overrides,
  };
}

const GOOGLE_VIEW: OIDCProviderView = {
  id: 'google',
  display_name: null,
  display_name_default: 'Google',
  display_name_source: 'well_known',
  issuer_url: null,
  issuer_url_default: 'https://accounts.google.com',
  issuer_url_source: 'well_known',
  client_id: null,
  client_id_default: 'cid',
  client_id_source: 'env',
  has_client_secret: true,
  client_secret_source: 'env',
  claim_style: null,
  claim_style_default: 'google',
  claim_style_source: 'well_known',
  scopes: null,
  scopes_default: ['openid', 'email', 'profile'],
  scopes_source: 'well_known',
  enabled: true,
  init_ok: true,
  callback_url: 'http://localhost:8080/v1/auth/oidc/google/callback',
  well_known: true,
};

const ADMIN = {
  uuid: 'u1',
  entity_uuid: 'e1',
  email: 'admin@example.com',
  given_name: 'A',
  family_name: 'B',
  is_admin: true,
  created_at: '',
  updated_at: '',
};

function pathOf(url: string): string {
  return url.replace(/^https?:\/\/[^/]+/, '');
}

beforeEach(() => {
  calls = [];
  routes = {};
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    const call: Call = {
      url: String(url),
      method: init?.method ?? 'GET',
      headers: { ...((init?.headers as Record<string, string>) ?? {}) },
      body: init?.body ? JSON.parse(init.body as string) : undefined,
    };
    calls.push(call);
    const key = `${call.method} ${pathOf(call.url).split('?')[0]}`;
    const handler = routes[key];
    if (handler) return handler(call);
    if (key === 'GET /v1/oidc-config/status') return json(200, status());
    if (key === 'GET /v1/self') return json(200, ADMIN);
    return json(404, { error: { code: 'not_found', message: `unrouted ${key}` } });
  }) as unknown as typeof fetch;
});

afterEach(() => {
  jest.useRealTimers();
  globalThis.fetch = originalFetch;
  window.open = originalOpen;
  localStorage.clear();
  resetUsersApiConfig();
  happyDOM.setURL('about:blank');
});

const callsTo = (method: string, path: string) =>
  calls.filter((c) => c.method === method && pathOf(c.url) === path);

async function renderLoaded(ui: React.ReactElement = <OidcConfigPage />) {
  const utils = render(ui);
  await screen.findByRole('switch', { name: 'Google' });
  return utils;
}

function typeToken(value: string) {
  fireEvent.change(screen.getByLabelText('Setup token'), { target: { value } });
}

const saveButton = () => screen.getByRole('button', { name: 'Save' });

/**
 * Boolean, not the element: a failing `expect(element)` makes bun format the
 * live DOM node (React fiber props included), which exhausts memory while
 * `waitFor` retries.
 */
const dialogOpen = () => document.querySelector('[role="dialog"]') !== null;

/** Flush pending promise callbacks without relying on (possibly faked) timers. */
async function flushMicrotasks() {
  await act(async () => {
    for (let i = 0; i < 20; i++) await Promise.resolve();
  });
}

describe('OidcConfigPage: loading and status', () => {
  test('shows loading, then the provider list with badges from /v1/oidc-config/status', async () => {
    render(<OidcConfigPage />);
    expect(screen.getByText('Loading status...')).toBeInTheDocument();

    const google = await screen.findByRole('switch', { name: 'Google' });
    expect(google).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('switch', { name: 'Microsoft' })).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByRole('switch', { name: 'Authelia' })).toBeDisabled();
    expect(screen.getByText('OK')).toBeInTheDocument();
    expect(screen.getByText('Failed')).toBeInTheDocument();
    expect(screen.getByText('Init error: bad issuer')).toBeInTheDocument();
    expect(screen.getByText('OIDC configuration')).toBeInTheDocument();
    expect(
      screen.getByText('Paste the setup token from the server logs and choose which providers to enable.'),
    ).toBeInTheDocument();
    expect(callsTo('GET', '/v1/oidc-config/status')[0]!.url).toBe(
      'http://localhost:8080/v1/oidc-config/status',
    );
  });

  test('a status fetch failure shows the error card', async () => {
    routes['GET /v1/oidc-config/status'] = () =>
      json(500, { error: { code: 'internal_error', message: 'database unavailable' } });
    render(<OidcConfigPage />);
    expect(await screen.findByText('Could not load status.')).toBeInTheDocument();
    expect(screen.getByRole('alert').textContent).toContain('database unavailable');
  });

  test('an empty provider list shows the registration hint', async () => {
    routes['GET /v1/oidc-config/status'] = () => json(200, status({ providers: [] }));
    render(<OidcConfigPage />);
    expect(
      await screen.findByText('No providers registered. Set provider env vars and restart the API.'),
    ).toBeInTheDocument();
  });
});

describe('OidcConfigPage: token mode', () => {
  test('Save stays disabled until a toggle changes (dirty detection) and a token is present', async () => {
    await renderLoaded();
    expect(saveButton()).toBeDisabled();
    typeToken('tok');
    expect(saveButton()).toBeDisabled();
    fireEvent.click(screen.getByRole('switch', { name: 'Microsoft' }));
    expect(saveButton()).toBeEnabled();
    typeToken('   ');
    expect(saveButton()).toBeDisabled();
    typeToken('tok');
    fireEvent.click(screen.getByRole('switch', { name: 'Microsoft' }));
    expect(saveButton()).toBeDisabled();
  });

  test('submit posts the trimmed token and configured+enabled providers; success calls onComplete after redirectDelayMs', async () => {
    let completed = 0;
    routes['POST /v1/oidc-config/confirm'] = () =>
      json(200, status({ confirmed: true, state: 'confirmed_ok' }));
    await renderLoaded(<OidcConfigPage onComplete={() => completed++} redirectDelayMs={1500} />);

    typeToken('  abc123  ');
    fireEvent.click(screen.getByRole('switch', { name: 'Microsoft' }));

    jest.useFakeTimers();
    fireEvent.click(saveButton());
    await flushMicrotasks();

    const confirm = callsTo('POST', '/v1/oidc-config/confirm');
    expect(confirm).toHaveLength(1);
    expect(confirm[0]!.body).toEqual({
      setup_token: 'abc123',
      enabled_providers: ['google', 'microsoft'],
      opt_out: false,
    });
    expect(confirm[0]!.headers.Authorization).toBeUndefined();

    expect(screen.getByText('Configuration saved')).toBeInTheDocument();
    expect(screen.getByText('Redirecting to the login page...')).toBeInTheDocument();
    // The single-use token field is gone with the form.
    expect(screen.queryByLabelText('Setup token')).toBeNull();

    act(() => {
      jest.advanceTimersByTime(1499);
    });
    expect(completed).toBe(0);
    act(() => {
      jest.advanceTimersByTime(1);
    });
    expect(completed).toBe(1);
  });

  test('the default redirect delay is 2000ms', async () => {
    let completed = 0;
    routes['POST /v1/oidc-config/confirm'] = () => json(200, status({ confirmed: true }));
    await renderLoaded(<OidcConfigPage onComplete={() => completed++} />);
    typeToken('abc');
    fireEvent.click(screen.getByRole('switch', { name: 'Microsoft' }));

    jest.useFakeTimers();
    fireEvent.click(saveButton());
    await flushMicrotasks();
    act(() => {
      jest.advanceTimersByTime(1999);
    });
    expect(completed).toBe(0);
    act(() => {
      jest.advanceTimersByTime(1);
    });
    expect(completed).toBe(1);
  });

  test('disabling every provider sends opt_out with an empty enabled set', async () => {
    routes['POST /v1/oidc-config/confirm'] = () => json(200, status({ confirmed: true }));
    await renderLoaded(<OidcConfigPage redirectDelayMs={0} />);
    typeToken('abc');
    fireEvent.click(screen.getByRole('switch', { name: 'Google' }));
    fireEvent.click(saveButton());
    await waitFor(() => expect(callsTo('POST', '/v1/oidc-config/confirm')).toHaveLength(1));
    expect(callsTo('POST', '/v1/oidc-config/confirm')[0]!.body).toEqual({
      setup_token: 'abc',
      enabled_providers: [],
      opt_out: true,
    });
  });

  test('confirmed:false shows the failing providers and does not call onComplete', async () => {
    let completed = 0;
    routes['POST /v1/oidc-config/confirm'] = () =>
      json(
        200,
        status({
          confirmed: false,
          providers: [
            { id: 'google', display_name: 'Google', configured: true, enabled: true, init_ok: true, error: null },
            { id: 'microsoft', display_name: 'Microsoft', configured: true, enabled: true, init_ok: false, error: 'bad issuer' },
          ],
        }),
      );
    await renderLoaded(<OidcConfigPage onComplete={() => completed++} redirectDelayMs={0} />);
    typeToken('abc');
    fireEvent.click(screen.getByRole('switch', { name: 'Microsoft' }));
    fireEvent.click(saveButton());

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain(
      'Configuration saved but providers still fail to initialize: Microsoft: bad issuer. Disable the failing providers or fix their env settings.',
    );
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(completed).toBe(0);
    expect(screen.queryByText('Redirecting to the login page...')).toBeNull();
    expect(screen.getByLabelText('Setup token')).toHaveValue('abc');
  });

  test('confirmed:false without a failing provider shows the generic unconfirmed message', async () => {
    routes['POST /v1/oidc-config/confirm'] = () => json(200, status({ confirmed: false }));
    await renderLoaded(<OidcConfigPage />);
    typeToken('abc');
    fireEvent.click(screen.getByRole('switch', { name: 'Google' }));
    fireEvent.click(saveButton());
    expect((await screen.findByRole('alert')).textContent).toContain(
      'Configuration saved but the system is not in a confirmed state. Check server logs.',
    );
  });

  test('an API error on confirm is shown inline', async () => {
    routes['POST /v1/oidc-config/confirm'] = () =>
      json(401, { error: { code: 'unauthenticated', message: 'invalid setup token' } });
    await renderLoaded();
    typeToken('wrong');
    fireEvent.click(screen.getByRole('switch', { name: 'Microsoft' }));
    fireEvent.click(saveButton());
    expect((await screen.findByRole('alert')).textContent).toContain('invalid setup token');
  });

  test('the pending onComplete timer is cleared on unmount', async () => {
    let completed = 0;
    routes['POST /v1/oidc-config/confirm'] = () => json(200, status({ confirmed: true }));
    const { unmount } = await renderLoaded(<OidcConfigPage onComplete={() => completed++} />);
    typeToken('abc');
    fireEvent.click(screen.getByRole('switch', { name: 'Microsoft' }));

    jest.useFakeTimers();
    fireEvent.click(saveButton());
    await flushMicrotasks();
    expect(screen.getByText('Configuration saved')).toBeInTheDocument();
    unmount();
    act(() => {
      jest.advanceTimersByTime(5000);
    });
    expect(completed).toBe(0);
  });
});

describe('OidcConfigPage: admin mode', () => {
  async function renderAdmin(onComplete?: () => void) {
    localStorage.setItem('auth_token', 'admin-jwt');
    render(
      <AuthProvider>
        <OidcConfigPage onComplete={onComplete} redirectDelayMs={0} />
      </AuthProvider>,
    );
    await screen.findByText('Toggle providers and confirm. Changes take effect immediately.');
  }

  test('hides the token field, sends a bearer token, stays on the page with refreshed status', async () => {
    let completed = 0;
    routes['POST /v1/oidc-config/confirm'] = () =>
      json(
        200,
        status({
          confirmed: true,
          providers: [
            { id: 'google', display_name: 'Google', configured: true, enabled: true, init_ok: true, error: null },
            { id: 'microsoft', display_name: 'Microsoft', configured: true, enabled: true, init_ok: true, error: null },
          ],
        }),
      );
    await renderAdmin(() => completed++);
    expect(screen.queryByLabelText('Setup token')).toBeNull();

    fireEvent.click(screen.getByRole('switch', { name: 'Microsoft' }));
    fireEvent.click(saveButton());

    expect(await screen.findByText('Configuration saved.')).toBeInTheDocument();
    const confirm = callsTo('POST', '/v1/oidc-config/confirm')[0]!;
    expect(confirm.headers.Authorization).toBe('Bearer admin-jwt');
    expect(confirm.body).toEqual({ enabled_providers: ['google', 'microsoft'], opt_out: false });

    // Refreshed status: both badges OK, no init error, Save clean again.
    expect(screen.getAllByText('OK')).toHaveLength(2);
    expect(screen.queryByText('Init error: bad issuer')).toBeNull();
    expect(saveButton()).toBeDisabled();
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(completed).toBe(0);
    expect(screen.queryByText('Redirecting to the login page...')).toBeNull();
  });

  test('a non-admin session still renders token mode inside AuthProvider', async () => {
    localStorage.setItem('auth_token', 'user-jwt');
    routes['GET /v1/self'] = () => json(200, { ...ADMIN, is_admin: false });
    render(
      <AuthProvider>
        <OidcConfigPage />
      </AuthProvider>,
    );
    await screen.findByRole('switch', { name: 'Google' });
    await waitFor(() => expect(callsTo('GET', '/v1/self')).toHaveLength(1));
    expect(screen.getByLabelText('Setup token')).toBeInTheDocument();
  });
});

describe('OidcConfigPage: revert', () => {
  test('restores toggles from /v1/oidc-config/saved and shows the revert message', async () => {
    routes['GET /v1/oidc-config/saved'] = () =>
      json(200, { enabled_providers: { microsoft: true }, opt_out: false, saved_at: null });
    await renderLoaded();
    fireEvent.click(screen.getByRole('button', { name: 'Revert' }));
    expect(await screen.findByText('Reverted to last saved configuration.')).toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'Google' })).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByRole('switch', { name: 'Microsoft' })).toHaveAttribute('aria-checked', 'true');
  });

  test('an empty saved config shows the nothing-to-revert message', async () => {
    routes['GET /v1/oidc-config/saved'] = () =>
      json(200, { enabled_providers: null, opt_out: false, saved_at: null });
    await renderLoaded();
    fireEvent.click(screen.getByRole('button', { name: 'Revert' }));
    expect(await screen.findByText('No saved config to revert to.')).toBeInTheDocument();
    expect(screen.getByRole('switch', { name: 'Google' })).toHaveAttribute('aria-checked', 'true');
  });
});

describe('OidcConfigPage: test-configuration result banner', () => {
  test('?test_result=ok renders the success banner once and strips the query', async () => {
    happyDOM.setURL(
      'http://localhost/oidc-config?test_result=ok&test_provider=google&test_email=a%40b.test&test_sub=123&test_issuer=https%3A%2F%2Faccounts.google.com',
    );
    const { unmount } = await renderLoaded();
    expect(screen.getByText('Test succeeded for google.')).toBeInTheDocument();
    expect(
      screen.getByText(/Verified identity from the provider: a@b\.test \(sub 123\) via https:\/\/accounts\.google\.com\./),
    ).toBeInTheDocument();
    expect(window.location.search).toBe('');
    expect(window.location.pathname).toBe('/oidc-config');

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByText('Test succeeded for google.')).toBeNull();

    unmount();
    await renderLoaded();
    expect(screen.queryByText('Test succeeded for google.')).toBeNull();
  });

  test('?test_result=fail renders the failure banner with the error', async () => {
    happyDOM.setURL(
      'http://localhost/oidc-config?test_result=fail&test_provider=microsoft&test_error=access_denied',
    );
    await renderLoaded();
    expect(screen.getByText('Test failed for microsoft.')).toBeInTheDocument();
    expect(screen.getByText('access_denied')).toBeInTheDocument();
    expect(window.location.search).toBe('');
  });
});

describe('OidcConfigPage: provider modals', () => {
  test('without auth the Add button is disabled and no Edit buttons render', async () => {
    await renderLoaded();
    expect(screen.getByRole('button', { name: 'Add provider' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Edit Google' })).toBeNull();
  });

  test('add modal creates a provider against the configured base URL and refreshes status', async () => {
    configureUsersApi({ baseUrl: '' });
    routes['POST /v1/oidc-config/providers'] = () => json(201, { ...GOOGLE_VIEW, id: 'okta' });
    await renderLoaded();
    typeToken('setup-tok');

    fireEvent.click(screen.getByRole('button', { name: 'Add provider' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Add provider')).toBeInTheDocument();
    const create = within(dialog).getByRole('button', { name: 'Create provider' });
    expect(create).toBeDisabled();

    fireEvent.change(within(dialog).getByLabelText('ID (slug)'), { target: { value: 'Bad_ID' } });
    fireEvent.click(create);
    expect(
      await within(dialog).findByText(
        'ID must be 2-32 characters, lowercase letters/digits/dashes, no leading or trailing dash.',
      ),
    ).toBeInTheDocument();
    expect(callsTo('POST', '/v1/oidc-config/providers')).toHaveLength(0);

    fireEvent.change(within(dialog).getByLabelText('ID (slug)'), { target: { value: 'okta' } });
    fireEvent.change(within(dialog).getByLabelText('Issuer URL'), {
      target: { value: 'https://okta.example.test' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create provider' }));

    await waitFor(() => expect(dialogOpen()).toBe(false));
    const post = callsTo('POST', '/v1/oidc-config/providers');
    expect(post).toHaveLength(1);
    expect(post[0]!.url).toBe('/v1/oidc-config/providers');
    expect(post[0]!.headers['X-Setup-Token']).toBe('setup-tok');
    expect(post[0]!.body).toEqual({
      id: 'okta',
      display_name: null,
      issuer_url: 'https://okta.example.test',
      client_id: null,
      claim_style: null,
      scopes: null,
      enabled: true,
      setup_token: 'setup-tok',
    });
    await waitFor(() => expect(callsTo('GET', '/v1/oidc-config/status')).toHaveLength(2));
    expect(callsTo('GET', '/v1/oidc-config/status')[1]!.url).toBe('/v1/oidc-config/status');
  });

  test('add modal maps a 409 to the already-exists message', async () => {
    routes['POST /v1/oidc-config/providers'] = () =>
      json(409, { error: { code: 'conflict', message: 'exists' } });
    await renderLoaded();
    typeToken('setup-tok');
    fireEvent.click(screen.getByRole('button', { name: 'Add provider' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('ID (slug)'), { target: { value: 'google' } });
    expect(
      within(dialog).getByText('Known provider. Server will apply well-known defaults for blank fields.'),
    ).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Create provider' }));
    expect(
      await within(dialog).findByText('Provider already exists — use Edit instead.'),
    ).toBeInTheDocument();
  });

  test('edit modal loads, saves, tests, and reverts against the configured base URL', async () => {
    configureUsersApi({ baseUrl: '' });
    const opened: Array<[string | URL | undefined, string | undefined]> = [];
    window.open = ((url?: string | URL, target?: string) => {
      opened.push([url, target]);
      return null;
    }) as typeof window.open;
    routes['GET /v1/oidc-config/providers/google'] = () => json(200, GOOGLE_VIEW);
    routes['PUT /v1/oidc-config/providers/google'] = () =>
      json(200, { ...GOOGLE_VIEW, client_id: 'new-cid', client_id_source: 'db' });
    routes['DELETE /v1/oidc-config/providers/google'] = () => new Response(null, { status: 204 });

    await renderLoaded();
    typeToken('setup-tok');
    fireEvent.click(screen.getByRole('button', { name: 'Edit Google' }));

    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByText('Edit provider: google')).toBeInTheDocument();
    const get = callsTo('GET', '/v1/oidc-config/providers/google');
    expect(get[0]!.url).toBe('/v1/oidc-config/providers/google');
    expect(get[0]!.headers['X-Setup-Token']).toBe('setup-tok');
    expect(within(dialog).getByText('Source: env var (AUTH_PROVIDER_GOOGLE_CLIENT_ID)')).toBeInTheDocument();

    // Test configuration opens the persisted-config round trip in a new tab.
    fireEvent.click(within(dialog).getByRole('button', { name: 'Test configuration' }));
    expect(opened).toEqual([['/v1/auth/oidc/google/start?mode=test', '_blank']]);

    // Save is dirty-gated; edit then save.
    const save = within(dialog).getByRole('button', { name: 'Save' });
    expect(save).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText('Client ID'), { target: { value: 'new-cid' } });
    expect(within(dialog).getByRole('button', { name: 'Test configuration' })).toBeDisabled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(callsTo('PUT', '/v1/oidc-config/providers/google')).toHaveLength(1));
    const put = callsTo('PUT', '/v1/oidc-config/providers/google')[0]!;
    expect(put.url).toBe('/v1/oidc-config/providers/google');
    expect(put.body).toEqual({
      display_name: null,
      issuer_url: null,
      client_id: 'new-cid',
      claim_style: null,
      scopes: null,
      enabled: true,
      setup_token: 'setup-tok',
    });
    expect(await within(dialog).findByText('Source: DB override')).toBeInTheDocument();
    await waitFor(() => expect(callsTo('GET', '/v1/oidc-config/status')).toHaveLength(2));

    fireEvent.click(within(dialog).getByRole('button', { name: 'Revert' }));
    await waitFor(() => expect(dialogOpen()).toBe(false));
    const del = callsTo('DELETE', '/v1/oidc-config/providers/google');
    expect(del).toHaveLength(1);
    expect(del[0]!.url).toBe('/v1/oidc-config/providers/google');
    await waitFor(() => expect(callsTo('GET', '/v1/oidc-config/status')).toHaveLength(3));
  });

  test('admin mode opens the edit modal with a bearer token', async () => {
    localStorage.setItem('auth_token', 'admin-jwt');
    routes['GET /v1/oidc-config/providers/google'] = () => json(200, GOOGLE_VIEW);
    render(
      <AuthProvider>
        <OidcConfigPage />
      </AuthProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Edit Google' }));
    await screen.findByText('Edit provider: google');
    const get = callsTo('GET', '/v1/oidc-config/providers/google')[0]!;
    expect(get.headers.Authorization).toBe('Bearer admin-jwt');
    expect(get.headers['X-Setup-Token']).toBeUndefined();
  });
});
