'use client';

import { useState } from 'react';
import { Button } from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import { ProfileRemedy } from './profile-remedy';
import { profileLabel } from './profile-switcher';
import { useAuth } from '../lib/auth-context';
import { ApiRequestError } from '../lib/api';
import { describeLockReason } from '../lib/profile-lock';

/**
 * Shown in place of protected content when the server answered
 * `401 users.profile_unavailable`: the reason, the remedy, and a way out
 * (another available profile, or signing out). Renders nothing when no lock
 * is set.
 */
export function ProfileLockNotice() {
  const { profileLock, profiles, selectProfile, logout } = useAuth();
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  if (!profileLock) return null;

  // The locked profile cannot be the way out; offer only usable others.
  const alternatives = profiles.filter(
    (p) => p.available && p.uuid !== profileLock.profileUuid,
  );

  async function handleSelect(uuid: string) {
    setPending(uuid);
    setError(null);
    try {
      // The locked access token cannot call the switch route, so recovery goes
      // through the refresh grant with the chosen profile.
      await selectProfile(uuid);
    } catch (err) {
      setError(
        err instanceof ApiRequestError
          ? err.message
          : 'Could not switch profile.',
      );
    } finally {
      setPending(null);
    }
  }

  return (
    <div role="alert" className="mx-auto max-w-md space-y-4 p-6">
      <h2 className="text-lg font-semibold">This profile is unavailable</h2>
      <p className="text-sm">{describeLockReason(profileLock.reason)}</p>
      <ProfileRemedy remedy={profileLock.remedy} />
      {alternatives.length > 0 && (
        <div className="space-y-2">
          <p className="text-sm font-medium">Use another profile</p>
          <ul className="space-y-1">
            {alternatives.map((p) => (
              <li key={p.uuid}>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={pending !== null}
                  onClick={() => void handleSelect(p.uuid)}
                >
                  {profileLabel(p)}
                </Button>
              </li>
            ))}
          </ul>
        </div>
      )}
      <ErrorMessage message={error} />
      <Button type="button" variant="ghost" size="sm" onClick={() => void logout()}>
        Sign out
      </Button>
    </div>
  );
}
