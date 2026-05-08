'use client';

import { useEffect } from 'react';
import { useAuth } from '../lib/auth-context';

interface RequireAuthProps {
  children: React.ReactNode;
  requireAdmin?: boolean;
  /**
   * Called when the user is not authenticated. The consumer injects
   * framework-specific navigation. Defaults to a no-op so the component
   * is usable in stories/tests without a router.
   */
  onUnauthenticated?: () => void;
  /**
   * Called when the user is authenticated but lacks admin access and
   * `requireAdmin` is true. Defaults to a no-op.
   */
  onUnauthorized?: () => void;
}

export function RequireAuth({
  children,
  requireAdmin = false,
  onUnauthenticated,
  onUnauthorized,
}: RequireAuthProps) {
  const { user, isLoading, isAdmin } = useAuth();

  useEffect(() => {
    if (isLoading) return;
    if (!user) {
      onUnauthenticated?.();
      return;
    }
    if (requireAdmin && !isAdmin) {
      onUnauthorized?.();
    }
  }, [user, isLoading, isAdmin, requireAdmin, onUnauthenticated, onUnauthorized]);

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-muted-foreground text-sm">Loading...</p>
      </div>
    );
  }

  if (!user) return null;
  if (requireAdmin && !isAdmin) return null;

  return <>{children}</>;
}
