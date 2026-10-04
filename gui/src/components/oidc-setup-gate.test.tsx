import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { render, screen, waitFor } from '@testing-library/react';
import type React from 'react';
import { OidcSetupGate } from './oidc-setup-gate';
import { ClientLayout } from './client-layout';
import { resetUsersApiConfig } from '../lib/config';

const originalFetch = globalThis.fetch;

let statusResponder: () => Response | Promise<Response>;
let fetched: string[];

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function status(confirmed: boolean) {
  return {
    state: confirmed ? 'confirmed_ok' : 'no_env_no_flag',
    confirmed,
    providers: [],
    no_oidc_accounts_env: false,
    needs_setup_token: !confirmed,
  };
}

beforeEach(() => {
  fetched = [];
  statusResponder = () => json(200, status(true));
  globalThis.fetch = (async (url: string) => {
    const u = String(url);
    fetched.push(u);
    if (u.endsWith('/v1/oidc-config/status')) return statusResponder();
    // AuthProvider without a stored token makes no calls; anything else is a bug.
    return json(404, { error: { code: 'not_found', message: u } });
  }) as unknown as typeof fetch;
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  localStorage.clear();
  resetUsersApiConfig();
});

function renderGate(currentPath: string, onNavigateToConfig?: () => void) {
  return render(
    <OidcSetupGate currentPath={currentPath} onNavigateToConfig={onNavigateToConfig}>
      {(state) => <div data-testid="child">{state}</div>}
    </OidcSetupGate>,
  );
}

describe('OidcSetupGate', () => {
  test('renders the loading screen while the status probe is in flight', () => {
    statusResponder = () => new Promise<Response>(() => {});
    renderGate('/dashboard');
    expect(screen.getByText('Loading...')).toBeInTheDocument();
    expect(screen.queryByTestId('child')).toBeNull();
  });

  test('confirmed renders the ready children and does not navigate', async () => {
    let navigated = 0;
    renderGate('/dashboard', () => navigated++);
    expect((await screen.findByTestId('child')).textContent).toBe('ready');
    expect(navigated).toBe(0);
    expect(fetched).toEqual(['http://localhost:8080/v1/oidc-config/status']);
  });

  test('needs-setup off the config path calls onNavigateToConfig and keeps the loading screen', async () => {
    statusResponder = () => json(200, status(false));
    let navigated = 0;
    renderGate('/dashboard', () => navigated++);
    await waitFor(() => expect(navigated).toBe(1));
    expect(screen.getByText('Loading...')).toBeInTheDocument();
    expect(screen.queryByTestId('child')).toBeNull();
  });

  test('needs-setup on the config path renders the setup children without navigating', async () => {
    statusResponder = () => json(200, status(false));
    let navigated = 0;
    renderGate('/oidc-config', () => navigated++);
    expect((await screen.findByTestId('child')).textContent).toBe('setup');
    expect(navigated).toBe(0);
  });

  test('a status fetch failure is treated as needs-setup', async () => {
    const originalError = console.error;
    console.error = () => {};
    try {
      statusResponder = () => {
        throw new Error('network down');
      };
      let navigated = 0;
      renderGate('/dashboard', () => navigated++);
      await waitFor(() => expect(navigated).toBe(1));
    } finally {
      console.error = originalError;
    }
  });

  test('a 5xx status response is treated as needs-setup', async () => {
    statusResponder = () => json(500, { error: { code: 'internal_error', message: 'boom' } });
    renderGate('/oidc-config');
    expect((await screen.findByTestId('child')).textContent).toBe('setup');
  });
});

function AnchorLink({
  href,
  className,
  children,
}: {
  href: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <a href={href} className={className}>
      {children}
    </a>
  );
}

describe('ClientLayout (composes OidcSetupGate)', () => {
  function renderLayout(currentPath: string, onNavigateToConfig?: () => void) {
    return render(
      <ClientLayout
        currentPath={currentPath}
        onNavigateToConfig={onNavigateToConfig}
        LinkComponent={AnchorLink}
      >
        <p>page body</p>
      </ClientLayout>,
    );
  }

  test('loading: renders only the loading screen', () => {
    statusResponder = () => new Promise<Response>(() => {});
    const { container } = renderLayout('/dashboard');
    expect(screen.getByText('Loading...')).toBeInTheDocument();
    expect(screen.queryByText('page body')).toBeNull();
    expect(container.querySelector('main')).toBeNull();
  });

  test('needs-setup: navigates off-route; on the config route renders children in a bare main without the sidebar', async () => {
    statusResponder = () => json(200, status(false));
    let navigated = 0;
    const first = renderLayout('/dashboard', () => navigated++);
    await waitFor(() => expect(navigated).toBe(1));
    expect(screen.queryByText('page body')).toBeNull();
    first.unmount();

    const { container } = renderLayout('/oidc-config', () => navigated++);
    const body = await screen.findByText('page body');
    const main = body.closest('main');
    expect(main).not.toBeNull();
    expect(main!.className).toBe('h-screen overflow-y-auto');
    expect(container.querySelector('nav')).toBeNull();
    expect(navigated).toBe(1);
  });

  test('ok: renders the sidebar chrome and children inside the flex main', async () => {
    const { container } = renderLayout('/dashboard');
    const body = await screen.findByText('page body');
    expect(body.closest('main')!.className).toBe('flex-1 overflow-y-auto');
    expect(container.firstElementChild!.className).toBe('flex h-screen overflow-hidden');
    expect(container.querySelector('aside nav')).not.toBeNull();
  });
});
