'use client';

import { useEffect, useRef, useState } from 'react';
import { useAuth } from '../lib/auth-context';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@moduleforge/core-gui';

export interface OidcCallbackPageProps {
  /**
   * Called once the callback completes successfully, with the site-relative
   * path the consumer should navigate to. This is either the validated
   * `return` value carried in the URL fragment, or `defaultReturnPath` when
   * that value is absent or fails the safety check below.
   */
  onComplete: (returnPath: string) => void;
  /**
   * Called when the callback fails: a provider-reported `?error=`, a
   * malformed/missing token, or a failed session hydration. `message` is
   * safe to display directly or forward as a login page's `?error=` value.
   */
  onError: (message: string) => void;
  /** Fallback return path when no safe `return` value is present. Defaults to `'/'`. */
  defaultReturnPath?: string;
}

/**
 * Validates that a candidate return path is a safe, same-origin relative
 * path. Rejects absolute URLs, protocol-relative URLs (`//evil.com`), and
 * any string attempting to embed a scheme before the first slash.
 */
function isSafeReturnPath(candidate: string | null): candidate is string {
  if (!candidate) return false;
  // Must start with exactly one '/'.
  if (!candidate.startsWith('/')) return false;
  // Reject protocol-relative paths like `//evil.com/foo`.
  if (candidate.startsWith('//')) return false;
  // Reject backslashes anywhere in the path. Some legacy browser URL parsers
  // normalize `\` to `/`, so `/\evil.com` can be interpreted as `//evil.com`
  // and trigger an open redirect. Belt-and-suspenders: disallow `\` outright.
  if (candidate.includes('\\')) return false;
  // Reject anything trying to sneak a scheme in before the path separator,
  // e.g. `/\x0Ajavascript:...` variants or `javascript:...`. Since we already
  // require a leading `/`, a `:` anywhere in the first segment is suspicious.
  const firstSlashAfterStart = candidate.indexOf('/', 1);
  const firstSegment =
    firstSlashAfterStart === -1
      ? candidate.slice(1)
      : candidate.slice(1, firstSlashAfterStart);
  if (firstSegment.includes(':')) return false;
  return true;
}

/**
 * Minimal structural JWT check — three non-empty base64url segments. Full
 * cryptographic validation happens server-side when we exchange the token
 * for the `/v1/self` response.
 */
function looksLikeJwt(token: string): boolean {
  const parts = token.split('.');
  if (parts.length !== 3) return false;
  const b64url = /^[A-Za-z0-9_-]+$/;
  return parts.every((p) => p.length > 0 && b64url.test(p));
}

export function OidcCallbackPage({
  onComplete,
  onError,
  defaultReturnPath = '/',
}: OidcCallbackPageProps) {
  const { completeExternalLogin } = useAuth();
  const [message, setMessage] = useState<string>('Signing you in...');
  // Guard against React 19 StrictMode's double-invoke and any other
  // accidental re-run of the effect — we must only consume the token once.
  const hasProcessed = useRef(false);

  useEffect(() => {
    if (hasProcessed.current) return;
    hasProcessed.current = true;

    async function run() {
      // Error path: server redirected with ?error=... in the query string.
      const query = new URLSearchParams(window.location.search);
      const errorParam = query.get('error');
      if (errorParam) {
        onError(errorParam);
        return;
      }

      // Success path: token + return come back in the fragment so they are
      // never sent to the server in access logs.
      const rawHash = window.location.hash.startsWith('#')
        ? window.location.hash.slice(1)
        : window.location.hash;
      const hashParams = new URLSearchParams(rawHash);
      const token = hashParams.get('token');
      const returnCandidate = hashParams.get('return');

      if (!token || !looksLikeJwt(token)) {
        onError('Sign-in failed: malformed response from authentication provider.');
        return;
      }

      // Remove the hash from the URL so the token does not linger in the
      // browser history. Must happen before any callback fires.
      try {
        window.history.replaceState(null, '', window.location.pathname);
      } catch {
        // history API may be unavailable in test environments — ignore.
      }

      const returnPath = isSafeReturnPath(returnCandidate)
        ? returnCandidate
        : defaultReturnPath;

      try {
        await completeExternalLogin(token);
      } catch (err) {
        console.error('[oidc-callback] completeExternalLogin failed', err);
        setMessage('Sign-in failed. Redirecting...');
        onError('Sign-in failed: your session could not be established.');
        return;
      }

      onComplete(returnPath);
    }

    void run();
  }, [completeExternalLogin, defaultReturnPath, onComplete, onError]);

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
