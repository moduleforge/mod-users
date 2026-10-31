'use client';

import React from 'react';
import { AuthProvider } from '../lib/auth-context';
import { SidebarNav, type SidebarNavProps } from './sidebar-nav';
import { ImpersonationBanner } from './impersonation-banner';
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
   * Called when OIDC is confirmed but the deployment is not yet bootstrapped
   * and the user is not already on `/bootstrap`. See `OidcSetupGate`.
   */
  onNavigateToBootstrap?: () => void;
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
  onNavigateToBootstrap,
  onNavigate,
  LinkComponent,
}: ClientLayoutProps) {
  return (
    <OidcSetupGate
      currentPath={currentPath}
      onNavigateToConfig={onNavigateToConfig}
      onNavigateToBootstrap={onNavigateToBootstrap}
    >
      {(state) =>
        state === 'setup' || state === 'bootstrap' ? (
          // On a first-run setup page (OIDC config or bootstrap), render
          // children without an AuthProvider. These pages are intentionally
          // unauthenticated: the operator has no session yet, and the API
          // would answer `/v1/me` with 503 while OIDC is unconfirmed.
          <main className="h-screen overflow-y-auto">{children}</main>
        ) : (
          <AuthProvider onNavigate={onNavigate}>
            <div className="flex h-screen flex-col overflow-hidden">
              {/* Above the sidebar so it stays visible on every page; renders
                  nothing unless the tab's session is an impersonation. */}
              <ImpersonationBanner />
              <div className="flex min-h-0 flex-1 overflow-hidden">
                <SidebarNav currentPath={currentPath} LinkComponent={LinkComponent} />
                <main className="flex-1 overflow-y-auto">{children}</main>
              </div>
            </div>
          </AuthProvider>
        )
      }
    </OidcSetupGate>
  );
}
