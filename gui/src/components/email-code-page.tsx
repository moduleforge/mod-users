'use client';

import { useState } from 'react';
import {
  Button,
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
  Input,
  Label,
} from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import { useAuth } from '../lib/auth-context';
import { api, ApiRequestError } from '../lib/api';
import { readReturnPath } from '../lib/return-path';
import { describeSignInError, finishSignIn } from '../lib/sign-in';

type Step = 'request' | 'verify';

export interface EmailCodePageProps {
  /**
   * Called after successful code verification and sign-in with the effective
   * return path (the validated `?<unauthenticatedReturnParam>=` value, else
   * `null`). Callbacks that declare no parameter keep working. Defaults to a
   * no-op.
   */
  onSuccess?: (returnPath: string | null) => void;
  /** The app (slug) the session signs in to; sent as `app` on verify. */
  app?: string;
  /**
   * Called instead of `onSuccess` when the sign-in found no landing profile
   * (`selection_required`). When omitted, `onSuccess` receives the
   * profile-selection route in place of the return path.
   */
  onSelectionRequired?: (selectProfilePath: string, returnPath: string | null) => void;
  /** Called when the user clicks "Sign in with password instead". Defaults to a no-op. */
  onNavigateToLogin?: () => void;
}

export function EmailCodePage({
  onSuccess,
  app,
  onSelectionRequired,
  onNavigateToLogin,
}: EmailCodePageProps) {
  // This component (unlike ForgotPasswordPage/ResetPasswordPage) calls
  // useAuth() directly to establish the session after a successful code
  // verification, matching LoginForm's use of useAuth().login().
  const { loginWithEmailCode } = useAuth();
  const [step, setStep] = useState<Step>('request');
  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleRequestSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      await api.auth.requestEmailCode({ email });
      setStep('verify');
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        console.error('[email-code]', err);
        setError('Something went wrong. Check the browser console for details.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleVerifySubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      const response = await loginWithEmailCode(email, code, app ? { app } : undefined);
      finishSignIn(response, readReturnPath(), { onSuccess, onSelectionRequired });
    } catch (err) {
      const known = describeSignInError(err);
      if (err instanceof ApiRequestError && err.code === 'users.invalid_credentials') {
        // Wrong, expired or unknown-user code: the API gives all three the
        // credentials code, so say "code", not "password".
        setError('Invalid or expired code.');
      } else if (known) {
        // Other coded refusals (BYO identity off, app access not granted,
        // profile unavailable) have shared copy.
        setError(known);
      } else if (err instanceof ApiRequestError && err.status === 401) {
        setError('Invalid or expired code.');
      } else if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        console.error('[email-code]', err);
        setError('Something went wrong. Check the browser console for details.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  function handleTryDifferentEmail() {
    // Internal same-component state reset, not a navigation callback —
    // mirrors AuthPage's internal mode-toggle precedent.
    setStep('request');
    setCode('');
    setError(null);
  }

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        {step === 'request' ? (
          <>
            <CardHeader>
              <CardTitle>Sign in with email code</CardTitle>
              <CardDescription>
                We will send a one-time code to your email address.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <form onSubmit={handleRequestSubmit} className="flex flex-col gap-4">
                <ErrorMessage message={error} />
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="email">Email</Label>
                  <Input
                    id="email"
                    type="email"
                    autoComplete="email"
                    required
                    value={email}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) => setEmail(e.target.value)}
                    placeholder="you@example.com"
                  />
                </div>
                <Button type="submit" className="w-full" disabled={isSubmitting}>
                  {isSubmitting ? 'Sending...' : 'Send code'}
                </Button>
              </form>
            </CardContent>
            <CardFooter className="text-sm text-center">
              <p className="text-muted-foreground">
                <button
                  type="button"
                  className="text-foreground hover:underline"
                  onClick={() => onNavigateToLogin?.()}
                >
                  Sign in with password instead
                </button>
              </p>
            </CardFooter>
          </>
        ) : (
          <>
            <CardHeader>
              <CardTitle>Enter your code</CardTitle>
              <CardDescription>
                We sent a code to <span className="font-medium text-foreground">{email}</span>.
                It expires in 5 minutes.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <form onSubmit={handleVerifySubmit} className="flex flex-col gap-4">
                <ErrorMessage message={error} />
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="code">Code</Label>
                  <Input
                    id="code"
                    type="text"
                    inputMode="numeric"
                    pattern="[0-9]{6}"
                    maxLength={6}
                    required
                    value={code}
                    onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
                      setCode(e.target.value.replace(/\D/g, ''))
                    }
                    placeholder="000000"
                    className="tracking-widest text-center text-lg"
                  />
                </div>
                <Button type="submit" className="w-full" disabled={isSubmitting}>
                  {isSubmitting ? 'Verifying...' : 'Verify code'}
                </Button>
              </form>
            </CardContent>
            <CardFooter className="text-sm text-center">
              <button
                type="button"
                className="text-foreground hover:underline"
                onClick={handleTryDifferentEmail}
              >
                Try a different email
              </button>
            </CardFooter>
          </>
        )}
      </Card>
    </div>
  );
}
