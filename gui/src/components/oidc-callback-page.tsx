'use client';

import { useEffect, useRef, useState } from 'react';
import { useAuth } from '../lib/auth-context';
import { ApiRequestError, USERS_ERROR_CODES } from '../lib/api';
import { isSafeReturnPath } from '../lib/return-path';
import { finishSignIn } from '../lib/sign-in';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@moduleforge/core-gui';

export interface OidcCallbackPageProps {
  /**
   * Called once the callback completes successfully, with the site-relative
   * path the consumer should navigate to. This is either the validated
   * `return` value carried in the URL fragment, or `defaultReturnPath` when
   * that value is absent or fails the safety check below. When the sign-in
   * ended with no landing profile and `onSelectionRequired` is not supplied,
   * this receives the profile-selection route (`USERS_GUI_ROUTES.selectProfile`)
   * instead.
   */
  onComplete: (returnPath: string) => void;
  /**
   * Called when the callback fails: a provider-refused sign-in
   * (`#error=<code>`), a missing handoff code, a sign-in not started in this
   * tab, or a failed exchange. `message` is readable and safe to display
   * directly, but if forwarding it as a login page's `?error=` query-string
   * value, the consumer must `encodeURIComponent` it first, e.g.
   * `` `/auth/login?error=${encodeURIComponent(message)}` ``.
   */
  onError: (message: string) => void;
  /**
   * Called instead of `onComplete` when the sign-in succeeded but found no
   * landing profile (`selection_required`): the consumer navigates to
   * `selectProfilePath` and resumes at `returnPath` after the choice.
   */
  onSelectionRequired?: (selectProfilePath: string, returnPath: string) => void;
  /** Fallback return path when no safe `return` value is present. Defaults to `'/'`. */
  defaultReturnPath?: string;
}

/**
 * Readable copy for the `#error=<code>` values the OIDC callback redirects
 * with. The set is closed server-side, but the fragment is attacker-shapeable
 * input: an unlisted code gets the generic copy and is never echoed.
 */
const OIDC_ERROR_COPY: Record<string, string> = {
  account_exists_sign_in_and_link:
    'An account with this email address already exists. Sign in with your existing sign-in method, then link this provider from your account settings.',
  no_account:
    'No account exists for this identity, and this site does not create accounts from this provider.',
  authentication_failed:
    'The identity provider could not confirm your identity. Please try again.',
  missing_email:
    'The identity provider did not share an email address, which this site needs to sign you in.',
};

const GENERIC_ERROR = 'Sign-in failed. Please try again.';
const VERIFIER_MISSING_ERROR =
  'Sign-in must be completed in the same browser tab where it was started. Please start again from the sign-in page.';

export function OidcCallbackPage({
  onComplete,
  onError,
  onSelectionRequired,
  defaultReturnPath = '/',
}: OidcCallbackPageProps) {
  const { completeHandoff } = useAuth();
  const [message, setMessage] = useState<string>('Signing you in...');
  // Guard against React 19 StrictMode's double-invoke and any other
  // accidental re-run of the effect — the handoff code is single-use, and the
  // fragment is already stripped after the first run.
  const hasProcessed = useRef(false);

  useEffect(() => {
    if (hasProcessed.current) return;
    hasProcessed.current = true;

    function fail(text: string) {
      // Show the reason here as well as reporting it: a consumer that does
      // not navigate away would otherwise leave "Signing you in..." forever.
      setMessage(text);
      onError(text);
    }

    async function run() {
      // The redirect carries its result in the fragment so it never reaches
      // server access logs.
      const rawHash = window.location.hash.startsWith('#')
        ? window.location.hash.slice(1)
        : window.location.hash;
      const hashParams = new URLSearchParams(rawHash);
      const handoff = hashParams.get('handoff');
      const errorCode = hashParams.get('error');
      const returnCandidate = hashParams.get('return');

      // Strip the fragment before anything else can run or throw: the
      // one-time code must not linger in history, and must not be re-read by
      // a remount (which would attempt a second, doomed exchange).
      try {
        window.history.replaceState(null, '', window.location.pathname + window.location.search);
      } catch {
        // history API may be unavailable in test environments — ignore.
      }

      if (errorCode) {
        // A refusal: no code to exchange. Map to copy instead of echoing the
        // untrusted fragment value.
        fail(OIDC_ERROR_COPY[errorCode] ?? GENERIC_ERROR);
        return;
      }

      if (!handoff) {
        // Neither a code nor an error: the page was opened directly or the
        // redirect was mangled. Nothing to exchange.
        fail(GENERIC_ERROR);
        return;
      }

      const returnPath = isSafeReturnPath(returnCandidate)
        ? returnCandidate
        : defaultReturnPath;

      let response;
      try {
        response = await completeHandoff(handoff);
      } catch (err) {
        if (
          err instanceof ApiRequestError &&
          err.code === USERS_ERROR_CODES.handoffVerifierMissing
        ) {
          // This tab holds no verifier: another tab or browser started the
          // sign-in, so the code cannot be redeemed here.
          fail(VERIFIER_MISSING_ERROR);
        } else {
          console.error('[oidc-callback] completeHandoff failed', err);
          fail('Sign-in failed: your session could not be established.');
        }
        return;
      }

      finishSignIn(response, returnPath, {
        onSuccess: (path) => onComplete(path ?? defaultReturnPath),
        onSelectionRequired: onSelectionRequired
          ? (selectPath, resumePath) =>
              onSelectionRequired(selectPath, resumePath ?? defaultReturnPath)
          : undefined,
      });
    }

    void run();
  }, [completeHandoff, defaultReturnPath, onComplete, onError, onSelectionRequired]);

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Signing you in</CardTitle>
          <CardDescription>{message}</CardDescription>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">
            One moment while we finish setting up your session.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
