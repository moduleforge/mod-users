'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { CheckCircle2, Pencil, Plus } from 'lucide-react';
import { ApiRequestError } from '@/lib/api';
import { useOptionalAuth } from '@/lib/auth-context';
import {
  fetchOIDCSaved,
  fetchOIDCStatus,
  postOIDCConfirm,
  type OIDCProviderStatus,
  type OIDCStatus,
} from '@/lib/oidc-config';
import type { OIDCProviderAuth } from '@/lib/oidc-provider';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { ErrorMessage } from '@/components/error-message';
import { ProviderEditModal } from './provider-edit-modal';
import { ProviderAddModal } from './provider-add-modal';

const REDIRECT_DELAY_MS = 2000;

interface TestResult {
  provider: string;
  ok: boolean;
  email?: string;
  sub?: string;
  issuer?: string;
  error?: string;
}

/**
 * Read the test-configuration result out of the URL query string and
 * strip it so a refresh doesn't re-surface the banner. Returns null
 * when no test params are present.
 */
function extractTestResultFromLocation(): TestResult | null {
  if (typeof window === 'undefined') return null;
  const qp = new URLSearchParams(window.location.search);
  const result = qp.get('test_result');
  if (result !== 'ok' && result !== 'fail') return null;
  const provider = qp.get('test_provider') ?? '';
  const out: TestResult = { provider, ok: result === 'ok' };
  if (result === 'ok') {
    out.email = qp.get('test_email') ?? undefined;
    out.sub = qp.get('test_sub') ?? undefined;
    out.issuer = qp.get('test_issuer') ?? undefined;
  } else {
    out.error = qp.get('test_error') ?? undefined;
  }
  // Clean the URL so refreshes don't re-surface the banner.
  window.history.replaceState(null, '', window.location.pathname);
  return out;
}

/**
 * `/oidc-config` — dual-mode setup + reconfigure page.
 *
 * Always renders the provider list + toggles + per-provider OK/Failed
 * status badges. Two authorization paths:
 *   - **Token mode**: no admin session. User pastes the setup token from
 *     the server logs. On successful confirm → hard-redirect to /auth/login.
 *   - **Admin mode**: AuthProvider is mounted and the user is an admin.
 *     Token input is hidden; submit sends Authorization: Bearer <jwt>.
 *     Success stays on the page and refreshes status — admin can tweak
 *     and re-confirm without another round trip.
 *
 * Partial-failure / strict confirmation: if the submitted config still
 * has a broken provider, the API returns 200 with `confirmed: false`. We
 * show that inline and do NOT redirect, giving the admin a chance to
 * disable the failing provider explicitly.
 */
export default function OIDCConfigPage() {
  const auth = useOptionalAuth();
  const isAdminMode = auth?.isAdmin === true && auth.token !== null;

  const [status, setStatus] = useState<OIDCStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);

  const [setupToken, setSetupToken] = useState('');
  const [toggles, setToggles] = useState<Record<string, boolean>>({});
  // baselineToggles snapshots the persisted enabled state. Dirty
  // detection compares `toggles` to this; Save is disabled when they
  // match. Updated on a fresh /status fetch and on successful confirm
  // — but NOT on a failed confirm, so the admin can retry without
  // losing unsaved toggles.
  const [baselineToggles, setBaselineToggles] = useState<
    Record<string, boolean>
  >({});
  const [formError, setFormError] = useState<string | null>(null);
  const [revertMessage, setRevertMessage] = useState<string | null>(null);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isReverting, setIsReverting] = useState(false);
  const [isTokenSuccess, setIsTokenSuccess] = useState(false);
  // Per-provider edit modal + add modal state. We keep them at the page
  // level so a successful save can trigger the shared status refresh.
  const [editingProviderId, setEditingProviderId] = useState<string | null>(
    null,
  );
  const [isAddOpen, setIsAddOpen] = useState(false);
  // Test-configuration result banner (9.18). Populated from query
  // params the API appended when redirecting back from a /start?mode=test
  // round-trip. Parsed from window.location on mount to avoid pulling
  // useSearchParams (which requires Suspense to work under SSR).
  const [testResult, setTestResult] = useState<TestResult | null>(null);

  function applyStatus(s: OIDCStatus) {
    setStatus(s);
    const next = Object.fromEntries(
      s.providers.map((p) => [p.id, p.enabled]),
    );
    setToggles(next);
    setBaselineToggles(next);
  }

  // Dirty iff any toggle differs from the snapshot of what the server
  // currently persists. Drives the Save button's disabled state;
  // matches the modal's baselineForm pattern so both screens feel the
  // same.
  const isDirty = useMemo(() => {
    const keys = new Set([
      ...Object.keys(toggles),
      ...Object.keys(baselineToggles),
    ]);
    for (const k of keys) {
      if ((toggles[k] ?? false) !== (baselineToggles[k] ?? false)) return true;
    }
    return false;
  }, [toggles, baselineToggles]);

  // Build the auth object the per-provider helpers expect. Admin mode
  // wins when both paths are technically available (admin can still paste
  // a setup token but we prefer the session). Null means "no auth" and
  // must disable the modal triggers.
  const providerAuth: OIDCProviderAuth | null = useMemo(() => {
    if (isAdminMode && auth?.token) return { adminBearer: auth.token };
    const trimmed = setupToken.trim();
    if (trimmed !== '') return { setupToken: trimmed };
    return null;
  }, [isAdminMode, auth?.token, setupToken]);

  // Reload status after a modal save so the OK/Failed badges and the
  // current enabled set match the newly-persisted DB state.
  const refreshStatus = useCallback(async () => {
    try {
      const next = await fetchOIDCStatus();
      applyStatus(next);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setFormError(err.message);
      } else {
        setFormError('Could not refresh status.');
      }
    }
  }, []);

  // Parse any ?test_result=… query params on first mount. This runs
  // once per page load; the extract helper strips the params from the
  // URL after reading so a refresh shows a clean page.
  useEffect(() => {
    const tr = extractTestResultFromLocation();
    if (tr) setTestResult(tr);
  }, []);

  // Initial status fetch.
  useEffect(() => {
    let cancelled = false;
    fetchOIDCStatus()
      .then((s) => {
        if (cancelled) return;
        applyStatus(s);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiRequestError) {
          setStatusError(err.message);
        } else {
          setStatusError('Could not load configuration status.');
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  function handleToggle(providerId: string, next: boolean) {
    setToggles((prev) => ({ ...prev, [providerId]: next }));
    setRevertMessage(null);
    setSuccessMessage(null);
  }

  async function handleRevert() {
    setFormError(null);
    setRevertMessage(null);
    setSuccessMessage(null);
    setIsReverting(true);
    try {
      const saved = await fetchOIDCSaved();
      const enabled = saved.enabled_providers;
      if (!enabled || Object.keys(enabled).length === 0) {
        setRevertMessage('No saved config to revert to.');
        return;
      }
      setToggles((prev) => {
        const next: Record<string, boolean> = {};
        for (const id of Object.keys(prev)) {
          next[id] = enabled[id] === true;
        }
        return next;
      });
      setRevertMessage('Reverted to last saved configuration.');
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setFormError(err.message);
      } else {
        setFormError('Could not load saved configuration.');
      }
    } finally {
      setIsReverting(false);
    }
  }

  async function handleConfirm(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    setRevertMessage(null);
    setSuccessMessage(null);
    setIsSubmitting(true);

    const configuredIds = new Set(
      (status?.providers ?? []).filter((p) => p.configured).map((p) => p.id),
    );
    const enabledProviders = Object.entries(toggles)
      .filter(([id, on]) => on && configuredIds.has(id))
      .map(([id]) => id);
    const optOut = enabledProviders.length === 0;

    try {
      const updated = isAdminMode
        ? await postOIDCConfirm({
            adminBearer: auth!.token!,
            enabled_providers: enabledProviders,
            opt_out: optOut,
          })
        : await postOIDCConfirm({
            setup_token: setupToken.trim(),
            enabled_providers: enabledProviders,
            opt_out: optOut,
          });

      // Strict confirmation (Phase 9.10a): the API may return 200 with
      // confirmed=false if an enabled provider still fails init. Don't
      // redirect; let the admin see the error list and try again.
      if (!updated.confirmed) {
        applyStatus(updated);
        const failing = updated.providers.filter(
          (p) => p.enabled && !p.init_ok,
        );
        if (failing.length > 0) {
          const details = failing
            .map((p) => `${p.display_name}: ${p.error ?? 'init failed'}`)
            .join('; ');
          setFormError(
            `Configuration saved but providers still fail to initialize: ${details}. Disable the failing providers or fix their env settings.`,
          );
        } else {
          setFormError(
            'Configuration saved but the system is not in a confirmed state. Check server logs.',
          );
        }
        return;
      }

      if (isAdminMode) {
        // Admin session stays valid; keep them on the page with an
        // inline success + refreshed status so they can see the new
        // badges and re-toggle if needed.
        applyStatus(updated);
        setSuccessMessage('Configuration saved.');
      } else {
        // Token flow: clear single-use token and hand off to login via a
        // hard navigation so ClientLayout remounts and re-fetches status.
        setSetupToken('');
        setIsTokenSuccess(true);
        setTimeout(() => {
          window.location.assign('/auth/login');
        }, REDIRECT_DELAY_MS);
      }
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setFormError(err.message);
      } else {
        console.error('[oidc-config]', err);
        setFormError('Something went wrong. Check the browser console.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  // ─── Rendering branches ────────────────────────────────────────────────

  if (statusError) {
    return (
      <PageShell>
        <Card className="w-full max-w-lg">
          <CardHeader>
            <CardTitle>OIDC configuration</CardTitle>
            <CardDescription>Could not load status.</CardDescription>
          </CardHeader>
          <CardContent>
            <ErrorMessage message={statusError} />
          </CardContent>
        </Card>
      </PageShell>
    );
  }

  if (status === null) {
    return (
      <PageShell>
        <p className="text-sm text-muted-foreground">Loading status...</p>
      </PageShell>
    );
  }

  if (isTokenSuccess) {
    return (
      <PageShell>
        <Card className="w-full max-w-lg">
          <CardHeader>
            <div className="flex items-center gap-2">
              <CheckCircle2 className="size-5 text-primary" />
              <CardTitle>Configuration saved</CardTitle>
            </div>
            <CardDescription>
              Redirecting to the login page...
            </CardDescription>
          </CardHeader>
        </Card>
      </PageShell>
    );
  }

  return (
    <PageShell>
      <Card className="w-full max-w-lg">
        <CardHeader>
          <CardTitle>OIDC configuration</CardTitle>
          <CardDescription>
            {isAdminMode
              ? 'Toggle providers and confirm. Changes take effect immediately.'
              : 'Paste the setup token from the server logs and choose which providers to enable.'}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleConfirm} className="flex flex-col gap-5">
            {testResult && (
              <TestResultBanner
                result={testResult}
                onDismiss={() => setTestResult(null)}
              />
            )}
            <ErrorMessage message={formError} />
            {successMessage && (
              <div className="flex items-center gap-2 rounded-md border border-primary/20 bg-primary/5 px-3 py-2 text-sm">
                <CheckCircle2 className="size-4 text-primary" />
                <span>{successMessage}</span>
              </div>
            )}

            {!isAdminMode && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="setup-token">Setup token</Label>
                <Input
                  id="setup-token"
                  type="text"
                  required
                  autoComplete="off"
                  spellCheck={false}
                  value={setupToken}
                  onChange={(e) => setSetupToken(e.target.value)}
                  placeholder="hex token from server logs"
                />
              </div>
            )}

            <ProviderList
              providers={status.providers}
              toggles={toggles}
              onToggle={handleToggle}
              onEdit={
                providerAuth
                  ? (id) => setEditingProviderId(id)
                  : undefined
              }
            />

            <div className="flex items-center justify-center">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={!providerAuth}
                onClick={() => setIsAddOpen(true)}
              >
                <Plus /> Add provider
              </Button>
            </div>

            {revertMessage && (
              <p className="text-xs text-muted-foreground">{revertMessage}</p>
            )}

            <div className="flex items-center justify-between gap-2 pt-1">
              <Button
                type="button"
                variant="outline"
                onClick={handleRevert}
                disabled={isReverting || isSubmitting}
              >
                {isReverting ? 'Reverting...' : 'Revert'}
              </Button>
              <Button
                type="submit"
                // Save disabled when nothing changed (matches the
                // modal's dirty-detection UX). Token-mode admins still
                // need a token pasted. A failed save leaves Save
                // enabled because baselineToggles only advances on
                // success.
                disabled={
                  isSubmitting ||
                  !isDirty ||
                  (!isAdminMode && setupToken.trim() === '')
                }
              >
                {isSubmitting ? 'Saving...' : 'Save'}
              </Button>
            </div>
          </form>
        </CardContent>
        <CardFooter>
          <p className="text-xs text-muted-foreground">
            Turning every provider off records an opt-out; only local auth
            will be available.
          </p>
        </CardFooter>
      </Card>

      <ProviderEditModal
        providerId={editingProviderId}
        auth={editingProviderId ? providerAuth : null}
        onClose={() => setEditingProviderId(null)}
        onSaved={refreshStatus}
      />

      <ProviderAddModal
        open={isAddOpen}
        auth={isAddOpen ? providerAuth : null}
        onClose={() => setIsAddOpen(false)}
        onCreated={refreshStatus}
      />
    </PageShell>
  );
}

function PageShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      {children}
    </div>
  );
}

interface ProviderListProps {
  providers: OIDCProviderStatus[];
  toggles: Record<string, boolean>;
  onToggle: (id: string, next: boolean) => void;
  /**
   * When provided, each row renders an Edit pencil that invokes this
   * callback with the provider id. Omit to hide the affordance (e.g. when
   * no auth path is available yet).
   */
  onEdit?: (id: string) => void;
}

function ProviderList({
  providers,
  toggles,
  onToggle,
  onEdit,
}: ProviderListProps) {
  if (providers.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No providers registered. Set provider env vars and restart the API.
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <Label>Providers</Label>
      <div className="flex flex-col gap-2 rounded-md border">
        {providers.map((p) => (
          <ProviderRow
            key={p.id}
            provider={p}
            checked={toggles[p.id] ?? false}
            onToggle={(next) => onToggle(p.id, next)}
            onEdit={onEdit ? () => onEdit(p.id) : undefined}
          />
        ))}
      </div>
    </div>
  );
}

interface ProviderRowProps {
  provider: OIDCProviderStatus;
  checked: boolean;
  onToggle: (next: boolean) => void;
  onEdit?: () => void;
}

function ProviderRow({
  provider,
  checked,
  onToggle,
  onEdit,
}: ProviderRowProps) {
  const disabled = !provider.configured;
  const switchId = `provider-${provider.id}`;
  const statusBadge = useMemo(() => {
    if (!provider.configured) return null;
    if (provider.init_ok) {
      return <Badge variant="default">OK</Badge>;
    }
    return <Badge variant="destructive">Failed</Badge>;
  }, [provider.configured, provider.init_ok]);

  return (
    <div className="flex flex-col gap-1 px-3 py-2.5 [&:not(:last-child)]:border-b">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Label htmlFor={switchId} className="text-sm font-medium">
            {provider.display_name}
          </Label>
          {statusBadge}
        </div>
        <div className="flex items-center gap-1">
          <Switch
            id={switchId}
            checked={checked}
            onCheckedChange={onToggle}
            disabled={disabled}
          />
          {onEdit && (
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={onEdit}
              aria-label={`Edit ${provider.display_name}`}
            >
              <Pencil />
            </Button>
          )}
        </div>
      </div>
      {!provider.configured && (
        <p className="text-xs text-muted-foreground">
          Env vars not set — edit <code>.env</code> and restart to enable.
        </p>
      )}
      {provider.configured && !provider.init_ok && provider.error && (
        <p className="text-xs text-destructive">
          Init error: {provider.error}
        </p>
      )}
    </div>
  );
}

/**
 * Green or red banner summarizing the outcome of a "Test configuration"
 * round-trip. Shown above the form after the API redirects back from a
 * /start?mode=test flow. Dismissible; also auto-cleared from the URL
 * by extractTestResultFromLocation.
 */
function TestResultBanner({
  result,
  onDismiss,
}: {
  result: TestResult;
  onDismiss: () => void;
}) {
  const provider = result.provider || 'provider';
  if (result.ok) {
    return (
      <div className="flex items-start gap-2 rounded-md border border-primary/30 bg-primary/5 px-3 py-2 text-sm">
        <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-primary" />
        <div className="flex-1">
          <p className="font-medium">Test succeeded for {provider}.</p>
          <p className="text-xs text-muted-foreground mt-0.5">
            Verified identity from the provider:
            {result.email ? ` ${result.email}` : ''}
            {result.sub ? ` (sub ${result.sub})` : ''}
            {result.issuer ? ` via ${result.issuer}` : ''}.
          </p>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onDismiss}
          className="h-6 px-2 text-xs"
        >
          Dismiss
        </Button>
      </div>
    );
  }
  return (
    <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm">
      <div className="flex-1">
        <p className="font-medium text-destructive">
          Test failed for {provider}.
        </p>
        <p className="text-xs text-muted-foreground mt-0.5">
          {result.error ?? 'The provider rejected the sign-in attempt.'}
        </p>
      </div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onDismiss}
        className="h-6 px-2 text-xs"
      >
        Dismiss
      </Button>
    </div>
  );
}
