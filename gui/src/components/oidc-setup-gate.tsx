'use client';

import React, { useEffect, useState } from 'react';
import { fetchOIDCStatus } from '../lib/oidc-config';
import { USERS_GUI_ROUTES } from '../lib/routes';

/**
 * Tri-state for the OIDC onboarding gate:
 * - `loading`: initial fetch in flight; render a minimal placeholder.
 * - `ok`:      OIDC is confirmed; proceed with the normal AuthProvider flow.
 * - `needs-setup`: API reports unconfirmed (or was unreachable); route to
 *   the OIDC config page unless the user is already there.
 */
type OIDCReady = 'loading' | 'ok' | 'needs-setup';

function LoadingScreen() {
  return (
    <div className="flex h-screen items-center justify-center text-sm text-muted-foreground">
      Loading...
    </div>
  );
}

export interface OidcSetupGateProps {
  /**
   * The current pathname. Inject from the router (e.g. Next.js
   * `usePathname()`, React Router `useLocation().pathname`).
   */
  currentPath: string;
  /**
   * Called when OIDC is unconfirmed and the user is not already on
   * `USERS_GUI_ROUTES.oidcConfig`. The consumer performs the actual
   * navigation. Defaults to a no-op so the gate is usable in stories/tests.
   */
  onNavigateToConfig?: () => void;
  /**
   * Rendered once the gate resolves:
   * - `'ready'`: OIDC is confirmed; render the app normally (typically inside
   *   an `AuthProvider`).
   * - `'setup'`: OIDC is unconfirmed and `currentPath` is the config page —
   *   the one route allowed through. Render it WITHOUT an `AuthProvider`
   *   (`/v1/self` would 503 while OIDC is unconfirmed).
   */
  children: (state: 'ready' | 'setup') => React.ReactNode;
}

/**
 * Enforces OIDC onboarding: probes `/v1/oidc-config/status` once on mount and,
 * while unconfirmed, redirects every route except the config page there.
 * Usable by apps that do not use `ClientLayout`'s admin chrome.
 */
export function OidcSetupGate({
  currentPath,
  onNavigateToConfig,
  children,
}: OidcSetupGateProps) {
  const [oidcReady, setOidcReady] = useState<OIDCReady>('loading');

  // Probe OIDC status exactly once on mount. Any failure (network error,
  // 5xx, invalid JSON) is treated as "needs setup" so an operator with a
  // broken API can still reach the config page to fix things.
  useEffect(() => {
    let cancelled = false;
    fetchOIDCStatus()
      .then((status) => {
        if (cancelled) return;
        setOidcReady(status.confirmed ? 'ok' : 'needs-setup');
      })
      .catch(() => {
        if (cancelled) return;
        setOidcReady('needs-setup');
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Redirect to the config page whenever the API says we're unconfirmed and
  // we're not already there. Runs on status change or route change.
  useEffect(() => {
    if (oidcReady === 'needs-setup' && currentPath !== USERS_GUI_ROUTES.oidcConfig) {
      onNavigateToConfig?.();
    }
  }, [oidcReady, currentPath, onNavigateToConfig]);

  if (oidcReady === 'loading') {
    return <LoadingScreen />;
  }

  // When unconfirmed, the only route we allow through is the setup page
  // itself. Everything else renders the loading screen while the redirect
  // above dispatches — this guarantees the caller's AuthProvider never mounts
  // and therefore never fires `/v1/self` (which would 503 under unconfirmed).
  if (oidcReady === 'needs-setup') {
    if (currentPath !== USERS_GUI_ROUTES.oidcConfig) {
      return <LoadingScreen />;
    }
    return <>{children('setup')}</>;
  }

  return <>{children('ready')}</>;
}
