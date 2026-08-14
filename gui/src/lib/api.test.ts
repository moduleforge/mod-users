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
});
