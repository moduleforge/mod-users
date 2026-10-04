'use client';

import React from 'react';
import { AuthProvider } from '../lib/auth-context';
import { SidebarNav, type SidebarNavProps } from './sidebar-nav';
import { OidcSetupGate } from './oidc-setup-gate';

export interface ClientLayoutProps {
  children: React.ReactNode;
  /**
   * The current pathname. Inject from the router (e.g. Next.js
   * `usePathname()`, React Router `useLocation().pathname`).
   */
  currentPath: string;
  /**
   * Called when OIDC is unconfirmed and the user is not already on
   * `/oidc-config`. The consumer (Next.js app, etc.) performs the actual
   * navigation. Defaults to a no-op so the layout is usable in stories/tests.
   */
  onNavigateToConfig?: () => void;
  /**
   * Called by `AuthProvider` when it needs to navigate (e.g., after logout).
   * The consumer injects framework-specific navigation.
   */
  onNavigate?: (path: string) => void;
  /**
   * Link component passed through to `SidebarNav`. See `SidebarNavProps`.
   */
  LinkComponent: SidebarNavProps['LinkComponent'];
}

export function ClientLayout({
  children,
  currentPath,
  onNavigateToConfig,
  onNavigate,
  LinkComponent,
}: ClientLayoutProps) {
  return (
    <OidcSetupGate currentPath={currentPath} onNavigateToConfig={onNavigateToConfig}>
      {(state) =>
        state === 'setup' ? (
          // On the setup page itself, render children without an AuthProvider.
          // The page is intentionally unauthenticated; no consumer on that page
          // should be calling `useAuth()`.
          <main className="h-screen overflow-y-auto">{children}</main>
        ) : (
          <AuthProvider onNavigate={onNavigate}>
            <div className="flex h-screen overflow-hidden">
              <SidebarNav currentPath={currentPath} LinkComponent={LinkComponent} />
              <main className="flex-1 overflow-y-auto">{children}</main>
            </div>
          </AuthProvider>
        )
      }
    </OidcSetupGate>
  );
}
