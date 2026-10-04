'use client';

import { useState } from 'react';
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@moduleforge/core-gui';
import { LoginForm } from './login-form';
import { RegisterForm } from './register-form';

export type AuthMode = 'login' | 'register';

export interface AuthPageProps {
  /** Which mode to render first. Defaults to `'login'`. */
  initialMode?: AuthMode;
  /** Called after a successful login or registration (either mode). */
  onAuthenticated?: () => void;
  /**
   * Initial error message forwarded into `LoginForm` — e.g. surfaced from an
   * OIDC callback's `?error=` query param by the consuming app. Only applies
   * while in login mode.
   */
  initialError?: string | null;
  /** Forwarded to `LoginForm`'s OIDC `return` path. Defaults to `'/'`. */
  returnPath?: string;
  /**
   * Whether the UI offers registration. Defaults to `true`. When `false`, the
   * "Create one" control and the register panel are not rendered (no
   * `RegisterForm` is mounted) and login mode is forced even when
   * `initialMode="register"`. This is a UI affordance only — the API's
   * register endpoint stays open.
   */
  allowRegistration?: boolean;
  /**
   * Forwarded to `LoginForm`. When supplied, a "Forgot password?" control is
   * rendered in the login form; the consumer owns navigation to its
   * `ForgotPasswordPage`.
   */
  onForgotPassword?: () => void;
}

export function AuthPage({
  initialMode,
  onAuthenticated,
  initialError = null,
  returnPath = '/',
  allowRegistration = true,
  onForgotPassword,
}: AuthPageProps) {
  // Internal, uncontrolled mode state — this module does not own routing
  // (per docs/mod-users-spec.md's Non-goals), so mode-switching must not
  // require the consumer to change URL or route. `initialMode` only seeds
  // the first render; subsequent toggling is entirely internal.
  const [requestedMode, setMode] = useState<AuthMode>(initialMode ?? 'login');
  const mode: AuthMode = allowRegistration ? requestedMode : 'login';

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        {/*
          Both modes stay mounted; only visibility toggles. This keeps
          LoginForm mounted across toggles so its fetchProviders() effect
          doesn't re-fire every time the user switches back from register.
          `contents` makes the wrapper transparent to Card's flex layout
          (which expects CardHeader/CardContent/CardFooter as direct flex
          children); `hidden` removes the inactive mode from the flow.
        */}
        <div className={mode === 'login' ? 'contents' : 'hidden'}>
          <CardHeader>
            <CardTitle>Sign in</CardTitle>
            <CardDescription>Enter your credentials to continue</CardDescription>
          </CardHeader>
          <CardContent>
            <LoginForm
              idPrefix="login"
              onSuccess={onAuthenticated}
              initialError={initialError}
              returnPath={returnPath}
              onForgotPassword={onForgotPassword}
            />
          </CardContent>
          {allowRegistration && (
            <CardFooter className="text-sm text-center">
              <p className="text-muted-foreground">
                No account?{' '}
                <button
                  type="button"
                  className="text-foreground hover:underline"
                  onClick={() => setMode('register')}
                >
                  Create one
                </button>
              </p>
            </CardFooter>
          )}
        </div>
        {allowRegistration && (
          <div className={mode === 'register' ? 'contents' : 'hidden'}>
            <CardHeader>
              <CardTitle>Create an account</CardTitle>
              <CardDescription>Fill in your details to get started</CardDescription>
            </CardHeader>
            <CardContent>
              <RegisterForm idPrefix="register" onSuccess={onAuthenticated} />
            </CardContent>
            <CardFooter className="text-sm text-center">
              <p className="text-muted-foreground">
                Already have an account?{' '}
                <button
                  type="button"
                  className="text-foreground hover:underline"
                  onClick={() => setMode('login')}
                >
                  Sign in
                </button>
              </p>
            </CardFooter>
          </div>
        )}
      </Card>
    </div>
  );
}
