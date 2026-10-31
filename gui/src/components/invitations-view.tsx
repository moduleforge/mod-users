'use client';

import { useCallback, useEffect, useState } from 'react';
import { Button } from '@moduleforge/core-gui';
import { ErrorMessage } from './error-message';
import { profileLabel } from './profile-switcher';
import { useAuth, useStepUpHandler } from '../lib/auth-context';
import {
  api,
  ApiActionRequiredError,
  ApiRequestError,
  STEP_UP_REQUIRED_ACTION,
  type ProfileView,
  type StepUpHandler,
} from '../lib/api';

export interface InvitationsViewProps {
  /**
   * The step-up prompt, registered with the auth context while this view is
   * mounted (see `useStepUpHandler`). When a handler is registered, a request
   * answering `409 users.step_up_required` is retried after the prompt
   * resolves. Memoize it. Omit if the host registered one app-wide.
   */
  stepUpHandler?: StepUpHandler;
  /**
   * Called when an action still ended in `users.step_up_required` (no handler,
   * or the user cancelled), so the host can route the user to its own prompt.
   */
  onStepUpRequired?: (error: ApiActionRequiredError) => void;
}

type Action = 'accept' | 'decline' | 'leave';

const ACTION_FAILURE: Record<Action, string> = {
  accept: 'Could not accept the invitation.',
  decline: 'Could not decline the invitation.',
  leave: 'Could not leave the party.',
};

/**
 * The caller's party-profile invitations (accept / decline) and the active
 * party profiles they can leave, from `GET /v1/me/profiles`. Refreshes the
 * list and the auth context's profile list after every action.
 */
export function InvitationsView({
  stepUpHandler,
  onStepUpRequired,
}: InvitationsViewProps) {
  const { refreshUser } = useAuth();
  const [profiles, setProfiles] = useState<ProfileView[] | null>(null);
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // `null` handler registers nothing, so the hook is safe to call unconditionally.
  useStepUpHandler(stepUpHandler ?? null);

  const load = useCallback(async () => {
    try {
      setProfiles((await api.me.profiles.list()).profiles);
    } catch (err) {
      setError(
        err instanceof ApiRequestError
          ? err.message
          : 'Could not load your profiles.',
      );
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function run(action: Action, profile: ProfileView) {
    setPending(profile.uuid);
    setError(null);
    try {
      if (action === 'accept') await api.me.profiles.accept(profile.uuid);
      else if (action === 'decline') await api.me.profiles.decline(profile.uuid);
      else await api.me.profiles.leave(profile.uuid);
      // Both lists changed: this view's, and the switcher's in the auth context.
      await Promise.all([load(), refreshUser()]);
    } catch (err) {
      if (
        err instanceof ApiActionRequiredError &&
        err.code === STEP_UP_REQUIRED_ACTION
      ) {
        // Reached only when no handler handled it: tell the user and let the
        // host route them to its prompt.
        setError('Confirm your identity (step-up) and try again.');
        onStepUpRequired?.(err);
      } else {
        setError(
          err instanceof ApiRequestError ? err.message : ACTION_FAILURE[action],
        );
      }
    } finally {
      setPending(null);
    }
  }

  if (profiles === null) {
    return <p className="p-6 text-sm text-muted-foreground">Loading...</p>;
  }

  const invited = profiles.filter((p) => p.status === 'invited');
  // Only party profiles can be left; a personal profile is never a membership.
  const leavable = profiles.filter(
    (p) => p.kind === 'party' && p.status === 'active',
  );

  return (
    <section aria-label="Invitations" className="mx-auto max-w-lg space-y-6 p-6">
      <div className="space-y-2">
        <h1 className="text-xl font-semibold">Invitations</h1>
        {invited.length === 0 ? (
          <p className="text-sm text-muted-foreground">No pending invitations.</p>
        ) : (
          <ul className="space-y-2">
            {invited.map((p) => (
              <li
                key={p.uuid}
                className="flex items-center justify-between gap-2 rounded-md border p-3"
              >
                <span className="text-sm">{profileLabel(p)}</span>
                <span className="flex gap-2">
                  <Button
                    type="button"
                    size="sm"
                    disabled={pending !== null}
                    onClick={() => void run('accept', p)}
                    aria-label={`Accept invitation to ${profileLabel(p)}`}
                  >
                    Accept
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={pending !== null}
                    onClick={() => void run('decline', p)}
                    aria-label={`Decline invitation to ${profileLabel(p)}`}
                  >
                    Decline
                  </Button>
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>

      {leavable.length > 0 && (
        <div className="space-y-2">
          <h2 className="text-base font-semibold">Your parties</h2>
          <ul className="space-y-2">
            {leavable.map((p) => (
              <li
                key={p.uuid}
                className="flex items-center justify-between gap-2 rounded-md border p-3"
              >
                <span className="text-sm">{profileLabel(p)}</span>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={pending !== null}
                  onClick={() => void run('leave', p)}
                  aria-label={`Leave ${profileLabel(p)}`}
                >
                  Leave
                </Button>
              </li>
            ))}
          </ul>
        </div>
      )}
      <ErrorMessage message={error} />
    </section>
  );
}
