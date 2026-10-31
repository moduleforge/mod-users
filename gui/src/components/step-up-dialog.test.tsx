import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { StepUpDialog } from './step-up-dialog';
import { api, ApiActionRequiredError } from '../lib/api';
import { getStoredToken } from '../lib/config';
import { PROFILE_A, tokenResponse } from '../test-support/users-fixtures';
import {
  errorReply,
  jsonResponse,
  mountSignedIn,
  setUpHarness,
  tearDownHarness,
} from '../test-support/chrome-harness';

beforeEach(setUpHarness);
afterEach(tearDownHarness);

const STEP_UP_ACTION = {
  action: {
    code: 'users.step_up_required',
    message: 'Confirm it is you.',
    path: '/step-up',
  },
};

/** A gated route: answers the step-up action until `unlocked`, then the list. */
function gatedSessions() {
  const state = { unlocked: false, hits: 0 };
  return {
    state,
    route: () => {
      state.hits += 1;
      return state.unlocked
        ? { sessions: [] }
        : jsonResponse(STEP_UP_ACTION, 409);
    },
  };
}

describe('StepUpDialog', () => {
  test('a 409 prompts, the password step-up succeeds and the request is retried once', async () => {
    const gate = gatedSessions();
    const m = await mountSignedIn(<StepUpDialog providers={[]} />, {
      profiles: [PROFILE_A],
      routes: {
        'GET /v1/me/sessions': gate.route,
        'POST /v1/session/step-up/password': () => {
          gate.state.unlocked = true;
          return tokenResponse({ access_token: 'fresh-access', refresh_token: null });
        },
      },
    });

    let result: Promise<unknown> = Promise.resolve();
    act(() => {
      result = api.me.sessions.list();
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Use my password' }));
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'correct horse' } });
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await expect(result).resolves.toEqual({ sessions: [] });
    // One 409, then exactly one replay: no loop.
    expect(gate.state.hits).toBe(2);
    const stepUps = m.calls.filter((c) => c.path === '/v1/session/step-up/password');
    expect(stepUps).toHaveLength(1);
    expect(stepUps[0]!.body).toEqual({ password: 'correct horse' });
    // The fresh token replaced the tab's access token; no step-up token exists.
    expect(getStoredToken()).toBe('fresh-access');
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  test('a wrong password keeps the prompt open with a message and does not retry', async () => {
    const gate = gatedSessions();
    await mountSignedIn(<StepUpDialog providers={[]} />, {
      profiles: [PROFILE_A],
      routes: {
        'GET /v1/me/sessions': gate.route,
        // On the step-up routes a refusal is the same 409 action.
        'POST /v1/session/step-up/password': () => jsonResponse(STEP_UP_ACTION, 409),
      },
    });
    let result: Promise<unknown> = Promise.resolve();
    act(() => {
      result = api.me.sessions.list().catch((err) => err);
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Use my password' }));
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'nope' } });
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    await screen.findByText('That password is wrong.');
    expect(gate.state.hits).toBe(1);

    // Cancelling abandons the retry: the caller sees the original action.
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(await result).toBeInstanceOf(ApiActionRequiredError);
    expect(gate.state.hits).toBe(1);
  });

  test('the emailed-code factor requests a code, enforces the code length and retries', async () => {
    const gate = gatedSessions();
    const m = await mountSignedIn(<StepUpDialog providers={[]} />, {
      profiles: [PROFILE_A],
      routes: {
        'GET /v1/me/sessions': gate.route,
        'POST /v1/session/step-up/email-code/request': () => undefined,
        'POST /v1/session/step-up/email-code/verify': () => {
          gate.state.unlocked = true;
          return tokenResponse({ access_token: 'fresh-by-code', refresh_token: null });
        },
      },
    });
    let result: Promise<unknown> = Promise.resolve();
    act(() => {
      result = api.me.sessions.list();
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Email me a code' }));
    fireEvent.click(screen.getByRole('button', { name: 'Send code' }));
    const input = await screen.findByLabelText('Verification code');
    // A short code cannot be submitted, and resend is on cooldown.
    fireEvent.change(input, { target: { value: '123' } });
    expect((screen.getByRole('button', { name: 'Verify and continue' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole('button', { name: /Resend code \(\d+s\)/ })).toBeTruthy();
    fireEvent.change(input, { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify and continue' }));

    await expect(result).resolves.toEqual({ sessions: [] });
    expect(gate.state.hits).toBe(2);
    expect(m.calls.find((c) => c.path === '/v1/session/step-up/email-code/verify')!.body).toEqual({
      code: '123456',
    });
    expect(getStoredToken()).toBe('fresh-by-code');
  });

  test('lists the supplied OIDC providers as a factor', async () => {
    const gate = gatedSessions();
    await mountSignedIn(
      <StepUpDialog providers={[{ id: 'google', display_name: 'Google' }]} />,
      { profiles: [PROFILE_A], routes: { 'GET /v1/me/sessions': gate.route } },
    );
    act(() => {
      void api.me.sessions.list().catch(() => undefined);
    });
    await screen.findByRole('button', { name: 'Continue with Google' });
  });

  test('a non-step-up failure on a factor call shows the server message', async () => {
    const gate = gatedSessions();
    await mountSignedIn(<StepUpDialog providers={[]} />, {
      profiles: [PROFILE_A],
      routes: {
        'GET /v1/me/sessions': gate.route,
        'POST /v1/session/step-up/password': () => errorReply(400, 'bad_request', 'Enter a password.'),
      },
    });
    act(() => {
      void api.me.sessions.list().catch(() => undefined);
    });
    fireEvent.click(await screen.findByRole('button', { name: 'Use my password' }));
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'x' } });
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    await screen.findByText('Enter a password.');
  });
});
