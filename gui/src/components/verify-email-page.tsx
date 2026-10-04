'use client';

import { useEffect, useId, useRef, useState } from 'react';
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
import { useOptionalAuth } from '../lib/auth-context';
import { api, ApiRequestError } from '../lib/api';

const DEFAULT_MESSAGE = 'Verify your email address before continuing.';
const INVALID_CODE_MESSAGE = 'That code is invalid or has expired. Request a new one.';
const GENERIC_ERROR_MESSAGE = 'Something went wrong. Check the browser console for details.';

export interface VerifyEmailPageProps {
  /**
   * Called once after a successful verification (and after the signed-in
   * user is refreshed, when an `AuthProvider` is present). The app owns the
   * navigation. Defaults to a no-op.
   */
  onVerified?: () => void;
  /**
   * When supplied, renders a "Sign in with a different account" button. The
   * page itself never signs the user out.
   */
  onNavigateToLogin?: () => void;
  /**
   * The address to verify. Defaults to the signed-in user's email from the
   * surrounding `AuthProvider`; when neither is available the page shows an
   * email field first.
   */
  email?: string;
  /** Lead sentence. Defaults to the backend's own action-required text. */
  message?: string;
  /**
   * Client-side throttle (seconds) on "Send a new code". Defaults to 30;
   * `0` disables it.
   */
  resendCooldownSeconds?: number;
}

/**
 * Standard, router-agnostic "verify your email" screen: the target of the
 * backend's `users.email_unverified` action-required navigation
 * (`/verify-email`). Verification is by the 6-digit emailed code only.
 * Works with or without an `AuthProvider`.
 */
export function VerifyEmailPage({
  onVerified,
  onNavigateToLogin,
  email: emailProp,
  message = DEFAULT_MESSAGE,
  resendCooldownSeconds = 30,
}: VerifyEmailPageProps) {
  const auth = useOptionalAuth();
  const knownEmail = emailProp ?? auth?.user?.email ?? undefined;
  const [typedEmail, setTypedEmail] = useState('');
  const email = (knownEmail ?? typedEmail).trim();

  const [code, setCode] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [isSending, setIsSending] = useState(false);
  const [isVerifying, setIsVerifying] = useState(false);
  const [verified, setVerified] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const [focusCode, setFocusCode] = useState(0);

  const emailId = useId();
  const codeId = useId();
  const cooldownHintId = useId();
  const codeRef = useRef<HTMLInputElement>(null);

  // Cooldown ticker: timers live only in effects (no browser API at render).
  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setTimeout(() => setCooldown((c) => c - 1), 1000);
    return () => clearTimeout(timer);
  }, [cooldown]);

  // Move focus to the code input after a code was sent.
  useEffect(() => {
    if (focusCode > 0) codeRef.current?.focus();
  }, [focusCode]);

  function describeError(err: unknown): string {
    if (err instanceof ApiRequestError) {
      // Every 401 is surfaced by the client as a fixed "Authentication
      // required"; on this page it means a wrong/expired code.
      return err.status === 401 ? INVALID_CODE_MESSAGE : err.message;
    }
    console.error('[verify-email]', err);
    return GENERIC_ERROR_MESSAGE;
  }

  async function handleResend() {
    if (!email || isSending || cooldown > 0) return;
    setError(null);
    setStatus(null);
    setIsSending(true);
    try {
      await api.auth.requestEmailCode(
        { email, purpose: 'verify_email' },
        { skipAuthRedirect: true },
      );
      setStatus(`A new code was sent to ${email}. It expires in 5 minutes.`);
      setCooldown(Math.max(0, Math.floor(resendCooldownSeconds)));
      setFocusCode((n) => n + 1);
    } catch (err) {
      setError(describeError(err));
    } finally {
      setIsSending(false);
    }
  }

  async function handleVerify(e: React.FormEvent) {
    e.preventDefault();
    if (!email || isVerifying) return;
    setError(null);
    setStatus(null);
    setIsVerifying(true);
    try {
      await api.auth.verifyEmail({ email, code });
    } catch (err) {
      setError(describeError(err));
      setIsVerifying(false);
      return;
    }
    try {
      await auth?.refreshUser();
    } catch {
      // The email is verified regardless; a failed refresh must not block it.
    }
    setVerified(true);
    setIsVerifying(false);
    onVerified?.();
  }

  const cooling = cooldown > 0;

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>{verified ? 'Email verified' : 'Verify your email'}</CardTitle>
          <CardDescription>
            {verified ? (
              'Your email address has been verified.'
            ) : (
              <>
                {message}
                {email ? (
                  <>
                    {' '}
                    Enter the 6-digit code we sent to{' '}
                    <span className="font-medium text-foreground">{email}</span>.
                  </>
                ) : null}
              </>
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="flex flex-col gap-4">
            <ErrorMessage message={error} />
            <div aria-live="polite" role="status" className="text-sm text-muted-foreground">
              {verified ? 'Email verified' : status}
            </div>
            {verified ? null : (
              <>
                {knownEmail === undefined ? (
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor={emailId}>Email</Label>
                    <Input
                      id={emailId}
                      type="email"
                      autoComplete="email"
                      required
                      value={typedEmail}
                      onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
                        setTypedEmail(e.target.value)
                      }
                      placeholder="you@example.com"
                    />
                  </div>
                ) : null}
                <form onSubmit={handleVerify} className="flex flex-col gap-4">
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor={codeId}>Code</Label>
                    <Input
                      id={codeId}
                      ref={codeRef}
                      type="text"
                      inputMode="numeric"
                      autoComplete="one-time-code"
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
                  <Button
                    type="submit"
                    className="w-full"
                    disabled={isVerifying || !email}
                    aria-busy={isVerifying}
                  >
                    {isVerifying ? 'Verifying...' : 'Verify email'}
                  </Button>
                </form>
                <Button
                  type="button"
                  variant="outline"
                  className="w-full"
                  onClick={() => void handleResend()}
                  disabled={isSending || cooling || !email}
                  aria-busy={isSending}
                  aria-describedby={cooling ? cooldownHintId : undefined}
                >
                  {isSending ? 'Sending...' : 'Send a new code'}
                </Button>
                {cooling ? (
                  <p id={cooldownHintId} className="text-xs text-muted-foreground text-center">
                    You can request another code in {cooldown}s.
                  </p>
                ) : null}
              </>
            )}
          </div>
        </CardContent>
        {onNavigateToLogin && !verified ? (
          <CardFooter className="text-sm text-center">
            <button
              type="button"
              className="text-foreground hover:underline"
              onClick={() => onNavigateToLogin()}
            >
              Sign in with a different account
            </button>
          </CardFooter>
        ) : null}
      </Card>
    </div>
  );
}
