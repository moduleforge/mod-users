'use client';

import React, { useEffect, useState } from 'react';
import { api } from '../lib/api';
import { fetchOIDCStatus } from '../lib/oidc-config';
import { USERS_GUI_ROUTES } from '../lib/routes';

/**
 * What the two first-run probes say. The setup token is active (decision U9)
 * while the operator is not bootstrapped OR OIDC is unconfirmed; the gate
 * keeps the operator inside the setup pages exactly as long as that holds.
 */
interface SetupProbe {
  bootstrapped: boolean;
  oidcConfirmed: boolean;
}

/**
 * Gate state:
 * - `loading`: the probes are in flight; render a minimal placeholder.
 * - `resolved`: both probes answered.
 * - `unreachable`: a probe failed (network error, 5xx, invalid JSON).
 */
type GateState =
  | { kind: 'loading' }
  | { kind: 'resolved'; probe: SetupProbe }
  | { kind: 'unreachable' };

/** What the gate does with the current path, given the probe. */
type GateRoute =
  | { kind: 'ready' }
  | { kind: 'render'; state: 'setup' | 'bootstrap' }
  | { kind: 'redirect'; to: 'config' | 'bootstrap' };

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
   * Called when first-run setup is pending and the user is on a route the
   * gate does not allow, and the OIDC configuration page is the next step
   * (OIDC unconfirmed, or the API unreachable). The consumer performs the
   * actual navigation. Defaults to a no-op so the gate is usable in
   * stories/tests.
   */
  onNavigateToConfig?: () => void;
  /**
   * Called when OIDC is confirmed but the deployment is not bootstrapped, so
   * the bootstrap page (`USERS_GUI_ROUTES.bootstrap`) is the only next step.
   * Defaults to a no-op.
   */
  onNavigateToBootstrap?: () => void;
  /**
   * Rendered once the gate resolves:
   * - `'ready'`: setup is done (bootstrapped and OIDC confirmed); render the
   *   app normally (typically inside an `AuthProvider`).
   * - `'setup'`: setup is pending and `currentPath` is the OIDC config page.
   *   Render it WITHOUT an `AuthProvider` (`/v1/me` would 503 while OIDC is
   *   unconfirmed).
   * - `'bootstrap'`: setup is pending and `currentPath` is the bootstrap page.
   *   Render it WITHOUT an `AuthProvider`: there is no operator to sign in
   *   as yet. Added alongside `'setup'`; a consumer that only distinguishes
   *   `'setup'` from everything else must treat `'bootstrap'` like `'setup'`.
   */
  children: (state: 'ready' | 'setup' | 'bootstrap') => React.ReactNode;
}

/**
 * Decides what to do with `currentPath` for a resolved probe.
 *
 * First-run order (either order works, because the setup token stays active
 * until both steps are done):
 *   - OIDC unconfirmed, not bootstrapped: both pages are allowed; any other
 *     route is sent to the OIDC config page.
 *   - OIDC unconfirmed, bootstrapped: only the OIDC config page is allowed.
 *     Token mode works only in this state; with the operator in place, an
 *     operator admin can also configure from a session.
 *   - OIDC confirmed, not bootstrapped: only the bootstrap page is allowed.
 *     The OIDC routes no longer accept the setup token once OIDC is confirmed,
 *     and there is no operator admin session to use instead, so the config
 *     page would be a dead end.
 *   - Both done: `ready`; every route renders normally.
 */
function routeFor(probe: SetupProbe, currentPath: string): GateRoute {
  const { bootstrapped, oidcConfirmed } = probe;
  if (bootstrapped && oidcConfirmed) {
    // Setup is over: nothing to gate. Implies the setup token is cleared and
    // the config/bootstrap routes are ordinary pages again.
    return { kind: 'ready' };
  }
  const onConfig = currentPath === USERS_GUI_ROUTES.oidcConfig;
  const onBootstrap = currentPath === USERS_GUI_ROUTES.bootstrap;
  if (!oidcConfirmed) {
    if (onConfig) return { kind: 'render', state: 'setup' };
    // Bootstrap may precede OIDC confirmation, but only while it is pending:
    // a bootstrapped deployment has nothing left to do on that page.
    if (onBootstrap && !bootstrapped) return { kind: 'render', state: 'bootstrap' };
    // OIDC first is the default order: its token mode is the one that
    // disappears once OIDC is confirmed without an operator admin.
    return { kind: 'redirect', to: 'config' };
  }
  // OIDC confirmed and bootstrap pending (the both-done case returned above).
  if (onBootstrap) return { kind: 'render', state: 'bootstrap' };
  return { kind: 'redirect', to: 'bootstrap' };
}

/**
 * Enforces first-run setup: probes `/v1/bootstrap/status` and
 * `/v1/oidc-config/status` once on mount and, while the setup token is
 * active, redirects every route outside the setup pages. Usable by apps that
 * do not use `ClientLayout`'s admin chrome.
 */
export function OidcSetupGate({
  currentPath,
  onNavigateToConfig,
  onNavigateToBootstrap,
  children,
}: OidcSetupGateProps) {
  const [gate, setGate] = useState<GateState>({ kind: 'loading' });

  // Probe both statuses exactly once on mount. Any failure of either (network
  // error, 5xx, invalid JSON) is treated as "setup needed" so an operator with
  // a broken API can still reach the config page to fix things.
  useEffect(() => {
    let cancelled = false;
    Promise.all([api.bootstrap.status(), fetchOIDCStatus()])
      .then(([boot, oidc]) => {
        if (cancelled) return;
        setGate({
          kind: 'resolved',
          probe: { bootstrapped: boot.bootstrapped, oidcConfirmed: oidc.confirmed },
        });
      })
      .catch(() => {
        if (cancelled) return;
        setGate({ kind: 'unreachable' });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // An unreachable API pins the operator to the OIDC config page (the page
  // that shows the status error). It implies "bootstrapped, unconfirmed" for
  // routing: only the config page is allowed and nothing else is offered.
  const route: GateRoute | null =
    gate.kind === 'loading'
      ? null
      : routeFor(
          gate.kind === 'resolved'
            ? gate.probe
            : { bootstrapped: true, oidcConfirmed: false },
          currentPath,
        );

  // Redirect whenever the probes say we're mid-setup and we're on a route the
  // gate does not allow. Runs on status change or route change.
  const redirectTo = route?.kind === 'redirect' ? route.to : null;
  useEffect(() => {
    if (redirectTo === 'config') onNavigateToConfig?.();
    else if (redirectTo === 'bootstrap') onNavigateToBootstrap?.();
  }, [redirectTo, currentPath, onNavigateToConfig, onNavigateToBootstrap]);

  if (route === null) {
    return <LoadingScreen />;
  }

  // Off the allowed pages the redirect above is dispatching; show the loading
  // screen meanwhile so the caller's AuthProvider never mounts and never
  // fires `/v1/me` (which would 503 under unconfirmed).
  if (route.kind === 'redirect') {
    return <LoadingScreen />;
  }

  if (route.kind === 'render') {
    return <>{children(route.state)}</>;
  }

  return <>{children('ready')}</>;
}
