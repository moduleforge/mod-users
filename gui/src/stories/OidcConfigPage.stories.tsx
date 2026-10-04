import type { Story } from '@ladle/react';
import React, { useEffect, useState } from 'react';
import { OidcConfigPage } from '../components/oidc-config-page';
import type { OIDCStatus } from '../lib/oidc-config';

// The page talks to `/v1/oidc-config/*`; there is no live API in Ladle, so
// each story swaps `window.fetch` for a canned responder while mounted.

const STATUS: OIDCStatus = {
  state: 'no_env_no_flag',
  confirmed: false,
  no_oidc_accounts_env: false,
  needs_setup_token: true,
  providers: [
    { id: 'google', display_name: 'Google', configured: true, enabled: true, init_ok: true, error: null },
    { id: 'microsoft', display_name: 'Microsoft', configured: true, enabled: false, init_ok: false, error: 'issuer discovery failed' },
    { id: 'authelia', display_name: 'Authelia', configured: false, enabled: false, init_ok: false, error: null },
  ],
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

type Responder = (path: string, init?: RequestInit) => Response;

const happyPath: Responder = (path, init) => {
  if (path.endsWith('/v1/oidc-config/status')) return jsonResponse(200, STATUS);
  if (path.endsWith('/v1/oidc-config/saved')) {
    return jsonResponse(200, { enabled_providers: { google: true }, opt_out: false, saved_at: null });
  }
  if (path.endsWith('/v1/oidc-config/confirm') && init?.method === 'POST') {
    return jsonResponse(200, { ...STATUS, confirmed: true, state: 'confirmed_ok' });
  }
  return jsonResponse(404, { error: { code: 'not_found', message: 'Not mocked in this story.' } });
};

/** Installs `respond` as `window.fetch` before children mount; restores it on unmount. */
function MockFetch({ respond, children }: { respond: Responder; children: React.ReactNode }) {
  const [original] = useState(() => {
    const saved = window.fetch;
    window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) =>
      respond(String(input), init)) as typeof fetch;
    return saved;
  });
  useEffect(
    () => () => {
      window.fetch = original;
    },
    [original],
  );
  return <>{children}</>;
}

/** Token mode: paste any token, flip a toggle, and Save to see the success state. */
export const TokenMode: Story = () => (
  <MockFetch respond={happyPath}>
    <OidcConfigPage onComplete={() => {}} />
  </MockFetch>
);

/** Status endpoint unreachable: the error card. */
export const StatusError: Story = () => (
  <MockFetch
    respond={() => jsonResponse(500, { error: { code: 'internal_error', message: 'database unavailable' } })}
  >
    <OidcConfigPage />
  </MockFetch>
);

/** Strict confirmation: Save returns `confirmed: false` with a failing provider. */
export const StillFailing: Story = () => (
  <MockFetch
    respond={(path, init) =>
      path.endsWith('/v1/oidc-config/confirm') && init?.method === 'POST'
        ? jsonResponse(200, {
            ...STATUS,
            providers: STATUS.providers.map((p) => (p.id === 'microsoft' ? { ...p, enabled: true } : p)),
          })
        : happyPath(path, init)
    }
  >
    <OidcConfigPage />
  </MockFetch>
);
