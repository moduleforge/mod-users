import { afterEach, describe, expect, test } from 'bun:test';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { AuthProvider, useAuth } from './auth-context';
import { configureUsersApi, resetUsersApiConfig } from './config';

// Exercises the three `api.self.get()` call sites this task wires: the mount
// effect, `refreshUser`, and `completeExternalLogin` must each catch
// `ApiActionRequiredError` before any other error-shaped handling and
// navigate via the provider's injected `onNavigate`, without clearing the
// stored token or (for `completeExternalLogin`) rethrowing. Every
// non-action-required error must keep its pre-existing behavior
// (regression checks).

const TOKEN_KEY = 'auth_token';
const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
  localStorage.clear();
  resetUsersApiConfig();
});

function stubFetch(body: unknown, status: number): void {
  globalThis.fetch = (async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })) as unknown as typeof fetch;
}

function stubFetchNetworkError(): void {
  globalThis.fetch = (async () => {
    throw new Error('network down');
  }) as unknown as typeof fetch;
}

const ACTION_REQUIRED_BODY = {
  action: {
    code: 'users.email_unverified',
    message: 'Verify your email address before continuing.',
    path: '/verify-email',
  },
};

/** Renders the context's live values/actions so tests can assert on them. */
function Probe({
  trigger,
}: {
  trigger?: 'refresh' | 'completeExternalLogin';
}) {
  const auth = useAuth();
  const [completeError, setCompleteError] = useState<string | null>(null);
  const [completeSettled, setCompleteSettled] = useState(false);

  return (
    <div>
      <div data-testid="loading">{String(auth.isLoading)}</div>
      <div data-testid="token">{auth.token ?? 'null'}</div>
      <div data-testid="complete-settled">{String(completeSettled)}</div>
      <div data-testid="complete-error">{completeError ?? 'none'}</div>
      {trigger === 'refresh' && (
        <button onClick={() => void auth.refreshUser()}>refresh</button>
      )}
      {trigger === 'completeExternalLogin' && (
        <button
          onClick={() =>
            auth
              .completeExternalLogin('new-token')
              .then(() => setCompleteSettled(true))
              .catch((err: unknown) => {
                setCompleteSettled(true);
                setCompleteError(err instanceof Error ? err.message : String(err));
              })
          }
        >
          complete
        </button>
      )}
    </div>
  );
}

describe('mount effect: token validation on mount', () => {
  test('ApiActionRequiredError navigates and keeps the stored token intact', async () => {
    localStorage.setItem(TOKEN_KEY, 'stored-token');
    stubFetch(ACTION_REQUIRED_BODY, 403);
    const navigate = (path: string) => navigateCalls.push(path);
    const navigateCalls: string[] = [];

    render(
      <AuthProvider onNavigate={navigate}>
        <Probe />
      </AuthProvider>,
    );

    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    expect(navigateCalls).toEqual(['/verify-email']);
    expect(localStorage.getItem(TOKEN_KEY)).toBe('stored-token');
    expect(screen.getByTestId('token').textContent).toBe('stored-token');
  });

  test('a non-action-required error still clears the stored token (regression)', async () => {
    localStorage.setItem(TOKEN_KEY, 'stored-token');
    stubFetch({ error: { code: 'internal_error', message: 'boom' } }, 500);
    const navigateCalls: string[] = [];

    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe />
      </AuthProvider>,
    );

    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    expect(navigateCalls).toEqual([]);
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(screen.getByTestId('token').textContent).toBe('null');
  });

  test('a network failure still clears the stored token (regression)', async () => {
    localStorage.setItem(TOKEN_KEY, 'stored-token');
    stubFetchNetworkError();
    const navigateCalls: string[] = [];

    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe />
      </AuthProvider>,
    );

    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    expect(navigateCalls).toEqual([]);
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
  });
});

describe('refreshUser', () => {
  test('ApiActionRequiredError navigates without logging out', async () => {
    const navigateCalls: string[] = [];

    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe trigger="refresh" />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    stubFetch(ACTION_REQUIRED_BODY, 403);
    fireEvent.click(screen.getByText('refresh'));

    await waitFor(() => expect(navigateCalls.length).toBe(1));

    expect(navigateCalls).toEqual(['/verify-email']);
    // logout() would navigate to '/auth/login' in addition to clearing the
    // token; neither happened.
    expect(screen.getByTestId('token').textContent).toBe('null');
  });

  test('a 401 still logs out (regression)', async () => {
    const navigateCalls: string[] = [];

    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe trigger="refresh" />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    stubFetch({ error: { code: 'unauthenticated', message: 'nope' } }, 401);
    fireEvent.click(screen.getByText('refresh'));

    await waitFor(() => expect(navigateCalls).toEqual(['/auth/login']));
  });

  test('a non-401, non-action-required error is silently swallowed (regression)', async () => {
    const navigateCalls: string[] = [];

    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe trigger="refresh" />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    stubFetchNetworkError();
    fireEvent.click(screen.getByText('refresh'));

    // Give the rejected promise a tick to resolve, then confirm nothing fired.
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(navigateCalls).toEqual([]);
  });
});

describe('loginPath', () => {
  function LogoutProbe() {
    const auth = useAuth();
    return (
      <div>
        <div data-testid="loading">{String(auth.isLoading)}</div>
        <button onClick={() => auth.logout()}>logout</button>
        <button onClick={() => void auth.refreshUser()}>refresh</button>
      </div>
    );
  }

  async function renderWith(loginPath?: string, calls: string[] = []) {
    render(
      <AuthProvider onNavigate={(p) => calls.push(p)} loginPath={loginPath}>
        <LogoutProbe />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));
  }

  test('logout navigates to /auth/login by default', async () => {
    const calls: string[] = [];
    await renderWith(undefined, calls);
    fireEvent.click(screen.getByText('logout'));
    expect(calls).toEqual(['/auth/login']);
  });

  test('logout navigates to the configured loginPath', async () => {
    const calls: string[] = [];
    await renderWith('/signin', calls);
    fireEvent.click(screen.getByText('logout'));
    expect(calls).toEqual(['/signin']);
  });

  test('the refreshUser 401 path navigates to the configured loginPath', async () => {
    const calls: string[] = [];
    await renderWith('/signin', calls);
    stubFetch({ error: { code: 'unauthenticated', message: 'nope' } }, 401);
    fireEvent.click(screen.getByText('refresh'));
    await waitFor(() => expect(calls).toEqual(['/signin']));
  });

  test('an invalid loginPath falls back to the default with a console.error', async () => {
    const originalError = console.error;
    const errors: unknown[][] = [];
    console.error = (...a: unknown[]) => {
      errors.push(a);
    };
    try {
      const calls: string[] = [];
      await renderWith('//evil.test', calls);
      fireEvent.click(screen.getByText('logout'));
      expect(calls).toEqual(['/auth/login']);
      expect(errors.length).toBeGreaterThan(0);
    } finally {
      console.error = originalError;
    }
  });
});

describe('completeExternalLogin', () => {
  test('ApiActionRequiredError keeps the token, navigates, and does not rethrow', async () => {
    const navigateCalls: string[] = [];

    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe trigger="completeExternalLogin" />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    stubFetch(ACTION_REQUIRED_BODY, 403);
    fireEvent.click(screen.getByText('complete'));

    await waitFor(() => expect(screen.getByTestId('complete-settled').textContent).toBe('true'));

    expect(navigateCalls).toEqual(['/verify-email']);
    expect(screen.getByTestId('complete-error').textContent).toBe('none');
    expect(localStorage.getItem(TOKEN_KEY)).toBe('new-token');
    expect(screen.getByTestId('token').textContent).toBe('new-token');
  });

  test('a non-action-required error clears the token and rethrows (regression)', async () => {
    const navigateCalls: string[] = [];

    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe trigger="completeExternalLogin" />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    stubFetch({ error: { code: 'unauthenticated', message: 'bad token' } }, 401);
    fireEvent.click(screen.getByText('complete'));

    await waitFor(() => expect(screen.getByTestId('complete-settled').textContent).toBe('true'));

    expect(navigateCalls).toEqual([]);
    expect(screen.getByTestId('complete-error').textContent).toBe('Authentication required');
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(screen.getByTestId('token').textContent).toBe('null');
  });
});

describe('custom tokenStorageKey', () => {
  const CUSTOM = 'custom_key';

  function ActionProbe() {
    const auth = useAuth();
    return (
      <div>
        <div data-testid="loading">{String(auth.isLoading)}</div>
        <div data-testid="token">{auth.token ?? 'null'}</div>
        <button onClick={() => void auth.login('a@b.test', 'pw')}>login</button>
        <button onClick={() => auth.logout()}>logout</button>
      </div>
    );
  }

  test('mount reads only the custom key', async () => {
    configureUsersApi({ tokenStorageKey: CUSTOM });
    localStorage.setItem(TOKEN_KEY, 'default-token');
    render(
      <AuthProvider>
        <ActionProbe />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));
    expect(screen.getByTestId('token').textContent).toBe('null');
    expect(localStorage.getItem(TOKEN_KEY)).toBe('default-token');
  });

  test('mount failure clears only the custom key', async () => {
    configureUsersApi({ tokenStorageKey: CUSTOM });
    localStorage.setItem(CUSTOM, 'stored');
    localStorage.setItem(TOKEN_KEY, 'default-token');
    stubFetch({ error: { code: 'internal_error', message: 'boom' } }, 500);
    render(
      <AuthProvider>
        <ActionProbe />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));
    expect(localStorage.getItem(CUSTOM)).toBeNull();
    expect(localStorage.getItem(TOKEN_KEY)).toBe('default-token');
  });

  test('login writes and logout clears only the custom key', async () => {
    configureUsersApi({ tokenStorageKey: CUSTOM });
    render(
      <AuthProvider>
        <ActionProbe />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));
    stubFetch({ token: 'tok', user: { id: 'u' } }, 200);
    fireEvent.click(screen.getByText('login'));
    await waitFor(() => expect(localStorage.getItem(CUSTOM)).toBe('tok'));
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull();

    localStorage.setItem(TOKEN_KEY, 'default-token');
    fireEvent.click(screen.getByText('logout'));
    expect(localStorage.getItem(CUSTOM)).toBeNull();
    expect(localStorage.getItem(TOKEN_KEY)).toBe('default-token');
  });

  test('completeExternalLogin writes the custom key; failure clears only it', async () => {
    configureUsersApi({ tokenStorageKey: CUSTOM });
    localStorage.setItem(TOKEN_KEY, 'default-token');
    render(
      <AuthProvider>
        <Probe trigger="completeExternalLogin" />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    stubFetch(ACTION_REQUIRED_BODY, 403);
    fireEvent.click(screen.getByText('complete'));
    await waitFor(() => expect(screen.getByTestId('complete-settled').textContent).toBe('true'));
    expect(localStorage.getItem(CUSTOM)).toBe('new-token');
    expect(localStorage.getItem(TOKEN_KEY)).toBe('default-token');
  });

  test('completeExternalLogin failure removes the custom key only', async () => {
    configureUsersApi({ tokenStorageKey: CUSTOM });
    localStorage.setItem(TOKEN_KEY, 'default-token');
    render(
      <AuthProvider>
        <Probe trigger="completeExternalLogin" />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));
    stubFetch({ error: { code: 'unauthenticated', message: 'bad' } }, 401);
    fireEvent.click(screen.getByText('complete'));
    await waitFor(() => expect(screen.getByTestId('complete-settled').textContent).toBe('true'));
    expect(localStorage.getItem(CUSTOM)).toBeNull();
    expect(localStorage.getItem(TOKEN_KEY)).toBe('default-token');
  });
});

describe('action-required navigation is skipped when already on the target path', () => {
  const OIDC_BODY = {
    action: {
      code: 'users.oidc_not_confirmed',
      message: 'OIDC configuration must be confirmed.',
      path: '/oidc-config',
    },
  };

  // happy-dom starts at about:blank, where history.replaceState cannot set a
  // path; point it at a real origin for these tests and restore afterwards.
  const happyDOM = (window as unknown as { happyDOM: { setURL(url: string): void } }).happyDOM;
  function goTo(path: string): void {
    happyDOM.setURL(`http://localhost${path}`);
  }

  afterEach(() => {
    happyDOM.setURL('about:blank');
  });

  test('mount effect does not navigate to the page it is already on', async () => {
    goTo('/oidc-config');
    localStorage.setItem(TOKEN_KEY, 'stored-token');
    stubFetch(OIDC_BODY, 503);
    const navigateCalls: string[] = [];

    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    expect(navigateCalls).toEqual([]);
    expect(localStorage.getItem(TOKEN_KEY)).toBe('stored-token');
  });

  test('mount effect still navigates from any other path', async () => {
    goTo('/dashboard');
    localStorage.setItem(TOKEN_KEY, 'stored-token');
    stubFetch(OIDC_BODY, 503);
    const navigateCalls: string[] = [];

    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    expect(navigateCalls).toEqual(['/oidc-config']);
  });

  test('refreshUser does not navigate to the page it is already on', async () => {
    goTo('/oidc-config');
    const navigateCalls: string[] = [];
    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe trigger="refresh" />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    stubFetch(OIDC_BODY, 503);
    fireEvent.click(screen.getByText('refresh'));
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(navigateCalls).toEqual([]);
  });

  test('completeExternalLogin does not navigate to the page it is already on', async () => {
    goTo('/oidc-config');
    const navigateCalls: string[] = [];
    render(
      <AuthProvider onNavigate={(path) => navigateCalls.push(path)}>
        <Probe trigger="completeExternalLogin" />
      </AuthProvider>,
    );
    await waitFor(() => expect(screen.getByTestId('loading').textContent).toBe('false'));

    stubFetch(OIDC_BODY, 503);
    fireEvent.click(screen.getByText('complete'));
    await waitFor(() => expect(screen.getByTestId('complete-settled').textContent).toBe('true'));

    expect(navigateCalls).toEqual([]);
    expect(screen.getByTestId('complete-error').textContent).toBe('none');
    expect(localStorage.getItem(TOKEN_KEY)).toBe('new-token');
  });
});
