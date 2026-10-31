'use client';

import { useState } from 'react';
import { Button } from '@moduleforge/core-gui';
import { Building2, Lock, User } from 'lucide-react';
import { ErrorMessage } from './error-message';
import { ProfileRemedy } from './profile-remedy';
import { useAuth } from '../lib/auth-context';
import { ApiRequestError, type ProfileView } from '../lib/api';
import { describeLockReason } from '../lib/profile-lock';

export interface ProfileSwitcherProps {
  /** Called after a successful switch, with the profile now acting. */
  onSwitched?: (profileUuid: string) => void;
}

/** The label for a profile: personal profiles by name, party profiles by party. */
export function profileLabel(profile: ProfileView): string {
  return profile.kind === 'party' && profile.party
    ? profile.party.display_name
    : profile.display_name;
}

/**
 * Lists the principal's profiles, marks the active one, switches to an
 * available one, and shows locked ones with their reason and remedy instead of
 * hiding them. A switch replaces only this tab's token.
 */
export function ProfileSwitcher({ onSwitched }: ProfileSwitcherProps) {
  const { profiles, profile: active, switchProfile } = useAuth();
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  if (profiles.length === 0) return null;

  async function handleSwitch(uuid: string) {
    setPending(uuid);
    setError(null);
    try {
      await switchProfile(uuid);
      onSwitched?.(uuid);
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
    <section aria-label="Profiles" className="space-y-1">
      <ul className="space-y-1">
        {profiles.map((p) => {
          const isActive = active?.uuid === p.uuid;
          const Icon = p.kind === 'party' ? Building2 : User;
          return (
            <li key={p.uuid} className="rounded-md px-2 py-1.5 text-sm">
              <div className="flex items-center gap-2">
                {p.available ? (
                  <Icon className="size-4 shrink-0" aria-hidden="true" />
                ) : (
                  <Lock className="size-4 shrink-0" aria-hidden="true" />
                )}
                <span className="min-w-0 flex-1 truncate">
                  {profileLabel(p)}
                  <span className="ml-1 text-xs text-muted-foreground">
                    {p.kind === 'party' ? 'Party' : 'Personal'}
                  </span>
                </span>
                {isActive && (
                  <span
                    aria-current="true"
                    className="text-xs font-medium text-sidebar-accent-foreground"
                  >
                    Active
                  </span>
                )}
                {/* Switching to the active profile is a no-op; locked ones cannot be entered. */}
                {p.available && !isActive && (
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={pending !== null}
                    onClick={() => void handleSwitch(p.uuid)}
                    aria-label={`Switch to ${profileLabel(p)}`}
                  >
                    Switch
                  </Button>
                )}
              </div>
              {/* Locked profiles stay visible so the user can see why and what fixes it. */}
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
          );
        })}
      </ul>
      <ErrorMessage message={error} />
    </section>
  );
}
