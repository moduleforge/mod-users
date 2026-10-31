'use client';

import { useState } from 'react';
import { Button } from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import { ProfileRemedy } from './profile-remedy';
import { profileLabel } from './profile-switcher';
import { useAuth } from '../lib/auth-context';
import { ApiRequestError, type TokenResponse } from '../lib/api';
import { getStoredSelectionReturnPath } from '../lib/config';
import { isSafeReturnPath } from '../lib/return-path';
import { describeLockReason } from '../lib/profile-lock';

export interface SelectProfilePageProps {
  /**
   * Called after the chosen profile has landed the tab. The host navigates to
   * its home page here. `resumePath` is the validated return path to resume
   * at: the `returnPath` prop, else the path stored for this tab, else
   * `defaultReturnPath`.
   */
  onSelected?: (response: TokenResponse, resumePath: string) => void;
  /**
   * The return path handed over by `onSelectionRequired`. When omitted, the
   * path stored for this tab by the sign-in is used (it survives a reload).
   * Either source is checked with `isSafeReturnPath`.
   */
  returnPath?: string | null;
  /** Where to resume when no safe return path exists. Defaults to `'/'`. */
  defaultReturnPath?: string;
}

/**
 * Landing profile selection, mounted at `USERS_GUI_ROUTES.selectProfile`.
 * Shown when a sign-in answered `selection_required`; lists every profile with
 * its availability and lands the tab on the chosen available one.
 */
export function SelectProfilePage({
  onSelected,
  returnPath,
  defaultReturnPath = '/',
}: SelectProfilePageProps) {
  const { profiles, selectionRequired, selectProfile } = useAuth();
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Visiting the route with a landed profile has nothing to choose.
  if (!selectionRequired) return null;

  async function handleSelect(uuid: string) {
    setPending(uuid);
    setError(null);
    // Read before the grant: landing the profile clears the stored path.
    const candidate = returnPath ?? getStoredSelectionReturnPath();
    try {
      const response = await selectProfile(uuid);
      // The path came from a URL or from storage, both attacker-influenceable;
      // an unsafe one falls back to the default so the page never redirects
      // off-site or to a non-path.
      onSelected?.(
        response,
        isSafeReturnPath(candidate) ? candidate : defaultReturnPath,
      );
    } catch (err) {
      setError(
        err instanceof ApiRequestError
          ? err.message
          : 'Could not select that profile.',
      );
    } finally {
      setPending(null);
    }
  }

  return (
    <main className="mx-auto max-w-md space-y-4 p-6">
      <h1 className="text-xl font-semibold">Choose a profile</h1>
      <p className="text-sm text-muted-foreground">
        Pick the profile to continue as. You can switch later.
      </p>
      <ul className="space-y-2">
        {profiles.map((p) => (
          <li key={p.uuid} className="rounded-md border p-3">
            <div className="flex items-center justify-between gap-2">
              <span className="text-sm font-medium">
                {profileLabel(p)}
                <span className="ml-1 text-xs font-normal text-muted-foreground">
                  {p.kind === 'party' ? 'Party' : 'Personal'}
                </span>
              </span>
              {p.available && (
                <Button
                  type="button"
                  size="sm"
                  disabled={pending !== null}
                  onClick={() => void handleSelect(p.uuid)}
                  aria-label={`Continue as ${profileLabel(p)}`}
                >
                  Continue
                </Button>
              )}
            </div>
            {/* A locked profile cannot be selected; say why and what fixes it. */}
            {!p.available && (
              <div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {p.lock
                    ? describeLockReason(p.lock.reason)
                    : 'This profile is not available to this session.'}
                </p>
                <ProfileRemedy remedy={p.lock?.remedy ?? null} />
              </div>
            )}
          </li>
        ))}
      </ul>
      <ErrorMessage message={error} />
    </main>
  );
}
