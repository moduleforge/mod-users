import type { Story } from '@ladle/react';
import React, { useEffect, useState } from 'react';
import { BootstrapPage } from '../components/bootstrap-page';
import type { TokenResponse } from '../lib/api';
import { STORY_PERSONAL } from './story-support';

// BootstrapPage talks to `/v1/bootstrap*`; there is no live API in Ladle, so
// each story swaps `window.fetch` for a canned responder while mounted.

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const TOKEN: TokenResponse = {
  access_token: 'story-access',
  token_type: 'Bearer',
  expires_in: 3600,
  refresh_token: 'story-refresh',
  session: {
    uuid: 'story-session',
    acr: 1,
    amr: ['pwd'],
    auth_time: '2026-01-01T00:00:00Z',
    impersonating: false,
  },
  profile: STORY_PERSONAL,
  selection_required: false,
  profiles: [STORY_PERSONAL],
};

type Responder = (path: string, init?: RequestInit) => Response;

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

const pending: Responder = (path, init) => {
  if (path.endsWith('/v1/bootstrap/status')) {
    return jsonResponse(200, { bootstrapped: false, oidc_confirmed: false });
  }
  if (path.endsWith('/v1/bootstrap') && init?.method === 'POST') {
    return jsonResponse(201, TOKEN);
  }
  return jsonResponse(404, { error: { code: 'not_found', message: 'Not mocked in this story.' } });
};

/** First run: fill in any token, an operator and an administrator, then submit. */
export const Default: Story = () => (
  <MockFetch respond={pending}>
    <BootstrapPage onComplete={() => {}} />
  </MockFetch>
);

/** The deployment already has an operator: "Already set up" with a sign-in link. */
export const AlreadyBootstrapped: Story = () => (
  <MockFetch respond={() => jsonResponse(200, { bootstrapped: true, oidc_confirmed: true })}>
    <BootstrapPage />
  </MockFetch>
);

/** Submitting answers `401 users.setup_token_invalid`. */
export const InvalidSetupToken: Story = () => (
  <MockFetch
    respond={(path, init) =>
      path.endsWith('/v1/bootstrap') && init?.method === 'POST'
        ? jsonResponse(401, { error: { code: 'users.setup_token_invalid', message: 'invalid setup token' } })
        : pending(path, init)
    }
  >
    <BootstrapPage />
  </MockFetch>
);
