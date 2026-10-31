import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { render, screen, waitFor } from '@testing-library/react';
import type React from 'react';
import { OidcSetupGate } from './oidc-setup-gate';
import { ClientLayout } from './client-layout';
import { resetUsersApiConfig } from '../lib/config';

const originalFetch = globalThis.fetch;

let statusResponder: () => Response | Promise<Response>;
let bootstrapResponder: () => Response | Promise<Response>;
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
  bootstrapResponder = () => json(200, { bootstrapped: true, oidc_confirmed: true });
  globalThis.fetch = (async (url: string) => {
    const u = String(url);
    fetched.push(u);
    if (u.endsWith('/v1/oidc-config/status')) return statusResponder();
    if (u.endsWith('/v1/bootstrap/status')) return bootstrapResponder();
    // AuthProvider without a stored token makes no calls; anything else is a bug.
    return json(404, { error: { code: 'not_found', message: u } });
  }) as unknown as typeof fetch;
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  localStorage.clear();
  resetUsersApiConfig();
});

/** Scripts the two probes: bootstrapped and OIDC confirmed. */
function setState(bootstrapped: boolean, confirmed: boolean) {
  statusResponder = () => json(200, status(confirmed));
  bootstrapResponder = () => json(200, { bootstrapped, oidc_confirmed: confirmed });
}

function renderGate(
  currentPath: string,
  onNavigateToConfig?: () => void,
  onNavigateToBootstrap?: () => void,
) {
  return render(
    <OidcSetupGate
      currentPath={currentPath}
      onNavigateToConfig={onNavigateToConfig}
      onNavigateToBootstrap={onNavigateToBootstrap}
    >
      {(state) => <div data-testid="child">{state}</div>}
    </OidcSetupGate>,
  );
}

describe('OidcSetupGate', () => {
  test('renders the loading screen while the status probes are in flight', () => {
    statusResponder = () => new Promise<Response>(() => {});
    renderGate('/dashboard');
    expect(screen.getByText('Loading...')).toBeInTheDocument();
    expect(screen.queryByTestId('child')).toBeNull();
  });

  test('bootstrapped and confirmed renders ready and navigates nowhere; both probes are read once', async () => {
    let toConfig = 0;
    let toBootstrap = 0;
    renderGate('/dashboard', () => toConfig++, () => toBootstrap++);
    expect((await screen.findByTestId('child')).textContent).toBe('ready');
    expect(toConfig + toBootstrap).toBe(0);
    expect(fetched.sort()).toEqual([
      'http://localhost:8080/v1/bootstrap/status',
      'http://localhost:8080/v1/oidc-config/status',
    ]);
  });

  test('ready also renders the config and bootstrap routes (no gating once setup is done)', async () => {
    renderGate('/oidc-config');
    expect((await screen.findByTestId('child')).textContent).toBe('ready');
  });

  describe('not bootstrapped, OIDC unconfirmed', () => {
    beforeEach(() => setState(false, false));

    test('other routes go to the OIDC config page first', async () => {
      let toConfig = 0;
      let toBootstrap = 0;
      renderGate('/dashboard', () => toConfig++, () => toBootstrap++);
      await waitFor(() => expect(toConfig).toBe(1));
      expect(toBootstrap).toBe(0);
      expect(screen.getByText('Loading...')).toBeInTheDocument();
      expect(screen.queryByTestId('child')).toBeNull();
    });

    test('both setup pages are allowed through, in either order', async () => {
      let navigated = 0;
      const first = renderGate('/oidc-config', () => navigated++, () => navigated++);
      expect((await screen.findByTestId('child')).textContent).toBe('setup');
      first.unmount();
      renderGate('/bootstrap', () => navigated++, () => navigated++);
      expect((await screen.findByTestId('child')).textContent).toBe('bootstrap');
      expect(navigated).toBe(0);
    });
  });

  describe('bootstrapped, OIDC unconfirmed', () => {
    beforeEach(() => setState(true, false));

    test('only the config page is allowed; the bootstrap page redirects to config', async () => {
      let toConfig = 0;
      renderGate('/bootstrap', () => toConfig++);
      await waitFor(() => expect(toConfig).toBe(1));
      expect(screen.queryByTestId('child')).toBeNull();
    });

    test('the config page renders setup without navigating', async () => {
      let navigated = 0;
      renderGate('/oidc-config', () => navigated++, () => navigated++);
      expect((await screen.findByTestId('child')).textContent).toBe('setup');
      expect(navigated).toBe(0);
    });
  });

  describe('not bootstrapped, OIDC confirmed', () => {
    beforeEach(() => setState(false, true));

    test('other routes, including the config page, go to the bootstrap page', async () => {
      let toConfig = 0;
      let toBootstrap = 0;
      const first = renderGate('/dashboard', () => toConfig++, () => toBootstrap++);
      await waitFor(() => expect(toBootstrap).toBe(1));
      first.unmount();
      renderGate('/oidc-config', () => toConfig++, () => toBootstrap++);
      await waitFor(() => expect(toBootstrap).toBe(2));
      expect(toConfig).toBe(0);
      expect(screen.queryByTestId('child')).toBeNull();
    });

    test('the bootstrap page renders the bootstrap state', async () => {
      renderGate('/bootstrap');
      expect((await screen.findByTestId('child')).textContent).toBe('bootstrap');
    });
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

  test('a failing bootstrap probe alone also counts as needs-setup (config page only)', async () => {
    bootstrapResponder = () => json(500, { error: { code: 'internal_error', message: 'boom' } });
    let toConfig = 0;
    renderGate('/bootstrap', () => toConfig++);
    await waitFor(() => expect(toConfig).toBe(1));
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
    setState(false, false);
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

  test('bootstrap pending after OIDC: the bootstrap route renders children in a bare main without the sidebar', async () => {
    setState(false, true);
    const { container } = render(
      <ClientLayout currentPath="/bootstrap" LinkComponent={AnchorLink}>
        <p>page body</p>
      </ClientLayout>,
    );
    const body = await screen.findByText('page body');
    expect(body.closest('main')!.className).toBe('h-screen overflow-y-auto');
    expect(container.querySelector('nav')).toBeNull();
  });

  test('ok: renders the sidebar chrome and children inside the flex main', async () => {
    const { container } = renderLayout('/dashboard');
    const body = await screen.findByText('page body');
    expect(body.closest('main')!.className).toBe('flex-1 overflow-y-auto');
    // The banner sits above the sidebar/main row, so the outer box is a column.
    expect(container.firstElementChild!.className).toBe('flex h-screen flex-col overflow-hidden');
    expect(body.closest('main')!.parentElement!.className).toBe('flex min-h-0 flex-1 overflow-hidden');
    expect(container.querySelector('aside nav')).not.toBeNull();
  });
});
