import type { Story } from '@ladle/react';
import React, { useEffect, useState } from 'react';
import { OidcConfigPage } from '../components/oidc-config-page';
import type { OIDCStatus } from '../lib/oidc-config';
import type { OIDCProviderView } from '../lib/oidc-provider';
import { SignedInStory, STORY_PERSONAL } from './story-support';

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
  if (path.endsWith('/v1/bootstrap/status')) {
    return jsonResponse(200, { bootstrapped: true, oidc_confirmed: false });
  }
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

const GOOGLE_VIEW: OIDCProviderView = {
  id: 'google',
  display_name: null,
  display_name_default: 'Google',
  display_name_source: 'well_known',
  issuer_url: null,
  issuer_url_default: 'https://accounts.google.com',
  issuer_url_source: 'well_known',
  client_id: 'story-client',
  client_id_default: null,
  client_id_source: 'db',
  has_client_secret: true,
  client_secret_source: 'db',
  claim_style: null,
  claim_style_default: 'google',
  claim_style_source: 'well_known',
  scopes: null,
  scopes_default: ['openid', 'email', 'profile'],
  scopes_source: 'well_known',
  enabled: true,
  party_uuid: null,
  jit_mode: 'byo',
  trust_acr: false,
  init_ok: true,
  callback_url: 'http://localhost:8080/v1/auth/oidc/google/callback',
  well_known: true,
};

/** Token mode (no session, OIDC unconfirmed): paste any token, flip a toggle, and Save to see the success state. */
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

/**
 * Token mode with the bootstrap still pending: after Save the page does not
 * claim the setup token is spent and points at the bootstrap page.
 */
export const TokenModeBootstrapPending: Story = () => (
  <MockFetch
    respond={(path, init) =>
      path.endsWith('/v1/bootstrap/status')
        ? jsonResponse(200, { bootstrapped: false, oidc_confirmed: false })
        : happyPath(path, init)
    }
  >
    <OidcConfigPage onBootstrapPending={() => {}} />
  </MockFetch>
);

/** Session mode: a signed-in tab; no token field, requests carry the Bearer token. */
export const SessionMode: Story = () => (
  <SignedInStory
    profiles={[STORY_PERSONAL]}
    routes={{
      'GET /v1/oidc-config/status': () => ({ ...STATUS, confirmed: true, state: 'confirmed_ok' }),
      'GET /v1/bootstrap/status': () => ({ bootstrapped: true, oidc_confirmed: true }),
      'GET /v1/oidc-config/saved': () => ({ enabled_providers: { google: true }, opt_out: false, saved_at: null }),
      'POST /v1/oidc-config/confirm': () => ({ ...STATUS, confirmed: true, state: 'confirmed_ok' }),
    }}
  >
    <OidcConfigPage />
  </SignedInStory>
);

/** Session mode where the server refuses the profile: the "requires an operator admin profile" message. */
export const SessionModeForbidden: Story = () => (
  <SignedInStory
    profiles={[STORY_PERSONAL]}
    routes={{
      'GET /v1/oidc-config/status': () => ({ ...STATUS, confirmed: true, state: 'confirmed_ok' }),
      'GET /v1/bootstrap/status': () => ({ bootstrapped: true, oidc_confirmed: true }),
      'POST /v1/oidc-config/confirm': () =>
        jsonResponse(403, { error: { code: 'forbidden', message: 'forbidden' } }),
    }}
  >
    <OidcConfigPage />
  </SignedInStory>
);

/** A provider with `jit_mode: managed` bound to a party: open "Edit Google" to see the policy fields. */
export const ManagedProvider: Story = () => (
  <SignedInStory
    profiles={[STORY_PERSONAL]}
    routes={{
      'GET /v1/oidc-config/status': () => ({ ...STATUS, confirmed: true, state: 'confirmed_ok' }),
      'GET /v1/bootstrap/status': () => ({ bootstrapped: true, oidc_confirmed: true }),
      'GET /v1/oidc-config/providers/google': () => ({
        ...GOOGLE_VIEW,
        party_uuid: '11111111-2222-3333-4444-555555555555',
        jit_mode: 'managed',
        trust_acr: true,
      }),
    }}
  >
    <OidcConfigPage />
  </SignedInStory>
);
