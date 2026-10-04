import { afterEach, beforeEach, describe, expect, test } from 'bun:test';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AuthProvider } from '../lib/auth-context';
import { resetUsersApiConfig } from '../lib/config';
import { AuthPage } from './auth-page';
import { LoginForm } from './login-form';

const originalFetch = globalThis.fetch;

beforeEach(() => {
  // fetchProviders() runs on LoginForm mount; return no providers.
  globalThis.fetch = (async () =>
    new Response('[]', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })) as unknown as typeof fetch;
});

afterEach(() => {
  cleanup();
  globalThis.fetch = originalFetch;
  localStorage.clear();
  resetUsersApiConfig();
});

/** Lets the fetchProviders() effect settle so state updates don't leak. */
async function settle() {
  await waitFor(() => expect(document.getElementById('login-email')).not.toBeNull());
}

describe('AuthPage allowRegistration', () => {
  test('default shows "Create one" and toggles to the register panel', async () => {
    render(
      <AuthProvider>
        <AuthPage />
      </AuthProvider>,
    );
    await settle();
    const createOne = screen.getByRole('button', { name: 'Create one' });
    const registerHeading = screen.getByText('Create an account');
    expect(registerHeading.closest('.hidden')).not.toBeNull();
    fireEvent.click(createOne);
    expect(registerHeading.closest('.hidden')).toBeNull();
    expect(registerHeading.closest('.contents')).not.toBeNull();
  });

  test('false hides "Create one" and mounts no register panel', async () => {
    render(
      <AuthProvider>
        <AuthPage allowRegistration={false} />
      </AuthProvider>,
    );
    await settle();
    expect(screen.queryByText('Create one')).toBeNull();
    expect(screen.queryByText('Create an account')).toBeNull();
    expect(screen.queryByText('Already have an account?')).toBeNull();
    expect(document.getElementById('register-email')).toBeNull();
    expect(document.getElementById('register-password')).toBeNull();
  });

  test('false forces login mode even with initialMode="register"', async () => {
    render(
      <AuthProvider>
        <AuthPage allowRegistration={false} initialMode="register" />
      </AuthProvider>,
    );
    await settle();
    const signIn = screen.getByText('Sign in', { selector: '[data-slot="card-title"], div, h3' });
    expect(signIn.closest('.hidden')).toBeNull();
    expect(screen.queryByText('Create an account')).toBeNull();
  });
});

describe('Forgot password control', () => {
  test('is absent by default in LoginForm and AuthPage', async () => {
    const { unmount } = render(
      <AuthProvider>
        <LoginForm />
      </AuthProvider>,
    );
    await settle();
    expect(screen.queryByText('Forgot password?')).toBeNull();
    unmount();
    render(
      <AuthProvider>
        <AuthPage />
      </AuthProvider>,
    );
    await settle();
    expect(screen.queryByText('Forgot password?')).toBeNull();
  });

  test('calls the callback when present and does not submit the form', async () => {
    let calls = 0;
    let submits = 0;
    render(
      <AuthProvider>
        <div onSubmit={() => submits++}>
          <AuthPage onForgotPassword={() => calls++} />
        </div>
      </AuthProvider>,
    );
    await settle();
    const button = screen.getByRole('button', { name: 'Forgot password?' });
    expect(button.getAttribute('type')).toBe('button');
    fireEvent.click(button);
    expect(calls).toBe(1);
    expect(submits).toBe(0);
  });
});
