'use client';

import { useEffect, useState } from 'react';
import { Button } from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import { useAuth } from '../lib/auth-context';
import { api, ApiRequestError } from '../lib/api';

/** Formats a non-negative millisecond span as `m:ss`. */
function formatRemaining(ms: number): string {
  const total = Math.max(0, Math.ceil(ms / 1000));
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return `${minutes}:${String(seconds).padStart(2, '0')}`;
}

/**
 * Always-visible bar while this tab's session is an impersonation: who is being
 * impersonated, the time left, and renew / end actions. Renders nothing
 * otherwise. Ending restores the administrator's own token (done by the API
 * client), which flips the auth context back and hides the bar.
 */
export function ImpersonationBanner() {
  const { impersonating, profile, accessTokenExpiresAt } = useAuth();
  const [now, setNow] = useState(() => Date.now());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Tick only while there is a countdown to show.
  const counting = impersonating && accessTokenExpiresAt !== null;
  useEffect(() => {
    if (!counting) return undefined;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    // The interval is released on unmount and when the countdown stops.
    return () => clearInterval(id);
  }, [counting, accessTokenExpiresAt]);

  if (!impersonating) return null;

  async function act(kind: 'renew' | 'end') {
    setBusy(true);
    setError(null);
    try {
      if (kind === 'renew') await api.impersonations.renew();
      else await api.impersonations.end();
    } catch (err) {
      setError(
        err instanceof ApiRequestError
          ? err.message
          : kind === 'renew'
            ? 'Could not renew the impersonation.'
            : 'Could not end the impersonation.',
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-3 border-b bg-amber-100 px-4 py-2 text-sm text-amber-950"
    >
      <span className="font-medium">
        {`Impersonating ${profile?.display_name ?? 'a profile'}`}
      </span>
      {/* After a reload no token response has arrived, so the deadline is unknown. */}
      <span>
        {accessTokenExpiresAt !== null
          ? `${formatRemaining(accessTokenExpiresAt - now)} remaining`
          : 'Time remaining unknown'}
      </span>
      <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => void act('renew')}>
        Renew
      </Button>
      <Button type="button" size="sm" disabled={busy} onClick={() => void act('end')}>
        End impersonation
      </Button>
      <ErrorMessage message={error} />
    </div>
  );
}
