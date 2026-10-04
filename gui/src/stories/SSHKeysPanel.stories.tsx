import { useEffect, useState } from 'react';
import type { Story } from '@ladle/react';
import { SSHKeysPanel } from '../components/ssh-keys-panel';
import type { SSHKey } from '../lib/api';

// SSHKeysPanel calls the shared `api` singleton directly and does not call
// useAuth(), so no AuthProvider is needed. The `Default` story talks to a
// live API: without a running one, every call fails with `network_error`,
// matching how the other stories are documented. The remaining stories
// replace `globalThis.fetch` for the lifetime of the story (restored on
// unmount) so each panel state can be viewed without a backend.

const SAMPLE_KEYS: SSHKey[] = [
  {
    uuid: '11111111-1111-4111-8111-111111111111',
    key_type: 'ssh-ed25519',
    fingerprint: 'SHA256:3b0Vn9u5N7mZkq1Qm0hGJ8nF2YcRtPz4aWkLxD6sEuA',
    public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample1',
    label: 'Work laptop',
    created_at: '2026-09-28T12:34:56Z',
  },
  {
    uuid: '22222222-2222-4222-8222-222222222222',
    key_type: 'ecdsa-sha2-nistp256',
    fingerprint: 'SHA256:Qe7TnXr1uB5sVfC0pLw9Hy3KdZ2MaJgN8oIvRb4cEtU',
    public_key: 'ecdsa-sha2-nistp256 AAAAE2VjZHNhExample2',
    label: '',
    created_at: '2026-09-29T08:00:00Z',
  },
];

type Scenario = 'populated' | 'empty' | 'step-up' | 'email-unverified';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** Builds a fetch stub that answers the panel's `/v1/` calls for one scenario. */
function createStub(scenario: Scenario, realFetch: typeof fetch): typeof fetch {
  let keys: SSHKey[] = scenario === 'populated' ? [...SAMPLE_KEYS] : [];
  let registerAttempts = 0;

  return (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://stub.invalid');
    const path = url.pathname;
    const method = init?.method ?? 'GET';
    const headers = (init?.headers ?? {}) as Record<string, string>;

    if (!path.startsWith('/v1/')) return realFetch(input, init);

    if (path === '/v1/self/ssh-keys' && method === 'GET') {
      return jsonResponse(200, { items: keys, total: keys.length });
    }

    if (path === '/v1/self/ssh-keys' && method === 'POST') {
      registerAttempts += 1;
      if (scenario === 'email-unverified') {
        return jsonResponse(403, {
          action: {
            code: 'users.email_unverified',
            message: 'Verify your email address before continuing.',
            path: '/verify-email',
          },
        });
      }
      if (scenario === 'step-up' && !headers['X-Step-Up-Token']) {
        return jsonResponse(409, {
          action: {
            code: 'users.step_up_required',
            message: 'Confirm it is you to continue.',
            path: '/step-up',
          },
        });
      }
      const body = JSON.parse(String(init?.body ?? '{}')) as {
        public_key: string;
        label?: string;
      };
      const created: SSHKey = {
        uuid: `00000000-0000-4000-8000-${String(registerAttempts).padStart(12, '0')}`,
        key_type: body.public_key.split(' ')[0] ?? 'ssh-ed25519',
        fingerprint: `SHA256:story${String(registerAttempts).padStart(38, 'x')}`,
        public_key: body.public_key,
        label: body.label ?? '',
        created_at: new Date().toISOString(),
      };
      keys = [...keys, created];
      return jsonResponse(201, created);
    }

    if (path.startsWith('/v1/self/ssh-keys/') && method === 'DELETE') {
      if (scenario === 'email-unverified') {
        return jsonResponse(403, {
          action: {
            code: 'users.email_unverified',
            message: 'Verify your email address before continuing.',
            path: '/verify-email',
          },
        });
      }
      const uuid = decodeURIComponent(path.slice('/v1/self/ssh-keys/'.length));
      keys = keys.filter((key) => key.uuid !== uuid);
      return new Response(null, { status: 204 });
    }

    if (path === '/v1/self/credential/step-up') return new Response(null, { status: 204 });
    if (path === '/v1/self/credential/step-up/verify') {
      return jsonResponse(200, { step_up_token: 'story-token', expires_in: 300 });
    }

    return jsonResponse(404, { error: { code: 'not_found', message: 'Not stubbed in this story.' } });
  }) as typeof fetch;
}

/** Installs the stub before mounting the panel and restores `fetch` on unmount. */
function StubbedPanel({ scenario }: { scenario: Scenario }) {
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const realFetch = globalThis.fetch;
    globalThis.fetch = createStub(scenario, realFetch);
    setReady(true);
    return () => {
      globalThis.fetch = realFetch;
      setReady(false);
    };
  }, [scenario]);

  return ready ? <SSHKeysPanel onActionRequired={() => {}} /> : null;
}

export const Default: Story = () => (
  <div className="w-full max-w-3xl p-6">
    <SSHKeysPanel />
  </div>
);

export const Populated: Story = () => (
  <div className="w-full max-w-3xl p-6">
    <StubbedPanel scenario="populated" />
  </div>
);

export const Empty: Story = () => (
  <div className="w-full max-w-3xl p-6">
    <StubbedPanel scenario="empty" />
  </div>
);

// Add a key: the first POST is answered with the step-up action, which opens
// the inline challenge. Any 6-character code verifies.
export const StepUpChallenge: Story = () => (
  <div className="w-full max-w-3xl p-6">
    <StubbedPanel scenario="step-up" />
  </div>
);

// Add or revoke a key: the stub answers with `users.email_unverified`.
export const EmailUnverified: Story = () => (
  <div className="w-full max-w-3xl p-6">
    <StubbedPanel scenario="email-unverified" />
  </div>
);
