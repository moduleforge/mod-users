'use client';

import { useEffect } from 'react';
import { useAuth } from '../lib/auth-context';
import { ProfileLockNotice } from './profile-lock-notice';

interface RequireAuthProps {
  children: React.ReactNode;
  /**
   * Called when the user is not authenticated. The consumer injects
   * framework-specific navigation. Defaults to a no-op so the component
   * is usable in stories/tests without a router.
   */
  onUnauthenticated?: () => void;
  /**
   * Called when the sign-in found no landing profile (`selectionRequired`);
   * navigate to `USERS_GUI_ROUTES.selectProfile`. Defaults to a no-op.
   */
  onSelectionRequired?: () => void;
}

/**
 * Gates children on authentication only. There is no admin gate: administrator
 * authority no longer exists as a client-side flag, and the server enforces it
 * by answering 403 on admin routes, which the page handles.
 *
 * Order matters: selection and lock are checked before the user record, since
 * neither state has a loaded user (no usable access token).
 */
export function RequireAuth({
  children,
  onUnauthenticated,
  onSelectionRequired,
}: RequireAuthProps) {
  const { user, isLoading, selectionRequired, profileLock } = useAuth();

  useEffect(() => {
    if (isLoading) return;
    // Signed in without a landing profile: the tab must choose one first.
    if (selectionRequired) {
      onSelectionRequired?.();
      return;
    }
    // A lock keeps the tokens (so the user can recover), so it is not an
    // unauthenticated state and must not redirect to sign-in.
    if (profileLock) return;
    if (!user) {
      onUnauthenticated?.();
    }
  }, [
    user,
    isLoading,
    selectionRequired,
    profileLock,
    onUnauthenticated,
    onSelectionRequired,
  ]);

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-muted-foreground text-sm">Loading...</p>
      </div>
    );
  }

  if (selectionRequired) return null;
  // Show the reason and remedy instead of the protected content.
  if (profileLock) return <ProfileLockNotice />;
  if (!user) return null;

  return <>{children}</>;
}
