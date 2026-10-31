'use client';

import { useState } from 'react';
import { Button } from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import { api, ApiRequestError, type LockRemedy } from '../lib/api';
import { isSafeReturnPath } from '../lib/return-path';
import { describeRemedy } from '../lib/profile-lock';

export interface ProfileRemedyProps {
  /** The remedy to present; null renders nothing. */
  remedy: LockRemedy | null;
}

/**
 * True for an absolute http(s) URL. The authorize URL comes from the server,
 * but it is about to become a full-page navigation, so only web schemes may
 * reach that sink (never `javascript:` or `data:`).
 */
function isWebUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' || url.protocol === 'http:';
  } catch {
    return false;
  }
}

/**
 * What would lift a profile lock: a readable description, plus an action for
 * `sign_in_with_provider` (the provider step-up).
 */
export function ProfileRemedy({ remedy }: ProfileRemedyProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const description = describeRemedy(remedy);
  if (!remedy || !description) return null;

  async function startProviderStepUp(providerId: string) {
    setBusy(true);
    setError(null);
    try {
      // Come back to the page the user is on once the provider step-up ends.
      const here = window.location.pathname;
      const { authorize_url } = await api.session.stepUp.startOidc(
        providerId,
        isSafeReturnPath(here) ? here : undefined,
      );
      if (!isWebUrl(authorize_url)) {
        setError('The sign-in provider returned an address that cannot be opened.');
        return;
      }
      // Full page navigation: the provider owns the next screens.
      window.location.assign(authorize_url);
    } catch (err) {
      setError(
        err instanceof ApiRequestError
          ? err.message
          : 'Could not start the provider sign-in.',
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mt-1 space-y-1.5">
      <p className="text-xs text-muted-foreground">{description}</p>
      {/* Only this remedy has an in-place fix; the others happen elsewhere. */}
      {remedy.kind === 'sign_in_with_provider' && remedy.provider_id && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={busy}
          onClick={() => void startProviderStepUp(remedy.provider_id as string)}
        >
          {`Sign in with ${remedy.provider_id}`}
        </Button>
      )}
      <ErrorMessage message={error} />
    </div>
  );
}
