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
}

export function AuthPage({
  initialMode,
  onAuthenticated,
  initialError = null,
  returnPath = '/',
}: AuthPageProps) {
  // Internal, uncontrolled mode state — this module does not own routing
  // (per docs/mod-users-spec.md's Non-goals), so mode-switching must not
  // require the consumer to change URL or route. `initialMode` only seeds
  // the first render; subsequent toggling is entirely internal.
  const [mode, setMode] = useState<AuthMode>(initialMode ?? 'login');

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        {mode === 'login' ? (
          <>
            <CardHeader>
              <CardTitle>Sign in</CardTitle>
              <CardDescription>Enter your credentials to continue</CardDescription>
            </CardHeader>
            <CardContent>
              <LoginForm
                onSuccess={onAuthenticated}
                initialError={initialError}
                returnPath={returnPath}
              />
            </CardContent>
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
          </>
        ) : (
          <>
            <CardHeader>
              <CardTitle>Create an account</CardTitle>
              <CardDescription>Fill in your details to get started</CardDescription>
            </CardHeader>
            <CardContent>
              <RegisterForm onSuccess={onAuthenticated} />
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
          </>
        )}
      </Card>
    </div>
  );
}
