import { afterEach, describe, expect, test } from 'bun:test';
import {
  ApiActionRequiredError,
  ApiRequestError,
  createUsersClient,
} from './api';

// Exercises `request()`'s non-2xx discrimination logic (the fix this task
// implements): a body carrying a top-level `action` member must short-circuit
// straight to `ApiActionRequiredError` and never reach the `ApiRequestError`
// path, a body carrying a top-level `error` object must still throw
// `ApiRequestError` exactly as before, and `action.path` must be sanitized
// against the documented attack shapes before being attached to the thrown
// error.

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
});

function stubFetch(body: unknown, status: number): void {
  globalThis.fetch = (async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })) as unknown as typeof fetch;
}

const client = createUsersClient({ baseUrl: 'http://localhost:8080' });

async function requestAndCatch(): Promise<unknown> {
  return client.self.get({ skipAuthRedirect: true }).catch((err: unknown) => err);
}

describe('request() action-required discrimination', () => {
  test('a 403 body with a top-level action throws ApiActionRequiredError with the right fields', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'Verify your email address before continuing.',
          path: '/verify-email',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;

    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(err).not.toBeInstanceOf(ApiRequestError);
    expect(err.code).toBe('users.email_unverified');
    expect(err.message).toBe('Verify your email address before continuing.');
    expect(err.path).toBe('/verify-email');
    expect(err.status).toBe(403);
    expect(err.data).toBeUndefined();
  });

  test('a 503 body with a top-level action carries optional auxiliary data through', async () => {
    stubFetch(
      {
        action: {
          code: 'users.oidc_not_confirmed',
          message: 'Single sign-on is not finished configuring.',
          path: '/oidc-config',
          data: { state: 'awaiting_oidc_config' },
        },
      },
      503,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;

    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(err.status).toBe(503);
    expect(err.data).toEqual({ state: 'awaiting_oidc_config' });
  });

  test('a 409 body with a top-level action (step-up) throws ApiActionRequiredError, never ApiRequestError', async () => {
    stubFetch(
      {
        action: {
          code: 'users.step_up_required',
          message: 'Confirm your identity to remove this login method.',
          path: '/step-up?return=%2Fself%2Fidentities',
        },
      },
      409,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;

    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(err).not.toBeInstanceOf(ApiRequestError);
    expect(err.path).toBe('/step-up?return=%2Fself%2Fidentities');
  });

  test('a 409 body with a top-level error object still throws ApiRequestError exactly as before (regression check)', async () => {
    stubFetch(
      {
        error: {
          code: 'conflict',
          message: "You can't remove your last sign-in method. Add another first.",
        },
      },
      409,
    );

    const err = (await requestAndCatch()) as ApiRequestError;

    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err).not.toBeInstanceOf(ApiActionRequiredError);
    expect(err.code).toBe('conflict');
    expect(err.message).toBe(
      "You can't remove your last sign-in method. Add another first.",
    );
    expect(err.status).toBe(409);
  });

  test('a 403 body with a top-level error object still throws ApiRequestError exactly as before (regression check)', async () => {
    stubFetch(
      { error: { code: 'forbidden', message: 'You may not access this.' } },
      403,
    );

    const err = (await requestAndCatch()) as ApiRequestError;

    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.code).toBe('forbidden');
    expect(err.status).toBe(403);
  });

  test('a flat string error member degrades gracefully to unknown_error instead of crashing (latent-bug fix)', async () => {
    stubFetch({ error: 'some_flat_string_error' }, 403);

    const err = (await requestAndCatch()) as ApiRequestError;

    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.code).toBe('unknown_error');
    expect(err.status).toBe(403);
  });
});

describe('request() action.path same-origin-relative guard', () => {
  test('accepts a well-formed single-leading-slash path unchanged', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'Verify your email address before continuing.',
          path: '/verify-email',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/verify-email');
  });

  test('rejects an empty path and falls back to the safe default', async () => {
    stubFetch(
      { action: { code: 'users.email_unverified', message: 'msg', path: '' } },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/');
  });

  test('rejects an absolute URL with a scheme and falls back to the safe default', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'msg',
          path: 'https://evil.example.com/steal',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/');
  });

  test('rejects a protocol-relative //host/... authority and falls back to the safe default', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'msg',
          path: '//evil.example.com/steal',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/');
  });

  test('rejects a /\\host/... backslash trick and falls back to the safe default', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'msg',
          path: '/\\evil.example.com/steal',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/');
  });

  test('rejects a leading-slash path with an embedded tab before a second slash and falls back to the safe default', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'msg',
          path: '/\t/evil.example.com',
        },
      },
      403,
    );

    const err = (await requestAndCatch()) as ApiActionRequiredError;
    expect(err.path).toBe('/');
  });
});

// ─── SSH keys / step-up client methods ───────────────────────────────────────

interface RecordedCall {
  url: string;
  method: string | undefined;
  headers: Record<string, string>;
  body: unknown;
}

function recordingFetch(
  body: unknown,
  status: number,
): { calls: RecordedCall[] } {
  const calls: RecordedCall[] = [];
  globalThis.fetch = (async (url: string, init?: RequestInit) => {
    calls.push({
      url,
      method: init?.method,
      headers: { ...(init?.headers as Record<string, string>) },
      body: init?.body,
    });
    return status === 204
      ? new Response(null, { status })
      : new Response(JSON.stringify(body), {
          status,
          headers: { 'Content-Type': 'application/json' },
        });
  }) as unknown as typeof fetch;
  return { calls };
}

describe('sshKeys / stepUp client methods', () => {
  test('list() sends no query string without params and adds ?limit=200', async () => {
    const rec = recordingFetch({ items: [], total: 0 }, 200);
    await client.sshKeys.list();
    await client.sshKeys.list({ limit: 200 });
    expect(rec.calls[0].url).toBe('http://localhost:8080/v1/self/ssh-keys');
    expect(rec.calls[0].method).toBeUndefined();
    expect(rec.calls[1].url).toBe('http://localhost:8080/v1/self/ssh-keys?limit=200');
  });

  test('register sends no step-up header by default or for an empty token', async () => {
    const rec = recordingFetch({}, 201);
    const data = { public_key: 'ssh-ed25519 AAAA', label: 'laptop' };
    await client.sshKeys.register(data);
    await client.sshKeys.register(data, { stepUpToken: '' });
    for (const call of rec.calls) {
      expect(call.method).toBe('POST');
      expect(call.headers['X-Step-Up-Token']).toBeUndefined();
      expect(call.body).toBe(JSON.stringify(data));
    }
  });

  test('register sends X-Step-Up-Token when a token is supplied', async () => {
    const rec = recordingFetch({}, 201);
    const data = { public_key: 'ssh-ed25519 AAAA' };
    await client.sshKeys.register(data, { stepUpToken: 't' });
    expect(rec.calls[0].headers['X-Step-Up-Token']).toBe('t');
    expect(rec.calls[0].body).toBe(JSON.stringify(data));
  });

  test('revoke URL-encodes the key uuid, sends the step-up header, and resolves undefined on 204', async () => {
    const rec = recordingFetch(null, 204);
    const result = await client.sshKeys.revoke('a/b c', { stepUpToken: 'tok' });
    expect(result).toBeUndefined();
    expect(rec.calls[0].url).toBe('http://localhost:8080/v1/self/ssh-keys/a%2Fb%20c');
    expect(rec.calls[0].method).toBe('DELETE');
    expect(rec.calls[0].headers['X-Step-Up-Token']).toBe('tok');
  });

  test('a 409 step-up action on register rejects with ApiActionRequiredError', async () => {
    stubFetch(
      {
        action: {
          code: 'users.step_up_required',
          message: 'Step-up required.',
          path: '/step-up',
        },
      },
      409,
    );
    const err = (await client.sshKeys
      .register({ public_key: 'k' })
      .catch((e: unknown) => e)) as ApiActionRequiredError;
    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(err.code).toBe('users.step_up_required');
    expect(err.path).toBe('/step-up');
    expect(err.status).toBe(409);
  });

  test('a 403 email_unverified action on revoke rejects with ApiActionRequiredError', async () => {
    stubFetch(
      {
        action: {
          code: 'users.email_unverified',
          message: 'Verify your email.',
          path: '/verify-email',
        },
      },
      403,
    );
    const err = (await client.sshKeys
      .revoke('u1')
      .catch((e: unknown) => e)) as ApiActionRequiredError;
    expect(err).toBeInstanceOf(ApiActionRequiredError);
    expect(err.code).toBe('users.email_unverified');
    expect(err.status).toBe(403);
  });

  test('a 409 ssh_key_in_use conflict rejects with ApiRequestError carrying details', async () => {
    const details = [
      { field: 'public_key', code: 'users.ssh_key_in_use', message: 'Key already in use.' },
    ];
    stubFetch({ error: { code: 'conflict', message: 'Conflict', details } }, 409);
    const err = (await client.sshKeys
      .register({ public_key: 'k' })
      .catch((e: unknown) => e)) as ApiRequestError;
    expect(err).toBeInstanceOf(ApiRequestError);
    expect(err.code).toBe('conflict');
    expect(err.status).toBe(409);
    expect(err.details).toEqual(details);
  });

  test('stepUp.request() POSTs and resolves undefined on 204', async () => {
    const rec = recordingFetch(null, 204);
    expect(await client.stepUp.request()).toBeUndefined();
    expect(rec.calls[0].url).toBe('http://localhost:8080/v1/self/credential/step-up');
    expect(rec.calls[0].method).toBe('POST');
  });

  test('stepUp.verify 401 rejects unauthenticated without clearing the session or redirecting', async () => {
    const rec = recordingFetch({ error: { code: 'unauthenticated', message: 'bad code' } }, 401);
    localStorage.setItem('auth_token', 'session-token');
    const hrefBefore = window.location.href;
    try {
      const err = (await client.stepUp
        .verify('000000')
        .catch((e: unknown) => e)) as ApiRequestError;
      expect(err).toBeInstanceOf(ApiRequestError);
      expect(err.code).toBe('unauthenticated');
      expect(localStorage.getItem('auth_token')).toBe('session-token');
      expect(window.location.href).toBe(hrefBefore);
      expect(rec.calls[0].url).toBe(
        'http://localhost:8080/v1/self/credential/step-up/verify',
      );
      expect(rec.calls[0].body).toBe(JSON.stringify({ code: '000000' }));
    } finally {
      localStorage.removeItem('auth_token');
    }
  });
});
